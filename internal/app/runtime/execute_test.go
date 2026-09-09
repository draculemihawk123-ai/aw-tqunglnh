package runtime_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// scheduledExecutionFixture publishes the same full AGENT resolution chain
// scheduleFixture's own callers use, then actually calls
// ScheduleExecutableNodeRun so an ExecutionAttempt (QUEUED) and its own
// EXECUTE_NODE job genuinely exist — everything an execute.go/finalize.go
// test needs to drive the envelope for real, rather than hand-constructing
// an Attempt no scheduling transaction ever produced.
func scheduledExecutionFixture(t *testing.T, timeoutSeconds uint32) (uow *fake.UnitOfWork, ids idsource.Source, runID, nodeRunID, attemptID string) {
	t.Helper()
	buildID := sharedTestAdapterBuild(t).ID()
	uow, ids, runID, nodeRunID = scheduleFixture(t, agentExecutableDocument("agent-profile-v1", fullyResolvablePolicyRefs(), &buildID))
	registerSharedTestAdapterBuild(t, uow)
	publishAgentProfileVersion(t, uow, "agent-profile-def", "agent-profile-v1", validAgentProfileDocument())
	publishPolicyVersion(t, uow, "attempt-policy-def", "attempt-policy-v1", attemptPolicyDocument(timeoutSeconds))
	publishPolicyVersion(t, uow, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())

	scheduled, err := runtime.ScheduleExecutableNodeRun(context.Background(), uow, ids, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: runID, NodeRunID: nodeRunID, CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("ScheduleExecutableNodeRun: %v", err)
	}
	return uow, ids, runID, nodeRunID, scheduled.AttemptID
}

// claimableExecuteNodeJob builds a ports.DurableJob for the real EXECUTE_NODE
// job scheduledExecutionFixture already enqueued, and marks it as actively
// leased on the fake JobsRepository — test setup standing in for a real
// workerpool.Pool.ClaimJob call, since this fake has no claim lifecycle of
// its own (see fake.JobsRepository.SetActiveLease's own doc comment).
func claimableExecuteNodeJob(t *testing.T, uow *fake.UnitOfWork, attemptID string) ports.DurableJob {
	t.Helper()
	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	var found *ports.EnqueueJobRequest
	for i := range jobs {
		if jobs[i].Kind == runtime.ExecuteNodeJobKind && jobs[i].AggregateID == attemptID {
			found = &jobs[i]
		}
	}
	if found == nil {
		t.Fatalf("no %s job found for attempt %s among %+v", runtime.ExecuteNodeJobKind, attemptID, jobs)
	}
	leaseUntil := time.Now().Add(time.Minute)
	lease := ports.JobLease{JobID: found.ID, Owner: "worker-1", Token: 1, LeaseUntil: leaseUntil}
	uow.Snapshot.Jobs().(*fake.JobsRepository).SetActiveLease(string(found.ID), lease)
	return ports.DurableJob{
		ID: found.ID, AggregateType: found.AggregateType, AggregateID: found.AggregateID,
		Payload: found.Payload, LeaseOwner: lease.Owner, LeaseToken: lease.Token, LeaseUntil: &leaseUntil,
	}
}

// --- ExecuteNodeHandler: happy path ---

