package runtime_test

// V12-19 (ADR-054): the family queue starts one task at a time, in order, after the one before is DONE; a failed task pauses it.

import (
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

func (f *rerunSQLite) readyChild(key, title string) string {
	f.t.Helper()
	child, err := work.CreateChildWorkItem(f.ctx, f.uow, f.ids, redoCommand("idem-child-"+key, "CreateChildWorkItem"), work.CreateChildWorkItemRequest{
		ParentWorkItemID: f.rootID(), Title: title, ParentJoinPolicy: "ALL_CHILDREN_DONE",
		EffectiveScope: []work.ScopeGrantRequest{{RepositoryID: "repo-1", Access: string(workdomain.RepositoryWrite), PathScopes: []string{"**"}, Reason: title}},
		Contract: &work.WorkItemContractRequest{
			SchemaVersion: 1, Behavior: title, VerificationSpec: "run the tests", RiskLevel: "LOW",
			AcceptanceCriteria: []work.AcceptanceCriterionRequest{{Description: "tests pass", VerificationRef: "cmd:test"}},
			WorkflowVersionID:  string(f.version.ID()),
		},
	})
	if err != nil {
		f.t.Fatalf("CreateChildWorkItem: %v", err)
	}
	if _, err := work.MarkWorkItemReady(f.ctx, f.uow, redoCommand("idem-ready-"+key, "MarkWorkItemReady"), work.MarkWorkItemReadyRequest{WorkItemID: child.WorkItemID}); err != nil {
		f.t.Fatalf("MarkWorkItemReady: %v", err)
	}
	return child.WorkItemID
}

func (f *rerunSQLite) rootID() string {
	f.t.Helper()
	var id string
	_ = f.uow.WithReadOnly(f.ctx, func(tx ports.Tx) error {
		item, err := tx.Work().GetWorkItem(f.ctx, f.workItemID)
		if err == nil && item.ParentID != nil {
			id = string(*item.ParentID)
		} else {
			id = f.workItemID
		}
		return err
	})
	return id
}

func (f *rerunSQLite) queue(key, workItemID, waitsFor string) runtime.QueueEntryView {
	f.t.Helper()
	view, err := runtime.SetWorkItemQueue(f.ctx, f.uow, redoCommand("idem-queue-"+key, "SetWorkItemQueue"), runtime.SetQueueRequest{
		ProjectID: "project-1", WorkItemID: workItemID, Queued: true, WaitsForWorkItemID: waitsFor,
	})
	if err != nil {
		f.t.Fatalf("SetWorkItemQueue(%s): %v", workItemID, err)
	}
	return view
}

func (f *rerunSQLite) sweepQueues() runtime.QueueAdvanceReport {
	f.t.Helper()
	report, err := runtime.AdvanceFamilyQueues(f.ctx, f.uow, f.ids, time.Now().UTC())
	if err != nil {
		f.t.Fatalf("AdvanceFamilyQueues: %v", err)
	}
	return report
}

func (f *rerunSQLite) setRunState(runID string, next runtimedomain.WorkflowRunState) {
	f.t.Helper()
	if err := f.uow.WithSerializedWrite(f.ctx, func(tx ports.Tx) error {
		run, err := tx.Runtime().GetWorkflowRun(f.ctx, runID)
		if err != nil {
			return err
		}
		_, err = tx.Runtime().TransitionWorkflowRunState(f.ctx, ports.TransitionWorkflowRunStateRequest{
			RunID: runID, ExpectedState: run.State, ExpectedVersion: run.Version, NextState: next,
		})
		return err
	}); err != nil {
		f.t.Fatalf("set run %s %s: %v", runID, next, err)
	}
}

func (f *rerunSQLite) setStatus(workItemID string, next workdomain.WorkItemStatus) {
	f.t.Helper()
	if err := f.uow.WithSerializedWrite(f.ctx, func(tx ports.Tx) error {
		item, err := tx.Work().GetWorkItem(f.ctx, workItemID)
		if err != nil {
			return err
		}
		_, err = tx.Work().TransitionWorkItemStatus(f.ctx, ports.TransitionWorkItemStatusRequest{
			WorkItemID: workItemID, ExpectedStatus: item.Status, ExpectedVersion: item.Version, NextStatus: next,
		})
		return err
	}); err != nil {
		f.t.Fatalf("set %s %s: %v", workItemID, next, err)
	}
}

func TestFamilyQueue_StartsOneTaskAtATimeAndPausesOnFailure(t *testing.T) {
	f, _ := newRecreateSQLite(t)
	first := f.workItemID
	second := f.readyChild("2", "T-02: list the orders")
	f.queue("1", first, "")
	f.queue("2", second, first)

	report := f.sweepQueues()
	if len(report.Started) != 1 || report.Started[0] != first {
		t.Fatalf("first sweep started %v, want only %s", report.Started, first)
	}
	queue, err := runtime.GetFamilyQueue(f.ctx, f.uow, "project-1", queueFamily(t, f, first))
	if err != nil {
		t.Fatalf("GetFamilyQueue: %v", err)
	}
	if len(queue.Entries) != 2 || queue.Entries[0].State != "STARTED" || queue.Entries[0].RunID == "" || queue.Entries[1].State != "QUEUED" {
		t.Fatalf("queue = %+v, want the first STARTED with its run and the second QUEUED", queue.Entries)
	}
	if !strings.Contains(queue.Waiting, "to finish") {
		t.Fatalf("waiting = %q, want the second to wait for the first", queue.Waiting)
	}
	if again := f.sweepQueues(); len(again.Started) != 0 {
		t.Fatalf("a second sweep started %v while the first task runs", again.Started)
	}

	// The first task's run fails: the queue pauses (fail-closed), it does not start the second.
	f.setRunState(queue.Entries[0].RunID, runtimedomain.WorkflowRunFailed)
	if paused := f.sweepQueues(); len(paused.Started) != 0 {
		t.Fatalf("the queue started %v after a failure", paused.Started)
	}
	queue, _ = runtime.GetFamilyQueue(f.ctx, f.uow, "project-1", queueFamily(t, f, first))
	if !strings.HasPrefix(queue.Waiting, "paused:") {
		t.Fatalf("waiting = %q, want paused", queue.Waiting)
	}

	// Once the first task is DONE, the second starts.
	f.setStatus(first, workdomain.WorkItemDone)
	if resumed := f.sweepQueues(); len(resumed.Started) != 1 || resumed.Started[0] != second {
		t.Fatalf("after the first is DONE the sweep started %v, want %s", resumed.Started, second)
	}

	// A DONE work item cannot be queued; taking a STARTED entry out is refused.
	if _, err := runtime.SetWorkItemQueue(f.ctx, f.uow, redoCommand("idem-queue-done", "SetWorkItemQueue"), runtime.SetQueueRequest{
		ProjectID: "project-1", WorkItemID: first, Queued: true,
	}); err == nil {
		t.Fatal("queueing a DONE work item must be refused")
	}
}

func queueFamily(t *testing.T, f *rerunSQLite, workItemID string) string {
	t.Helper()
	var family string
	if err := f.uow.WithReadOnly(f.ctx, func(tx ports.Tx) error {
		item, err := tx.Work().GetWorkItem(f.ctx, workItemID)
		family = string(item.FamilyID)
		return err
	}); err != nil {
		t.Fatalf("GetWorkItem: %v", err)
	}
	return family
}
