package workflowcompiler_test

// This file is V5-12's own schema-foundation test suite (2026-09-10):
// every AGENT node must declare an explicit, valid workflow.AgentRole
// (MAKER or CHECKER) to publish through CompileAndResolve — the real
// publish entrypoint — even though the domain layer's own
// workflow.ValidateDocument stays permissive about an empty Role (so an
// already-persisted WorkflowVersion predating this field keeps
// rebuilding via internal/adapters/sqlite's own loadWorkflowVersion). See
// baocaov5checklist.md's "V5-12" section for the full narrative and
// contract 1's exact wording.

import (
	"context"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/workflowcompiler"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

func seedSimpleAgentProfile(t *testing.T, uow *fake.UnitOfWork) {
	t.Helper()
	seedVersion(t, uow, definition.KindAgentProfile, "profile-1", "profile-1-v1",
		map[string]any{"providerKey": "claude", "model": "x", "toolRefs": []string{}, "compatibility": map[string]any{"os": []string{"linux"}}, "budget": map[string]any{"maxTokens": 1}},
		"sha256:compiled-profile-1-v1")
}

func TestCompileAndResolve_RejectsAgentNodeMissingRole(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	seedSimpleAgentProfile(t, uow)

	doc := simpleAgentDocument("profile-1-v1")
	doc.Nodes[1].Agent.Role = ""

	_, err := workflowcompiler.CompileAndResolve(ctx, uow, testDefinition(), testPublishRequest("wf-1-v1", doc))
	if err == nil {
		t.Fatal("CompileAndResolve should reject an AGENT node with no explicit role")
	}
	if !strings.Contains(err.Error(), `must declare an explicit agent.role`) {
		t.Fatalf("err = %v, want it to mention the missing explicit role", err)
	}
	if _, ok := err.(*workflowcompiler.AgentRoleValidationError); !ok {
		t.Fatalf("err = %v (%T), want *workflowcompiler.AgentRoleValidationError", err, err)
	}
}

func TestCompileAndResolve_AcceptsExplicitCheckerRole(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	seedSimpleAgentProfile(t, uow)

	doc := simpleAgentDocument("profile-1-v1")
	doc.Nodes[1].Agent.Role = workflow.AgentRoleChecker

	if _, err := workflowcompiler.CompileAndResolve(ctx, uow, testDefinition(), testPublishRequest("wf-1-v1", doc)); err != nil {
		t.Fatalf("CompileAndResolve should accept an explicit CHECKER role: %v", err)
	}
}

func TestCompileAndResolve_RejectsOneOfTwoAgentNodesMissingRole(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	seedVersion(t, uow, definition.KindAgentProfile, "profile-1", "profile-1-v1", map[string]any{}, "sha256:v1")
	seedVersion(t, uow, definition.KindAgentProfile, "profile-1", "profile-1-v2", map[string]any{}, "sha256:v2")

	doc := workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"go"}},
			{Key: "agent1", Type: workflow.NodeAgent, Outcomes: []string{"done"}, Agent: &workflow.AgentNodeConfig{
				ProfileRef: definition.DependencyPin{Kind: definition.KindAgentProfile, DefinitionID: "profile-1", VersionID: "profile-1-v1"},
				Role:       workflow.AgentRoleMaker,
			}},
			{Key: "agent2", Type: workflow.NodeAgent, Outcomes: []string{"done"}, Agent: &workflow.AgentNodeConfig{
				ProfileRef: definition.DependencyPin{Kind: definition.KindAgentProfile, DefinitionID: "profile-1", VersionID: "profile-1-v1"},
				// Role deliberately left empty.
			}},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "e1", From: "start", Outcome: "go", To: "agent1"},
			{Key: "e2", From: "agent1", Outcome: "done", To: "agent2"},
			{Key: "e3", From: "agent2", Outcome: "done", To: "end"},
		},
	}

	_, err := workflowcompiler.CompileAndResolve(ctx, uow, testDefinition(), testPublishRequest("wf-1-v1", doc))
	if err == nil {
		t.Fatal("CompileAndResolve should reject a graph where any AGENT node lacks an explicit role")
	}
	if !strings.Contains(err.Error(), `node "agent2" must declare an explicit agent.role`) {
		t.Fatalf("err = %v, want it to name node \"agent2\"", err)
	}
}
