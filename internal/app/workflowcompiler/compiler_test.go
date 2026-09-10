package workflowcompiler_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/workflowcompiler"
	"github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

func seedVersion(t *testing.T, uow *fake.UnitOfWork, kind definition.Kind, definitionID, versionID string, doc any, compiledHash string) {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal seeded document: %v", err)
	}
	fields, err := definition.NewVersionFields(definition.NewVersionFieldsRequest{
		ID: versionID, DefinitionID: definitionID, Kind: kind,
		VersionNumber: 1, SchemaVersion: 1,
		CanonicalSource: string(raw), SourceHash: "sha256:source-" + versionID,
		CompiledSnapshot: string(raw), CompiledHash: compiledHash,
		PublishedBy: "test", PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("construct seeded version fields: %v", err)
	}
	uow.Snapshot.Definitions().(*fake.DefinitionsRepository).Seed(fields)
}

func seedAdapterBuild(t *testing.T, uow *fake.UnitOfWork, providerKey string) adapterbuild.Build {
	t.Helper()
	build, err := adapterbuild.NewBuild(adapterbuild.NewBuildRequest{
		Tuple: adapterbuild.CandidateTuple{
			ProviderKey: providerKey, ExecutablePath: "/usr/bin/" + providerKey,
			ExecutableContentHash: "sha256:exec-" + providerKey, ProtocolVersion: "v1",
			CapabilityManifestHash: "sha256:cap-" + providerKey, OS: "linux", Toolchain: "node-20", ConfigIdentity: "default",
		},
		CapabilityManifest: adapterbuild.CapabilityManifest{SupportsStart: true},
		RegisteredBy:       "test", RegisteredAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("construct seeded adapter build: %v", err)
	}
	_, _, err = uow.Snapshot.AdapterBuilds().InsertIfAbsent(context.Background(), build)
	if err != nil {
		t.Fatalf("seed adapter build: %v", err)
	}
	return build
}

func simpleAgentDocument(profileVersionID string) workflow.WorkflowDocument {
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

func testDefinition() workflow.WorkflowDefinition {
	return workflow.WorkflowDefinition{ID: "wf-1", Name: "test", Status: workflow.DefinitionDraft, Version: 1}
}

func testPublishRequest(versionID string, document workflow.WorkflowDocument) workflow.PublishRequest {
	return workflow.PublishRequest{
		VersionID: workflow.WorkflowVersionID(versionID), VersionNumber: 1, Document: document,
		PublishedBy: "test", PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestCompileAndResolve_ResolvesExecutorPinIntoManifest(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	seedVersion(t, uow, definition.KindAgentProfile, "profile-1", "profile-1-v1",
		map[string]any{"providerKey": "claude", "model": "x", "toolRefs": []string{}, "compatibility": map[string]any{"os": []string{"linux"}}, "budget": map[string]any{"maxTokens": 1}},
		"sha256:compiled-profile-1-v1")

	version, err := workflowcompiler.CompileAndResolve(ctx, uow, testDefinition(), testPublishRequest("wf-1-v1", simpleAgentDocument("profile-1-v1")))
	if err != nil {
		t.Fatalf("CompileAndResolve: %v", err)
	}
	deps := version.Dependencies()
	if len(deps.Pins) != 1 {
		t.Fatalf("len(deps.Pins) = %d, want 1", len(deps.Pins))
	}
	pin := deps.Pins[0]
	if pin.Kind != "AGENT_PROFILE" || pin.Key != "profile-1" || pin.Version != "profile-1-v1" || pin.Hash != "sha256:compiled-profile-1-v1" {
		t.Fatalf("resolved pin = %+v, want kind=AGENT_PROFILE key=profile-1 version=profile-1-v1 hash=sha256:compiled-profile-1-v1", pin)
	}
}

func TestCompileAndResolve_RejectsUnresolvedPin(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	// No version seeded at all.
	_, err := workflowcompiler.CompileAndResolve(ctx, uow, testDefinition(), testPublishRequest("wf-1-v1", simpleAgentDocument("profile-1-v1")))
	if err == nil {
		t.Fatal("CompileAndResolve should reject a pin that does not resolve to any published version")
	}
	if !strings.Contains(err.Error(), "does not resolve to any published version") {
		t.Fatalf("err = %v, want it to mention unresolved pin", err)
	}
}

func TestCompileAndResolve_RejectsKindDefinitionIDMismatch(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	// Seed a version under a DIFFERENT DefinitionID than the pin claims.
	seedVersion(t, uow, definition.KindAgentProfile, "some-other-profile", "profile-1-v1", map[string]any{}, "sha256:x")

	_, err := workflowcompiler.CompileAndResolve(ctx, uow, testDefinition(), testPublishRequest("wf-1-v1", simpleAgentDocument("profile-1-v1")))
	if err == nil {
		t.Fatal("CompileAndResolve should reject a pin whose resolved version belongs to a different definition")
	}
	if !strings.Contains(err.Error(), "actually belongs to") {
		t.Fatalf("err = %v, want it to mention the mismatch", err)
	}
}

// TestCompileAndResolve_SameRegistrySnapshot_SameCompiledHash and
// TestCompileAndResolve_DifferentPin_DifferentCompiledHash together are
// V2-09's own "Hoàn thành khi" bar: "cùng SourceHash + exact registry
// snapshot tạo cùng CompiledSnapshotHash; thay một dependency pin tạo
// hash khác."
func TestCompileAndResolve_SameRegistrySnapshot_SameCompiledHash(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	seedVersion(t, uow, definition.KindAgentProfile, "profile-1", "profile-1-v1", map[string]any{}, "sha256:compiled-v1")

	first, err := workflowcompiler.CompileAndResolve(ctx, uow, testDefinition(), testPublishRequest("wf-1-v1", simpleAgentDocument("profile-1-v1")))
	if err != nil {
		t.Fatalf("first compile: %v", err)
	}
	second, err := workflowcompiler.CompileAndResolve(ctx, uow, testDefinition(), testPublishRequest("wf-1-v2", simpleAgentDocument("profile-1-v1")))
	if err != nil {
		t.Fatalf("second compile: %v", err)
	}
	if first.ContentHash() != second.ContentHash() {
		t.Fatalf("same source + same registry snapshot produced different hashes: %s vs %s", first.ContentHash(), second.ContentHash())
	}
}

func TestCompileAndResolve_DifferentPin_DifferentCompiledHash(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	seedVersion(t, uow, definition.KindAgentProfile, "profile-1", "profile-1-v1", map[string]any{}, "sha256:compiled-v1")
	seedVersion(t, uow, definition.KindAgentProfile, "profile-1", "profile-1-v2", map[string]any{}, "sha256:compiled-v2")

	first, err := workflowcompiler.CompileAndResolve(ctx, uow, testDefinition(), testPublishRequest("wf-1-v1", simpleAgentDocument("profile-1-v1")))
	if err != nil {
		t.Fatalf("compile pinning v1: %v", err)
	}
	second, err := workflowcompiler.CompileAndResolve(ctx, uow, testDefinition(), testPublishRequest("wf-1-v1", simpleAgentDocument("profile-1-v2")))
	if err != nil {
		t.Fatalf("compile pinning v2: %v", err)
	}
	if first.ContentHash() == second.ContentHash() {
		t.Fatal("changing which version a node pins should change the compiled hash")
	}
}

func TestCompileAndResolve_ConflictingVersionsForSameDefinitionRejected(t *testing.T) {
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
				ProfileRef: definition.DependencyPin{Kind: definition.KindAgentProfile, DefinitionID: "profile-1", VersionID: "profile-1-v2"},
				Role:       workflow.AgentRoleMaker,
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
		t.Fatal("pinning the same definition at two different versions in one graph should be rejected")
	}
	if !strings.Contains(err.Error(), "pinned at two different versions") {
		t.Fatalf("err = %v, want it to mention conflicting versions", err)
	}
}

func TestCompileAndResolve_AdapterBuildID_ResolvedIntoManifest(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	seedVersion(t, uow, definition.KindAgentProfile, "profile-1", "profile-1-v1", map[string]any{}, "sha256:compiled-v1")
	build := seedAdapterBuild(t, uow, "claude")

	doc := simpleAgentDocument("profile-1-v1")
	buildID := build.ID()
	doc.Nodes[1].Agent.AdapterBuildID = &buildID

	version, err := workflowcompiler.CompileAndResolve(ctx, uow, testDefinition(), testPublishRequest("wf-1-v1", doc))
	if err != nil {
		t.Fatalf("CompileAndResolve: %v", err)
	}
	deps := version.Dependencies()
	found := false
	for _, pin := range deps.Pins {
		if pin.Kind == "ADAPTER_BUILD_VERSION" {
			found = true
			if pin.Key != buildID || pin.Version != buildID || pin.Hash != buildID {
				t.Fatalf("adapter build pin = %+v, want key=version=hash=%s", pin, buildID)
			}
		}
	}
	if !found {
		t.Fatal("compiled manifest should include the resolved ADAPTER_BUILD_VERSION pin")
	}
}

func TestCompileAndResolve_RejectsUnknownAdapterBuildID(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	seedVersion(t, uow, definition.KindAgentProfile, "profile-1", "profile-1-v1", map[string]any{}, "sha256:compiled-v1")

	doc := simpleAgentDocument("profile-1-v1")
	unknown := "sha256:does-not-exist"
	doc.Nodes[1].Agent.AdapterBuildID = &unknown

	_, err := workflowcompiler.CompileAndResolve(ctx, uow, testDefinition(), testPublishRequest("wf-1-v1", doc))
	if err == nil {
		t.Fatal("an unknown adapter build id should be rejected")
	}
	if !strings.Contains(err.Error(), "does not resolve to any registered build") {
		t.Fatalf("err = %v, want it to mention the unresolved adapter build", err)
	}
}

func commandDocument(t *testing.T, cwdTarget string) command.CommandDocument {
	t.Helper()
	return command.CommandDocument{
		Executable:          command.ExecutableRef{OwnerVersionID: "skill-1-v1", ResourceKey: "run.sh", ContentHash: "sha256:x"},
		Argv:                []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run.sh"}},
		CwdRepositoryTarget: cwdTarget,
		Compatibility:       command.Compatibility{OS: []string{"linux"}},
		NetworkAccess:       command.NetworkAccessNone,
		TimeoutSeconds:      60,
		Output:              command.OutputContract{MaxOutputBytes: 1024},
	}
}

func twoCommandDocument(repoA, repoB string) workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"go"}},
			{Key: "cmd1", Type: workflow.NodeCommand, Outcomes: []string{"done"}, Command: &workflow.CommandNodeConfig{
				CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "cmd-a", VersionID: "cmd-a-v1"},
			}},
			{Key: "cmd2", Type: workflow.NodeCommand, Outcomes: []string{"done"}, Command: &workflow.CommandNodeConfig{
				CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "cmd-b", VersionID: "cmd-b-v1"},
			}},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "e1", From: "start", Outcome: "go", To: "cmd1"},
			{Key: "e2", From: "cmd1", Outcome: "done", To: "cmd2"},
			{Key: "e3", From: "cmd2", Outcome: "done", To: "end"},
		},
	}
}

