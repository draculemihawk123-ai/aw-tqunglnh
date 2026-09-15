// Package definitions is V6-05's own HTTP slice
// (docs/design/08-v6-api-projections.md V6-05: "create/validate/publish/
// list/detail/version/diff cho global và project definitions"). It owns a
// dedicated subpackage under internal/delivery/httpapi (Contract chung
// §1.8: "Mỗi endpoint/CLI task sở hữu subpackage, descriptor/schema
// fragment và test riêng"), mirroring internal/delivery/httpapi/workitem
// and .../catalog's own established shape.
//
// Every handler in this package follows the identical flow those two
// sibling packages' own doc comments already prescribe: authenticate/
// authorize (BindPrincipal, already wired by the composition root) →
// derive THIS route's own scope from its path alone (global prefix, or a
// real {projectId} — never the request body) → reload the route's own
// authoritative target (authoritative.go's loadDefinitionInScope/
// loadVersionInScope) and confirm it actually belongs to that derived
// scope, folding "does not exist" and "exists in a different scope" into
// the identical leakage-normalized 404 (V6-02A's own policy) → for a
// mutation, build the command envelope (Idempotency-Key required; no
// route in this package needs If-Match — see publish.go's own doc comment
// for why publish is create-shaped, never update-shaped) → receipt lookup
// → replay/conflict → only once absent, dispatch the real, already-existing
// application command (internal/app/definitions.CreateDefinition/
// ValidateDraft/PublishDefinitionVersion) → encode the result. This
// package never writes or records a command receipt itself, and never
// opens a write transaction of its own — enforced by
// internal/archtest.TestDeliveryHTTPAPINeverWritesOrRecordsAReceipt, which
// already walks internal/delivery/httpapi recursively.
//
// Route inventory (7 operations × 2 scopes = 14 routes). Six of the seven
// carry a {kind} path segment (create/validate/publish/detail/list-
// versions are all routed by Kind, the same convention
// internal/app/definitions.CreateDefinition/PublishDefinitionVersion/
// ListVersions themselves already use to pick between the shared
// definitions table and Workflow's own dedicated tables); the remaining
// two (get one version, diff) deliberately do NOT — a VersionID is already
// globally unique and carries its own Kind (definition.VersionFields.Kind()),
// so appdefinitions.LoadAnyVersion resolves it without the caller having to
// already know which of the nine kinds it belongs to, the identical
// fallback cmd/aw/definition.go's own 'show'/'diff' subcommands already use
// for the same reason (that file's own loadAnyVersion doc comment).
//
//	POST /definitions/{kind}                                          createDefinition
//	POST /definitions/{kind}/{id}/validate                            validateDefinitionDraft
//	POST /definitions/{kind}/{id}/publish                             publishDefinitionVersion
//	GET  /definitions/{kind}/{id}                                     getDefinition
//	GET  /definitions/{kind}/{id}/versions                            listDefinitionVersions
//	GET  /definitions/versions/{versionId}                            getDefinitionVersion
//	GET  /definitions/versions/diff?a=&b=                             diffDefinitionVersions
//
//	POST /projects/{projectId}/definitions/{kind}                     createProjectDefinition
//	POST /projects/{projectId}/definitions/{kind}/{id}/validate       validateProjectDefinitionDraft
//	POST /projects/{projectId}/definitions/{kind}/{id}/publish        publishProjectDefinitionVersion
//	GET  /projects/{projectId}/definitions/{kind}/{id}                getProjectDefinition
//	GET  /projects/{projectId}/definitions/{kind}/{id}/versions       listProjectDefinitionVersions
//	GET  /projects/{projectId}/definitions/versions/{versionId}       getProjectDefinitionVersion
//	GET  /projects/{projectId}/definitions/versions/diff?a=&b=        diffProjectDefinitionVersions
package definitions

import (
	"net/http"

	appdefinitions "github.com/taQuangLing/agent-workflow/internal/app/definitions"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// RegisterRoutes registers every route this package owns onto reg — a
// composition root (cmd/aw/serve.go) calls this once, alongside every
// sibling endpoint task's own RegisterRoutes.
func RegisterRoutes(reg *httpapi.RouteRegistry, deps Dependencies) {
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/definitions/{kind}", OperationID: "createDefinition",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: createDefinitionBody{}, ResponseSchema: appdefinitions.CreateDefinitionResult{},
		Handler: handleCreateDefinition(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/definitions/{kind}/{id}/validate", OperationID: "validateDefinitionDraft",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: authorDocumentBody{}, ResponseSchema: versionFieldsView{},
		Handler: handleValidateDefinitionDraft(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/definitions/{kind}/{id}/publish", OperationID: "publishDefinitionVersion",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: authorDocumentBody{}, ResponseSchema: versionFieldsView{},
		Handler: handlePublishDefinitionVersion(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/definitions/{kind}/{id}", OperationID: "getDefinition",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: struct{}{}, ResponseSchema: definitionView{},
		Handler: handleGetDefinition(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/definitions/{kind}/{id}/versions", OperationID: "listDefinitionVersions",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: struct{}{}, ResponseSchema: versionListResponse{},
		Handler: handleListDefinitionVersions(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/definitions/versions/{versionId}", OperationID: "getDefinitionVersion",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: struct{}{}, ResponseSchema: versionFieldsView{},
		Handler: handleGetDefinitionVersion(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/definitions/versions/diff", OperationID: "diffDefinitionVersions",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: struct{}{}, ResponseSchema: versionDiffView{},
		Handler: handleDiffDefinitionVersions(deps),
	})

	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/projects/{projectId}/definitions/{kind}", OperationID: "createProjectDefinition",
		ScopeKind: httpapi.ScopeProject, RequestSchema: createDefinitionBody{}, ResponseSchema: appdefinitions.CreateDefinitionResult{},
		Handler: handleCreateProjectDefinition(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/projects/{projectId}/definitions/{kind}/{id}/validate", OperationID: "validateProjectDefinitionDraft",
		ScopeKind: httpapi.ScopeProject, RequestSchema: authorDocumentBody{}, ResponseSchema: versionFieldsView{},
		Handler: handleValidateProjectDefinitionDraft(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/projects/{projectId}/definitions/{kind}/{id}/publish", OperationID: "publishProjectDefinitionVersion",
		ScopeKind: httpapi.ScopeProject, RequestSchema: authorDocumentBody{}, ResponseSchema: versionFieldsView{},
		Handler: handlePublishProjectDefinitionVersion(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{projectId}/definitions/{kind}/{id}", OperationID: "getProjectDefinition",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: definitionView{},
		Handler: handleGetProjectDefinition(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{projectId}/definitions/{kind}/{id}/versions", OperationID: "listProjectDefinitionVersions",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: versionListResponse{},
		Handler: handleListProjectDefinitionVersions(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{projectId}/definitions/versions/{versionId}", OperationID: "getProjectDefinitionVersion",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: versionFieldsView{},
		Handler: handleGetProjectDefinitionVersion(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{projectId}/definitions/versions/diff", OperationID: "diffProjectDefinitionVersions",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: versionDiffView{},
		Handler: handleDiffProjectDefinitionVersions(deps),
	})
}
