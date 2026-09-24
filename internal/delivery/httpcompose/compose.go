// Package httpcompose is V6-12's own extraction of the route-composition
// block that used to live inline inside cmd/aw/serve.go. Composing every
// HTTP route fragment (health/bootstrap plus each of the 19+ leaf
// packages' own RegisterRoutes call) is a `package main` (cmd/aw)
// responsibility, and nothing outside cmd/aw can ever import package
// main — so before this package existed, no test or tool could construct
// "the real, fully-composed production route set" without literally
// copy-pasting cmd/aw/serve.go's own registration sequence into a second,
// hand-maintained location that would silently drift the moment a future
// leaf task added a RegisterRoutes call to serve.go only.
//
// ComposeRoutes below is now the ONE place that sequence lives.
// cmd/aw/serve.go calls it with its own real, already-constructed runtime
// dependencies (a real SQLite-backed UnitOfWork, a real artifact store, a
// real Git worktree provider, ...). V6-12's own contract generator, golden
// test, breaking-diff gate and route-inventory test (internal/app/apicontract)
// call the identical function with dependencies built the same way
// cmd/aw/serve_test.go already builds them for its own end-to-end tests —
// a real temporary SQLite database and real temporary artifact/workspace
// root directories, never a mock RouteRegistry — so "served routes equal
// declared routes" holds true by construction, never by a second,
// separately-maintained assertion that could drift from what actually
// ships.
//
// This package registers no new route, DTO or authority of its own
// (docs/design/08-v6-api-projections.md V6-12's own "Không làm: no new
// endpoint/DTO/authority") — it only relocates the exact
// `routes.Register`/`xxx.RegisterRoutes` call sequence that used to live,
// verbatim, inside cmd/aw/serve.go's own serve function. Every leaf
// package still owns its own RegisterRoutes function, route fragment,
// descriptor and test (contract point 8, §1 of the design doc: "Mỗi
// endpoint/CLI task sở hữu subpackage, descriptor/schema fragment và test
// riêng. Chỉ V6-12 compose HTTP router/OpenAPI") — this file only
// sequences the calls.
package httpcompose

import (
	"context"
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	safesettingsapp "github.com/taQuangLing/agent-workflow/internal/app/safesettings"
	appworkspaceinspection "github.com/taQuangLing/agent-workflow/internal/app/workspaceinspection"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	httpadapterbuild "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/adapterbuild"
	httpcatalog "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/catalog"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/decision"
	httpdefinitions "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/definitions"
	httpdiagnostics "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/diagnostics"
	httpdoctor "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/doctor"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/eventstream"
	httpevidence "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/evidence"
	httpkanban "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/kanban"
	httpmessage "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/message"
	httpprojectionrebuild "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/projectionrebuild"
	recoveryhttp "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/recovery"
	httpreleaseset "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/releaseset"
	runhttp "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/run"
	httprundetail "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/rundetail"
	httpsafesettings "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/safesettings"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/workitem"
	httpworkspaceinspection "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/workspaceinspection"
)

