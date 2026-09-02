package policy_test

import (
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
)

func validAttemptDocument() policy.PolicyDocument {
	return policy.PolicyDocument{
		Category: policy.CategoryAttempt,
		Attempt: &policy.AttemptRules{
			MaxAttempts:         3,
			RetryableErrorCodes: []string{"UNAVAILABLE", "TIMEOUT"},
			BackoffSeconds:      5,
			TimeoutSeconds:      60,
		},
	}
}

func validCompletionDocument() policy.PolicyDocument {
	return policy.PolicyDocument{
		Category: policy.CategoryCompletion,
		Completion: &policy.CompletionRules{
			RequiredEvidenceKinds: []string{"TEST_RESULT", "CODE_REVIEW"},
		},
	}
}

func validPermissionDocument() policy.PolicyDocument {
	return policy.PolicyDocument{
		Category: policy.CategoryPermission,
		Permission: &policy.PermissionRules{
			IsolationTier:       policy.IsolationTierEnforcedIsolated,
			GrantedCapabilities: []string{"INTEGRATION_MULTI_REPOSITORY_WRITE"},
		},
	}
}

func validContextDocument() policy.PolicyDocument {
	return policy.PolicyDocument{
		Category: policy.CategoryContext,
		Context: &policy.ContextRules{
			Selector: []string{"skills/*", "layers/*"},
			Order:    []string{"layers/base", "skills/lint"},
			Budget:   policy.ContextBudget{MaxTokens: 8000},
			ResourceRefs: []policy.ResourceRef{
				{OwnerVersionID: "skill-1-v1", ResourceKey: "lint.md", ContentHash: "sha256:abc"},
			},
		},
	}
}

func validCleanupDocument() policy.PolicyDocument {
	return policy.PolicyDocument{
		Category: policy.CategoryCleanup,
		Cleanup:  &policy.CleanupRules{RetentionDays: 7},
	}
}

func requireProblemPath(t *testing.T, diags authoring.Diagnostics, path string) {
	t.Helper()
	for _, d := range diags {
		if d.Path == path {
			return
		}
	}
	t.Fatalf("diags = %v, want a problem at path %q", diags, path)
}

func TestValidateDocument_ValidDocumentsHaveNoProblems(t *testing.T) {
	docs := map[string]policy.PolicyDocument{
		"attempt":    validAttemptDocument(),
		"completion": validCompletionDocument(),
		"permission": validPermissionDocument(),
		"context":    validContextDocument(),
		"cleanup":    validCleanupDocument(),
	}
	for name, doc := range docs {
		t.Run(name, func(t *testing.T) {
			diags := policy.ValidateDocument(doc)
			if diags.HasProblems() {
				t.Fatalf("ValidateDocument(%s) = %v, want no problems", name, diags)
			}
		})
	}
}

func TestValidateDocument_RejectsUnknownCategory(t *testing.T) {
	doc := policy.PolicyDocument{Category: "NOT_A_CATEGORY"}
	diags := policy.ValidateDocument(doc)
	requireProblemPath(t, diags, "category")
}

func TestValidateDocument_RejectsMissingRulesForDeclaredCategory(t *testing.T) {
	doc := policy.PolicyDocument{Category: policy.CategoryAttempt}
	diags := policy.ValidateDocument(doc)
	requireProblemPath(t, diags, "attempt")
}

// TestValidateDocument_RejectsRulesFromAnotherCategory is this package's
// own "Chỉ semantics tương ứng" bar: a Policy declaring ATTEMPT must not
// also carry COMPLETION rules.
func TestValidateDocument_RejectsRulesFromAnotherCategory(t *testing.T) {
	doc := validAttemptDocument()
	doc.Completion = &policy.CompletionRules{RequiredEvidenceKinds: []string{"X"}}
	diags := policy.ValidateDocument(doc)
	requireProblemPath(t, diags, "completion")
}

func TestValidateDocument_Attempt_RejectsZeroMaxAttempts(t *testing.T) {
	doc := validAttemptDocument()
	doc.Attempt.MaxAttempts = 0
	diags := policy.ValidateDocument(doc)
	requireProblemPath(t, diags, "attempt.maxAttempts")
}

