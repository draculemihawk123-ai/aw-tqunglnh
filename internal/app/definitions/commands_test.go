package definitions_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/definitions"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

func testCommand(idempotencyKey, requestHash string, scope ports.CommandScope, commandType string) ports.Command {
	return ports.Command{
		ID: "cmd-" + idempotencyKey, IdempotencyKey: idempotencyKey, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: scope, RequestedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Type: commandType, RequestHash: requestHash,
	}
}

func policyPublishCompile(definitionID, versionID string, granted []string) func() (definition.VersionFields, error) {
	return func() (definition.VersionFields, error) {
		return policy.Compile(
			policy.PolicyDefinition{
				ID:     policy.PolicyDefinitionID(definitionID),
				Fields: definition.Fields{Kind: definition.KindPolicy, Scope: definition.GlobalScope(), Name: "policy under test", Status: definition.StatusDraft, Version: 1},
			},
			policy.PublishRequest{
				VersionID: policy.PolicyVersionID(versionID), VersionNumber: 1, SchemaVersion: 1,
				Document: policy.PolicyDocument{
					Category: policy.CategoryPermission,
					Permission: &policy.PermissionRules{
						IsolationTier: policy.IsolationTierEnforcedIsolated, GrantedCapabilities: granted,
					},
				},
				PublishedBy: "operator-1", PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			},
		)
	}
}

// --- CreateDefinition ---

func TestCreateDefinition_SharedKind_CreatesAndEmitsEvent(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	cmd := testCommand("idem-1", "hash-a", ports.InstallationScope(), "CreateDefinition")

	result, err := definitions.CreateDefinition(ctx, uow, cmd, definitions.CreateDefinitionRequest{
		DefinitionID: "def-1", Kind: definition.KindPolicy, Scope: definition.GlobalScope(), Name: "test policy",
	})
	if err != nil {
		t.Fatalf("CreateDefinition: %v", err)
	}
	if result.DefinitionID != "def-1" || result.Kind != definition.KindPolicy {
		t.Fatalf("result = %+v, want DefinitionID=def-1 Kind=POLICY", result)
	}

	events := uow.Snapshot.Events().(*fake.EventsRepository).Items()
	if len(events) != 1 || events[0].EventType != "DefinitionCreated" {
		t.Fatalf("events = %+v, want exactly one DefinitionCreated", events)
	}
}

func TestCreateDefinition_DuplicateSameRequest_ReplaysWithoutNewEvent(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	cmd := testCommand("idem-1", "hash-a", ports.InstallationScope(), "CreateDefinition")
	req := definitions.CreateDefinitionRequest{DefinitionID: "def-1", Kind: definition.KindSkill, Scope: definition.GlobalScope(), Name: "test skill"}

	first, err := definitions.CreateDefinition(ctx, uow, cmd, req)
	if err != nil {
		t.Fatalf("first CreateDefinition: %v", err)
	}
	second, err := definitions.CreateDefinition(ctx, uow, cmd, req)
	if err != nil {
		t.Fatalf("second (replayed) CreateDefinition: %v", err)
	}
	if second != first {
		t.Fatalf("replayed result = %+v, want identical to first %+v", second, first)
	}

	events := uow.Snapshot.Events().(*fake.EventsRepository).Items()
	if len(events) != 1 {
		t.Fatalf("events after replay = %d, want 1 (a replay must never redo the mutation)", len(events))
	}
}

func TestCreateDefinition_Workflow_CreatesWorkflowDefinitionRow(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	cmd := testCommand("idem-1", "hash-a", ports.InstallationScope(), "CreateDefinition")

	_, err := definitions.CreateDefinition(ctx, uow, cmd, definitions.CreateDefinitionRequest{
		DefinitionID: "wf-def-1", Kind: definition.KindWorkflow, Scope: definition.GlobalScope(), Name: "test workflow",
	})
	if err != nil {
		t.Fatalf("CreateDefinition(KindWorkflow): %v", err)
	}
}

// --- ValidateDraft ---

func TestValidateDraft_SharedKind_ReturnsCompiledCandidateWithoutPersisting(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()

	fields, err := definitions.ValidateDraft(ctx, uow, definitions.ValidateDraftRequest{
		Kind: definition.KindPolicy, Compile: policyPublishCompile("def-1", "ver-1", []string{"X"}),
	})
	if err != nil {
		t.Fatalf("ValidateDraft: %v", err)
	}
	if fields.DefinitionID() != "def-1" || fields.Kind() != definition.KindPolicy {
		t.Fatalf("fields = %+v, want DefinitionID=def-1 Kind=POLICY", fields)
	}

	versions, err := definitions.ListVersions(ctx, uow, definition.KindPolicy, "def-1")
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 0 {
		t.Fatalf("ListVersions after a dry run = %d, want 0 (ValidateDraft must never persist)", len(versions))
	}
}

