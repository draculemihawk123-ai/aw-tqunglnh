// Package evidence is V6-07B's own HTTP surface for Evidence, Artifact and
// standalone ContextSnapshot query endpoints
// (docs/design/08-v6-api-projections.md V6-07B: "list/inspect evidence/
// context/artifact và stream authorized content an toàn"). It owns a
// dedicated subpackage under internal/delivery/httpapi — the same "mỗi
// endpoint task sở hữu subpackage, descriptor/schema fragment và test
// riêng" discipline internal/delivery/httpapi/workitem (V6-04) and
// internal/delivery/httpapi/message (V6-07) already establish.
//
// Every handler here is READ-ONLY (uow.WithReadOnly via
// internal/app/runtime's own queries.go, never WithSerializedWrite) and
// never builds a ports.Command/CommandEnvelope — this task's own "Phạm vi"
// line is entirely list/inspect/stream, with no mutation anywhere (mirrors
// how ListMessages/GetSafeSettings-style queries elsewhere in this codebase
// use WithReadOnly, per this task's own brief). Every query reloads its own
// owning WorkItem (and, for Evidence-scoped routes, the Evidence row
// itself) FIRST, unconditionally, and cross-checks it against BOTH the
// path's projectId and workItemId — never trusting either from the path
// alone — before ever touching Artifact/ContextSnapshot storage; a
// resource that either does not exist or belongs to a different scope than
// the path claims produces the IDENTICAL leakage-normalized
// httpapi.WriteResourceHidden response (V6-02A's own policy), never a
// distinguishable 403.
//
// Artifact content (getArtifactContent) is the one route that streams raw
// bytes rather than a JSON DTO: it resolves a real, already-durable
// Artifact row's own stored columns into a ports.ArtifactRef ENTIRELY
// server-side (internal/app/runtime.ResolveEvidenceArtifactContent) — the
// caller only ever supplies an opaque platform-minted Artifact ID, never a
// locator or filesystem path (this task's own "Không làm: KHÔNG expose
// locator" line, satisfied by construction: no field anywhere in this
// package's own wire DTOs is ever populated from artifact.Artifact.Locator).
// ports.ArtifactStore.Verify/Open are only ever called from THIS package
// (never from internal/app/runtime, which never touches ArtifactStore at
// all — see that package's own queries.go doc comment for why: real
// store I/O must happen outside any database transaction,
// docs/architecture/04-go-core-spec.md §11.1), so a tampered artifact
// (bytes no longer matching their own recorded hash) is caught here and
// surfaced as a typed error before a single byte is ever written to the
// response, never served silently.
//
// Route inventory (all five project-scoped, WorkItem-scoped; operationIds
// locked in by docs/design/11-v6-00-ux-artifact.md's own Screen 11/12
// action inventory):
//
//	GET /projects/{projectId}/work-items/{workItemId}/evidence                                              listEvidence
//	GET /projects/{projectId}/work-items/{workItemId}/evidence/{evidenceId}                                  getEvidence
//	GET /projects/{projectId}/work-items/{workItemId}/evidence/{evidenceId}/artifacts                        listArtifacts
//	GET /projects/{projectId}/work-items/{workItemId}/evidence/{evidenceId}/artifacts/{artifactId}/content   getArtifactContent
//	GET /projects/{projectId}/work-items/{workItemId}/context-snapshots/{snapshotId}                         getContextSnapshot
//
// listArtifacts/getArtifactContent are deliberately nested under one
// specific Evidence row rather than under the WorkItem directly — see
// internal/app/runtime/queries.go's own top-of-file doc comment for the
// full "artifact inventory" investigation this task's own instructions
// required: an Artifact row carries no WorkItem/Run column of its own, so
// an Evidence row's own ArtifactReferences is the only real domain-modeled
// ownership link a WorkItem-scoped surface has to a specific set of
// Artifact IDs today.
package evidence

import (
	"net/http"

	runtimeapp "github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// RegisterRoutes registers every route this package owns onto reg — a
// composition root (cmd/aw/serve.go) calls this once, alongside every
// sibling endpoint task's own RegisterRoutes, to compose the final root
// router.
func RegisterRoutes(reg *httpapi.RouteRegistry, deps Dependencies) {
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{projectId}/work-items/{workItemId}/evidence", OperationID: "listEvidence",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: listEvidenceResponse{},
		Handler: handleListEvidence(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{projectId}/work-items/{workItemId}/evidence/{evidenceId}", OperationID: "getEvidence",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: runtimeapp.EvidenceDetail{},
		Handler: handleGetEvidence(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{projectId}/work-items/{workItemId}/evidence/{evidenceId}/artifacts", OperationID: "listArtifacts",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: listArtifactsResponse{},
		Handler: handleListArtifacts(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{projectId}/work-items/{workItemId}/evidence/{evidenceId}/artifacts/{artifactId}/content", OperationID: "getArtifactContent",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: struct{}{},
		Handler: handleGetArtifactContent(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{projectId}/work-items/{workItemId}/context-snapshots/{snapshotId}", OperationID: "getContextSnapshot",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: runtimeapp.ContextSnapshotDetail{},
		Handler: handleGetContextSnapshot(deps),
	})
}
