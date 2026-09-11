package policy_test

// This file is V5-11's own schema-foundation test suite (2026-09-10):
// the V2 required-assurance ladder (AssuranceLevel/AssuranceRequirement/
// ApprovalRequirement) extending CompletionRules per the user's own
// binding contract — see baocaov5checklist.md's "V5-11" section for the
// full narrative.

import (
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
)

func validAssuranceLadderDocument() policy.PolicyDocument {
	return policy.PolicyDocument{
		Category: policy.CategoryCompletion,
		Completion: &policy.CompletionRules{
			RequiredAssurance: []policy.AssuranceRequirement{
				{Level: policy.AssuranceStatic, RequiredEvidenceKinds: []string{"LINT_RESULT"}},
				{
					Level:                 policy.AssuranceUnit,
					RequiredEvidenceKinds: []string{"UNIT_TEST_RESULT"},
					RequiredApprovals:     []policy.ApprovalRequirement{{AuthorizedRoles: []string{"tech-lead"}}},
				},
				{Level: policy.AssuranceHuman, RequiredApprovals: []policy.ApprovalRequirement{{AuthorizedRoles: []string{"product-owner"}}}},
			},
		},
	}
}

func TestValidateDocument_AssuranceLadder_ValidDocumentHasNoProblems(t *testing.T) {
	diags := policy.ValidateDocument(validAssuranceLadderDocument())
	if diags.HasProblems() {
		t.Fatalf("diags = %v, want none", diags)
	}
}

func TestValidateDocument_AssuranceLadder_RejectsBothV1AndV2(t *testing.T) {
	doc := validAssuranceLadderDocument()
	doc.Completion.RequiredEvidenceKinds = []string{"TEST_RESULT"}
	diags := policy.ValidateDocument(doc)
	requireProblemPath(t, diags, "completion")
}

func TestValidateDocument_AssuranceLadder_RejectsUnknownLevel(t *testing.T) {
	doc := validAssuranceLadderDocument()
	doc.Completion.RequiredAssurance[0].Level = "NOT_A_REAL_LEVEL"
	diags := policy.ValidateDocument(doc)
	requireProblemPath(t, diags, "completion.requiredAssurance[0].level")
}

func TestValidateDocument_AssuranceLadder_RejectsDuplicateLevel(t *testing.T) {
	doc := validAssuranceLadderDocument()
	doc.Completion.RequiredAssurance[1].Level = policy.AssuranceStatic
	diags := policy.ValidateDocument(doc)
	requireProblemPath(t, diags, "completion.requiredAssurance[1].level")
}

func TestValidateDocument_AssuranceLadder_RejectsEmptyRequirement(t *testing.T) {
	doc := validAssuranceLadderDocument()
	doc.Completion.RequiredAssurance = []policy.AssuranceRequirement{{Level: policy.AssuranceStatic}}
	diags := policy.ValidateDocument(doc)
	requireProblemPath(t, diags, "completion.requiredAssurance[0]")
}

func TestValidateDocument_AssuranceLadder_RejectsDuplicateEvidenceKindWithinLevel(t *testing.T) {
	doc := validAssuranceLadderDocument()
	doc.Completion.RequiredAssurance[0].RequiredEvidenceKinds = []string{"LINT_RESULT", "LINT_RESULT"}
	diags := policy.ValidateDocument(doc)
	requireProblemPath(t, diags, "completion.requiredAssurance[0].requiredEvidenceKinds[1]")
}

func TestValidateDocument_AssuranceLadder_RejectsEmptyEvidenceKind(t *testing.T) {
	doc := validAssuranceLadderDocument()
	doc.Completion.RequiredAssurance[0].RequiredEvidenceKinds = []string{" "}
	diags := policy.ValidateDocument(doc)
	requireProblemPath(t, diags, "completion.requiredAssurance[0].requiredEvidenceKinds[0]")
}

