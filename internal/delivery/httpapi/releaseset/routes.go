// Package releaseset is V6-10F's own HTTP slice
// (docs/design/08-v6-api-projections.md V6-10F: "expose V6-10E authorities
// and exact operation status"). It owns a dedicated subpackage under
// internal/delivery/httpapi — the same "mỗi endpoint task sở hữu
// subpackage, descriptor/schema fragment và test riêng" discipline
// internal/delivery/httpapi/workitem's own doc comment already establishes
// — rather than adding flat files directly into internal/delivery/httpapi.
//
// Every handler in this package follows the identical flow V6-02's own
// commandenvelope.go/receiptreplay.go doc comments prescribe, and
// workitem's own handlers already follow byte-for-byte: authenticate/
// authorize (BindPrincipal, already wired by the composition root before
// this package's handlers ever run) → reload the route's own authoritative
// target and derive/confirm its real project scope (Contract chung §3:
// "không tin ID shape, payload hoặc projection") → build the command
// envelope (Idempotency-Key required always; If-Match required only for
// the two ReleaseSet lifecycle transitions, sealReleaseSet/
// abandonReleaseSet, which mutate an EXISTING resource rather than creating
// a new one) → receipt lookup → replay/conflict → "nếu absent mới kiểm
// current version" → dispatch the real, already-existing application
// command (internal/app/work/release_set.go's CreateReleaseSet/
// SealReleaseSet/AbandonReleaseSet, release_set_queries.go's GetReleaseSet/
// ListReleaseSetsForFamily/GetReleaseSetLocalCommitStatus — the last one
// added BY this task, see that file's own doc comment —
// internal/app/releasesetcommit.RequestReleaseSetLocalCommit) → encode the
// result. This package never writes or records a command receipt itself,
// and never opens a WithSerializedWrite transaction of its own — enforced
// for real by internal/archtest's TestDeliveryHTTPAPINeverWritesOrRecordsAReceipt,
// which walks internal/delivery/httpapi recursively and therefore already
// covers this subpackage too.
//
// This package's own single most locked-down rule (V6-10F's own "Không
// làm": "handler must never call a real Git adapter or worker directly...
// no remote route of any kind") is proven mechanically, not just by doc
// comment, by internal/archtest's own
// TestDeliveryReleaseSetRoutesNeverReachGitOrWorker (this task's own
// architecture test, mirroring workspaceroutes.go's own
// TestDeliveryWorkspaceRoutesNeverReachWorkspaceIOOrExecutor idiom exactly)
// and by this package's own TestRegisterRoutes_ExcludesAnyRemoteGitVerb
// (routes_test.go), which enumerates every route this package ever
// registers and asserts none of them name a remote-Git operation
// (push/fetch/pr/merge/rebase/force-push) — this whole task family is
// explicitly LOCAL-only.
//
// Route inventory (all seven project-scoped — ReleaseSet/
// ReleaseSetLocalCommit are not in ADR-025's closed installation-scope
// table):
//
//	POST /projects/{projectId}/task-families/{familyId}/release-sets                              createReleaseSet
//	GET  /projects/{projectId}/task-families/{familyId}/release-sets                              listReleaseSetsForFamily
//	GET  /projects/{projectId}/release-sets/{releaseSetId}                                        getReleaseSet
//	POST /projects/{projectId}/release-sets/{releaseSetId}/seal                                   sealReleaseSet
//	POST /projects/{projectId}/release-sets/{releaseSetId}/abandon                                abandonReleaseSet
//	POST /projects/{projectId}/release-sets/{releaseSetId}/local-commits                          requestReleaseSetLocalCommit
//	GET  /projects/{projectId}/release-sets/{releaseSetId}/local-commits/{localCommitId}          getReleaseSetLocalCommitStatus
package releaseset

import (
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/releasesetcommit"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// RegisterRoutes registers every route this package owns onto reg — a
// composition root (cmd/aw/serve.go) calls this once, alongside every
// sibling endpoint task's own RegisterRoutes, to compose the final root
// router; this package itself never touches a shared registry beyond this
// one call (Contract chung §1.8: "Chỉ V6-12 compose HTTP router").
func RegisterRoutes(reg *httpapi.RouteRegistry, deps Dependencies) {
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/projects/{projectId}/task-families/{familyId}/release-sets", OperationID: "createReleaseSet",
		ScopeKind: httpapi.ScopeProject, RequestSchema: createReleaseSetBody{}, ResponseSchema: workapp.ReleaseSetResult{},
		Handler: handleCreateReleaseSet(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{projectId}/task-families/{familyId}/release-sets", OperationID: "listReleaseSetsForFamily",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: releaseSetListResponse{},
		Handler: handleListReleaseSetsForFamily(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{projectId}/release-sets/{releaseSetId}", OperationID: "getReleaseSet",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: workapp.ReleaseSetDetail{},
		Handler: handleGetReleaseSet(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/projects/{projectId}/release-sets/{releaseSetId}/seal", OperationID: "sealReleaseSet",
		ScopeKind: httpapi.ScopeProject, RequestSchema: emptyBody{}, ResponseSchema: workapp.ReleaseSetResult{},
		Handler: handleSealReleaseSet(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/projects/{projectId}/release-sets/{releaseSetId}/abandon", OperationID: "abandonReleaseSet",
		ScopeKind: httpapi.ScopeProject, RequestSchema: emptyBody{}, ResponseSchema: workapp.ReleaseSetResult{},
		Handler: handleAbandonReleaseSet(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/projects/{projectId}/release-sets/{releaseSetId}/local-commits", OperationID: "requestReleaseSetLocalCommit",
		ScopeKind: httpapi.ScopeProject, RequestSchema: requestReleaseSetLocalCommitBody{}, ResponseSchema: releasesetcommit.RequestReleaseSetLocalCommitResult{},
		Handler: handleRequestReleaseSetLocalCommit(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{projectId}/release-sets/{releaseSetId}/local-commits/{localCommitId}", OperationID: "getReleaseSetLocalCommitStatus",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: workapp.ReleaseSetLocalCommitStatus{},
		Handler: handleGetReleaseSetLocalCommitStatus(deps),
	})
}
