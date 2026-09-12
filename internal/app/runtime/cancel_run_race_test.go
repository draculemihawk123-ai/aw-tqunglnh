package runtime_test

import (
	"context"
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// V4-12B's own four mandatory race tests (docs/design/06-v4-runtime-engine.md
// Verify line): cancel-vs-attempt-success, cancel-vs-END, cancel-vs-
// CompletionPolicy PASS, and stale finalize after cancel intent — each run
// in both commit orders. Plus cancel-vs-BLOCK/cancel-vs-REWORK proving
// those two do NOT turn cancel into a no-op (see cancel_run_test.go's own
// TestFinalizeExecutionAttempt_BlockedThenCancel_NotAutoClosedByCancelling
// and TestCancelRun_ForcedEscalationRework_StillCancelled for the other
// half of each).

func markAttemptRunning(t *testing.T, uow *fake.UnitOfWork, runID, nodeRunID, attemptID string) {
	t.Helper()
	if err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		attempt, err := tx.Runtime().GetExecutionAttempt(context.Background(), attemptID)
		if err != nil {
			return err
		}
		if _, err := tx.Runtime().TransitionExecutionAttempt(context.Background(), ports.TransitionExecutionAttemptRequest{
			AttemptID: attemptID, ExpectedState: runtimedomain.ExecutionAttemptQueued, ExpectedVersion: attempt.Version,
			NextState: runtimedomain.ExecutionAttemptRunning,
		}); err != nil {
			return err
		}
		nodeRun, err := tx.Runtime().GetNodeRun(context.Background(), nodeRunID)
		if err != nil {
			return err
		}
		_, err = tx.Runtime().TransitionNodeRun(context.Background(), ports.TransitionNodeRunRequest{
			NodeRunID: nodeRunID, ExpectedState: runtimedomain.NodeRunQueued, ExpectedVersion: nodeRun.Version,
			NextState: runtimedomain.NodeRunRunning,
		})
		return err
	}); err != nil {
		t.Fatalf("mark attempt RUNNING: %v", err)
	}
}

// --- cancel-vs-attempt-success ---