func TestValidateDraft_MissingCompile_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	_, err := definitions.ValidateDraft(ctx, uow, definitions.ValidateDraftRequest{Kind: definition.KindPolicy})
	if err == nil {
		t.Fatal("ValidateDraft with a nil Compile for a non-workflow kind should be rejected")
	}
}

func TestValidateDraft_Workflow_ResolvesAgainstRegistry(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	seedAgentProfileVersion(t, uow, "profile-1", "profile-1-v1")

	fields, err := definitions.ValidateDraft(ctx, uow, definitions.ValidateDraftRequest{
		Kind:               definition.KindWorkflow,
		WorkflowDefinition: testWorkflowDefinition("wf-1"),
		WorkflowRequest:    testWorkflowPublishRequest("wf-1-v1", simpleAgentWorkflowDocument("profile-1-v1")),
	})
	if err != nil {
		t.Fatalf("ValidateDraft(KindWorkflow): %v", err)
	}
	if fields.Kind() != definition.KindWorkflow || len(fields.Dependencies().Pins) != 1 {
		t.Fatalf("fields = %+v, want Kind=WORKFLOW with 1 resolved dependency pin", fields)
	}
}

// --- PublishDefinitionVersion: duplicate publish / cross-project ref ---

func TestPublishDefinitionVersion_DuplicatePublish_SameIdempotencyKey_ReplaysWithoutNewEventOrVersion(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	createTestDefinition(t, ctx, uow, "def-1", definition.KindPolicy, definition.GlobalScope())
	cmd := testCommand("idem-1", "hash-a", ports.InstallationScope(), "PublishDefinitionVersion")
	req := definitions.PublishDefinitionVersionRequest{
		DefinitionID: "def-1", Kind: definition.KindPolicy, Compile: policyPublishCompile("def-1", "ver-1", []string{"X"}),
	}

	first, err := definitions.PublishDefinitionVersion(ctx, uow, cmd, req)
	if err != nil {
		t.Fatalf("first PublishDefinitionVersion: %v", err)
	}
	second, err := definitions.PublishDefinitionVersion(ctx, uow, cmd, req)
	if err != nil {
		t.Fatalf("second (replayed) PublishDefinitionVersion: %v", err)
	}
	if second.ID() != first.ID() || second.VersionNumber() != first.VersionNumber() {
		t.Fatalf("replayed result = %+v, want identical to first %+v", second, first)
	}

	versions, err := definitions.ListVersions(ctx, uow, definition.KindPolicy, "def-1")
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("version rows after duplicate publish = %d, want 1", len(versions))
	}
	events := countEventsOfType(uow, "DefinitionVersionPublished")
	if events != 1 {
		t.Fatalf("DefinitionVersionPublished events after duplicate publish = %d, want 1 (a same-key replay must never fire a second event)", events)
	}
}

func TestPublishDefinitionVersion_DuplicateDifferentPayload_ReturnsConflict(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	createTestDefinition(t, ctx, uow, "def-1", definition.KindPolicy, definition.GlobalScope())

	first := testCommand("idem-1", "hash-a", ports.InstallationScope(), "PublishDefinitionVersion")
	if _, err := definitions.PublishDefinitionVersion(ctx, uow, first, definitions.PublishDefinitionVersionRequest{
		DefinitionID: "def-1", Kind: definition.KindPolicy, Compile: policyPublishCompile("def-1", "ver-1", []string{"X"}),
	}); err != nil {
		t.Fatalf("first PublishDefinitionVersion: %v", err)
	}

	second := testCommand("idem-1", "hash-b", ports.InstallationScope(), "PublishDefinitionVersion")
	_, err := definitions.PublishDefinitionVersion(ctx, uow, second, definitions.PublishDefinitionVersionRequest{
		DefinitionID: "def-1", Kind: definition.KindPolicy, Compile: policyPublishCompile("def-1", "ver-2", []string{"Y"}),
	})
	if !errors.Is(err, ports.ErrReceiptConflict) {
		t.Fatalf("second PublishDefinitionVersion err = %v, want ports.ErrReceiptConflict", err)
	}
}

