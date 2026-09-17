// Package kanban is V6-10's own HTTP surface for the projected Kanban card
// list and the projected (non-authoritative) WorkItem detail sibling data
// (docs/design/08-v6-api-projections.md V6-10: "bounded project views with
// filters, badges, blockers and freshness"). It owns its own subpackage
// under internal/delivery/httpapi — Contract chung §1 rule 8's own "mỗi
// endpoint task sở hữu subpackage, descriptor/schema fragment và test
// riêng" — deliberately separate from internal/delivery/httpapi/workitem
// (V6-04/V6-04A's own AUTHORITATIVE WorkItem/TaskFamily/readiness routes):
// that package's own routes.go doc comment already anticipates this one by
// name ("V6-10 does not exist yet, and nothing in this package imports
// anything resembling one").
//
// # What this package reads
//
// Every GET here is backed by internal/app/projection's own frozen
// WorkItemCardRow schema (V6-08, internal/app/projection/row.go) via
// ports.ProjectionRepository (tx.Projections()) — never a runtime table
// directly (this task's own "Hoàn thành khi: Kanban reads no runtime tables
// directly"). ListProjectionRows returns the FULL row set for one
// (ProjectID, ProjectionName, Generation); this package is the one that
// applies status/family filtering, keyset pagination and multi-repo badge
// aggregation over that already-fetched set in memory — see list.go's own
// doc comment for the exact keyset/watermark mechanics, mirroring
// internal/delivery/httpapi/message's own handleListMessages idiom (the
// first package in this codebase to actually assemble httpapi.CursorCodec/
// httpapi.Bind/httpapi.Freshness into a real paginated route) adapted to a
// string EntityKey (WorkItemID) instead of a numeric Sequence.
//
// # Authoritative decoration — the "projection không là authority" rule
//
// This task's own "Không làm" line is explicit: "projection không decide
// readiness/ValidAction or mutate state." Every card this package returns
// carries the row's own PROJECTED Status/ActiveRunStatus/BlockerCount as
// display-only badges (possibly stale — see Freshness). GET
// /work-items/{id}/detail additionally calls
// internal/app/work.ExplainWorkItemReadiness — the ONLY readiness/
// valid-action authority in this codebase (there is no separate
// "ValidAction function") — fresh, server-side, AFTER the projected card is
// already built, against the CURRENTLY loaded authoritative WorkItem, never
// against anything the projection claims. detail.go's own doc comment
// explains exactly why this ordering is what makes the "action race" Verify
// bullet (a projected row that has gone stale between read and response
// must still report a FRESH authoritative readiness, never a cached one)
// hold true by construction rather than by convention.
//
// # Route inventory (both project-scoped in practice, one without a path
// segment)
//
//	GET /projects/{projectId}/work-items/kanban   listWorkItemKanban
//	GET /work-items/{workItemId}/detail            getWorkItemProjectedDetail
//
// getWorkItemProjectedDetail deliberately has no {projectId} path segment —
// mirroring internal/delivery/httpapi/workitem's own markWorkItemReady and
// internal/delivery/httpapi/rundetail's own three routes: ProjectID is
// derived SOLELY by reloading the WorkItem's own real, stored row directly
// (detail.go's own loadWorkItemForDetail), never trusted from a client-
// supplied path segment (contract point 3: "không tin ID shape, payload
// hoặc projection"). listWorkItemKanban DOES carry {projectId}: it is a
// project-wide list, not a single-resource lookup, so the path segment IS
// the primary scope, the same way GET /projects/{projectId}/work-items
// (workitem package) already trusts its own {projectId} directly.
//
// # Phụ thuộc
//
// V6-00 (this codebase's own event-sourced foundation), V6-02A (cursor.go/
// freshness.go/page.go/action.go, reused verbatim, never reinvented here),
// V6-04A (internal/app/work.ExplainWorkItemReadiness, the authoritative
// decoration this package's own detail route calls), V6-08A (the live
// projection consumer that actually populates the rows/checkpoints this
// package reads — not itself a compile-time dependency of this package, but
// the real data source every test in this package seeds directly, mirroring
// how internal/delivery/httpapi/workitem's own tests seed a project via
// tx.Catalog().CreateProject rather than driving a second real command
// pipeline that is a different task's own job to build).
//
// # Nguồn
//
// AK-ARCH-022 (poison event -> DEGRADED/STALE, never silently skipped —
// Freshness.Status here is that exact vocabulary, reused verbatim from
// ports.ProjectionStatus), HE-08-M03, HE-08-M05.
package kanban

import (
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// Dependencies is everything this package's own handlers need — mirrors
// internal/delivery/httpapi/rundetail.Dependencies' own shape (a real
// ports.UnitOfWork plus this package's own opaque pagination cursor codec):
// no idsource.Source or clock.Clock is needed anywhere in this package,
// since it never mints an ID or builds a command envelope (every route here
// is GET-only, read-only, dispatches no command — internal/archtest's own
// TestDeliveryHTTPAPINeverWritesOrRecordsAReceipt, which already walks
// internal/delivery/httpapi recursively, proves this package never calls
// .WithSerializedWrite or .Record anywhere).
type Dependencies struct {
	// UnitOfWork is the one real ports.UnitOfWork every handler in this
	// package reads through (uow.WithReadOnly only, never
	// WithSerializedWrite) — this package never opens a second, competing
	// persistence path.
	UnitOfWork ports.UnitOfWork
	// Cursor signs/opens this package's own opaque listWorkItemKanban
	// pagination cursor (httpapi.CursorCodec, cursor.go) — a fresh
	// per-process secret a composition root mints exactly like
	// internal/delivery/httpapi/message's and .../rundetail's own (never a
	// fixed compiled-in value, never shared cross-package in a way that
	// would let a cursor minted for one route's own query shape verify
	// against another's — Bind's own QueryFingerprint check already
	// prevents cross-query replay even when the SAME codec instance is
	// reused, which cmd/aw/serve.go's own composition root does, exactly
	// like every other CursorCodec consumer in this process).
	Cursor *httpapi.CursorCodec
}

// RegisterRoutes registers both of this package's own routes onto reg — a
// composition root (cmd/aw/serve.go) calls this once, alongside every
// sibling endpoint task's own RegisterRoutes, to compose the final root
// router (Contract chung §1 rule 8: "Parallel work không sửa registry
// chung... Chỉ V6-12 compose HTTP router").
func RegisterRoutes(reg *httpapi.RouteRegistry, deps Dependencies) {
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{projectId}/work-items/kanban", OperationID: "listWorkItemKanban",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: kanbanListResponse{},
		Handler: handleListKanban(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/work-items/{workItemId}/detail", OperationID: "getWorkItemProjectedDetail",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: workItemDetailResponse{},
		Handler: handleGetWorkItemDetail(deps),
	})
}
