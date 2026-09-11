// V5-15C — "Provider loss" (docs/design/07-v5-execution-evidence.md
// V5-15's own "provider loss, adapter drift, isolation unavailable" line;
// user's own binding 5-part PR split, full text in baocaov5checklist.md's
// own "V5-15" section and memory
// agent-kit-v5-15-acceptance-gate-contract.md).
//
// What "provider loss" means here, and why (confirmed by reading the real
// code, internal/app/runtime/admission.go's own runAdmissionProbePhase):
// this package's own real AdapterBuild pinning always names a real
// ProviderKey (V5-08B's own real requirement, GC-INV-23) — the real
// registry an ExecuteNodeHandler dispatches through is what actually
// resolves that key to a live ports.AgentExecutor
// (agentregistry.Registry.Resolve). A "provider loss" is exactly what
// happens when that registry no longer carries the pinned provider — an
// operator removed it from configuration, a process crashed and was never
// re-registered, or (as reproduced here) two components were wired against
// different registry instances by mistake. This is a real, EXISTING code
// path — agentregistry.ErrUnknownProvider — and, per admission.go's own
// doc comment, a genuine TECHNICAL Handle() failure (retried like any
// other job error), never a business BLOCKED admission outcome: the
// Attempt/NodeRun never even leave QUEUED, because runAdmissionProbePhase
// itself returns an error before Phase 2 ever runs. The only real,
// observable trace is the durable EXECUTE_NODE job itself failing on every
// claim until its own MaxClaims is exhausted and it goes DEAD — proving
// this codebase fails closed (never silently runs, never falsely
// succeeds) rather than hanging forever or masking the loss.
package v5accept