// TestPublishDefinitionVersion_DifferentIdempotencyKey_SameContent_DedupesVersionAndEvent
// is the second half of the idempotent-republish-vs-event distinction
// documented on PublishDefinitionVersion itself: a genuinely different
// command (different IdempotencyKey) that happens to compile to
// byte-identical content gets its own receipt, but deduplicates at both
// the version-row layer (AK-ARCH-005B: dedup by DefinitionID +
// CompiledSnapshotHash) and the event layer — zero new
// DefinitionVersionPublished events, zero new version rows.
func TestPublishDefinitionVersion_DifferentIdempotencyKey_SameContent_DedupesVersionAndEvent(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	createTestDefinition(t, ctx, uow, "def-1", definition.KindPolicy, definition.GlobalScope())

	first := testCommand("idem-1", "hash-a", ports.InstallationScope(), "PublishDefinitionVersion")
	firstResult, err := definitions.PublishDefinitionVersion(ctx, uow, first, definitions.PublishDefinitionVersionRequest{
		DefinitionID: "def-1", Kind: definition.KindPolicy, Compile: policyPublishCompile("def-1", "ver-1", []string{"X"}),
	})
	if err != nil {
		t.Fatalf("first PublishDefinitionVersion: %v", err)
	}

	// A different idempotency key (so the receipt-level replay never
	// short-circuits) whose Compile call produces byte-identical
	// CompiledSnapshot/CompiledHash (same DefinitionID, same document,
	// same dependencies) — a genuine second command, not a retry.
	second := testCommand("idem-2", "hash-c", ports.InstallationScope(), "PublishDefinitionVersion")
	secondResult, err := definitions.PublishDefinitionVersion(ctx, uow, second, definitions.PublishDefinitionVersionRequest{
		DefinitionID: "def-1", Kind: definition.KindPolicy, Compile: policyPublishCompile("def-1", "ver-1-retry", []string{"X"}),
	})
	if err != nil {
		t.Fatalf("second PublishDefinitionVersion: %v", err)
	}

	if secondResult.ID() != firstResult.ID() || secondResult.VersionNumber() != firstResult.VersionNumber() {
		t.Fatalf("second publish of identical content = %+v, want the SAME version as first %+v (dedup by DefinitionID+CompiledSnapshotHash)", secondResult, firstResult)
	}

	versions, err := definitions.ListVersions(ctx, uow, definition.KindPolicy, "def-1")
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("version rows after publishing identical content twice under different commands = %d, want 1", len(versions))
	}
	events := countEventsOfType(uow, "DefinitionVersionPublished")
	if events != 1 {
		t.Fatalf("DefinitionVersionPublished events = %d, want 1 (deduped content must never fire a second event even under a fresh command)", events)
	}

	// Both commands still get their own receipt — the command-level
	// audit trail is not the same thing as the aggregate-level event log.
	if _, found, err := uow.Snapshot.Receipts().Load(ctx, "actor-1", ports.InstallationScope(), "idem-1", first.Type); err != nil || !found {
		t.Fatalf("first command's own receipt should still exist: found=%v err=%v", found, err)
	}
	if _, found, err := uow.Snapshot.Receipts().Load(ctx, "actor-1", ports.InstallationScope(), "idem-2", second.Type); err != nil || !found {
		t.Fatalf("second command's own receipt should still exist: found=%v err=%v", found, err)
	}
}

func TestPublishDefinitionVersion_DifferentContent_ProducesNewVersionAndEvent(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	createTestDefinition(t, ctx, uow, "def-1", definition.KindPolicy, definition.GlobalScope())

	first := testCommand("idem-1", "hash-a", ports.InstallationScope(), "PublishDefinitionVersion")
	firstResult, err := definitions.PublishDefinitionVersion(ctx, uow, first, definitions.PublishDefinitionVersionRequest{
		DefinitionID: "def-1", Kind: definition.KindPolicy, Compile: policyPublishCompile("def-1", "ver-1", []string{"X"}),
	})
	if err != nil {
		t.Fatalf("first PublishDefinitionVersion: %v", err)
	}

	second := testCommand("idem-2", "hash-c", ports.InstallationScope(), "PublishDefinitionVersion")
	secondResult, err := definitions.PublishDefinitionVersion(ctx, uow, second, definitions.PublishDefinitionVersionRequest{
		DefinitionID: "def-1", Kind: definition.KindPolicy, Compile: policyPublishCompile("def-1", "ver-2", []string{"Y"}),
	})
	if err != nil {
		t.Fatalf("second PublishDefinitionVersion: %v", err)
	}
	if secondResult.ID() == firstResult.ID() || secondResult.VersionNumber() == firstResult.VersionNumber() {
		t.Fatalf("genuinely different content should produce a distinct version, got first=%+v second=%+v", firstResult, secondResult)
	}

	events := countEventsOfType(uow, "DefinitionVersionPublished")
	if events != 2 {
		t.Fatalf("DefinitionVersionPublished events after two genuinely different publishes = %d, want 2", events)
	}
}

