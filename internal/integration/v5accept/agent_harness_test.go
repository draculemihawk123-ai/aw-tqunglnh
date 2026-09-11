// Real AGENT support for this package's own scenarios (V5-15B's own
// "claim done" injection needs it — see claim_done_test.go's own package
// doc comment for why: COMMAND/MACHINE_GATE have no free-text self-report
// channel at all, their own outcome is always derived deterministically
// from a real exit code, so only AGENT has a genuine "claim" a real
// completion oracle must never trust alone). Every piece here is real: a
// real cmd/fake-claude binary (built once via `go build`, mirroring
// internal/spikeacceptance's own buildScenarioBinaries), a real
// claude.Adapter spawning it through the real ports.ProcessSupervisor, a
// real agentregistry.Registry (which really calls Capabilities() — a real
// `fake-claude --version` spawn), and a real AdapterBuild pinned from that
// same real probe's own observed values (adapterbuild.VerifyNoDrift
// re-probes live at every admission, so a fabricated pin would fail
// immediately, not just be dishonest).
package v5accept

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	stdruntime "runtime"
	"sync"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/providers/claude"
	"github.com/taQuangLing/agent-workflow/internal/app/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/definitions"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	domainadapterbuild "github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/domain/agentprofile"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
)

// v5AcceptFakeClaudeBinaryOnce/Value cache the real, compiled cmd/fake-claude
// binary across every test in this package — building it is real wall-clock
// `go build` work (a few seconds), and every scenario needing a real AGENT
// spawns the SAME binary (its own behavior is chosen per-request via real
// env vars, never by rebuilding it differently).
var (
	v5AcceptFakeClaudeBinaryOnce  sync.Once
	v5AcceptFakeClaudeBinaryValue string
	v5AcceptFakeClaudeBinaryErr   error
)

// v5AcceptFakeClaudeBinary returns the real, on-disk path to a real
// compiled cmd/fake-claude binary — mirrors
// internal/spikeacceptance/registry_test.go's own buildScenarioBinaries
// exactly (same `go build` via runtime.GOROOT()'s own toolchain, same
// moduleRoot-walk-up-to-go.mod technique), built once for the whole
// package test run.
func v5AcceptFakeClaudeBinary(t *testing.T) string {
	t.Helper()
	v5AcceptFakeClaudeBinaryOnce.Do(func() {
		root, err := v5AcceptModuleRoot()
		if err != nil {
			v5AcceptFakeClaudeBinaryErr = err
			return
		}
		binDir, err := os.MkdirTemp("", "v5accept-fake-claude")
		if err != nil {
			v5AcceptFakeClaudeBinaryErr = err
			return
		}
		goExecutable := filepath.Join(stdruntime.GOROOT(), "bin", "go")
		output := filepath.Join(binDir, "fake-claude")
		if stdruntime.GOOS == "windows" {
			goExecutable += ".exe"
			output += ".exe"
		}
		cmd := exec.Command(goExecutable, "build", "-o", output, "./cmd/fake-claude")
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			v5AcceptFakeClaudeBinaryErr = fmt.Errorf("build cmd/fake-claude: %v\n%s", err, out)
			return
		}
		v5AcceptFakeClaudeBinaryValue = output
	})
	if v5AcceptFakeClaudeBinaryErr != nil {
		t.Fatalf("v5AcceptFakeClaudeBinary: %v", v5AcceptFakeClaudeBinaryErr)
	}
	return v5AcceptFakeClaudeBinaryValue
}

// v5AcceptModuleRoot walks up from this test file's own directory to find
// go.mod — mirrors internal/spikeacceptance/registry_test.go's own
// moduleRoot exactly, just returning an error instead of calling t.Fatal
// directly (this runs once inside a sync.Once, where only the first
// caller's own *testing.T would ever be in scope).
func v5AcceptModuleRoot() (string, error) {
	_, file, _, ok := stdruntime.Caller(0)
	if !ok {
		return "", fmt.Errorf("resolve this test file's own location")
	}
	directory := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory, nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", fmt.Errorf("go.mod not found above %s", file)
		}
		directory = parent
	}
}

// newClaudeAdapter builds a real claude.Adapter spawning the real
// cmd/fake-claude binary through f's own real ports.ProcessSupervisor.
// InheritedEnvironment lists AGENTKIT_HELPER_MODE/AGENTKIT_HELPER_OUTCOME
// (the real fake-CLI protocol's own mode-selection contract,
// internal/adapters/providers/fixtures.go) so a caller controls this
// adapter's own real spawned behavior by setting those two names in the
// CURRENT process's own environment (t.Setenv — safe, auto-restoring)
// before driving a Run through it; production code never populates
// ports.AgentExecutionRequest.Environment for AGENT at all (confirmed by
// reading internal/app/runtime/assemble_execution_request.go — no such
// caller exists anywhere), so this is the one real, sanctioned way a test
// process can choose the real fake CLI's own behavior per scenario.
func (f *v5AcceptFixture) newClaudeAdapter(t *testing.T) *claude.Adapter {
	t.Helper()
	adapter, err := claude.New(f.supervisor, claude.Config{
		Executable: v5AcceptFakeClaudeBinary(t), PermissionMode: "dontAsk",
		// AGENTKIT_HELPER_WRITE_PATH: see fixtures.go's own doc comment —
		// V5-15D's own "checker write attempt" scenario is the one real
		// caller of this today.
		InheritedEnvironment: []string{"AGENTKIT_HELPER_MODE", "AGENTKIT_HELPER_OUTCOME", "AGENTKIT_HELPER_WRITE_PATH"},
	})
	if err != nil {
		t.Fatalf("claude.New: %v", err)
	}
	return adapter
}