func TestCompileAndResolve_SingleRepository_NoGrantNeeded(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	seedVersion(t, uow, definition.KindCommand, "cmd-a", "cmd-a-v1", commandDocument(t, "repo-1"), "sha256:cmd-a")
	seedVersion(t, uow, definition.KindCommand, "cmd-b", "cmd-b-v1", commandDocument(t, "repo-1"), "sha256:cmd-b")

	_, err := workflowcompiler.CompileAndResolve(ctx, uow, testDefinition(), testPublishRequest("wf-1-v1", twoCommandDocument("repo-1", "repo-1")))
	if err != nil {
		t.Fatalf("a single targeted repository should never need a grant: %v", err)
	}
}

func TestCompileAndResolve_MultiRepository_RejectedWithoutGrant(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	seedVersion(t, uow, definition.KindCommand, "cmd-a", "cmd-a-v1", commandDocument(t, "repo-1"), "sha256:cmd-a")
	seedVersion(t, uow, definition.KindCommand, "cmd-b", "cmd-b-v1", commandDocument(t, "repo-2"), "sha256:cmd-b")

	_, err := workflowcompiler.CompileAndResolve(ctx, uow, testDefinition(), testPublishRequest("wf-1-v1", twoCommandDocument("repo-1", "repo-2")))
	if err == nil {
		t.Fatal("targeting two repositories without a grant should be rejected")
	}
	if !strings.Contains(err.Error(), "INTEGRATION_MULTI_REPOSITORY_WRITE") {
		t.Fatalf("err = %v, want it to mention INTEGRATION_MULTI_REPOSITORY_WRITE", err)
	}
}

