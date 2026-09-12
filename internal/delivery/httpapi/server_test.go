package httpapi_test

import (
	"context"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/logging"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// testPrincipal is the LocalPrincipalSnapshot every test in this package
// uses unless it is specifically exercising principal validation/spoofing —
// an arbitrary-but-valid stand-in for ADR-028's real "local-operator"/
// "operator" default.
func testPrincipal() httpapi.LocalPrincipalSnapshot {
	return httpapi.LocalPrincipalSnapshot{Actor: "local-operator", Roles: []string{"operator"}}
}

func newTestServer(t *testing.T, routes *httpapi.RouteRegistry) *httpapi.Server {
	t.Helper()
	server, err := httpapi.NewServer(httpapi.Config{
		Host:         "127.0.0.1",
		Port:         0,
		Routes:       routes,
		IDs:          idsource.Random{},
		Logger:       logging.New(io.Discard, logging.JSON, redact.NewMatcher()),
		MaxBodyBytes: 1 << 20,
		Token:        "test-session-token",
		Principal:    testPrincipal(),
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return server
}

func TestNewServer_RejectsNonLoopbackHost(t *testing.T) {
	registry := httpapi.NewRouteRegistry()
	_, err := httpapi.NewServer(httpapi.Config{
		Host:         "0.0.0.0",
		Port:         0,
		Routes:       registry,
		IDs:          idsource.Random{},
		MaxBodyBytes: 1024,
	})
	if err == nil {
		t.Fatal("NewServer with host 0.0.0.0 should be rejected (ADR-016 external bind refusal)")
	}
}

func TestNewServer_RejectsExternalHostname(t *testing.T) {
	registry := httpapi.NewRouteRegistry()
	_, err := httpapi.NewServer(httpapi.Config{
		Host:         "example.com",
		Port:         0,
		Routes:       registry,
		IDs:          idsource.Random{},
		MaxBodyBytes: 1024,
	})
	if err == nil {
		t.Fatal("NewServer with a public hostname should be rejected")
	}
}

func TestNewServer_AcceptsLoopbackIPAndLocalhost(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "localhost", "::1"} {
		t.Run(host, func(t *testing.T) {
			registry := httpapi.NewRouteRegistry()
			server, err := httpapi.NewServer(httpapi.Config{
				Host:         host,
				Port:         0,
				Routes:       registry,
				IDs:          idsource.Random{},
				MaxBodyBytes: 1024,
				Token:        "test-session-token",
				Principal:    testPrincipal(),
			})
			if err != nil {
				t.Fatalf("NewServer(%s): %v", host, err)
			}
			defer server.Shutdown(context.Background())
		})
	}
}

func TestServer_ServeHealthEndpoints_RealHTTPRoundTrip(t *testing.T) {
	registry := httpapi.NewRouteRegistry()
	checker := httpapi.NewReadinessChecker()
	checker.Register("always-ready", func(ctx context.Context) error { return nil })
	registry.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/health/live", OperationID: "healthLive",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: struct{}{}, ResponseSchema: struct{}{},
		Handler: httpapi.LiveHandler(),
	})
	registry.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/health/ready", OperationID: "healthReady",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: struct{}{}, ResponseSchema: struct{}{},
		Handler: checker.ReadyHandler(),
	})

	server := newTestServer(t, registry)
	done := make(chan error, 1)
	go func() { done <- server.Serve() }()
	defer func() {
		if err := server.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
		if err := <-done; err != nil {
			t.Errorf("Serve returned error after Shutdown: %v", err)
		}
	}()

	client := &http.Client{Timeout: 5 * time.Second}
	base := "http://" + server.Addr()

	liveResp, err := client.Get(base + "/health/live")
	if err != nil {
		t.Fatalf("GET /health/live: %v", err)
	}
	defer liveResp.Body.Close()
	if liveResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /health/live status = %d, want 200", liveResp.StatusCode)
	}
	if got := liveResp.Header.Get(httpapi.CorrelationIDHeader); got == "" {
		t.Fatal("response missing correlation ID header")
	}

	readyResp, err := client.Get(base + "/health/ready")
	if err != nil {
		t.Fatalf("GET /health/ready: %v", err)
	}
	defer readyResp.Body.Close()
	if readyResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /health/ready status = %d, want 200", readyResp.StatusCode)
	}
}

