package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/logging"
)

// Server is a production loopback HTTP server: it refuses to bind
// anything but a loopback address (ADR-016's "external bind phải bị từ
// chối, không chỉ off-by-default"), composes every registered route
// behind the shared correlation-ID/panic-recovery/body-limit middleware
// chain, and exposes Serve/Shutdown as the two lifecycle primitives a
// composition root orchestrates (signal handling itself is the
// composition root's own job, not this package's — V6-01's own "Không
// làm" keeps this package free of any policy about when to stop, only
// how).
type Server struct {
	httpServer *http.Server
	listener   net.Listener
}

// Config is what NewServer needs to compose the listener and middleware
// chain. MaxBodyBytes bounds every request body this server accepts
// (before any handler-specific limit); Logger may be nil (Recover then
// simply does not log a caught panic, it still recovers).
//
// Token and Principal are V6-01A's own browser-security additions
// (docs/design/08-v6-api-projections.md V6-01A, ADR-016, ADR-028). Both are
// required, and both are bound to this Server for its entire process
// lifetime: Token must be generated fresh per process start (a composition
// root calls IDs.NewID() itself, once, before constructing Config — NOT
// something NewServer derives on its own, so a caller can register the one
// bootstrap route that needs to embed it before Serve ever accepts a
// connection) and Principal must come from trusted startup config, never
// from a request. Neither has a setter anywhere in this package — the only
// way to change either is a fresh NewServer call, i.e. a process restart.
type Config struct {
	Host         string
	Port         int
	Routes       *RouteRegistry
	IDs          idsource.Source
	Logger       *logging.Logger
	MaxBodyBytes int64
	Token        string
	Principal    LocalPrincipalSnapshot
}

// NewServer validates cfg, binds the listener and composes the handler
// chain. It does not start serving — call Serve for that — so a
// composition root can log/report the bound address (Addr()) before
// accepting its first connection.
func NewServer(cfg Config) (*Server, error) {
	if err := validateLoopbackHost(cfg.Host); err != nil {
		return nil, err
	}
	if cfg.Routes == nil {
		return nil, errors.New("httpapi: Config.Routes is required")
	}
	if cfg.IDs == nil {
		return nil, errors.New("httpapi: Config.IDs is required")
	}
	if cfg.MaxBodyBytes <= 0 {
		return nil, errors.New("httpapi: Config.MaxBodyBytes must be positive")
	}
	if cfg.Token == "" {
		return nil, errors.New("httpapi: Config.Token is required (a fresh per-start session token — ADR-016)")
	}
	if err := cfg.Principal.validate(); err != nil {
		return nil, fmt.Errorf("httpapi: %w", err)
	}

	mux := http.NewServeMux()
	for _, d := range cfg.Routes.Descriptors() {
		mux.HandleFunc(d.Method+" "+d.Path, d.Handler)
	}

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("httpapi: listen %s: %w", addr, err)
	}

	// The Host/Origin a real request must match is derived from cfg.Host
	// (the literal string an operator configured — "127.0.0.1", "localhost"
	// or "::1", already proven loopback-only above) joined with the actual
	// bound port, not cfg.Port itself: Config.Port:0 means "OS-assigned
	// ephemeral port", so only the listener's own resolved address knows the
	// real port a request's Host header will name.
	_, boundPort, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("httpapi: parse bound address %s: %w", listener.Addr().String(), err)
	}
	local := LocalOrigin{
		Host:   net.JoinHostPort(cfg.Host, boundPort),
		Origin: "http://" + net.JoinHostPort(cfg.Host, boundPort),
	}

	handler := Chain(mux,
		HostOriginGuard(local),
		CorrelationID(cfg.IDs),
		Recover(cfg.Logger),
		RequireSessionToken(cfg.Token),
		MaxBytes(cfg.MaxBodyBytes),
		BindPrincipal(cfg.Principal),
	)

	return &Server{
		httpServer: &http.Server{Handler: handler},
		listener:   listener,
	}, nil
}

// Addr returns the actual bound address (useful when Config.Port is 0 and
// the OS assigned an ephemeral port).
func (s *Server) Addr() string {
	return s.listener.Addr().String()
}

// Serve blocks, accepting connections until Shutdown is called (or the
// listener otherwise fails). A clean shutdown (via Shutdown) is reported
// as a nil error, not http.ErrServerClosed, so a caller's own error
// handling never has to special-case that sentinel.
func (s *Server) Serve() error {
	err := s.httpServer.Serve(s.listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown gracefully stops the server: it stops accepting new
// connections and waits for in-flight requests to finish, or for ctx to
// be done, whichever comes first (V6-01's own "graceful shutdown" scope
// line).
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// validateLoopbackHost proves host can only ever resolve to a loopback
// address before net.Listen ever runs — ADR-016's "external bind phải bị
// từ chối" as a real check, not an assumption. A literal IP is checked
// directly; a hostname (including "localhost") is resolved and every
// returned address must be loopback.
func validateLoopbackHost(host string) error {
	if host == "" {
		return errors.New("httpapi: bind host is required")
	}
	if ip := net.ParseIP(host); ip != nil {
		if !ip.IsLoopback() {
			return fmt.Errorf("httpapi: bind host %q is not a loopback address — refusing external bind (ADR-016)", host)
		}
		return nil
	}
	addrs, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("httpapi: resolve bind host %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("httpapi: bind host %q resolved to no addresses", host)
	}
	for _, addr := range addrs {
		if !addr.IsLoopback() {
			return fmt.Errorf("httpapi: bind host %q resolves to non-loopback address %s — refusing external bind (ADR-016)", host, addr)
		}
	}
	return nil
}
