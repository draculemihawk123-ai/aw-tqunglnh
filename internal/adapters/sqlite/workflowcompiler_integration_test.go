package sqlite

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/app/workflowcompiler"
	domainadapterbuild "github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// TestWorkflowCompiler_ResolvesAgainstRealSQLite proves V2-09's
// workflowcompiler package works against the real sqlite-backed
// ports.Tx implementation, not just internal/app/ports/fake — the same
// "two independent implementations satisfy one interface" discipline
// this session already applied to ports.AdapterBuildRepository (V2-07A).
// It publishes a real AgentProfileVersion through the existing V2-02
// shared publish path, registers a real AdapterBuildVersion through the
// existing V2-07A/B flow, and confirms workflowcompiler.CompileAndResolve
// resolves both into one compiled WorkflowVersion's dependency manifest.
func TestWorkflowCompiler_ResolvesAgainstRealSQLite(t *testing.T) {
	ctx := context.Background()
	store := openDefinitionsTestStore(t, "agentkit-workflowcompiler-integration.db")
	uow := NewUnitOfWork(store)

	createTestSharedDefinition(t, store, "profile-1", definition.KindAgentProfile, definition.GlobalScope(), "test profile")
	published, err := store.PublishDefinitionVersion(ctx, publishRequest("profile-1", definition.KindAgentProfile, "profile-1-v1", `{"providerKey":"claude"}`))
	if err != nil {
		t.Fatalf("publish agent profile version: %v", err)
	}

	executablePath := filepath.Join(t.TempDir(), "provider-cli")
	if err := os.WriteFile(executablePath, []byte("fake-provider-binary"), 0o755); err != nil {
		t.Fatalf("write fake executable: %v", err)
	}
	capabilityManifest := domainadapterbuild.CapabilityManifest{SupportsStart: true}

	token, err := adapterbuild.ProbeAdapterBuild(ctx, uow, adapterbuild.ProbeRequest{
		ProviderKey: "claude", ExecutablePath: executablePath, ProtocolVersion: "v1",
		CapabilityManifest: capabilityManifest, OS: "linux", Toolchain: "node-20", ConfigIdentity: "default",
	})
	if err != nil {
		t.Fatalf("probe adapter build: %v", err)
	}
	registerResult, err := adapterbuild.RegisterAdapterBuild(ctx, uow, adapterbuild.RegisterRequest{
		Token: token, CapabilityManifest: capabilityManifest, RegisteredBy: "operator-1",
	})
	if err != nil {
		t.Fatalf("register adapter build: %v", err)
	}
	buildID := registerResult.Build.ID()

	def := workflow.WorkflowDefinition{ID: "wf-1", Name: "test", Status: workflow.DefinitionDraft, Version: 1}
	document := workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"go"}},
			{Key: "agent", Type: workflow.NodeAgent, Outcomes: []string{"done"}, Agent: &workflow.AgentNodeConfig{
				ProfileRef:     definition.DependencyPin{Kind: definition.KindAgentProfile, DefinitionID: "profile-1", VersionID: "profile-1-v1"},
				AdapterBuildID: &buildID,
			}},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "e1", From: "start", Outcome: "go", To: "agent"},
			{Key: "e2", From: "agent", Outcome: "done", To: "end"},
		},
	}
	request := workflow.PublishRequest{
		VersionID: "wf-1-v1", VersionNumber: 1, Document: document,
		PublishedBy: "operator-1", PublishedAt: time.Now().UTC(),
	}

	version, err := workflowcompiler.CompileAndResolve(ctx, uow, def, request)
	if err != nil {
		t.Fatalf("CompileAndResolve against real sqlite: %v", err)
	}
	deps := version.Dependencies()
	if len(deps.Pins) != 2 {
		t.Fatalf("len(deps.Pins) = %d, want 2 (profile + adapter build)", len(deps.Pins))
	}
	foundProfile, foundBuild := false, false
	for _, pin := range deps.Pins {
		if pin.Kind == "AGENT_PROFILE" && pin.Key == "profile-1" && pin.Version == "profile-1-v1" {
			foundProfile = true
			if pin.Hash != published.CompiledHash() {
				t.Fatalf("resolved profile hash = %q, want %q", pin.Hash, published.CompiledHash())
			}
		}
		if pin.Kind == "ADAPTER_BUILD_VERSION" && pin.Key == buildID {
			foundBuild = true
		}
	}
	if !foundProfile || !foundBuild {
		t.Fatalf("deps.Pins = %+v, want both the resolved profile and adapter build pins", deps.Pins)
	}
}