func TestServer_UnregisteredPathReturns404(t *testing.T) {
	registry := httpapi.NewRouteRegistry()
	server := newTestServer(t, registry)
	done := make(chan error, 1)
	go func() { done <- server.Serve() }()
	defer func() {
		server.Shutdown(context.Background())
		<-done
	}()

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + server.Addr() + "/does-not-exist")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestServer_PanicInHandlerReturns500NotCrash(t *testing.T) {
	registry := httpapi.NewRouteRegistry()
	registry.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/boom", OperationID: "boom",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: struct{}{}, ResponseSchema: struct{}{},
		Handler: func(w http.ResponseWriter, r *http.Request) { panic("kaboom") },
	})
	server := newTestServer(t, registry)
	done := make(chan error, 1)
	go func() { done <- server.Serve() }()
	defer func() {
		server.Shutdown(context.Background())
		<-done
	}()

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + server.Addr() + "/boom")
	if err != nil {
		t.Fatalf("GET /boom: %v (server should survive a handler panic)", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}

	// The server must still be alive for a second, unrelated request.
	resp2, err := client.Get("http://" + server.Addr() + "/does-not-exist")
	if err != nil {
		t.Fatalf("server did not survive the earlier panic: %v", err)
	}
	defer resp2.Body.Close()
}

func TestServer_RequestCancellation_HandlerObservesContextDone(t *testing.T) {
	registry := httpapi.NewRouteRegistry()
	handlerSawCancel := make(chan bool, 1)
	registry.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/slow", OperationID: "slow",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: struct{}{}, ResponseSchema: struct{}{},
		Handler: func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
				handlerSawCancel <- true
			case <-time.After(5 * time.Second):
				handlerSawCancel <- false
			}
		},
	})
	server := newTestServer(t, registry)
	done := make(chan error, 1)
	go func() { done <- server.Serve() }()
	defer func() {
		server.Shutdown(context.Background())
		<-done
	}()

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+server.Addr()+"/slow", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}

	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	client := &http.Client{}
	_, err = client.Do(req)
	if err == nil {
		t.Fatal("expected client.Do to fail after cancellation")
	}

	select {
	case saw := <-handlerSawCancel:
		if !saw {
			t.Fatal("handler timed out instead of observing context cancellation")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handler never returned after cancellation")
	}
}

func TestServer_GracefulShutdown_WaitsForInFlightRequest(t *testing.T) {
	registry := httpapi.NewRouteRegistry()
	handlerStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	registry.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/inflight", OperationID: "inflight",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: struct{}{}, ResponseSchema: struct{}{},
		Handler: func(w http.ResponseWriter, r *http.Request) {
			close(handlerStarted)
			<-releaseHandler
			w.WriteHeader(http.StatusOK)
		},
	})
	server := newTestServer(t, registry)
	done := make(chan error, 1)
	go func() { done <- server.Serve() }()

	var wg sync.WaitGroup
	wg.Add(1)
	var clientErr error
	go func() {
		defer wg.Done()
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get("http://" + server.Addr() + "/inflight")
		clientErr = err
		if resp != nil {
			resp.Body.Close()
		}
	}()

	<-handlerStarted // request is now in flight

	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- server.Shutdown(context.Background())
	}()

	// Give Shutdown a moment to start waiting, then release the handler —
	// Shutdown must not return before this in-flight request completes.
	time.Sleep(100 * time.Millisecond)
	close(releaseHandler)

	if err := <-shutdownDone; err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	wg.Wait()
	if clientErr != nil {
		t.Fatalf("in-flight request failed instead of completing before shutdown: %v", clientErr)
	}
	if err := <-done; err != nil {
		t.Fatalf("Serve returned error: %v", err)
	}
}

