package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/artifactstore"
	"github.com/taQuangLing/agent-workflow/internal/adapters/gitworktree"
	"github.com/taQuangLing/agent-workflow/internal/adapters/process"
	"github.com/taQuangLing/agent-workflow/internal/adapters/repoprobe"
	"github.com/taQuangLing/agent-workflow/internal/adapters/secretenv"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/agentevents"
	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/artifactsweep"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/logging"
	"github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/projection"
	"github.com/taQuangLing/agent-workflow/internal/app/projectionrebuild"
	"github.com/taQuangLing/agent-workflow/internal/app/projectionrebuildworker"
	"github.com/taQuangLing/agent-workflow/internal/app/readinesscheck"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/releasesetcommit"
	"github.com/taQuangLing/agent-workflow/internal/app/repositoryprobe"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	appwork "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
	"github.com/taQuangLing/agent-workflow/internal/app/workspaceprovision"
	"github.com/taQuangLing/agent-workflow/internal/app/workspacereconcile"
	"github.com/taQuangLing/agent-workflow/internal/app/workspacerelease"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

const (
	defaultProjectionInterval  = 500 * time.Millisecond
	defaultCompletionInterval  = time.Second
	projectionBatchSize        = 200
	projectionLeaseTTL         = 30 * time.Second
	attachmentClaimSweepPeriod = time.Minute
	defaultShutdownGrace       = 30 * time.Second
	localCommitWriteLeaseTTL   = 2 * time.Minute
	pollIntervalDefault        = 200 * time.Millisecond
)

// runWorker is `aw worker`'s entry point: it wires SIGINT/SIGTERM into a
// cancelable context and delegates to worker, mirroring runServe, so the
// real composition can be driven by a test-controlled context.
func runWorker(arguments []string, stdout io.Writer) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return worker(ctx, arguments, stdout)
}

// workerOptions is everything `aw worker` needs from its own flags.
type workerOptions struct {
	dbPath, artifactRoot, workspaceRoot string
	claudeExecutable, codexExecutable   string
	workerID                            string
	concurrency                         int
	leaseTTL, leaseHeartbeat            time.Duration
	pollInterval, shutdownGrace         time.Duration
	projectionInterval                  time.Duration
	completionInterval                  time.Duration
	envAllowlist                        []string
}

// worker is the production `aw worker` composition root. Until V6-14 this
// subcommand was a stub and no production code registered a single
// workerpool.Handler (every Registry.Register call lived in a test), so a
// real `aw serve` could enqueue durable jobs that nothing ever ran. worker
// registers a handler for every durable job kind, runs the pool until ctx
// is cancelled, and alongside it drives the three things that have no job of
// their own: the live projection consumer, the completion orchestrator and the
// expired-attachment-claim sweep.
func worker(ctx context.Context, arguments []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("worker", flag.ContinueOnError)
	defaults := config.Defaults()
	dbPath := flags.String("db", "", "sqlite database path (the same file `aw serve` uses)")
	artifactRoot := flags.String("artifact-root", "", "artifact storage root directory (must already exist; the same root `aw serve` uses)")
	workspaceRoot := flags.String("workspace-root", "", "root directory for Git worktree-backed workspaces (the same root `aw serve` uses)")
	claudeExecutable := flags.String("claude-executable", "", "path to the Claude CLI executable to register as an agent provider (omitted = not registered; AGENT nodes pinned to it cannot run)")
	codexExecutable := flags.String("codex-executable", "", "path to the Codex CLI executable to register as an agent provider (omitted = not registered; AGENT nodes pinned to it cannot run)")
	workerID := flags.String("worker-id", fmt.Sprintf("aw-worker-%d", os.Getpid()), "lease-owner identity for this process; must be unique among running workers")
	concurrency := flags.Int("worker-concurrency", defaults.WorkerConcurrency, "maximum jobs run at once")
	leaseTTL := flags.Duration("lease-ttl", defaults.LeaseTTL, "how long a claimed job's lease stays valid without a heartbeat")
	leaseHeartbeat := flags.Duration("lease-heartbeat", defaults.LeaseHeartbeat, "how often an in-flight job's lease is renewed; must be shorter than --lease-ttl")
	pollInterval := flags.Duration("poll-interval", pollIntervalDefault, "how long an idle worker waits before polling for a job again")
	shutdownGrace := flags.Duration("shutdown-grace", defaultShutdownGrace, "how long in-flight jobs may finish after a shutdown signal before their context is cancelled")
	projectionInterval := flags.Duration("projection-interval", defaultProjectionInterval, "how often the live projection consumer scans the event journal")
	completionInterval := flags.Duration("completion-interval", defaultCompletionInterval, "how often the completion orchestrator looks for runs waiting in VERIFYING")
	envAllowlist := flags.String("env-allowlist", "", "comma-separated names of parent environment variables a spawned provider/command process may inherit (default: none)")
	if err := flags.Parse(arguments); err != nil {
		return usageError{err}
	}
	for _, required := range []struct{ name, value string }{
		{"--db", *dbPath}, {"--artifact-root", *artifactRoot}, {"--workspace-root", *workspaceRoot},
	} {
		if strings.TrimSpace(required.value) == "" {
			return usageError{fmt.Errorf("%s is required", required.name)}
		}
	}

	assembled, err := assembleWorker(ctx, workerOptions{
		dbPath: *dbPath, artifactRoot: *artifactRoot, workspaceRoot: *workspaceRoot,
		claudeExecutable: strings.TrimSpace(*claudeExecutable), codexExecutable: strings.TrimSpace(*codexExecutable),
		workerID: *workerID, concurrency: *concurrency,
		leaseTTL: *leaseTTL, leaseHeartbeat: *leaseHeartbeat,
		pollInterval: *pollInterval, shutdownGrace: *shutdownGrace,
		projectionInterval: *projectionInterval, completionInterval: *completionInterval, envAllowlist: splitCommaList(*envAllowlist),
	})
	if err != nil {
		return err
	}
	defer assembled.close()
	return assembled.run(ctx, stdout)
}

