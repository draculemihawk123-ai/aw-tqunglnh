// Package adapterbuild is V6-10J's own HTTP slice
// (docs/design/08-v6-api-projections.md V6-10J: "expose list/detail/probe/
// register only after V6-10I hardening") over the already-hardened
// internal/app/adapterbuild application commands (V6-10I, PR #36, `4e44af1`):
// ListAdapterBuilds/GetAdapterBuild are plain public queries, and
// ProbeAdapterBuild/RegisterAdapterBuild are ports.Command-enveloped
// mutations this package only ever DISPATCHES, never re-implements.
//
// Route inventory (all four installation-scoped — ADR-025's own closed
// table names ProbeAdapterBuild/RegisterAdapterBuild explicitly, and
// AdapterBuildVersion is "thuộc tính của máy đang chạy chứ không phải của
// dự án" (docs/architecture/02-architecture-decisions.md ADR-022) — there
// is deliberately no project-scoped mirror of any of these four routes,
// this task's own "Không làm: no project mirror" line):
//
//	GET  /adapter-builds              listAdapterBuilds
//	GET  /adapter-builds/{id}         getAdapterBuild
//	POST /adapter-builds/probe        probeAdapterBuild
//	POST /adapter-builds              registerAdapterBuild
//
// Every mutating handler follows the SAME shorter "create-shaped command"
// preamble internal/delivery/httpapi/workitem's own prepareCreateCommand
// establishes (require Idempotency-Key, canonicalize the body, build the
// ports.Command envelope with ports.InstallationScope(), dispatch) — but,
// unlike workitem, this package's handlers never call httpapi.LookupReceipt/
// ReconcileReceipt/WriteReceiptReplay before dispatch. That pre-dispatch
// fast path is a deliberate, documented latency optimization in workitem
// (receiptreplay.go's own doc comment: "purely a latency/UX optimization
// ... never a correctness dependency"), but THIS task's own "Không làm: ...
// transport receipt" line is a stronger, explicit instruction: this
// package must never invent its own separate receipt/idempotency
// bookkeeping layered on top of what ProbeAdapterBuild/RegisterAdapterBuild
// ALREADY do internally (loadProbeReplay/loadRegisterReplay, V6-10I) —
// both commands already perform their own receipt lookup, BEFORE any real
// filesystem I/O, inside their own real transaction boundary. Adding a
// second, HTTP-layer replay check here would not be wrong, exactly, but it
// would be exactly the kind of "double up" this task's spec explicitly
// forbids, and it buys nothing: neither Probe nor Register can be made any
// safer by a second, redundant check of the same underlying receipt row.
// So every mutating handler here dispatches straight through — "thin
// dispatch only" (this task's own "Thực hiện" line) — and lets the real
// command's own internal replay logic be the only replay logic that ever
// runs.
//
// This package never spawns a real provider CLI process and never reads a
// file off the local filesystem itself (this task's own "Không làm: ...
// direct prober/process" line) — ProbeRequest's own real shape
// (ProviderKey/ExecutablePath/ProtocolVersion/CapabilityManifest/OS/
// Toolchain/ConfigIdentity, internal/app/adapterbuild/commands.go) is
// exactly what this package's own probeAdapterBuildBody DTO mirrors
// field-for-field: a caller supplies every measured/declared value in the
// request body, and this package forwards it verbatim to
// appadapterbuild.ProbeAdapterBuild, which is the ONLY place any of the
// real filesystem hashing (HashExecutableFile) happens — never this
// package's own handler, never cmd/aw/adapter.go's own
// newAgentExecutor/AgentExecutor.Capabilities live-process-spawn path
// (that CLI-only convenience of deriving ProtocolVersion/CapabilityManifest
// from a REAL live executor is deliberately not replicated here; an
// HTTP caller supplies those fields directly). Proved mechanically, not
// just by doc comment, by internal/archtest's own
// TestAdapterBuildHTTPNeverReachesProcessOrFilesystemDirectly, mirroring
// V6-06D's TestRecoveryHTTPNeverReachesSchedulerOrWorkerOrMutatesStateDirectly.
package adapterbuild

import (
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	domainadapterbuild "github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
)

// Dependencies is everything this package's four handlers need from the
// composition root. Deliberately narrower than
// internal/delivery/httpapi/recovery's own Dependencies (no idsource.Source,
// no Isolation/Agents): neither ProbeAdapterBuild nor RegisterAdapterBuild
// mints a new aggregate ID (a Build's ID is content-addressed —
// domainadapterbuild.CandidateTuple.ID(), computed inside the command
// itself) or needs a real AgentExecutor/isolation checker.
type Dependencies struct {
	// UnitOfWork is the one real ports.UnitOfWork every handler in this
	// package dispatches through.
	UnitOfWork ports.UnitOfWork
	// Clock supplies cmd.RequestedAt for every command envelope this
	// package's two mutating handlers build — internal/app/clock's own "no
	// caller reaches for time.Now() directly" discipline.
	Clock clock.Clock
}

// RegisterRoutes registers this package's own four route fragments onto
// routes — a composition root (cmd/aw/serve.go) calls this once, alongside
// every sibling endpoint task's own RegisterRoutes. This function only ever
// adds its own four descriptors; aggregating every endpoint task's
// fragments into one served router/OpenAPI document is V6-12's own job
// alone (contract point 8: "Parallel work không sửa registry chung... Chỉ
// V6-12 compose HTTP router/OpenAPI").
func RegisterRoutes(routes *httpapi.RouteRegistry, deps Dependencies) {
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/adapter-builds", OperationID: "listAdapterBuilds",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: struct{}{}, ResponseSchema: adapterBuildListResponse{},
		Handler: handleListAdapterBuilds(deps),
	})
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/adapter-builds/{id}", OperationID: "getAdapterBuild",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: struct{}{}, ResponseSchema: adapterBuildView{},
		Handler: handleGetAdapterBuild(deps),
	})
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/adapter-builds/probe", OperationID: "probeAdapterBuild",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: probeAdapterBuildBody{}, ResponseSchema: domainadapterbuild.CandidateToken{},
		Handler: handleProbeAdapterBuild(deps),
	})
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/adapter-builds", OperationID: "registerAdapterBuild",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: registerAdapterBuildBody{}, ResponseSchema: registerAdapterBuildResponse{},
		Handler: handleRegisterAdapterBuild(deps),
	})
}

// commandType constants mirror cmd/aw/adapter.go's own exact command-type
// strings (requestHash("ProbeAdapterBuild", ...) /
// requestHash("RegisterAdapterBuild", ...)) so a receipt/event recorded
// through this HTTP surface and one recorded through the CLI are
// indistinguishable at the command-type level — both are the same public
// operation, reached through two different transports.
const (
	commandTypeProbe    = "ProbeAdapterBuild"
	commandTypeRegister = "RegisterAdapterBuild"
)