func TestServer_DuplicateRouteRegistration_FailsAtComposition(t *testing.T) {
	registry := httpapi.NewRouteRegistry()
	descriptor := httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/dup", OperationID: "dup",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: struct{}{}, ResponseSchema: struct{}{},
		Handler: func(w http.ResponseWriter, r *http.Request) {},
	}
	registry.Register(descriptor)

	defer func() {
		if recover() == nil {
			t.Fatal("registering the same route twice should panic before NewServer is ever reached")
		}
	}()
	registry.Register(descriptor)
	_, _ = httpapi.NewServer(httpapi.Config{Host: "127.0.0.1", Routes: registry, IDs: idsource.Random{}, MaxBodyBytes: 1024})
}

func TestNewServer_RequiresRoutesIDsAndPositiveMaxBody(t *testing.T) {
	valid := httpapi.Config{Host: "127.0.0.1", Routes: httpapi.NewRouteRegistry(), IDs: idsource.Random{}, MaxBodyBytes: 1024}

	missingRoutes := valid
	missingRoutes.Routes = nil
	if _, err := httpapi.NewServer(missingRoutes); err == nil {
		t.Fatal("NewServer without Routes should fail")
	}

	missingIDs := valid
	missingIDs.IDs = nil
	if _, err := httpapi.NewServer(missingIDs); err == nil {
		t.Fatal("NewServer without IDs should fail")
	}

	zeroMaxBody := valid
	zeroMaxBody.MaxBodyBytes = 0
	if _, err := httpapi.NewServer(zeroMaxBody); err == nil {
		t.Fatal("NewServer with MaxBodyBytes <= 0 should fail")
	}
}

// TestNewServer_RequiresTokenAndPrincipal is V6-01A's own extension of the
// same "programming error caught at boot" discipline
// TestNewServer_RequiresRoutesIDsAndPositiveMaxBody already covers for
// V6-01's fields: a Server with no per-start session token, or an
// incomplete/empty LocalPrincipalSnapshot, must never start — ADR-016/
// ADR-028 both treat these as required trust-boundary inputs, not optional
// ones with a silent zero-value fallback.
func TestNewServer_RequiresTokenAndPrincipal(t *testing.T) {
	valid := httpapi.Config{
		Host: "127.0.0.1", Routes: httpapi.NewRouteRegistry(), IDs: idsource.Random{},
		MaxBodyBytes: 1024, Token: "test-session-token", Principal: testPrincipal(),
	}
	validServer, err := httpapi.NewServer(valid)
	if err != nil {
		t.Fatalf("NewServer(valid) = %v, want success", err)
	}
	defer validServer.Shutdown(context.Background())

	missingToken := valid
	missingToken.Token = ""
	if _, err := httpapi.NewServer(missingToken); err == nil {
		t.Fatal("NewServer without Token should fail")
	}

	missingActor := valid
	missingActor.Principal = httpapi.LocalPrincipalSnapshot{Actor: "", Roles: []string{"operator"}}
	if _, err := httpapi.NewServer(missingActor); err == nil {
		t.Fatal("NewServer with an empty Principal.Actor should fail")
	}

	missingRoles := valid
	missingRoles.Principal = httpapi.LocalPrincipalSnapshot{Actor: "local-operator", Roles: nil}
	if _, err := httpapi.NewServer(missingRoles); err == nil {
		t.Fatal("NewServer with empty Principal.Roles should fail")
	}

	blankRole := valid
	blankRole.Principal = httpapi.LocalPrincipalSnapshot{Actor: "local-operator", Roles: []string{""}}
	if _, err := httpapi.NewServer(blankRole); err == nil {
		t.Fatal("NewServer with a blank role should fail")
	}

	dupRoles := valid
	dupRoles.Principal = httpapi.LocalPrincipalSnapshot{Actor: "local-operator", Roles: []string{"operator", "operator"}}
	if _, err := httpapi.NewServer(dupRoles); err == nil {
		t.Fatal("NewServer with a duplicate role should fail")
	}
}
