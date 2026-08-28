package agentregistry

import (
	"context"
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

func TestRegistryResolvesProviderByCapabilities(t *testing.T) {
	claude := &fakeExecutor{capabilities: testCapabilities(ports.ProviderClaude, true, true)}
	codex := &fakeExecutor{capabilities: testCapabilities(ports.ProviderCodex, true, true)}
	registry, err := New(context.Background(), claude, codex)
	if err != nil {
		t.Fatal(err)
	}

	executor, capabilities, err := registry.Resolve(ports.ProviderCodex, Requirements{
		Resume: true,
		Cancel: true,
		EventKinds: []ports.AgentEventKind{
			ports.AgentEventCheckpointProposed,
			ports.AgentEventExecutionFinished,
		},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if capabilities.Provider != ports.ProviderCodex || executor != codex {
		t.Fatalf("resolved unexpected executor/capabilities: %#v %#v", executor, capabilities)
	}
	providers := registry.Providers()
	if len(providers) != 2 || providers[0] != ports.ProviderClaude || providers[1] != ports.ProviderCodex {
		t.Fatalf("Providers() = %#v", providers)
	}
}

func TestRegistryRejectsUnknownAndMissingCapabilities(t *testing.T) {
	registry, err := New(context.Background(), fakeExecutor{capabilities: testCapabilities(ports.ProviderClaude, false, false)})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := registry.Resolve(ports.ProviderCodex, Requirements{}); !errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("unknown Resolve() error = %v", err)
	}
	if _, _, err := registry.Resolve(ports.ProviderClaude, Requirements{Resume: true}); !errors.Is(err, ErrMissingCapability) {
		t.Fatalf("resume Resolve() error = %v", err)
	}
	if _, _, err := registry.Resolve(ports.ProviderClaude, Requirements{EventKinds: []ports.AgentEventKind{"MISSING"}}); !errors.Is(err, ErrMissingCapability) {
		t.Fatalf("event Resolve() error = %v", err)
	}
}

func TestRegistryRejectsDuplicateProvider(t *testing.T) {
	_, err := New(context.Background(),
		fakeExecutor{capabilities: testCapabilities(ports.ProviderClaude, true, true)},
		fakeExecutor{capabilities: testCapabilities(ports.ProviderClaude, true, true)},
	)
	if !errors.Is(err, ErrDuplicateProvider) {
		t.Fatalf("New() error = %v, want ErrDuplicateProvider", err)
	}
}

type fakeExecutor struct {
	capabilities ports.AgentCapabilities
}

func (f fakeExecutor) Capabilities(context.Context) (ports.AgentCapabilities, error) {
	return f.capabilities, nil
}

func (fakeExecutor) Start(context.Context, ports.AgentExecutionRequest, ports.AgentEventSink) (ports.AgentExecutionResult, error) {
	return ports.AgentExecutionResult{}, errors.New("not used")
}

func (fakeExecutor) Resume(context.Context, ports.AgentExecutionRequest, ports.ProviderSessionRef, ports.AgentEventSink) (ports.AgentExecutionResult, error) {
	return ports.AgentExecutionResult{}, errors.New("not used")
}

func (fakeExecutor) Cancel(context.Context, ports.ExecutionAttemptID) error {
	return errors.New("not used")
}

func testCapabilities(provider ports.ProviderKey, resume, cancel bool) ports.AgentCapabilities {
	return ports.AgentCapabilities{
		Provider:        provider,
		AdapterVersion:  "test/v1",
		ProtocolVersion: "test/v1",
		SupportsStart:   true,
		SupportsResume:  resume,
		SupportsCancel:  cancel,
		CanonicalEventKinds: []ports.AgentEventKind{
			ports.AgentEventCheckpointProposed,
			ports.AgentEventExecutionFinished,
		},
	}
}
