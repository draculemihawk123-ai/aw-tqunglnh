package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/worker"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// Aliases onto crashworker_fixtures.go's exported constants: this file was
// written against these unexported names before the shared, non-test worker
// logic was extracted (docs/design/02-v0-spike-verdict.md V0-10B).
const (
	crashAttemptTermRunID                 = CrashAttemptTermRunID
	crashAttemptTermNodeRunID             = CrashAttemptTermNodeRunID
	crashAttemptTermAttemptID             = CrashAttemptTermAttemptID
	crashAttemptTermRepositoryID          = CrashAttemptTermRepositoryID
	crashAttemptTermWorkspaceSetID        = CrashAttemptTermWorkspaceSetID
	crashAttemptTermRepositoryWorkspaceID = CrashAttemptTermRepositoryWorkspaceID
	crashAttemptTermBaseRevision          = CrashAttemptTermBaseRevision
)

// TestSPK04FaultAfterProcessExitReadOnlyAttemptBecomesLost and
// TestSPK04FaultAfterProcessExitMutatingAttemptBecomesIndeterminate close
// fault point 5/6 of SPK-04: the worker is hard-killed after a real external
// process has already exited but before any outcome is durably committed.
// Neither test infers success from that exit code; recovery classifies the
// interrupted attempt from durable evidence (whether it ever held a
// WriteLease) instead.
func TestSPK04FaultAfterProcessExitReadOnlyAttemptBecomesLost(t *testing.T) {
	if os.Getenv(crashWorkerModeEnvironment) == "process-exit-readonly-then-hang" {
		runFaultAfterProcessExitWorkerProcess(t, false)
		return
	}
	runFaultAfterProcessExitScenario(t, false)
}

func TestSPK04FaultAfterProcessExitMutatingAttemptBecomesIndeterminate(t *testing.T) {
	if os.Getenv(crashWorkerModeEnvironment) == "process-exit-mutating-then-hang" {
		runFaultAfterProcessExitWorkerProcess(t, true)
		return
	}
	runFaultAfterProcessExitScenario(t, true)
}

