package agentprofile_test

import (
	"strings"
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

// --- V9-05 (gap G5): AgentProfileDocument.EnvAllowlist -----------------

// TestValidateDocument_AcceptsEnvAllowlistNames: real variable names of both
// operating systems are valid, including ones with characters a naive
// "identifier" rule would refuse (Windows has ProgramFiles(x86); a name may
// start with an underscore; the case differs between PATH and Path and both
// are plain names here — matching against the worker's list is exact).
func TestValidateDocument_AcceptsEnvAllowlistNames(t *testing.T) {
	doc := validDocument()
	doc.EnvAllowlist = []string{"PATH", "HOME", "USERPROFILE", "SystemRoot", "ProgramFiles(x86)", "_AW_X", "a.b-c", "Path"}
	if diags := agentprofile.ValidateDocument(doc); diags.HasProblems() {
		t.Fatalf("ValidateDocument(valid env allowlist) = %v, want no problems", diags)
	}
}

// TestValidateDocument_AbsentOrEmptyEnvAllowlistIsValid: the field is
// optional, and an explicit empty list means "inherit nothing" — the same as
// leaving it out.
func TestValidateDocument_AbsentOrEmptyEnvAllowlistIsValid(t *testing.T) {
	for name, list := range map[string][]string{"nil": nil, "empty": {}} {
		doc := validDocument()
		doc.EnvAllowlist = list
		if diags := agentprofile.ValidateDocument(doc); diags.HasProblems() {
			t.Fatalf("%s env allowlist: ValidateDocument = %v, want no problems", name, diags)
		}
	}
}

// TestValidateDocument_RejectsInvalidEnvAllowlistNames: each rule on its own.
// A name is a name, never a NAME=value pair (the value would be a secret
// written into a definition), never padded (matching is exact), never empty
// or NUL (the process supervisor refuses those at spawn).
func TestValidateDocument_RejectsInvalidEnvAllowlistNames(t *testing.T) {
	tests := []struct {
		name  string
		entry string
	}{
		{"empty", ""},
		{"NUL", "PA\x00TH"},
		{"equals sign", "PATH=/usr/bin"},
		{"bare equals sign", "="},
		{"leading space", " PATH"},
		{"trailing space", "PATH "},
		{"inner space", "MY VAR"},
		{"tab", "MY\tVAR"},
		{"newline", "MY\nVAR"},
		{"only whitespace", "   "},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			doc := validDocument()
			doc.EnvAllowlist = []string{"HOME", test.entry}
			diags := agentprofile.ValidateDocument(doc)
			requireProblemPath(t, diags, "envAllowlist[1]")
			for _, d := range diags {
				if d.Path == "envAllowlist[0]" {
					t.Fatalf("the valid entry envAllowlist[0] was reported: %v", d)
				}
			}
		})
	}
}

// TestValidateDocument_EnvAllowlistReportsEveryBadEntry: collect-everything
// convention — three bad entries are three problems, not just the first.
func TestValidateDocument_EnvAllowlistReportsEveryBadEntry(t *testing.T) {
	doc := validDocument()
	doc.EnvAllowlist = []string{"", "A=b", "HOME", "HOME"}
	diags := agentprofile.ValidateDocument(doc)
	requireProblemPath(t, diags, "envAllowlist[0]")
	requireProblemPath(t, diags, "envAllowlist[1]")
	requireProblemPath(t, diags, "envAllowlist[3]")
}

func TestValidateDocument_RejectsDuplicateEnvAllowlistName(t *testing.T) {
	doc := validDocument()
	doc.EnvAllowlist = []string{"PATH", "HOME", "PATH"}
	diags := agentprofile.ValidateDocument(doc)
	requireProblemPath(t, diags, "envAllowlist[2]")
}

// TestValidateDocument_EnvAllowlistNamesDifferingOnlyByCaseAreNotDuplicates:
// matching is exact and case-sensitive (also on Windows, where the OS would
// treat them as one variable), so PATH and Path are two different names and
// the publish does not pretend otherwise.
func TestValidateDocument_EnvAllowlistNamesDifferingOnlyByCaseAreNotDuplicates(t *testing.T) {
	doc := validDocument()
	doc.EnvAllowlist = []string{"PATH", "Path"}
	if diags := agentprofile.ValidateDocument(doc); diags.HasProblems() {
		t.Fatalf("ValidateDocument = %v, want no problems", diags)
	}
}

// TestValidateDocument_EnvAllowlistDiagnosticNeverEchoesAValue: a rejected
// NAME=value entry names the problem without repeating what follows the "=".
func TestValidateDocument_EnvAllowlistDiagnosticNeverEchoesAValue(t *testing.T) {
	const secret = "sk-this-must-never-appear-in-a-diagnostic"
	doc := validDocument()
	doc.EnvAllowlist = []string{"API_KEY=" + secret}
	diags := agentprofile.ValidateDocument(doc)
	requireProblemPath(t, diags, "envAllowlist[0]")
	for _, d := range diags {
		for _, text := range []string{d.What, d.Why, d.Fix} {
			if strings.Contains(text, secret) {
				t.Fatalf("diagnostic %+v repeats the value that followed the \"=\"", d)
			}
		}
	}
}
