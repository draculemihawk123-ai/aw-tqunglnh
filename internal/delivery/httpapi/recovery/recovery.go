// Package recovery is V6-06D's own HTTP surface
// (docs/design/08-v6-api-projections.md V6-06D: "transport cho
// RetryBlockedActivation, CancelWorkItem, ResolveWorkItemBlocker"): a thin
// dispatch layer over three existing, already-tested internal/app/runtime
// commands — retry_blocked_activation.go, cancel_work_item.go,
// resolve_work_item_blocker.go — never a generic status/state setter, and
// never a direct call into the internal recovery worker/scheduler/finalize
// pipeline (this task's own "Không làm" line; proved mechanically by
// internal/archtest's own TestRecoveryHTTPNeverReachesSchedulerOrWorker,
// mirroring V6-06's own TestRunControlHTTPNeverReachesSchedulerOrWorker).
//
// All three wrapped commands share one trait that sets this package apart
// from internal/delivery/httpapi/workitem's own CommandEnvelope/receipt-
// replay flow (V6-02): none of them takes a ports.Command, an
// Idempotency-Key or an If-Match/ExpectedVersion precondition — confirmed by
// reading all three signatures before writing any handler here, per this
// task's own explicit review instruction:
//
//   - RetryBlockedActivationHandler.Retry(ctx, RetryBlockedActivationRequest{
//     NodeRunID, Actor, Reason, CorrelationID}) — idempotent by the admission
//     blocker's own State (AlreadyRetried covers a redelivered or
//     concurrently-raced duplicate call; a revalidation failure is a
//     structured business result, never an error — see retry.go's own doc
//     comment).
//   - CancelWorkItem(ctx, uow, ids, CancelWorkItemRequest{WorkItemID, Actor,
//     Reason, CorrelationID}) — idempotent by WorkItemID via its own durable
//     WorkItemCancellationIntent, the WorkItem-level mirror of
//     internal/delivery/httpapi/run's own CancelRun handler (cancel.go there
//     documents the identical reasoning at length; this package does not
//     repeat it).
//   - ResolveWorkItemBlocker(ctx, uow, ResolveWorkItemBlockerRequest{
//     BlockerID, Mode, Actor, Reason, PolicyGrantRef, CorrelationID}) —
//     idempotent by BlockerID: a blocker already RESOLVED/WAIVED replays as
//     AlreadyResolved, never a second decision.
//
// Every route therefore follows the SAME shorter flow run.CancelRunHandler
// already established (not workitem's CommandEnvelope one): authenticate
// (BindPrincipal, already wired by the composition root) → decode/validate
// the request body's own required fields → dispatch the one real,
// already-existing application command → map its result/error onto the
// shared httpapi envelope. Actor always comes from the bound
// LocalPrincipalSnapshot (ADR-028), never from a request body/header field —
// none of this package's three request DTOs even declares one.
package recovery

import (
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// maxRecoveryCommandBodyBytes bounds every POST body this package decodes —
// each of the three request DTOs is a handful of short string fields, the
// same reasoning run.maxCancelRunBodyBytes (internal/delivery/httpapi/run/cancel.go)
// already documents for an identically-shaped body.
const maxRecoveryCommandBodyBytes = 1 << 16

// Dependencies is everything this package's three handlers need from the
// composition root.
type Dependencies struct {
	// UOW is the one real ports.UnitOfWork every handler dispatches
	// through.
	UOW ports.UnitOfWork
	// IDs mints the one new ID RetryBlockedActivationHandler.Retry itself
	// mints (a reactivated NodeRun's own ID) — CancelWorkItem also takes
	// one (its own WorkItemCancellationIntent ID); ResolveWorkItemBlocker
	// takes none (see resolveblocker.go).
	IDs idsource.Source
	// Isolation and Agents are RetryBlockedActivationHandler's own V5-08
	// admission dependencies (see retry_blocked_activation.go's own struct
	// field doc comments) — Retry re-runs the SAME real isolation-
	// enforceability and adapter-build-drift checks admission originally
	// ran, so it needs the SAME two dependencies a production
	// ExecuteNodeHandler already carries. See this package's own
	// cmd/aw/serve.go composition-root wiring (and that file's own doc
	// comment on the two new --*-executable flags) for how these are
	// really constructed in the aw serve binary, and this package's own
	// checklist narrative (baocaov6checklist.md, section V6-06D) for the
	// full reasoning behind that choice.
	Isolation ports.IsolationEnforcementChecker
	Agents    *agentregistry.Registry
}

// RegisterRoutes registers this package's own three route fragments —
// operationId retryBlockedActivation (POST
// /node-runs/{nodeRunId}/retry-blocked-activation), cancelWorkItem (POST
// /work-items/{workItemId}/cancel) and resolveWorkItemBlocker (POST
// /work-item-blockers/{blockerId}/resolve), names locked in by
// docs/design/11-v6-00-ux-artifact.md's own action inventory (Screen 7 row
// 5, Screen 8 row 5/7) — into routes. Every path is a flat, single-ID
// resource path, never project-prefixed: mirrors
// internal/delivery/httpapi/run's own identical choice for CancelRun/
// StartWorkflowRun, for the identical reason — none of the three commands
// this package wraps takes a ProjectID field at all, so there is nothing
// for a {projectId} path segment to ever validate against (every handler
// still reloads and scope-checks its own real target before dispatch; see
// each handler's own doc comment). This function only ever adds its own
// three descriptors; contract point 8 ("Parallel work không sửa registry
// chung... Chỉ V6-12 compose HTTP router/OpenAPI") reserves aggregation
// across every endpoint task's fragments for V6-12 alone.
func RegisterRoutes(routes *httpapi.RouteRegistry, deps Dependencies) {
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/node-runs/{nodeRunId}/retry-blocked-activation", OperationID: "retryBlockedActivation",
		ScopeKind: httpapi.ScopeProject, RequestSchema: RetryBlockedActivationRequest{}, ResponseSchema: RetryBlockedActivationResponse{},
		Handler: RetryBlockedActivationHTTPHandler(deps),
	})
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/work-items/{workItemId}/cancel", OperationID: "cancelWorkItem",
		ScopeKind: httpapi.ScopeProject, RequestSchema: CancelWorkItemRequest{}, ResponseSchema: CancelWorkItemResponse{},
		Handler: CancelWorkItemHTTPHandler(deps),
	})
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/work-item-blockers/{blockerId}/resolve", OperationID: "resolveWorkItemBlocker",
		ScopeKind: httpapi.ScopeProject, RequestSchema: ResolveWorkItemBlockerRequest{}, ResponseSchema: ResolveWorkItemBlockerResponse{},
		Handler: ResolveWorkItemBlockerHTTPHandler(deps),
	})
}
