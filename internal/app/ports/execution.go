package ports

import (
	"context"
	"encoding/json"

	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// NodeExecutionRequest is what ExecuteNodeHandler (internal/app/runtime,
// V4-05) hands to a NodeExecutor to actually perform one ExecutionAttempt's
// own work.
type NodeExecutionRequest struct {
	AttemptID            string
	NodeRunID            string
	RunID                string
	ExecutorKind         string
	ExecutionProfileHash string
}

// NodeExecutionResult is what a NodeExecutor proposes back. This proposal
// is deliberately never trusted or committed as-is (GC-INV-17/18):
// FinalizeExecutionAttempt is the sole authority that decides whether to
// accept it, based on whether the JobLease/WriteLease fencing this
// executor ran under is still valid at acceptance time — "worker chỉ
// propose outcome; orchestrator quyết transition" (V4-05's own Hoàn thành
// khi). State is only ever SUCCEEDED, FAILED or (V4-12A) BLOCKED: TIMED_OUT
// and the "cancelled context, do not finalize" case are both decided by
// the envelope itself observing its own derived-deadline context, never
// proposed by the executor as a result value (see ExecuteNodeHandler's own
// doc comment for why).
type NodeExecutionResult struct {
	State             runtime.ExecutionAttemptState
	TerminationReason runtime.TerminationReason
	// SelectedOutcome is the NodeRun outcome to route on — required (and
	// must name one of the node's own declared Outcomes; AdvanceRun's own
	// GC-INV-11 allow-list check is the actual enforcement point) when
	// State == SUCCEEDED, meaningless otherwise.
	SelectedOutcome string
	// ErrorCode is populated now (V4-06): required when State == FAILED —
	// the exact errorcode.Code (go-core-spec §18) this executor classifies
	// its own failure as, so ExecuteNodeHandler's retry decision
	// (FinalizeExecutionAttempt) can check it against the pinned
	// AttemptRules.RetryableErrorCodes allow-list. Never populated for
	// State == SUCCEEDED. TIMED_OUT is deliberately NOT a value this type
	// itself ever carries — the execution envelope's own derived deadline
	// decides that outcome, never the executor (see ExecuteNodeHandler's
	// own doc comment).
	ErrorCode     errorcode.Code
	ResultPayload json.RawMessage
	// RequestedScopeExpansion is populated now (V4-12A, confirmed with the
	// user before writing this task's code): required when State ==
	// ExecutionAttemptBlocked, meaningless (and rejected as
	// OUTCOME_REJECTED if present) otherwise — mutually exclusive with
	// SelectedOutcome/ErrorCode, exactly like those two are already
	// mutually exclusive with each other. The executor only ever
	// PROPOSES; FinalizeExecutionAttempt validates/canonicalizes it before
	// anything durable (a ScopeExpansionOrigin, a BLOCKED Attempt/NodeRun)
	// is ever built from it — never itself a grant.
	RequestedScopeExpansion *runtime.ScopeExpansionProposal
}

// NodeExecutor executes one ExecutionAttempt's actual work — a real
// provider/command adapter in production (V5), a scripted fake in tests
// (V4-05's own scope, ports/fake.NodeExecutor). Execute must respect ctx
// cancellation/deadline: ExecuteNodeHandler derives a deadline from the
// Attempt's own pinned TimeoutSeconds and relies on Execute returning
// (possibly with ctx.Err()) once that deadline (or an outer cancellation)
// fires, rather than running unboundedly in the background.
type NodeExecutor interface {
	Execute(ctx context.Context, req NodeExecutionRequest) (NodeExecutionResult, error)
}