func splitCommaList(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// assembledWorker is a fully wired, not-yet-running worker process.
type assembledWorker struct {
	opts       workerOptions
	store      *sqlite.Store
	uow        ports.UnitOfWork
	ids        idsource.Source
	clk        clock.Clock
	artifacts  ports.ArtifactStore
	registry   *workerpool.Registry
	pool       *workerpool.Pool
	catalog    *projection.Catalog
	completion *runtime.CompletionOrchestrator
	logger     *logging.Logger
}

func (w *assembledWorker) close() { _ = w.store.Close() }

// workerDeps is every collaborator buildWorkerRegistry wires into handlers.
type workerDeps struct {
	store      *sqlite.Store
	uow        ports.UnitOfWork
	ids        idsource.Source
	clk        clock.Clock
	artifacts  ports.ArtifactStore
	provider   *gitworktree.Provider
	supervisor ports.ProcessSupervisor
	secrets    ports.SecretResolver
	agents     *agentregistry.Registry
	isolation  ports.IsolationEnforcementChecker
	execConfig ports.RuntimeExecutionConfigProvider
	matcher    redact.Matcher
	events     *eventschema.Registry
	prober     ports.RepositoryProber
	catalog    *projection.Catalog
}

// assembleWorker opens the database and every adapter and wires the handler
// registry and pool. Nothing runs until (*assembledWorker).run.
func assembleWorker(ctx context.Context, opts workerOptions) (*assembledWorker, error) {
	cfg := config.Defaults()
	cfg.DatabasePath = opts.dbPath
	cfg.ArtifactRoot = opts.artifactRoot
	cfg.WorkerID = opts.workerID
	cfg.WorkerConcurrency = opts.concurrency
	cfg.LeaseTTL = opts.leaseTTL
	cfg.LeaseHeartbeat = opts.leaseHeartbeat
	cfg.EnvAllowlist = opts.envAllowlist
	if opts.claudeExecutable != "" {
		cfg.ProviderExecutables["claude"] = opts.claudeExecutable
	}
	if opts.codexExecutable != "" {
		cfg.ProviderExecutables["codex"] = opts.codexExecutable
	}
	for _, interval := range []struct {
		name  string
		value time.Duration
	}{{"--projection-interval", opts.projectionInterval}, {"--completion-interval", opts.completionInterval}} {
		if interval.value <= 0 {
			return nil, usageError{fmt.Errorf("%s must be positive", interval.name)}
		}
	}
	if err := config.Validate(cfg); err != nil {
		// config.Validate keeps the per-field problems in Details; without
		// them the operator only sees "1 config problem(s) found".
		message := err.Error()
		var appErr *apperror.Error
		if errors.As(err, &appErr) && appErr.Details["problems"] != "" {
			message += ": " + appErr.Details["problems"]
		}
		return nil, usageError{errors.New(message)}
	}
	if info, err := os.Stat(opts.artifactRoot); err != nil || !info.IsDir() {
		return nil, usageError{fmt.Errorf("--artifact-root %q is not an existing directory", opts.artifactRoot)}
	}

	store, uow, err := openDefinitionDB(ctx, opts.dbPath)
	if err != nil {
		return nil, err
	}
	built := false
	defer func() {
		if !built {
			_ = store.Close()
		}
	}()

	artifacts, err := artifactstore.New(opts.artifactRoot)
	if err != nil {
		return nil, fmt.Errorf("open artifact store: %w", err)
	}
	provider, err := gitworktree.New(gitworktree.Config{Root: opts.workspaceRoot})
	if err != nil {
		return nil, fmt.Errorf("open git workspace provider: %w", err)
	}
	prober, err := repoprobe.New(repoprobe.Config{})
	if err != nil {
		return nil, fmt.Errorf("construct repository prober: %w", err)
	}
	agents, err := newWorkerAgentRegistry(ctx, opts.claudeExecutable, opts.codexExecutable)
	if err != nil {
		return nil, err
	}

	events := eventschema.NewRegistry()
	agentevents.RegisterEventSchemas(events)
	matcher := redact.NewMatcher()

	deps := workerDeps{
		store: store, uow: uow, ids: idsource.Random{}, clk: clock.System{},
		artifacts: artifacts, provider: provider, supervisor: process.NewSupervisor(),
		secrets: secretenv.NewResolver(), agents: agents, isolation: process.NewIsolationChecker(),
		execConfig: process.NewRuntimeExecutionConfigProvider(cfg), matcher: matcher, events: events,
		prober: prober, catalog: projection.NewCatalog(),
	}
	registry := buildWorkerRegistry(deps)

	pool, err := workerpool.New(store, registry, workerpool.Config{
		Concurrency: opts.concurrency, Owner: opts.workerID, LeaseTTL: opts.leaseTTL,
		HeartbeatEvery: opts.leaseHeartbeat, PollInterval: opts.pollInterval, ShutdownGrace: opts.shutdownGrace,
	})
	if err != nil {
		return nil, fmt.Errorf("construct worker pool: %w", err)
	}

	built = true
	return &assembledWorker{
		opts: opts, store: store, uow: uow, ids: deps.ids, clk: deps.clk, artifacts: artifacts,
		registry: registry, pool: pool, catalog: deps.catalog,
		completion: runtime.NewCompletionOrchestrator(uow, deps.ids, deps.clk),
		logger:     logging.New(os.Stderr, logging.JSON, matcher),
	}, nil
}

// newWorkerAgentRegistry builds the live agent-provider registry the same
// way `aw serve` does (see serve.go): a provider is registered only when the
// operator names its executable, and zero executors is the documented-safe
// empty registry.
func newWorkerAgentRegistry(ctx context.Context, claudeExecutable, codexExecutable string) (*agentregistry.Registry, error) {
	var executors []ports.AgentExecutor
	for _, entry := range []struct{ provider, executable string }{
		{string(ports.ProviderClaude), claudeExecutable},
		{string(ports.ProviderCodex), codexExecutable},
	} {
		if strings.TrimSpace(entry.executable) == "" {
			continue
		}
		executor, err := newAgentExecutor(entry.provider, entry.executable)
		if err != nil {
			return nil, fmt.Errorf("construct %s agent executor: %w", entry.provider, err)
		}
		executors = append(executors, executor)
	}
	registry, err := agentregistry.New(ctx, executors...)
	if err != nil {
		return nil, fmt.Errorf("build agent executor registry: %w", err)
	}
	return registry, nil
}

// buildWorkerRegistry registers one handler for every durable job kind.
// worker_test.go asserts the registered set equals the set of every
// `...JobKind` constant declared under internal/app, so a job kind added
// later without a handler here fails a test instead of silently never
// running.
func buildWorkerRegistry(d workerDeps) *workerpool.Registry {
	router := &runtime.NodeExecutorRouter{
		Agent: runtime.NewAgentNodeExecutor(
			d.uow, d.ids, d.artifacts, d.provider, d.store, d.agents, d.events, d.matcher, d.store, d.clk, d.store, d.store),
		Command: runtime.NewCommandNodeExecutor(
			d.uow, d.ids, d.artifacts, d.provider, d.store, d.supervisor, d.secrets, d.events, d.matcher, d.store, d.clk, d.store, d.store),
		Gate: runtime.NewGateNodeExecutor(
			d.uow, d.ids, d.artifacts, d.provider, d.supervisor, d.secrets, d.events, d.matcher, d.store, d.clk, d.store, d.store),
	}

	registry := workerpool.NewRegistry()
	// Run execution.
	registry.Register(runtime.AdvanceRunJobKind, runtime.NewScheduler(d.uow, d.ids))
	registry.Register(runtime.ScheduleNodeRunJobKind, runtime.NewNodeSchedulingHandler(d.uow, d.ids, d.execConfig))
	registry.Register(runtime.ExecuteNodeJobKind, runtime.NewExecuteNodeHandler(d.uow, d.ids, router, d.clk, d.isolation, d.agents, d.store))
	registry.Register(runtime.WaitTimerJobKind, runtime.NewWaitTimeoutHandler(d.uow, d.ids))
	registry.Register(runtime.ApprovalTimerJobKind, runtime.NewApprovalTimeoutHandler(d.uow, d.ids))
	registry.Register(runtime.RequestScopeExpansionJobKind, runtime.NewRequestScopeExpansionHandler(d.uow, d.ids))
	registry.Register(appwork.ScopeExpansionReconcileJobKind, runtime.NewScopeExpansionReconcileHandler(d.uow, d.ids))
	registry.Register(runtime.CancelRunCoordinatorJobKind, runtime.NewCancelRunCoordinatorHandler(d.uow, d.ids))
	registry.Register(runtime.RecoveryReaperJobKind, runtime.NewRecoveryReaperHandler(d.uow, d.ids, d.clk, d.store, d.store, d.store))
	// Catalog and workspace.
	registry.Register(catalog.RepositoryProbeJobKind, repositoryprobe.New(d.uow, d.ids, d.prober))
	registry.Register(readinesscheck.BaselineEvidenceJobKind, readinesscheck.New(d.uow, d.ids, d.supervisor, d.provider))
	registry.Register(appwork.WorkspaceProvisionJobKind, workspaceprovision.New(d.uow, d.ids, d.provider))
	registry.Register(workspacereconcile.WorkspaceReconciliationJobKind, workspacereconcile.New(d.uow, d.ids, d.provider, d.store))
	registry.Register(workspacerelease.WorkspaceSetReleaseJobKind, workspacerelease.NewHandler(d.uow, d.ids, d.provider, d.store))
	// Release and projection.
	registry.Register(releasesetcommit.ReleaseSetLocalCommitJobKind, releasesetcommit.NewHandler(releasesetcommit.ExecuteReleaseSetLocalCommitDeps{
		UnitOfWork: d.uow, IDs: d.ids, WriteLeases: d.store, MarkerReader: d.provider, Creator: d.provider,
		Lifecycle: d.store, WriteLeaseTTL: localCommitWriteLeaseTTL,
	}))
	registry.Register(projectionrebuild.ProjectionRebuildJobKind, projectionrebuildworker.NewHandler(projectionrebuildworker.Deps{
		UnitOfWork: d.uow, IDs: d.ids, Catalog: d.catalog,
	}))
	// Retention.
	registry.Register(artifactsweep.ArtifactSweepJobKind, artifactsweep.NewHandler(d.uow, d.ids, d.clk, d.artifacts))
	return registry
}

// run enqueues the two self-rescheduling control jobs, starts the two
// background loops, announces readiness on stdout, and blocks in the pool
// until ctx is cancelled.
func (w *assembledWorker) run(ctx context.Context, stdout io.Writer) error {
	if err := runtime.StartupRecoveryScan(ctx, w.uow, w.ids); err != nil {
		return fmt.Errorf("startup recovery scan: %w", err)
	}
	if err := artifactsweep.StartupArtifactSweep(ctx, w.uow, w.ids); err != nil {
		return fmt.Errorf("startup artifact sweep: %w", err)
	}

	loopCtx, cancelLoops := context.WithCancel(ctx)
	var loops sync.WaitGroup
	loops.Add(3)
	go func() { defer loops.Done(); w.driveProjections(loopCtx) }()
	go func() { defer loops.Done(); w.sweepAttachmentClaims(loopCtx) }()
	go func() { defer loops.Done(); w.driveCompletion(loopCtx) }()

	fmt.Fprintf(stdout, "{\"workerId\":%q}\n", w.opts.workerID)
	err := w.pool.Run(ctx)
	cancelLoops()
	loops.Wait()
	return err
}

func (w *assembledWorker) identity() logging.Identity {
	return logging.Identity{ActorType: "worker", ActorID: w.opts.workerID}
}

// driveProjections is the live projection consumer's production driver.
// projection.ApplyBatch applies one atomic batch for one (project,
// projection) but nothing had ever called it outside tests, so a real
// installation's kanban and work-item detail read models would have stayed
// empty. Each tick drains every active project until it is caught up.
// ports.ErrOptimisticConflict means another consumer (or a rebuild) holds the
// lease, which is normal and simply retried on the next tick.
func (w *assembledWorker) driveProjections(ctx context.Context) {
	ticker := time.NewTicker(w.opts.projectionInterval)
	defer ticker.Stop()
	for {
		w.projectAllActiveProjects(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *assembledWorker) projectAllActiveProjects(ctx context.Context) {
	var projects []project.Project
	if err := w.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		projects, err = tx.Catalog().ListProjects(ctx)
		return err
	}); err != nil {
		if ctx.Err() == nil {
			_ = w.logger.Warn(w.identity(), "projection consumer: list projects failed", map[string]any{"error": err.Error()})
		}
		return
	}
	for _, p := range projects {
		if p.Status != project.ProjectActive {
			continue
		}
		for ctx.Err() == nil {
			outcome, err := projection.ApplyBatch(ctx, w.uow, w.catalog, projection.ApplyBatchRequest{
				ProjectID: string(p.ID), ProjectionName: projection.ProjectionName, Owner: w.opts.workerID,
				TTL: projectionLeaseTTL, BatchSize: projectionBatchSize, Now: w.clk.Now(), IDs: w.ids,
			})
			if err != nil {
				if !errors.Is(err, ports.ErrOptimisticConflict) && ctx.Err() == nil {
					_ = w.logger.Warn(w.identity(), "projection consumer: apply batch failed",
						map[string]any{"projectId": string(p.ID), "error": err.Error()})
				}
				break
			}
			if outcome.Poisoned {
				_ = w.logger.Error(w.identity(), "PROJECTION_POISON", "projection consumer: poison event",
					map[string]any{"projectId": string(p.ID), "reason": outcome.PoisonReason})
				break
			}
			if outcome.EventsScanned < projectionBatchSize {
				break
			}
		}
	}
}

// sweepAttachmentClaims resumes or cleans attachment claims a crashed ingest
// left behind (message.ResumeOrCleanExpiredAttachmentClaims had no caller).
func (w *assembledWorker) sweepAttachmentClaims(ctx context.Context) {
	ticker := time.NewTicker(attachmentClaimSweepPeriod)
	defer ticker.Stop()
	for {
		report, err := message.ResumeOrCleanExpiredAttachmentClaims(ctx, w.uow, w.artifacts, w.clk)
		switch {
		case err != nil && ctx.Err() == nil:
			_ = w.logger.Warn(w.identity(), "attachment claim sweep failed", map[string]any{"error": err.Error()})
		case err == nil && (report.Released > 0 || report.Purged > 0):
			_ = w.logger.Info(w.identity(), "attachment claim sweep", map[string]any{"released": report.Released, "purged": report.Purged})
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// driveCompletion is the completion orchestrator's production driver.
// A WorkflowRun that reaches END waits in VERIFYING, and
// runtime.EvaluateCompletionCandidate is the only thing that may move it on,
// but no route, CLI verb or job ever called it, so no run could complete.
// Each tick decides every VERIFYING run once; the decision is idempotent, so
// two workers sweeping the same run cannot disagree.
func (w *assembledWorker) driveCompletion(ctx context.Context) {
	ticker := time.NewTicker(w.opts.completionInterval)
	defer ticker.Stop()
	for {
		report, err := w.completion.Sweep(ctx)
		if err != nil && ctx.Err() == nil {
			_ = w.logger.Warn(w.identity(), "completion orchestrator: sweep failed", map[string]any{"error": err.Error()})
		}
		if report.Evaluated > 0 {
			_ = w.logger.Info(w.identity(), "completion orchestrator: decided completion candidates",
				map[string]any{"evaluated": report.Evaluated, "outcomes": report.Outcomes})
		}
		for _, nodeRunID := range report.ReworkNodeRunIDs {
			_ = w.logger.Warn(w.identity(), "completion orchestrator: REWORK created a node run that nothing schedules yet (known V5-11 gap)",
				map[string]any{"nodeRunId": nodeRunID})
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