func TestExecuteNodeHandler_Success_FinalizesAndAdvances(t *testing.T) {
	uow, ids, runID, nodeRunID, attemptID := scheduledExecutionFixture(t, 600)
	job := claimableExecuteNodeJob(t, uow, attemptID)

	executor := &fake.NodeExecutor{Result: ports.NodeExecutionResult{State: runtimedomain.ExecutionAttemptSucceeded, SelectedOutcome: "done"}}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{}, fake.IsolationEnforcementChecker{}, sharedTestAgentRegistry(t))
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	attempt, err := uow.Snapshot.Runtime().GetExecutionAttempt(context.Background(), attemptID)
	if err != nil {
		t.Fatalf("GetExecutionAttempt: %v", err)
	}
	if attempt.State != runtimedomain.ExecutionAttemptSucceeded || attempt.TerminationReason != runtimedomain.TerminationReasonCompleted {
		t.Fatalf("attempt = %+v, want SUCCEEDED/COMPLETED", attempt)
	}

	// The NodeRun (originally the "implement" node, RUNNING) must have been
	// routed forward to "end" via the SelectedOutcome the executor proposed.
	nodeRun, err := uow.Snapshot.Runtime().GetNodeRun(context.Background(), nodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun: %v", err)
	}
	if nodeRun.State != runtimedomain.NodeRunSucceeded || nodeRun.SelectedOutcome != "done" {
		t.Fatalf("node run = %+v, want SUCCEEDED with outcome done", nodeRun)
	}

	events := uow.Snapshot.Events().(*fake.EventsRepository).Items()
	found := false
	for _, e := range events {
		if e.EventType == runtime.ExecutionAttemptFinalizedEventType && e.AggregateID == attemptID {
			found = true
		}
	}
	if !found {
		t.Fatalf("no %s event found among %+v", runtime.ExecutionAttemptFinalizedEventType, events)
	}
	_ = runID
}

// --- ExecuteNodeHandler: failure path (never routes via AdvanceRun; V4-06's
// own retry policy decides retry vs NodeRun-FAILED instead) ---

// TestExecuteNodeHandler_Failure_NonRetryableCode_FailsNodeRun proves a
// FAILED Attempt never goes through AdvanceRun's own outcome routing — it is
// V4-06's own decideRetryOrExhaustion that decides what happens to the
// NodeRun. attemptPolicyDocument's own fixture declares no
// RetryableErrorCodes at all, so the executor's (fallback) CodeExecutionFailed
// is never eligible for retry regardless of remaining budget — the NodeRun
// is CAS'd straight to FAILED with a NODE_RUN_FAILED/NON_RETRYABLE_FAILURE
// event, and the Attempt's own already-set TerminationReason is left
// untouched (exhaustion/non-retryability is a NodeRun-level classification,
// never rewritten onto the Attempt that actually failed).
func TestExecuteNodeHandler_Failure_NonRetryableCode_FailsNodeRun(t *testing.T) {
	uow, ids, _, nodeRunID, attemptID := scheduledExecutionFixture(t, 600)
	job := claimableExecuteNodeJob(t, uow, attemptID)

	executor := &fake.NodeExecutor{Result: ports.NodeExecutionResult{State: runtimedomain.ExecutionAttemptFailed}}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{}, fake.IsolationEnforcementChecker{}, sharedTestAgentRegistry(t))
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	attempt, err := uow.Snapshot.Runtime().GetExecutionAttempt(context.Background(), attemptID)
	if err != nil {
		t.Fatalf("GetExecutionAttempt: %v", err)
	}
	if attempt.State != runtimedomain.ExecutionAttemptFailed || attempt.TerminationReason != runtimedomain.TerminationReasonExecutionFailed {
		t.Fatalf("attempt = %+v, want FAILED/EXECUTION_FAILED", attempt)
	}

	// The NodeRun is NOT routed via AdvanceRun/SelectedOutcome — it is CAS'd
	// straight to FAILED by decideRetryOrExhaustion instead.
	nodeRun, err := uow.Snapshot.Runtime().GetNodeRun(context.Background(), nodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun: %v", err)
	}
	if nodeRun.State != runtimedomain.NodeRunFailed {
		t.Fatalf("node run = %+v, want FAILED (non-retryable code fails the node run immediately)", nodeRun)
	}

	events := uow.Snapshot.Events().(*fake.EventsRepository).Items()
	found := false
	for _, e := range events {
		if e.EventType == runtime.NodeRunFailedEventType && e.AggregateID == nodeRunID {
			found = true
		}
	}
	if !found {
		t.Fatalf("no %s event found among %+v", runtime.NodeRunFailedEventType, events)
	}
}

// --- ExecuteNodeHandler: executor error (no ctx cancellation) also fails closed ---