func runFaultAfterProcessExitScenario(t *testing.T, mutating bool) {
	t.Helper()
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "agentkit-crash-attempt-term.db")
	store, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("open initial store: %v", err)
	}
	seedCrashResumeOwners(t, ctx, store)

	definition := crashResumeWorkflowDefinition()
	versionOne := compileCrashResumeWorkflowVersion(t, definition, "workflow-version-crash-term-v1", 1, crashResumeWorkflowDocumentV1(), "skill-v1")
	if _, err := store.PublishWorkflowVersion(ctx, definition, versionOne); err != nil {
		t.Fatalf("publish workflow v1: %v", err)
	}
	run, err := runtime.NewWorkflowRun(
		crashAttemptTermRunID, "project-crash", "work-item-crash", versionOne, "family-crash", 1,
		json.RawMessage(`{"checkpoint":"created"}`),
	)
	if err != nil {
		t.Fatalf("create workflow run: %v", err)
	}
	if err := store.StartWorkflowRun(ctx, run); err != nil {
		t.Fatalf("start workflow run: %v", err)
	}
	if _, err := store.CompareAndSwapWorkflowRun(ctx, ports.WorkflowRunTransition{
		RunID:           run.ID,
		ExpectedState:   runtime.WorkflowRunCreated,
		ExpectedVersion: 1,
		NextState:       runtime.WorkflowRunRunning,
		SharedState:     json.RawMessage(`{"checkpoint":"worker-dispatched"}`),
		OccurredAt:      time.Date(2026, 8, 28, 13, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("move workflow run to RUNNING: %v", err)
	}

	jobID := ports.JobID("job-crash-term-readonly")
	mode := "process-exit-readonly-then-hang"
	if mutating {
		jobID = "job-crash-term-mutating"
		mode = "process-exit-mutating-then-hang"
	}
	if _, err := store.EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID:             jobID,
		ProjectID:      "project-crash",
		Kind:           "EXECUTE_NODE",
		AggregateType:  "WorkflowRun",
		AggregateID:    string(run.ID),
		Payload:        json.RawMessage(`{"runId":"` + crashAttemptTermRunID + `"}`),
		MaxClaims:      3,
		IdempotencyKey: "execute-" + string(jobID),
	}); err != nil {
		t.Fatalf("enqueue durable job: %v", err)
	}
	seedCrashAttemptTermNodeRunAndAttempt(t, ctx, store, run.ID)
	if mutating {
		seedCrashAttemptTermWorkspace(t, ctx, store)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close initial store: %v", err)
	}

	const crashedWorkerTTL = 900 * time.Millisecond
	ready := startAndHardKillCrashWorker(t, databasePath, crashedWorkerTTL, versionOne.ID(), versionOne.ContentHash(), mode)
	if ready.JobID != jobID {
		t.Fatalf("crashed worker claimed job %s, want %s", ready.JobID, jobID)
	}

	restarted, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("open store after hard crash: %v", err)
	}
	defer func() {
		if err := restarted.Close(); err != nil {
			t.Errorf("close restarted store: %v", err)
		}
	}()
	waitForExpiredJobRecovery(t, ctx, restarted, ready.LeaseUntil)
	if _, _, err := restarted.ClaimJob(ctx, "worker-after-restart", 5*time.Second); err != nil {
		t.Fatalf("replacement worker claim: %v", err)
	}

	// The exit code observed by the killed worker (0, a clean exit) must
	// never leak into this classification: it is derived purely from durable
	// WriteLease evidence.
	nextState, reason, err := worker.ClassifyInterruptedAttempt(ctx, restarted, crashAttemptTermAttemptID)
	if err != nil {
		t.Fatalf("ClassifyInterruptedAttempt() error = %v", err)
	}
	if reason != runtime.TerminationReasonProcessExitBeforeOutcomeCommit {
		t.Fatalf("classify reason = %q, want %q", reason, runtime.TerminationReasonProcessExitBeforeOutcomeCommit)
	}
	wantState := runtime.ExecutionAttemptLost
	if mutating {
		wantState = runtime.ExecutionAttemptIndeterminate
	}
	if nextState != wantState {
		t.Fatalf("classify state = %s, want %s", nextState, wantState)
	}

	terminationUpdate := ports.AttemptTerminationUpdate{
		AttemptID:       crashAttemptTermAttemptID,
		ExpectedVersion: 1,
		NextState:       nextState,
		Reason:          reason,
		EventID:         "event-attempt-term-1",
		CorrelationID:   "crash-attempt-term",
		OccurredAt:      time.Date(2026, 8, 28, 13, 5, 0, 0, time.UTC),
	}
	if err := restarted.TerminateInterruptedAttempt(ctx, terminationUpdate); err != nil {
		t.Fatalf("TerminateInterruptedAttempt() error = %v", err)
	}
	// A second termination attempt at the same expected version must never
	// duplicate the terminal transition or its outbox event.
	if err := restarted.TerminateInterruptedAttempt(ctx, terminationUpdate); !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("duplicate TerminateInterruptedAttempt() error = %v, want ErrOptimisticConflict", err)
	}
	var eventCount int
	if err := restarted.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM domain_events WHERE aggregate_type = 'ExecutionAttempt' AND aggregate_id = ?`,
		crashAttemptTermAttemptID,
	).Scan(&eventCount); err != nil {
		t.Fatalf("count attempt termination events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("attempt termination events = %d, want exactly 1", eventCount)
	}

	if !mutating {
		return
	}
	var currentRevision string
	if err := restarted.db.QueryRowContext(ctx,
		`SELECT current_revision FROM repository_workspaces WHERE id = ?`, crashAttemptTermRepositoryWorkspaceID,
	).Scan(&currentRevision); err != nil {
		t.Fatalf("read current workspace revision: %v", err)
	}
	verdict, err := worker.ReconcileMutatingAttempt(crashAttemptTermBaseRevision, currentRevision)
	if err != nil {
		t.Fatalf("ReconcileMutatingAttempt() error = %v", err)
	}
	if verdict != worker.ReconciliationClean {
		t.Fatalf("reconciliation verdict = %s, want %s", verdict, worker.ReconciliationClean)
	}
}

// runFaultAfterProcessExitWorkerProcess, seedCrashAttemptTermNodeRunAndAttempt
// and seedCrashAttemptTermWorkspace delegate to the shared RunCrashWorker
// (internal/adapters/sqlite/crashworker.go) and crashworker_fixtures.go's
// exported seed helpers.
func runFaultAfterProcessExitWorkerProcess(t *testing.T, mutating bool) {
	t.Helper()
	mode := CrashModeProcessExitReadonly
	if mutating {
		mode = CrashModeProcessExitMutating
	}
	if err := RunCrashWorker(mode, testBinarySpawner); err != nil {
		t.Fatal(err)
	}
}

func seedCrashAttemptTermNodeRunAndAttempt(t *testing.T, ctx context.Context, store *Store, runID runtime.WorkflowRunID) {
	t.Helper()
	if err := SeedCrashAttemptTermNodeRunAndAttempt(ctx, store, runID); err != nil {
		t.Fatal(err)
	}
}

func seedCrashAttemptTermWorkspace(t *testing.T, ctx context.Context, store *Store) {
	t.Helper()
	if err := SeedCrashAttemptTermWorkspace(ctx, store); err != nil {
		t.Fatal(err)
	}
}
