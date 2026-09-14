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

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/logging"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	httpcatalog "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/catalog"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/decision"
	runhttp "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/run"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/workitem"
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
	host := flags.String("host", "127.0.0.1", "loopback bind host")
	port := flags.Int("port", 0, "bind port (0 = OS-assigned ephemeral port)")
	maxBodyBytes := flags.Int64("max-body-bytes", 1<<20, "maximum accepted request body size in bytes")
	principalConfigPath := flags.String("principal-config", "", "path to a trusted JSON config file's localPrincipal.actor/localPrincipal.roles (ADR-028); omitted or missing means the local-operator/[operator] default — this is the only allowed way to select a principal, there is no --actor/--role flag")
	if err := flags.Parse(arguments); err != nil {
		return usageError{err}
	}
	if strings.TrimSpace(*dbPath) == "" {
		return usageError{errors.New("--db is required")}
	}
	if strings.TrimSpace(*artifactRoot) == "" {
		return usageError{errors.New("--artifact-root is required")}
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

	// sessionToken is registered as a known secret with the shared redactor
	// as defense-in-depth: ADR-016 forbids ever logging it, and this ensures
	// that even a future logging call some other code path mistakenly adds
	// still cannot emit it verbatim.
	logger := logging.New(os.Stderr, logging.JSON, redact.NewMatcher(sessionToken))

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
