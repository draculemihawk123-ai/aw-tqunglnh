package engineeringpack_test

import (
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/engineeringpack"
)

func validDocument() engineeringpack.EngineeringPackDocument {
	return engineeringpack.EngineeringPackDocument{
		Dependencies: []definition.DependencyPin{
			{Kind: definition.KindSkill, DefinitionID: "skill-1", VersionID: "skill-1-v1"},
			{Kind: definition.KindLayer, DefinitionID: "layer-1", VersionID: "layer-1-v1"},
		},
	}
}

func TestValidateDocument_ValidDocumentHasNoProblems(t *testing.T) {
	diags := engineeringpack.ValidateDocument(validDocument())
	if diags.HasProblems() {
		t.Fatalf("ValidateDocument(valid) = %v, want no problems", diags)
	}
}

func TestValidateDocument_RejectsNoDependencies(t *testing.T) {
	diags := engineeringpack.ValidateDocument(engineeringpack.EngineeringPackDocument{})
	requireProblemPath(t, diags, "dependencies")
}

func TestValidateDocument_RejectsMissingDefinitionID(t *testing.T) {
	doc := validDocument()
	doc.Dependencies[0].DefinitionID = ""
	diags := engineeringpack.ValidateDocument(doc)
	requireProblemPath(t, diags, "dependencies[0].definitionId")
}

func TestValidateDocument_RejectsMissingVersionID(t *testing.T) {
	doc := validDocument()
	doc.Dependencies[0].VersionID = ""
	diags := engineeringpack.ValidateDocument(doc)
	requireProblemPath(t, diags, "dependencies[0].versionId")
}

func TestValidateDocument_RejectsNonComposableKind(t *testing.T) {
	for _, kind := range []definition.Kind{
		definition.KindCommand, definition.KindGate, definition.KindAgentProfile,
		definition.KindPolicy, definition.KindBlock, definition.KindWorkflow,
	} {
		t.Run(string(kind), func(t *testing.T) {
			doc := validDocument()
			doc.Dependencies[0].Kind = kind
			diags := engineeringpack.ValidateDocument(doc)
			requireProblemPath(t, diags, "dependencies[0].kind")
		})
	}
}

func TestValidateDocument_AcceptsEveryComposableKind(t *testing.T) {
	for _, kind := range []definition.Kind{
		definition.KindSkill, definition.KindLayer, definition.KindEngineeringPack,
	} {
		t.Run(string(kind), func(t *testing.T) {
			doc := validDocument()
			doc.Dependencies[0].Kind = kind
			diags := engineeringpack.ValidateDocument(doc)
			for _, d := range diags {
				if d.Path == "dependencies[0].kind" {
					t.Fatalf("ValidateDocument should accept kind=%s, got %v", kind, d)
				}
			}
		})
	}
}

func TestValidateDocument_RejectsDuplicateDefinitionDependency(t *testing.T) {
	doc := validDocument()
	doc.Dependencies = append(doc.Dependencies, definition.DependencyPin{
		Kind: definition.KindSkill, DefinitionID: "skill-1", VersionID: "skill-1-v2",
	})
	diags := engineeringpack.ValidateDocument(doc)
	requireProblemPath(t, diags, "dependencies[2]")
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
