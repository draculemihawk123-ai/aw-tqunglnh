// Package diagnostics is V6-06C's own HTTP surface
// (docs/design/08-v6-api-projections.md V6-06C: "expose safe queue/job/
// lease/fence/provider/workspace diagnostics và recovery actions"). It owns
// a dedicated subpackage under internal/delivery/httpapi — the same "mỗi
// endpoint task sở hữu subpackage, descriptor/schema fragment và test
// riêng" discipline internal/delivery/httpapi/workitem (V6-04),
// internal/delivery/httpapi/evidence (V6-07B) and
// internal/delivery/httpapi/recovery (V6-06D, this task's own dependency)
// already establish.
//
// Exactly one route, exactly one query, exactly one operationId — this
// task's own "Phạm vi" line, taken literally: `GET
// /projects/{projectId}/runs/{runId}/diagnostics` (operationId
// getRunDiagnostics), a thin dispatch layer over
// internal/app/runtime.GetRunDiagnostics (diagnostics.go there) — never a
// second, competing read path, never a mutation of any kind (this
// package's own architecture-test proof,
// internal/archtest/diagnostics_http_test.go, mirrors
// TestRunControlHTTPNeverReachesSchedulerOrWorker/
// TestRecoveryHTTPNeverReachesSchedulerOrWorkerOrMutatesStateDirectly for
// the identical reason: a diagnostics surface that could itself trigger
// execution or mutate a blocker/Run/WorkItem would defeat the whole point
// of "safe").
//
// Project-prefixed, unlike V6-06/V6-06D's own flat mutation-command routes:
// this is a READ query, not a command — internal/app/runtime.GetRunDiagnostics
// takes a ports.CommandScope exactly like every other V6-07B query in that
// same package (queries.go's own requireProjectScope/verifyWorkItemInScope
// contract), so the caller's own path must actually NAME a project for that
// scope check to verify against. This mirrors
// internal/delivery/httpapi/evidence's own identical project-prefixed
// routes (GET /projects/{projectId}/work-items/{workItemId}/...) exactly,
// not V6-06's cancelRun/V6-06D's three recovery commands (none of which
// takes a ProjectID field on its own request struct at all — see
// recovery.go's own doc comment for why those stay flat). A Run that
// genuinely exists but belongs to a DIFFERENT project than the path names
// is leakage-normalized identically to a genuine not-found
// (httpapi.WriteResourceHidden, V6-02A's own policy) — this task's own
// "role/project matrix" Verify bullet, "wrong project" half.
//
// "Wrong role" half of that same Verify bullet: this query, like every
// other read query in this codebase (internal/app/runtime/queries.go's own
// GetContextSnapshot/ListEvidenceForWorkItem, internal/app/workspacestate's
// own GetWorkspaceSetState), is NOT role-gated — only a subset of
// MUTATING commands (e.g. runtime.ResolveApproval) check
// cmd.ActorRoles against a node's own AuthorizedRoles. There is no
// per-project "member role" concept anywhere in this codebase to gate a
// read against in the first place (ADR-028: one LocalPrincipalSnapshot per
// whole server process, not per-request/per-project). This package's own
// TestGetRunDiagnostics_HTTP_ArbitraryRoleStillReads test proves this
// intentional behavior directly (a principal holding a role with no
// relationship to this Run/WorkItem/Project still reads diagnostics
// successfully) rather than silently assuming it.
package diagnostics

import (
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// Dependencies is everything this package's one handler needs from the
// composition root — the SAME three dependencies
// internal/delivery/httpapi/recovery already needs and cmd/aw/serve.go
// already constructs for it (uow, the real process.IsolationChecker, the
// real agentregistry.Registry) — this task adds no new flag, no new
// composition-root construction of its own.
type Dependencies struct {
	UOW       ports.UnitOfWork
	Isolation ports.IsolationEnforcementChecker
	Agents    *agentregistry.Registry
}

// RegisterRoutes registers this package's own single route — operationId
// getRunDiagnostics (GET /projects/{projectId}/runs/{runId}/diagnostics),
// the name locked in by docs/design/11-v6-00-ux-artifact.md's own action
// inventory (Screen 8 row 4, reused verbatim by Screen 13 row 3 — "authority
// dùng chung", no second owner) — into routes. Contract point 8 ("Parallel
// work không sửa registry chung... Chỉ V6-12 compose HTTP router/OpenAPI")
// reserves aggregation across every endpoint task's fragments for V6-12
// alone; this function only ever adds its own one descriptor.
func RegisterRoutes(routes *httpapi.RouteRegistry, deps Dependencies) {
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{projectId}/runs/{runId}/diagnostics", OperationID: "getRunDiagnostics",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: RunDiagnosticsResponse{},
		Handler: handleGetRunDiagnostics(deps),
	})
}
