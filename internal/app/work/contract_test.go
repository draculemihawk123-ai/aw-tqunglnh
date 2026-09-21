package work_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/work"
)

// TestWorkItemContractRequest_Validate pins exactly which shapes Validate
// rejects (the ones that can never become a valid contract) and — just as
// important — which it deliberately accepts: absence of any field, including
// an acceptance criterion with no VerificationRef, is never a problem at
// creation (completeness is ValidateReadinessGate's question, asked later).
func TestWorkItemContractRequest_Validate(t *testing.T) {
	tests := []struct {
		name       string
		contract   *work.WorkItemContractRequest
		wantFields []string // nil = valid
	}{
		{name: "nil (no contract given)", contract: nil},
		{name: "empty contract", contract: &work.WorkItemContractRequest{}},
		{name: "partial contract", contract: &work.WorkItemContractRequest{Behavior: "only a behavior"}},
		{name: "full contract", contract: fullContractRequest("wf-v-1")},
		{name: "schemaVersion 0 means not given", contract: &work.WorkItemContractRequest{SchemaVersion: 0}},
		{
			name:     "descriptive-only criterion is legitimate",
			contract: &work.WorkItemContractRequest{AcceptanceCriteria: []work.AcceptanceCriterionRequest{{Description: "a human checks it"}}},
		},
		{name: "negative schemaVersion", contract: &work.WorkItemContractRequest{SchemaVersion: -1}, wantFields: []string{"schemaVersion"}},
		{
			name: "criterion with no description",
			contract: &work.WorkItemContractRequest{AcceptanceCriteria: []work.AcceptanceCriterionRequest{
				{Description: "ok"}, {Description: "  \t", VerificationRef: "go test"},
			}},
			wantFields: []string{"acceptanceCriteria[1].description"},
		},
		{name: "blank exclusion entry", contract: &work.WorkItemContractRequest{Exclusions: []string{"a", " ", "c"}}, wantFields: []string{"exclusions[1]"}},
		{name: "whitespace-only workflowVersionId", contract: &work.WorkItemContractRequest{WorkflowVersionID: "  "}, wantFields: []string{"workflowVersionId"}},
		{
			name: "every problem reported at once, in field order",
			contract: &work.WorkItemContractRequest{
				SchemaVersion: -3, AcceptanceCriteria: []work.AcceptanceCriterionRequest{{}}, Exclusions: []string{""}, WorkflowVersionID: " ",
			},
			wantFields: []string{"schemaVersion", "acceptanceCriteria[0].description", "exclusions[0]", "workflowVersionId"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.contract.Validate()
			if tc.wantFields == nil {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			var invalid *work.InvalidWorkItemContractError
			if !errors.As(err, &invalid) {
				t.Fatalf("Validate() = %v, want *InvalidWorkItemContractError", err)
			}
			got := make([]string, 0, len(invalid.Problems))
			for _, p := range invalid.Problems {
				if strings.TrimSpace(p.Message) == "" {
					t.Errorf("problem for %q has no message", p.Field)
				}
				got = append(got, p.Field)
			}
			if !reflect.DeepEqual(got, tc.wantFields) {
				t.Fatalf("problem fields = %v, want %v", got, tc.wantFields)
			}
			for _, field := range tc.wantFields {
				if !strings.Contains(err.Error(), field) {
					t.Errorf("Error() = %q, want it to name %q", err.Error(), field)
				}
			}
		})
	}
}
