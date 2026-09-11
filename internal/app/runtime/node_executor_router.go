// This file closes the acceptance gap the 2026-09-10 post-merge review
// found in already-merged V5-09/V5-10: no production composition ever
// selected a NodeExecutor by ExecutorKind (schedule.go's own resolved
// profile field, populated at every ScheduleExecutableNodeRun call for
// AGENT/COMMAND/MACHINE_GATE alike). ExecuteNodeHandler (execute.go) has
// always taken exactly one ports.NodeExecutor slot, plumbed straight
// through from ExecuteNodeHandler.Handle's own ports.NodeExecutionRequest
// construction — every test so far filled that ONE slot directly with a
// single-kind executor (fake.NodeExecutor, a real *AgentNodeExecutor, a
// real *CommandNodeExecutor, or a real *GateNodeExecutor), never a
// dispatcher that could actually serve a workflow mixing node kinds.
// NodeExecutorRouter is that dispatcher: it implements ports.NodeExecutor
// itself, so it drops straight into ExecuteNodeHandler's existing single
// slot without any change to execute.go.
package runtime

import (
	"context"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// NodeExecutorRouter dispatches ports.NodeExecutor.Execute to the
// injected executor matching req.ExecutorKind — the same
// runtimedomain.ExecutorKind string schedule.go's own resolveExecutionProfile
// already pins onto every ResolvedExecutionProfileV1 and execute.go's own
// Handle already reads back out (profile.Executor.Kind) and forwards
// unchanged as ports.NodeExecutionRequest.ExecutorKind. A caller wires only
// the kinds it actually runs — a nil field for an unused kind fails closed
// with a clear error rather than a nil-pointer panic, exactly like an
// unrecognized kind string does.
type NodeExecutorRouter struct {
	Agent   ports.NodeExecutor
	Command ports.NodeExecutor
	Gate    ports.NodeExecutor
}

var _ ports.NodeExecutor = (*NodeExecutorRouter)(nil)

// Execute implements ports.NodeExecutor by dispatching on req.ExecutorKind.
func (r *NodeExecutorRouter) Execute(ctx context.Context, req ports.NodeExecutionRequest) (ports.NodeExecutionResult, error) {
	var executor ports.NodeExecutor
	switch runtimedomain.ExecutorKind(req.ExecutorKind) {
	case runtimedomain.ExecutorKindAgent:
		executor = r.Agent
	case runtimedomain.ExecutorKindCommand:
		executor = r.Command
	case runtimedomain.ExecutorKindMachineGate:
		executor = r.Gate
	default:
		return ports.NodeExecutionResult{}, fmt.Errorf("runtime: NodeExecutorRouter has no route for executor kind %q", req.ExecutorKind)
	}
	if executor == nil {
		return ports.NodeExecutionResult{}, fmt.Errorf("runtime: NodeExecutorRouter has no executor wired for kind %q", req.ExecutorKind)
	}
	return executor.Execute(ctx, req)
}