func TestExecuteNodeHandler_ExecutorReturnsError_FinalizesFailed(t *testing.T) {
	uow, ids, _, _, attemptID := scheduledExecutionFixture(t, 600)
	job := claimableExecuteNodeJob(t, uow, attemptID)

	executor := &fake.NodeExecutor{Err: errors.New("boom: provider crashed")}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{}, fake.IsolationEnforcementChecker{}, sharedTestAgentRegistry(t))
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	attempt, err := uow.Snapshot.Runtime().GetExecutionAttempt(context.Background(), attemptID)
	if err != nil {
		t.Fatalf("GetExecutionAttempt: %v", err)
	}
	if attempt.State != runtimedomain.ExecutionAttemptFailed || attempt.TerminationReason != runtimedomain.TerminationReasonExecutionFailed {
		t.Fatalf("attempt = %+v, want FAILED/EXECUTION_FAILED", attempt)
	}
}

// --- ExecuteNodeHandler: AttemptPolicy deadline ---

// TestExecuteNodeHandler_AttemptDeadlineFinalizesTimedOut proves the
// envelope's OWN derived-deadline context (from the pinned
// ResolvedExecutionProfileV1.TimeoutSeconds), not any bare
// context.DeadlineExceeded, is what triggers TIMED_OUT/DEADLINE_EXCEEDED —
// the fake executor blocks until the derived context's own deadline fires.
// The fixture's own attemptPolicyDocument declares no RetryableErrorCodes,
// so CodeTimeout is non-retryable here too — decideRetryOrExhaustion CAS's
// the NodeRun straight to FAILED, mirroring
// TestExecuteNodeHandler_Failure_NonRetryableCode_FailsNodeRun.
func TestExecuteNodeHandler_AttemptDeadlineFinalizesTimedOut(t *testing.T) {
	uow, ids, _, nodeRunID, attemptID := scheduledExecutionFixture(t, 1)
	job := claimableExecuteNodeJob(t, uow, attemptID)

	executor := &fake.NodeExecutor{Block: make(chan struct{})} // never closed: the executor "runs forever"
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{}, fake.IsolationEnforcementChecker{}, sharedTestAgentRegistry(t))
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	attempt, err := uow.Snapshot.Runtime().GetExecutionAttempt(context.Background(), attemptID)
	if err != nil {
		t.Fatalf("GetExecutionAttempt: %v", err)
	}
	if attempt.State != runtimedomain.ExecutionAttemptTimedOut || attempt.TerminationReason != runtimedomain.TerminationReasonDeadlineExceeded {
		t.Fatalf("attempt = %+v, want TIMED_OUT/DEADLINE_EXCEEDED", attempt)
	}

	nodeRun, err := uow.Snapshot.Runtime().GetNodeRun(context.Background(), nodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun: %v", err)
	}
	if nodeRun.State != runtimedomain.NodeRunFailed {
		t.Fatalf("node run = %+v, want FAILED (non-retryable TIMEOUT fails the node run immediately)", nodeRun)
	}
}

// --- ExecuteNodeHandler: outer context cancellation does NOT finalize ---

