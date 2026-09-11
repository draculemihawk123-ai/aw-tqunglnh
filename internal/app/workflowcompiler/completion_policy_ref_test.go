package workflowcompiler_test

// This file is V5-11's own schema-foundation test suite (2026-09-10):
// WorkflowDocument.CompletionPolicyRef (a root-level pin, not per-node)
// resolved and category-verified by CompileAndResolve, exactly mirroring
// every existing node-level Agent/Command/Gate/PolicyRefs pin's own
// resolution — see baocaov5checklist.md's "V5-11" section for the full
// narrative and contract 1's exact wording.

import (
	"context"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/workflowcompiler"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

func documentWithCompletionPolicyRef(ref *definition.DependencyPin) workflow.WorkflowDocument {
	doc := simpleAgentDocument("profile-1-v1")
	doc.CompletionPolicyRef = ref
	return doc
}

func validCompletionPolicyDocument() policy.PolicyDocument {
	return policy.PolicyDocument{
		Category:   policy.CategoryCompletion,
		Completion: &policy.CompletionRules{RequiredEvidenceKinds: []string{"TEST_RESULT"}},
	}
}

func TestCompileAndResolve_ResolvesCompletionPolicyRefIntoManifest(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	seedVersion(t, uow, definition.KindAgentProfile, "profile-1", "profile-1-v1",
		map[string]any{"providerKey": "claude", "model": "x", "toolRefs": []string{}, "compatibility": map[string]any{"os": []string{"linux"}}, "budget": map[string]any{"maxTokens": 1}},
		"sha256:compiled-profile-1-v1")
	seedVersion(t, uow, definition.KindPolicy, "completion-policy-1", "completion-policy-1-v1",
		validCompletionPolicyDocument(), "sha256:compiled-completion-policy-1-v1")

	doc := documentWithCompletionPolicyRef(&definition.DependencyPin{
		Kind: definition.KindPolicy, DefinitionID: "completion-policy-1", VersionID: "completion-policy-1-v1",
	})
	version, err := workflowcompiler.CompileAndResolve(ctx, uow, testDefinition(), testPublishRequest("wf-1-v1", doc))
	if err != nil {
		t.Fatalf("CompileAndResolve: %v", err)
	}

	deps := version.Dependencies()
	var found *workflow.DependencyPin
	for i := range deps.Pins {
		if deps.Pins[i].Key == "completion-policy-1" {
			found = &deps.Pins[i]
		}
	}
	if found == nil {
		t.Fatalf("deps.Pins = %+v, want a completion-policy-1 entry", deps.Pins)
	}
	if found.Version != "completion-policy-1-v1" || found.Hash != "sha256:compiled-completion-policy-1-v1" {
		t.Fatalf("resolved completion policy pin = %+v, want version=completion-policy-1-v1 hash=sha256:compiled-completion-policy-1-v1", found)
	}
}

func TestCompileAndResolve_RejectsCompletionPolicyRefWrongCategory(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	seedVersion(t, uow, definition.KindAgentProfile, "profile-1", "profile-1-v1",
		map[string]any{"providerKey": "claude", "model": "x", "toolRefs": []string{}, "compatibility": map[string]any{"os": []string{"linux"}}, "budget": map[string]any{"maxTokens": 1}},
		"sha256:compiled-profile-1-v1")
	// Seeded as a PERMISSION-category policy, not COMPLETION.
	seedVersion(t, uow, definition.KindPolicy, "wrong-category-policy", "wrong-category-policy-v1",
		policy.PolicyDocument{
			Category:   policy.CategoryPermission,
			Permission: &policy.PermissionRules{IsolationTier: policy.IsolationTierEnforcedIsolated},
		}, "sha256:compiled-wrong-category-policy-v1")

	doc := documentWithCompletionPolicyRef(&definition.DependencyPin{
		Kind: definition.KindPolicy, DefinitionID: "wrong-category-policy", VersionID: "wrong-category-policy-v1",
	})
	_, err := workflowcompiler.CompileAndResolve(ctx, uow, testDefinition(), testPublishRequest("wf-1-v1", doc))
	if err == nil {
		t.Fatal("CompileAndResolve should reject a completionPolicyRef that does not resolve to a COMPLETION-category policy")
	}
	if !strings.Contains(err.Error(), "not a COMPLETION-category policy") {
		t.Fatalf("err = %v, want it to mention the category mismatch", err)
	}
}

func TestCompileAndResolve_RejectsUnresolvedCompletionPolicyRef(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	seedVersion(t, uow, definition.KindAgentProfile, "profile-1", "profile-1-v1",
		map[string]any{"providerKey": "claude", "model": "x", "toolRefs": []string{}, "compatibility": map[string]any{"os": []string{"linux"}}, "budget": map[string]any{"maxTokens": 1}},
		"sha256:compiled-profile-1-v1")
	// No completion-policy version seeded at all.

	doc := documentWithCompletionPolicyRef(&definition.DependencyPin{
		Kind: definition.KindPolicy, DefinitionID: "missing-completion-policy", VersionID: "missing-completion-policy-v1",
	})
	_, err := workflowcompiler.CompileAndResolve(ctx, uow, testDefinition(), testPublishRequest("wf-1-v1", doc))
	if err == nil {
		t.Fatal("CompileAndResolve should reject an unresolvable completionPolicyRef")
	}
	if !strings.Contains(err.Error(), "does not resolve to any published version") {
		t.Fatalf("err = %v, want it to mention unresolved pin", err)
	}
}
