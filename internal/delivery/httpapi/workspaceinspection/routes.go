// Package workspaceinspection is V6-10D's own thin HTTP surface over
// V6-10C's already-complete, already-safe application query layer
// (internal/app/workspaceinspection.Queries) — the exact package name
// mirrors that app-layer package on purpose (docs/design/08-v6-api-projections.md
// V6-10D: "map V6-10C read-only queries to HTTP", nothing more), imported
// into cmd/aw/serve.go under an aliased name exactly the way
// internal/delivery/httpapi/evidence and .../definitions already are there
// (httpevidence, httpdefinitions).
//
// Every handler in this package does EXACTLY ONE thing: parse path/query
// parameters into the app layer's own GetSourceRequest/GetDiffRequest/
// GetRepositoryLogRequest shapes, call the one already-injected
// *workspaceinspection.Queries, and map the result (or error) onto real HTTP
// semantics. This package's own "Không làm" line (V6-10D) is enforced not
// merely by convention but by two real, checked architecture facts:
//
//  1. internal/archtest's own whole-package
//     TestDeliveryWorkspaceRoutesNeverReachWorkspaceIOOrExecutor already
//     forbids every file under internal/delivery/httpapi/** (this
//     subpackage included, since that test walks the tree recursively) from
//     importing "os", "os/exec" or any internal/adapters/... package.
//  2. This package's own internal/archtest/workspace_inspection_http_test.go
//     (TestWorkspaceInspectionHTTPNeverImportsFilesystemOrProcess) restates
//     that identical guarantee scoped to exactly this one subpackage, so the
//     invariant is self-evident here even if the whole-package test above
//     were ever narrowed.
//
// Structural consequence: this package can name only two of
// internal/app/workspaceinspection's own typed sentinels (ErrScopeMismatch,
// ErrWorkspaceNotReady) plus the shared ports.ErrPersistenceNotFound —
// anything else Queries.GetSource/GetDiff/GetRepositoryLog returns
// (internal/adapters/gitworktree's own ErrInvalidSpec/ErrPathNotFound/
// ErrUnsupportedEntry/ErrWorkspaceReleased/...) can never be named here by
// its own concrete sentinel, since doing so would require importing the
// forbidden adapter package. errors.go's own writeQueryError therefore maps
// every OTHER error opaquely to one safe, sensible bucket rather than
// guessing at adapter internals it structurally cannot see — see that
// file's own doc comment for the full reasoning.
//
// Route inventory (all three project-scoped, hung off the identical
// RepositoryWorkspace resource V6-10B's own
// getRepositoryWorkspaceState/requestWorkspaceReconciliation routes already
// use — internal/delivery/httpapi/workspaceroutes.go — so a caller who
// already has a RepositoryWorkspaceID from that route reuses the exact same
// path prefix here):
//
//	GET /projects/{projectId}/repository-workspaces/{repositoryWorkspaceId}/source            getWorkspaceSource
//	GET /projects/{projectId}/repository-workspaces/{repositoryWorkspaceId}/diff               getWorkspaceDiff
//	GET /projects/{projectId}/repository-workspaces/{repositoryWorkspaceId}/repository-log     getWorkspaceRepositoryLog
//
// repositoryId/workspaceSetId are deliberately REQUIRED query parameters on
// every one of these three routes, never inferred from the path or from a
// second persistence read this package would have to perform itself: V6-10C's
// own workspaceinspection.WorkspaceScope requires all four identifiers
// (ProjectID, RepositoryID, WorkspaceSetID, RepositoryWorkspaceID) to match
// the real, persisted ownership chain, rejecting ANY mismatch as
// ErrScopeMismatch — a deliberately stricter, defense-in-depth check than
// V6-10B's own workspacestate queries (which only ever check
// ProjectID+RepositoryWorkspaceID) precisely because these three routes
// stream real repository CONTENT rather than mere state. A caller must
// already know a RepositoryWorkspace's own full scope chain (e.g. from a
// prior getWorkspaceSetState/getRepositoryWorkspaceState response, which
// already names WorkspaceSetID/RepositoryID) to read anything through this
// package — never merely guess a RepositoryWorkspaceID and have the other
// three identifiers inferred for it.
//
// Revision identity (revision/generation, base/baseGeneration,
// result/resultGeneration, anchor/anchorGeneration query parameters) is
// likewise always caller-supplied raw values, never resolved by this
// package from a symbolic ref: workspace.Revision.RepositoryID is always
// set from the same repositoryId query parameter as WorkspaceScope.RepositoryID
// (a RepositoryWorkspace's own revisions can only ever belong to the one
// repository it is bound to — never a second, differently-named revision
// repository), so no route here accepts a redundant, separately-named
// "revision's own repository id" parameter. The real revision authorization
// (proving the caller-supplied VCSObjectID+WorkspaceGeneration is actually
// one of the workspace's own two known-good revisions — BaseRevision or
// CurrentRevision) happens entirely inside
// internal/adapters/gitworktree.Provider.authorizeRevision, one layer below
// this package and one layer below internal/app/workspaceinspection too —
// this package never resolves, validates the shape of beyond basic
// non-empty/parseable, or otherwise reasons about a revision string itself,
// per V6-10D's own "Không làm: handler no ... revision resolution" line.
package workspaceinspection

import (
	"net/http"

	appinspection "github.com/taQuangLing/agent-workflow/internal/app/workspaceinspection"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// repositoryWorkspacePrefix is the shared path prefix every route in this
// package hangs off of — identical to
// internal/delivery/httpapi/workspaceroutes.go's own
// "/projects/{projectId}/repository-workspaces/{repositoryWorkspaceId}"
// route, so a caller reuses one RepositoryWorkspaceID across both packages.
const repositoryWorkspacePrefix = "/projects/{projectId}/repository-workspaces/{repositoryWorkspaceId}"

// Dependencies is everything this package's own handlers need: the one real
// *appinspection.Queries a composition root builds once, from a real
// ports.UnitOfWork and a real ports.WorkspaceInspectionReader
// (internal/adapters/gitworktree.Provider in production) — see
// cmd/aw/serve.go's own V6-10D wiring comment for exactly how. This package
// never constructs its own Queries and never receives the raw
// UnitOfWork/WorkspaceInspectionReader pair directly, so it structurally
// cannot dispatch anything other than Queries' own three named methods.
type Dependencies struct {
	Queries *appinspection.Queries
}

// RegisterRoutes registers this task's own 3 read-only routes into reg — a
// composition root calls this once, alongside every sibling endpoint task's
// own RegisterRoutes call (cmd/aw/serve.go's own additive
// "routes.Register(...)" convention).
func RegisterRoutes(reg *httpapi.RouteRegistry, deps Dependencies) {
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: repositoryWorkspacePrefix + "/source", OperationID: "getWorkspaceSource",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: struct{}{},
		Handler: handleGetSource(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: repositoryWorkspacePrefix + "/diff", OperationID: "getWorkspaceDiff",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: diffContentResponse{},
		Handler: handleGetDiff(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: repositoryWorkspacePrefix + "/repository-log", OperationID: "getWorkspaceRepositoryLog",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: repositoryLogPageResponse{},
		Handler: handleGetRepositoryLog(deps),
	})
}
