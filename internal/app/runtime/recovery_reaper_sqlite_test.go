package runtime_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// V4-13's own orphaned-RUNNING-attempt recovery tests. These live in
// package runtime_test (not internal/adapters/sqlite) so they can reuse
// sqliteExecutionFixture/claimExecuteNodeJob (finalize_execution_attempt_sqlite_test.go)
// directly — a real *sqlite.Store satisfies worker.InterruptionRecoveryStore/
// WorkspaceReconciler/RecoveryStore structurally (SPK-04/SPK-09's own
// interfaces), so RecoveryReaperHandler can be constructed with it directly
// alongside the real sqlite.NewUnitOfWork(store) for its ports.UnitOfWork
// half — proving the real classification/reconciliation wiring this task's
// own Verify line requires ("kill tại sáu fault boundary"), not just the
// pure-fake stranded-intent sweeps recovery_reaper_test.go already covers.

// TestRecoveryReaperHandler_OrphanedReadOnlyAttempt_RetriesWithNewAttemptAndJob
// is the task's own required proof: "nhánh retry tạo đúng một Attempt/job
// RUN_WORK". A RUNNING Attempt whose own driving EXECUTE_NODE job lease has
// genuinely expired (real wall-clock sleep past a short TTL — matching
// finalize_execution_attempt_sqlite_test.go's own established technique)
// and which never acquired a WriteLease is classified LOST
// (TerminationReasonLeaseLost) and, since retry budget remains, gets a
// fresh ExecutionAttempt (AttemptNumber=2) with its own new EXECUTE_NODE
// job — never AgentExecutor.Start (RecoveryReaperHandler's own type
// signature never even references ports.AgentExecutor, proof by
// construction that this handler can never call it).
func TestRecoveryReaperHandler_OrphanedReadOnlyAttempt_RetriesWithNewAttemptAndJob(t *testing.T) {
	ctx := context.Background()
	uow, store, ids, runID, nodeRunID, attemptID := sqliteExecutionFixture(t)

	_, lease := claimExecuteNodeJob(t, ctx, store, 50*time.Millisecond)
	_ = lease
	time.Sleep(150 * time.Millisecond)

	handler := runtime.NewRecoveryReaperHandler(uow, ids, clock.System{}, store, store, store)
	if err := runtime.StartupRecoveryScan(ctx, uow, ids); err != nil {
		t.Fatalf("StartupRecoveryScan: %v", err)
	}
	job := firstSQLiteJobOfKind(t, ctx, store, runtime.RecoveryReaperJobKind)
	if err := handler.Handle(ctx, job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	original, err := uowGetExecutionAttempt(ctx, uow, attemptID)
	if err != nil {
		t.Fatalf("get original attempt: %v", err)
	}
	if original.State != runtimedomain.ExecutionAttemptLost || original.TerminationReason != runtimedomain.TerminationReasonLeaseLost {
		t.Fatalf("original attempt = %+v, want LOST/LEASE_LOST", original)
	}

	nodeRun, err := uowGetNodeRun(ctx, uow, nodeRunID)
	if err != nil {
		t.Fatalf("get node run: %v", err)
	}
	if nodeRun.State != runtimedomain.NodeRunRunning {
		t.Fatalf("node run state after retry = %s, want still RUNNING (a fresh attempt is in flight)", nodeRun.State)
	}

	// Find the retry attempt via ListExecutionAttemptsForRun (V4-12B) rather
	// than touching the job queue — claiming/discarding a job of the wrong
	// kind while probing would leave it stuck LEASED (never re-claimable),
	// corrupting this test's own later "no duplicate work" check.
	var attempts []runtimedomain.ExecutionAttempt
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		attempts, err = tx.Runtime().ListExecutionAttemptsForRun(ctx, runID)
		return err
	}); err != nil {
		t.Fatalf("ListExecutionAttemptsForRun: %v", err)
	}
	var retryAttempt *runtimedomain.ExecutionAttempt
	for i := range attempts {
		if attempts[i].AttemptNumber == 2 {
			retryAttempt = &attempts[i]
		}
	}
	if retryAttempt == nil {
		t.Fatalf("no AttemptNumber=2 retry attempt found among %+v", attempts)
	}
	if retryAttempt.State != runtimedomain.ExecutionAttemptQueued {
		t.Fatalf("retry attempt state = %s, want QUEUED", retryAttempt.State)
	}

	var hasJob bool
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		hasJob, err = tx.Jobs().HasActiveJobForAggregateIDs(ctx, []string{string(retryAttempt.ID)})
		return err
	}); err != nil {
		t.Fatalf("HasActiveJobForAggregateIDs: %v", err)
	}
	if !hasJob {
		t.Fatal("no EXECUTE_NODE job found for the new retry attempt")
	}

	// No duplicate work on a second sweep: the retry attempt is QUEUED (not
	// RUNNING), so it is never itself picked up as orphaned.
	job2 := firstSQLiteJobOfKind(t, ctx, store, runtime.RecoveryReaperJobKind)
	if err := handler.Handle(ctx, job2); err != nil {
		t.Fatalf("second Handle: %v", err)
	}
	var attemptsAfter []runtimedomain.ExecutionAttempt
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		attemptsAfter, err = tx.Runtime().ListExecutionAttemptsForRun(ctx, runID)
		return err
	}); err != nil {
		t.Fatalf("ListExecutionAttemptsForRun (after second sweep): %v", err)
	}
	if len(attemptsAfter) != len(attempts) {
		t.Fatalf("attempt count after second sweep = %d, want unchanged %d", len(attemptsAfter), len(attempts))
	}
}

