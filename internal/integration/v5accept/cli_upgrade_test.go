package v5accept

import (
	"context"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/cliupgrade"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/app/workitemredo"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// V12-05 (UX-05): the provider CLI updated itself, every AGENT node is blocked ADAPTER_BUILD_DRIFT. "Use the new CLI" measures the
// CLI as it is now, registers that build, and publishes a new workflow version pinning it — the blocked run keeps its old pin.
func TestV12UseNewCLI_RegistersTheNewBuildAndPublishesARepinnedVersion(t *testing.T) {
	f := newV5AcceptFixture(t)
	ctx := context.Background()
	t.Setenv("AGENTKIT_HELPER_MODE", "outcome-success")
	t.Setenv("AGENTKIT_HELPER_OUTCOME", "done")

	adapter := f.newClaudeAdapter(t)
	driftedBuildID := f.registerDriftedAgentBuild(t, adapter)
	agentRegistry := f.newAgentRegistry(t, adapter)
	agentExecutor := f.newAgentExecutor(agentRegistry)

	publishPolicyVersion(t, f.uow, "v5c-drift-context-policy-def", "v5c-drift-context-policy-v1", v5AcceptContextPolicyDocument())
	publishAgentProfileVersion(t, f.uow, "v5c-drift-agent-profile-def", "v5c-drift-agent-profile-v1", "v5c-drift-context-policy-def", "v5c-drift-context-policy-v1")
	publishPolicyVersion(t, f.uow, "v5c-drift-attempt-policy-def", "v5c-drift-attempt-policy-v1", v5AcceptAttemptPolicyDocument(60))
	publishPolicyVersion(t, f.uow, "v5c-drift-permission-policy-def", "v5c-drift-permission-policy-v1", v5AcceptDriftPermissionPolicyDocument())
	doc := v5AcceptAdapterDriftDocument(driftedBuildID)
	for i := range doc.Nodes {
		if doc.Nodes[i].Agent != nil {
			doc.Nodes[i].Agent.Role = workflow.AgentRoleMaker // a published workflow declares roles (the compiler requires it)
		}
	}
	version := publishWorkflowVersion(t, f.uow, v5AcceptProjectID, "v5c-drift-workflow-def", "v5c-drift-workflow-v1", doc, workflow.DependencyManifest{})

	router := &runtime.NodeExecutorRouter{Agent: agentExecutor}
	registry := f.registerHandlersWithAgents(router, "v12cli", agentRegistry)
	_, stopPool := f.startPool(t, registry)
	defer stopPool()

	root := f.createRootWorkItem(t, "cli-upgrade-root", workdomain.RepositoryRead)
	child := f.createChildWorkItem(t, root.WorkItemID, "cli-upgrade-child", workdomain.RepositoryRead)
	started, err := runtime.StartWorkflowRun(ctx, f.uow, f.ids, testCmd("v12-cli-start", ports.ProjectScope(v5AcceptProjectID), "StartWorkflowRun"),
		runtime.StartWorkflowRunRequest{ProjectID: v5AcceptProjectID, WorkItemID: child.WorkItemID, WorkflowVersionID: string(version.ID())})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	nodeRun := f.waitForNodeRunState(t, started.RunID, "maker", runtimedomain.NodeRunBlocked)

	deps := cliupgrade.Deps{UnitOfWork: f.uow, IDs: f.ids, Agents: agentRegistry, Redo: workitemredo.Service{UnitOfWork: f.uow}}
	cmd := testCmd("v12-cli-upgrade", ports.ProjectScope(v5AcceptProjectID), "UseNewCLI")
	result, err := cliupgrade.UseNewCLI(ctx, deps, cmd, cliupgrade.Request{
		ProjectID: v5AcceptProjectID, WorkItemID: child.WorkItemID, NodeRunID: string(nodeRun.ID),
	})
	if err != nil {
		t.Fatalf("UseNewCLI: %v", err)
	}
	if result.PreviousAdapterBuildID != driftedBuildID || result.AdapterBuildID == "" || result.AdapterBuildID == driftedBuildID {
		t.Fatalf("builds = %s -> %s, want a new build replacing %s", result.PreviousAdapterBuildID, result.AdapterBuildID, driftedBuildID)
	}
	if result.WorkflowVersionNumber != 2 || len(result.RepinnedNodes) != 1 || result.RepinnedNodes[0] != "maker" || result.NextStep == "" {
		t.Fatalf("result = %+v, want version 2 repinning maker and a next step (the run keeps the old pin)", result)
	}
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		published, err := tx.Definitions().GetWorkflowVersion(ctx, result.WorkflowVersionID)
		if err != nil {
			return err
		}
		for _, node := range published.Document().Nodes {
			if node.Key == "maker" && (node.Agent.AdapterBuildID == nil || *node.Agent.AdapterBuildID != result.AdapterBuildID) {
				t.Errorf("published maker pins %v, want %s", node.Agent.AdapterBuildID, result.AdapterBuildID)
			}
		}
		run, err := tx.Runtime().GetWorkflowRun(ctx, started.RunID)
		if err != nil {
			return err
		}
		if string(run.WorkflowVersionID) != string(version.ID()) {
			t.Errorf("the blocked run was repinned to %s", run.WorkflowVersionID)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Asking again replays: the same build, the same version.
	again, err := cliupgrade.UseNewCLI(ctx, deps, cmd, cliupgrade.Request{
		ProjectID: v5AcceptProjectID, WorkItemID: child.WorkItemID, NodeRunID: string(nodeRun.ID),
	})
	if err != nil || again.AdapterBuildID != result.AdapterBuildID || again.WorkflowVersionID != result.WorkflowVersionID {
		t.Fatalf("replay = %+v, %v; want the first result", again, err)
	}
}
