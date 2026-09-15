package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/artifactstore"
	"github.com/taQuangLing/agent-workflow/internal/adapters/gitworktree"
	"github.com/taQuangLing/agent-workflow/internal/adapters/process"
	"github.com/taQuangLing/agent-workflow/internal/adapters/providers/claude"
	"github.com/taQuangLing/agent-workflow/internal/adapters/providers/codex"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/logging"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/safesettings"
	appworkspaceinspection "github.com/taQuangLing/agent-workflow/internal/app/workspaceinspection"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	httpadapterbuild "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/adapterbuild"
	httpcatalog "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/catalog"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/decision"
	httpdiagnostics "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/diagnostics"
	httpdefinitions "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/definitions"
	httpdoctor "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/doctor"
	httpevidence "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/evidence"
	httpmessage "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/message"
	recoveryhttp "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/recovery"
	httpreleaseset "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/releaseset"
	runhttp "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/run"
	httpsafesettings "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/safesettings"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/workitem"
	httpworkspaceinspection "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/workspaceinspection"
)

// runServe is V6-01's own composition root entry point: it wires
// SIGINT/SIGTERM into a cancelable context and delegates everything else
// to serve, so the real composition logic (serve) can be driven by a
// test-controlled context instead of requiring an actual OS signal to
// exercise graceful shutdown.
func runServe(arguments []string, stdout io.Writer) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serve(ctx, arguments, stdout)
}