// TestExecuteNodeHandler_CancelledContextDoesNotFinalize proves that when
// the OUTER context is cancelled for a reason other than the envelope's own
// AttemptPolicy deadline (pool shutdown, heartbeat loss, ...), the Attempt
// is left RUNNING — never finalized as CANCELLED, never mapped to
// DEADLINE_EXCEEDED either — for a later recovery authority (V4-12B/V4-13)
// to observe.
func TestExecuteNodeHandler_CancelledContextDoesNotFinalize(t *testing.T) {
	uow, ids, _, nodeRunID, attemptID := scheduledExecutionFixture(t, 600) // long attempt-policy timeout — irrelevant here
	job := claimableExecuteNodeJob(t, uow, attemptID)

	executor := &fake.NodeExecutor{Block: make(chan struct{})}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{}, fake.IsolationEnforcementChecker{}, sharedTestAgentRegistry(t))

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	err := handler.Handle(ctx, job)
	if err == nil {
		t.Fatal("Handle succeeded, want an error propagating the outer cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}

	attempt, getErr := uow.Snapshot.Runtime().GetExecutionAttempt(context.Background(), attemptID)
	if getErr != nil {
		t.Fatalf("GetExecutionAttempt: %v", getErr)
	}
	if attempt.State != runtimedomain.ExecutionAttemptRunning {
		t.Fatalf("attempt = %+v, want unchanged RUNNING (cancelled context must not finalize)", attempt)
	}

	nodeRun, getErr := uow.Snapshot.Runtime().GetNodeRun(context.Background(), nodeRunID)
	if getErr != nil {
		t.Fatalf("GetNodeRun: %v", getErr)
	}
	if nodeRun.State != runtimedomain.NodeRunRunning {
		t.Fatalf("node run = %+v, want unchanged RUNNING", nodeRun)
	}
}

// --- ExecuteNodeHandler: V5-08C's own poller detects a durable
// cancellation intent recorded WHILE the executor is running ---

// cancellationAwareExecutor is a tiny ports.NodeExecutor stand-in for
// AgentNodeExecutor's own classifyCancellation read-only-attempt branch
// (agent_node_executor_cancellation.go): it blocks until its ctx is
// cancelled, then reports a definitive CANCELLED result with no error —
// exactly what a real bridge does once it has re-derived that a genuine
// durable RunCancellationIntent, not some other ambiguous cause, is why
// its own process-level ctx just stopped it. fake.NodeExecutor cannot
// express this (it only ever returns ctx.Err() itself on ctx.Done()), so
// this handler-level test needs its own.
type cancellationAwareExecutor struct{ calls int }

func (e *cancellationAwareExecutor) Execute(ctx context.Context, _ ports.NodeExecutionRequest) (ports.NodeExecutionResult, error) {
	e.calls++
	<-ctx.Done()
	return ports.NodeExecutionResult{
		State: runtimedomain.ExecutionAttemptCancelled, TerminationReason: runtimedomain.TerminationReasonRunCancelled,
	}, nil
}

var _ ports.NodeExecutor = (*cancellationAwareExecutor)(nil)

// TestExecuteNodeHandler_PollerDetectsDurableCancellation_UnrecognizedExecutorLeavesRunning
// proves the poller mechanism itself: a plain fake.NodeExecutor (blocked,
// with no idea what V5-08C's own cancellation classification even is) is
// interrupted within roughly one cancellationPollInterval tick of a real
// runtime.CancelRun recording its durable intent — nothing about this test
// touches the outer job ctx directly, unlike
// TestExecuteNodeHandler_CancelledContextDoesNotFinalize. The Attempt is
// left RUNNING (the safe default for an executor that does not resolve the
// cancellation itself, matching every other ambiguous-cause branch in this
// file) — not silently finalized as FAILED (the bug this task fixed) and
// not left blocked until the (long) AttemptPolicy deadline.
func TestExecuteNodeHandler_PollerDetectsDurableCancellation_UnrecognizedExecutorLeavesRunning(t *testing.T) {
	uow, ids, runID, nodeRunID, attemptID := scheduledExecutionFixture(t, 600) // deliberately long: proves the poller, not the deadline, ends this
	job := claimableExecuteNodeJob(t, uow, attemptID)

	executor := &fake.NodeExecutor{Block: make(chan struct{})} // never closed: only poller-cancelled execCtx ends this
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{}, fake.IsolationEnforcementChecker{}, sharedTestAgentRegistry(t))

	handleErr := make(chan error, 1)
	start := time.Now()
	go func() { handleErr <- handler.Handle(context.Background(), job) }()

	// Give Handle time to actually start (claim RUNNING, pass the context
	// snapshot check, reach the executor call) before recording the
	// cancellation intent — otherwise CancelRun could race ahead of
	// admission's own pre-execute cancelling re-check.
	time.Sleep(50 * time.Millisecond)
	if _, err := runtime.CancelRun(context.Background(), uow, ids, runtime.CancelRunRequest{
		RunID: runID, Actor: "actor-1", Reason: "test: mid-execution cancel",
	}); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}

	select {
	case err := <-handleErr:
		elapsed := time.Since(start)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Handle err = %v, want context.Canceled", err)
		}
		// Well under both the 600s AttemptPolicy deadline and a generous
		// bound on "a couple of poll ticks" — proves the poller, not the
		// deadline, is what ended this.
		if elapsed > 3*time.Second {
			t.Fatalf("Handle took %s to return after CancelRun, want well under the poller's own multi-tick bound", elapsed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Handle did not return within 5s of a recorded cancellation intent — poller did not fire")
	}

	attempt, err := uow.Snapshot.Runtime().GetExecutionAttempt(context.Background(), attemptID)
	if err != nil {
		t.Fatalf("GetExecutionAttempt: %v", err)
	}
	if attempt.State != runtimedomain.ExecutionAttemptRunning {
		t.Fatalf("attempt = %+v, want still RUNNING (unrecognized-cause cancellation must not finalize)", attempt)
	}
	nodeRun, err := uow.Snapshot.Runtime().GetNodeRun(context.Background(), nodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun: %v", err)
	}
	if nodeRun.State != runtimedomain.NodeRunRunning {
		t.Fatalf("node run = %+v, want unchanged RUNNING", nodeRun)
	}
}