// Dependencies is every already-constructed real dependency ComposeRoutes
// needs to register every HTTP route this process serves. Every field
// mirrors a value a composition root (cmd/aw/serve.go) already builds
// during its own startup sequence — ComposeRoutes performs no I/O and
// constructs nothing itself, it only wires already-built values into each
// leaf package's own RegisterRoutes call, exactly the way serve.go used
// to do inline. idsource.Source and clock.Clock are deliberately NOT
// fields here: every leaf's own production wiring always uses the
// stateless idsource.Random{}/clock.System{} literals (never a shared
// instance with meaningful state), so ComposeRoutes constructs those
// inline, the same way serve.go always has, rather than threading two
// more fields through this struct for no behavioral difference.
type Dependencies struct {
	// UnitOfWork is the one real ports.UnitOfWork every route-owning
	// package dispatches through.
	UnitOfWork ports.UnitOfWork
	// ArtifactStore is the real, composition-root-owned content-addressed
	// store internal/delivery/httpapi/message and .../evidence both read/
	// write through — the SAME store, never two separately-rooted ones.
	ArtifactStore ports.ArtifactStore
	// WorkspaceInspectionQueries is the real
	// internal/app/workspaceinspection.Queries backing the source/diff/
	// repository-log routes (internal/delivery/httpapi/workspaceinspection),
	// itself built from a real internal/adapters/gitworktree.Provider
	// rooted at the composition root's own --workspace-root.
	WorkspaceInspectionQueries *appworkspaceinspection.Queries
	// Matcher is the ONE process-lifetime known-secrets redact.Matcher
	// every redacting route in this process reuses (message, rundetail,
	// safesettings, eventstream) — never a second, differently-scoped one.
	Matcher redact.Matcher
	// Cursor signs/opens every package's own opaque pagination cursor
	// (httpapi.CursorCodec) — one process-lifetime secret shared by
	// message/rundetail/kanban, never a fresh one per package.
	Cursor *httpapi.CursorCodec
	// Isolation is the real ports.IsolationEnforcementChecker (ADR-013/
	// ADR-023) recovery/diagnostics both read.
	Isolation ports.IsolationEnforcementChecker
	// Agents is the real agentregistry.Registry (possibly holding zero
	// registered executors — the documented-safe default) recovery/
	// diagnostics both read.
	Agents *agentregistry.Registry
	// AppConfig is this process' own resolved, real internal/app/config.Config
	// — GET /doctor's own CheckAppConfig reads it directly.
	AppConfig config.Config
	// Store is the real ports.QueryStore GET /doctor's own CheckDatabase
	// pings — wraps the SAME database handle UnitOfWork already uses,
	// never a second connection.
	Store ports.QueryStore
	// SafeSettingsEffective is this process' own fixed, boot-time
	// safe-settings Effective snapshot (internal/app/safesettings/startup.go's
	// own ResolveEffective) — resolved exactly once, by the composition
	// root, before ComposeRoutes is ever called.
	SafeSettingsEffective safesettingsapp.Effective
	// Shutdown, passed to the eventstream package's own Dependencies.Shutdown,
	// is the context a composition root cancels on SIGINT/SIGTERM so every
	// open SSE stream closes itself before graceful shutdown proceeds. May
	// be nil (eventstream.Dependencies.Shutdown itself documents nil as "no
	// separate shutdown signal, r.Context() alone still applies") — a
	// caller that only needs the route LIST, never an actual open
	// connection, may leave this unset.
	Shutdown context.Context

	// LiveHandler, ReadyHandler, BootstrapHandler and StaticAssetHandler are
	// built by the caller: a real composition root builds them from its own
	// httpapi.ReadinessChecker, per-start session token, trusted
	// LocalPrincipalSnapshot and (V7-02A) `--ui-dist` build directory (none
	// of which ComposeRoutes has any business constructing itself —
	// V6-01/V6-01A's own territory, extended by V7-02A for the UI static
	// asset route). A caller that only needs the registered route LIST —
	// V6-12's own contract generator, golden test and route-inventory test —
	// may pass any non-nil http.HandlerFunc placeholder:
	// RouteDescriptor.Handler's own identity plays no role in the generated
	// contract or any of V6-12's gates, only httpapi.RouteRegistry.Register's
	// own non-nil check.
	LiveHandler        http.HandlerFunc
	ReadyHandler       http.HandlerFunc
	BootstrapHandler   http.HandlerFunc
	StaticAssetHandler http.HandlerFunc
}