func TestValidateDocument_Attempt_RejectsZeroTimeout(t *testing.T) {
	doc := validAttemptDocument()
	doc.Attempt.TimeoutSeconds = 0
	diags := policy.ValidateDocument(doc)
	requireProblemPath(t, diags, "attempt.timeoutSeconds")
}

func TestValidateDocument_Attempt_RejectsZeroBackoff(t *testing.T) {
	doc := validAttemptDocument()
	doc.Attempt.BackoffSeconds = 0
	diags := policy.ValidateDocument(doc)
	requireProblemPath(t, diags, "attempt.backoffSeconds")
}

func TestValidateDocument_Attempt_RejectsUnknownErrorCode(t *testing.T) {
	doc := validAttemptDocument()
	doc.Attempt.RetryableErrorCodes = []string{"NOT_A_REAL_CODE"}
	diags := policy.ValidateDocument(doc)
	requireProblemPath(t, diags, "attempt.retryableErrorCodes[0]")
}

// TestValidateDocument_Attempt_RejectsNonRetryableCode is a real, cited
// rule (go-core-spec.md §14/§18): ISOLATION_ENFORCEMENT_UNAVAILABLE must
// never be treated as retryable.
func TestValidateDocument_Attempt_RejectsNonRetryableCode(t *testing.T) {
	doc := validAttemptDocument()
	doc.Attempt.RetryableErrorCodes = []string{"ISOLATION_ENFORCEMENT_UNAVAILABLE"}
	diags := policy.ValidateDocument(doc)
	requireProblemPath(t, diags, "attempt.retryableErrorCodes[0]")
}

func TestValidateDocument_Attempt_RejectsDuplicateErrorCode(t *testing.T) {
	doc := validAttemptDocument()
	doc.Attempt.RetryableErrorCodes = []string{"TIMEOUT", "TIMEOUT"}
	diags := policy.ValidateDocument(doc)
	requireProblemPath(t, diags, "attempt.retryableErrorCodes[1]")
}

func TestValidateDocument_Completion_RejectsNoRequiredEvidenceKinds(t *testing.T) {
	doc := validCompletionDocument()
	doc.Completion.RequiredEvidenceKinds = nil
	diags := policy.ValidateDocument(doc)
	requireProblemPath(t, diags, "completion.requiredEvidenceKinds")
}

func TestValidateDocument_Permission_RejectsUnsupportedIsolationTier(t *testing.T) {
	doc := validPermissionDocument()
	doc.Permission.IsolationTier = "SOMETHING_ELSE"
	diags := policy.ValidateDocument(doc)
	requireProblemPath(t, diags, "permission.isolationTier")
}

func TestValidateDocument_Context_RejectsNoSelector(t *testing.T) {
	doc := validContextDocument()
	doc.Context.Selector = nil
	diags := policy.ValidateDocument(doc)
	requireProblemPath(t, diags, "context.selector")
}

func TestValidateDocument_Context_RejectsZeroMaxTokens(t *testing.T) {
	doc := validContextDocument()
	doc.Context.Budget.MaxTokens = 0
	diags := policy.ValidateDocument(doc)
	requireProblemPath(t, diags, "context.budget.maxTokens")
}

func TestValidateDocument_Context_RejectsDuplicateOrderEntry(t *testing.T) {
	doc := validContextDocument()
	doc.Context.Order = []string{"a", "a"}
	diags := policy.ValidateDocument(doc)
	requireProblemPath(t, diags, "context.order[1]")
}

func TestValidateDocument_Context_RejectsIncompleteResourceRef(t *testing.T) {
	doc := validContextDocument()
	doc.Context.ResourceRefs = []policy.ResourceRef{{OwnerVersionID: "v1"}}
	diags := policy.ValidateDocument(doc)
	requireProblemPath(t, diags, "context.resourceRefs[0]")
}

func TestValidateDocument_Cleanup_RejectsZeroRetentionDays(t *testing.T) {
	doc := validCleanupDocument()
	doc.Cleanup.RetentionDays = 0
	diags := policy.ValidateDocument(doc)
	requireProblemPath(t, diags, "cleanup.retentionDays")
}