func TestCompileAndResolve_MultiRepository_AcceptedWithGrant(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	seedVersion(t, uow, definition.KindCommand, "cmd-a", "cmd-a-v1", commandDocument(t, "repo-1"), "sha256:cmd-a")
	seedVersion(t, uow, definition.KindCommand, "cmd-b", "cmd-b-v1", commandDocument(t, "repo-2"), "sha256:cmd-b")
	seedVersion(t, uow, definition.KindPolicy, "integration-policy", "integration-policy-v1",
		policy.PolicyDocument{Category: policy.CategoryPermission, Permission: &policy.PermissionRules{
			IsolationTier: policy.IsolationTierEnforcedIsolated, GrantedCapabilities: []string{"INTEGRATION_MULTI_REPOSITORY_WRITE"},
		}}, "sha256:policy")

	doc := twoCommandDocument("repo-1", "repo-2")
	doc.Nodes[1].Command.PolicyRefs = []definition.DependencyPin{
		{Kind: definition.KindPolicy, DefinitionID: "integration-policy", VersionID: "integration-policy-v1"},
	}

	_, err := workflowcompiler.CompileAndResolve(ctx, uow, testDefinition(), testPublishRequest("wf-1-v1", doc))
	if err != nil {
		t.Fatalf("a multi-repository graph with an INTEGRATION_MULTI_REPOSITORY_WRITE grant should be accepted: %v", err)
	}
}

