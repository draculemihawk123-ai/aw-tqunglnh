package sqlite

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/worker"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// Aliases onto crashworker.go/crashworker_fixtures.go's exported
// constants: this file was written against these unexported names before
// the shared, non-test worker logic was extracted
// (docs/design/02-v0-spike-verdict.md V0-10B).
const (
	crashCheckpointProviderModeEnvironment = CrashCheckpointProviderModeEnvironment
	crashCheckpointProviderReadyPrefix     = CrashCheckpointProviderReadyPrefix
	crashCheckpointRunID                   = CrashCheckpointRunID
	crashCheckpointNodeRunID               = CrashCheckpointNodeRunID
	crashCheckpointInterruptedAttempt      = CrashCheckpointInterruptedAttempt
	crashCheckpointID                      = CrashCheckpointID
	crashCheckpointContextSnapshotID       = CrashCheckpointContextSnapshotID
	crashCheckpointReplacementAttempt      = CrashCheckpointReplacementAttempt
)

// TestSPK03HardCrashJoinsCheckpointContextRecovery joins the three pieces
// that were previously proven separately: a genuinely killed worker OS
// process (see crash_resume_integration_test.go), checkpoint/context
// recovery (see checkpoint_store_test.go), and interrupted-attempt recovery
// (worker.ReconcileInterruptedAttempt, docs/design/02-v0-spike-verdict.md
// V0-10C). The worker child spawns a real fake provider grandchild process,
// persists a Checkpoint+ContextSnapshot only after it hears back from that
// process, then is hard-killed with no graceful shutdown. A replacement
// worker recovers purely from SQLite: it terminates the interrupted attempt
// out of RUNNING (this fixture never acquires a WriteLease, so it always
// classifies read-only -> LOST) and starts a distinct execution attempt from
// the checkpoint's canonical ContextSnapshot, always calling
// AgentExecutor.Start (never Resume).
func TestSPK03HardCrashJoinsCheckpointContextRecovery(t *testing.T) {
	if os.Getenv(crashCheckpointProviderModeEnvironment) == "1" {
		runCrashCheckpointFakeProviderProcess(t)
		return
	}
	if os.Getenv(crashWorkerModeEnvironment) == "checkpoint-then-hang" {
		runCrashCheckpointWorkerProcess(t)
		return
	}

	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "agentkit-crash-checkpoint.db")
	store, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("open initial store: %v", err)
	}
	seedCrashResumeOwners(t, ctx, store)

	definition := crashResumeWorkflowDefinition()
	versionOne := compileCrashResumeWorkflowVersion(t, definition, "workflow-version-crash-ckpt-v1", 1, crashResumeWorkflowDocumentV1(), "skill-v1")
	if _, err := store.PublishWorkflowVersion(ctx, definition, versionOne); err != nil {
		t.Fatalf("publish workflow v1: %v", err)
	}
	run, err := runtime.NewWorkflowRun(
		crashCheckpointRunID, "project-crash", "work-item-crash", versionOne, "family-crash", 1,
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
		OccurredAt:      time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("move workflow run to RUNNING: %v", err)
	}
	if _, err := store.EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID:             "job-crash-ckpt",
		ProjectID:      "project-crash",
		Kind:           "EXECUTE_NODE",
		AggregateType:  "WorkflowRun",
		AggregateID:    string(run.ID),
		Payload:        json.RawMessage(`{"runId":"` + crashCheckpointRunID + `"}`),
		MaxClaims:      3,
		IdempotencyKey: "execute-workflow-run-crash-ckpt",
	}); err != nil {
		t.Fatalf("enqueue durable job: %v", err)
	}
	seedCrashCheckpointNodeRunAndAttempt(t, ctx, store, run.ID)

	if err := store.Close(); err != nil {
		t.Fatalf("close initial store: %v", err)
	}

	const crashedWorkerTTL = 900 * time.Millisecond
	ready := startAndHardKillCrashWorker(t, databasePath, crashedWorkerTTL, versionOne.ID(), versionOne.ContentHash(), "checkpoint-then-hang")
	if ready.JobID != "job-crash-ckpt" {
		t.Fatalf("crashed worker claimed job %s, want job-crash-ckpt", ready.JobID)
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

	jobAfterRestart, replacementLease, err := restarted.ClaimJob(ctx, "worker-after-restart", 5*time.Second)
	if err != nil {
		t.Fatalf("replacement worker claim: %v", err)
	}
	if jobAfterRestart.ID != ready.JobID || jobAfterRestart.ClaimCount != 2 {
		t.Fatalf("replacement claim = job %s count %d, want %s count 2",
			jobAfterRestart.ID, jobAfterRestart.ClaimCount, ready.JobID)
	}

	// The checkpoint and its ContextSnapshot must have survived the hard
	// kill: they were written by a process whose SQLite connection was never
	// closed and which was terminated with no graceful shutdown.
	persistedCheckpoint, err := restarted.LoadLatestCheckpoint(ctx, crashCheckpointInterruptedAttempt)
	if err != nil {
		t.Fatalf("load persisted checkpoint after crash: %v", err)
	}
	if persistedCheckpoint.ID != crashCheckpointID || persistedCheckpoint.Sequence != 1 ||
		persistedCheckpoint.ContextSnapshotID != crashCheckpointContextSnapshotID {
		t.Fatalf("unexpected persisted checkpoint after crash: %+v", persistedCheckpoint)
	}
	if _, err := restarted.LoadContextSnapshot(ctx, persistedCheckpoint.ContextSnapshotID); err != nil {
		t.Fatalf("load persisted context snapshot after crash: %v", err)
	}

	// Replacement execution: always a new attempt id, always Start, never
	// Resume, sourced only from the durable ContextSnapshot/Checkpoint chain
	// — nothing carried over from the killed process's memory.
	recorder := &freshRecoveryExecutor{}
	result, err := worker.StartFreshFromLatestCheckpoint(
		ctx, restarted, recorder, crashCheckpointInterruptedAttempt,
		ports.AgentExecutionRequest{AttemptID: crashCheckpointReplacementAttempt, WorkingDirectory: t.TempDir(), Timeout: time.Second},
		ports.AgentEventSinkFunc(func(context.Context, ports.AgentEvent) error { return nil }),
	)
	if err != nil {
		t.Fatalf("StartFreshFromLatestCheckpoint() error = %v", err)
	}
	if result.AttemptID != crashCheckpointReplacementAttempt || recorder.startCalls != 1 || recorder.resumeCalls != 0 {
		t.Fatalf("replacement execution = result:%+v start:%d resume:%d", result, recorder.startCalls, recorder.resumeCalls)
	}
	if crashCheckpointReplacementAttempt == persistedCheckpoint.AttemptID {
		t.Fatalf("replacement attempt id %s must differ from the interrupted attempt id", crashCheckpointReplacementAttempt)
	}

	// The interrupted attempt must never be left stranded at RUNNING: this
	// fixture's crashed worker never acquires a WriteLease, so recovery must
	// classify it read-only and terminate it LOST — never inferring anything
	// from the killed process's own (unobserved) exit code.
	recovery, err := worker.ReconcileInterruptedAttempt(ctx, restarted, restarted, worker.InterruptedAttemptRecoveryRequest{
		AttemptID: crashCheckpointInterruptedAttempt, ExpectedAttemptVersion: 1,
		TerminationEventID: "event-crash-ckpt-terminate", CorrelationID: "crash-resume-checkpoint",
		OccurredAt: time.Date(2026, 8, 28, 12, 0, 2, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("ReconcileInterruptedAttempt() error = %v", err)
	}
	if recovery.NextState != runtime.ExecutionAttemptLost || recovery.Reason != runtime.TerminationReasonProcessExitBeforeOutcomeCommit {
		t.Fatalf("interrupted attempt recovery = %+v, want LOST/PROCESS_EXIT_BEFORE_OUTCOME_COMMIT", recovery)
	}
	attemptState, attemptVersion, err := restarted.LoadExecutionAttemptState(ctx, crashCheckpointInterruptedAttempt)
	if err != nil {
		t.Fatalf("load interrupted attempt state after recovery: %v", err)
	}
	if attemptState != runtime.ExecutionAttemptLost || attemptVersion != 2 {
		t.Fatalf("interrupted attempt after recovery = %s@%d, want LOST@2", attemptState, attemptVersion)
	}

	assertExactlyOneTerminalWorkflowTransition(t, ctx, restarted, run.ID, replacementLease)

	var durableState string
	if err := restarted.db.QueryRowContext(ctx,
		`SELECT state FROM durable_jobs WHERE id = ?`, ready.JobID,
	).Scan(&durableState); err != nil {
		t.Fatalf("read final durable job state: %v", err)
	}
	if durableState != string(ports.JobSucceeded) {
		t.Fatalf("final durable job state = %s, want SUCCEEDED", durableState)
	}
}

// runCrashCheckpointWorkerProcess and runCrashCheckpointFakeProviderProcess
// delegate to the shared RunCrashWorker/RunCrashCheckpointFakeProvider
// (internal/adapters/sqlite/crashworker.go); seedCrashCheckpointNodeRunAndAttempt
// delegates to crashworker_fixtures.go's exported equivalent. Kept under
// their original names so nothing else in this file needs to change.
func runCrashCheckpointWorkerProcess(t *testing.T) {
	t.Helper()
	if err := RunCrashWorker(CrashModeCheckpointThenHang, testBinarySpawner); err != nil {
		t.Fatal(err)
	}
}

func runCrashCheckpointFakeProviderProcess(t *testing.T) {
	t.Helper()
	RunCrashCheckpointFakeProvider()
}

func seedCrashCheckpointNodeRunAndAttempt(t *testing.T, ctx context.Context, store *Store, runID runtime.WorkflowRunID) {
	t.Helper()
	if err := SeedCrashCheckpointNodeRunAndAttempt(ctx, store, runID); err != nil {
		t.Fatal(err)
	}
}
