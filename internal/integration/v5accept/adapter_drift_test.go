// V5-15C — "Adapter drift" (docs/design/07-v5-execution-evidence.md
// V5-15's own "provider loss, adapter drift, isolation unavailable" line;
// user's own binding 5-part PR split, full text in baocaov5checklist.md's
// own "V5-15" section and memory
// agent-kit-v5-15-acceptance-gate-contract.md).
//
// Proves adapterbuild.VerifyNoDrift (internal/app/adapterbuild/drift.go)
// really rejects a real Attempt end-to-end, not merely in its own unit
// test: this file pins a real AdapterBuild whose ExecutableContentHash has
// been deliberately mutated after being derived from a real, live probe of
// the real cmd/fake-claude binary (agent_harness_test.go's own
// registerAgentBuild, copied and perturbed by exactly one field) — the
// underlying binary on disk is never touched, only the row this package
// pins as the Attempt's own AdapterBuildID. A real AGENT node's real
// admission phase (admission.go's own runAdmissionProbePhase) re-probes
// that SAME real binary live and re-derives its own fresh CandidateTuple —
// which can only ever match the binary's own true hash, never the
// deliberately-wrong one pinned here — so VerifyNoDrift's own real
// Build.ID() comparison really mismatches and really rejects the Attempt,
// exactly the way an operator rolling out a new fake-claude binary without
// re-registering its AdapterBuild would trigger this in production.
package v5accept

import (
	"context"
	stdruntime "runtime"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/providers/claude"
	"github.com/taQuangLing/agent-workflow/internal/app/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	domainadapterbuild "github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// registerDriftedAgentBuild is registerAgentBuild (agent_harness_test.go),
// copied and perturbed at exactly one point: ExecutableContentHash gets a
// suffix appended AFTER being derived from the real binary's own real
// file hash, so the pinned Build.ID() can never match what a real, live
// VerifyNoDrift re-probe of the SAME, untouched binary will ever compute.
// Every other field is derived identically to registerAgentBuild — this is
// a real pin with exactly one real, deliberate lie in it, not a wholesale
// fabrication.
func (f *v5AcceptFixture) registerDriftedAgentBuild(t *testing.T, adapter *claude.Adapter) string {
	t.Helper()
	ctx := context.Background()
	capabilities, err := adapter.Capabilities(ctx)
	if err != nil {
		t.Fatalf("adapter.Capabilities: %v", err)
	}
	contentHash, err := adapterbuild.HashExecutableFile(v5AcceptFakeClaudeBinary(t))
	if err != nil {
		t.Fatalf("HashExecutableFile: %v", err)
	}
	eventKinds := make([]string, len(capabilities.CanonicalEventKinds))
	for i, kind := range capabilities.CanonicalEventKinds {
		eventKinds[i] = string(kind)
	}
	manifest := domainadapterbuild.CapabilityManifest{
		SupportsStart: capabilities.SupportsStart, SupportsResume: capabilities.SupportsResume, SupportsCancel: capabilities.SupportsCancel,
		CanonicalEventKinds: eventKinds,
	}
	_, manifestHash, err := domainadapterbuild.HashCapabilityManifest(manifest)
	if err != nil {
		t.Fatalf("HashCapabilityManifest: %v", err)
	}
	tuple := domainadapterbuild.CandidateTuple{
		ProviderKey: string(capabilities.Provider), ExecutablePath: v5AcceptFakeClaudeBinary(t),
		// The one deliberate lie: a real live re-probe of this same,
		// untouched binary will only ever derive the REAL contentHash
		// computed above, never this suffixed value — VerifyNoDrift's own
		// Build.ID() comparison is what turns that mismatch into a real
		// rejection.
		ExecutableContentHash: contentHash + "-drifted", ProtocolVersion: capabilities.ProtocolVersion,
		CapabilityManifestHash: manifestHash, OS: stdruntime.GOOS, Toolchain: stdruntime.Version(),
		ConfigIdentity: "v5accept-drifted",
	}
	build, err := domainadapterbuild.NewBuild(domainadapterbuild.NewBuildRequest{
		Tuple: tuple, CapabilityManifest: manifest, RegisteredBy: "operator-1", RegisteredAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("NewBuild: %v", err)
	}
	if err := f.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, _, err := tx.AdapterBuilds().InsertIfAbsent(ctx, build)
		return err
	}); err != nil {
		t.Fatalf("InsertIfAbsent(drifted AdapterBuild): %v", err)
	}
	return build.ID()
}

