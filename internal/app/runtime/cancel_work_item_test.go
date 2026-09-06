package runtime_test

import (
	"context"
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// V4-12C's own WorkItem-level cancellation tests (docs/design/06-v4-runtime-engine.md,
// ADR-020). See resolve_work_item_blocker_test.go for the blocker-resolution
// authority matrix.

// cancelWorkItemFixture creates one ACTIVE repository, a root WorkItem READY
// to start a run (readyFixture), and publishes workflowDocumentV1 — exposing
// WorkItemID/WorkflowVersionID directly (unlike startWorkflowRunFixture,
// which starts exactly one run inline) so a test can start zero, one, or
// several runs against the SAME WorkItem across its own lifecycle.
func cancelWorkItemFixture(t *testing.T) (uow *fake.UnitOfWork, ids idsource.Source, workItemID, workflowVersionID string) {
	t.Helper()
	u := fake.New()
	seq := idsource.NewSequential("id")
	root := readyFixture(t, u, seq, "project-1", "repo-1")
	version := publishWorkflowVersionDocument(t, u, "project-1", "wf-def-1", "wf-v-1", workflowDocumentV1())
	return u, seq, root.WorkItemID, string(version.ID())
}

// startRun starts a new WorkflowRun for workItemID — the WorkItem must
// currently be READY. idemKey must be unique per call (a fresh
// StartWorkflowRun, never a replay).
func startRun(t *testing.T, uow *fake.UnitOfWork, ids idsource.Source, workItemID, workflowVersionID, idemKey string) runtime.StartWorkflowRunResult {
	t.Helper()
	cmd := testCommand(idemKey, "hash-"+idemKey, ports.ProjectScope("project-1"), "StartWorkflowRun")
	result, err := runtime.StartWorkflowRun(context.Background(), uow, ids, cmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: workItemID, WorkflowVersionID: workflowVersionID,
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	return result
}

func workItemState(t *testing.T, uow *fake.UnitOfWork, workItemID string) workdomain.WorkItem {
	t.Helper()
	item, err := uow.Snapshot.Work().GetWorkItem(context.Background(), workItemID)
	if err != nil {
		t.Fatalf("GetWorkItem: %v", err)
	}
	return item
}

func workItemBlockers(t *testing.T, uow *fake.UnitOfWork, workItemID string) []workdomain.WorkItemBlocker {
	t.Helper()
	blockers, err := uow.Snapshot.Work().ListWorkItemBlockersForWorkItem(context.Background(), workItemID)
	if err != nil {
		t.Fatalf("ListWorkItemBlockersForWorkItem: %v", err)
	}
	return blockers
}

// TestCancelWorkItem_ZeroRuns_ImmediatelyCancelled proves the "0 active Run"
// case the task's own Verify line names directly: a WorkItem that never
// started any run at all closes out to CANCELLED the moment CancelWorkItem's
// own transaction commits — reconcileWorkItemCancellationTx's own tail call,
// never waiting for a Run-closing transaction that will never come.
func TestCancelWorkItem_ZeroRuns_ImmediatelyCancelled(t *testing.T) {
	uow, ids, workItemID, _ := cancelWorkItemFixture(t)

	result, err := runtime.CancelWorkItem(context.Background(), uow, ids, runtime.CancelWorkItemRequest{
		WorkItemID: workItemID, Actor: "operator-1", Reason: "no longer needed",
	})
	if err != nil {
		t.Fatalf("CancelWorkItem: %v", err)
	}
	if result.AlreadyRequested || result.Status != string(workdomain.WorkItemCancelled) {
		t.Fatalf("result = %+v, want AlreadyRequested=false Status=CANCELLED", result)
	}
	item := workItemState(t, uow, workItemID)
	if item.Status != workdomain.WorkItemCancelled {
		t.Fatalf("work item status = %s, want CANCELLED", item.Status)
	}
}

// TestCancelWorkItem_OneActiveRun_QuiescesThenTerminalizesOnceRunCloses
// proves the ordinary "1 active Run" case: CancelWorkItem's own transaction
// drives the Run to CANCELLING (never CANCELLED directly — that still needs
// the coordinator's own sweep) and leaves the WorkItem itself untouched
// (never BLOCKED — this is the WorkItem-driven cancellation exception
// openRunCancelledBlockerTx's own doc comment describes) until the Run
// actually finishes quiescing, at which point transitionRunToCancelledTx's
// own tail call (reconcileWorkItemCancellationTx) finally closes the
// WorkItem out. This also proves no RUN_CANCELLED blocker is ever opened for
// a WorkItem-driven cancellation.
func TestCancelWorkItem_OneActiveRun_QuiescesThenTerminalizesOnceRunCloses(t *testing.T) {
	uow, ids, workItemID, workflowVersionID := cancelWorkItemFixture(t)
	run := startRun(t, uow, ids, workItemID, workflowVersionID, "idem-start-1")

	result, err := runtime.CancelWorkItem(context.Background(), uow, ids, runtime.CancelWorkItemRequest{
		WorkItemID: workItemID, Actor: "operator-1", Reason: "no longer needed",
	})
	if err != nil {
		t.Fatalf("CancelWorkItem: %v", err)
	}
	if result.Status != string(workdomain.WorkItemActive) {
		t.Fatalf("result.Status = %s immediately after CancelWorkItem, want still ACTIVE (Run not yet quiesced)", result.Status)
	}
	if got := runState(t, uow, run.RunID).State; got != runtimedomain.WorkflowRunCancelling {
		t.Fatalf("run state = %s, want CANCELLING", got)
	}
	item := workItemState(t, uow, workItemID)
	if item.Status != workdomain.WorkItemActive {
		t.Fatalf("work item status after CancelWorkItem = %s, want still ACTIVE (never forced BLOCKED while a WorkItem-level cancel is in flight)", item.Status)
	}

	driveCancelRunCoordinator(t, uow, ids, run.RunID)

	if got := runState(t, uow, run.RunID).State; got != runtimedomain.WorkflowRunCancelled {
		t.Fatalf("run state after coordinator sweep = %s, want CANCELLED", got)
	}
	item = workItemState(t, uow, workItemID)
	if item.Status != workdomain.WorkItemCancelled {
		t.Fatalf("work item status after run closed = %s, want CANCELLED", item.Status)
	}
	for _, b := range workItemBlockers(t, uow, workItemID) {
		if b.Type == workdomain.BlockerRunCancelled {
			t.Fatalf("unexpected RUN_CANCELLED blocker %+v opened for a WorkItem-driven cancellation", b)
		}
	}

	intent, err := uow.Snapshot.Runtime().GetWorkItemCancellationIntent(context.Background(), workItemID)
	if err != nil {
		t.Fatalf("GetWorkItemCancellationIntent: %v", err)
	}
	if intent.State != runtimedomain.CancellationIntentCompleted {
		t.Fatalf("intent state = %s, want COMPLETED", intent.State)
	}
}

// TestCancelWorkItem_ManyRuns_SkipsTerminalQuiescesActive proves the "nhiều
// active Run" case: a WorkItem that already has one historically-terminal
// Run (a standalone CancelRun/coordinator cycle completed earlier, cycling
// the WorkItem back through READY) plus a second, currently-live Run.
// CancelWorkItem must never re-touch the first (already terminal) Run and
// must drive the second to CANCELLING exactly like the single-Run case.
func TestCancelWorkItem_ManyRuns_SkipsTerminalQuiescesActive(t *testing.T) {
	uow, ids, workItemID, workflowVersionID := cancelWorkItemFixture(t)

	firstRun := startRun(t, uow, ids, workItemID, workflowVersionID, "idem-start-1")
	cancelActor(t, uow, ids, firstRun.RunID)
	driveCancelRunCoordinator(t, uow, ids, firstRun.RunID)
	if got := runState(t, uow, firstRun.RunID).State; got != runtimedomain.WorkflowRunCancelled {
		t.Fatalf("first run state = %s, want CANCELLED", got)
	}
	// Resolve the RUN_CANCELLED blocker this standalone cancel opened, to
	// cycle the WorkItem back to READY so a second run can start.
	var firstBlocker workdomain.WorkItemBlocker
	for _, b := range workItemBlockers(t, uow, workItemID) {
		if b.Type == workdomain.BlockerRunCancelled {
			firstBlocker = b
		}
	}
	if firstBlocker.ID == "" {
		t.Fatal("no RUN_CANCELLED blocker found after first run cancelled")
	}
	if _, err := runtime.ResolveWorkItemBlocker(context.Background(), uow, runtime.ResolveWorkItemBlockerRequest{
		BlockerID: string(firstBlocker.ID), Mode: runtime.ResolutionModeResolved, Actor: "operator-1", Reason: "starting fresh",
	}); err != nil {
		t.Fatalf("ResolveWorkItemBlocker: %v", err)
	}
	if got := workItemState(t, uow, workItemID).Status; got != workdomain.WorkItemReady {
		t.Fatalf("work item status after resolving first blocker = %s, want READY", got)
	}

	secondRun := startRun(t, uow, ids, workItemID, workflowVersionID, "idem-start-2")

	result, err := runtime.CancelWorkItem(context.Background(), uow, ids, runtime.CancelWorkItemRequest{
		WorkItemID: workItemID, Actor: "operator-1", Reason: "cancel everything",
	})
	if err != nil {
		t.Fatalf("CancelWorkItem: %v", err)
	}
	if result.Status != string(workdomain.WorkItemActive) {
		t.Fatalf("result.Status = %s, want still ACTIVE (second run not yet quiesced)", result.Status)
	}

	// The first run must be completely untouched by this second cancel cycle.
	firstAfter := runState(t, uow, firstRun.RunID)
	if firstAfter.State != runtimedomain.WorkflowRunCancelled {
		t.Fatalf("first run state = %s, want unchanged CANCELLED", firstAfter.State)
	}
	if got := runState(t, uow, secondRun.RunID).State; got != runtimedomain.WorkflowRunCancelling {
		t.Fatalf("second run state = %s, want CANCELLING", got)
	}

	driveCancelRunCoordinator(t, uow, ids, secondRun.RunID)
	if got := runState(t, uow, secondRun.RunID).State; got != runtimedomain.WorkflowRunCancelled {
		t.Fatalf("second run state after coordinator sweep = %s, want CANCELLED", got)
	}
	if got := workItemState(t, uow, workItemID).Status; got != workdomain.WorkItemCancelled {
		t.Fatalf("work item status after second run closed = %s, want CANCELLED", got)
	}
}

// TestCancelWorkItem_AlreadyRequested_Idempotent proves a second
// CancelWorkItem call for the same WorkItem is a harmless no-op, never a
// second intent/event.
func TestCancelWorkItem_AlreadyRequested_Idempotent(t *testing.T) {
	uow, ids, workItemID, workflowVersionID := cancelWorkItemFixture(t)
	startRun(t, uow, ids, workItemID, workflowVersionID, "idem-start-1")

	first, err := runtime.CancelWorkItem(context.Background(), uow, ids, runtime.CancelWorkItemRequest{
		WorkItemID: workItemID, Actor: "operator-1", Reason: "first",
	})
	if err != nil {
		t.Fatalf("first CancelWorkItem: %v", err)
	}
	second, err := runtime.CancelWorkItem(context.Background(), uow, ids, runtime.CancelWorkItemRequest{
		WorkItemID: workItemID, Actor: "operator-2", Reason: "second",
	})
	if err != nil {
		t.Fatalf("second CancelWorkItem: %v", err)
	}
	if !second.AlreadyRequested {
		t.Fatalf("second result = %+v, want AlreadyRequested=true", second)
	}
	if second.Status != first.Status {
		t.Fatalf("second.Status = %s, want unchanged %s", second.Status, first.Status)
	}
}

// TestCancelWorkItem_AlreadyDone_Rejected proves ADR-020's own WorkItem-level
// cancel-vs-PASS race, "PASS trước → CancelWorkItem bị ErrWorkItemAlreadyTerminal"
// — simulated here by directly driving the WorkItem to DONE the same way a
// future CompletionPolicy PASS transaction would (no such command exists yet
// in this codebase, V5-11's own scope), the identical "simulate the future
// command's own CAS directly" discipline V4-12B's own cancel-vs-PASS race
// test already establishes at the Run level.
func TestCancelWorkItem_AlreadyDone_Rejected(t *testing.T) {
	uow, ids, workItemID, _ := cancelWorkItemFixture(t)
	item := workItemState(t, uow, workItemID)
	if err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Work().TransitionWorkItemStatus(context.Background(), ports.TransitionWorkItemStatusRequest{
			WorkItemID: workItemID, ExpectedStatus: item.Status, ExpectedVersion: item.Version, NextStatus: workdomain.WorkItemDone,
		})
		return err
	}); err != nil {
		t.Fatalf("force work item DONE: %v", err)
	}

	_, err := runtime.CancelWorkItem(context.Background(), uow, ids, runtime.CancelWorkItemRequest{
		WorkItemID: workItemID, Actor: "operator-1", Reason: "too late",
	})
	if !errors.Is(err, runtime.ErrWorkItemAlreadyTerminal) {
		t.Fatalf("CancelWorkItem error = %v, want ErrWorkItemAlreadyTerminal", err)
	}
}

