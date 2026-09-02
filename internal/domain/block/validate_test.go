package block_test

import (
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/block"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

func validDocument() block.BlockDocument {
	return block.BlockDocument{
		CompatibleNodeTypes:  []block.NodeType{block.NodeTypeAgent},
		RequiredCapabilities: []string{"INTEGRATION_MULTI_REPOSITORY_WRITE"},
		TimeoutSeconds:       60,
		ScopeSelector: block.ScopeSelector{
			Access:     block.ScopeAccessWrite,
			PathScopes: []string{"src/**"},
		},
		DoneCondition: "result.outcome",
		ExecutorRef: definition.DependencyPin{
			Kind:         definition.KindCommand,
			DefinitionID: "cmd-1",
			VersionID:    "cmd-1-v1",
		},
		PolicyRefs: []definition.DependencyPin{
			{Kind: definition.KindPolicy, DefinitionID: "policy-1", VersionID: "policy-1-v1"},
		},
		Outcomes: []string{"success", "failure"},
	}
}

func TestValidateDocument_ValidDocumentHasNoProblems(t *testing.T) {
	diags := block.ValidateDocument(validDocument())
	if diags.HasProblems() {
		t.Fatalf("ValidateDocument(valid) = %v, want no problems", diags)
	}
}

func TestValidateDocument_RejectsNoCompatibleNodeTypes(t *testing.T) {
	doc := validDocument()
	doc.CompatibleNodeTypes = nil
	diags := block.ValidateDocument(doc)
	requireProblemPath(t, diags, "compatibleNodeTypes")
}

func TestValidateDocument_RejectsUnsupportedNodeType(t *testing.T) {
	doc := validDocument()
	doc.CompatibleNodeTypes = []block.NodeType{"ROUTER"}
	diags := block.ValidateDocument(doc)
	requireProblemPath(t, diags, "compatibleNodeTypes[0]")
}

func TestValidateDocument_RejectsDuplicateCompatibleNodeType(t *testing.T) {
	doc := validDocument()
	doc.CompatibleNodeTypes = []block.NodeType{block.NodeTypeAgent, block.NodeTypeAgent}
	diags := block.ValidateDocument(doc)
	requireProblemPath(t, diags, "compatibleNodeTypes[1]")
}

func TestValidateDocument_RejectsDuplicateRequiredCapability(t *testing.T) {
	doc := validDocument()
	doc.RequiredCapabilities = []string{"X", "X"}
	diags := block.ValidateDocument(doc)
	requireProblemPath(t, diags, "requiredCapabilities[1]")
}

func TestValidateDocument_RejectsZeroTimeout(t *testing.T) {
	doc := validDocument()
	doc.TimeoutSeconds = 0
	diags := block.ValidateDocument(doc)
	requireProblemPath(t, diags, "timeoutSeconds")
}

func TestValidateDocument_RejectsInvalidScopeAccess(t *testing.T) {
	doc := validDocument()
	doc.ScopeSelector.Access = "DELETE"
	diags := block.ValidateDocument(doc)
	requireProblemPath(t, diags, "scopeSelector.access")
}

func TestValidateDocument_RejectsEmptyDoneCondition(t *testing.T) {
	doc := validDocument()
	doc.DoneCondition = ""
	diags := block.ValidateDocument(doc)
	requireProblemPath(t, diags, "doneCondition")
}

func TestValidateDocument_RejectsExecutorRefMissingIDs(t *testing.T) {
	doc := validDocument()
	doc.ExecutorRef = definition.DependencyPin{Kind: definition.KindCommand}
	diags := block.ValidateDocument(doc)
	requireProblemPath(t, diags, "executorRef.definitionId")
	requireProblemPath(t, diags, "executorRef.versionId")
}

// TestValidateDocument_RejectsSkillOrLayerAsExecutorRef is V2-04's own
// "Hoàn thành khi" bar: a Block must never be able to use a Skill or
// Layer as its executable ref, since both are architecturally passive
// resources with no execution authority.
func TestValidateDocument_RejectsSkillOrLayerAsExecutorRef(t *testing.T) {
	for _, kind := range []definition.Kind{definition.KindSkill, definition.KindLayer} {
		t.Run(string(kind), func(t *testing.T) {
			doc := validDocument()
			doc.ExecutorRef = definition.DependencyPin{Kind: kind, DefinitionID: "x", VersionID: "x-v1"}
			diags := block.ValidateDocument(doc)
			if !diags.HasProblems() {
				t.Fatalf("ValidateDocument should reject executorRef.kind=%s", kind)
			}
			if !strings.Contains(diags.Error(), "executorRef.kind") {
				t.Fatalf("diags = %v, want a problem on executorRef.kind", diags)
			}
			if !strings.Contains(diags.Error(), "passive resource") {
				t.Fatalf("diags = %v, want the passive-resource explanation for Skill/Layer", diags)
			}
		})
	}
}

func TestValidateDocument_AcceptsEveryExecutableExecutorKind(t *testing.T) {
	for _, kind := range []definition.Kind{
		definition.KindBlock, definition.KindCommand, definition.KindGate, definition.KindAgentProfile,
	} {
		t.Run(string(kind), func(t *testing.T) {
			doc := validDocument()
			doc.ExecutorRef = definition.DependencyPin{Kind: kind, DefinitionID: "x", VersionID: "x-v1"}
			diags := block.ValidateDocument(doc)
			for _, d := range diags {
				if d.Path == "executorRef.kind" {
					t.Fatalf("ValidateDocument should accept executorRef.kind=%s, got %v", kind, d)
				}
			}
		})
	}
}

func TestValidateDocument_RejectsWorkflowAsExecutorRef(t *testing.T) {
	doc := validDocument()
	doc.ExecutorRef = definition.DependencyPin{Kind: definition.KindWorkflow, DefinitionID: "x", VersionID: "x-v1"}
	diags := block.ValidateDocument(doc)
	requireProblemPath(t, diags, "executorRef.kind")
}

func TestValidateDocument_RejectsPolicyRefWithWrongKind(t *testing.T) {
	doc := validDocument()
	doc.PolicyRefs = []definition.DependencyPin{
		{Kind: definition.KindCommand, DefinitionID: "not-a-policy", VersionID: "v1"},
	}
	diags := block.ValidateDocument(doc)
	requireProblemPath(t, diags, "policyRefs[0].kind")
}

func TestValidateDocument_RejectsDuplicatePolicyRef(t *testing.T) {
	doc := validDocument()
	doc.PolicyRefs = []definition.DependencyPin{
		{Kind: definition.KindPolicy, DefinitionID: "policy-1", VersionID: "v1"},
		{Kind: definition.KindPolicy, DefinitionID: "policy-1", VersionID: "v2"},
	}
	diags := block.ValidateDocument(doc)
	requireProblemPath(t, diags, "policyRefs[1]")
}

func TestValidateDocument_RejectsNoOutcomes(t *testing.T) {
	doc := validDocument()
	doc.Outcomes = nil
	diags := block.ValidateDocument(doc)
	requireProblemPath(t, diags, "outcomes")
}

func TestValidateDocument_RejectsDuplicateOutcome(t *testing.T) {
	doc := validDocument()
	doc.Outcomes = []string{"ok", "ok"}
	diags := block.ValidateDocument(doc)
	requireProblemPath(t, diags, "outcomes[1]")
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