// v5AcceptAdapterDriftDocument is a minimal real graph — a single real
// AGENT node pinned to the drifted build, no gate: admission never reaches
// a point where a completion policy would matter.
func v5AcceptAdapterDriftDocument(driftedBuildID string) workflow.WorkflowDocument {
	attemptPermissionRefs := []definition.DependencyPin{
		{Kind: definition.KindPolicy, DefinitionID: "v5c-drift-attempt-policy-def", VersionID: "v5c-drift-attempt-policy-v1"},
		{Kind: definition.KindPolicy, DefinitionID: "v5c-drift-permission-policy-def", VersionID: "v5c-drift-permission-policy-v1"},
	}
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{
				Key: "maker", Type: workflow.NodeAgent, Outcomes: []string{"done"},
				Agent: &workflow.AgentNodeConfig{
					ProfileRef:     definition.DependencyPin{Kind: definition.KindAgentProfile, DefinitionID: "v5c-drift-agent-profile-def", VersionID: "v5c-drift-agent-profile-v1"},
					PolicyRefs:     attemptPermissionRefs,
					AdapterBuildID: &driftedBuildID,
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

func TestV5AcceptAdapterDrift_RealAdmissionRejectsMismatchedPin(t *testing.T) {
	f := newV5AcceptFixture(t)
	ctx := context.Background()

	// A real, working fake-claude — if admission ever let this Attempt
	// through, it would really run and really claim "done". The point of
	// this test is that it never gets the chance to.
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
	version := publishWorkflowVersion(t, f.uow, v5AcceptProjectID, "v5c-drift-workflow-def", "v5c-drift-workflow-v1", doc, workflow.DependencyManifest{})

	router := &runtime.NodeExecutorRouter{Agent: agentExecutor}
	registry := f.registerHandlersWithAgents(router, "v5cdr", agentRegistry)
	_, stopPool := f.startPool(t, registry)
	defer stopPool()

	root := f.createRootWorkItem(t, "adapter-drift-root", workdomain.RepositoryRead)
	child := f.createChildWorkItem(t, root.WorkItemID, "adapter-drift-child", workdomain.RepositoryRead)

	startCmd := testCmd("v5c-drift-start", ports.ProjectScope(v5AcceptProjectID), "StartWorkflowRun")
	started, err := runtime.StartWorkflowRun(ctx, f.uow, f.ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: v5AcceptProjectID, WorkItemID: child.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	runID := started.RunID

	nodeRun := f.waitForNodeRunState(t, runID, "maker", runtimedomain.NodeRunBlocked)

	var attempts []runtimedomain.ExecutionAttempt
	var blockers []workdomain.WorkItemBlocker
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		attempts, err = tx.Runtime().ListExecutionAttemptsForRun(ctx, runID)
		if err != nil {
			return err
		}
		blockers, err = tx.Work().ListWorkItemBlockersForWorkItem(ctx, child.WorkItemID)
		return err
	}); err != nil {
		t.Fatalf("read admission-blocked state: %v", err)
	}

	var blockedAttempt *runtimedomain.ExecutionAttempt
	for i := range attempts {
		if attempts[i].NodeRunID == nodeRun.ID {
			blockedAttempt = &attempts[i]
		}
	}
	if blockedAttempt == nil {
		t.Fatalf("no ExecutionAttempt found for node run %s", nodeRun.ID)
	}
	if blockedAttempt.State != runtimedomain.ExecutionAttemptBlocked {
		t.Fatalf("attempt.State = %s, want BLOCKED", blockedAttempt.State)
	}
	if blockedAttempt.TerminationReason != runtimedomain.TerminationReasonAdapterBuildDrift {
		t.Fatalf("attempt.TerminationReason = %s, want %s", blockedAttempt.TerminationReason, runtimedomain.TerminationReasonAdapterBuildDrift)
	}

	foundBlocker := false
	for _, blocker := range blockers {
		if blocker.Type == workdomain.BlockerAdapterBuildDrift {
			foundBlocker = true
			if blocker.State != workdomain.BlockerOpen {
				t.Fatalf("blocker.State = %s, want OPEN", blocker.State)
			}
			if blocker.SourceAttemptID != string(blockedAttempt.ID) {
				t.Fatalf("blocker.SourceAttemptID = %s, want %s", blocker.SourceAttemptID, blockedAttempt.ID)
			}
		}
	}
	if !foundBlocker {
		t.Fatalf("no WorkItemBlocker of type %s found for work item %s; blockers=%+v", workdomain.BlockerAdapterBuildDrift, child.WorkItemID, blockers)
	}
}

// v5AcceptDriftPermissionPolicyDocument mirrors
// v5AcceptPermissionPolicyDocument but pins OperatorTrustedLocal (not
// EnforcedIsolated) — this file's own scenario is about adapter identity,
// not isolation; it must never trip the isolation-unavailable check first
// (admissionPriority checks isolation before adapter drift, admission.go)
// and mask the very rejection this test exists to prove. This package's
// own real handlers here still use the permissive
// fake.IsolationEnforcementChecker{} (registerHandlersWithAgents), which
// is satisfied by either tier — OperatorTrustedLocal is chosen purely for
// documentation clarity, matching what a real process.IsolationChecker{}
// would also honestly allow.
func v5AcceptDriftPermissionPolicyDocument() policy.PolicyDocument {
	return policy.PolicyDocument{
		Category: policy.CategoryPermission,
		Permission: &policy.PermissionRules{
			IsolationTier: policy.IsolationTierOperatorTrustedLocal,
		},
	}
}
