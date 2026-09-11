// Package integration — V2-12's own closing gate
// (docs/design/04-v2-definition-plane.md V2-12, AK-ARCH-002, GC-INV-07):
// it closes the whole V2 Definition Plane phase the same way V1-12's own
// TestFoundationCleanStartRestartContract closed V1 — by exercising the
// real, wired-together system end to end (through internal/app's real
// application commands, not internal/domain's pure functions in
// isolation) rather than re-proving anything any single V2 task's own
// unit tests already covered. It publishes a version for every one of
// the nine DefinitionKinds, chains as many of them together as the
// current Alpha schema actually lets a Workflow graph reference,
// registers a real AdapterBuildVersion through the same application
// commands docs/design/04-v2-definition-plane.md's own V2-07B CLI is
// built on, restarts the process, republishes a second WorkflowVersion,
// and confirms the first one never changed — V2-12's own "Hoàn thành
// khi" bar made concrete, not just asserted.
package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	appadapterbuild "github.com/taQuangLing/agent-workflow/internal/app/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/app/definitions"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/domain/agentprofile"
	"github.com/taQuangLing/agent-workflow/internal/domain/block"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/engineeringpack"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
	"github.com/taQuangLing/agent-workflow/internal/domain/layer"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	"github.com/taQuangLing/agent-workflow/internal/domain/skill"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// TestDefinitionPlaneGate is V2-12's own Verify scenario: publish a real
