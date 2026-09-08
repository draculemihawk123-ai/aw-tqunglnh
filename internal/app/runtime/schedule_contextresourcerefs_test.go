package runtime_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/definitions"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/layer"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	"github.com/taQuangLing/agent-workflow/internal/domain/skill"
)

// V5-08B0: these tests prove ScheduleExecutableNodeRun's own real candidate
// gathering (schedule.go's gatherContextResourceRefs/loadResourceCandidate)
// — replacing V5-04's own hardcoded ResourceRefs: nil — actually resolves a
// Skill's real, published resource through the AgentProfile's own
// ContextPolicyRef -> CONTEXT policy -> Skill chain, fails closed on a
// tampered pin, and excludes a candidate whose Selector does not match.

// oneResourceSkillDocument builds a SkillDocument with exactly one resource,
// Global (so no Selector is required for it to apply) unless a selector is
// supplied.
func oneResourceSkillDocument(key, instruction string, sel skill.Selector, global bool) skill.SkillDocument {
	return skill.SkillDocument{Resources: []skill.Resource{{
		Key: key, Instruction: instruction, Priority: definition.PriorityGuidance,
		Global: global, Selector: sel,
		Provenance: skill.Provenance{Owner: "team-x", Source: "doc-1", Revision: "v1"},
	}}}
}

func publishSkillVersion(t *testing.T, uow ports.UnitOfWork, definitionID, versionID string, doc skill.SkillDocument) {
	t.Helper()
	ctx := context.Background()
	createCmd := testCommand("idem-def-"+definitionID, "hash-def-"+definitionID, ports.InstallationScope(), "CreateDefinition")
	if _, err := definitions.CreateDefinition(ctx, uow, createCmd, definitions.CreateDefinitionRequest{
		DefinitionID: definitionID, Kind: definition.KindSkill, Scope: definition.GlobalScope(), Name: "skill " + definitionID,
	}); err != nil {
		t.Fatalf("CreateDefinition(%s): %v", definitionID, err)
	}
	publishCmd := testCommand("idem-pub-"+versionID, "hash-pub-"+versionID, ports.InstallationScope(), "PublishDefinitionVersion")
	if _, err := definitions.PublishDefinitionVersion(ctx, uow, publishCmd, definitions.PublishDefinitionVersionRequest{
		DefinitionID: definitionID, Kind: definition.KindSkill,
		Compile: func() (definition.VersionFields, error) {
			return skill.Compile(
				skill.SkillDefinition{
					ID: skill.SkillDefinitionID(definitionID),
					Fields: definition.Fields{
						Kind: definition.KindSkill, Scope: definition.GlobalScope(),
						Name: "skill", Status: definition.StatusDraft, Version: 1,
					},
				},
				skill.PublishRequest{
					VersionID: skill.SkillVersionID(versionID), VersionNumber: 1, SchemaVersion: 1,
					Document: doc, PublishedBy: "operator-1", PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				},
			)
		},
	}); err != nil {
		t.Fatalf("PublishDefinitionVersion(%s): %v", versionID, err)
	}
}

// resourceContentHash re-derives skill.ResourceIdentities' own ContentHash
// for one resource — a test-only stand-in for "whatever a real ContextRoute
// policy's own compiler would have pinned at authoring time".
func resourceContentHash(t *testing.T, ownerVersionID, resourceKey string, doc skill.SkillDocument) string {
	t.Helper()
	identities, err := skill.ResourceIdentities(skill.SkillVersionID(ownerVersionID), doc)
	if err != nil {
		t.Fatalf("ResourceIdentities: %v", err)
	}
	for _, id := range identities {
		if id.Identity.ResourceKey == resourceKey {
			return id.Identity.ContentHash
		}
	}
	t.Fatalf("resource key %s not found in computed identities", resourceKey)
	return ""
}

