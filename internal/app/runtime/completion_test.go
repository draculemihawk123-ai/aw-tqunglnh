package runtime_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// V4-12's own "Run-level completion candidate / failure aggregation"
// tests (docs/design/06-v4-runtime-engine.md). Reuses workflowDocumentV1
// (advance_test.go, a plain start->end document), scheduledExecutionFixture
// (execute_test.go), and forkExecutableDocument/joinPolicyDocument/
// driveBranchOutcome (fork_test.go/join_test.go) rather than inventing new
// fixtures.

type runFailedPayload struct {
	RunID      string `json:"runId"`
	WorkItemID string `json:"workItemId"`
	Reason     string `json:"reason"`
}

func runFailedEventsFor(t *testing.T, uow *fake.UnitOfWork, runID string) []runFailedPayload {
	t.Helper()
	events := uow.Snapshot.Events().(*fake.EventsRepository).Items()
	var out []runFailedPayload
	for _, e := range events {
		if e.EventType != runtime.RunFailedEventType || e.AggregateID != runID {
			continue
		}
		var p runFailedPayload
		if err := json.Unmarshal([]byte(e.PayloadJSON), &p); err != nil {
			t.Fatalf("decode %s payload: %v", runtime.RunFailedEventType, err)
		}
		out = append(out, p)
	}
	return out
}