// ComposeRoutes registers every HTTP route fragment this process serves
// into routes, in the exact order `aw serve` has always used: health/live,
// health/ready, bootstrap, then each leaf package's own RegisterRoutes
// call. It is the ONE composition point the design doc's own §1 rule 8
// names ("Chỉ V6-12 compose HTTP router/OpenAPI") — every leaf package
// still owns its own RegisterRoutes function and route fragment (this
// function adds no endpoint of its own), it only sequences the calls that
// used to live inline inside cmd/aw/serve.go's own serve function.
func ComposeRoutes(routes *httpapi.RouteRegistry, deps Dependencies) {
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/health/live", OperationID: "healthLive",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: struct{}{}, ResponseSchema: struct{}{},
		Handler: deps.LiveHandler,
	})
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/health/ready", OperationID: "healthReady",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: struct{}{}, ResponseSchema: struct{}{},
		Handler: deps.ReadyHandler,
	})
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/", OperationID: "bootstrap",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: struct{}{}, ResponseSchema: struct{}{},
		Handler: deps.BootstrapHandler,
	})
	// V7-02A: the built UI's static assets (ADR-028's own "ngoại lệ duy
	// nhất là bootstrap/static asset của browser" — exempt from the
	// UI/CLI/application-command parity inventory, same as bootstrap
	// above, but still a real registered route like any other so the
	// contract/route-inventory gates see one deterministic route list
	// regardless of whether this process was started with --ui-dist).
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/assets/", OperationID: "staticAsset",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: struct{}{}, ResponseSchema: struct{}{},
		Handler: deps.StaticAssetHandler,
	})
	// V6-03A: Project/repository/component catalog routes.
	httpcatalog.RegisterRoutes(routes, httpcatalog.Dependencies{UoW: deps.UnitOfWork, IDs: idsource.Random{}})
	// V6-04: WorkItem/family/readiness/scope-expansion routes.
	workitem.RegisterRoutes(routes, workitem.Dependencies{UnitOfWork: deps.UnitOfWork, IDs: idsource.Random{}, Clock: clock.System{}})
	// V6-06: Run start/cancel controls.
	runhttp.RegisterRoutes(routes, runhttp.Dependencies{UOW: deps.UnitOfWork, IDs: idsource.Random{}})
	// V6-06B: Run detail/graph/timeline query routes. Reuses the SAME
	// process-lifetime matcher/cursor every other redacting/paging route
	// in this process reuses.
	httprundetail.RegisterRoutes(routes, httprundetail.Dependencies{UnitOfWork: deps.UnitOfWork, Matcher: deps.Matcher, Cursor: deps.Cursor})
	// V6-06A: Approval decision and typed WAIT signal endpoints.
	decision.RegisterRoutes(routes, decision.Dependencies{UOW: deps.UnitOfWork, IDs: idsource.Random{}})
	// V6-10B: WorkspaceSet/repository-workspace state, lease/fence/quarantine
	// and release/reconcile request routes.
	httpapi.RegisterWorkspaceRoutes(routes, deps.UnitOfWork, idsource.Random{})
	// V6-10D: bounded source/diff/repository-log inspection routes, backed
	// by the real gitworktree-rooted Queries the composition root built.
	httpworkspaceinspection.RegisterRoutes(routes, httpworkspaceinspection.Dependencies{Queries: deps.WorkspaceInspectionQueries})
	// V6-05: Definition authoring routes.
	httpdefinitions.RegisterRoutes(routes, httpdefinitions.Dependencies{UnitOfWork: deps.UnitOfWork, IDs: idsource.Random{}, Clock: clock.System{}})
	// V6-06D: RetryBlockedActivation/CancelWorkItem/ResolveWorkItemBlocker
	// recovery command routes. Isolation/Agents are the real dependencies
	// the composition root built.
	recoveryhttp.RegisterRoutes(routes, recoveryhttp.Dependencies{
		UOW: deps.UnitOfWork, IDs: idsource.Random{}, Isolation: deps.Isolation, Agents: deps.Agents,
	})
	// V6-06C: Run diagnostics query. Reuses the SAME isolationChecker/
	// agentRegistry V6-06D already uses above (never a second, separately-
	// configured pair).
	httpdiagnostics.RegisterRoutes(routes, httpdiagnostics.Dependencies{
		UOW: deps.UnitOfWork, Isolation: deps.Isolation, Agents: deps.Agents,
	})
	// V6-07/V6-07A: conversation message endpoints plus binary attachment
	// upload. The SAME UnitOfWork/ArtifactStore this process already built
	// (never a second instance).
	httpmessage.RegisterRoutes(routes, httpmessage.Dependencies{
		UnitOfWork: deps.UnitOfWork, ArtifactStore: deps.ArtifactStore, IDs: idsource.Random{}, Clock: clock.System{},
		Matcher: deps.Matcher, Cursor: deps.Cursor,
	})
	// V6-07B: Evidence, ContextSnapshot and artifact query/content-stream
	// routes. Reuses the SAME artifactStore every other artifact-producing/
	// consuming route in this process already uses.
	httpevidence.RegisterRoutes(routes, httpevidence.Dependencies{UnitOfWork: deps.UnitOfWork, ArtifactStore: deps.ArtifactStore})
	// V6-10J: adapter-build registry routes (list/detail/probe/register).
	httpadapterbuild.RegisterRoutes(routes, httpadapterbuild.Dependencies{UnitOfWork: deps.UnitOfWork, Clock: clock.System{}})
	// V6-10H: GET/PUT /settings/safe. Matcher is the SAME process-lifetime
	// redactor httpmessage already reuses; Effective is the fixed,
	// boot-time snapshot the composition root resolved.
	httpsafesettings.RegisterRoutes(routes, httpsafesettings.Dependencies{
		UnitOfWork: deps.UnitOfWork, IDs: idsource.Random{}, Clock: clock.System{},
		Matcher: deps.Matcher, Effective: deps.SafeSettingsEffective,
	})
	// V6-10F: ReleaseSet list/create/detail/seal/abandon and per-entry
	// local-commit request/status routes.
	httpreleaseset.RegisterRoutes(routes, httpreleaseset.Dependencies{UnitOfWork: deps.UnitOfWork, IDs: idsource.Random{}, Clock: clock.System{}})
	// V6-10A: GET /doctor. Store/UnitOfWork/Isolation/Config are the SAME
	// real instances every other route registration in this process uses.
	httpdoctor.RegisterRoutes(routes, httpdoctor.Dependencies{
		Config: deps.AppConfig, Store: deps.Store, UnitOfWork: deps.UnitOfWork, Isolation: deps.Isolation,
	})
	// V6-10: projected Kanban card list and WorkItem detail routes. Reuses
	// the SAME process-lifetime cursor httpmessage/httprundetail already
	// reuse.
	httpkanban.RegisterRoutes(routes, httpkanban.Dependencies{UnitOfWork: deps.UnitOfWork, Cursor: deps.Cursor})
	// V6-11: the redacted project invalidation/runtime-summary SSE stream.
	// Reuses the SAME process-lifetime matcher every other redacting route
	// in this process already reuses; Shutdown is the SAME context the
	// composition root watches for SIGINT/SIGTERM.
	eventstream.RegisterRoutes(routes, eventstream.Dependencies{UnitOfWork: deps.UnitOfWork, Matcher: deps.Matcher, Shutdown: deps.Shutdown})
	// V6-09B: projection status/rebuild-request/rebuild-operation-status
	// routes.
	httpprojectionrebuild.RegisterRoutes(routes, httpprojectionrebuild.Dependencies{UnitOfWork: deps.UnitOfWork, IDs: idsource.Random{}, Clock: clock.System{}})
	// A later endpoint task's own composition-root wiring adds its own
	// routes.Register/RegisterRoutes call here without needing to touch
	// this file's shared setup (contract point 8: "Parallel work không sửa
	// registry chung").
}
