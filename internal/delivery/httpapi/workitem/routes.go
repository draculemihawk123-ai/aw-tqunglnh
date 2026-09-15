// Package workitem is V6-04's own HTTP slice
// (docs/design/08-v6-api-projections.md V6-04: "expose root/child WorkItem,
// family/readiness và toàn bộ scope-expansion lifecycle"). It owns a
// dedicated subpackage under internal/delivery/httpapi — Contract chung
// §1.8's own "Mỗi endpoint/CLI task sở hữu subpackage, descriptor/schema
// fragment và test riêng" — rather than adding flat files directly into
// internal/delivery/httpapi the way the foundation tasks (V6-01/V6-01A/
// V6-02/V6-02A) did: three sibling endpoint tasks (V6-03A, V6-06, V6-10B)
// run in parallel worktrees against the same shared package right now, and
// a dedicated subpackage means none of the four ever collides on a filename
// or a package-level identifier while composing its own routes.
//
// Every handler in this package follows the identical flow V6-02's own
// commandenvelope.go/receiptreplay.go doc comments already prescribe:
// authenticate/authorize (BindPrincipal, already wired by the composition
// root before this package's handlers ever run) → reload the route's own
// authoritative target and derive/confirm its real project scope (Contract
// chung §3: "không tin ID shape, payload hoặc projection") → build the
// command envelope (Idempotency-Key required always; If-Match required only
// for the three ScopeExpansionRequest decision routes, which mutate an
// EXISTING resource rather than creating a new one) → receipt lookup →
// replay/conflict → "nếu absent mới kiểm current version" → dispatch the
// real, already-existing application command
// (internal/app/work/commands.go's CreateRootWorkItem/CreateChildWorkItem,
// scope_expansion.go's RequestScopeExpansion/ApproveScopeExpansion/
// RejectScopeExpansion/WithdrawScopeExpansion) → encode the result. This
// package never writes or records a command receipt itself, and never opens
// a WithSerializedWrite transaction of its own — enforced for real by
// internal/archtest's TestDeliveryHTTPAPINeverWritesOrRecordsAReceipt, which
// walks internal/delivery/httpapi recursively and therefore already covers
// this subpackage too.
//
// Route inventory (all twelve project-scoped — WorkItem/TaskFamily/
// ScopeExpansionRequest are not in ADR-025's closed installation-scope
// table):
//
//	POST /projects/{projectId}/work-items                                      createRootWorkItem
//	GET  /projects/{projectId}/work-items                                      listWorkItems
//	GET  /projects/{projectId}/work-items/{workItemId}                         getWorkItem
//	POST /projects/{projectId}/work-items/{workItemId}/children                createChildWorkItem
//	GET  /projects/{projectId}/work-items/{workItemId}/children                listChildWorkItems
//	GET  /projects/{projectId}/work-items/{workItemId}/readiness               getWorkItemReadiness
//	GET  /projects/{projectId}/task-families/{familyId}                        getTaskFamily
//	POST /projects/{projectId}/task-families/{familyId}/scope-expansions       requestScopeExpansion
//	GET  /projects/{projectId}/scope-expansions/{requestId}                    getScopeExpansionRequest
//	POST /projects/{projectId}/scope-expansions/{requestId}/approve            approveScopeExpansion
//	POST /projects/{projectId}/scope-expansions/{requestId}/reject             rejectScopeExpansion
//	POST /projects/{projectId}/scope-expansions/{requestId}/withdraw           withdrawScopeExpansion
//	POST /work-items/{workItemId}/mark-ready                                   markWorkItemReady
//
// markWorkItemReady (V6-04A, docs/design/08-v6-api-projections.md V6-04A) is
// this package's own thirteenth route, added after V6-04 itself merged — see
// mark_ready_command.go's own doc comment for the full contract, including
// why its path deliberately has NO {projectId} segment (unlike the twelve
// routes above): the design doc's own route fragment is verbatim
// `POST /work-items/{id}/mark-ready` (docs/design/01-system-design.md line
// 567, repeated in 08-v6-api-projections.md V6-04A's own "Phạm vi" line),
// and V6-06's own already-merged run package (internal/delivery/httpapi/run)
// established the concrete precedent for this exact shape first — a
// WorkItem-rooted action route with no project prefix, its own ProjectID
// derived by reloading the WorkItem directly rather than trusted from a path
// segment (contract point 3: "không tin ID shape, payload hoặc projection").
//
// This task's own "Không làm" line ("không generic status/family/workspace
// setter; không dùng projected detail để authorize") is satisfied by
// construction: no route here accepts a free "status"/"state" field of any
// kind (every mutating body DTO in this package names only the fields its
// own wrapped command actually needs — see workitem_commands.go/
// scope_expansion_commands.go), and every authorization decision reloads
// the real row via internal/app/work/queries.go, never a projection (V6-10
// does not exist yet, and nothing in this package imports anything
// resembling one) — internal/archtest's own
// TestWorkItemPackageNeverImportsProjectionOrDefinesGenericSetter (V6-04's
// own architecture test, mirroring V6-02's identical AST-scan idiom) proves
// both halves mechanically, not just by doc-comment assertion.
package workitem

