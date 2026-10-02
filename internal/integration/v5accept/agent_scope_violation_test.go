// V9-09 / B3 — "Agent writing outside pathScopes is reported as
// PROVIDER_UNAVAILABLE instead of SCOPE_VIOLATION".
//
// scope_violation_test.go proves the COMMAND-node variant, and
// checker_write_test.go proves a CHECKER AGENT's strict read-only violation —
// both are caught by buildEvidence AFTER the provider returned normally. This
// file exercises the path neither reaches: a MAKER AGENT whose real process
// writes outside its WorkItem's pathScopes BEFORE the provider's terminal
// event, so agentevents.Sink's mid-run checkpoint (captureCheckpointLocked ->
// scopeguard.ValidateDiffs) rejects it while the provider adapter is still
// consuming the stream. That rejection used to surface as the adapter's own
// Go error, which AgentNodeExecutor.classify treated as "a bare error before
// or during the provider process's lifecycle" — row 1 of its mapping table,
// FAILED + PROVIDER_UNAVAILABLE — misdiagnosing a scope breach as an
// infrastructure outage and pointing the operator at the wrong layer.
//
// The write is real: cmd/fake-claude's fixtures.go (AGENTKIT_HELPER_WRITE_PATH,
// opt-in, written before any protocol output) really creates a file inside the
// real repository mount, and the real claude adapter really raises the
// checkpoint when the fake CLI's terminal "result" event arrives.
package v5accept

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

func v5AcceptMakerScopeDocument(agentBuildID string) workflow.WorkflowDocument {
	attemptPermissionRefs := []definition.DependencyPin{
		{Kind: definition.KindPolicy, DefinitionID: "v9-maker-attempt-policy-def", VersionID: "v9-maker-attempt-policy-v1"},
		{Kind: definition.KindPolicy, DefinitionID: "v9-maker-permission-policy-def", VersionID: "v9-maker-permission-policy-v1"},
	}
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{
				Key: "maker", Type: workflow.NodeAgent, Outcomes: []string{"done"},
				Agent: &workflow.AgentNodeConfig{
					ProfileRef:     definition.DependencyPin{Kind: definition.KindAgentProfile, DefinitionID: "v9-maker-agent-profile-def", VersionID: "v9-maker-agent-profile-v1"},
					PolicyRefs:     attemptPermissionRefs,
					AdapterBuildID: &agentBuildID,
					Role:           workflow.AgentRoleMaker,
				},
			},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-maker", From: "start", Outcome: "next", To: "maker"},
			{Key: "maker-end", From: "maker", Outcome: "done", To: "end"},
		},
	}
}