// TestRace_CancelVsAttemptSuccess_CancelFirst: cancel intent commits while
// the Attempt is genuinely RUNNING (already dispatched, Alpha cannot force
// -stop it); its own later SUCCEEDED outcome is accepted as a real
// historical fact, but routes to no new downstream work, and the Run ends
// CANCELLED, never SUCCEEDED via that route.
func TestRace_CancelVsAttemptSuccess_CancelFirst(t *testing.T) {
	uow, ids, runID, nodeRunID, attemptID := scheduledExecutionFixture(t, 600)
	markAttemptRunning(t, uow, runID, nodeRunID, attemptID)
	cancelActor(t, uow, ids, runID)

	jobLease := ports.JobLease{JobID: ports.JobID(claimExecuteNodeJobID(t, uow, attemptID)), Owner: "worker-1", Token: 1}
	uow.Snapshot.Jobs().(*fake.JobsRepository).SetActiveLease(string(jobLease.JobID), jobLease)

	result, err := runtime.FinalizeExecutionAttempt(context.Background(), uow, ids, clock.System{}, nil, runtime.FinalizeExecutionAttemptRequest{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID, ExpectedVersion: 2,
		NextState: runtimedomain.ExecutionAttemptSucceeded, TerminationReason: runtimedomain.TerminationReasonCompleted,
		SelectedOutcome: "done", JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("FinalizeExecutionAttempt (late success): %v", err)
	}
	if result.AdvanceResult.NextNodeRunID != "" {
		t.Fatalf("AdvanceResult = %+v, want no new downstream NodeRun after cancel intent", result.AdvanceResult)
	}
	attempt, err := uow.Snapshot.Runtime().GetExecutionAttempt(context.Background(), attemptID)
	if err != nil {
		t.Fatalf("GetExecutionAttempt: %v", err)
	}
	if attempt.State != runtimedomain.ExecutionAttemptSucceeded {
		t.Fatalf("attempt state = %s, want SUCCEEDED (accepted as historical fact)", attempt.State)
	}
	if final := runState(t, uow, runID); final.State != runtimedomain.WorkflowRunCancelled {
		t.Fatalf("run state = %s, want CANCELLED, never SUCCEEDED via this late outcome", final.State)
	}
}

// TestRace_CancelVsAttemptSuccess_SuccessFirst: the Attempt's own SUCCEEDED
// outcome (and its own routing) commits BEFORE any cancel intent exists —
// ordinary successful completion, unaffected by cancellation. CancelRun
// called afterward against whatever state the Run is now in (VERIFYING, if
// this hop reached END) is still accepted normally — VERIFYING is not yet
// genuinely terminal.
func TestRace_CancelVsAttemptSuccess_SuccessFirst(t *testing.T) {
	uow, ids, runID, nodeRunID, attemptID := scheduledExecutionFixture(t, 600)
	markAttemptRunning(t, uow, runID, nodeRunID, attemptID)

	jobLease := ports.JobLease{JobID: ports.JobID(claimExecuteNodeJobID(t, uow, attemptID)), Owner: "worker-1", Token: 1}
	uow.Snapshot.Jobs().(*fake.JobsRepository).SetActiveLease(string(jobLease.JobID), jobLease)

	_, err := runtime.FinalizeExecutionAttempt(context.Background(), uow, ids, clock.System{}, nil, runtime.FinalizeExecutionAttemptRequest{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID, ExpectedVersion: 2,
		NextState: runtimedomain.ExecutionAttemptSucceeded, TerminationReason: runtimedomain.TerminationReasonCompleted,
		SelectedOutcome: "done", JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("FinalizeExecutionAttempt (success before cancel): %v", err)
	}

	result := cancelActor(t, uow, ids, runID)
	if result.AlreadyRequested {
		t.Fatalf("CancelRun result = %+v, want a fresh request accepted normally", result)
	}
	final := runState(t, uow, runID)
	if final.State != runtimedomain.WorkflowRunCancelling && final.State != runtimedomain.WorkflowRunCancelled {
		t.Fatalf("run state = %s, want CANCELLING or CANCELLED", final.State)
	}
}

// --- cancel-vs-END ---

// TestRace_CancelVsEnd_CancelFirst: cancel commits while the last live
// NodeRun is still RUNNING; that Attempt's own SUCCEEDED outcome reaches
// the literal END edge, but advanceRunTx's own cancelling guard skips
// creating the END NodeRun entirely — the Run must never observe VERIFYING
// on its way to CANCELLED.
func TestRace_CancelVsEnd_CancelFirst(t *testing.T) {
	uow, ids, runID, nodeRunID, attemptID := scheduledExecutionFixture(t, 600)
	markAttemptRunning(t, uow, runID, nodeRunID, attemptID)
	cancelActor(t, uow, ids, runID)

	jobLease := ports.JobLease{JobID: ports.JobID(claimExecuteNodeJobID(t, uow, attemptID)), Owner: "worker-1", Token: 1}
	uow.Snapshot.Jobs().(*fake.JobsRepository).SetActiveLease(string(jobLease.JobID), jobLease)

	_, err := runtime.FinalizeExecutionAttempt(context.Background(), uow, ids, clock.System{}, nil, runtime.FinalizeExecutionAttemptRequest{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID, ExpectedVersion: 2,
		NextState: runtimedomain.ExecutionAttemptSucceeded, TerminationReason: runtimedomain.TerminationReasonCompleted,
		SelectedOutcome: "done", JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("FinalizeExecutionAttempt: %v", err)
	}
	final := runState(t, uow, runID)
	if final.State == runtimedomain.WorkflowRunVerifying {
		t.Fatalf("run state = VERIFYING, want the Run to never observe VERIFYING once cancel intent has committed")
	}
	if final.State != runtimedomain.WorkflowRunCancelled {
		t.Fatalf("run state = %s, want CANCELLED", final.State)
	}
}

// TestRace_CancelVsEnd_EndFirst: END is reached normally first (Run ->
// VERIFYING, a completion CANDIDATE, not yet genuinely terminal per
// ADR-011), THEN CancelRun is called — still accepted, moving the Run on
// to CANCELLING/CANCELLED, proving a completion candidate never blocks
// cancellation the way a genuinely PASS-decided SUCCEEDED would.
func TestRace_CancelVsEnd_EndFirst(t *testing.T) {
	uow, ids, runID, nodeRunID, attemptID := scheduledExecutionFixture(t, 600)
	markAttemptRunning(t, uow, runID, nodeRunID, attemptID)

	jobLease := ports.JobLease{JobID: ports.JobID(claimExecuteNodeJobID(t, uow, attemptID)), Owner: "worker-1", Token: 1}
	uow.Snapshot.Jobs().(*fake.JobsRepository).SetActiveLease(string(jobLease.JobID), jobLease)

	_, err := runtime.FinalizeExecutionAttempt(context.Background(), uow, ids, clock.System{}, nil, runtime.FinalizeExecutionAttemptRequest{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID, ExpectedVersion: 2,
		NextState: runtimedomain.ExecutionAttemptSucceeded, TerminationReason: runtimedomain.TerminationReasonCompleted,
		SelectedOutcome: "done", JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("FinalizeExecutionAttempt: %v", err)
	}
	if verifying := runState(t, uow, runID); verifying.State != runtimedomain.WorkflowRunVerifying {
		t.Fatalf("run state before cancel = %s, want VERIFYING (this fixture's own document reaches END directly)", verifying.State)
	}

	result := cancelActor(t, uow, ids, runID)
	if result.AlreadyRequested {
		t.Fatalf("CancelRun result = %+v, want accepted (VERIFYING is not yet genuinely terminal)", result)
	}
	driveCancelRunCoordinator(t, uow, ids, runID)
	final := runState(t, uow, runID)
	if final.State != runtimedomain.WorkflowRunCancelled {
		t.Fatalf("run state = %s, want CANCELLED (cancel still wins over an undecided completion candidate)", final.State)
	}
}

// --- cancel-vs-CompletionPolicy PASS (simulated: no real V5-11 service
// exists yet, so the exact CAS a future PASS decision would use —
// TransitionWorkflowRunState(VERIFYING->SUCCEEDED) — is driven directly) ---

// TestRace_CancelVsCompletionPolicyPass_CancelFirst: cancel intent commits
// while the Run is VERIFYING; the simulated PASS decision's own CAS
// (ExpectedState=VERIFYING) naturally fails once the Run has already
// moved to CANCELLING — exactly "mọi authoritative outcome đến muộn bị CAS
// từ chối", with no special-case code needed beyond ordinary optimistic
// concurrency.
func TestRace_CancelVsCompletionPolicyPass_CancelFirst(t *testing.T) {
	uow, ids, runID, nodeRunID, attemptID := scheduledExecutionFixture(t, 600)
	markAttemptRunning(t, uow, runID, nodeRunID, attemptID)
	jobLease := ports.JobLease{JobID: ports.JobID(claimExecuteNodeJobID(t, uow, attemptID)), Owner: "worker-1", Token: 1}
	uow.Snapshot.Jobs().(*fake.JobsRepository).SetActiveLease(string(jobLease.JobID), jobLease)
	if _, err := runtime.FinalizeExecutionAttempt(context.Background(), uow, ids, clock.System{}, nil, runtime.FinalizeExecutionAttemptRequest{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID, ExpectedVersion: 2,
		NextState: runtimedomain.ExecutionAttemptSucceeded, TerminationReason: runtimedomain.TerminationReasonCompleted,
		SelectedOutcome: "done", JobLease: jobLease,
	}); err != nil {
		t.Fatalf("FinalizeExecutionAttempt: %v", err)
	}
	verifying := runState(t, uow, runID)
	if verifying.State != runtimedomain.WorkflowRunVerifying {
		t.Fatalf("run state = %s, want VERIFYING before cancel", verifying.State)
	}

	cancelActor(t, uow, ids, runID)
	driveCancelRunCoordinator(t, uow, ids, runID)

	err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Runtime().TransitionWorkflowRunState(context.Background(), ports.TransitionWorkflowRunStateRequest{
			RunID: runID, ExpectedState: runtimedomain.WorkflowRunVerifying, ExpectedVersion: verifying.Version,
			NextState: runtimedomain.WorkflowRunSucceeded,
		})
		return err
	})
	if !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("simulated PASS transition after cancel err = %v, want ErrOptimisticConflict", err)
	}
	final := runState(t, uow, runID)
	if final.State != runtimedomain.WorkflowRunCancelled {
		t.Fatalf("run state = %s, want CANCELLED (the late PASS decision must never win)", final.State)
	}
}

// TestRace_CancelVsCompletionPolicyPass_PassFirst: the simulated PASS
// decision commits first (Run -> SUCCEEDED, genuinely terminal), THEN
// CancelRun is rejected outright — ErrRunAlreadyTerminal, never a no-op
// masquerading as success.
func TestRace_CancelVsCompletionPolicyPass_PassFirst(t *testing.T) {
	uow, ids, runID, nodeRunID, attemptID := scheduledExecutionFixture(t, 600)
	markAttemptRunning(t, uow, runID, nodeRunID, attemptID)
	jobLease := ports.JobLease{JobID: ports.JobID(claimExecuteNodeJobID(t, uow, attemptID)), Owner: "worker-1", Token: 1}
	uow.Snapshot.Jobs().(*fake.JobsRepository).SetActiveLease(string(jobLease.JobID), jobLease)
	if _, err := runtime.FinalizeExecutionAttempt(context.Background(), uow, ids, clock.System{}, nil, runtime.FinalizeExecutionAttemptRequest{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID, ExpectedVersion: 2,
		NextState: runtimedomain.ExecutionAttemptSucceeded, TerminationReason: runtimedomain.TerminationReasonCompleted,
		SelectedOutcome: "done", JobLease: jobLease,
	}); err != nil {
		t.Fatalf("FinalizeExecutionAttempt: %v", err)
	}
	verifying := runState(t, uow, runID)

	if err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Runtime().TransitionWorkflowRunState(context.Background(), ports.TransitionWorkflowRunStateRequest{
			RunID: runID, ExpectedState: runtimedomain.WorkflowRunVerifying, ExpectedVersion: verifying.Version,
			NextState: runtimedomain.WorkflowRunSucceeded,
		})
		return err
	}); err != nil {
		t.Fatalf("simulated PASS transition: %v", err)
	}

	_, err := runtime.CancelRun(context.Background(), uow, ids, runtime.CancelRunRequest{RunID: runID, Actor: "operator-1", Reason: "too late"})
	if !errors.Is(err, runtime.ErrRunAlreadyTerminal) {
		t.Fatalf("CancelRun after PASS err = %v, want ErrRunAlreadyTerminal", err)
	}
}