func TestScheduleExecutableNodeRun_GathersRealResourceRefsFromContextRoute(t *testing.T) {
	uow, ids, runID, nodeRunID := scheduleFixture(t, agentExecutableDocument("agent-profile-v1", fullyResolvablePolicyRefs(), nil))

	skillDoc := oneResourceSkillDocument("golden-rule", "always follow this", skill.Selector{}, true)
	publishSkillVersion(t, uow, "skill-def-1", "skill-v1", skillDoc)
	hash := resourceContentHash(t, "skill-v1", "golden-rule", skillDoc)

	publishPolicyVersion(t, uow, "context-policy-def", "context-policy-v1", policy.PolicyDocument{
		Category: policy.CategoryContext,
		Context: &policy.ContextRules{
			Selector: []string{"*"},
			Budget:   policy.ContextBudget{MaxTokens: 65536},
			ResourceRefs: []policy.ResourceRef{
				{OwnerVersionID: "skill-v1", ResourceKey: "golden-rule", ContentHash: hash},
			},
		},
	})
	publishAgentProfileVersionOnly(t, uow, "agent-profile-def", "agent-profile-v1", validAgentProfileDocument())
	publishPolicyVersion(t, uow, "attempt-policy-def", "attempt-policy-v1", attemptPolicyDocument(600))
	publishPolicyVersion(t, uow, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())

	ctx := context.Background()
	result, err := runtime.ScheduleExecutableNodeRun(ctx, uow, ids, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: runID, NodeRunID: nodeRunID, CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("ScheduleExecutableNodeRun: %v", err)
	}

	attempt, err := uow.Snapshot.Runtime().GetExecutionAttempt(ctx, result.AttemptID)
	if err != nil {
		t.Fatalf("GetExecutionAttempt: %v", err)
	}
	if attempt.ContextSnapshotID == nil {
		t.Fatal("attempt.ContextSnapshotID is nil, want the bound V5-04 snapshot")
	}
	snapshot, err := uow.Snapshot.ContextSnapshots().GetSnapshot(ctx, string(*attempt.ContextSnapshotID))
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}
	if len(snapshot.ResourceRefs) != 1 {
		t.Fatalf("snapshot.ResourceRefs = %+v, want exactly 1 resolved candidate", snapshot.ResourceRefs)
	}
	got := snapshot.ResourceRefs[0]
	if got.OwnerVersionID != "skill-v1" || got.ResourceKey != "golden-rule" || got.ContentHash != hash {
		t.Fatalf("snapshot.ResourceRefs[0] = %+v, want OwnerVersionID=skill-v1 ResourceKey=golden-rule ContentHash=%s", got, hash)
	}
}

func TestScheduleExecutableNodeRun_ContextRoute_ExcludesNonMatchingSelector(t *testing.T) {
	uow, ids, runID, nodeRunID := scheduleFixture(t, agentExecutableDocument("agent-profile-v1", fullyResolvablePolicyRefs(), nil))

	// Not global, and its own Selector names a TaskKind the scheduling
	// WorkItem never declares — MatchesContext must be false, so this
	// resource is Excluded (NOT_APPLICABLE), never Selected.
	skillDoc := oneResourceSkillDocument("niche-rule", "only for a task kind that never occurs here",
		skill.Selector{TaskKinds: []string{"never-matches-anything"}}, false)
	publishSkillVersion(t, uow, "skill-def-1", "skill-v1", skillDoc)
	hash := resourceContentHash(t, "skill-v1", "niche-rule", skillDoc)

	publishPolicyVersion(t, uow, "context-policy-def", "context-policy-v1", policy.PolicyDocument{
		Category: policy.CategoryContext,
		Context: &policy.ContextRules{
			Selector: []string{"*"},
			Budget:   policy.ContextBudget{MaxTokens: 65536},
			ResourceRefs: []policy.ResourceRef{
				{OwnerVersionID: "skill-v1", ResourceKey: "niche-rule", ContentHash: hash},
			},
		},
	})
	publishAgentProfileVersionOnly(t, uow, "agent-profile-def", "agent-profile-v1", validAgentProfileDocument())
	publishPolicyVersion(t, uow, "attempt-policy-def", "attempt-policy-v1", attemptPolicyDocument(600))
	publishPolicyVersion(t, uow, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())

	ctx := context.Background()
	result, err := runtime.ScheduleExecutableNodeRun(ctx, uow, ids, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: runID, NodeRunID: nodeRunID, CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("ScheduleExecutableNodeRun: %v", err)
	}
	attempt, err := uow.Snapshot.Runtime().GetExecutionAttempt(ctx, result.AttemptID)
	if err != nil {
		t.Fatalf("GetExecutionAttempt: %v", err)
	}
	snapshot, err := uow.Snapshot.ContextSnapshots().GetSnapshot(ctx, string(*attempt.ContextSnapshotID))
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}
	if len(snapshot.ResourceRefs) != 0 {
		t.Fatalf("snapshot.ResourceRefs = %+v, want none selected (Selector never matches)", snapshot.ResourceRefs)
	}
}

