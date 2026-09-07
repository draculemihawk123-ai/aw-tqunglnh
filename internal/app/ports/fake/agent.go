package fake

import (
	"context"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// AgentExecutor is a fully scriptable ports.AgentExecutor test double —
// every method returns whatever the test pre-loaded, or a zero value plus
// nil error by default. CapabilitiesCalls counts live Capabilities(ctx)
// invocations so a test can assert a fresh probe actually happened (as
// opposed to a cached value), the same "Calls int" pattern
// fake.NodeExecutor already established.
type AgentExecutor struct {
	CapabilitiesResult ports.AgentCapabilities
	CapabilitiesErr    error
	CapabilitiesCalls  int

	StartResult ports.AgentExecutionResult
	StartErr    error

	ResumeResult ports.AgentExecutionResult
	ResumeErr    error

	CancelErr error
}

var _ ports.AgentExecutor = (*AgentExecutor)(nil)

func (e *AgentExecutor) Capabilities(context.Context) (ports.AgentCapabilities, error) {
	e.CapabilitiesCalls++
	return e.CapabilitiesResult, e.CapabilitiesErr
}

func (e *AgentExecutor) Start(context.Context, ports.AgentExecutionRequest, ports.AgentEventSink) (ports.AgentExecutionResult, error) {
	return e.StartResult, e.StartErr
}

func (e *AgentExecutor) Resume(context.Context, ports.AgentExecutionRequest, ports.ProviderSessionRef, ports.AgentEventSink) (ports.AgentExecutionResult, error) {
	return e.ResumeResult, e.ResumeErr
}

func (e *AgentExecutor) Cancel(context.Context, ports.ExecutionAttemptID) error {
	return e.CancelErr
}