// TestRecoveryReaperHandler_OrphanedAttempt_BudgetExhausted_Escalates proves
// the ESCALATE branch: a RUNNING Attempt at AttemptNumber=3 (this fixture's
// own pinned AttemptRules.MaxAttempts=3, attemptPolicyDocument) whose job
// lease has expired is classified LOST but has no retry budget left —
// recorded as a RecoveryDecisionKind DecisionArtifact with NextAction=ESCALATE,
// never a new Attempt/job.
func TestRecoveryReaperHandler_OrphanedAttempt_BudgetExhausted_Escalates(t *testing.T) {
	ctx := context.Background()
	uow, store, ids, runID, nodeRunID, attemptID := sqliteExecutionFixture(t)

	original, err := uowGetExecutionAttempt(ctx, uow, attemptID)
	if err != nil {
		t.Fatalf("get original attempt: %v", err)
	}

	// Fail the original attempt out of the way (a real historical fact —
	// not itself under test here), then seed a fresh AttemptNumber=3 RUNNING
	// attempt directly, mirroring sqliteExecutionFixture's own "force
	// RUNNING directly" technique for the identical reason (no real
	// executor to drive this fixture through a real retry cycle twice).
	thirdAttemptID := ids.NewID()
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if _, err := tx.Runtime().TransitionExecutionAttempt(ctx, ports.TransitionExecutionAttemptRequest{
			AttemptID: attemptID, ExpectedState: runtimedomain.ExecutionAttemptRunning, ExpectedVersion: original.Version,
			NextState: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonExecutionFailed,
			FailureCode: "EXECUTION_FAILED",
		}); err != nil {
			return err
		}
		nextAttempt, err := runtimedomain.NewExecutionAttempt(
			runtimedomain.ExecutionAttemptID(thirdAttemptID), original.NodeRunID, 3,
			original.ExecutionProfileHash, original.ProviderKey, original.InputRevisionSet,
		)
		if err != nil {
			return err
		}
		if _, err := tx.Runtime().CreateExecutionAttempt(ctx, nextAttempt); err != nil {
			return err
		}
		_, err = tx.Runtime().TransitionExecutionAttempt(ctx, ports.TransitionExecutionAttemptRequest{
			AttemptID: thirdAttemptID, ExpectedState: runtimedomain.ExecutionAttemptQueued, ExpectedVersion: 1,
			NextState: runtimedomain.ExecutionAttemptRunning,
		})
		return err
	}); err != nil {
		t.Fatalf("seed AttemptNumber=3 running attempt: %v", err)
	}

	thirdJobID := ids.NewID()
	thirdPayload, _ := json.Marshal(runtime.ExecuteNodeJobPayload{RunID: runID, NodeRunID: nodeRunID, AttemptID: thirdAttemptID})
	if _, err := store.EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: ports.JobID(thirdJobID), ProjectID: "project-1", Kind: runtime.ExecuteNodeJobKind, RunID: runID,
		AggregateType: "ExecutionAttempt", AggregateID: thirdAttemptID, Payload: thirdPayload,
		MaxClaims: 3, IdempotencyKey: "execute-" + thirdAttemptID,
	}); err != nil {
		t.Fatalf("enqueue third EXECUTE_NODE job: %v", err)
	}
	// Claim every EXECUTE_NODE job currently sitting AVAILABLE (the
	// original attempt's own long-dormant one from sqliteExecutionFixture,
	// plus the third attempt's own job just enqueued above) with a short
	// TTL before ever calling StartupRecoveryScan/firstSQLiteJobOfKind:
	// firstSQLiteJobOfKind's own claim-until-kind-matches search would
	// otherwise incidentally claim (and thus re-lease, non-expired) an
	// AVAILABLE EXECUTE_NODE job it merely passes over while hunting for
	// the RECOVERY_REAPER kind — silently making that job (and the attempt
	// it drives) look NOT orphaned by the time Handle's own sweep runs.
	// Claiming the original's job here is incidental/harmless: that
	// attempt is already FAILED, excluded from orphan detection by state
	// alone regardless of its own job's lease.
	claimExecuteNodeJob(t, ctx, store, 50*time.Millisecond)
	claimExecuteNodeJob(t, ctx, store, 50*time.Millisecond)
	time.Sleep(150 * time.Millisecond)

	handler := runtime.NewRecoveryReaperHandler(uow, ids, clock.System{}, store, store, store)
	if err := runtime.StartupRecoveryScan(ctx, uow, ids); err != nil {
		t.Fatalf("StartupRecoveryScan: %v", err)
	}
	job := firstSQLiteJobOfKind(t, ctx, store, runtime.RecoveryReaperJobKind)
	if err := handler.Handle(ctx, job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	third, err := uowGetExecutionAttempt(ctx, uow, thirdAttemptID)
	if err != nil {
		t.Fatalf("get third attempt: %v", err)
	}
	if third.State != runtimedomain.ExecutionAttemptLost {
		t.Fatalf("third attempt state = %s, want LOST", third.State)
	}

	var artifact runtimedomain.DecisionArtifact
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		artifact, err = tx.Runtime().GetDecisionArtifact(ctx, thirdAttemptID+"-recovery-decision-gen-0")
		return err
	}); err != nil {
		t.Fatalf("GetDecisionArtifact: %v", err)
	}
	if artifact.Kind != runtime.RecoveryDecisionKind {
		t.Fatalf("decision artifact kind = %s, want %s", artifact.Kind, runtime.RecoveryDecisionKind)
	}
	var result struct {
		NextAction string `json:"nextAction"`
	}
	if err := json.Unmarshal(artifact.Result, &result); err != nil {
		t.Fatalf("decode decision artifact result: %v", err)
	}
	if result.NextAction != string(runtime.RecoveryActionEscalate) {
		t.Fatalf("decision next action = %s, want ESCALATE", result.NextAction)
	}

	// No new Attempt was created for the escalated attempt: exactly the
	// original (failed) + third (lost) attempts exist for this NodeRun, and
	// no fourth attempt/job was ever produced.
	var attemptsAfter []runtimedomain.ExecutionAttempt
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		attemptsAfter, err = tx.Runtime().ListExecutionAttemptsForRun(ctx, runID)
		return err
	}); err != nil {
		t.Fatalf("ListExecutionAttemptsForRun: %v", err)
	}
	if len(attemptsAfter) != 2 {
		t.Fatalf("attempt count after ESCALATE = %d, want 2 (original + third, no new retry attempt): %+v", len(attemptsAfter), attemptsAfter)
	}
}

