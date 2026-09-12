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

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/logging"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
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

// serve is V6-01's own composition root: it wires config/DB/UnitOfWork/
// artifact-root readiness and the two health routes into a real
// httpapi.Server, then serves until ctx is done, at which point it
// gracefully shuts down. No business endpoint is registered here — this
// task's own "Không làm" line reserves that for the endpoint tasks that
// come after V6-01A/V6-02/V6-02A.
func serve(ctx context.Context, arguments []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	dbPath := flags.String("db", "", "sqlite database path")
	artifactRoot := flags.String("artifact-root", "", "artifact storage root directory")
	host := flags.String("host", "127.0.0.1", "loopback bind host")
	port := flags.Int("port", 0, "bind port (0 = OS-assigned ephemeral port)")
	maxBodyBytes := flags.Int64("max-body-bytes", 1<<20, "maximum accepted request body size in bytes")
	if err := flags.Parse(arguments); err != nil {
		return usageError{err}
	}
	if strings.TrimSpace(*dbPath) == "" {
		return usageError{errors.New("--db is required")}
	}
	if strings.TrimSpace(*artifactRoot) == "" {
		return usageError{errors.New("--artifact-root is required")}
	}

	store, uow, err := openDefinitionDB(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer store.Close()

	if info, statErr := os.Stat(*artifactRoot); statErr != nil || !info.IsDir() {
		return fmt.Errorf("--artifact-root %q is not an existing directory", *artifactRoot)
	}

	logger := logging.New(os.Stderr, logging.JSON, redact.NewMatcher())

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
	// No further route fragments exist yet in this task; a later endpoint
	// task's own composition-root wiring adds its own routes.Register call
	// here without needing to touch this file's shared setup.
	routesFinalized = true

	server, err := httpapi.NewServer(httpapi.Config{
		Host: *host, Port: *port, Routes: routes, IDs: idsource.Random{},
		Logger: logger, MaxBodyBytes: *maxBodyBytes,
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