// --- stale finalize after cancel intent ---

// TestRace_StaleFinalizeAfterCancelIntent_Failed proves the identical
// "accepted as historical fact, routes nowhere" rule for a FAILED (not
// just SUCCEEDED) late outcome: cancel intent commits, then the RUNNING
// Attempt's own natural conclusion is a genuine FAILURE — decideRetryOrExhaustion's
// own cancelling guard must not spawn a retry Attempt (the Run is
// cancelling, no new work), and the NodeRun's own FAILED transition still
// stands as a real fact.
func TestRace_StaleFinalizeAfterCancelIntent_Failed(t *testing.T) {
	uow, ids, runID, nodeRunID, attemptID := scheduledExecutionFixture(t, 600)
	markAttemptRunning(t, uow, runID, nodeRunID, attemptID)
	cancelActor(t, uow, ids, runID)

	jobLease := ports.JobLease{JobID: ports.JobID(claimExecuteNodeJobID(t, uow, attemptID)), Owner: "worker-1", Token: 1}
	uow.Snapshot.Jobs().(*fake.JobsRepository).SetActiveLease(string(jobLease.JobID), jobLease)

	jobsBefore := len(uow.Snapshot.Jobs().(*fake.JobsRepository).Items())
	_, err := runtime.FinalizeExecutionAttempt(context.Background(), uow, ids, clock.System{}, nil, runtime.FinalizeExecutionAttemptRequest{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID, ExpectedVersion: 2,
		NextState: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonExecutionFailed,
		FailureCode: errorcode.CodeExecutionFailed, JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("FinalizeExecutionAttempt (late failure after cancel intent): %v", err)
	}
	jobsAfter := len(uow.Snapshot.Jobs().(*fake.JobsRepository).Items())
	if jobsAfter != jobsBefore {
		t.Fatalf("job count = %d after late failure, want unchanged %d (no retry Attempt/job spawned once cancelling)", jobsAfter, jobsBefore)
	}
	nodeRun := nodeRunState(t, uow, nodeRunID)
	if nodeRun.State != runtimedomain.NodeRunFailed {
		t.Fatalf("node run state = %s, want FAILED (accepted as a real historical fact)", nodeRun.State)
	}
	final := runState(t, uow, runID)
	if final.State != runtimedomain.WorkflowRunCancelled {
		t.Fatalf("run state = %s, want CANCELLED", final.State)
	}
}

// TestRace_StaleFinalizeAfterCancelIntent_ReplayAfterAlreadyCancelled
// proves a redelivered/duplicate finalize attempt arriving AFTER the Run
// has already fully reached CANCELLED is still handled safely: the
// Attempt's own fenced JobLease check is what actually protects this
// codebase (a stale/duplicate lease is rejected before ever touching the
// Attempt's own row), never a special "Run is cancelled" branch — proving
// that layer's own fencing composes correctly with cancellation.
func TestRace_StaleFinalizeAfterCancelIntent_ReplayAfterAlreadyCancelled(t *testing.T) {
	uow, ids, runID, nodeRunID, attemptID := scheduledExecutionFixture(t, 600)
	markAttemptRunning(t, uow, runID, nodeRunID, attemptID)
	realJobID := claimExecuteNodeJobID(t, uow, attemptID)
	jobLease := ports.JobLease{JobID: ports.JobID(realJobID), Owner: "worker-1", Token: 1}
	uow.Snapshot.Jobs().(*fake.JobsRepository).SetActiveLease(realJobID, jobLease)

	cancelActor(t, uow, ids, runID)
	driveCancelRunCoordinator(t, uow, ids, runID)
	// The coordinator's own sweep does not touch a RUNNING Attempt (Alpha
	// cannot force-stop it) — the Run itself stays CANCELLING until this
	// Attempt's own outcome eventually arrives.
	if state := runState(t, uow, runID).State; state != runtimedomain.WorkflowRunCancelling {
		t.Fatalf("run state after sweep = %s, want still CANCELLING (RUNNING Attempt left alone)", state)
	}

	// Consume the lease once, legitimately.
	if _, err := runtime.FinalizeExecutionAttempt(context.Background(), uow, ids, clock.System{}, nil, runtime.FinalizeExecutionAttemptRequest{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID, ExpectedVersion: 2,
		NextState: runtimedomain.ExecutionAttemptSucceeded, TerminationReason: runtimedomain.TerminationReasonCompleted,
		SelectedOutcome: "done", JobLease: jobLease,
	}); err != nil {
		t.Fatalf("FinalizeExecutionAttempt: %v", err)
	}
	if final := runState(t, uow, runID); final.State != runtimedomain.WorkflowRunCancelled {
		t.Fatalf("run state = %s, want CANCELLED", final.State)
	}

	// A redelivered/duplicate finalize replaying the SAME (now-consumed)
	// lease must be rejected by the job-lease fence itself.
	_, err := runtime.FinalizeExecutionAttempt(context.Background(), uow, ids, clock.System{}, nil, runtime.FinalizeExecutionAttemptRequest{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID, ExpectedVersion: 2,
		NextState: runtimedomain.ExecutionAttemptSucceeded, TerminationReason: runtimedomain.TerminationReasonCompleted,
		SelectedOutcome: "done", JobLease: jobLease,
	})
	if !errors.Is(err, ports.ErrJobLeaseLost) {
		t.Fatalf("replayed FinalizeExecutionAttempt err = %v, want ErrJobLeaseLost", err)
	}
}

func claimExecuteNodeJobID(t *testing.T, uow *fake.UnitOfWork, attemptID string) string {
	t.Helper()
	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	for i := range jobs {
		if jobs[i].Kind == runtime.ExecuteNodeJobKind && jobs[i].AggregateID == attemptID {
			return string(jobs[i].ID)
		}
	}
	t.Fatalf("no %s job found for attempt %s", runtime.ExecuteNodeJobKind, attemptID)
	return ""
}