func TestScheduleExecutableNodeRun_ContextRoute_TamperedContentHash_FailsClosed(t *testing.T) {
	uow, ids, runID, nodeRunID := scheduleFixture(t, agentExecutableDocument("agent-profile-v1", fullyResolvablePolicyRefs(), nil))

	skillDoc := oneResourceSkillDocument("golden-rule", "always follow this", skill.Selector{}, true)
	publishSkillVersion(t, uow, "skill-def-1", "skill-v1", skillDoc)

	publishPolicyVersion(t, uow, "context-policy-def", "context-policy-v1", policy.PolicyDocument{
		Category: policy.CategoryContext,
		Context: &policy.ContextRules{
			Selector: []string{"*"},
			Budget:   policy.ContextBudget{MaxTokens: 65536},
			ResourceRefs: []policy.ResourceRef{
				// Deliberately wrong — simulates the Skill's content having
				// changed (or the policy having been authored against stale
				// content) since this route was published.
				{OwnerVersionID: "skill-v1", ResourceKey: "golden-rule", ContentHash: "sha256:not-the-real-hash"},
			},
		},
	})
	publishAgentProfileVersionOnly(t, uow, "agent-profile-def", "agent-profile-v1", validAgentProfileDocument())
	publishPolicyVersion(t, uow, "attempt-policy-def", "attempt-policy-v1", attemptPolicyDocument(600))
	publishPolicyVersion(t, uow, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())

	ctx := context.Background()
	_, err := runtime.ScheduleExecutableNodeRun(ctx, uow, ids, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: runID, NodeRunID: nodeRunID, CorrelationID: "corr-1",
	})
	if err == nil {
		t.Fatal("ScheduleExecutableNodeRun succeeded despite a tampered/stale pinned ContentHash, want a fail-closed error")
	}
}

// oneResourceLayerDocument mirrors oneResourceSkillDocument for Layer —
// V5-03's own audit finding (2026-09-08) explicitly names Layer as a
// resource kind the real gathering path must be proven against, not just
// Skill.
func oneResourceLayerDocument(key, convention string, sel skill.Selector, global bool) layer.LayerDocument {
	return layer.LayerDocument{Resources: []layer.Resource{{
		Key: key, Convention: convention, Priority: definition.PriorityGuidance,
		Global: global, Selector: layer.Selector{
			ComponentTags: sel.ComponentTags, PathTags: sel.PathTags, TaskKinds: sel.TaskKinds,
			BlockKinds: sel.BlockKinds, RiskClasses: sel.RiskClasses,
		},
		Provenance: layer.Provenance{Owner: "team-x", Source: "doc-1", Revision: "v1"},
	}}}
}

func publishLayerVersion(t *testing.T, uow ports.UnitOfWork, definitionID, versionID string, doc layer.LayerDocument) {
	t.Helper()
	ctx := context.Background()
	createCmd := testCommand("idem-def-"+definitionID, "hash-def-"+definitionID, ports.InstallationScope(), "CreateDefinition")
	if _, err := definitions.CreateDefinition(ctx, uow, createCmd, definitions.CreateDefinitionRequest{
		DefinitionID: definitionID, Kind: definition.KindLayer, Scope: definition.GlobalScope(), Name: "layer " + definitionID,
	}); err != nil {
		t.Fatalf("CreateDefinition(%s): %v", definitionID, err)
	}
	publishCmd := testCommand("idem-pub-"+versionID, "hash-pub-"+versionID, ports.InstallationScope(), "PublishDefinitionVersion")
	if _, err := definitions.PublishDefinitionVersion(ctx, uow, publishCmd, definitions.PublishDefinitionVersionRequest{
		DefinitionID: definitionID, Kind: definition.KindLayer,
		Compile: func() (definition.VersionFields, error) {
			return layer.Compile(
				layer.LayerDefinition{
					ID: layer.LayerDefinitionID(definitionID),
					Fields: definition.Fields{
						Kind: definition.KindLayer, Scope: definition.GlobalScope(),
						Name: "layer", Status: definition.StatusDraft, Version: 1,
					},
				},
				layer.PublishRequest{
					VersionID: layer.LayerVersionID(versionID), VersionNumber: 1, SchemaVersion: 1,
					Document: doc, PublishedBy: "operator-1", PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				},
			)
		},
	}); err != nil {
		t.Fatalf("PublishDefinitionVersion(%s): %v", versionID, err)
	}
}

func layerResourceContentHash(t *testing.T, ownerVersionID, resourceKey string, doc layer.LayerDocument) string {
	t.Helper()
	identities, err := layer.ResourceIdentities(layer.LayerVersionID(ownerVersionID), doc)
	if err != nil {
		t.Fatalf("ResourceIdentities: %v", err)
	}
	for _, id := range identities {
		if id.Identity.ResourceKey == resourceKey {
			return id.Identity.ContentHash
		}
	}
	t.Fatalf("resource key %s not found in computed identities", resourceKey)
	return ""
}

