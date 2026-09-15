// Package rundetail is V6-06B's own HTTP surface for authoritative Run
// detail plus bounded graph/timeline queries
// (docs/design/08-v6-api-projections.md V6-06B: "cung cấp authoritative Run
// detail và bounded graph/timeline cho V7"). It owns its own subpackage
// under internal/delivery/httpapi, mirroring internal/delivery/httpapi/
// evidence (V6-07B) and internal/delivery/httpapi/message (V6-07)'s own
// "mỗi endpoint task sở hữu subpackage, descriptor/schema fragment và test
// riêng" discipline.
//
// Every handler here is READ-ONLY: each one calls exactly one
// internal/app/runtime query function (GetRunDetail/GetRunGraph/
// GetRunTimeline, run_detail_queries.go), never builds a ports.Command or
// touches uow.WithSerializedWrite — this task's own "Không làm: không
// diagnostics/recovery mutation" line (that is V6-06C's own separate job,
// internal/delivery/httpapi/recovery) is enforced by construction: no
// import of internal/app/runtime's own command constructors (CancelRun,
// RetryBlockedActivation, ...) appears anywhere in this package.
//
// Route inventory (all Run-scoped by RunID alone — no {projectId} path
// segment, mirroring internal/delivery/httpapi/run's own POST
// /runs/{runId}/cancel: this package's own requireRun/loadRun helpers
// reload the Run FIRST via runtimeapp.GetWorkflowRun-equivalent and derive
// its real ProjectID from that row itself, never from an untrusted path
// claim — see cancel.go's own requireRunExists doc comment for the
// identical reasoning applied here):
//
//	GET /runs/{id}           getRunDetail
//	GET /runs/{id}/graph     getRunGraph
//	GET /runs/{id}/timeline  getRunTimeline
//
// Pagination and freshness (graph.go/timeline.go): internal/app/runtime's
// own GetRunGraph/GetRunTimeline each return their FULL, already-ordered
// result for a Run (see run_detail_queries.go's own package doc comment for
// why) — THIS package is the only place that ever touches
// httpapi.CursorCodec/httpapi.Bind/httpapi.Freshness, mirroring
// internal/delivery/httpapi/message's own handleListMessages exactly: an
// in-memory slice of an already-bounded, already-sorted result, never a
// second SQL query shape.
//
// Freshness (every paginated response's own httpapi.Freshness): this
// endpoint reads authoritative Run/NodeRun/ExecutionAttempt rows directly,
// never a V6-08 lagging projection, so Status is always LIVE and
// Generation is always 0 (no real projection-generation concept applies
// here — mirrors handleListMessages' own identical Generation:0 choice,
// list.go). AsOfJournalPosition reuses that same wire field to carry this
// Run's own highest NodeRun.ActivationSequence — a Run-scoped, strictly
// increasing counter (completion_policy.go's own maxActivationSequence),
// never a cross-run global journal_position (no ports.Tx accessor exposes
// one to internal/app/runtime by design — see that package's own
// run_detail_queries.go doc comment). A client comparing
// AsOfJournalPosition across requests still gets the intended "has this
// advanced since I last asked" signal (V6-02A's own Freshness doc comment)
// — it just answers that question at Run scope instead of installation
// scope, which is exactly this route's own scope.
package rundetail

import (
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	runtimeapp "github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// Dependencies is everything this package's own handlers need — mirrors
// internal/delivery/httpapi/message.Dependencies' own shape: a real
// ports.UnitOfWork, the SAME process-lifetime redact.Matcher every other
// redacting route in this composition root reuses (never a second,
// differently-scoped one — see that package's own Matcher doc comment), and
// this package's own opaque pagination cursor codec (Cursor), a fresh
// per-process secret the composition root mints exactly like httpmessage's
// own (never a fixed compiled-in value, never shared with another
// package's own CursorCodec instance — cursor.go's own CursorState is only
// ever Bind-checked against the SAME route/package that minted it).
type Dependencies struct {
	UnitOfWork ports.UnitOfWork
	Matcher    redact.Matcher
	Cursor     *httpapi.CursorCodec
}

// RegisterRoutes registers this package's own three route fragments onto
// reg — a composition root (cmd/aw/serve.go) calls this once, alongside
// every sibling endpoint task's own RegisterRoutes, to compose the final
// root router (contract point 8: "Parallel work không sửa registry
// chung... Chỉ V6-12 compose HTTP router/OpenAPI" — this function only ever
// adds its OWN three descriptors).
func RegisterRoutes(reg *httpapi.RouteRegistry, deps Dependencies) {
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/runs/{id}", OperationID: "getRunDetail",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: runtimeapp.RunDetail{},
		Handler: handleGetRunDetail(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/runs/{id}/graph", OperationID: "getRunGraph",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: runGraphResponse{},
		Handler: handleGetRunGraph(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/runs/{id}/timeline", OperationID: "getRunTimeline",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: runTimelineResponse{},
		Handler: handleGetRunTimeline(deps),
	})
}
