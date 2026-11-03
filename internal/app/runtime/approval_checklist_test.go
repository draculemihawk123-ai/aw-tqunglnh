package runtime_test

import (
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// V12-31 (ADR-063): a QA checklist approval, against a real sqlite store.

func checklistDocument() workflow.WorkflowDocument {
	document := approvalDocument(600)
	for index := range document.Nodes {
		if document.Nodes[index].Key == "gate" {
			document.Nodes[index].Approval.Checklist = &workflow.ApprovalChecklistConfig{
				FromAcceptanceCriteria: true, Items: []string{"Login works on a phone", "No console error"},
				PassedOutcome: "approved", FailedOutcome: "rejected",
			}
		}
	}
	return document
}

func (fx *releaseFixture) resolveChecklist(key, outcome string, results []runtime.ChecklistItemResult) (runtime.ResolveApprovalResult, error) {
	return runtime.ResolveApproval(fx.ctx, fx.uow, fx.ids, reviewerCommand(key, "hash-"+key), runtime.ResolveApprovalRequest{
		RunID: fx.runID, ApprovalRequestID: fx.approvalRequestID, Outcome: outcome, Checklist: results,
	})
}

func TestChecklistItems_TakeTheAcceptanceCriteriaThenTheManualItems(t *testing.T) {
	cfg := &workflow.ApprovalChecklistConfig{FromAcceptanceCriteria: true, Items: []string{"by hand"}, PassedOutcome: "a", FailedOutcome: "b"}
	items := runtime.ChecklistItems(cfg, workdomain.WorkItem{AcceptanceCriteria: []workdomain.AcceptanceCriterion{{Description: "Given X then Y"}}})
	if len(items) != 2 || items[0].ID != "ac-1" || items[0].Text != "Given X then Y" || items[0].Source != runtime.ChecklistSourceAcceptance ||
		items[1].ID != "item-1" || items[1].Source != runtime.ChecklistSourceManual {
		t.Fatalf("items = %+v", items)
	}
}

func TestResolveApproval_ChecklistDecidesTheOutcomeAndIsRecorded(t *testing.T) {
	fx := newGateFixture(t, checklistDocument())
	detail, err := runtime.GetRunDetail(fx.ctx, fx.uow, fx.runID)
	if err != nil || detail.ApprovalRequests[0].Checklist == nil || len(detail.ApprovalRequests[0].Checklist.Items) != 2 {
		t.Fatalf("pending checklist = %+v, %v", detail.ApprovalRequests, err)
	}

	if _, err := fx.resolve("plain", "approved", nil); !errors.Is(err, runtime.ErrChecklistRequired) {
		t.Fatalf("a decision without results: %v", err)
	}
	failed := []runtime.ChecklistItemResult{{ID: "item-1", Result: "PASSED"}, {ID: "item-2", Result: "FAILED", Note: "TypeError in the console", AttachmentMessageIDs: []string{"msg-1"}}}
	if _, err := fx.resolveChecklist("wrong-outcome", "approved", failed); !errors.Is(err, runtime.ErrChecklistOutcome) {
		t.Fatalf("approved with a failed item: %v", err)
	}
	if _, err := fx.resolveChecklist("no-note", "rejected", []runtime.ChecklistItemResult{{ID: "item-1", Result: "PASSED"}, {ID: "item-2", Result: "FAILED"}}); !errors.Is(err, runtime.ErrChecklistInvalid) {
		t.Fatalf("a failed item without a note: %v", err)
	}
	if _, err := fx.resolveChecklist("unknown", "approved", []runtime.ChecklistItemResult{{ID: "item-1", Result: "PASSED"}, {ID: "item-2", Result: "PASSED"}, {ID: "item-9", Result: "PASSED"}}); !errors.Is(err, runtime.ErrChecklistInvalid) {
		t.Fatalf("an unknown item: %v", err)
	}
	if fx.request(t).State != "PENDING" {
		t.Fatal("a refused checklist must leave the gate PENDING")
	}

	result, err := fx.resolveChecklist("ok", "rejected", failed)
	if err != nil || !result.Won || result.NextNodeKey != "end_rejected" {
		t.Fatalf("resolve = %+v, %v", result, err)
	}
	detail, err = runtime.GetRunDetail(fx.ctx, fx.uow, fx.runID)
	if err != nil {
		t.Fatal(err)
	}
	qa := detail.ApprovalRequests[0].Checklist
	if qa == nil || qa.Verdict != "FAILED" || qa.DecidedBy == "" || len(qa.Items) != 2 || qa.Items[1].Result != "FAILED" ||
		qa.Items[1].Note != "TypeError in the console" || qa.Items[1].AttachmentMessageIDs[0] != "msg-1" || qa.Items[1].Text != "No console error" {
		t.Fatalf("decided checklist = %+v", qa)
	}
}

func TestResolveApproval_APlainApprovalRefusesAChecklist(t *testing.T) {
	fx := newReleaseFixture(t, nil)
	_, err := fx.resolveChecklist("k", "approved", []runtime.ChecklistItemResult{{ID: "item-1", Result: "PASSED"}})
	if !errors.Is(err, runtime.ErrChecklistNotOffered) {
		t.Fatalf("err = %v", err)
	}
	detail, _ := runtime.GetRunDetail(fx.ctx, fx.uow, fx.runID)
	if detail.ApprovalRequests[0].Checklist != nil {
		t.Fatal("a plain approval must not carry a checklist")
	}
}