// TestReconcileRunTerminality_EndReached_RunVerifying_WorkItemUntouched is
// this task's own "agent proposed done / no evidence" Verify-line test:
// reaching END — with no Evidence of any kind ever recorded, exactly the
// AK-ARCH-005/GC-INV-12 scenario the task's own Nguồn cites — must never
// itself produce Run SUCCEEDED or WorkItem DONE. It produces exactly
// VERIFYING (ADR-011's own "completion candidate"), leaves WorkItem
// completely untouched (still ACTIVE, per readyFixture's own setup), and
// appends RUN_COMPLETION_REQUESTED naming the END NodeRun reached.
func TestReconcileRunTerminality_EndReached_RunVerifying_WorkItemUntouched(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, startNodeRunID := startWorkflowRunFixture(t, workflowDocumentV1())

	hop, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: runID, NodeRunID: startNodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun (start->end): %v", err)
	}
	if hop.NextNodeKey != "end" {
		t.Fatalf("hop = %+v, want NextNodeKey=end", hop)
	}

	run, err := uow.Snapshot.Runtime().GetWorkflowRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	if run.State != runtimedomain.WorkflowRunVerifying {
		t.Fatalf("run state = %s, want VERIFYING", run.State)
	}

	events := uow.Snapshot.Events().(*fake.EventsRepository).Items()
	found := false
	for _, e := range events {
		if e.EventType == runtime.RunCompletionRequestedEventType && e.AggregateID == runID {
			var payload struct {
				EndNodeRunID string `json:"endNodeRunId"`
				EndNodeKey   string `json:"endNodeKey"`
			}
			if err := json.Unmarshal([]byte(e.PayloadJSON), &payload); err != nil {
				t.Fatalf("decode %s payload: %v", runtime.RunCompletionRequestedEventType, err)
			}
			if payload.EndNodeRunID != hop.NextNodeRunID || payload.EndNodeKey != "end" {
				t.Fatalf("%s payload = %+v, want endNodeRunId=%s endNodeKey=end", runtime.RunCompletionRequestedEventType, payload, hop.NextNodeRunID)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("no %s event found for run %s among %+v", runtime.RunCompletionRequestedEventType, runID, events)
	}

	item, err := uow.Snapshot.Work().GetWorkItem(ctx, string(run.WorkItemID))
	if err != nil {
		t.Fatalf("GetWorkItem: %v", err)
	}
	if item.Status != workdomain.WorkItemActive {
		t.Fatalf("work item status = %s, want unchanged ACTIVE — reaching END must never itself make WorkItem DONE (AK-ARCH-005/GC-INV-12)", item.Status)
	}
}

// TestReconcileRunTerminality_NodeRunFailedNoOtherLiveWork_RunFailed
// proves the base case the user's own example named directly: a single,
// non-forked NodeRun exhausting its retry budget with no other live
// NodeRun anywhere in the Run must not leave the Run stuck RUNNING
// forever — reconcileRunTerminalityTx (called from
// decideRetryOrExhaustion, V4-06) CASes it to FAILED with
// Reason=RunFailureReasonRunFailed.
func TestReconcileRunTerminality_NodeRunFailedNoOtherLiveWork_RunFailed(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, nodeRunID, attemptID := scheduledExecutionFixture(t, 600)
	job := claimableExecuteNodeJob(t, uow, attemptID)

	executor := &fake.NodeExecutor{Result: ports.NodeExecutionResult{State: runtimedomain.ExecutionAttemptFailed}}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{}, fake.IsolationEnforcementChecker{}, agentregistry.Empty())
	if err := handler.Handle(ctx, job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	nodeRun, err := uow.Snapshot.Runtime().GetNodeRun(ctx, nodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun: %v", err)
	}
	if nodeRun.State != runtimedomain.NodeRunFailed {
		t.Fatalf("node run = %+v, want FAILED", nodeRun)
	}

	run, err := uow.Snapshot.Runtime().GetWorkflowRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	if run.State != runtimedomain.WorkflowRunFailed {
		t.Fatalf("run state = %s, want FAILED (no other live NodeRun anywhere in this Run)", run.State)
	}
	failedEvents := runFailedEventsFor(t, uow, runID)
	if len(failedEvents) != 1 || failedEvents[0].Reason != runtime.RunFailureReasonRunFailed {
		t.Fatalf("%s events = %+v, want exactly 1 with Reason=%s", runtime.RunFailedEventType, failedEvents, runtime.RunFailureReasonRunFailed)
	}
}

// TestReconcileRunTerminality_ForkBranchFailed_NoOtherLiveWork_RunFailed
// proves the same base case as the test above, but through a FORK branch
// rather than a plain top-level node: forkExecutableDocument's own
// "to_implement" (a real AGENT) is the ONLY branch ever live ("shortcut"
// already reached its own outer JOIN at fan-out time, per V4-10) — once it
// fails for real with nothing else live anywhere in the Run, the Run
// itself has no path forward and CASes to FAILED, exactly like the
// non-forked case.
func TestReconcileRunTerminality_ForkBranchFailed_NoOtherLiveWork_RunFailed(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, _, hop := forkScheduleFixture(t, forkExecutableDocument())
	publishAgentProfileVersion(t, uow, "agent-profile-def", "agent-profile-v1", validAgentProfileDocument())
	publishPolicyVersion(t, uow, "attempt-policy-def", "attempt-policy-v1", attemptPolicyDocument(600))
	publishPolicyVersion(t, uow, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())

	implementBranch := findForkedBranch(hop.ForkedBranches, "to_implement")
	if implementBranch == nil {
		t.Fatalf("ForkedBranches = %+v, want to_implement", hop.ForkedBranches)
	}

	scheduled, err := runtime.ScheduleExecutableNodeRun(ctx, uow, ids, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: runID, NodeRunID: implementBranch.NodeRunID, CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("ScheduleExecutableNodeRun: %v", err)
	}
	job := claimableExecuteNodeJob(t, uow, scheduled.AttemptID)
	executor := &fake.NodeExecutor{Result: ports.NodeExecutionResult{State: runtimedomain.ExecutionAttemptFailed}}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{}, fake.IsolationEnforcementChecker{}, agentregistry.Empty())
	if err := handler.Handle(ctx, job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	run, err := uow.Snapshot.Runtime().GetWorkflowRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	if run.State != runtimedomain.WorkflowRunFailed {
		t.Fatalf("run state = %s, want FAILED", run.State)
	}
	failedEvents := runFailedEventsFor(t, uow, runID)
	if len(failedEvents) != 1 || failedEvents[0].Reason != runtime.RunFailureReasonRunFailed {
		t.Fatalf("%s events = %+v, want exactly 1 with Reason=%s", runtime.RunFailedEventType, failedEvents, runtime.RunFailureReasonRunFailed)
	}
}

// TestReconcileRunTerminality_BlockedSibling_NeverFailsRun proves the
// user's own explicit correction to the reducer's priority order: a
// BLOCKED NodeRun is recoverable-stalled, never a reason to fail the Run,
// even when it is the ONLY thing keeping LiveCount at zero — the
// RetryBlockedActivation/scope-amendment/cancel protocol (V4-12A/V4-12B,
// neither built yet) owns it exclusively. Branch B's own NodeRun is
// poked directly to BLOCKED (mirroring seedRunningNodeRun's own "poke
// state directly for test setup" precedent — nothing in this codebase
// produces NodeRunBlocked for real yet), then branch A fails for real
// through the public ExecuteNodeHandler pipeline, which is what actually
// drives reconcileRunTerminalityTx.
func TestReconcileRunTerminality_BlockedSibling_NeverFailsRun(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, _, hop := forkScheduleFixture(t, joinPolicyDocument(workflow.JoinModeAll, 0, []string{"a", "b"}))
	publishJoinPolicyFixtures(t, uow)

	branchA := findForkedBranch(hop.ForkedBranches, "a")
	branchB := findForkedBranch(hop.ForkedBranches, "b")
	if branchA == nil || branchB == nil {
		t.Fatalf("ForkedBranches = %+v, want a and b", hop.ForkedBranches)
	}

	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		current, err := tx.Runtime().GetNodeRun(ctx, branchB.NodeRunID)
		if err != nil {
			return err
		}
		_, err = tx.Runtime().TransitionNodeRun(ctx, ports.TransitionNodeRunRequest{
			NodeRunID: branchB.NodeRunID, ExpectedState: current.State, ExpectedVersion: current.Version,
			NextState: runtimedomain.NodeRunBlocked,
		})
		return err
	}); err != nil {
		t.Fatalf("seed branch b BLOCKED: %v", err)
	}

	driveBranchOutcome(t, uow, ids, runID, branchA.NodeRunID, false)

	run, err := uow.Snapshot.Runtime().GetWorkflowRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	if run.State != runtimedomain.WorkflowRunRunning {
		t.Fatalf("run state = %s, want still RUNNING (branch b is BLOCKED, not a failure signal)", run.State)
	}
	if failedEvents := runFailedEventsFor(t, uow, runID); len(failedEvents) != 0 {
		t.Fatalf("%s events = %+v, want none", runtime.RunFailedEventType, failedEvents)
	}
}

