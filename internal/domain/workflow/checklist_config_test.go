package workflow

import (
	"strings"
	"testing"
)

func checklistTestDocument(cfg *ApprovalChecklistConfig) WorkflowDocument {
	return WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []Node{
			{Key: "start", Type: NodeStart, Outcomes: []string{"next"}},
			{Key: "qa", Type: NodeApproval, Outcomes: []string{"passed", "failed"}, Approval: &ApprovalNodeConfig{
				AuthorizedRoles: []string{"operator"}, TimeoutSeconds: 60, EscalationOutcome: "failed", Checklist: cfg,
			}},
			{Key: "end", Type: NodeEnd},
		},
		Edges: []Edge{
			{Key: "s", From: "start", Outcome: "next", To: "qa"},
			{Key: "p", From: "qa", Outcome: "passed", To: "end"},
			{Key: "f", From: "qa", Outcome: "failed", To: "end"},
		},
	}
}

func TestValidate_AChecklistNamesDistinctDeclaredOutcomesAndHasItems(t *testing.T) {
	if err := ValidateDocument(checklistTestDocument(&ApprovalChecklistConfig{FromAcceptanceCriteria: true, PassedOutcome: "passed", FailedOutcome: "failed"})); err != nil {
		t.Fatalf("a valid checklist: %v", err)
	}
	for name, cfg := range map[string]*ApprovalChecklistConfig{
		"must differ":         {Items: []string{"x"}, PassedOutcome: "passed", FailedOutcome: "passed"},
		"needs items":         {PassedOutcome: "passed", FailedOutcome: "failed"},
		"is not declared":     {Items: []string{"x"}, PassedOutcome: "passed", FailedOutcome: "nope"},
		"must declare":        {Items: []string{"x"}, FailedOutcome: "failed"},
		"duplicate item":      {Items: []string{"x", "x"}, PassedOutcome: "passed", FailedOutcome: "failed"},
		"has an invalid item": {Items: []string{" x"}, PassedOutcome: "passed", FailedOutcome: "failed"},
	} {
		err := ValidateDocument(checklistTestDocument(cfg))
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