// serve is V6-01/V6-01A's own composition root: it wires config/DB/
// UnitOfWork/artifact-root readiness, the two health routes, the trusted
// LocalPrincipalSnapshot (ADR-028) and a fresh per-start session token
// (ADR-016) into a real httpapi.Server, registers the one bootstrap route
// allowed to hand that token to a browser, then serves until ctx is done,
// at which point it gracefully shuts down. No business endpoint is
// registered here — this task's own "Không làm" line reserves that for the
// endpoint tasks that come after V6-02/V6-02A.
func serve(ctx context.Context, arguments []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	dbPath := flags.String("db", "", "sqlite database path")
	artifactRoot := flags.String("artifact-root", "", "artifact storage root directory")
	// workspaceRoot is V6-10D's own composition-root addition: the source/
	// diff/repository-log routes (internal/delivery/httpapi/workspaceinspection)
	// need a real ports.WorkspaceInspectionReader — a real
	// internal/adapters/gitworktree.Provider, the first production composition
	// root in this codebase to ever construct one (grep confirms every prior
	// gitworktree.New call site is test-only). Required exactly like --db/
	// --artifact-root above: gitworktree.New itself requires a non-empty Root
	// (and creates it if missing, unlike --artifact-root's own
	// must-already-exist check below). This is deliberately its own flag, not
	// a reuse of safesettings' own ManagedWorkspaceRoot: that field is a
	// mutable-at-runtime (PUT /settings/safe) SQLite-backed value with no live
	// consumer anywhere in this codebase yet (its own doc comment: "no prior
	// task ever added that plumbing"; V6-10H's response even carries a
	// restartRequired field for exactly this reason) — constructing a real,
	// I/O-performing adapter from a value that can change underneath the
	// running process without ever being re-read would misrepresent both
	// concepts. --workspace-root is instead an ordinary immutable-per-process
	// startup flag, exactly like --artifact-root already is for artifactStore.
	workspaceRoot := flags.String("workspace-root", "", "root directory for real Git worktree-backed workspace storage (internal/adapters/gitworktree.Provider) that the source/diff/repository-log inspection routes read through")
	host := flags.String("host", "127.0.0.1", "loopback bind host")
	port := flags.Int("port", 0, "bind port (0 = OS-assigned ephemeral port)")
	maxBodyBytes := flags.Int64("max-body-bytes", 1<<20, "maximum accepted request body size in bytes")
	principalConfigPath := flags.String("principal-config", "", "path to a trusted JSON config file's localPrincipal.actor/localPrincipal.roles (ADR-028); omitted or missing means the local-operator/[operator] default — this is the only allowed way to select a principal, there is no --actor/--role flag")
	// claudeExecutable/codexExecutable are V6-06D's own composition-root
	// addition: RetryBlockedActivation (internal/delivery/httpapi/recovery)
	// needs a real, live agentregistry.Registry to re-probe a pinned
	// AdapterBuildVersion's own capabilities (the SAME dependency a
	// production ExecuteNodeHandler already carries). Each flag names the
	// executable for that provider EXACTLY the way `aw adapter probe/register
	// --executable` already does (cmd/aw/adapter.go's own newAgentExecutor) —
	// deliberately omitted (empty) by default, never defaulted to a bare
	// "claude"/"codex" PATH lookup: agentregistry.New itself does real I/O
	// (spawns the executable to measure its capabilities) for every executor
	// it is given, so silently registering both by default would make `aw
	// serve` itself fail to start on any machine that does not happen to
	// have both CLIs installed (most CI/dev machines). Leaving both unset
	// yields an empty Registry — the documented-safe zero-executor default
	// (internal/app/runtime/execute.go's own NewExecuteNodeHandler doc
	// comment) — under which RetryBlockedActivation still runs its
	// isolation check for real, and only fails closed with a typed 503 if a
	// retry actually needs to resolve a provider this server was never
	// configured to run. See baocaov6checklist.md's V6-06D section for the
	// full reasoning.
	claudeExecutable := flags.String("claude-executable", "", "path to the Claude CLI executable to register as a live agent provider for RetryBlockedActivation's own admission re-checks (omitted = provider not registered, RetryBlockedActivation fails closed with 503 for a build pinned to it)")
	codexExecutable := flags.String("codex-executable", "", "path to the Codex CLI executable to register as a live agent provider for RetryBlockedActivation's own admission re-checks (omitted = provider not registered, RetryBlockedActivation fails closed with 503 for a build pinned to it)")
	// workerID is V6-10A's own composition-root addition
	// (docs/design/08-v6-api-projections.md): the one flag needed to build a
	// real, fully-valid internal/app/config.Config for GET /doctor's own
	// CheckAppConfig (internal/app/doctor/checks.go) to run against — before
	// this task, `aw serve` never constructed a config.Config value at all
	// (only ad hoc --db/--artifact-root flags), so CheckAppConfig had
	// nothing to check. `aw serve` itself never runs the lease/reaper worker
	// pool (there is no --worker-concurrency/--lease-ttl/--lease-heartbeat
	// flag here, and Doctor's own CheckWorker stays false, see
	// internal/delivery/httpapi/doctor.Dependencies' own doc comment) — this
	// is honestly this SERVE process' own identity string, not a lease-fence
	// owner id a real worker pool would use; config.Validate requires it
	// non-empty regardless (it has no way to know a given process opts out
	// of running a worker), so it defaults to a stable, always-valid value
	// rather than leaving Doctor permanently, un-actionably BLOCKED on every
	// installation that never sets it.
	workerID := flags.String("worker-id", "aw-serve", "identity string recorded in this process' own config.Config for GET /doctor's config-validity check; this process does not itself run the lease/reaper worker pool (see the future `aw worker` command's own --worker-id for that)")
	if err := flags.Parse(arguments); err != nil {
		return usageError{err}
	}
	if strings.TrimSpace(*dbPath) == "" {
		return usageError{errors.New("--db is required")}
	}
	if strings.TrimSpace(*artifactRoot) == "" {
		return usageError{errors.New("--artifact-root is required")}
	}
	if strings.TrimSpace(*workspaceRoot) == "" {
		return usageError{errors.New("--workspace-root is required")}
	}

	// ADR-028: Actor/Roles are read only from trusted startup config, never
	// from a per-command flag — resolved once, here, before anything else
	// starts, and bound to the server for its whole process lifetime.
	// Changing --principal-config only ever takes effect on the next
	// restart (there is no reload path).
	localPrincipal, err := config.LoadLocalPrincipalFile(*principalConfigPath)
	if err != nil {
		return err
	}
	if err := config.ValidateLocalPrincipal(localPrincipal); err != nil {
		return err
	}

	// ADR-016: a fresh random token every process start, kept only in
	// memory (via httpapi.Config.Token / the closure BootstrapHandler
	// captures) — generated here, once, using the same idsource.Source
	// primitive every other ID in this composition root uses, and never
	// assigned to a variable that later gets logged, printed or persisted.
	sessionToken := idsource.Random{}.NewID()

	store, uow, err := openDefinitionDB(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	if info, statErr := os.Stat(*artifactRoot); statErr != nil || !info.IsDir() {
		return fmt.Errorf("--artifact-root %q is not an existing directory", *artifactRoot)
	}

	// V6-07: the first real caller in this composition root that needs a
	// ports.ArtifactStore (internal/app/message.AppendMessage's own
	// content-addressed attach step) — rooted at the same --artifact-root
	// every other artifact-producing command in this process will ever use,
	// never a second, competing root.
	artifactStore, err := artifactstore.New(*artifactRoot)
	if err != nil {
		return fmt.Errorf("open artifact store: %w", err)
	}

	// V6-10D: the first real caller in this composition root that needs a
	// ports.WorkspaceInspectionReader — a real gitworktree.Provider rooted at
	// --workspace-root, feeding internal/delivery/httpapi/workspaceinspection's
	// own source/diff/repository-log routes below. gitworktree.New itself
	// creates --workspace-root if it does not already exist (unlike
	// --artifact-root's own must-already-exist os.Stat check above).
	workspaceProvider, err := gitworktree.New(gitworktree.Config{Root: *workspaceRoot})
	if err != nil {
		return fmt.Errorf("open git workspace provider: %w", err)
	}
	workspaceInspectionQueries := appworkspaceinspection.New(uow, workspaceProvider)

	// sessionToken is registered as a known secret with the shared redactor
	// as defense-in-depth: ADR-016 forbids ever logging it, and this ensures
	// that even a future logging call some other code path mistakenly adds
	// still cannot emit it verbatim. V6-07 reuses this identical matcher
	// (never a second, differently-scoped one) as the ONE process-lifetime
	// known-secrets matcher every AppendMessage call redacts against — see
	// internal/delivery/httpapi/message.Dependencies' own Matcher doc
	// comment for why an HTTP caller never gets to supply its own.
	matcher := redact.NewMatcher(sessionToken)
	logger := logging.New(os.Stderr, logging.JSON, matcher)

	// V6-07: a fresh per-process secret for this server's own opaque
	// ListMessages pagination cursor (httpapi.CursorCodec) — minted the
	// identical way sessionToken above already is (idsource.Random{}.NewID(),
	// never a fixed compiled-in value), never persisted or logged.
	cursorCodec := httpapi.NewCursorCodec([]byte(idsource.Random{}.NewID()))

	// V6-06D: build the real agent-provider registry
	// internal/delivery/httpapi/recovery's own RetryBlockedActivation route
	// needs — see --claude-executable/--codex-executable's own doc comment
	// above for why each provider is only ever registered when the operator
	// explicitly opts in, never defaulted. agentregistry.New does real I/O
	// (spawns the executable once, synchronously, to measure its live
	// capabilities) for every executor passed to it, so this fails the whole
	// `aw serve` startup closed — the same "a provider the operator asked
	// for but is actually broken should fail fast at boot, not silently
	// accept requests that will later fail confusingly" choice `aw adapter
	// probe/register` already makes for the identical construction.
	var agentExecutors []ports.AgentExecutor
	if executable := strings.TrimSpace(*claudeExecutable); executable != "" {
		claudeExecutor, err := claude.New(process.NewSupervisor(), claude.Config{Executable: executable})
		if err != nil {
			return fmt.Errorf("construct claude agent executor: %w", err)
		}
		agentExecutors = append(agentExecutors, claudeExecutor)
	}
	if executable := strings.TrimSpace(*codexExecutable); executable != "" {
		codexExecutor, err := codex.New(process.NewSupervisor(), codex.Config{Executable: executable})
		if err != nil {
			return fmt.Errorf("construct codex agent executor: %w", err)
		}
		agentExecutors = append(agentExecutors, codexExecutor)
	}
	// Zero executors (the default) is the documented-safe empty Registry
	// (internal/app/runtime/execute.go's own NewExecuteNodeHandler doc
	// comment: "a caller with no real adapter builds ever pinned can safely
	// pass agentregistry.New(ctx) with zero executors registered") — never a
	// nil *agentregistry.Registry, which RetryBlockedActivationHandler.Retry
	// would otherwise have to guard against separately.
	agentRegistry, err := agentregistry.New(ctx, agentExecutors...)
	if err != nil {
		return fmt.Errorf("build agent executor registry: %w", err)
	}
	// process.NewIsolationChecker is the one real ports.IsolationEnforcementChecker
	// this codebase has (ADR-013/ADR-023) — static and I/O-free, so unlike
	// the registry above this is unconditional, no flag needed.
	isolationChecker := process.NewIsolationChecker()

	// V6-10A: this process' own real, fully-valid config.Config — built from
	// config.Defaults() (which already supplies valid non-zero
	// WorkerConcurrency/LeaseTTL/LeaseHeartbeat/ProcessOutputLimit; `aw
	// serve` has no flag for any of those, and needs none, since it never
	// runs the worker pool CheckWorker would gate) with DatabasePath/
	// ArtifactRoot/WorkerID/ProviderExecutables overridden to the real
	// values THIS process actually has in scope — the same dbPath/
	// artifactRoot already used to open the database/artifact root above,
	// and the same claudeExecutable/codexExecutable already used to build
	// agentExecutors above, never a second, re-derived copy of any of them.
	// Exists for GET /doctor's own CheckAppConfig
	// (internal/delivery/httpapi/doctor, internal/app/doctor/checks.go) —
	// no earlier task ever constructed a config.Config here at all.
	appConfig := config.Defaults()
	appConfig.DatabasePath = *dbPath
	appConfig.ArtifactRoot = *artifactRoot
	appConfig.WorkerID = *workerID
	appConfig.ProviderExecutables = map[string]string{}
	if executable := strings.TrimSpace(*claudeExecutable); executable != "" {
		appConfig.ProviderExecutables["claude"] = executable
	}
	if executable := strings.TrimSpace(*codexExecutable); executable != "" {
		appConfig.ProviderExecutables["codex"] = executable
	}

	routes := httpapi.NewRouteRegistry()
	checker := httpapi.NewReadinessChecker()
	checker.Register("database", func(ctx context.Context) error {
		return uow.WithReadOnly(ctx, func(ports.Tx) error { return nil })
	})
	checker.Register("artifact_root", func(ctx context.Context) error {
		info, err := os.Stat(*artifactRoot)
		if err != nil {
			return fmt.Errorf("artifact root: %w", err)
		}
		if !info.IsDir() {
			return errors.New("artifact root is not a directory")
		}
		return nil
	})
	// V6-10G (docs/design/08-v6-api-projections.md): a corrupt persisted
	// safe-settings desired document must fail readiness typed, never a
	// panic or a silent fallback to the zero document.
	checker.Register("safe_settings", func(ctx context.Context) error {
		return uow.WithReadOnly(ctx, func(tx ports.Tx) error {
			_, err := tx.SafeSettings().Get(ctx)
			return err
		})
	})

	// V6-10H (docs/design/08-v6-api-projections.md): this process' own
	// fixed, boot-time safe-settings Effective snapshot
	// (internal/app/safesettings/startup.go's own ResolveEffective) —
	// resolved EXACTLY ONCE, here, right after the database is open and
	// before any route is registered, from whatever SafeSettingsRecord.
	// Desired is persisted right now plus the file/env/flag
	// StartupOverrides this process actually has in scope. `aw serve` does
	// not parse a config file, and does not read any of the 7 safe-
	// settings-allowlisted fields from the environment, today — no prior
	// task ever added that plumbing (internal/app/config.Load exists but
	// nothing in cmd/aw calls it; --db/--artifact-root above are this
	// command's own ad hoc flags, never routed through config.Overrides at
	// all) — so the file and env StartupOverrides below are both the
	// honest empty value, and flags is likewise empty (this task adds no
	// --managed-workspace-root-style flag of its own, per its own "Không
	// làm: no extra setting"). Only the defaults and SQLite layers are
	// live in this composition root today; a future task that adds real
	// file/env/flag parsing for these 7 fields only has to populate the
	// StartupOverrides values below — this call's own shape never changes.
	//
	// Captured once, never recomputed per-request: Alpha has no hot-reload
	// path at all (V6-10G's own "no live mutation of immutable process
	// config"), so this value accurately describes what THIS running
	// process actually uses for its entire lifetime, even after a later
	// PUT /settings/safe changes the live desired document underneath it —
	// see internal/delivery/httpapi/safesettings.Dependencies' own
	// Effective doc comment, and baocaov6checklist.md's V6-10H section, for
	// the full reasoning.
	//
	// A corrupt persisted document (ports.ErrSafeSettingsCorrupt) must
	// never prevent this process from starting — that failure surfaces
	// via the "safe_settings" readiness check registered just above
	// (TestServe_ReadyFailsIfSafeSettingsCorrupt proves this against the
	// real composition root: /health/ready must report 503 naming
	// "safe_settings", not `aw serve` itself refusing to boot). The error
	// is intentionally ignored here rather than failing startup a second,
	// harder way: GetSafeSettings' own source never assigns its result
	// before an error return, so safeSettingsAtBoot.Desired is already the
	// safe "never configured" zero document in that case — exactly the
	// state ResolveEffective already documents as "SQLite contributes
	// nothing to any field".
	safeSettingsAtBoot, _ := safesettings.GetSafeSettings(ctx, uow)
	safeSettingsEffective := safesettings.ResolveEffective(
		safesettings.Defaults(), safesettings.StartupOverrides{}, safeSettingsAtBoot.Desired,
		safesettings.StartupOverrides{}, safesettings.StartupOverrides{},
	)

	routesFinalized := false
	checker.Register("routes", func(ctx context.Context) error {
		if !routesFinalized {
			return errors.New("route composition not finalized")
		}
		return nil
	})

	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/health/live", OperationID: "healthLive",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: struct{}{}, ResponseSchema: struct{}{},
		Handler: httpapi.LiveHandler(),
	})
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/health/ready", OperationID: "healthReady",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: struct{}{}, ResponseSchema: struct{}{},
		Handler: checker.ReadyHandler(),
	})
	principal := httpapi.LocalPrincipalSnapshot{Actor: localPrincipal.Actor, Roles: localPrincipal.Roles}
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/", OperationID: "bootstrap",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: struct{}{}, ResponseSchema: struct{}{},
		Handler: httpapi.BootstrapHandler(sessionToken, principal, idsource.Random{}),
	})
	// V6-03A (docs/design/08-v6-api-projections.md): Project/repository/
	// component catalog routes, owning its own subpackage/descriptors/tests
	// (internal/delivery/httpapi/catalog) exactly as V6-01A's own comment
	// above anticipated.
	httpcatalog.RegisterRoutes(routes, httpcatalog.Dependencies{UoW: uow, IDs: idsource.Random{}})
	// V6-04: WorkItem/family/readiness/scope-expansion routes
	// (internal/delivery/httpapi/workitem) — an additive routes.Register
	// call only, no shared setup above touched.
	workitem.RegisterRoutes(routes, workitem.Dependencies{UnitOfWork: uow, IDs: idsource.Random{}, Clock: clock.System{}})
	// V6-06: Run start/cancel controls (internal/delivery/httpapi/run) — an
	// additive routes.Register call only, no shared setup above touched.
	runhttp.RegisterRoutes(routes, runhttp.Dependencies{UOW: uow, IDs: idsource.Random{}})
	// V6-06A: Approval decision and typed WAIT signal endpoints
	// (internal/delivery/httpapi/decision) — an additive routes.Register
	// call only, no shared setup above touched.
	decision.RegisterRoutes(routes, decision.Dependencies{UOW: uow, IDs: idsource.Random{}})
	// V6-10B: WorkspaceSet/repository-workspace state, lease/fence/quarantine
	// and release/reconcile request routes. Same uow/idsource.Random{} every
	// other route registration in this process already uses — never a
	// fresh source per request.
	httpapi.RegisterWorkspaceRoutes(routes, uow, idsource.Random{})
	// V6-10D: bounded source/diff/repository-log inspection routes
	// (internal/delivery/httpapi/workspaceinspection) — an additive
	// routes.Register call only, no shared setup above touched. Queries is
	// the one real *appworkspaceinspection.Queries built just above, backed
	// by the real gitworktree.Provider rooted at --workspace-root.
	httpworkspaceinspection.RegisterRoutes(routes, httpworkspaceinspection.Dependencies{Queries: workspaceInspectionQueries})
	// V6-05: Definition authoring routes (internal/delivery/httpapi/definitions)
	// — create/validate/publish/list/detail/version/diff for global and
	// project-scoped definitions — an additive routes.Register call only,
	// no shared setup above touched.
	httpdefinitions.RegisterRoutes(routes, httpdefinitions.Dependencies{UnitOfWork: uow, IDs: idsource.Random{}, Clock: clock.System{}})
	// V6-06D: RetryBlockedActivation/CancelWorkItem/ResolveWorkItemBlocker
	// recovery command routes (internal/delivery/httpapi/recovery) — an
	// additive routes.Register call only, no shared setup above touched.
	// Isolation/Agents are the real dependencies built just above.
	recoveryhttp.RegisterRoutes(routes, recoveryhttp.Dependencies{
		UOW: uow, IDs: idsource.Random{}, Isolation: isolationChecker, Agents: agentRegistry,
	})
	// V6-06C: Run diagnostics query (internal/delivery/httpapi/diagnostics)
	// — an additive routes.Register call only, no shared setup above
	// touched. Reuses the SAME isolationChecker/agentRegistry V6-06D already
	// built above (never a second, separately-configured pair): diagnostics
	// only ever performs pure, I/O-free lookups against them (a live
	// process re-probe stays RetryBlockedActivation's own exclusive
	// authority — see that package's own diagnostics.go doc comment).
	httpdiagnostics.RegisterRoutes(routes, httpdiagnostics.Dependencies{
		UOW: uow, Isolation: isolationChecker, Agents: agentRegistry,
	})
	// V6-07: conversation message endpoints (append/list/context-snapshot,
	// internal/delivery/httpapi/message) — an additive routes.Register call
	// only, no shared setup above touched.
	httpmessage.RegisterRoutes(routes, httpmessage.Dependencies{
		UnitOfWork: uow, ArtifactStore: artifactStore, IDs: idsource.Random{}, Clock: clock.System{},
		Matcher: matcher, Cursor: cursorCodec,
	})
	// V6-07B: Evidence, ContextSnapshot and artifact query/content-stream
	// routes (internal/delivery/httpapi/evidence) — an additive
	// routes.Register call only, no shared setup above touched. Reuses the
	// SAME artifactStore every other artifact-producing/consuming route in
	// this process already uses, never a second one rooted elsewhere.
	httpevidence.RegisterRoutes(routes, httpevidence.Dependencies{UnitOfWork: uow, ArtifactStore: artifactStore})
	// V6-10J: adapter-build registry routes (list/detail/probe/register,
	// internal/delivery/httpapi/adapterbuild) — an additive routes.Register
	// call only, no shared setup above touched. Installation-scoped, over
	// the same uow every other route registration in this process already
	// uses; no idsource.Source needed (a Build's own ID is content-addressed,
	// never minted).
	httpadapterbuild.RegisterRoutes(routes, httpadapterbuild.Dependencies{UnitOfWork: uow, Clock: clock.System{}})
	// V6-10H: GET/PUT /settings/safe (internal/delivery/httpapi/safesettings)
	// — an additive routes.Register call only, no shared setup above
	// touched. Matcher is the SAME process-lifetime redactor httpmessage
	// already reuses (never a second, differently-scoped one); Effective is
	// the fixed, boot-time snapshot resolved just above.
	httpsafesettings.RegisterRoutes(routes, httpsafesettings.Dependencies{
		UnitOfWork: uow, IDs: idsource.Random{}, Clock: clock.System{},
		Matcher: matcher, Effective: safeSettingsEffective,
	})
	// V6-10F: ReleaseSet list/create/detail/seal/abandon and per-entry
	// local-commit request/status routes (internal/delivery/httpapi/releaseset)
	// — an additive routes.Register call only, no shared setup above touched.
	httpreleaseset.RegisterRoutes(routes, httpreleaseset.Dependencies{UnitOfWork: uow, IDs: idsource.Random{}, Clock: clock.System{}})
	// V6-10A: GET /doctor (internal/delivery/httpapi/doctor) — an additive
	// routes.Register call only, no shared setup above touched. Store is
	// wrapped as a ports.QueryStore the same way internal/app/doctor's own
	// golden tests do (sqlite.NewQueryStore(store), never a second
	// connection); UnitOfWork/Isolation/Config are the SAME real instances
	// every other route registration in this process already uses.
	httpdoctor.RegisterRoutes(routes, httpdoctor.Dependencies{
		Config: appConfig, Store: sqlite.NewQueryStore(store), UnitOfWork: uow, Isolation: isolationChecker,
	})
	// A later endpoint task's own composition-root wiring adds its own
	// routes.Register call here without needing to touch this file's shared
	// setup (contract point 8: "Parallel work không sửa registry chung").
	routesFinalized = true

	server, err := httpapi.NewServer(httpapi.Config{
		Host: *host, Port: *port, Routes: routes, IDs: idsource.Random{},
		Logger: logger, MaxBodyBytes: *maxBodyBytes,
		Token: sessionToken, Principal: principal,
	})
	if err != nil {
		return err
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve() }()

	fmt.Fprintf(stdout, "{\"address\":%q}\n", server.Addr())

	select {
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return <-serveErr
	}
}
