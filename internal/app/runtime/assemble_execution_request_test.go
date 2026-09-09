package runtime_test

import (
	"context"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/artifactstore"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	domainadapterbuild "github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
	domainmessage "github.com/taQuangLing/agent-workflow/internal/domain/message"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	"github.com/taQuangLing/agent-workflow/internal/domain/skill"
)

// V5-08B0: end-to-end tests for AssembleAgentExecutionRequest — a real
// scheduled Attempt (real message, real skill resource via a real
// ContextRoute policy, real pinned AdapterBuild) assembled into a real
// ports.AgentExecutionRequest against a real filesystem ArtifactStore.

func assembleFixtureBuild(t *testing.T) domainadapterbuild.Build {
	t.Helper()
	executablePath := writeAdmissionExecutable(t, "fake-provider-cli-v1")
	return admissionPinnedBuild(t, executablePath, ports.AgentCapabilities{
		Provider: ports.ProviderClaude, ProtocolVersion: "claude-stream-json/v1",
		SupportsStart: true, SupportsResume: true, SupportsCancel: true,
	})
}

func artifactstoreForTest(t *testing.T) ports.ArtifactStore {
	t.Helper()
	store, err := artifactstore.New(t.TempDir())
	if err != nil {
		t.Fatalf("artifactstore.New: %v", err)
	}
	return store
}

