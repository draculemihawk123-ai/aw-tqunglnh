package adapterbuild

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
)

func writeDriftFixture(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "provider-cli")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func pinnedBuild(t *testing.T, executablePath string, capabilities ports.AgentCapabilities) adapterbuild.Build {
	t.Helper()
	contentHash, err := HashExecutableFile(executablePath)
	if err != nil {
		t.Fatalf("hash fixture: %v", err)
	}
	manifest := adapterbuild.CapabilityManifest{
		SupportsStart:       capabilities.SupportsStart,
		SupportsResume:      capabilities.SupportsResume,
		SupportsCancel:      capabilities.SupportsCancel,
		CanonicalEventKinds: canonicalEventKindStrings(capabilities.CanonicalEventKinds),
	}
	_, manifestHash, err := adapterbuild.HashCapabilityManifest(manifest)
	if err != nil {
		t.Fatalf("hash manifest: %v", err)
	}
	tuple := adapterbuild.CandidateTuple{
		ProviderKey: string(capabilities.Provider), ExecutablePath: executablePath,
		ExecutableContentHash: contentHash, ProtocolVersion: capabilities.ProtocolVersion,
		CapabilityManifestHash: manifestHash, OS: runtime.GOOS, Toolchain: runtime.Version(),
		ConfigIdentity: "default",
	}
	build, err := adapterbuild.NewBuild(adapterbuild.NewBuildRequest{
		Tuple: tuple, CapabilityManifest: manifest, RegisteredBy: "operator-1", RegisteredAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("construct pinned build: %v", err)
	}
	return build
}

func TestVerifyNoDrift_MatchingExecutableAndCapabilities_NoError(t *testing.T) {
	t.Parallel()

	executablePath := writeDriftFixture(t, "binary-v1")
	capabilities := ports.AgentCapabilities{
		Provider: ports.ProviderClaude, AdapterVersion: "claude-stream-json/v1", ProtocolVersion: "claude-stream-json/v1",
		TestedCLIVersion: "1.0.0", SupportsStart: true, SupportsResume: true, SupportsCancel: true,
		CanonicalEventKinds: []ports.AgentEventKind{ports.AgentEventExecutionStarted},
	}
	build := pinnedBuild(t, executablePath, capabilities)
	executor := &fake.AgentExecutor{CapabilitiesResult: capabilities}

	if err := VerifyNoDrift(context.Background(), executor, build); err != nil {
		t.Fatalf("VerifyNoDrift = %v, want nil", err)
	}
	if executor.CapabilitiesCalls != 1 {
		t.Fatalf("CapabilitiesCalls = %d, want 1 (a fresh probe, not a cached value)", executor.CapabilitiesCalls)
	}
}

func TestVerifyNoDrift_ExecutableContentChanged_ReturnsDrift(t *testing.T) {
	t.Parallel()

	executablePath := writeDriftFixture(t, "binary-v1")
	capabilities := ports.AgentCapabilities{
		Provider: ports.ProviderClaude, AdapterVersion: "claude-stream-json/v1", ProtocolVersion: "claude-stream-json/v1",
		SupportsStart: true, SupportsResume: true, SupportsCancel: true,
	}
	build := pinnedBuild(t, executablePath, capabilities)

	if err := os.WriteFile(executablePath, []byte("binary-v2-swapped"), 0o755); err != nil {
		t.Fatalf("swap fixture: %v", err)
	}
	executor := &fake.AgentExecutor{CapabilitiesResult: capabilities}

	err := VerifyNoDrift(context.Background(), executor, build)
	if !errors.Is(err, ErrAdapterBuildDrifted) {
		t.Fatalf("VerifyNoDrift = %v, want ErrAdapterBuildDrifted", err)
	}
}

func TestVerifyNoDrift_ProtocolVersionChanged_ReturnsDrift(t *testing.T) {
	t.Parallel()

	executablePath := writeDriftFixture(t, "binary-v1")
	capabilities := ports.AgentCapabilities{
		Provider: ports.ProviderClaude, AdapterVersion: "claude-stream-json/v1", ProtocolVersion: "claude-stream-json/v1",
		SupportsStart: true, SupportsResume: true, SupportsCancel: true,
	}
	build := pinnedBuild(t, executablePath, capabilities)

	drifted := capabilities
	drifted.ProtocolVersion = "claude-stream-json/v2"
	executor := &fake.AgentExecutor{CapabilitiesResult: drifted}

	err := VerifyNoDrift(context.Background(), executor, build)
	if !errors.Is(err, ErrAdapterBuildDrifted) {
		t.Fatalf("VerifyNoDrift = %v, want ErrAdapterBuildDrifted", err)
	}
}

func TestVerifyNoDrift_ProbeFails_ReturnsDrift(t *testing.T) {
	t.Parallel()

	executablePath := writeDriftFixture(t, "binary-v1")
	capabilities := ports.AgentCapabilities{Provider: ports.ProviderClaude, ProtocolVersion: "v1", SupportsStart: true}
	build := pinnedBuild(t, executablePath, capabilities)
	executor := &fake.AgentExecutor{CapabilitiesErr: errors.New("executable not found")}

	err := VerifyNoDrift(context.Background(), executor, build)
	if !errors.Is(err, ErrAdapterBuildDrifted) {
		t.Fatalf("VerifyNoDrift = %v, want ErrAdapterBuildDrifted", err)
	}
}

func TestVerifyNoDrift_MissingExecutable_ReturnsDrift(t *testing.T) {
	t.Parallel()

	executablePath := writeDriftFixture(t, "binary-v1")
	capabilities := ports.AgentCapabilities{Provider: ports.ProviderClaude, ProtocolVersion: "v1", SupportsStart: true}
	build := pinnedBuild(t, executablePath, capabilities)

	if err := os.Remove(executablePath); err != nil {
		t.Fatalf("remove fixture: %v", err)
	}
	executor := &fake.AgentExecutor{CapabilitiesResult: capabilities}

	err := VerifyNoDrift(context.Background(), executor, build)
	if !errors.Is(err, ErrAdapterBuildDrifted) {
		t.Fatalf("VerifyNoDrift = %v, want ErrAdapterBuildDrifted", err)
	}
}