func TestPublishDefinitionVersion_CrossProjectDependency_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	projectA := mustProjectScope(t, "project-a")
	projectB := mustProjectScope(t, "project-b")
	createTestDefinition(t, ctx, uow, "def-dep", definition.KindSkill, projectA)
	createTestDefinition(t, ctx, uow, "def-main", definition.KindBlock, projectB)

	cmd := testCommand("idem-1", "hash-a", ports.InstallationScope(), "PublishDefinitionVersion")
	_, err := definitions.PublishDefinitionVersion(ctx, uow, cmd, definitions.PublishDefinitionVersionRequest{
		DefinitionID: "def-main", Kind: definition.KindBlock,
		Compile: func() (definition.VersionFields, error) {
			return definition.NewVersionFields(definition.NewVersionFieldsRequest{
				ID: "ver-1", DefinitionID: "def-main", Kind: definition.KindBlock, VersionNumber: 1, SchemaVersion: 1,
				CanonicalSource: `{"v":1}`, SourceHash: "sha256:source", CompiledSnapshot: `{"v":1}`, CompiledHash: "sha256:compiled",
				Dependencies: definition.DependencyManifest{Pins: []definition.DependencyPin{
					{Kind: definition.KindSkill, DefinitionID: "def-dep", VersionID: "dep-ver-1"},
				}},
				PublishedBy: "operator-1", PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			})
		},
	})
	if !errors.Is(err, ports.ErrCrossProjectDependency) {
		t.Fatalf("PublishDefinitionVersion with a cross-project dependency err = %v, want ports.ErrCrossProjectDependency", err)
	}
}

// --- PublishDefinitionVersion: workflow and shared-kind end-to-end ---

