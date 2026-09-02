package agentprofile_test

import (
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/domain/agentprofile"
	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

func validDocument() agentprofile.AgentProfileDocument {
	return agentprofile.AgentProfileDocument{
		ProviderKey: "claude",
		Model:       "claude-opus-4",
		ToolRefs:    []string{"read_file", "write_file"},
		ContextPolicyRef: definition.DependencyPin{
			Kind:         definition.KindPolicy,
			DefinitionID: "policy-context-1",
			VersionID:    "policy-context-1-v1",
		},
		Compatibility: agentprofile.Compatibility{
			OS:        []string{"windows", "linux"},
			Toolchain: []string{"git"},
		},
		RequiredCapabilities: []string{"INTEGRATION_MULTI_REPOSITORY_WRITE"},
		Budget:               agentprofile.Budget{MaxTokens: 100000},
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
	diags := agentprofile.ValidateDocument(validDocument())
	if diags.HasProblems() {
		t.Fatalf("ValidateDocument(valid) = %v, want no problems", diags)
	}
}

func TestValidateDocument_RejectsEmptyProviderKey(t *testing.T) {
	doc := validDocument()
	doc.ProviderKey = ""
	diags := agentprofile.ValidateDocument(doc)
	requireProblemPath(t, diags, "providerKey")
}

func TestValidateDocument_RejectsEmptyModel(t *testing.T) {
	doc := validDocument()
	doc.Model = ""
	diags := agentprofile.ValidateDocument(doc)
	requireProblemPath(t, diags, "model")
}

func TestValidateDocument_RejectsEmptyToolRef(t *testing.T) {
	doc := validDocument()
	doc.ToolRefs = []string{""}
	diags := agentprofile.ValidateDocument(doc)
	requireProblemPath(t, diags, "toolRefs[0]")
}

func TestValidateDocument_RejectsDuplicateToolRef(t *testing.T) {
	doc := validDocument()
	doc.ToolRefs = []string{"read_file", "read_file"}
	diags := agentprofile.ValidateDocument(doc)
	requireProblemPath(t, diags, "toolRefs[1]")
}

// TestValidateDocument_RejectsContextPolicyRefMissingIDs and
// TestValidateDocument_RejectsContextPolicyRefWrongKind are V2-07's own
// "policy dependency" test case.
func TestValidateDocument_RejectsContextPolicyRefMissingIDs(t *testing.T) {
	doc := validDocument()
	doc.ContextPolicyRef = definition.DependencyPin{Kind: definition.KindPolicy}
	diags := agentprofile.ValidateDocument(doc)
	requireProblemPath(t, diags, "contextPolicyRef.definitionId")
	requireProblemPath(t, diags, "contextPolicyRef.versionId")
}

func TestValidateDocument_RejectsContextPolicyRefWrongKind(t *testing.T) {
	doc := validDocument()
	doc.ContextPolicyRef = definition.DependencyPin{
		Kind: definition.KindCommand, DefinitionID: "x", VersionID: "x-v1",
	}
	diags := agentprofile.ValidateDocument(doc)
	requireProblemPath(t, diags, "contextPolicyRef.kind")
}

// TestValidateDocument_RejectsNoOS and
// TestValidateDocument_RejectsUnsupportedOS are V2-07's own "OS
// mismatch" test case.
func TestValidateDocument_RejectsNoOS(t *testing.T) {
	doc := validDocument()
	doc.Compatibility.OS = nil
	diags := agentprofile.ValidateDocument(doc)
	requireProblemPath(t, diags, "compatibility.os")
}

func TestValidateDocument_RejectsUnsupportedOS(t *testing.T) {
	doc := validDocument()
	doc.Compatibility.OS = []string{"darwin"}
	diags := agentprofile.ValidateDocument(doc)
	requireProblemPath(t, diags, "compatibility.os[0]")
}

func TestValidateDocument_RejectsDuplicateOS(t *testing.T) {
	doc := validDocument()
	doc.Compatibility.OS = []string{"windows", "windows"}
	diags := agentprofile.ValidateDocument(doc)
	requireProblemPath(t, diags, "compatibility.os[1]")
}

func TestValidateDocument_RejectsEmptyToolchainEntry(t *testing.T) {
	doc := validDocument()
	doc.Compatibility.Toolchain = []string{""}
	diags := agentprofile.ValidateDocument(doc)
	requireProblemPath(t, diags, "compatibility.toolchain[0]")
}

// TestValidateDocument_RejectsEmptyRequiredCapability is V2-07's own
// "missing capability" test case.
func TestValidateDocument_RejectsEmptyRequiredCapability(t *testing.T) {
	doc := validDocument()
	doc.RequiredCapabilities = []string{""}
	diags := agentprofile.ValidateDocument(doc)
	requireProblemPath(t, diags, "requiredCapabilities[0]")
}

func TestValidateDocument_RejectsDuplicateRequiredCapability(t *testing.T) {
	doc := validDocument()
	doc.RequiredCapabilities = []string{"X", "X"}
	diags := agentprofile.ValidateDocument(doc)
	requireProblemPath(t, diags, "requiredCapabilities[1]")
}

// TestValidateDocument_RejectsZeroBudget is V2-07's own "invalid budget"
// test case.
func TestValidateDocument_RejectsZeroBudget(t *testing.T) {
	doc := validDocument()
	doc.Budget.MaxTokens = 0
	diags := agentprofile.ValidateDocument(doc)
	requireProblemPath(t, diags, "budget.maxTokens")
}