// graph that references every DefinitionKind plus one registered
// AdapterBuildVersion, restart, publish a second version, and prove the
// first version's snapshot never changed while dependency drift, an
// unregistered adapter build, and an invalid executor kind are all
// rejected.
func TestDefinitionPlaneGate(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dbPath := filepath.Join(root, "agentkit.db")

	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open (clean start): %v", err)
	}
	uow := sqlite.NewUnitOfWork(store)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// ---------------------------------------------------------------
	// Clean start: publish a version for every one of the nine
	// DefinitionKinds, chaining as many together as the current Alpha
	// schema actually lets one another reference.
	// ---------------------------------------------------------------

	// SKILL: one global resource, published so a CONTEXT policy below
	// can pin its exact resource identity (ADR-012).
	mustCreateDefinition(t, ctx, uow, definition.KindSkill, "skill-1", "skill-1")
	skillDoc := skill.SkillDocument{
		Resources: []skill.Resource{{
			Key: "setup", Instruction: "Run the setup script before editing.",
			Priority: definition.PriorityGuidance, Global: true,
			Provenance: skill.Provenance{Owner: "platform-team", Source: "onboarding-doc", Revision: "rev-1"},
		}},
	}
	skillVersion := mustPublishSkill(t, ctx, uow, now, "skill-1", "skill-1-v1", skillDoc)
	skillResources, err := skill.ResourceIdentities(skill.SkillVersionID(skillVersion.ID()), skillDoc)
	if err != nil {
		t.Fatalf("skill.ResourceIdentities: %v", err)
	}
	skillResourceHash := skillResources[0].Identity.ContentHash

	// LAYER: one global convention, published standalone — nothing in
	// the compiled Workflow graph schema can reference a Layer directly
	// (V2-08's own design guidance never gives a node a Layer pin), so
	// this proves Layer's own publish pipeline works without claiming
	// graph reachability the schema does not support.
	mustCreateDefinition(t, ctx, uow, definition.KindLayer, "layer-1", "layer-1")
	layerDoc := layer.LayerDocument{
		Resources: []layer.Resource{{
			Key: "style", Convention: "Use gofmt defaults.", Priority: definition.PriorityRequiredProcedure, Global: true,
			Provenance: layer.Provenance{Owner: "platform-team", Source: "style-guide", Revision: "rev-1"},
		}},
	}
	layerVersion := mustPublishLayer(t, ctx, uow, now, "layer-1", "layer-1-v1", layerDoc)

	// ENGINEERING_PACK: depends on both Skill and Layer above — the one
	// existing, legitimate way Skill/Layer participate in a larger
	// definition graph (V2-06's own dependency-pin mechanism), even
	// though nothing in a Workflow graph can reach an EngineeringPack.
	mustCreateDefinition(t, ctx, uow, definition.KindEngineeringPack, "pack-1", "pack-1")
	packDoc := engineeringpack.EngineeringPackDocument{
		Dependencies: []definition.DependencyPin{
			{Kind: definition.KindSkill, DefinitionID: "skill-1", VersionID: skillVersion.ID()},
			{Kind: definition.KindLayer, DefinitionID: "layer-1", VersionID: layerVersion.ID()},
		},
	}
	mustPublishEngineeringPack(t, ctx, uow, now, "pack-1", "pack-1-v1", packDoc)

	// POLICY (CONTEXT): pins the Skill resource's exact identity
	// (ADR-012's "Context route ... pin selector/order/budget và
	// resource identities").
	mustCreateDefinition(t, ctx, uow, definition.KindPolicy, "policy-context-1", "policy-context-1")
	contextPolicyDoc := policy.PolicyDocument{
		Category: policy.CategoryContext,
		Context: &policy.ContextRules{
			Selector: []string{"setup"}, Order: []string{"setup"},
			Budget:       policy.ContextBudget{MaxTokens: 2000},
			ResourceRefs: []policy.ResourceRef{{OwnerVersionID: skillVersion.ID(), ResourceKey: "setup", ContentHash: skillResourceHash}},
		},
	}
	contextPolicyVersion := mustPublishPolicy(t, ctx, uow, now, "policy-context-1", "policy-context-1-v1", contextPolicyDoc)

	// POLICY (ATTEMPT): pinned by the AGENT node below.
	mustCreateDefinition(t, ctx, uow, definition.KindPolicy, "policy-attempt-1", "policy-attempt-1")
	attemptPolicyDoc := policy.PolicyDocument{
		Category: policy.CategoryAttempt,
		Attempt:  &policy.AttemptRules{MaxAttempts: 3, BackoffSeconds: 5, TimeoutSeconds: 120},
	}
	attemptPolicyVersion := mustPublishPolicy(t, ctx, uow, now, "policy-attempt-1", "policy-attempt-1-v1", attemptPolicyDoc)

	// POLICY (PERMISSION): pinned by the MACHINE_GATE node below.
	mustCreateDefinition(t, ctx, uow, definition.KindPolicy, "policy-permission-1", "policy-permission-1")
	permissionPolicyDoc := policy.PolicyDocument{
		Category:   policy.CategoryPermission,
		Permission: &policy.PermissionRules{IsolationTier: policy.IsolationTierOperatorTrustedLocal},
	}
	permissionPolicyVersion := mustPublishPolicy(t, ctx, uow, now, "policy-permission-1", "policy-permission-1-v1", permissionPolicyDoc)

	// AGENT_PROFILE: pins the CONTEXT policy above.
	mustCreateDefinition(t, ctx, uow, definition.KindAgentProfile, "profile-1", "profile-1")
	profileDoc := agentprofile.AgentProfileDocument{
		ProviderKey: "claude", Model: "claude-test-model",
		ContextPolicyRef: definition.DependencyPin{Kind: definition.KindPolicy, DefinitionID: "policy-context-1", VersionID: contextPolicyVersion.ID()},
		Compatibility:    agentprofile.Compatibility{OS: []string{"linux", "windows"}},
		Budget:           agentprofile.Budget{MaxTokens: 100000},
	}
	profileVersion := mustPublishAgentProfile(t, ctx, uow, now, "profile-1", "profile-1-v1", profileDoc)

	// COMMAND: pins the Skill resource above as its own executable
	// content (ADR-012's resource identity, reused across kinds by
	// design).
	mustCreateDefinition(t, ctx, uow, definition.KindCommand, "command-1", "command-1")
	commandDocV1 := command.CommandDocument{
		Executable:          command.ExecutableRef{OwnerVersionID: skillVersion.ID(), ResourceKey: "setup", ContentHash: skillResourceHash},
		Argv:                []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "setup.sh"}},
		CwdRepositoryTarget: "repo-primary", Compatibility: command.Compatibility{OS: []string{"linux"}},
		NetworkAccess: command.NetworkAccessNone, TimeoutSeconds: 60,
		Output: command.OutputContract{CaptureStdout: true, MaxOutputBytes: 1 << 16},
	}
	commandVersionV1 := mustPublishCommand(t, ctx, uow, now, "command-1", "command-1-v1", commandDocV1)

	// GATE: pins the Command above.
	mustCreateDefinition(t, ctx, uow, definition.KindGate, "gate-1", "gate-1")
	gateDoc := gate.GateDocument{
		CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "command-1", VersionID: commandVersionV1.ID()},
		Criteria:   []gate.Criterion{{Name: "exit-code-zero", EvidenceKey: "exitCode"}},
	}
	gateVersion := mustPublishGate(t, ctx, uow, now, "gate-1", "gate-1-v1", gateDoc)

	// BLOCK: pins the Command above as its executor — published
	// standalone, proving Block's own pipeline works, even though
	// V2-08's Workflow schema never lets a node pin a Block directly
	// (nodes pin AgentProfile/Command/Gate directly instead).
	mustCreateDefinition(t, ctx, uow, definition.KindBlock, "block-1", "block-1")
	blockDoc := block.BlockDocument{
		CompatibleNodeTypes: []block.NodeType{block.NodeTypeCommand},
		TimeoutSeconds:      60,
		ScopeSelector:       block.ScopeSelector{Access: block.ScopeAccessWrite, PathScopes: []string{"src/**"}},
		DoneCondition:       "result.outcome",
		ExecutorRef:         definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "command-1", VersionID: commandVersionV1.ID()},
		Outcomes:            []string{"success", "failure"},
	}
	mustPublishBlock(t, ctx, uow, now, "block-1", "block-1-v1", blockDoc)

	// ADAPTER_BUILD_VERSION: registered through the exact same
	// application commands docs/design/04-v2-definition-plane.md's own
	// V2-07B CLI (agentkit adapter probe|register) is built on top of.
	executablePath := filepath.Join(root, "provider-cli")
	if err := os.WriteFile(executablePath, []byte("fake-provider-binary-v1"), 0o644); err != nil {
		t.Fatalf("write fake provider executable: %v", err)
	}
	capabilityManifest := adapterbuild.CapabilityManifest{SupportsStart: true, SupportsResume: true, SupportsCancel: true}
	probeToken, err := appadapterbuild.ProbeAdapterBuild(ctx, uow, appadapterbuild.ProbeRequest{
		ProviderKey: "claude", ExecutablePath: executablePath, ProtocolVersion: "claude-stream-json/v1",
		CapabilityManifest: capabilityManifest, OS: "linux", Toolchain: "node-20", ConfigIdentity: "default",
	})
	if err != nil {
		t.Fatalf("ProbeAdapterBuild: %v", err)
	}
	registerResult, err := appadapterbuild.RegisterAdapterBuild(ctx, uow, appadapterbuild.RegisterRequest{
		Token: probeToken, CapabilityManifest: capabilityManifest, RegisteredBy: "operator-gate",
	})
	if err != nil {
		t.Fatalf("RegisterAdapterBuild: %v", err)
	}
	buildID := registerResult.Build.ID()

	// WORKFLOW: references AGENT_PROFILE, COMMAND, GATE and (via
	// PolicyRefs) both remaining POLICY versions, plus the registered
	// AdapterBuildVersion — every DefinitionKind a compiled Workflow
	// graph can structurally reach, per V2-08's own schema.
	workflowDoc := func(commandVersionID string) workflow.WorkflowDocument {
		return workflow.WorkflowDocument{
			SchemaVersion: "1",
			Nodes: []workflow.Node{
				{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"go"}},
				{
					Key: "agent", Type: workflow.NodeAgent, Outcomes: []string{"done"},
					Agent: &workflow.AgentNodeConfig{
						ProfileRef:     definition.DependencyPin{Kind: definition.KindAgentProfile, DefinitionID: "profile-1", VersionID: profileVersion.ID()},
						Role:           workflow.AgentRoleMaker,
						PolicyRefs:     []definition.DependencyPin{{Kind: definition.KindPolicy, DefinitionID: "policy-attempt-1", VersionID: attemptPolicyVersion.ID()}},
						AdapterBuildID: &buildID,
					},
				},
				{
					Key: "command", Type: workflow.NodeCommand, Outcomes: []string{"pass"},
					Command: &workflow.CommandNodeConfig{
						CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "command-1", VersionID: commandVersionID},
					},
				},
				{
					Key: "gate", Type: workflow.NodeMachineGate, Outcomes: []string{"verified"},
					MachineGate: &workflow.MachineGateNodeConfig{
						GateRef:    definition.DependencyPin{Kind: definition.KindGate, DefinitionID: "gate-1", VersionID: gateVersion.ID()},
						PolicyRefs: []definition.DependencyPin{{Kind: definition.KindPolicy, DefinitionID: "policy-permission-1", VersionID: permissionPolicyVersion.ID()}},
					},
				},
				{Key: "end", Type: workflow.NodeEnd},
			},
			Edges: []workflow.Edge{
				{Key: "e1", From: "start", Outcome: "go", To: "agent"},
				{Key: "e2", From: "agent", Outcome: "done", To: "command"},
				{Key: "e3", From: "command", Outcome: "pass", To: "gate"},
				{Key: "e4", From: "gate", Outcome: "verified", To: "end"},
			},
		}
	}

	workflowDef := workflow.WorkflowDefinition{ID: "gate-workflow", Name: "V2-12 gate workflow", Status: workflow.DefinitionDraft, Version: 1}
	mustCreateDefinition(t, ctx, uow, definition.KindWorkflow, "gate-workflow", "V2-12 gate workflow")

	v1Fields, err := definitions.PublishDefinitionVersion(ctx, uow, gateCommand("publish-workflow-v1"), definitions.PublishDefinitionVersionRequest{
		DefinitionID: "gate-workflow", Kind: definition.KindWorkflow,
		WorkflowDefinition: workflowDef,
		WorkflowRequest: workflow.PublishRequest{
			VersionID: "gate-workflow-v1", VersionNumber: 1, Document: workflowDoc(commandVersionV1.ID()),
			PublishedBy: "operator-gate", PublishedAt: now,
		},
	})
	if err != nil {
		t.Fatalf("publish workflow V1: %v", err)
	}

	// The compiled graph structurally reaches every kind V2-08's own
	// node schema can pin: AGENT_PROFILE, COMMAND, GATE, POLICY, and —
	// via AdapterBuildID — the registered AdapterBuildVersion.
	deps := v1Fields.Dependencies()
	wantKinds := map[definition.Kind]bool{
		definition.KindAgentProfile: false, definition.KindCommand: false,
		definition.KindGate: false, definition.KindPolicy: false,
	}
	hasAdapterBuildPin := false
	for _, pin := range deps.Pins {
		if _, tracked := wantKinds[pin.Kind]; tracked {
			wantKinds[pin.Kind] = true
		}
		if string(pin.Kind) == "ADAPTER_BUILD_VERSION" {
			hasAdapterBuildPin = true
		}
	}
	for kind, found := range wantKinds {
		if !found {
			t.Fatalf("compiled V1 dependency manifest is missing a %s pin: %+v", kind, deps.Pins)
		}
	}
	if !hasAdapterBuildPin {
		t.Fatalf("compiled V1 dependency manifest is missing the adapter build pin: %+v", deps.Pins)
	}

	// ---------------------------------------------------------------
	// Restart (mirrors V1-12's own TestFoundationCleanStartRestartContract).
	// ---------------------------------------------------------------
	if err := store.Close(); err != nil {
		t.Fatalf("Close (simulated restart): %v", err)
	}
	store2, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open (restart): %v", err)
	}
	defer store2.Close()
	uow2 := sqlite.NewUnitOfWork(store2)

	// ---------------------------------------------------------------
	// Publish V2: pin a genuinely different Command version — proving
	// AK-ARCH-005B/ADR-012 (a changed dependency pin forces a new
	// CompiledSnapshotHash) after a real restart.
	// ---------------------------------------------------------------
	commandDocV2 := commandDocV1
	commandDocV2.Argv = []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "setup-v2.sh"}}
	commandVersionV2 := mustPublishCommand(t, ctx, uow2, now.Add(time.Hour), "command-1", "command-1-v2", commandDocV2)
	if commandVersionV2.ID() == commandVersionV1.ID() {
		t.Fatal("test invariant broken: command V2 must be a genuinely different published version")
	}

	v2Fields, err := definitions.PublishDefinitionVersion(ctx, uow2, gateCommand("publish-workflow-v2"), definitions.PublishDefinitionVersionRequest{
		DefinitionID: "gate-workflow", Kind: definition.KindWorkflow,
		WorkflowDefinition: workflowDef,
		WorkflowRequest: workflow.PublishRequest{
			VersionID: "gate-workflow-v2", VersionNumber: 2, Document: workflowDoc(commandVersionV2.ID()),
			PublishedBy: "operator-gate", PublishedAt: now.Add(time.Hour),
		},
	})
	if err != nil {
		t.Fatalf("publish workflow V2: %v", err)
	}
	if v2Fields.ID() == v1Fields.ID() {
		t.Fatal("V2 must be a distinct, new version from V1")
	}
	if v2Fields.CompiledHash() == v1Fields.CompiledHash() {
		t.Fatal("changing a pinned dependency (Command V1 -> V2) must change the compiled snapshot hash")
	}

	// ---------------------------------------------------------------
	// Load V1 exact: publishing V2 must never change V1's own snapshot
	// (AK-ARCH-002/GC-INV-07 — "publish version mới không thay graph
	// hoặc dependency của run cũ").
	// ---------------------------------------------------------------
	reloadedV1, err := store2.LoadWorkflowVersion(ctx, workflow.WorkflowVersionID(v1Fields.ID()))
	if err != nil {
		t.Fatalf("load V1 after publishing V2: %v", err)
	}
	if reloadedV1.ContentHash() != v1Fields.CompiledHash() {
		t.Fatalf("V1's content hash changed after publishing V2: was %s, now %s", v1Fields.CompiledHash(), reloadedV1.ContentHash())
	}
	reloadedV1Manifest := reloadedV1.Dependencies()
	if len(reloadedV1Manifest.Pins) != len(deps.Pins) {
		t.Fatalf("V1's own dependency manifest changed after publishing V2: had %d pins, now %d", len(deps.Pins), len(reloadedV1Manifest.Pins))
	}
	for _, pin := range reloadedV1Manifest.Pins {
		if pin.Kind == "COMMAND" && pin.Version != commandVersionV1.ID() {
			t.Fatalf("V1's own Command pin silently repointed to %s (want it to stay pinned at %s)", pin.Version, commandVersionV1.ID())
		}
	}

	// ---------------------------------------------------------------
	// Reject: dependency drift — a workflow node pinning a Command
	// version that was never published.
	// ---------------------------------------------------------------
	driftDoc := workflowDoc(commandVersionV1.ID())
	driftDoc.Nodes[2].Command.CommandRef.VersionID = "command-1-does-not-exist"
	_, err = definitions.ValidateDraft(ctx, uow2, definitions.ValidateDraftRequest{
		Kind: definition.KindWorkflow, WorkflowDefinition: workflowDef,
		WorkflowRequest: workflow.PublishRequest{
			VersionID: "gate-workflow-drift", VersionNumber: 3, Document: driftDoc,
			PublishedBy: "operator-gate", PublishedAt: now,
		},
	})
	if err == nil {
		t.Fatal("a workflow pinning a Command version that was never published should be rejected")
	}

	// ---------------------------------------------------------------
	// Reject: adapter drift — a workflow node pinning an AdapterBuildID
	// that was never registered.
	// ---------------------------------------------------------------
	unknownBuildID := "sha256:never-registered"
	adapterDriftDoc := workflowDoc(commandVersionV1.ID())
	adapterDriftDoc.Nodes[1].Agent.AdapterBuildID = &unknownBuildID
	_, err = definitions.ValidateDraft(ctx, uow2, definitions.ValidateDraftRequest{
		Kind: definition.KindWorkflow, WorkflowDefinition: workflowDef,
		WorkflowRequest: workflow.PublishRequest{
			VersionID: "gate-workflow-adapter-drift", VersionNumber: 3, Document: adapterDriftDoc,
			PublishedBy: "operator-gate", PublishedAt: now,
		},
	})
	if err == nil {
		t.Fatal("a workflow pinning an unregistered AdapterBuildID should be rejected")
	}

	// ---------------------------------------------------------------
	// Reject: invalid executable authority — an AGENT node's executor
	// pin naming a Skill instead of an AgentProfile (Skill/Layer are
	// architecturally passive resources, never executors — the same
	// rule internal/domain/block's own ValidateDocument already
	// enforces at the Block layer, proven here end to end at the
	// Workflow layer too).
	// ---------------------------------------------------------------
	invalidExecutorDoc := workflowDoc(commandVersionV1.ID())
	invalidExecutorDoc.Nodes[1].Agent.ProfileRef = definition.DependencyPin{Kind: definition.KindSkill, DefinitionID: "skill-1", VersionID: skillVersion.ID()}
	_, err = definitions.ValidateDraft(ctx, uow2, definitions.ValidateDraftRequest{
		Kind: definition.KindWorkflow, WorkflowDefinition: workflowDef,
		WorkflowRequest: workflow.PublishRequest{
			VersionID: "gate-workflow-invalid-executor", VersionNumber: 3, Document: invalidExecutorDoc,
			PublishedBy: "operator-gate", PublishedAt: now,
		},
	})
	if err == nil {
		t.Fatal("an AGENT node pinning a Skill as its executor should be rejected")
	}
}

