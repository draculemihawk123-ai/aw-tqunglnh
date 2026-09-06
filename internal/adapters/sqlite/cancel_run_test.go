package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// V4-12B's own sqlite-level proof (docs/design/06-v4-runtime-engine.md,
// ADR-020): the CHECK constraint that enforces Kind<->JobClass at the
// database level (never just trusting ports.ClassifyJobKind's own Go-side
// mapping), the claim CAS actually split by class, the enqueue-time
// "run not cancelling" fence, and FenceAndCancelRunJobs's own atomic
// fence-and-flip.

func seedCancelRunTestRun(t *testing.T, ctx context.Context, store *Store, runID, state string) {
	t.Helper()
	seedWorkflowRunOwners(t, ctx, store)
	definition := testWorkflowDefinition()
	version := compileWorkflowVersion(t, definition, "wf-cancel-run", 1, workflowDocumentV1(), "1")
	if _, err := store.PublishWorkflowVersion(ctx, definition, version); err != nil {
		t.Fatalf("publish version: %v", err)
	}
	now := "2026-08-28T00:00:00Z"
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO workflow_runs (
    id, project_id, work_item_id, workflow_version_id, family_id,
    scope_version, state, shared_state_json, version, created_at, updated_at
) VALUES (?, 'project-1', 'work-item-1', ?, 'family-1', 1, ?, '{}', 1, ?, ?)`,
		runID, version.ID(), state, now, now); err != nil {
		t.Fatalf("seed run %s: %v", runID, err)
	}
}

// TestDurableJobs_JobClassMapping_ConstraintRejectsMismatchedControl proves
// the migration 0023 CHECK constraint is the actual authority for
// Kind<->JobClass, not merely ports.ClassifyJobKind's own Go-side mapping:
// a hand-rolled INSERT claiming job_class='CONTROL' for a Kind outside the
// closed allow-list is rejected by SQLite itself.
func TestDurableJobs_JobClassMapping_ConstraintRejectsMismatchedControl(t *testing.T) {
	store := openSchedulingTestStore(t)
	ctx := context.Background()
	seedSchedulingFixture(t, store)

	now := "2026-08-28T00:00:00Z"
	_, err := store.db.ExecContext(ctx, `
INSERT INTO durable_jobs (
    id, project_id, kind, aggregate_type, aggregate_id, payload_json, state,
    available_at, priority, claim_count, max_claims, lease_token,
    idempotency_key, version, created_at, updated_at, job_class
) VALUES ('bad-job', 'project-1', 'EXECUTE_NODE', 'ExecutionAttempt', 'attempt-1', '{}', 'AVAILABLE',
    ?, 0, 0, 3, 0, 'bad-job-key', 1, ?, ?, 'CONTROL')`, now, now, now)
	if err == nil {
		t.Fatal("expected CHECK constraint to reject job_class=CONTROL for a Kind outside the allow-list")
	}

	// The inverse direction is rejected too: a real CONTROL kind claiming
	// RUN_WORK.
	_, err = store.db.ExecContext(ctx, `
INSERT INTO durable_jobs (
    id, project_id, kind, aggregate_type, aggregate_id, payload_json, state,
    available_at, priority, claim_count, max_claims, lease_token,
    idempotency_key, version, created_at, updated_at, job_class
) VALUES ('bad-job-2', 'project-1', 'CANCEL_RUN_COORDINATOR', 'WorkflowRun', 'run-1', '{}', 'AVAILABLE',
    ?, 0, 0, 3, 0, 'bad-job-2-key', 1, ?, ?, 'RUN_WORK')`, now, now, now)
	if err == nil {
		t.Fatal("expected CHECK constraint to reject job_class=RUN_WORK for CANCEL_RUN_COORDINATOR")
	}

	// The legitimate pairing succeeds.
	_, err = store.db.ExecContext(ctx, `
INSERT INTO durable_jobs (
    id, project_id, kind, aggregate_type, aggregate_id, payload_json, state,
    available_at, priority, claim_count, max_claims, lease_token,
    idempotency_key, version, created_at, updated_at, job_class
) VALUES ('good-job', 'project-1', 'CANCEL_RUN_COORDINATOR', 'WorkflowRun', 'run-1', '{}', 'AVAILABLE',
    ?, 0, 0, 3, 0, 'good-job-key', 1, ?, ?, 'CONTROL')`, now, now, now)
	if err != nil {
		t.Fatalf("legitimate CANCEL_RUN_COORDINATOR/CONTROL pairing should be accepted: %v", err)
	}
}

// TestEnqueueJob_ComputesJobClassFromKind proves EnqueueJob itself (not
// just a hand-rolled INSERT) persists the correct job_class for both a
// CONTROL and a RUN_WORK kind, and that EnqueueJobRequest has no field a
// caller could use to override it.
func TestEnqueueJob_ComputesJobClassFromKind(t *testing.T) {
	store := openSchedulingTestStore(t)
	ctx := context.Background()
	seedSchedulingFixture(t, store)

	controlJob, err := store.EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: "control-job", ProjectID: "project-1", Kind: "CANCEL_RUN_COORDINATOR",
		AggregateType: "WorkflowRun", AggregateID: "run-1", MaxClaims: 3, IdempotencyKey: "control-job-key",
	})
	if err != nil {
		t.Fatalf("EnqueueJob(CONTROL): %v", err)
	}
	if controlJob.JobClass != ports.JobClassControl {
		t.Fatalf("controlJob.JobClass = %q, want %q", controlJob.JobClass, ports.JobClassControl)
	}

	runWorkJob, err := store.EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: "run-work-job", ProjectID: "project-1", Kind: "EXECUTE_NODE",
		AggregateType: "ExecutionAttempt", AggregateID: "attempt-1", MaxClaims: 3, IdempotencyKey: "run-work-job-key",
	})
	if err != nil {
		t.Fatalf("EnqueueJob(RUN_WORK): %v", err)
	}
	if runWorkJob.JobClass != ports.JobClassRunWork {
		t.Fatalf("runWorkJob.JobClass = %q, want %q", runWorkJob.JobClass, ports.JobClassRunWork)
	}
}

// TestEnqueueJob_RunWorkForCancellingRun_Rejected proves "Enqueue RUN_WORK
// mới CAS rằng Run còn non-cancelling": a RUN_WORK job enqueue naming a
// RunID whose own WorkflowRun is already CANCELLING (or CANCELLED) fails
// with ports.ErrRunCancelling — the defense-in-depth backstop, never
// itself the primary enforcement (every real caller in
// internal/app/runtime already checks first).
func TestEnqueueJob_RunWorkForCancellingRun_Rejected(t *testing.T) {
	for _, state := range []string{"CANCELLING", "CANCELLED"} {
		t.Run(state, func(t *testing.T) {
			store := openSchedulingTestStore(t)
			ctx := context.Background()
			seedCancelRunTestRun(t, ctx, store, "run-1", state)

			_, err := store.EnqueueJob(ctx, ports.EnqueueJobRequest{
				ID: ports.JobID("job-" + state), ProjectID: "project-1", Kind: "EXECUTE_NODE",
				AggregateType: "ExecutionAttempt", AggregateID: "attempt-1", RunID: "run-1",
				MaxClaims: 3, IdempotencyKey: "job-key-" + state,
			})
			if err == nil {
				t.Fatalf("EnqueueJob(RUN_WORK, RunID=run-1 state=%s) succeeded, want ErrRunCancelling", state)
			}
			if !isRunCancelling(err) {
				t.Fatalf("EnqueueJob error = %v, want ports.ErrRunCancelling", err)
			}
		})
	}
}

// TestEnqueueJob_RunWorkForNonCancellingRun_Succeeds is the positive
// control: RUNNING/WAITING/CREATED/VERIFYING/BLOCKED all still admit a new
// RUN_WORK job.
func TestEnqueueJob_RunWorkForNonCancellingRun_Succeeds(t *testing.T) {
	for _, state := range []string{"CREATED", "RUNNING", "WAITING", "BLOCKED", "VERIFYING"} {
		t.Run(state, func(t *testing.T) {
			store := openSchedulingTestStore(t)
			ctx := context.Background()
			seedCancelRunTestRun(t, ctx, store, "run-1", state)

			_, err := store.EnqueueJob(ctx, ports.EnqueueJobRequest{
				ID: ports.JobID("job-" + state), ProjectID: "project-1", Kind: "EXECUTE_NODE",
				AggregateType: "ExecutionAttempt", AggregateID: "attempt-1", RunID: "run-1",
				MaxClaims: 3, IdempotencyKey: "job-key-" + state,
			})
			if err != nil {
				t.Fatalf("EnqueueJob(RUN_WORK, RunID=run-1 state=%s): %v, want success", state, err)
			}
		})
	}
}

func isRunCancelling(err error) bool {
	return errors.Is(err, ports.ErrRunCancelling)
}

// TestClaimJob_ControlJobClaimableWhileRunWorkOfSameRunIsFenced is the
// Verify line's own "cancellation job CONTROL vẫn claim được trong khi
// workload job của cùng Run thì không": after FenceAndCancelRunJobs fences
// a Run's own RUN_WORK jobs, a CONTROL job for that SAME run still claims
// fine, while every fenced RUN_WORK job never claims again.
func TestClaimJob_ControlJobClaimableWhileRunWorkOfSameRunIsFenced(t *testing.T) {
	store := openSchedulingTestStore(t)
	ctx := context.Background()
	seedCancelRunTestRun(t, ctx, store, "run-1", "RUNNING")

	if _, err := store.EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: "run-work-job", ProjectID: "project-1", Kind: "EXECUTE_NODE",
		AggregateType: "ExecutionAttempt", AggregateID: "attempt-1", RunID: "run-1",
		MaxClaims: 3, IdempotencyKey: "run-work-job-key",
	}); err != nil {
		t.Fatalf("enqueue RUN_WORK job: %v", err)
	}
	if _, err := store.EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: "control-job", ProjectID: "project-1", Kind: "CANCEL_RUN_COORDINATOR",
		AggregateType: "WorkflowRun", AggregateID: "run-1", RunID: "run-1",
		MaxClaims: 3, IdempotencyKey: "control-job-key",
	}); err != nil {
		t.Fatalf("enqueue CONTROL job: %v", err)
	}

	tx, err := beginTestJobsTx(ctx, store)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE workflow_runs SET state = 'CANCELLING' WHERE id = 'run-1'`); err != nil {
		t.Fatalf("transition run to CANCELLING: %v", err)
	}
	repo := jobsRepository{tx: tx}
	affected, err := repo.FenceAndCancelRunJobs(ctx, "run-1")
	if err != nil {
		t.Fatalf("FenceAndCancelRunJobs: %v", err)
	}
	if affected != 1 {
		t.Fatalf("FenceAndCancelRunJobs affected = %d, want 1 (only the RUN_WORK job)", affected)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// The RUN_WORK job was AVAILABLE and gets CASed straight to CANCELLED
	// by the fence itself — never claimable again.
	job, _, err := store.ClaimJob(ctx, "worker-1", 30*time.Second)
	if err != nil {
		t.Fatalf("ClaimJob (expect only the CONTROL job to be claimable): %v", err)
	}
	if job.Kind != "CANCEL_RUN_COORDINATOR" {
		t.Fatalf("claimed job.Kind = %s, want CANCEL_RUN_COORDINATOR (the RUN_WORK job must never be claimable)", job.Kind)
	}

	// Nothing else is claimable now.
	if _, _, err := store.ClaimJob(ctx, "worker-2", 30*time.Second); err != ports.ErrNoJobAvailable {
		t.Fatalf("second ClaimJob error = %v, want ErrNoJobAvailable", err)
	}

	var runWorkState string
	var runWorkCancelEpoch *int64
	if err := store.db.QueryRowContext(ctx, `SELECT state, cancel_epoch FROM durable_jobs WHERE id = 'run-work-job'`).Scan(&runWorkState, &runWorkCancelEpoch); err != nil {
		t.Fatalf("read run-work-job row: %v", err)
	}
	if runWorkState != "CANCELLED" {
		t.Fatalf("run-work-job state = %s, want CANCELLED", runWorkState)
	}
	if runWorkCancelEpoch == nil || *runWorkCancelEpoch != 1 {
		t.Fatalf("run-work-job cancel_epoch = %v, want 1", runWorkCancelEpoch)
	}
}