// TestExecuteNodeHandler_PollerDetectsDurableCancellation_DefinitiveResultFinalizesCancelled
// proves the OTHER half: when the executor itself resolves the
// poller-triggered cancellation into a definitive CANCELLED result (exactly
// what AgentNodeExecutor's own classifyCancellation read-only-attempt
// branch does — see cancellationAwareExecutor above), Handle's own switch
// (execute.go) now finalizes the Attempt CANCELLED/RUN_CANCELLED instead of
// silently defaulting to FAILED (the bug this task's own research found:
// the switch had no explicit case for ExecutionAttemptCancelled, so
// nextState stayed at its hardcoded Failed default).
func TestExecuteNodeHandler_PollerDetectsDurableCancellation_DefinitiveResultFinalizesCancelled(t *testing.T) {
	uow, ids, runID, nodeRunID, attemptID := scheduledExecutionFixture(t, 600)
	job := claimableExecuteNodeJob(t, uow, attemptID)

	executor := &cancellationAwareExecutor{}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{}, fake.IsolationEnforcementChecker{}, sharedTestAgentRegistry(t))

	handleErr := make(chan error, 1)
	go func() { handleErr <- handler.Handle(context.Background(), job) }()

	time.Sleep(50 * time.Millisecond)
	if _, err := runtime.CancelRun(context.Background(), uow, ids, runtime.CancelRunRequest{
		RunID: runID, Actor: "actor-1", Reason: "test: mid-execution cancel, bridge resolves it",
	}); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}

	select {
	case err := <-handleErr:
		if err != nil {
			t.Fatalf("Handle: %v, want nil (finalize succeeded)", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Handle did not return within 5s of a recorded cancellation intent — poller did not fire")
	}

	if executor.calls != 1 {
		t.Fatalf("executor.calls = %d, want exactly 1", executor.calls)
	}

	attempt, err := uow.Snapshot.Runtime().GetExecutionAttempt(context.Background(), attemptID)
	if err != nil {
		t.Fatalf("GetExecutionAttempt: %v", err)
	}
	if attempt.State != runtimedomain.ExecutionAttemptCancelled || attempt.TerminationReason != runtimedomain.TerminationReasonRunCancelled {
		t.Fatalf("attempt = %+v, want CANCELLED/RUN_CANCELLED", attempt)
	}

	// FinalizeExecutionAttempt's own switch (finalize.go) routes a
	// CANCELLED Attempt through decideCancelledOutcomeTx — pre-existing
	// V4-12B logic that was already fully wired end to end (NodeRun
	// RUNNING->CANCELLED, branch-token terminalization, run-terminality
	// reconciliation); this handler's own switch fix (execute.go) was the
	// only missing link needed to ever reach it via a real poller-detected
	// cancellation.
	nodeRun, err := uow.Snapshot.Runtime().GetNodeRun(context.Background(), nodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun: %v", err)
	}
	if nodeRun.State != runtimedomain.NodeRunCancelled {
		t.Fatalf("node run = %+v, want CANCELLED (decideCancelledOutcomeTx)", nodeRun)
	}

	run, err := uow.Snapshot.Runtime().GetWorkflowRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	if run.State != runtimedomain.WorkflowRunCancelled {
		t.Fatalf("run = %+v, want CANCELLED (reconcileRunTerminalityTx should close out a CANCELLING run with zero live nodes)", run)
	}
}

// --- ExecuteNodeHandler: idempotent replay ---

func TestExecuteNodeHandler_ReplayAfterAlreadyRunning_IsNoOp(t *testing.T) {
	uow, ids, _, _, attemptID := scheduledExecutionFixture(t, 600)
	job := claimableExecuteNodeJob(t, uow, attemptID)

	blockedExecutor := &fake.NodeExecutor{Block: make(chan struct{})}
	handler := runtime.NewExecuteNodeHandler(uow, ids, blockedExecutor, clock.System{}, fake.IsolationEnforcementChecker{}, sharedTestAgentRegistry(t))

	// First delivery: cancel it mid-flight so the Attempt is left RUNNING
	// (not yet finalized) — exactly the state a redelivered job would find.
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	if err := handler.Handle(ctx, job); !errors.Is(err, context.Canceled) {
		t.Fatalf("first Handle err = %v, want context.Canceled", err)
	}

	// Second delivery of the SAME job: the Attempt is now RUNNING, not
	// QUEUED — claimRunning's own idempotent no-op must fire, never
	// re-executing the (still-blocked) executor a second time.
	unusedExecutor := &fake.NodeExecutor{Err: errors.New("must not be called")}
	replayHandler := runtime.NewExecuteNodeHandler(uow, ids, unusedExecutor, clock.System{}, fake.IsolationEnforcementChecker{}, sharedTestAgentRegistry(t))
	if err := replayHandler.Handle(context.Background(), job); err != nil {
		t.Fatalf("replayed Handle: %v", err)
	}

	attempt, err := uow.Snapshot.Runtime().GetExecutionAttempt(context.Background(), attemptID)
	if err != nil {
		t.Fatalf("GetExecutionAttempt: %v", err)
	}
	if attempt.State != runtimedomain.ExecutionAttemptRunning {
		t.Fatalf("attempt = %+v, want still RUNNING (replay must not re-execute)", attempt)
	}
}

// --- FinalizeExecutionAttempt: validation ---

// seedRunningAttemptAndNodeRun drives both the Attempt and its own NodeRun
// QUEUED->RUNNING directly — claimRunning's own job (execute.go), normally
// done by ExecuteNodeHandler — for a FinalizeExecutionAttempt test that
// wants to call it directly rather than through the full handler.
func seedRunningAttemptAndNodeRun(t *testing.T, uow *fake.UnitOfWork, nodeRunID, attemptID string) {
	t.Helper()
	if err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		currentAttempt, err := tx.Runtime().GetExecutionAttempt(context.Background(), attemptID)
		if err != nil {
			return err
		}
		currentNodeRun, err := tx.Runtime().GetNodeRun(context.Background(), nodeRunID)
		if err != nil {
			return err
		}
		if _, err := tx.Runtime().TransitionNodeRun(context.Background(), ports.TransitionNodeRunRequest{
			NodeRunID: nodeRunID, ExpectedState: runtimedomain.NodeRunQueued, ExpectedVersion: currentNodeRun.Version,
			NextState: runtimedomain.NodeRunRunning,
		}); err != nil {
			return err
		}
		_, err = tx.Runtime().TransitionExecutionAttempt(context.Background(), ports.TransitionExecutionAttemptRequest{
			AttemptID: attemptID, ExpectedState: runtimedomain.ExecutionAttemptQueued, ExpectedVersion: currentAttempt.Version,
			NextState: runtimedomain.ExecutionAttemptRunning,
		})
		return err
	}); err != nil {
		t.Fatalf("seed RUNNING attempt/node run: %v", err)
	}
}