// newAgentExecutor wraps adapter in a real agentregistry.Registry (a real
// Capabilities() probe — a real `fake-claude --version` spawn) and a real
// runtime.AgentNodeExecutor.
func (f *v5AcceptFixture) newAgentExecutor(registry *agentregistry.Registry) *runtime.AgentNodeExecutor {
	return runtime.NewAgentNodeExecutor(
		f.uow, f.ids, f.artifacts, f.provider, f.store, registry,
		v5AcceptEventRegistry(), redact.NewMatcher(), f.store, clock.System{}, f.store, f.store,
	)
}

// newAgentRegistry wraps adapter in a real agentregistry.Registry (a real
// Capabilities() probe — a real `fake-claude --version` spawn). The SAME
// returned instance must be passed to BOTH newAgentExecutor and
// registerHandlersWithAgents — see that method's own doc comment for why
// (ExecuteNodeHandler's own admission-time drift check resolves against
// this exact registry, not AgentNodeExecutor's own internal one).
func (f *v5AcceptFixture) newAgentRegistry(t *testing.T, adapter *claude.Adapter) *agentregistry.Registry {
	t.Helper()
	registry, err := agentregistry.New(context.Background(), adapter)
	if err != nil {
		t.Fatalf("agentregistry.New: %v", err)
	}
	return registry
}

// registerAgentBuild pins a real AdapterBuild for adapter — its own
// ExecutableContentHash is the real fake-claude binary's real file hash,
// and its own CapabilityManifest comes from a real, live Capabilities()
// probe (the exact same shape adapterbuild.VerifyNoDrift itself
// re-derives at every admission — a fabricated manifest would simply fail
// drift-check on the very first real Attempt, never silently pass).
func (f *v5AcceptFixture) registerAgentBuild(t *testing.T, adapter *claude.Adapter) string {
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
		ExecutableContentHash: contentHash, ProtocolVersion: capabilities.ProtocolVersion,
		CapabilityManifestHash: manifestHash, OS: stdruntime.GOOS, Toolchain: stdruntime.Version(),
		ConfigIdentity: "v5accept",
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
		t.Fatalf("InsertIfAbsent(AdapterBuild): %v", err)
	}
	return build.ID()
}

// v5AcceptContextPolicyDocument is a minimal real CONTEXT-category policy
// — Selector matches no real Skill/Layer resource this package ever
// publishes under it (deliberately: the fake-claude protocol never reads
// its own real prompt content, so this profile needs zero real context
// resources resolved, only a real, resolvable ContextPolicyRef pin).
func v5AcceptContextPolicyDocument() policy.PolicyDocument {
	return policy.PolicyDocument{
		Category: policy.CategoryContext,
		Context:  &policy.ContextRules{Selector: []string{"v5accept-agent-context"}, Budget: policy.ContextBudget{MaxTokens: 4096}},
	}
}

// publishAgentProfileVersion publishes a real AgentProfileDocument pinning
// ProviderKey to the real ports.ProviderClaude this package's own real
// claude.Adapter reports.
func publishAgentProfileVersion(t *testing.T, uow ports.UnitOfWork, definitionID, versionID, contextPolicyDefID, contextPolicyVersionID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := definitions.CreateDefinition(ctx, uow, testCmd("v5a-def-"+definitionID, ports.InstallationScope(), "CreateDefinition"), definitions.CreateDefinitionRequest{
		DefinitionID: definitionID, Kind: definition.KindAgentProfile, Scope: definition.GlobalScope(), Name: "agent profile " + definitionID,
	}); err != nil {
		t.Fatalf("CreateDefinition(%s): %v", definitionID, err)
	}
	if _, err := definitions.PublishDefinitionVersion(ctx, uow, testCmd("v5a-pub-"+versionID, ports.InstallationScope(), "PublishDefinitionVersion"), definitions.PublishDefinitionVersionRequest{
		DefinitionID: definitionID, Kind: definition.KindAgentProfile,
		Compile: func() (definition.VersionFields, error) {
			return agentprofile.Compile(
				agentprofile.AgentProfileDefinition{ID: agentprofile.AgentProfileDefinitionID(definitionID), Fields: definition.Fields{
					Kind: definition.KindAgentProfile, Scope: definition.GlobalScope(), Name: "agent profile", Status: definition.StatusDraft, Version: 1,
				}},
				agentprofile.PublishRequest{
					VersionID: agentprofile.AgentProfileVersionID(versionID), VersionNumber: 1, SchemaVersion: 1,
					Document: agentprofile.AgentProfileDocument{
						ProviderKey: string(ports.ProviderClaude), Model: "fake-model", ToolRefs: []string{"read_file"},
						ContextPolicyRef: definition.DependencyPin{Kind: definition.KindPolicy, DefinitionID: contextPolicyDefID, VersionID: contextPolicyVersionID},
						Compatibility:    agentprofile.Compatibility{OS: []string{stdruntime.GOOS}},
						Budget:           agentprofile.Budget{MaxTokens: 4096},
					},
					PublishedBy: "operator-1", PublishedAt: time.Now().UTC(),
				},
			)
		},
	}); err != nil {
		t.Fatalf("PublishDefinitionVersion(%s): %v", versionID, err)
	}
}