func TestPublishDefinitionVersion_Workflow_EndToEnd_ResolvesPinsAndCommitsAtomically(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	seedAgentProfileVersion(t, uow, "profile-1", "profile-1-v1")
	createDef := testCommand("idem-create", "hash-create", ports.InstallationScope(), "CreateDefinition")
	if _, err := definitions.CreateDefinition(ctx, uow, createDef, definitions.CreateDefinitionRequest{
		DefinitionID: "wf-1", Kind: definition.KindWorkflow, Scope: definition.GlobalScope(), Name: "test workflow",
	}); err != nil {
		t.Fatalf("CreateDefinition(KindWorkflow): %v", err)
	}

	cmd := testCommand("idem-publish", "hash-publish", ports.InstallationScope(), "PublishDefinitionVersion")
	result, err := definitions.PublishDefinitionVersion(ctx, uow, cmd, definitions.PublishDefinitionVersionRequest{
		DefinitionID:       "wf-1",
		Kind:               definition.KindWorkflow,
		WorkflowDefinition: testWorkflowDefinition("wf-1"),
		WorkflowRequest:    testWorkflowPublishRequest("wf-1-v1", simpleAgentWorkflowDocument("profile-1-v1")),
	})
	if err != nil {
		t.Fatalf("PublishDefinitionVersion(KindWorkflow): %v", err)
	}
	if result.Kind() != definition.KindWorkflow || result.VersionNumber() != 1 || len(result.Dependencies().Pins) != 1 {
		t.Fatalf("result = %+v, want Kind=WORKFLOW VersionNumber=1 with 1 resolved dependency pin", result)
	}

	versions, err := definitions.ListVersions(ctx, uow, definition.KindWorkflow, "wf-1")
	if err != nil {
		t.Fatalf("ListVersions(KindWorkflow): %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("workflow version rows = %d, want 1", len(versions))
	}
	if countEventsOfType(uow, "DefinitionVersionPublished") != 1 {
		t.Fatal("expected exactly one DefinitionVersionPublished event for the workflow publish")
	}
	if _, found, err := uow.Snapshot.Receipts().Load(ctx, "actor-1", ports.InstallationScope(), "idem-publish", cmd.Type); err != nil || !found {
		t.Fatalf("expected a receipt for the workflow publish command: found=%v err=%v", found, err)
	}
}

func TestPublishDefinitionVersion_SharedKind_EndToEnd(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	createTestDefinition(t, ctx, uow, "policy-1", definition.KindPolicy, definition.GlobalScope())

	cmd := testCommand("idem-1", "hash-a", ports.InstallationScope(), "PublishDefinitionVersion")
	result, err := definitions.PublishDefinitionVersion(ctx, uow, cmd, definitions.PublishDefinitionVersionRequest{
		DefinitionID: "policy-1", Kind: definition.KindPolicy,
		Compile: policyPublishCompile("policy-1", "policy-1-v1", []string{"INTEGRATION_MULTI_REPOSITORY_WRITE"}),
	})
	if err != nil {
		t.Fatalf("PublishDefinitionVersion(KindPolicy): %v", err)
	}
	if result.Kind() != definition.KindPolicy || result.VersionNumber() != 1 {
		t.Fatalf("result = %+v, want Kind=POLICY VersionNumber=1", result)
	}

	loaded, err := definitions.LoadVersion(ctx, uow, result.ID())
	if err != nil {
		t.Fatalf("LoadVersion: %v", err)
	}
	if loaded.CompiledHash() != result.CompiledHash() {
		t.Fatalf("LoadVersion returned %+v, want it to match the published result %+v", loaded, result)
	}
}

// --- test helpers ---

func createTestDefinition(t *testing.T, ctx context.Context, uow *fake.UnitOfWork, id string, kind definition.Kind, scope definition.Scope) {
	t.Helper()
	cmd := testCommand("idem-create-"+id, "hash-create-"+id, ports.InstallationScope(), "CreateDefinition")
	if _, err := definitions.CreateDefinition(ctx, uow, cmd, definitions.CreateDefinitionRequest{
		DefinitionID: id, Kind: kind, Scope: scope, Name: "test " + id,
	}); err != nil {
		t.Fatalf("createTestDefinition(%s): %v", id, err)
	}
}

func mustProjectScope(t *testing.T, id string) definition.Scope {
	t.Helper()
	return definition.ProjectScope(project.ProjectID(id))
}

func countEventsOfType(uow *fake.UnitOfWork, eventType string) int {
	count := 0
	for _, event := range uow.Snapshot.Events().(*fake.EventsRepository).Items() {
		if event.EventType == eventType {
			count++
		}
	}
	return count
}

func seedAgentProfileVersion(t *testing.T, uow *fake.UnitOfWork, definitionID, versionID string) {
	t.Helper()
	fields, err := definition.NewVersionFields(definition.NewVersionFieldsRequest{
		ID: versionID, DefinitionID: definitionID, Kind: definition.KindAgentProfile,
		VersionNumber: 1, SchemaVersion: 1,
		CanonicalSource: `{"providerKey":"claude"}`, SourceHash: "sha256:source-" + versionID,
		CompiledSnapshot: `{"providerKey":"claude"}`, CompiledHash: "sha256:compiled-" + versionID,
		PublishedBy: "test", PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("construct seeded agent profile version: %v", err)
	}
	uow.Snapshot.Definitions().(*fake.DefinitionsRepository).Seed(fields)
}

func testWorkflowDefinition(id string) workflow.WorkflowDefinition {
	return workflow.WorkflowDefinition{
		ID: workflow.WorkflowDefinitionID(id), Name: "test workflow", Status: workflow.DefinitionDraft, Version: 1,
	}
}

func testWorkflowPublishRequest(versionID string, document workflow.WorkflowDocument) workflow.PublishRequest {
	return workflow.PublishRequest{
		VersionID: workflow.WorkflowVersionID(versionID), VersionNumber: 1, Document: document,
		PublishedBy: "test", PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func simpleAgentWorkflowDocument(profileVersionID string) workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"go"}},
			{
				Key: "agent", Type: workflow.NodeAgent, Outcomes: []string{"done"},
				Agent: &workflow.AgentNodeConfig{
					ProfileRef: definition.DependencyPin{Kind: definition.KindAgentProfile, DefinitionID: "profile-1", VersionID: profileVersionID},
					Role:       workflow.AgentRoleMaker,
				},
			},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "e1", From: "start", Outcome: "go", To: "agent"},
			{Key: "e2", From: "agent", Outcome: "done", To: "end"},
		},
	}
}