func TestValidateDocument_AssuranceLadder_RejectsApprovalWithNoAuthorizedRoles(t *testing.T) {
	doc := validAssuranceLadderDocument()
	doc.Completion.RequiredAssurance[1].RequiredApprovals = []policy.ApprovalRequirement{{}}
	diags := policy.ValidateDocument(doc)
	requireProblemPath(t, diags, "completion.requiredAssurance[1].requiredApprovals[0].authorizedRoles")
}

func TestValidateDocument_AssuranceLadder_RejectsDuplicateAuthorizedRole(t *testing.T) {
	doc := validAssuranceLadderDocument()
	doc.Completion.RequiredAssurance[1].RequiredApprovals = []policy.ApprovalRequirement{
		{AuthorizedRoles: []string{"tech-lead", "tech-lead"}},
	}
	diags := policy.ValidateDocument(doc)
	requireProblemPath(t, diags, "completion.requiredAssurance[1].requiredApprovals[0].authorizedRoles[1]")
}

func TestValidateDocument_AssuranceLadder_RejectsDuplicateApprovalRequirement(t *testing.T) {
	doc := validAssuranceLadderDocument()
	doc.Completion.RequiredAssurance[1].RequiredApprovals = []policy.ApprovalRequirement{
		{AuthorizedRoles: []string{"tech-lead"}},
		{AuthorizedRoles: []string{"tech-lead"}},
	}
	diags := policy.ValidateDocument(doc)
	requireProblemPath(t, diags, "completion.requiredAssurance[1].requiredApprovals[1]")
}

// TestCompile_AssuranceLadder_SetOrderIndependent proves the ladder
// itself, each level's own requiredEvidenceKinds, each level's own
// requiredApprovals, and each approval's own authorizedRoles all
// canonicalize identically regardless of authored array order — matching
// 2026-09-10's own "Thứ tự level do domain code định nghĩa; không tin
// thứ tự mảng từ JSON" decision.
func TestCompile_AssuranceLadder_SetOrderIndependent(t *testing.T) {
	first := validPublishRequest()
	first.Document = policy.PolicyDocument{
		Category: policy.CategoryCompletion,
		Completion: &policy.CompletionRules{
			RequiredAssurance: []policy.AssuranceRequirement{
				{
					Level:                 policy.AssuranceUnit,
					RequiredEvidenceKinds: []string{"UNIT_TEST_RESULT", "COVERAGE_RESULT"},
					RequiredApprovals: []policy.ApprovalRequirement{
						{AuthorizedRoles: []string{"tech-lead", "qa-lead"}},
					},
				},
				{Level: policy.AssuranceStatic, RequiredEvidenceKinds: []string{"LINT_RESULT"}},
			},
		},
	}

	second := validPublishRequest()
	second.Document = policy.PolicyDocument{
		Category: policy.CategoryCompletion,
		Completion: &policy.CompletionRules{
			RequiredAssurance: []policy.AssuranceRequirement{
				{Level: policy.AssuranceStatic, RequiredEvidenceKinds: []string{"LINT_RESULT"}},
				{
					Level:                 policy.AssuranceUnit,
					RequiredEvidenceKinds: []string{"COVERAGE_RESULT", "UNIT_TEST_RESULT"},
					RequiredApprovals: []policy.ApprovalRequirement{
						{AuthorizedRoles: []string{"qa-lead", "tech-lead"}},
					},
				},
			},
		},
	}

	fieldsFirst, err := policy.Compile(draftDefinition("policy-1"), first)
	if err != nil {
		t.Fatalf("Compile(first): %v", err)
	}
	fieldsSecond, err := policy.Compile(draftDefinition("policy-1"), second)
	if err != nil {
		t.Fatalf("Compile(second): %v", err)
	}
	if fieldsFirst.SourceHash() != fieldsSecond.SourceHash() {
		t.Fatalf("SourceHash differs for the same ladder content in different set order: %s vs %s",
			fieldsFirst.SourceHash(), fieldsSecond.SourceHash())
	}
}