// assembleRequestFixture builds one fully-admitted, RUNNING-eligible Attempt
// with a real message, a real Skill resource resolved through a real
// ContextRoute policy, and a real pinned AdapterBuild — everything
// AssembleAgentExecutionRequest needs to succeed. The returned store is the
// SAME instance the fixture's own message content was Put into — a caller
// MUST reuse it (never call artifactstoreForTest again) or message content
// physically will not exist at the Locator the fixture already durably
// recorded.
func assembleRequestFixture(t *testing.T) (uow *fake.UnitOfWork, ids idsource.Source, store ports.ArtifactStore, runID, nodeRunID, attemptID string) {
	t.Helper()
	ctx := context.Background()
	build := assembleFixtureBuild(t)
	buildID := build.ID()
	store = artifactstoreForTest(t)

	u, seq, rID, nrID := scheduleFixture(t, agentExecutableDocument("agent-profile-v1", fullyResolvablePolicyRefs(), &buildID))
	if err := u.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, _, err := tx.AdapterBuilds().InsertIfAbsent(ctx, build)
		return err
	}); err != nil {
		t.Fatalf("register pinned adapter build: %v", err)
	}

	run, err := u.Snapshot.Runtime().GetWorkflowRun(ctx, rID)
	if err != nil {
		t.Fatalf("load run: %v", err)
	}

	msgCmd := testCommand("idem-msg-1", "hash-msg-1", ports.ProjectScope("project-1"), "AppendMessage")
	if _, err := message.AppendMessage(ctx, u, store, seq, clock.System{}, msgCmd, message.AppendMessageRequest{
		ProjectID: "project-1", WorkItemID: string(run.WorkItemID), Role: domainmessage.RoleUser,
		Content: []byte("please implement the feature"), ContentType: "text/plain", Sensitivity: redact.Public,
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	skillDoc := oneResourceSkillDocument("golden-rule", "always follow this", skill.Selector{}, true)
	publishSkillVersion(t, u, "skill-def-1", "skill-v1", skillDoc)
	hash := resourceContentHash(t, "skill-v1", "golden-rule", skillDoc)
	publishPolicyVersion(t, u, "context-policy-def", "context-policy-v1", policy.PolicyDocument{
		Category: policy.CategoryContext,
		Context: &policy.ContextRules{
			Selector: []string{"*"}, Budget: policy.ContextBudget{MaxTokens: 65536},
			ResourceRefs: []policy.ResourceRef{{OwnerVersionID: "skill-v1", ResourceKey: "golden-rule", ContentHash: hash}},
		},
	})
	publishAgentProfileVersionOnly(t, u, "agent-profile-def", "agent-profile-v1", validAgentProfileDocument())
	publishPolicyVersion(t, u, "attempt-policy-def", "attempt-policy-v1", attemptPolicyDocument(600))
	publishPolicyVersion(t, u, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())

	result, err := runtime.ScheduleExecutableNodeRun(ctx, u, seq, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: rID, NodeRunID: nrID, CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("ScheduleExecutableNodeRun: %v", err)
	}
	return u, seq, store, rID, nrID, result.AttemptID
}

func TestAssembleAgentExecutionRequest_RealSnapshot_ProducesValidRequest(t *testing.T) {
	uow, _, store, runID, nodeRunID, attemptID := assembleRequestFixture(t)
	ctx := context.Background()

	req, err := runtime.AssembleAgentExecutionRequest(ctx, uow, store, runtime.AssembleAgentExecutionRequestRequest{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID,
	})
	if err != nil {
		t.Fatalf("AssembleAgentExecutionRequest: %v", err)
	}
	if string(req.AttemptID) != attemptID {
		t.Fatalf("req.AttemptID = %s, want %s", req.AttemptID, attemptID)
	}
	// The Attempt's own pinned ProviderKey comes from validAgentProfileDocument
	// ("fake-provider" — the AgentProfile's own declared provider), never
	// from the separately-pinned AdapterBuild's own Tuple.ProviderKey
	// ("claude", used only for the admission drift-check fixture above) —
	// these are two independent pins (ADR-012's own AgentProfile vs
	// AdapterBuildVersion split) and this assertion exists specifically to
	// catch a regression that conflates them.
	if req.ProviderKey != "fake-provider" {
		t.Fatalf("req.ProviderKey = %s, want fake-provider (from the AgentProfile, not the AdapterBuild)", req.ProviderKey)
	}
	if req.AdapterBuildID == "" {
		t.Fatal("req.AdapterBuildID is empty, want the pinned build's own ID")
	}
	if req.ContextSnapshot == nil || req.ContextSnapshot.ID == "" || req.ContextSnapshot.ManifestHash == "" {
		t.Fatalf("req.ContextSnapshot = %+v, want a populated pin", req.ContextSnapshot)
	}
	if req.InstructionArtifact.Locator == "" || req.InstructionArtifact.SHA256 == "" {
		t.Fatalf("req.InstructionArtifact = %+v, want a populated, durable artifact ref", req.InstructionArtifact)
	}
	if err := store.Verify(ctx, req.InstructionArtifact); err != nil {
		t.Fatalf("store.Verify(InstructionArtifact): %v", err)
	}
	if req.IdempotencyKey != attemptID {
		t.Fatalf("req.IdempotencyKey = %s, want %s", req.IdempotencyKey, attemptID)
	}
	if len(req.WorkspaceMounts) != 1 || req.WorkspaceMounts[0].VCSObjectID == "" {
		t.Fatalf("req.WorkspaceMounts = %+v, want exactly one mount with a real VCSObjectID", req.WorkspaceMounts)
	}
	// V5-08B: agentExecutableDocument's own "implement" node declares
	// Outcomes: []string{"done"} with no CyclePolicy — AllowedOutcomes
	// must be resolved from the pinned WorkflowVersion's own Document,
	// never left empty or hardcoded.
	if len(req.AllowedOutcomes) != 1 || req.AllowedOutcomes[0] != "done" {
		t.Fatalf("req.AllowedOutcomes = %+v, want [done]", req.AllowedOutcomes)
	}

	// Re-assembling from scratch (the "revalidate right before spawn" call
	// a future V5-08B caller makes) must be fully deterministic: identical
	// InstructionArtifact content, byte for byte.
	again, err := runtime.AssembleAgentExecutionRequest(ctx, uow, store, runtime.AssembleAgentExecutionRequestRequest{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID,
	})
	if err != nil {
		t.Fatalf("second AssembleAgentExecutionRequest: %v", err)
	}
	if again.InstructionArtifact.SHA256 != req.InstructionArtifact.SHA256 {
		t.Fatalf("InstructionArtifact.SHA256 changed across identical re-assembly: %s vs %s", req.InstructionArtifact.SHA256, again.InstructionArtifact.SHA256)
	}
}

func TestAssembleAgentExecutionRequest_MismatchedIDs_FailsClosed(t *testing.T) {
	uow, _, store, runID, nodeRunID, attemptID := assembleRequestFixture(t)
	ctx := context.Background()

	if _, err := runtime.AssembleAgentExecutionRequest(ctx, uow, store, runtime.AssembleAgentExecutionRequestRequest{
		RunID: "wrong-run", NodeRunID: nodeRunID, AttemptID: attemptID,
	}); err == nil {
		t.Fatal("AssembleAgentExecutionRequest succeeded with a mismatched RunID, want a fail-closed error")
	}
	if _, err := runtime.AssembleAgentExecutionRequest(ctx, uow, store, runtime.AssembleAgentExecutionRequestRequest{
		RunID: runID, NodeRunID: "wrong-node-run", AttemptID: attemptID,
	}); err == nil {
		t.Fatal("AssembleAgentExecutionRequest succeeded with a mismatched NodeRunID, want a fail-closed error")
	}
}

func TestAssembleAgentExecutionRequest_NoAdapterBuildPinned_FailsClosed(t *testing.T) {
	uow, ids, runID, nodeRunID := scheduleFixture(t, agentExecutableDocument("agent-profile-v1", fullyResolvablePolicyRefs(), nil))
	publishAgentProfileVersion(t, uow, "agent-profile-def", "agent-profile-v1", validAgentProfileDocument())
	publishPolicyVersion(t, uow, "attempt-policy-def", "attempt-policy-v1", attemptPolicyDocument(600))
	publishPolicyVersion(t, uow, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())

	ctx := context.Background()
	result, err := runtime.ScheduleExecutableNodeRun(ctx, uow, ids, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: runID, NodeRunID: nodeRunID, CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("ScheduleExecutableNodeRun: %v", err)
	}

	store := artifactstoreForTest(t)
	if _, err := runtime.AssembleAgentExecutionRequest(ctx, uow, store, runtime.AssembleAgentExecutionRequestRequest{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: result.AttemptID,
	}); err == nil {
		t.Fatal("AssembleAgentExecutionRequest succeeded with no AdapterBuild pinned, want a fail-closed error (go-core-spec §14 requires AdapterBuildVersion)")
	}
}