// TestRace_CancelWorkItemVsPass_IntentFirst_PassCASRejected proves the other
// half of that same race: an intent committing first CASes the Run out from
// under a later simulated PASS attempt (Run VERIFYING -> SUCCEEDED), which
// then fails with ErrOptimisticConflict naturally — no special "is this
// WorkItem cancelling" branch needed anywhere in that hypothetical future
// command, the identical reasoning V4-12B's own Run-level race test already
// establishes.
func TestRace_CancelWorkItemVsPass_IntentFirst_PassCASRejected(t *testing.T) {
	uow, ids, workItemID, workflowVersionID := cancelWorkItemFixture(t)
	run := startRun(t, uow, ids, workItemID, workflowVersionID, "idem-start-1")

	// Advance start->end so the Run reaches VERIFYING (a real completion
	// candidate) before the race begins.
	if _, err := runtime.AdvanceRun(context.Background(), uow, ids, runtime.AdvanceRunRequest{RunID: run.RunID, NodeRunID: run.NodeRunID}); err != nil {
		t.Fatalf("AdvanceRun (start->end): %v", err)
	}
	verifying := runState(t, uow, run.RunID)
	if verifying.State != runtimedomain.WorkflowRunVerifying {
		t.Fatalf("run state = %s, want VERIFYING", verifying.State)
	}

	if _, err := runtime.CancelWorkItem(context.Background(), uow, ids, runtime.CancelWorkItemRequest{
		WorkItemID: workItemID, Actor: "operator-1", Reason: "cancel before verify resolves",
	}); err != nil {
		t.Fatalf("CancelWorkItem: %v", err)
	}
	cancelling := runState(t, uow, run.RunID)
	if cancelling.State != runtimedomain.WorkflowRunCancelling {
		t.Fatalf("run state after CancelWorkItem = %s, want CANCELLING", cancelling.State)
	}

	// Simulate the future CompletionPolicy PASS's own CAS, using the STALE
	// version captured before the cancel intent's own CAS already bumped it.
	err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Runtime().TransitionWorkflowRunState(context.Background(), ports.TransitionWorkflowRunStateRequest{
			RunID: run.RunID, ExpectedState: runtimedomain.WorkflowRunVerifying, ExpectedVersion: verifying.Version,
			NextState: runtimedomain.WorkflowRunSucceeded,
		})
		return err
	})
	if !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("simulated PASS after cancel intent committed error = %v, want ErrOptimisticConflict", err)
	}
}
