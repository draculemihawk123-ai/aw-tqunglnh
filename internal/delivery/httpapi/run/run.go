// Package run is V6-06's own HTTP surface for Run start and cancellation
// controls (docs/design/08-v6-api-projections.md V6-06: "expose start/cancel
// Run bằng typed commands và quiesce semantics"): a thin dispatch layer over
// internal/app/runtime's own StartWorkflowRun (commands.go) and CancelRun
// (cancel_run.go) — never a generic status setter, never a scheduler/worker
// fast path reachable from HTTP (this task's own "Không làm" line; proved by
// internal/archtest's own TestRunControlHTTPNeverReachesSchedulerOrWorker).
//
// StartWorkflowRun and CancelRun are wrapped very differently ON PURPOSE —
// confirmed by re-reading both signatures side by side before writing any
// handler code here, exactly as this task's own instructions required:
//
//   - StartWorkflowRun takes a real ports.Command and does its own receipt
//     lookup/replay inside its transaction (V1-06's idempotent-command
//     shape, identical to internal/app/catalog.CreateProject). start.go
//     therefore follows V6-02's full CommandEnvelope flow: Idempotency-Key
//     required, canonical semantic hash, httpapi.LookupReceipt fast path,
//     replay-or-conflict-or-dispatch.
//
//   - CancelRun takes a plain CancelRunRequest{RunID, Actor, Reason,
//     CorrelationID} — no ports.Command, no IdempotencyKey field, no
//     ExpectedVersion field exist on it anywhere. This is not an oversight
//     for this package to paper over: ADR-020 §22 ("Cancellation protocol",
//     docs/architecture/02-architecture-decisions.md line 354) states
//     plainly "command là idempotent theo run" — CancelRun is idempotent BY
//     RUNID, structurally (cancel_run.go's own package doc comment: "a
//     duplicate call is a harmless no-op, never a second intent"), not by a
//     client-supplied Idempotency-Key feeding a command-receipt lookup.
//     cancel.go therefore never requires Idempotency-Key or If-Match — see
//     its own doc comment for the full reasoning, including why requiring
//     (and validating, but never actually using) an Idempotency-Key header
//     here would misrepresent the real contract rather than honor it.
package run

import (
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// Dependencies is what every handler in this package needs from the
// composition root: the real ports.UnitOfWork and idsource.Source every
// application command in this codebase already takes, nothing httpapi-
// specific and nothing this package cannot get from a real cmd/aw/serve.go
// wiring today.
type Dependencies struct {
	UOW ports.UnitOfWork
	IDs idsource.Source
}

// RegisterRoutes registers this package's own two route fragments —
// operationId startWorkflowRun (POST /work-items/{workItemId}/runs) and
// operationId cancelRun (POST /runs/{runId}/cancel), both names locked in by
// docs/design/11-v6-00-ux-artifact.md's own action inventory (Screen 5 row
// 4/5, Screen 7 row 3/4) — into routes. This function only ever adds its OWN
// two descriptors; contract point 8 ("Parallel work không sửa registry
// chung... Chỉ V6-12 compose HTTP router/OpenAPI") reserves aggregation
// across every endpoint task's fragments for V6-12 alone.
func RegisterRoutes(routes *httpapi.RouteRegistry, deps Dependencies) {
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/work-items/{workItemId}/runs", OperationID: "startWorkflowRun",
		ScopeKind: httpapi.ScopeProject, RequestSchema: StartRunRequest{}, ResponseSchema: StartRunResponse{},
		Handler: StartWorkflowRunHandler(deps),
	})
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/runs/{runId}/cancel", OperationID: "cancelRun",
		ScopeKind: httpapi.ScopeProject, RequestSchema: CancelRunRequest{}, ResponseSchema: CancelRunResponse{},
		Handler: CancelRunHandler(deps),
	})
}
