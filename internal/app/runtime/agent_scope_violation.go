package runtime

import (
	"context"

	"github.com/taQuangLing/agent-workflow/internal/app/agentevents"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// scopeViolationResult is AgentNodeExecutor.classify's single answer for an
// attempt whose process changed something outside what it may change —
// whichever check noticed it: agentevents.Sink's mid-run checkpoint (the
// error surfaces as the provider adapter's own error, wrapped with %w all the
// way up), buildEvidence's final post-quiescence diff, or a strict read-only
// check. The attempt is FAILED with ADR-020's SCOPE_VIOLATION termination
// reason and the SCOPE_VIOLATION error code — never PROVIDER_UNAVAILABLE
// (V9-09): the provider was reachable and did what it was asked; it is the
// attempt's writes that were out of bounds, and an operator told "provider
// unavailable" looks at the wrong layer.
//
// cause is also what tells the operator WHICH paths: it is written into the
// attempt's agent_events stream as a SCOPE_VIOLATION DIAGNOSTIC
// (agentevents.RecordScopeViolation), which GetRunTimeline reads back as the
// attempt's failureDetail. That annotation is best-effort on purpose — the
// classification above is already decided from cause and must not change if
// the diagnostic cannot be stored (for instance because a lease was lost, in
// which case finalize will refuse this attempt anyway).
func (e *AgentNodeExecutor) scopeViolationResult(
	ctx context.Context, req ports.NodeExecutionRequest, resolved resolvedExecutionResources, cause error,
) ports.NodeExecutionResult {
	return scopeViolationNodeResult(ctx, e.uow, e.ids, e.clk, e.matcher, req, resolved, cause)
}

// scopeViolationNodeResult is scopeViolationResult for any executor that
// holds the same collaborators — GateNodeExecutor's strict read-only check
// ends in the identical FAILED/SCOPE_VIOLATION answer and records the same
// operator-visible diagnostic.
func scopeViolationNodeResult(
	ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, clk clock.Clock, matcher redact.Matcher,
	req ports.NodeExecutionRequest, resolved resolvedExecutionResources, cause error,
) ports.NodeExecutionResult {
	_ = agentevents.RecordScopeViolation(ctx, agentevents.ScopeViolationRecord{
		AttemptID: req.AttemptID, JobLease: req.JobLease, WriteLeases: resolved.writeLeaseGrants,
		UOW: uow, IDs: ids, Clock: clk, Matcher: matcher,
	}, cause)
	return ports.NodeExecutionResult{
		State: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonScopeViolation,
		ErrorCode: errorcode.CodeScopeViolation,
	}
}