func gateCommand(id string) ports.Command {
	return ports.Command{
		ID: id, IdempotencyKey: id, Actor: "operator-gate",
		CorrelationID: id, Scope: ports.InstallationScope(),
		RequestedAt: time.Now(), Type: "integration.definitionplane", RequestHash: id,
	}
}

func mustCreateDefinition(t *testing.T, ctx context.Context, uow ports.UnitOfWork, kind definition.Kind, id, name string) {
	t.Helper()
	if _, err := definitions.CreateDefinition(ctx, uow, gateCommand("create-"+id), definitions.CreateDefinitionRequest{
		DefinitionID: id, Kind: kind, Scope: definition.GlobalScope(), Name: name,
	}); err != nil {
		t.Fatalf("CreateDefinition(%s, %s): %v", kind, id, err)
	}
}

func draftFields(kind definition.Kind, name string) definition.Fields {
	return definition.Fields{Kind: kind, Scope: definition.GlobalScope(), Name: name, Status: definition.StatusDraft, Version: 1}
}

func mustPublishSkill(t *testing.T, ctx context.Context, uow ports.UnitOfWork, now time.Time, id, versionID string, doc skill.SkillDocument) definition.VersionFields {
	t.Helper()
	compile := func() (definition.VersionFields, error) {
		return skill.Compile(skill.SkillDefinition{ID: skill.SkillDefinitionID(id), Fields: draftFields(definition.KindSkill, id)}, skill.PublishRequest{
			VersionID: skill.SkillVersionID(versionID), VersionNumber: 1, SchemaVersion: 1,
			Document: doc, PublishedBy: "operator-gate", PublishedAt: now,
		})
	}
	v, err := definitions.PublishDefinitionVersion(ctx, uow, gateCommand("publish-"+versionID), definitions.PublishDefinitionVersionRequest{
		DefinitionID: id, Kind: definition.KindSkill, Compile: compile,
	})
	if err != nil {
		t.Fatalf("publish skill %s: %v", id, err)
	}
	return v
}

