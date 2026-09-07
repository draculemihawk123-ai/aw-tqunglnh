package agentregistry

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

var (
	ErrUnknownProvider     = errors.New("agent provider is not registered")
	ErrDuplicateProvider   = errors.New("agent provider is registered more than once")
	ErrMissingCapability   = errors.New("agent provider does not satisfy required capability")
	ErrInvalidCapabilities = errors.New("agent executor reported invalid capabilities")
)

type Requirements struct {
	Resume     bool
	Cancel     bool
	EventKinds []ports.AgentEventKind
}

// Registry is the application boundary that selects a provider adapter. It
// depends only on the neutral AgentExecutor port; neither workflow nor worker
// code needs a Claude/Codex switch.
type Registry struct {
	entries map[ports.ProviderKey]entry
}

type entry struct {
	executor     ports.AgentExecutor
	capabilities ports.AgentCapabilities
}

// Empty returns a Registry with no providers registered — useful for a
// caller (or a test) with no real adapter builds ever pinned yet. New with
// zero executors can never itself fail (its own loop never runs), so this
// needs no error return.
func Empty() *Registry {
	registry, _ := New(context.Background())
	return registry
}

func New(ctx context.Context, executors ...ports.AgentExecutor) (*Registry, error) {
	registry := &Registry{entries: make(map[ports.ProviderKey]entry, len(executors))}
	for _, executor := range executors {
		if executor == nil {
			return nil, errors.New("agent executor is nil")
		}
		capabilities, err := executor.Capabilities(ctx)
		if err != nil {
			return nil, fmt.Errorf("read agent executor capabilities: %w", err)
		}
		if err := validateCapabilities(capabilities); err != nil {
			return nil, err
		}
		if _, exists := registry.entries[capabilities.Provider]; exists {
			return nil, fmt.Errorf("%w: %s", ErrDuplicateProvider, capabilities.Provider)
		}
		registry.entries[capabilities.Provider] = entry{executor: executor, capabilities: cloneCapabilities(capabilities)}
	}
	return registry, nil
}

func (r *Registry) Resolve(provider ports.ProviderKey, requirements Requirements) (ports.AgentExecutor, ports.AgentCapabilities, error) {
	if r == nil {
		return nil, ports.AgentCapabilities{}, errors.New("agent executor registry is nil")
	}
	entry, exists := r.entries[provider]
	if !exists {
		return nil, ports.AgentCapabilities{}, fmt.Errorf("%w: %s", ErrUnknownProvider, provider)
	}
	if requirements.Resume && !entry.capabilities.SupportsResume {
		return nil, ports.AgentCapabilities{}, fmt.Errorf("%w: %s does not support resume", ErrMissingCapability, provider)
	}
	if requirements.Cancel && !entry.capabilities.SupportsCancel {
		return nil, ports.AgentCapabilities{}, fmt.Errorf("%w: %s does not support cancel", ErrMissingCapability, provider)
	}
	availableEvents := make(map[ports.AgentEventKind]struct{}, len(entry.capabilities.CanonicalEventKinds))
	for _, kind := range entry.capabilities.CanonicalEventKinds {
		availableEvents[kind] = struct{}{}
	}
	for _, kind := range requirements.EventKinds {
		if _, exists := availableEvents[kind]; !exists {
			return nil, ports.AgentCapabilities{}, fmt.Errorf("%w: %s lacks event %s", ErrMissingCapability, provider, kind)
		}
	}
	return entry.executor, cloneCapabilities(entry.capabilities), nil
}

func (r *Registry) Providers() []ports.ProviderKey {
	if r == nil {
		return nil
	}
	providers := make([]ports.ProviderKey, 0, len(r.entries))
	for provider := range r.entries {
		providers = append(providers, provider)
	}
	sort.Slice(providers, func(left, right int) bool { return providers[left] < providers[right] })
	return providers
}

func validateCapabilities(capabilities ports.AgentCapabilities) error {
	if capabilities.Provider == "" || capabilities.AdapterVersion == "" || capabilities.ProtocolVersion == "" ||
		!capabilities.SupportsStart {
		return ErrInvalidCapabilities
	}
	seen := make(map[ports.AgentEventKind]struct{}, len(capabilities.CanonicalEventKinds))
	for _, kind := range capabilities.CanonicalEventKinds {
		if kind == "" {
			return ErrInvalidCapabilities
		}
		if _, duplicate := seen[kind]; duplicate {
			return ErrInvalidCapabilities
		}
		seen[kind] = struct{}{}
	}
	return nil
}

func cloneCapabilities(source ports.AgentCapabilities) ports.AgentCapabilities {
	result := source
	result.CanonicalEventKinds = append([]ports.AgentEventKind(nil), source.CanonicalEventKinds...)
	return result
}
