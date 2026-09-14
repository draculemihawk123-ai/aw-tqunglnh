// Package decision is V6-06A's own HTTP surface for the two typed human
// decision endpoints docs/design/08-v6-api-projections.md V6-06A names
// ("expose approve/reject và typed WAIT signal tách khỏi free-text
// conversation"): a thin dispatch layer over internal/app/runtime's own
// ResolveApproval (approval.go) and SignalWait (wait.go) — never a place a
// chat message's free text could drive a control decision (this task's own
// "Không làm" line), and never a place that reads an actor/role claim out of
// a request body (ADR-028).
//
// Both handlers in this package follow V6-02's full CommandEnvelope flow
// (Idempotency-Key required, canonical semantic hash, httpapi.LookupReceipt
// fast path, replay-or-conflict-or-dispatch) — ResolveApproval and
// SignalWait both take a real ports.Command and both do their own receipt
// lookup/replay inside their own transaction (V1-06's idempotent-command
// shape, identical to runtime.StartWorkflowRun). Beyond that shared shape,
// the two handlers deliberately DIVERGE on whether a strong If-Match
// precondition is required — confirmed by reading both approval.go's and
// wait.go's own package doc comments in full, and by reading run.go's own
// StartWorkflowRun/CancelRun split, before writing either handler here:
//
//   - resolveApproval (approval.go) DOES require If-Match. A human operator
//     viewing an ApprovalRequest through a UI has necessarily just loaded
//     its current version, so echoing that version back as a precondition
//     before deciding is exactly workitem/scope_expansion_commands.go's own
//     ApproveScopeExpansion/RejectScopeExpansion/WithdrawScopeExpansion
//     precedent: reload the target ONCE (also used to derive scope and to
//     catch a stale/cross-run RunID before ever touching Idempotency-Key or
//     the body), require a strong If-Match, and compare the caller's
//     claimed version against that single reload — belt-and-suspenders
//     alongside ResolveApproval's own INTERNAL fenced CAS
//     (TransitionApprovalRequestRequest), which remains the one true
//     race-decider (this package's own reload+compare can only ever catch
//     an ALREADY-stale caller, never fully replace the command's own
//     transactional CAS — two concurrent callers who both read the same
//     fresh version still race safely inside ResolveApproval itself, and
//     exactly one gets Won=true).
//
//   - submitWaitSignal (wait.go) deliberately does NOT require If-Match —
//     mirroring run/cancel.go's own reasoning for CancelRun, not
//     ApproveScopeExpansion's. A WAIT signal's caller is typically an
//     external system (e.g. a CI webhook) reporting a real-world event it
//     identifies by SignalKey, not a human who has just reloaded a UI page;
//     SignalWait's own doc comment is explicit that the exact same event
//     reported through a genuinely different command invocation must still
//     be recognized as one signal and safely no-op (Won=false, still a 200,
//     never an error) — REQUIRING a caller to already know and echo back
//     the WaitRegistration's current numeric Version before every retry
//     would defeat that: a legitimate duplicate delivery that has no way of
//     knowing whether an earlier delivery already consumed the registration
//     (and therefore what version it is now at) would be rejected with a
//     stale-precondition error instead of being gracefully deduplicated.
//     The registration is still reloaded once, exactly like the approval
//     endpoint, purely to derive scope and catch a stale/cross-run RunID —
//     just never compared against a client-claimed version.
//
// DecisionArtifact references (V6-06A's own "Phạm vi" line): reading
// ResolveApproval/SignalWait's own source confirms neither call records a
// new runtime.DecisionArtifact row today — HE-08-M08's own audit
// requirement ("MUST ghi actor, cause, previous/new state, time") is
// already satisfied by the ApprovalRequest/WaitRegistration row itself
// (DecidedBy/DecidedRole/DecidedOutcome/Reason/DecidedAt, or
// ConsumedSignalID — see internal/domain/runtime/approval.go's and
// wait.go's own doc comments). This package therefore surfaces that
// existing row's own ID/State (already present on
// runtime.ResolveApprovalResult/SignalWaitResult) as the durable audit
// reference a caller follows up on — never inventing a new
// DecisionArtifact write inside an HTTP handler, which would violate this
// whole package's own "handler chỉ dispatch" discipline (run.go's own line,
// reused here) by adding business logic no application-layer command
// itself performs.
//
// Outcome is deliberately NOT two separate routes (contrast
// workitem/routes.go's own three distinct
// approveScopeExpansion/rejectScopeExpansion/withdrawScopeExpansion
// commands): docs/design/11-v6-00-ux-artifact.md row 11 states outright
// that approve/reject are "hai nút/leaf riêng cho cùng một command với
// Outcome khác nhau, không phải hai authority" — ResolveApprovalRequest.Outcome
// is an open vocabulary the node's own compiled ApprovalNodeConfig
// declares (never a closed approved/rejected enum: GC-INV-11's own
// allow-list check inside advanceRunTx is what actually validates it) — so
// one route with an Outcome field in the body is the only shape that can
// stay correct for a node declaring more than two outcomes.
package decision

import (
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// Dependencies is what every handler in this package needs from the
// composition root — the same shape run.Dependencies already established
// for a sibling Run-scoped task: the real ports.UnitOfWork and
// idsource.Source every application command in this codebase already
// takes, nothing httpapi-specific and nothing this package cannot get from
// a real cmd/aw/serve.go wiring today.
type Dependencies struct {
	UOW ports.UnitOfWork
	IDs idsource.Source
}

// RegisterRoutes registers this package's own two route fragments —
// operationId resolveApproval (POST
// /runs/{runId}/approval-requests/{approvalRequestId}/resolve) and
// operationId submitWaitSignal (POST
// /runs/{runId}/wait-registrations/{waitRegistrationId}/signal), both names
// locked in by docs/design/11-v6-00-ux-artifact.md's own action inventory
// (Screen 7 rows 11/12) — into routes. This function only ever adds its OWN
// two descriptors; contract point 8 ("Parallel work không sửa registry
// chung... Chỉ V6-12 compose HTTP router/OpenAPI") reserves aggregation
// across every endpoint task's fragments for V6-12 alone.
func RegisterRoutes(routes *httpapi.RouteRegistry, deps Dependencies) {
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/runs/{runId}/approval-requests/{approvalRequestId}/resolve", OperationID: "resolveApproval",
		ScopeKind: httpapi.ScopeProject, RequestSchema: ResolveApprovalBody{}, ResponseSchema: ResolveApprovalResponse{},
		Handler: ResolveApprovalHandler(deps),
	})
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/runs/{runId}/wait-registrations/{waitRegistrationId}/signal", OperationID: "submitWaitSignal",
		ScopeKind: httpapi.ScopeProject, RequestSchema: SubmitWaitSignalBody{}, ResponseSchema: SubmitWaitSignalResponse{},
		Handler: SubmitWaitSignalHandler(deps),
	})
}
