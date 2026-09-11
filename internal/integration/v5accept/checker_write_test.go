// V5-15D — "Checker write attempt" (docs/design/07-v5-execution-evidence.md
// V5-15's own "scope violation, checker write attempt, cancel giữa mutating
// attempt" line; user's own binding 5-part PR split, full text in
// baocaov5checklist.md's own "V5-15" section and memory
// agent-kit-v5-15-acceptance-gate-contract.md).
//
// Proves V5-12's own real CHECKER-role enforcement
// (agent_node_executor_resources.go's own
// `strictReadOnly := profile.Role == workflow.AgentRoleChecker`, feeding
// `validateStrictlyReadOnlyDiffs` inside buildEvidence) really rejects a
// real observed mutation end to end — not merely that the enforcement
// exists, which V5-12's own unit tests already prove. This is a REAL
// write: a real cmd/fake-claude process really writes a real file inside
// its own real repository mount, despite that mount being forced
// ports.WorkspaceReadOnly (V5-05's own forceReadOnlyMounts, unconditional
// for CHECKER regardless of the owning WorkItem's own real EffectiveScope)
// — the fake CLI protocol has no built-in real filesystem side effect
// (confirmed by reading internal/adapters/providers/fixtures.go: every
// other mode only emits protocol JSONL over stdout), so this file adds the
// one, deliberately opt-in real write fixtures.go now supports
// (AGENTKIT_HELPER_WRITE_PATH) and points it at the real mount's own real
// WorkingDirectory — a path CHECKER's own resolveAgentWorkingDirectory
// would never pick as the process's own cwd (it always falls back to an
// empty scratchDirectory() for a forced-read-only executor), so this test
// passes it out of band, mirroring exactly how AGENTKIT_HELPER_MODE/
// AGENTKIT_HELPER_OUTCOME already steer this same fake CLI's own behavior.
package v5accept

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

func v5AcceptCheckerWriteDocument(agentBuildID string) workflow.WorkflowDocument {
	attemptPermissionRefs := []definition.DependencyPin{
		{Kind: definition.KindPolicy, DefinitionID: "v5d-checker-attempt-policy-def", VersionID: "v5d-checker-attempt-policy-v1"},
		{Kind: definition.KindPolicy, DefinitionID: "v5d-checker-permission-policy-def", VersionID: "v5d-checker-permission-policy-v1"},
	}
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{
				Key: "checker", Type: workflow.NodeAgent, Outcomes: []string{"done"},
				Agent: &workflow.AgentNodeConfig{
					ProfileRef:     definition.DependencyPin{Kind: definition.KindAgentProfile, DefinitionID: "v5d-checker-agent-profile-def", VersionID: "v5d-checker-agent-profile-v1"},
					PolicyRefs:     attemptPermissionRefs,
					AdapterBuildID: &agentBuildID,
					Role:           workflow.AgentRoleChecker,
				},
			},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-checker", From: "start", Outcome: "next", To: "checker"},
			{Key: "checker-end", From: "checker", Outcome: "done", To: "end"},
		},
	}
}

