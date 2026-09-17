// Package projectionrebuild is V6-09B's own HTTP slice
// (docs/design/08-v6-api-projections.md V6-09B: "expose projection status,
// request and exact rebuild-operation status") over the already-hardened
// internal/app/projectionrebuild application layer (V6-09, merged):
// GetProjectionStatus is a plain public query this task adds (see that
// function's own doc comment for why it lives in that package rather than
// here or in internal/app/projection); RequestProjectionRebuild is a
// ports.Command-enveloped mutation this package only ever DISPATCHES,
// never re-implements; GetProjectionRebuildStatus is a plain, exact,
// by-ID public query.
//
// Route inventory (all three project-scoped — the design doc's own
// "GET /projects/{id}/projection, POST rebuild, GET operation status"
// line, with the path spelling that first route already locks in, "{id}",
// reused for the other two rather than switching to "{projectId}" partway
// through one package):
//
//	GET  /projects/{id}/projection                                    getProjectionStatus
//	POST /projects/{id}/projection/rebuild                            requestProjectionRebuild
//	GET  /projects/{id}/projection/rebuild-operations/{operationId}   getProjectionRebuildOperationStatus
//
// Every route reloads its own authoritative target before doing anything
// else (Contract chung §3: "Mọi item route reload authoritative target để
// suy Project/scope và authorize; không tin ID shape, payload hoặc
// projection"): the first two reload the named Project itself via
// catalog.GetProject (mirrors internal/delivery/httpapi/workitem's own
// handleCreateRootWorkItem and internal/delivery/httpapi/catalog's own
// listProjectComponents — there is no other, narrower aggregate for those
// two routes to reload, the Project named by the path IS the target); the
// third instead reloads the ProjectionRebuildOperation itself (by ID, via
// GetProjectionRebuildStatus) and checks its own ProjectID against the
// path — mirrors internal/delivery/httpapi/releaseset's own
// handleGetReleaseSetLocalCommitStatus exactly, the identical "the
// by-ID-loaded record's own ProjectID is the authoritative scope check,
// not a second reload of the Project" shape for a query whose real target
// already carries its own ProjectID.
//
// The mutating route (requestProjectionRebuild) follows
// internal/delivery/httpapi/adapterbuild's own "thin dispatch only, no
// pre-dispatch receipt-replay fast path" shape (adapterbuild.go's own doc
// comment explains the reasoning at length), NOT
// internal/delivery/httpapi/workitem's or releaseset's own
// prepareCreateCommand+replayOrProceed shape — confirmed correct by
// reading internal/app/projectionrebuild.RequestProjectionRebuild's own
// doc comment before writing this package: it already performs its own
// receipt lookup/replay internally, inside its own WithSerializedWrite
// transaction, exactly like ProbeAdapterBuild/RegisterAdapterBuild do. A
// second, HTTP-layer-only replay check here would not be wrong, exactly,
// but it would double up with what the real command already does, for no
// benefit — so this package's own POST handler still requires
// Idempotency-Key (to build the ports.Command envelope, since
// RequestProjectionRebuild's own replay keys on it), but dispatches
// straight through, every time, and lets RequestProjectionRebuild's own
// internal replay logic be the only replay logic that ever runs. A retry
// with the same Idempotency-Key therefore still returns the exact same
// OperationID (V6-09B's own "Verify: ... replay" line) — just via the real
// command's own replay, not a transport-layer shortcut.
//
// This package never reaches internal/app/projection's live-consumer/
// worker code, internal/app/projectionrebuildworker's rebuild worker, or
// any raw ports.Tx accessor directly (V6-09B's own "Không làm: handler
// không call worker/row store or infer latest operation" line) — every
// handler here only ever calls into internal/app/projectionrebuild's own
// three public functions, proved mechanically by internal/archtest's own
// TestProjectionRebuildHTTPNeverReachesWorkerOrRowStoreDirectly, mirroring
// V6-10J's TestAdapterBuildHTTPNeverReachesProcessOrFilesystemDirectly and
// V6-06D's TestRecoveryHTTPNeverReachesSchedulerOrWorkerOrMutatesStateDirectly.
// GetProjectionRebuildStatus's own by-ID-only contract is also why this
// package never tries to compute "the latest operation" itself — there is
// no such query to call, by design (that function's own doc comment: "it
// never infers 'the latest' operation").
package projectionrebuild

import (
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// Dependencies is everything this package's three handlers need from the
// composition root.
type Dependencies struct {
	// UnitOfWork is the one real ports.UnitOfWork every handler in this
	// package dispatches through.
	UnitOfWork ports.UnitOfWork
	// IDs mints RequestProjectionRebuild's own new OperationID/JobID — the
	// only one of this package's three routes that ever mints an ID.
	IDs idsource.Source
	// Clock supplies cmd.RequestedAt for the one command envelope this
	// package's mutating handler builds — internal/app/clock's own "no
	// caller reaches for time.Now() directly" discipline.
	Clock clock.Clock
}

// RegisterRoutes registers this package's own three route fragments onto
// routes — a composition root (cmd/aw/serve.go) calls this once, alongside
// every sibling endpoint task's own RegisterRoutes. This function only ever
// adds its own three descriptors; aggregating every endpoint task's
// fragments into one served router/OpenAPI document is V6-12's own job
// alone (contract point 8: "Parallel work không sửa registry chung... Chỉ
// V6-12 compose HTTP router/OpenAPI").
func RegisterRoutes(routes *httpapi.RouteRegistry, deps Dependencies) {
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{id}/projection", OperationID: "getProjectionStatus",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: projectionStatusView{},
		Handler: handleGetProjectionStatus(deps),
	})
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/projects/{id}/projection/rebuild", OperationID: "requestProjectionRebuild",
		ScopeKind: httpapi.ScopeProject, RequestSchema: requestProjectionRebuildBody{}, ResponseSchema: projectionRebuildResultView{},
		Handler: handleRequestProjectionRebuild(deps),
	})
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{id}/projection/rebuild-operations/{operationId}", OperationID: "getProjectionRebuildOperationStatus",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: projectionRebuildOperationStatusView{},
		Handler: handleGetProjectionRebuildOperationStatus(deps),
	})
}

// commandTypeRequestProjectionRebuild is the exact command-type string
// this package's own POST handler stamps onto every ports.Command it
// builds — must equal internal/app/projectionrebuild's own
// RequestProjectionRebuild receipt CommandType exactly (that function
// itself never hardcodes a name, it always reads cmd.Type verbatim), so a
// receipt recorded through this HTTP surface is addressed consistently
// across replays.
const commandTypeRequestProjectionRebuild = "RequestProjectionRebuild"