// TestReconcileRunTerminality_JoinAllMode_SiblingStillActive_ThenLateArrival_RunFailedOnlyOnceEmpty
// is the central proof of the reducer's own "must run after every
// transaction that could remove the last live activation, not just the
// one that first triggers a failure" correction: branch A fails (ALL mode
// -> JOIN already decides FAILED, per V4-11) while branch B is STILL
// genuinely live — the Run must stay RUNNING at that exact moment, not
// FAILED — and only once branch B later, separately, reaches its own real
// terminal state (here: SUCCEEDED) does the Run transition to FAILED, at
// THAT later hop, driven by branch A's own earlier failure (FailedCount
// still counts it) rather than by branch B's own outcome.
func TestReconcileRunTerminality_JoinAllMode_SiblingStillActive_ThenLateArrival_RunFailedOnlyOnceEmpty(t *testing.T) {
	uow, ids, runID, _, hop := forkScheduleFixture(t, joinPolicyDocument(workflow.JoinModeAll, 0, []string{"a", "b"}))
	publishJoinPolicyFixtures(t, uow)

	branchA := findForkedBranch(hop.ForkedBranches, "a")
	branchB := findForkedBranch(hop.ForkedBranches, "b")
	if branchA == nil || branchB == nil {
		t.Fatalf("ForkedBranches = %+v, want a and b", hop.ForkedBranches)
	}

	driveBranchOutcome(t, uow, ids, runID, branchA.NodeRunID, false)

	runAfterA, err := uow.Snapshot.Runtime().GetWorkflowRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("GetWorkflowRun after branch A: %v", err)
	}
	if runAfterA.State != runtimedomain.WorkflowRunRunning {
		t.Fatalf("run state after branch A failed = %s, want still RUNNING (branch B still ACTIVE)", runAfterA.State)
	}

	driveBranchOutcome(t, uow, ids, runID, branchB.NodeRunID, true)

	runAfterB, err := uow.Snapshot.Runtime().GetWorkflowRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("GetWorkflowRun after branch B: %v", err)
	}
	if runAfterB.State != runtimedomain.WorkflowRunFailed {
		t.Fatalf("run state after branch B terminated = %s, want FAILED (branch A's own earlier failure, no END ever reached)", runAfterB.State)
	}
	failedEvents := runFailedEventsFor(t, uow, runID)
	if len(failedEvents) != 1 || failedEvents[0].Reason != runtime.RunFailureReasonRunFailed {
		t.Fatalf("%s events = %+v, want exactly 1 with Reason=%s", runtime.RunFailedEventType, failedEvents, runtime.RunFailureReasonRunFailed)
	}
}