func TestV5AcceptCheckerWriteAttempt_RealStrictReadOnlyDiffRejectsRealMutation(t *testing.T) {
	f := newV5AcceptFixture(t)
	ctx := context.Background()

	t.Setenv("AGENTKIT_HELPER_MODE", "outcome-success")
	t.Setenv("AGENTKIT_HELPER_OUTCOME", "done")

	adapter := f.newClaudeAdapter(t)
	agentBuildID := f.registerAgentBuild(t, adapter)
	agentRegistry := f.newAgentRegistry(t, adapter)
	agentExecutor := f.newAgentExecutor(agentRegistry)

	publishPolicyVersion(t, f.uow, "v5d-checker-context-policy-def", "v5d-checker-context-policy-v1", v5AcceptContextPolicyDocument())
	publishAgentProfileVersion(t, f.uow, "v5d-checker-agent-profile-def", "v5d-checker-agent-profile-v1", "v5d-checker-context-policy-def", "v5d-checker-context-policy-v1")
	publishPolicyVersion(t, f.uow, "v5d-checker-attempt-policy-def", "v5d-checker-attempt-policy-v1", v5AcceptAttemptPolicyDocument(60))
	publishPolicyVersion(t, f.uow, "v5d-checker-permission-policy-def", "v5d-checker-permission-policy-v1", v5AcceptDriftPermissionPolicyDocument())

	doc := v5AcceptCheckerWriteDocument(agentBuildID)
	version := publishWorkflowVersion(t, f.uow, v5AcceptProjectID, "v5d-checker-workflow-def", "v5d-checker-workflow-v1", doc, workflow.DependencyManifest{})

	router := &runtime.NodeExecutorRouter{Agent: agentExecutor}
	registry := f.registerHandlersWithAgents(router, "v5dcw", agentRegistry)
	_, stopPool := f.startPool(t, registry)
	defer stopPool()

	// A real WRITE grant at the WorkItem level — deliberately: the point
	// of this scenario is that CHECKER forces its own resolved mount
	// read-only regardless of what the owning WorkItem actually grants,
	// not that the WorkItem itself lacked a real grant.
	root := f.createRootWorkItem(t, "checker-write-root", workdomain.RepositoryWrite)
	child := f.createChildWorkItem(t, root.WorkItemID, "checker-write-child", workdomain.RepositoryWrite)

	// Resolve the real, on-disk WorkingDirectory of repo-a's own real
	// mount — the exact real path this scenario's own real fake-claude
	// process will really write into, out of band via
	// AGENTKIT_HELPER_WRITE_PATH (never the process's own real cwd, which
	// CHECKER's own resolveAgentWorkingDirectory always sets to an empty
	// scratch dir instead — see this file's own package doc comment).
	baseHandle := f.repositoryWorkspaceHandle(t, root.WorkspaceSetID, v5AcceptRepositoryID, 1)
	workingDirectory, err := f.provider.WorkingDirectory(ctx, baseHandle)
	if err != nil {
		t.Fatalf("WorkingDirectory: %v", err)
	}
	mutationPath := filepath.Join(workingDirectory, "checker-mutation.txt")
	t.Setenv("AGENTKIT_HELPER_WRITE_PATH", mutationPath)

	startCmd := testCmd("v5d-checker-start", ports.ProjectScope(v5AcceptProjectID), "StartWorkflowRun")
	started, err := runtime.StartWorkflowRun(ctx, f.uow, f.ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: v5AcceptProjectID, WorkItemID: child.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	runID := started.RunID

	// v5AcceptAttemptPolicyDocument declares no RetryableErrorCodes, so a
	// real CodeScopeViolation failure (validateStrictlyReadOnlyDiffs wraps
	// the SAME scopeguard.ErrScopeViolation a real out-of-path write
	// would — see agent_node_executor_resources.go's own doc comment) is
	// non-retryable — the Run fails directly (completion.go's own
	// NON_RETRYABLE_FAILURE -> transitionRunToFailedTx path, nothing new
	// here), the same shape scope_violation_test.go's own COMMAND-node
	// scenario already exercises for the sibling check.
	run := f.waitForRunState(t, runID, runtimedomain.WorkflowRunFailed)

	var workItem workdomain.WorkItem
	var attempts []runtimedomain.ExecutionAttempt
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		if workItem, err = tx.Work().GetWorkItem(ctx, child.WorkItemID); err != nil {
			return err
		}
		attempts, err = tx.Runtime().ListExecutionAttemptsForRun(ctx, runID)
		return err
	}); err != nil {
		t.Fatalf("read final state: %v", err)
	}
	if run.State != runtimedomain.WorkflowRunFailed {
		t.Fatalf("run.State = %s, want FAILED", run.State)
	}
	if workItem.Status == workdomain.WorkItemDone {
		t.Fatal("workItem.Status = DONE — a real checker write must never reach DONE")
	}

	var failed *runtimedomain.ExecutionAttempt
	for i := range attempts {
		if attempts[i].State == runtimedomain.ExecutionAttemptFailed {
			failed = &attempts[i]
		}
	}
	if failed == nil {
		t.Fatalf("no ExecutionAttempt reached FAILED; attempts=%+v", attempts)
	}
	if failed.TerminationReason != runtimedomain.TerminationReasonScopeViolation {
		t.Fatalf("attempt.TerminationReason = %s, want %s", failed.TerminationReason, runtimedomain.TerminationReasonScopeViolation)
	}
	if failed.FailureCode != errorcode.CodeScopeViolation {
		t.Fatalf("attempt.FailureCode = %s, want %s", failed.FailureCode, errorcode.CodeScopeViolation)
	}
	if len(attempts) != 1 {
		t.Fatalf("ExecutionAttempts for run %s = %d, want exactly 1 (non-retryable)", runID, len(attempts))
	}

	// The real proof this was a genuine observed mutation, not merely a
	// logic path that never actually ran: the fake CLI's own real write
	// really landed on disk.
	if _, err := os.Stat(mutationPath); err != nil {
		t.Fatalf("real mutation file %s was never written by the real checker process: %v", mutationPath, err)
	}
}