// TestCompileAndResolve_Golden is V2-09's own "compiler golden tests"
// requirement: a representative resolved graph's exact canonical content
// is compared byte-for-byte against a checked-in fixture, so a change to
// resolution or canonicalization behavior itself — not just
// determinism — is caught.
func TestCompileAndResolve_Golden(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	seedVersion(t, uow, definition.KindAgentProfile, "profile-1", "profile-1-v1", map[string]any{"providerKey": "claude"}, "sha256:compiled-profile-1-v1")

	version, err := workflowcompiler.CompileAndResolve(ctx, uow, testDefinition(), testPublishRequest("wf-1-v1", simpleAgentDocument("profile-1-v1")))
	if err != nil {
		t.Fatalf("CompileAndResolve: %v", err)
	}

	goldenPath := filepath.Join("testdata", "golden", "simple-agent-workflow.json")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got := append(append([]byte(nil), version.CanonicalContent()...), '\n')
	if string(got) != string(want) {
		t.Fatalf("canonical content does not match golden fixture.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestCompileAndResolve_StructuralValidationFailsBeforeAnyResolution
// confirms CompileAndResolve validates the document structurally first
// — a structurally invalid graph should never even attempt to resolve
// pins (no seeded data needed for this test to fail correctly).
func TestCompileAndResolve_StructuralValidationFailsBeforeAnyResolution(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	doc := simpleAgentDocument("profile-1-v1")
	doc.Nodes = doc.Nodes[:2] // drop the END node — structurally invalid

	_, err := workflowcompiler.CompileAndResolve(ctx, uow, testDefinition(), testPublishRequest("wf-1-v1", doc))
	if err == nil {
		t.Fatal("a structurally invalid document should be rejected")
	}
	if strings.Contains(err.Error(), "does not resolve") {
		t.Fatalf("err = %v, want a structural validation error, not a resolution error", err)
	}
}