// TestFenceAndCancelRunJobs_LeasedJobStaysLeasedButNeverReclaimableAfterRecovery
// proves the LEASED-at-fence-time half: a job a worker already claimed
// stays LEASED (never force-terminated — the worker holding it is
// expected to notice the fence at its own re-check checkpoints,
// execute.go), but once its lease naturally expires and
// RecoverExpiredJobs makes it AVAILABLE again, the fence set earlier means
// it is STILL never claimable again.
func TestFenceAndCancelRunJobs_LeasedJobStaysLeasedButNeverReclaimableAfterRecovery(t *testing.T) {
	store := openSchedulingTestStore(t)
	ctx := context.Background()
	seedCancelRunTestRun(t, ctx, store, "run-1", "RUNNING")

	if _, err := store.EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: "run-work-job", ProjectID: "project-1", Kind: "EXECUTE_NODE",
		AggregateType: "ExecutionAttempt", AggregateID: "attempt-1", RunID: "run-1",
		MaxClaims: 3, IdempotencyKey: "run-work-job-key",
	}); err != nil {
		t.Fatalf("enqueue RUN_WORK job: %v", err)
	}
	if _, _, err := store.ClaimJob(ctx, "worker-1", 30*time.Millisecond); err != nil {
		t.Fatalf("claim run-work-job: %v", err)
	}

	tx, err := beginTestJobsTx(ctx, store)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE workflow_runs SET state = 'CANCELLING' WHERE id = 'run-1'`); err != nil {
		t.Fatalf("transition run to CANCELLING: %v", err)
	}
	repo := jobsRepository{tx: tx}
	if _, err := repo.FenceAndCancelRunJobs(ctx, "run-1"); err != nil {
		t.Fatalf("FenceAndCancelRunJobs: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var stateAfterFence string
	if err := store.db.QueryRowContext(ctx, `SELECT state FROM durable_jobs WHERE id = 'run-work-job'`).Scan(&stateAfterFence); err != nil {
		t.Fatalf("read state after fence: %v", err)
	}
	if stateAfterFence != "LEASED" {
		t.Fatalf("run-work-job state right after fencing = %s, want unchanged LEASED (never force-terminated)", stateAfterFence)
	}

	waitForRecoveredJob(t, store)

	var stateAfterRecovery string
	if err := store.db.QueryRowContext(ctx, `SELECT state FROM durable_jobs WHERE id = 'run-work-job'`).Scan(&stateAfterRecovery); err != nil {
		t.Fatalf("read state after recovery: %v", err)
	}
	if stateAfterRecovery != "AVAILABLE" {
		t.Fatalf("run-work-job state after lease recovery = %s, want AVAILABLE", stateAfterRecovery)
	}

	if _, _, err := store.ClaimJob(ctx, "worker-2", 30*time.Second); err != ports.ErrNoJobAvailable {
		t.Fatalf("ClaimJob after recovery error = %v, want ErrNoJobAvailable (fenced job must never be reclaimable)", err)
	}
}

// TestCancelRunSchema_SurvivesRestart is this task's own "restart" proof:
// close/reopen the store mid-cancellation and verify workflow_runs.cancel_epoch
// and durable_jobs.run_id/job_class/cancel_epoch all read back correctly.
func TestCancelRunSchema_SurvivesRestart(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agentkit-cancel-run-restart.db")
	store, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	seedCancelRunTestRun(t, ctx, store, "run-1", "RUNNING")
	if _, err := store.EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: "run-work-job", ProjectID: "project-1", Kind: "EXECUTE_NODE",
		AggregateType: "ExecutionAttempt", AggregateID: "attempt-1", RunID: "run-1",
		MaxClaims: 3, IdempotencyKey: "run-work-job-key",
	}); err != nil {
		t.Fatalf("enqueue RUN_WORK job: %v", err)
	}

	tx, err := beginTestJobsTx(ctx, store)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	epoch := uint64(1)
	repo := runtimeRepository{tx: tx}
	if _, err := repo.TransitionWorkflowRunState(ctx, ports.TransitionWorkflowRunStateRequest{
		RunID: "run-1", ExpectedState: runtime.WorkflowRunRunning, ExpectedVersion: 1,
		NextState: runtime.WorkflowRunCancelling, NextCancelEpoch: &epoch,
	}); err != nil {
		t.Fatalf("transition run to CANCELLING: %v", err)
	}
	jobsRepo := jobsRepository{tx: tx}
	if _, err := jobsRepo.FenceAndCancelRunJobs(ctx, "run-1"); err != nil {
		t.Fatalf("FenceAndCancelRunJobs: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close before restart: %v", err)
	}

	reopened, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen after restart: %v", err)
	}
	defer reopened.Close()

	run, err := loadWorkflowRun(ctx, reopened.db, runtime.WorkflowRunID("run-1"))
	if err != nil {
		t.Fatalf("load run after restart: %v", err)
	}
	if run.State != runtime.WorkflowRunCancelling {
		t.Fatalf("run state after restart = %s, want CANCELLING", run.State)
	}
	if run.CancelEpoch == nil || *run.CancelEpoch != 1 {
		t.Fatalf("run CancelEpoch after restart = %v, want 1", run.CancelEpoch)
	}

	var jobState, jobClass string
	var jobRunID string
	var jobCancelEpoch *int64
	if err := reopened.db.QueryRowContext(ctx, `SELECT state, job_class, run_id, cancel_epoch FROM durable_jobs WHERE id = 'run-work-job'`).
		Scan(&jobState, &jobClass, &jobRunID, &jobCancelEpoch); err != nil {
		t.Fatalf("read job after restart: %v", err)
	}
	if jobState != "CANCELLED" || jobClass != "RUN_WORK" || jobRunID != "run-1" || jobCancelEpoch == nil || *jobCancelEpoch != 1 {
		t.Fatalf("job after restart = state=%s class=%s runId=%s cancelEpoch=%v, want CANCELLED/RUN_WORK/run-1/1",
			jobState, jobClass, jobRunID, jobCancelEpoch)
	}
}

func beginTestJobsTx(ctx context.Context, store *Store) (*sql.Tx, error) {
	return store.db.BeginTx(ctx, nil)
}