func mustPublishLayer(t *testing.T, ctx context.Context, uow ports.UnitOfWork, now time.Time, id, versionID string, doc layer.LayerDocument) definition.VersionFields {
	t.Helper()
	compile := func() (definition.VersionFields, error) {
		return layer.Compile(layer.LayerDefinition{ID: layer.LayerDefinitionID(id), Fields: draftFields(definition.KindLayer, id)}, layer.PublishRequest{
			VersionID: layer.LayerVersionID(versionID), VersionNumber: 1, SchemaVersion: 1,
			Document: doc, PublishedBy: "operator-gate", PublishedAt: now,
		})
	}
	v, err := definitions.PublishDefinitionVersion(ctx, uow, gateCommand("publish-"+versionID), definitions.PublishDefinitionVersionRequest{
		DefinitionID: id, Kind: definition.KindLayer, Compile: compile,
	})
	if err != nil {
		t.Fatalf("publish layer %s: %v", id, err)
	}
	return v
}

func mustPublishEngineeringPack(t *testing.T, ctx context.Context, uow ports.UnitOfWork, now time.Time, id, versionID string, doc engineeringpack.EngineeringPackDocument) definition.VersionFields {
	t.Helper()
	compile := func() (definition.VersionFields, error) {
		return engineeringpack.Compile(engineeringpack.EngineeringPackDefinition{ID: engineeringpack.EngineeringPackDefinitionID(id), Fields: draftFields(definition.KindEngineeringPack, id)}, engineeringpack.PublishRequest{
			VersionID: engineeringpack.EngineeringPackVersionID(versionID), VersionNumber: 1, SchemaVersion: 1,
			Document: doc, PublishedBy: "operator-gate", PublishedAt: now,
		})
	}
	v, err := definitions.PublishDefinitionVersion(ctx, uow, gateCommand("publish-"+versionID), definitions.PublishDefinitionVersionRequest{
		DefinitionID: id, Kind: definition.KindEngineeringPack, Compile: compile,
	})
	if err != nil {
		t.Fatalf("publish engineering pack %s: %v", id, err)
	}
	return v
}