import (
	"net/http"

	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// RegisterRoutes registers every route this package owns onto reg — a
// composition root (V6-12, later) calls this once, alongside every sibling
// endpoint task's own RegisterRoutes, to compose the final root router; this
// package itself never touches a shared registry beyond this one call
// (Contract chung §1.8: "Chỉ V6-12 compose HTTP router").
func RegisterRoutes(reg *httpapi.RouteRegistry, deps Dependencies) {
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/projects/{projectId}/work-items", OperationID: "createRootWorkItem",
		ScopeKind: httpapi.ScopeProject, RequestSchema: createRootWorkItemBody{}, ResponseSchema: workapp.CreateRootWorkItemResult{},
		Handler: handleCreateRootWorkItem(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{projectId}/work-items", OperationID: "listWorkItems",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: workItemListResponse{},
		Handler: handleListWorkItems(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{projectId}/work-items/{workItemId}", OperationID: "getWorkItem",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: workapp.WorkItemDetail{},
		Handler: handleGetWorkItem(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/projects/{projectId}/work-items/{workItemId}/children", OperationID: "createChildWorkItem",
		ScopeKind: httpapi.ScopeProject, RequestSchema: createChildWorkItemBody{}, ResponseSchema: workapp.CreateChildWorkItemResult{},
		Handler: handleCreateChildWorkItem(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{projectId}/work-items/{workItemId}/children", OperationID: "listChildWorkItems",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: workItemListResponse{},
		Handler: handleListChildWorkItems(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{projectId}/work-items/{workItemId}/readiness", OperationID: "getWorkItemReadiness",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: workapp.WorkItemReadiness{},
		Handler: handleGetWorkItemReadiness(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{projectId}/task-families/{familyId}", OperationID: "getTaskFamily",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: workapp.TaskFamilyDetail{},
		Handler: handleGetTaskFamily(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/projects/{projectId}/task-families/{familyId}/scope-expansions", OperationID: "requestScopeExpansion",
		ScopeKind: httpapi.ScopeProject, RequestSchema: requestScopeExpansionBody{}, ResponseSchema: workapp.RequestScopeExpansionResult{},
		Handler: handleRequestScopeExpansion(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{projectId}/scope-expansions/{requestId}", OperationID: "getScopeExpansionRequest",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: workapp.ScopeExpansionRequestDetail{},
		Handler: handleGetScopeExpansionRequest(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/projects/{projectId}/scope-expansions/{requestId}/approve", OperationID: "approveScopeExpansion",
		ScopeKind: httpapi.ScopeProject, RequestSchema: emptyBody{}, ResponseSchema: workapp.ApproveScopeExpansionResult{},
		Handler: handleApproveScopeExpansion(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/projects/{projectId}/scope-expansions/{requestId}/reject", OperationID: "rejectScopeExpansion",
		ScopeKind: httpapi.ScopeProject, RequestSchema: rejectScopeExpansionBody{}, ResponseSchema: workapp.RejectScopeExpansionResult{},
		Handler: handleRejectScopeExpansion(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/projects/{projectId}/scope-expansions/{requestId}/withdraw", OperationID: "withdrawScopeExpansion",
		ScopeKind: httpapi.ScopeProject, RequestSchema: emptyBody{}, ResponseSchema: workapp.WithdrawScopeExpansionResult{},
		Handler: handleWithdrawScopeExpansion(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/work-items/{workItemId}/mark-ready", OperationID: "markWorkItemReady",
		ScopeKind: httpapi.ScopeProject, RequestSchema: emptyBody{}, ResponseSchema: workapp.MarkWorkItemReadyResult{},
		Handler: handleMarkWorkItemReady(deps),
	})
}