func TestV9AcceptMakerAgentWriteOutsidePathScopes_IsScopeViolationNotProviderUnavailable(t *testing.T) {
	f := newV5AcceptFixture(t)
	ctx := context.Background()

	t.Setenv("AGENTKIT_HELPER_MODE", "outcome-success")
	t.Setenv("AGENTKIT_HELPER_OUTCOME", "done")

	adapter := f.newClaudeAdapter(t)
	agentBuildID := f.registerAgentBuild(t, adapter)
	agentRegistry := f.newAgentRegistry(t, adapter)
	agentExecutor := f.newAgentExecutor(agentRegistry)

	publishPolicyVersion(t, f.uow, "v9-maker-context-policy-def", "v9-maker-context-policy-v1", v5AcceptContextPolicyDocument())
	publishAgentProfileVersion(t, f.uow, "v9-maker-agent-profile-def", "v9-maker-agent-profile-v1", "v9-maker-context-policy-def", "v9-maker-context-policy-v1")
	publishPolicyVersion(t, f.uow, "v9-maker-attempt-policy-def", "v9-maker-attempt-policy-v1", v5AcceptAttemptPolicyDocument(60))
	publishPolicyVersion(t, f.uow, "v9-maker-permission-policy-def", "v9-maker-permission-policy-v1", v5AcceptDriftPermissionPolicyDocument())

	doc := v5AcceptMakerScopeDocument(agentBuildID)
	version := publishWorkflowVersion(t, f.uow, v5AcceptProjectID, "v9-maker-workflow-def", "v9-maker-workflow-v1", doc, workflow.DependencyManifest{})

	router := &runtime.NodeExecutorRouter{Agent: agentExecutor}
	registry := f.registerHandlersWithAgents(router, "v9ms", agentRegistry)
	_, stopPool := f.startPool(t, registry)
	defer stopPool()

	// A genuine WRITE grant, but only under "allowed": the write below lands
	// at the repository root, a path this grant never covers.
	root := f.createRootWorkItem(t, "maker-scope-root", workdomain.RepositoryWrite)
	child := f.createChildWorkItemWithPathScopes(t, root.WorkItemID, "maker-scope-child", workdomain.RepositoryWrite, []string{"allowed"})

	baseHandle := f.repositoryWorkspaceHandle(t, root.WorkspaceSetID, v5AcceptRepositoryID, 1)
	workingDirectory, err := f.provider.WorkingDirectory(ctx, baseHandle)
	if err != nil {
		t.Fatalf("WorkingDirectory: %v", err)
	}
	mutationPath := filepath.Join(workingDirectory, "leaked-by-agent.txt")
	t.Setenv("AGENTKIT_HELPER_WRITE_PATH", mutationPath)

	startCmd := testCmd("v9-maker-start", ports.ProjectScope(v5AcceptProjectID), "StartWorkflowRun")
	started, err := runtime.StartWorkflowRun(ctx, f.uow, f.ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: v5AcceptProjectID, WorkItemID: child.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	runID := started.RunID

	// The attempt policy declares no RetryableErrorCodes, so a FAILED
	// SCOPE_VIOLATION is final: the run fails after exactly one attempt. (On
	// the old classification the same run failed with PROVIDER_UNAVAILABLE.)
	f.waitForRunState(t, runID, runtimedomain.WorkflowRunFailed)

	var attempts []runtimedomain.ExecutionAttempt
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		attempts, err = tx.Runtime().ListExecutionAttemptsForRun(ctx, runID)
		return err
	}); err != nil {
		t.Fatalf("read attempts: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("ExecutionAttempts for run %s = %d, want exactly 1 (SCOPE_VIOLATION is non-retryable)", runID, len(attempts))
	}
	failed := attempts[0]
	if failed.State != runtimedomain.ExecutionAttemptFailed {
		t.Fatalf("attempt.State = %s, want FAILED", failed.State)
	}
	if failed.FailureCode != errorcode.CodeScopeViolation {
		t.Fatalf("attempt.FailureCode = %s, want %s (a scope breach must never be reported as an infrastructure outage)", failed.FailureCode, errorcode.CodeScopeViolation)
	}
	if failed.TerminationReason != runtimedomain.TerminationReasonScopeViolation {
		t.Fatalf("attempt.TerminationReason = %s, want %s", failed.TerminationReason, runtimedomain.TerminationReasonScopeViolation)
	}

	// The real proof this was a genuine observed write, not a logic path
	// that never ran.
	if _, err := os.Stat(mutationPath); err != nil {
		t.Fatalf("real mutation file %s was never written by the real agent process: %v", mutationPath, err)
	}

	// The operator can see WHICH path breached the scope through the run
	// timeline (the same query behind GET /runs/{id}/timeline and
	// `aw run timeline`).
	timeline, err := runtime.GetRunTimeline(ctx, f.uow, redact.NewMatcher(), runID)
	if err != nil {
		t.Fatalf("GetRunTimeline: %v", err)
	}
	var detail string
	for _, entry := range timeline.Entries {
		if entry.Kind == runtime.TimelineEntryExecutionAttempt && entry.AttemptID == string(failed.ID) {
			detail = entry.FailureDetail
		}
	}
	if !strings.Contains(detail, "leaked-by-agent.txt") {
		t.Fatalf("timeline failureDetail = %q, want it to name the violating path leaked-by-agent.txt", detail)
	}
}