func mustPublishPolicy(t *testing.T, ctx context.Context, uow ports.UnitOfWork, now time.Time, id, versionID string, doc policy.PolicyDocument) definition.VersionFields {
	t.Helper()
	compile := func() (definition.VersionFields, error) {
		return policy.Compile(policy.PolicyDefinition{ID: policy.PolicyDefinitionID(id), Fields: draftFields(definition.KindPolicy, id)}, policy.PublishRequest{
			VersionID: policy.PolicyVersionID(versionID), VersionNumber: 1, SchemaVersion: 1,
			Document: doc, PublishedBy: "operator-gate", PublishedAt: now,
		})
	}
	v, err := definitions.PublishDefinitionVersion(ctx, uow, gateCommand("publish-"+versionID), definitions.PublishDefinitionVersionRequest{
		DefinitionID: id, Kind: definition.KindPolicy, Compile: compile,
	})
	if err != nil {
		t.Fatalf("publish policy %s: %v", id, err)
	}
	return v
}

func mustPublishAgentProfile(t *testing.T, ctx context.Context, uow ports.UnitOfWork, now time.Time, id, versionID string, doc agentprofile.AgentProfileDocument) definition.VersionFields {
	t.Helper()
	compile := func() (definition.VersionFields, error) {
		return agentprofile.Compile(agentprofile.AgentProfileDefinition{ID: agentprofile.AgentProfileDefinitionID(id), Fields: draftFields(definition.KindAgentProfile, id)}, agentprofile.PublishRequest{
			VersionID: agentprofile.AgentProfileVersionID(versionID), VersionNumber: 1, SchemaVersion: 1,
			Document: doc, PublishedBy: "operator-gate", PublishedAt: now,
		})
	}
	v, err := definitions.PublishDefinitionVersion(ctx, uow, gateCommand("publish-"+versionID), definitions.PublishDefinitionVersionRequest{
		DefinitionID: id, Kind: definition.KindAgentProfile, Compile: compile,
	})
	if err != nil {
		t.Fatalf("publish agent profile %s: %v", id, err)
	}
	return v
}