import (
	"context"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// v5AcceptProviderLossDocument is a minimal real graph — a single real
// AGENT node pinned to a real, non-drifted AdapterBuild; this file's own
// fault is entirely in which registry ExecuteNodeHandler dispatches
// through, not in the graph or the pin itself.
func v5AcceptProviderLossDocument(agentBuildID string) workflow.WorkflowDocument {
	attemptPermissionRefs := []definition.DependencyPin{
		{Kind: definition.KindPolicy, DefinitionID: "v5c-loss-attempt-policy-def", VersionID: "v5c-loss-attempt-policy-v1"},
		{Kind: definition.KindPolicy, DefinitionID: "v5c-loss-permission-policy-def", VersionID: "v5c-loss-permission-policy-v1"},
	}
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{
				Key: "maker", Type: workflow.NodeAgent, Outcomes: []string{"done"},
				Agent: &workflow.AgentNodeConfig{
					ProfileRef:     definition.DependencyPin{Kind: definition.KindAgentProfile, DefinitionID: "v5c-loss-agent-profile-def", VersionID: "v5c-loss-agent-profile-v1"},
					PolicyRefs:     attemptPermissionRefs,
					AdapterBuildID: &agentBuildID,
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

func TestV5AcceptProviderLoss_RealDispatchFailsClosedUntilJobDies(t *testing.T) {
	f := newV5AcceptFixture(t)
	ctx := context.Background()

	t.Setenv("AGENTKIT_HELPER_MODE", "outcome-success")
	t.Setenv("AGENTKIT_HELPER_OUTCOME", "done")

	// A real, working adapter and a real, correctly-pinned AdapterBuild
	// (registerAgentBuild, unmodified — no drift here at all): the pin
	// itself is genuinely valid, only the registry ExecuteNodeHandler
	// dispatches through is missing the provider it names.
	adapter := f.newClaudeAdapter(t)
	agentBuildID := f.registerAgentBuild(t, adapter)

	publishPolicyVersion(t, f.uow, "v5c-loss-context-policy-def", "v5c-loss-context-policy-v1", v5AcceptContextPolicyDocument())
	publishAgentProfileVersion(t, f.uow, "v5c-loss-agent-profile-def", "v5c-loss-agent-profile-v1", "v5c-loss-context-policy-def", "v5c-loss-context-policy-v1")
	publishPolicyVersion(t, f.uow, "v5c-loss-attempt-policy-def", "v5c-loss-attempt-policy-v1", v5AcceptAttemptPolicyDocument(60))
	publishPolicyVersion(t, f.uow, "v5c-loss-permission-policy-def", "v5c-loss-permission-policy-v1", v5AcceptDriftPermissionPolicyDocument())

	doc := v5AcceptProviderLossDocument(agentBuildID)
	version := publishWorkflowVersion(t, f.uow, v5AcceptProjectID, "v5c-loss-workflow-def", "v5c-loss-workflow-v1", doc, workflow.DependencyManifest{})

	// The real fault: AgentNodeExecutor is wired against a real, working
	// registry (agentRegistry, holding the real claude.Adapter this
	// Attempt's own AdapterBuild actually names) — but
	// ExecuteNodeHandler's own admission phase, which is what resolves the
	// pinned provider before dispatch, is wired against a DIFFERENT,
	// EMPTY registry. A real operator misconfiguration (or a provider that
	// crashed and was deregistered) looks exactly like this: the
	// executor that could run the work still exists somewhere, but the
	// registry admission actually consults no longer knows about it.
	workingRegistry := f.newAgentRegistry(t, adapter)
	agentExecutor := f.newAgentExecutor(workingRegistry)
	router := &runtime.NodeExecutorRouter{Agent: agentExecutor}
	registry := f.registerHandlersWithAgents(router, "v5closs", agentregistry.Empty())
	_, stopPool := f.startPool(t, registry)
	defer stopPool()

	root := f.createRootWorkItem(t, "provider-loss-root", workdomain.RepositoryRead)
	child := f.createChildWorkItem(t, root.WorkItemID, "provider-loss-child", workdomain.RepositoryRead)

	startCmd := testCmd("v5c-loss-start", ports.ProjectScope(v5AcceptProjectID), "StartWorkflowRun")
	started, err := runtime.StartWorkflowRun(ctx, f.uow, f.ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: v5AcceptProjectID, WorkItemID: child.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	runID := started.RunID

	// Resolve the real ExecutionAttempt's own ID so this test can find
	// ITS OWN durable EXECUTE_NODE job below, among any others.
	var attemptID string
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		var attempts []runtimedomain.ExecutionAttempt
		if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
			nodeRuns, err := tx.Runtime().ListNodeRunsForRun(ctx, runID)
			if err != nil {
				return err
			}
			for _, nr := range nodeRuns {
				if nr.NodeKey != "maker" {
					continue
				}
				attempts, err = tx.Runtime().ListExecutionAttemptsForRun(ctx, runID)
				return err
			}
			return nil
		}); err == nil {
			for _, a := range attempts {
				attemptID = string(a.ID)
			}
		}
		if attemptID != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if attemptID == "" {
		t.Fatal("no real ExecutionAttempt ever appeared for the maker node run")
	}

	// The real EXECUTE_NODE job for this Attempt must fail every real
	// claim (agentregistry.ErrUnknownProvider from the empty registry
	// above) until its own real MaxClaims is exhausted and
	// RecoverExpiredJobs marks it DEAD — a real, observable "gave up
	// retrying a permanently-unavailable provider," never a silent hang
	// or a false success.
	jobDeadline := time.Now().Add(20 * time.Second)
	var lastState string
	var lastClaimCount int
	for time.Now().Before(jobDeadline) {
		rows, err := f.store.DebugListJobsByKind(ctx, runtime.ExecuteNodeJobKind)
		if err == nil {
			for _, row := range rows {
				if row.AggregateID != attemptID {
					continue
				}
				lastState, lastClaimCount = row.State, row.ClaimCount
				if row.State == string(ports.JobDead) {
					goto jobDead
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("EXECUTE_NODE job for attempt %s never reached DEAD within the deadline; last observed state=%s claimCount=%d", attemptID, lastState, lastClaimCount)

jobDead:
	// The real Attempt/NodeRun themselves must never have moved past
	// QUEUED — runAdmissionProbePhase's own error return means Phase 2
	// (the transaction that would CAS QUEUED->RUNNING or QUEUED->BLOCKED)
	// is never even reached.
	var attempt runtimedomain.ExecutionAttempt
	var nodeRun runtimedomain.NodeRun
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		attempt, err = tx.Runtime().GetExecutionAttempt(ctx, attemptID)
		if err != nil {
			return err
		}
		nodeRuns, err := tx.Runtime().ListNodeRunsForRun(ctx, runID)
		if err != nil {
			return err
		}
		for _, nr := range nodeRuns {
			if nr.NodeKey == "maker" {
				nodeRun = nr
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("read final attempt/node-run state: %v", err)
	}
	if attempt.State != runtimedomain.ExecutionAttemptQueued {
		t.Fatalf("attempt.State = %s, want QUEUED (admission never resolved the missing provider into BLOCKED or RUNNING)", attempt.State)
	}
	if nodeRun.State != runtimedomain.NodeRunQueued {
		t.Fatalf("nodeRun.State = %s, want QUEUED", nodeRun.State)
	}
}