func firstSQLiteJobOfKind(t *testing.T, ctx context.Context, store interface {
	ClaimJob(context.Context, string, time.Duration) (ports.DurableJob, ports.JobLease, error)
}, kind string) ports.DurableJob {
	t.Helper()
	for i := 0; i < 10; i++ {
		job, _, err := store.ClaimJob(ctx, "test-worker", time.Minute)
		if err != nil {
			t.Fatalf("ClaimJob: %v", err)
		}
		if job.Kind == kind {
			return job
		}
	}
	t.Fatalf("did not find a %s job to claim within 10 attempts", kind)
	return ports.DurableJob{}
}

func uowGetExecutionAttempt(ctx context.Context, uow ports.UnitOfWork, attemptID string) (runtimedomain.ExecutionAttempt, error) {
	var attempt runtimedomain.ExecutionAttempt
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		attempt, err = tx.Runtime().GetExecutionAttempt(ctx, attemptID)
		return err
	})
	return attempt, err
}

func uowGetNodeRun(ctx context.Context, uow ports.UnitOfWork, nodeRunID string) (runtimedomain.NodeRun, error) {
	var nodeRun runtimedomain.NodeRun
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		nodeRun, err = tx.Runtime().GetNodeRun(ctx, nodeRunID)
		return err
	})
	return nodeRun, err
}
