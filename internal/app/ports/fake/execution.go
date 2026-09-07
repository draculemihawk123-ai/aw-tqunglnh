package fake

import (
	"context"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// NodeExecutor is a scripted ports.NodeExecutor for tests (V4-05): no real
// provider/command adapter I/O, just whatever Result/Err a test sets, or
// Block (closed by the test) to simulate an executor that keeps running
// past its own attempt deadline — the "timeout" scenario, where the
// envelope's own derived-deadline context is what actually decides
// TIMED_OUT, not a value this fake returns.
type NodeExecutor struct {
	Result ports.NodeExecutionResult
	Err    error
	// Block, when non-nil, makes Execute wait on ctx.Done() instead of
	// returning Result/Err immediately — the fixture for a "the executor
	// never finishes in time" test: the caller's own derived-deadline
	// context expires first, and Execute returns that context's own error.
	Block chan struct{}
	// Calls counts every Execute call this fake has ever received — V5-04's
	// own "provider không start nếu snapshot chưa durable" bar is exactly
	// "executor spawn count bằng 0" on the rejected path, and this field is
	// what a test asserts that against.
	Calls int
}

var _ ports.NodeExecutor = (*NodeExecutor)(nil)

func (e *NodeExecutor) Execute(ctx context.Context, _ ports.NodeExecutionRequest) (ports.NodeExecutionResult, error) {
	e.Calls++
	if e.Block != nil {
		select {
		case <-ctx.Done():
			return ports.NodeExecutionResult{}, ctx.Err()
		case <-e.Block:
		}
	}
	if e.Err != nil {
		return ports.NodeExecutionResult{}, e.Err
	}
	return e.Result, nil
}