func TestScheduleExecutableNodeRun_GathersRealResourceRefsFromLayer(t *testing.T) {
	uow, ids, runID, nodeRunID := scheduleFixture(t, agentExecutableDocument("agent-profile-v1", fullyResolvablePolicyRefs(), nil))

	layerDoc := oneResourceLayerDocument("stack-convention", "always use this stack convention", skill.Selector{}, true)
	publishLayerVersion(t, uow, "layer-def-1", "layer-v1", layerDoc)
	hash := layerResourceContentHash(t, "layer-v1", "stack-convention", layerDoc)

	publishPolicyVersion(t, uow, "context-policy-def", "context-policy-v1", policy.PolicyDocument{
		Category: policy.CategoryContext,
		Context: &policy.ContextRules{
			Selector: []string{"*"}, Budget: policy.ContextBudget{MaxTokens: 65536},
			ResourceRefs: []policy.ResourceRef{{OwnerVersionID: "layer-v1", ResourceKey: "stack-convention", ContentHash: hash}},
		},
	})
	publishAgentProfileVersionOnly(t, uow, "agent-profile-def", "agent-profile-v1", validAgentProfileDocument())
	publishPolicyVersion(t, uow, "attempt-policy-def", "attempt-policy-v1", attemptPolicyDocument(600))
	publishPolicyVersion(t, uow, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())

	ctx := context.Background()
	result, err := runtime.ScheduleExecutableNodeRun(ctx, uow, ids, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: runID, NodeRunID: nodeRunID, CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("ScheduleExecutableNodeRun: %v", err)
	}
	attempt, err := uow.Snapshot.Runtime().GetExecutionAttempt(ctx, result.AttemptID)
	if err != nil {
		t.Fatalf("GetExecutionAttempt: %v", err)
	}
	snapshot, err := uow.Snapshot.ContextSnapshots().GetSnapshot(ctx, string(*attempt.ContextSnapshotID))
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}
	if len(snapshot.ResourceRefs) != 1 || snapshot.ResourceRefs[0].OwnerVersionID != "layer-v1" {
		t.Fatalf("snapshot.ResourceRefs = %+v, want exactly one ref from layer-v1", snapshot.ResourceRefs)
	}
}

// TestScheduleExecutableNodeRun_ContextRoute_HardConstraintConflict_FailsClosed
// proves a REAL hard-constraint conflict (two applicable candidates share a
// ResourceKey with different ContentHash, at least one HARD_CONSTRAINT)
// reached through the real gathering path (two real Skill versions via a
// real ContextRoute policy) fails ScheduleExecutableNodeRun closed —
// V5-03's own contextassembler_test.go already proves Resolve itself
// detects this; this proves schedule.go's own real caller actually surfaces
// it as a scheduling failure rather than silently swallowing/ignoring it.
func TestScheduleExecutableNodeRun_ContextRoute_HardConstraintConflict_FailsClosed(t *testing.T) {
	uow, ids, runID, nodeRunID := scheduleFixture(t, agentExecutableDocument("agent-profile-v1", fullyResolvablePolicyRefs(), nil))

	docA := skill.SkillDocument{Resources: []skill.Resource{{
		Key: "conflict-key", Instruction: "always do X", Priority: definition.PriorityHardConstraint,
		Global: true, Provenance: skill.Provenance{Owner: "team-x", Source: "doc-a", Revision: "v1"},
	}}}
	docB := skill.SkillDocument{Resources: []skill.Resource{{
		Key: "conflict-key", Instruction: "always do Y instead", Priority: definition.PriorityHardConstraint,
		Global: true, Provenance: skill.Provenance{Owner: "team-y", Source: "doc-b", Revision: "v1"},
	}}}
	publishSkillVersion(t, uow, "skill-def-a", "skill-v-a", docA)
	publishSkillVersion(t, uow, "skill-def-b", "skill-v-b", docB)
	hashA := resourceContentHash(t, "skill-v-a", "conflict-key", docA)
	hashB := resourceContentHash(t, "skill-v-b", "conflict-key", docB)

	publishPolicyVersion(t, uow, "context-policy-def", "context-policy-v1", policy.PolicyDocument{
		Category: policy.CategoryContext,
		Context: &policy.ContextRules{
			Selector: []string{"*"}, Budget: policy.ContextBudget{MaxTokens: 65536},
			ResourceRefs: []policy.ResourceRef{
				{OwnerVersionID: "skill-v-a", ResourceKey: "conflict-key", ContentHash: hashA},
				{OwnerVersionID: "skill-v-b", ResourceKey: "conflict-key", ContentHash: hashB},
			},
		},
	})
	publishAgentProfileVersionOnly(t, uow, "agent-profile-def", "agent-profile-v1", validAgentProfileDocument())
	publishPolicyVersion(t, uow, "attempt-policy-def", "attempt-policy-v1", attemptPolicyDocument(600))
	publishPolicyVersion(t, uow, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())

	ctx := context.Background()
	_, err := runtime.ScheduleExecutableNodeRun(ctx, uow, ids, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: runID, NodeRunID: nodeRunID, CorrelationID: "corr-1",
	})
	if err == nil {
		t.Fatal("ScheduleExecutableNodeRun succeeded despite a real hard-constraint conflict, want a fail-closed error")
	}
}