func mustPublishCommand(t *testing.T, ctx context.Context, uow ports.UnitOfWork, now time.Time, id, versionID string, doc command.CommandDocument) definition.VersionFields {
	t.Helper()
	compile := func() (definition.VersionFields, error) {
		return command.Compile(command.CommandDefinition{ID: command.CommandDefinitionID(id), Fields: draftFields(definition.KindCommand, id)}, command.PublishRequest{
			VersionID: command.CommandVersionID(versionID), VersionNumber: 1, SchemaVersion: 1,
			Document: doc, PublishedBy: "operator-gate", PublishedAt: now,
		})
	}
	v, err := definitions.PublishDefinitionVersion(ctx, uow, gateCommand("publish-"+versionID), definitions.PublishDefinitionVersionRequest{
		DefinitionID: id, Kind: definition.KindCommand, Compile: compile,
	})
	if err != nil {
		t.Fatalf("publish command %s (%s): %v", id, versionID, err)
	}
	return v
}

func mustPublishGate(t *testing.T, ctx context.Context, uow ports.UnitOfWork, now time.Time, id, versionID string, doc gate.GateDocument) definition.VersionFields {
	t.Helper()
	compile := func() (definition.VersionFields, error) {
		return gate.Compile(gate.GateDefinition{ID: gate.GateDefinitionID(id), Fields: draftFields(definition.KindGate, id)}, gate.PublishRequest{
			VersionID: gate.GateVersionID(versionID), VersionNumber: 1, SchemaVersion: 1,
			Document: doc, PublishedBy: "operator-gate", PublishedAt: now,
		})
	}
	v, err := definitions.PublishDefinitionVersion(ctx, uow, gateCommand("publish-"+versionID), definitions.PublishDefinitionVersionRequest{
		DefinitionID: id, Kind: definition.KindGate, Compile: compile,
	})
	if err != nil {
		t.Fatalf("publish gate %s: %v", id, err)
	}
	return v
}

func mustPublishBlock(t *testing.T, ctx context.Context, uow ports.UnitOfWork, now time.Time, id, versionID string, doc block.BlockDocument) definition.VersionFields {
	t.Helper()
	compile := func() (definition.VersionFields, error) {
		return block.Compile(block.BlockDefinition{ID: block.BlockDefinitionID(id), Fields: draftFields(definition.KindBlock, id)}, block.PublishRequest{
			VersionID: block.BlockVersionID(versionID), VersionNumber: 1, SchemaVersion: 1,
			Document: doc, PublishedBy: "operator-gate", PublishedAt: now,
		})
	}
	v, err := definitions.PublishDefinitionVersion(ctx, uow, gateCommand("publish-"+versionID), definitions.PublishDefinitionVersionRequest{
		DefinitionID: id, Kind: definition.KindBlock, Compile: compile,
	})
	if err != nil {
		t.Fatalf("publish block %s: %v", id, err)
	}
	return v
}