func TestFinalizeExecutionAttempt_MissingSelectedOutcomeForSucceeded_Rejected(t *testing.T) {
	uow, ids, runID, nodeRunID, attemptID := scheduledExecutionFixture(t, 600)
	job := claimableExecuteNodeJob(t, uow, attemptID)
	seedRunningAttemptAndNodeRun(t, uow, nodeRunID, attemptID)

	lease := ports.JobLease{JobID: job.ID, Owner: job.LeaseOwner, Token: job.LeaseToken, LeaseUntil: *job.LeaseUntil}
	_, err := runtime.FinalizeExecutionAttempt(context.Background(), uow, ids, clock.System{}, runtime.FinalizeExecutionAttemptRequest{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID, ExpectedVersion: 2,
		NextState: runtimedomain.ExecutionAttemptSucceeded, TerminationReason: runtimedomain.TerminationReasonCompleted,
		JobLease: lease,
	})
	if err == nil {
		t.Fatal("FinalizeExecutionAttempt succeeded, want an error (SelectedOutcome required for SUCCEEDED)")
	}
}

func TestFinalizeExecutionAttempt_UnsupportedNextState_Rejected(t *testing.T) {
	uow, ids, runID, nodeRunID, attemptID := scheduledExecutionFixture(t, 600)
	_, err := runtime.FinalizeExecutionAttempt(context.Background(), uow, ids, clock.System{}, runtime.FinalizeExecutionAttemptRequest{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID, ExpectedVersion: 1,
		NextState: runtimedomain.ExecutionAttemptRunning, TerminationReason: runtimedomain.TerminationReasonCompleted,
	})
	if !errors.Is(err, runtime.ErrUnsupportedFinalizeState) {
		t.Fatalf("err = %v, want ErrUnsupportedFinalizeState", err)
	}
}

