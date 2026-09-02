package command_test

import (
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

func validDocument() command.CommandDocument {
	return command.CommandDocument{
		Executable: command.ExecutableRef{
			OwnerVersionID: "skill-1-v1",
			ResourceKey:    "scripts/run.sh",
			ContentHash:    "sha256:abc",
		},
		Argv: []command.ArgvElement{
			{Kind: command.ArgvLiteral, Value: "run.sh"},
			{Kind: command.ArgvPlaceholder, Value: "TARGET"},
		},
		PlaceholderAllowlist: []string{"TARGET"},
		CwdRepositoryTarget:  "primary",
		Compatibility:        command.Compatibility{OS: []string{"linux"}},
		EnvAllowlist:         []string{"PATH"},
		NetworkAccess:        command.NetworkAccessNone,
		SecretRefs:           []string{"deploy-token"},
		TimeoutSeconds:       60,
		Output: command.OutputContract{
			CaptureStdout:  true,
			CaptureStderr:  true,
			MaxOutputBytes: 1 << 20,
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
	diags := command.ValidateDocument(validDocument())
	if diags.HasProblems() {
		t.Fatalf("ValidateDocument(valid) = %v, want no problems", diags)
	}
}

func TestValidateDocument_RejectsMissingExecutableFields(t *testing.T) {
	doc := validDocument()
	doc.Executable = command.ExecutableRef{}
	diags := command.ValidateDocument(doc)
	requireProblemPath(t, diags, "executable.ownerVersionId")
	requireProblemPath(t, diags, "executable.resourceKey")
	requireProblemPath(t, diags, "executable.contentHash")
}

func TestValidateDocument_RejectsEmptyArgv(t *testing.T) {
	doc := validDocument()
	doc.Argv = nil
	diags := command.ValidateDocument(doc)
	requireProblemPath(t, diags, "argv")
}

// TestValidateDocument_RejectsShellStringArgvElement is V2-05's own
// "reject shell string" requirement: an argv element kind outside the
// closed LITERAL/PLACEHOLDER set (e.g. a would-be "SHELL" kind) must be
// rejected — argv is never a free-form shell string.
func TestValidateDocument_RejectsUnsupportedArgvElementKind(t *testing.T) {
	doc := validDocument()
	doc.Argv = []command.ArgvElement{{Kind: "SHELL", Value: "rm -rf /"}}
	diags := command.ValidateDocument(doc)
	requireProblemPath(t, diags, "argv[0].kind")
}

func TestValidateDocument_RejectsUnknownPlaceholder(t *testing.T) {
	doc := validDocument()
	doc.Argv = append(doc.Argv, command.ArgvElement{Kind: command.ArgvPlaceholder, Value: "NOT_DECLARED"})
	diags := command.ValidateDocument(doc)
	requireProblemPath(t, diags, "argv[2].value")
}

func TestValidateDocument_AcceptsPlaceholderInAllowlist(t *testing.T) {
	diags := command.ValidateDocument(validDocument())
	for _, d := range diags {
		if d.Path == "argv[1].value" {
			t.Fatalf("ValidateDocument should accept a declared placeholder, got %v", d)
		}
	}
}

func TestValidateDocument_RejectsEmptyCwdRepositoryTarget(t *testing.T) {
	doc := validDocument()
	doc.CwdRepositoryTarget = ""
	diags := command.ValidateDocument(doc)
	requireProblemPath(t, diags, "cwdRepositoryTarget")
}

func TestValidateDocument_RejectsNoOSCompatibility(t *testing.T) {
	doc := validDocument()
	doc.Compatibility = command.Compatibility{}
	diags := command.ValidateDocument(doc)
	requireProblemPath(t, diags, "compatibility.os")
}

func TestValidateDocument_RejectsInvalidNetworkAccess(t *testing.T) {
	doc := validDocument()
	doc.NetworkAccess = "MAYBE"
	diags := command.ValidateDocument(doc)
	requireProblemPath(t, diags, "networkAccess")
}

func TestValidateDocument_RejectsDuplicateSecretRef(t *testing.T) {
	doc := validDocument()
	doc.SecretRefs = []string{"x", "x"}
	diags := command.ValidateDocument(doc)
	requireProblemPath(t, diags, "secretRefs[1]")
}

func TestValidateDocument_RejectsZeroTimeout(t *testing.T) {
	doc := validDocument()
	doc.TimeoutSeconds = 0
	diags := command.ValidateDocument(doc)
	requireProblemPath(t, diags, "timeoutSeconds")
}

func TestValidateDocument_RejectsZeroMaxOutputBytes(t *testing.T) {
	doc := validDocument()
	doc.Output.MaxOutputBytes = 0
	diags := command.ValidateDocument(doc)
	requireProblemPath(t, diags, "output.maxOutputBytes")
}

func TestValidateDocument_RejectsPolicyRefWithWrongKind(t *testing.T) {
	doc := validDocument()
	doc.PolicyRefs = []definition.DependencyPin{
		{Kind: definition.KindCommand, DefinitionID: "not-a-policy", VersionID: "v1"},
	}
	diags := command.ValidateDocument(doc)
	requireProblemPath(t, diags, "policyRefs[0].kind")
}
