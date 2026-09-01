package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/worker"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// Fixture identities shared between the top-level test and its crashed
// worker child, exactly as in crash_resume_checkpoint_integration_test.go.
const (
	crashAttemptTermRunID                 = "workflow-run-crash-term"
	crashAttemptTermNodeRunID             = "node-run-crash-term"
	crashAttemptTermAttemptID             = "attempt-crash-term"
	crashAttemptTermRepositoryID          = "repo-crash-term"
	crashAttemptTermWorkspaceSetID        = "workspace-set-crash-term"
	crashAttemptTermRepositoryWorkspaceID = "rw-crash-term"
	crashAttemptTermBaseRevision          = "rev-crash-term-base"
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

// runFaultAfterProcessExitWorkerProcess is the crashed-worker child. It runs
// a real external process to completion (the fault point under test: the
// external exit is genuine and already observed) and, for the mutating
// variant, acquires a real WriteLease before that — then is hard-killed with
// no chance to commit any outcome.
func runFaultAfterProcessExitWorkerProcess(t *testing.T, mutating bool) {
	t.Helper()
	databasePath := strings.TrimSpace(os.Getenv(crashWorkerDBEnvironment))
	if databasePath == "" {
		t.Fatal("crash attempt-termination worker database path is required")
	}
	ttl, err := time.ParseDuration(os.Getenv(crashWorkerTTLEnvironment))
	if err != nil || ttl <= 0 {
		t.Fatalf("parse crash attempt-termination worker TTL: %v", err)
	}

	ctx := context.Background()
	store, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("crash attempt-termination worker open store: %v", err)
	}
	// Intentionally no Close: the parent terminates this process while its
	// SQLite connection and durable lease are still live.

	run, err := store.LoadWorkflowRun(ctx, crashAttemptTermRunID)
	if err != nil {
		t.Fatalf("crash attempt-termination worker load workflow run: %v", err)
	}
	_, lease, err := store.ClaimJob(ctx, "worker-before-crash", ttl)
	if err != nil {
		t.Fatalf("crash attempt-termination worker claim job: %v", err)
	}

	if mutating {
		if _, err := store.AcquireWriteLeases(ctx, ports.AcquireWriteLeasesRequest{
			JobLease:  lease,
			AttemptID: crashAttemptTermAttemptID,
			Targets: []ports.WorkspaceLeaseTarget{{
				RepositoryID:          crashAttemptTermRepositoryID,
				RepositoryWorkspaceID: crashAttemptTermRepositoryWorkspaceID,
				Generation:            1,
			}},
			TTL: ttl,
		}); err != nil {
			t.Fatalf("crash attempt-termination worker acquire write lease: %v", err)
		}
	}

	// A real external process runs to completion and exits cleanly (code 0).
	// The worker is about to be killed with no chance to act on that exit;
	// nothing downstream may treat this clean exit as a committed success.
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve current test executable for external process: %v", err)
	}
	external := exec.Command(executable, "-test.run=^$")
	if err := external.Run(); err != nil {
		t.Fatalf("external process did not exit cleanly: %v", err)
	}

	fmt.Printf(
		"%s %s %d %s %s %s\n",
		crashWorkerReadyPrefix,
		lease.JobID,
		lease.Token,
		lease.LeaseUntil.UTC().Format(time.RFC3339Nano),
		run.WorkflowVersionID,
		run.WorkflowVersionHash,
	)
	_ = os.Stdout.Sync()
	select {}
}

func seedCrashAttemptTermNodeRunAndAttempt(t *testing.T, ctx context.Context, store *Store, runID runtime.WorkflowRunID) {
	t.Helper()
	const timestamp = "2026-08-28T13:00:00Z"
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO node_runs(
  id, run_id, node_key, activation_sequence, iteration, state,
  input_state_hash, version, created_at, updated_at
) VALUES (?, ?, 'implement', 1, 0, 'RUNNING', 'sha256:input-crash-term', 1, ?, ?);`,
		crashAttemptTermNodeRunID, runID, timestamp, timestamp,
	); err != nil {
		t.Fatalf("seed crash attempt-termination node run: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO execution_attempts(
  id, node_run_id, attempt_no, state, provider_key, execution_profile_hash,
  input_revision_set_json, version, created_at, updated_at
) VALUES (?, ?, 1, 'RUNNING', 'codex', 'sha256:profile-crash-term', '[]', 1, ?, ?);`,
		crashAttemptTermAttemptID, crashAttemptTermNodeRunID, timestamp, timestamp,
	); err != nil {
		t.Fatalf("seed crash attempt-termination execution attempt: %v", err)
	}
}

func seedCrashAttemptTermWorkspace(t *testing.T, ctx context.Context, store *Store) {
	t.Helper()
	const timestamp = "2026-08-28T13:00:00Z"
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO repositories(id, project_id, name, local_path, default_ref, status, version, created_at, updated_at)
VALUES (?, 'project-crash', 'attempt-term-repo', 'C:/fixture/attempt-term', 'main', 'ACTIVE', 1, ?, ?);`,
		crashAttemptTermRepositoryID, timestamp, timestamp,
	); err != nil {
		t.Fatalf("seed crash attempt-termination repository: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO workspace_sets(id, project_id, family_id, state, version, created_at, updated_at)
VALUES (?, 'project-crash', 'family-crash', 'READY', 1, ?, ?);`,
		crashAttemptTermWorkspaceSetID, timestamp, timestamp,
	); err != nil {
		t.Fatalf("seed crash attempt-termination workspace set: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO repository_workspaces(
  id, project_id, workspace_set_id, family_id, repository_id, generation,
  locator, branch_ref, base_revision, current_revision, state, version, created_at, updated_at
) VALUES (?, 'project-crash', ?, 'family-crash', ?, 1,
          'opaque:attempt-term', 'agentkit/family-crash/attempt-term', ?, ?, 'READY', 1, ?, ?);`,
		crashAttemptTermRepositoryWorkspaceID, crashAttemptTermWorkspaceSetID, crashAttemptTermRepositoryID,
		crashAttemptTermBaseRevision, crashAttemptTermBaseRevision, timestamp, timestamp,
	); err != nil {
		t.Fatalf("seed crash attempt-termination repository workspace: %v", err)
	}
}