// TestScheduleExecutableNodeRun_PersistsContextResolutionDecisionWithReasons
// is V5-03's own audit finding (2026-09-08) core fix: the full Resolution
// (Selected AND Excluded, each with its own SelectionReason) must be
// durably auditable, not just held in memory. One candidate matches (global)
// and is selected; a second, non-global candidate whose Selector never
// matches this WorkItem is excluded as NOT_APPLICABLE — both must appear,
// with their real reasons, in the persisted CONTEXT_RESOLUTION_V1 decision.
func TestScheduleExecutableNodeRun_PersistsContextResolutionDecisionWithReasons(t *testing.T) {
	uow, ids, runID, nodeRunID := scheduleFixture(t, agentExecutableDocument("agent-profile-v1", fullyResolvablePolicyRefs(), nil))

	includedDoc := oneResourceSkillDocument("included-rule", "always follow this", skill.Selector{}, true)
	excludedDoc := oneResourceSkillDocument("excluded-rule", "never matches this WorkItem",
		skill.Selector{TaskKinds: []string{"never-matches-anything"}}, false)
	publishSkillVersion(t, uow, "skill-def-included", "skill-v-included", includedDoc)
	publishSkillVersion(t, uow, "skill-def-excluded", "skill-v-excluded", excludedDoc)
	includedHash := resourceContentHash(t, "skill-v-included", "included-rule", includedDoc)
	excludedHash := resourceContentHash(t, "skill-v-excluded", "excluded-rule", excludedDoc)

	publishPolicyVersion(t, uow, "context-policy-def", "context-policy-v1", policy.PolicyDocument{
		Category: policy.CategoryContext,
		Context: &policy.ContextRules{
			Selector: []string{"*"}, Budget: policy.ContextBudget{MaxTokens: 65536},
			ResourceRefs: []policy.ResourceRef{
				{OwnerVersionID: "skill-v-included", ResourceKey: "included-rule", ContentHash: includedHash},
				{OwnerVersionID: "skill-v-excluded", ResourceKey: "excluded-rule", ContentHash: excludedHash},
			},
		},
	})
	publishAgentProfileVersionOnly(t, uow, "agent-profile-def", "agent-profile-v1", validAgentProfileDocument())
	publishPolicyVersion(t, uow, "attempt-policy-def", "attempt-policy-v1", attemptPolicyDocument(600))
	publishPolicyVersion(t, uow, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())

	ctx := context.Background()
	if _, err := runtime.ScheduleExecutableNodeRun(ctx, uow, ids, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: runID, NodeRunID: nodeRunID, CorrelationID: "corr-1",
	}); err != nil {
		t.Fatalf("ScheduleExecutableNodeRun: %v", err)
	}

	decision, err := uow.Snapshot.Runtime().GetDecisionArtifact(ctx, nodeRunID+"-context-resolution-v1")
	if err != nil {
		t.Fatalf("GetDecisionArtifact(context-resolution-v1): %v", err)
	}
	var resolution struct {
		Selected []struct {
			Identity struct{ ResourceKey string }
			Reason   string
		}
		Excluded []struct {
			Identity struct{ ResourceKey string }
			Reason   string
		}
	}
	if err := json.Unmarshal(decision.Result, &resolution); err != nil {
		t.Fatalf("unmarshal persisted resolution: %v", err)
	}
	if len(resolution.Selected) != 1 || resolution.Selected[0].Identity.ResourceKey != "included-rule" || resolution.Selected[0].Reason != "SELECTED" {
		t.Fatalf("resolution.Selected = %+v, want exactly included-rule/SELECTED", resolution.Selected)
	}
	if len(resolution.Excluded) != 1 || resolution.Excluded[0].Identity.ResourceKey != "excluded-rule" || resolution.Excluded[0].Reason != "NOT_APPLICABLE" {
		t.Fatalf("resolution.Excluded = %+v, want exactly excluded-rule/NOT_APPLICABLE", resolution.Excluded)
	}
}
