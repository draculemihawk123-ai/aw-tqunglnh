package gate_test

import (
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
)

func validDocument() gate.GateDocument {
	return gate.GateDocument{
		CommandRef: definition.DependencyPin{
			Kind: definition.KindCommand, DefinitionID: "command-1", VersionID: "command-1-v1",
		},
		Criteria: []gate.Criterion{
			{Name: "exit-code-zero", EvidenceKey: "exitCode"},
			{Name: "no-stderr", EvidenceKey: "stderr"},
		},
		PolicyRefs: []definition.DependencyPin{
			{Kind: definition.KindPolicy, DefinitionID: "policy-1", VersionID: "policy-1-v1"},
		},
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

func TestValidateDocument_ValidDocumentHasNoProblems(t *testing.T) {
	diags := gate.ValidateDocument(validDocument())
	if diags.HasProblems() {
		t.Fatalf("ValidateDocument(valid) = %v, want no problems", diags)
	}
}

func TestValidateDocument_RejectsMissingCommandRefIDs(t *testing.T) {
	doc := validDocument()
	doc.CommandRef = definition.DependencyPin{Kind: definition.KindCommand}
	diags := gate.ValidateDocument(doc)
	requireProblemPath(t, diags, "commandRef.definitionId")
	requireProblemPath(t, diags, "commandRef.versionId")
}

func TestValidateDocument_RejectsNonCommandRefKind(t *testing.T) {
	for _, kind := range []definition.Kind{definition.KindSkill, definition.KindLayer, definition.KindGate, definition.KindBlock} {
		t.Run(string(kind), func(t *testing.T) {
			doc := validDocument()
			doc.CommandRef = definition.DependencyPin{Kind: kind, DefinitionID: "x", VersionID: "x-v1"}
			diags := gate.ValidateDocument(doc)
			requireProblemPath(t, diags, "commandRef.kind")
		})
	}
}

// TestValidateDocument_RejectsMissingEvidenceMapping is V2-05's own
// "missing evidence mapping" Verify requirement.
func TestValidateDocument_RejectsMissingEvidenceMapping(t *testing.T) {
	doc := validDocument()
	doc.Criteria = nil
	diags := gate.ValidateDocument(doc)
	requireProblemPath(t, diags, "criteria")
}

func TestValidateDocument_RejectsEmptyEvidenceKey(t *testing.T) {
	doc := validDocument()
	doc.Criteria = []gate.Criterion{{Name: "check", EvidenceKey: ""}}
	diags := gate.ValidateDocument(doc)
	requireProblemPath(t, diags, "criteria[0].evidenceKey")
}

func TestValidateDocument_RejectsDuplicateCriterionName(t *testing.T) {
	doc := validDocument()
	doc.Criteria = []gate.Criterion{
		{Name: "same", EvidenceKey: "a"},
		{Name: "same", EvidenceKey: "b"},
	}
	diags := gate.ValidateDocument(doc)
	requireProblemPath(t, diags, "criteria[1].name")
}

func TestValidateDocument_RejectsDuplicateEvidenceKey(t *testing.T) {
	doc := validDocument()
	doc.Criteria = []gate.Criterion{
		{Name: "a", EvidenceKey: "same"},
		{Name: "b", EvidenceKey: "same"},
	}
	diags := gate.ValidateDocument(doc)
	requireProblemPath(t, diags, "criteria[1].evidenceKey")
}

func TestValidateDocument_RejectsPolicyRefWithWrongKind(t *testing.T) {
	doc := validDocument()
	doc.PolicyRefs = []definition.DependencyPin{
		{Kind: definition.KindCommand, DefinitionID: "not-a-policy", VersionID: "v1"},
	}
	diags := gate.ValidateDocument(doc)
	requireProblemPath(t, diags, "policyRefs[0].kind")
}