func TestFinalizeExecutionAttempt_StaleJobLease_RollsBackEverything(t *testing.T) {
	uow, ids, runID, nodeRunID, attemptID := scheduledExecutionFixture(t, 600)
	job := claimableExecuteNodeJob(t, uow, attemptID)
	seedRunningAttemptAndNodeRun(t, uow, nodeRunID, attemptID)

	staleLease := ports.JobLease{JobID: job.ID, Owner: "someone-else", Token: 999, LeaseUntil: time.Now().Add(time.Minute)}
	_, err := runtime.FinalizeExecutionAttempt(context.Background(), uow, ids, clock.System{}, runtime.FinalizeExecutionAttemptRequest{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID, ExpectedVersion: 2,
		NextState: runtimedomain.ExecutionAttemptSucceeded, TerminationReason: runtimedomain.TerminationReasonCompleted,
		SelectedOutcome: "done", JobLease: staleLease,
	})
	if !errors.Is(err, ports.ErrJobLeaseLost) {
		t.Fatalf("err = %v, want ErrJobLeaseLost", err)
	}

	attempt, getErr := uow.Snapshot.Runtime().GetExecutionAttempt(context.Background(), attemptID)
	if getErr != nil {
		t.Fatalf("GetExecutionAttempt: %v", getErr)
	}
	if attempt.State != runtimedomain.ExecutionAttemptRunning {
		t.Fatalf("attempt = %+v, want unchanged RUNNING (stale lease must roll back everything)", attempt)
	}
	nodeRun, getErr := uow.Snapshot.Runtime().GetNodeRun(context.Background(), nodeRunID)
	if getErr != nil {
		t.Fatalf("GetNodeRun: %v", getErr)
	}
	if nodeRun.State != runtimedomain.NodeRunRunning {
		t.Fatalf("node run = %+v, want unchanged RUNNING", nodeRun)
	}
	events := uow.Snapshot.Events().(*fake.EventsRepository).Items()
	for _, e := range events {
		if e.EventType == runtime.ExecutionAttemptFinalizedEventType {
			t.Fatalf("found %s event after a rolled-back finalize: %+v", runtime.ExecutionAttemptFinalizedEventType, e)
		}
	}
}
