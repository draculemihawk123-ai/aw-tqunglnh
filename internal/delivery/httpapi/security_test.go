package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/logging"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// securityTestServer wires a Server with one GET route (safe method, no
// token required) and one POST route (mutating, requires the session
// token) plus a route that echoes the request's own bound
// LocalPrincipalSnapshot back as JSON — the shared fixture every test below
// drives with a real HTTP round trip against a real bound listener.
type securityTestServer struct {
	server    *httpapi.Server
	token     string
	principal httpapi.LocalPrincipalSnapshot
	logBuf    *syncBuf
}

// syncBuf is a mutex-guarded bytes.Buffer: the logger writes from the
// server's own goroutine while a test reads it concurrently, exactly the
// same real DATA RACE class server_test.go's own syncBuffer comment
// documents finding in this package's sibling composition-root test.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func newSyncBuf() *syncBuf { return &syncBuf{} }

func (b *syncBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newSecurityTestServer(t *testing.T, principal httpapi.LocalPrincipalSnapshot) *securityTestServer {
	t.Helper()
	token := "test-session-token-" + idsource.Random{}.NewID()
	logBuf := newSyncBuf()
	logger := logging.New(logBuf, logging.JSON, redact.NewMatcher())

	routes := httpapi.NewRouteRegistry()
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/echo-principal", OperationID: "echoPrincipalGet",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: struct{}{}, ResponseSchema: struct{}{},
		Handler: echoPrincipalHandler,
	})
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/mutate", OperationID: "mutate",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: struct{}{}, ResponseSchema: struct{}{},
		Handler: echoPrincipalHandler,
	})
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/", OperationID: "bootstrap",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: struct{}{}, ResponseSchema: struct{}{},
		Handler: httpapi.BootstrapHandler(token, principal, idsource.Random{}),
	})

	server, err := httpapi.NewServer(httpapi.Config{
		Host: "127.0.0.1", Port: 0, Routes: routes, IDs: idsource.Random{},
		Logger: logger, MaxBodyBytes: 1 << 20, Token: token, Principal: principal,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve() }()
	t.Cleanup(func() {
		server.Shutdown(context.Background())
		<-done
	})
	return &securityTestServer{server: server, token: token, principal: principal, logBuf: logBuf}
}

// echoPrincipalHandler reads whatever attacker-controlled data a request
// carries (a JSON body claiming its own actor/roles, and an X-Actor header)
// and discards it — it echoes back ONLY httpapi.PrincipalFromContext, which
// is the server's own bound value BindPrincipal attached, never anything
// derived from r itself. This is the fixture TestActorRoleSpoof_Ignored
// uses to prove a caller cannot influence what a handler observes.
func echoPrincipalHandler(w http.ResponseWriter, r *http.Request) {
	_, _ = io.ReadAll(r.Body) // drain/ignore any spoofed body the caller sent
	p := httpapi.PrincipalFromContext(r.Context())
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"actor": p.Actor, "roles": p.Roles})
}

func defaultTestPrincipal() httpapi.LocalPrincipalSnapshot {
	return httpapi.LocalPrincipalSnapshot{Actor: "local-operator", Roles: []string{"operator"}}
}

// --- Host validation (DNS rebinding) -------------------------------------

func TestHostOriginGuard_RejectsForeignHost(t *testing.T) {
	ts := newSecurityTestServer(t, defaultTestPrincipal())
	req, err := http.NewRequest(http.MethodGet, "http://"+ts.server.Addr()+"/echo-principal", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	// Simulate DNS rebinding: the TCP peer is really 127.0.0.1 (that's how
	// the client connects), but the request claims a different Host, exactly
	// as a browser would after an attacker rebound a hostname's DNS record
	// to point at the loopback server.
	req.Host = "evil.example.com:1"
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a rebound/foreign Host header", resp.StatusCode)
	}
}

func TestHostOriginGuard_RejectsHostnameMismatchEvenWhenBothLoopback(t *testing.T) {
	// "exact loopback Host/port" means exact — "localhost" is not
	// interchangeable with "127.0.0.1" even though both are loopback.
	ts := newSecurityTestServer(t, defaultTestPrincipal())
	_, port, _ := splitHostPort(t, ts.server.Addr())
	req, err := http.NewRequest(http.MethodGet, "http://"+ts.server.Addr()+"/echo-principal", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Host = "localhost:" + port
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for Host=localhost when bound to 127.0.0.1", resp.StatusCode)
	}
}

func TestHostOriginGuard_AcceptsExactBoundHost(t *testing.T) {
	ts := newSecurityTestServer(t, defaultTestPrincipal())
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + ts.server.Addr() + "/echo-principal")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 for the server's own exact bound Host", resp.StatusCode)
	}
}

// --- Origin validation ----------------------------------------------------

func TestHostOriginGuard_RejectsForeignOrigin(t *testing.T) {
	ts := newSecurityTestServer(t, defaultTestPrincipal())
	req, err := http.NewRequest(http.MethodGet, "http://"+ts.server.Addr()+"/echo-principal", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Origin", "http://evil.example.com")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a foreign Origin", resp.StatusCode)
	}
}

func TestHostOriginGuard_RejectsRightHostWrongPortOrigin(t *testing.T) {
	ts := newSecurityTestServer(t, defaultTestPrincipal())
	host, _, _ := splitHostPort(t, ts.server.Addr())
	req, err := http.NewRequest(http.MethodGet, "http://"+ts.server.Addr()+"/echo-principal", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Origin", "http://"+host+":1")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a right-host-wrong-port Origin", resp.StatusCode)
	}
}

func TestHostOriginGuard_MissingOriginAllowed(t *testing.T) {
	// A plain top-level navigation (or a non-browser client) legitimately
	// sends no Origin at all; HostOriginGuard must not require one.
	ts := newSecurityTestServer(t, defaultTestPrincipal())
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + ts.server.Addr() + "/echo-principal")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 when Origin is absent", resp.StatusCode)
	}
}

func TestHostOriginGuard_AcceptsExactMatchingOrigin(t *testing.T) {
	ts := newSecurityTestServer(t, defaultTestPrincipal())
	req, err := http.NewRequest(http.MethodGet, "http://"+ts.server.Addr()+"/echo-principal", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Origin", "http://"+ts.server.Addr())
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 for the server's own exact Origin", resp.StatusCode)
	}
}

// --- CORS deny-by-default --------------------------------------------------

func TestCORS_NeverEmitsAccessControlAllowOriginHeader(t *testing.T) {
	ts := newSecurityTestServer(t, defaultTestPrincipal())
	client := &http.Client{Timeout: 5 * time.Second}

	// A same-origin request that succeeds...
	okReq, _ := http.NewRequest(http.MethodGet, "http://"+ts.server.Addr()+"/echo-principal", nil)
	okReq.Header.Set("Origin", "http://"+ts.server.Addr())
	okResp, err := client.Do(okReq)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer okResp.Body.Close()
	if got := okResp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q on a successful response, want none (deny-by-default, never reflect Origin)", got)
	}

	// ...and a cross-origin preflight-shaped request that gets rejected —
	// neither path may ever emit a permissive CORS header.
	preflight, _ := http.NewRequest(http.MethodOptions, "http://"+ts.server.Addr()+"/mutate", nil)
	preflight.Header.Set("Origin", "http://evil.example.com")
	preflight.Header.Set("Access-Control-Request-Method", "POST")
	preflightResp, err := client.Do(preflight)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer preflightResp.Body.Close()
	if got := preflightResp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q on a cross-origin preflight, want none", got)
	}
	if preflightResp.StatusCode == http.StatusOK {
		t.Fatalf("cross-origin preflight status = 200, want it rejected (deny-by-default)")
	}
}

// --- Session token ----------------------------------------------------------

func TestRequireSessionToken_MissingTokenRejectsMutation(t *testing.T) {
	ts := newSecurityTestServer(t, defaultTestPrincipal())
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Post("http://"+ts.server.Addr()+"/mutate", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a mutation with no session token", resp.StatusCode)
	}
}

func TestRequireSessionToken_WrongTokenRejectsMutation(t *testing.T) {
	ts := newSecurityTestServer(t, defaultTestPrincipal())
	req, err := http.NewRequest(http.MethodPost, "http://"+ts.server.Addr()+"/mutate", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set(httpapi.SessionTokenHeader, "wrong-token-value")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for a wrong session token", resp.StatusCode)
	}
}

func TestRequireSessionToken_CorrectTokenAllowsMutation(t *testing.T) {
	ts := newSecurityTestServer(t, defaultTestPrincipal())
	req, err := http.NewRequest(http.MethodPost, "http://"+ts.server.Addr()+"/mutate", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set(httpapi.SessionTokenHeader, ts.token)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 for the correct session token", resp.StatusCode)
	}
}

func TestRequireSessionToken_SafeMethodNeedsNoToken(t *testing.T) {
	ts := newSecurityTestServer(t, defaultTestPrincipal())
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + ts.server.Addr() + "/echo-principal")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 — a GET must never require the session token", resp.StatusCode)
	}
}

// --- Actor/role spoof -------------------------------------------------------

func TestActorRoleSpoof_BodyAndHeaderIgnored(t *testing.T) {
	principal := httpapi.LocalPrincipalSnapshot{Actor: "local-operator", Roles: []string{"operator"}}
	ts := newSecurityTestServer(t, principal)

	req, err := http.NewRequest(http.MethodPost, "http://"+ts.server.Addr()+"/mutate",
		strings.NewReader(`{"actor":"attacker","roles":["admin","root"]}`))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set(httpapi.SessionTokenHeader, ts.token)
	req.Header.Set("X-Actor", "attacker")
	req.Header.Set("X-Roles", "admin,root")
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var body struct {
		Actor string   `json:"actor"`
		Roles []string `json:"roles"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Actor != "local-operator" {
		t.Fatalf("echoed actor = %q, want local-operator (the server's own bound principal, not the attacker-claimed one)", body.Actor)
	}
	if len(body.Roles) != 1 || body.Roles[0] != "operator" {
		t.Fatalf("echoed roles = %v, want [operator] (not the attacker-claimed [admin root])", body.Roles)
	}
}

// --- Restart-only principal change -----------------------------------------

func TestPrincipal_ChangeOnlyTakesEffectOnFreshServer(t *testing.T) {
	// A running Server has no method to reassign Principal — the only way
	// this test (or any real caller) can observe a different principal is
	// by constructing an entirely new Server, simulating a process restart.
	// This is the behavioral proof for ADR-028's "Principal config đổi chỉ
	// có hiệu lực sau restart": within one Server's lifetime the bound
	// principal is immutable, and a "downgrade" only ever appears through a
	// fresh NewServer call.
	before := httpapi.LocalPrincipalSnapshot{Actor: "local-operator", Roles: []string{"operator", "auditor"}}
	tsBefore := newSecurityTestServer(t, before)
	respBefore, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + tsBefore.server.Addr() + "/echo-principal")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	var bodyBefore struct {
		Actor string   `json:"actor"`
		Roles []string `json:"roles"`
	}
	if err := json.NewDecoder(respBefore.Body).Decode(&bodyBefore); err != nil {
		t.Fatalf("decode: %v", err)
	}
	respBefore.Body.Close()
	if len(bodyBefore.Roles) != 2 {
		t.Fatalf("roles before = %v, want 2 roles", bodyBefore.Roles)
	}

	// Simulate `aw serve` restarting with a downgraded role set — a fresh
	// Server, fresh token, fresh listener, exactly what a real restart does.
	after := httpapi.LocalPrincipalSnapshot{Actor: "local-operator", Roles: []string{"operator"}}
	tsAfter := newSecurityTestServer(t, after)
	respAfter, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + tsAfter.server.Addr() + "/echo-principal")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer respAfter.Body.Close()
	var bodyAfter struct {
		Actor string   `json:"actor"`
		Roles []string `json:"roles"`
	}
	if err := json.NewDecoder(respAfter.Body).Decode(&bodyAfter); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(bodyAfter.Roles) != 1 || bodyAfter.Roles[0] != "operator" {
		t.Fatalf("roles after restart = %v, want [operator] (the downgraded set)", bodyAfter.Roles)
	}

	// The FIRST server, still running, must be entirely unaffected by the
	// second Server's construction — proving there is no shared/global
	// mutable state a "restart" elsewhere could have leaked through.
	respStillBefore, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + tsBefore.server.Addr() + "/echo-principal")
	if err != nil {
		t.Fatalf("Get (original server after the second started): %v", err)
	}
	defer respStillBefore.Body.Close()
	var bodyStillBefore struct {
		Actor string   `json:"actor"`
		Roles []string `json:"roles"`
	}
	if err := json.NewDecoder(respStillBefore.Body).Decode(&bodyStillBefore); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(bodyStillBefore.Roles) != 2 {
		t.Fatalf("original server's roles after a second Server started = %v, want unchanged 2 roles", bodyStillBefore.Roles)
	}
}

// --- Secret scan: token never appears in logs or in any URL-like context ---

func TestSecretScan_TokenNeverAppearsInLogOutput(t *testing.T) {
	ts := newSecurityTestServer(t, defaultTestPrincipal())
	client := &http.Client{Timeout: 5 * time.Second}

	// Drive a realistic mix of traffic, including requests that carry the
	// token (a correct mutation, a wrong one, and rejected Host/Origin
	// attempts) — anything that COULD end up logged, does so now.
	mustDo := func(req *http.Request) {
		t.Helper()
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		resp.Body.Close()
	}

	req1, _ := http.NewRequest(http.MethodPost, "http://"+ts.server.Addr()+"/mutate", strings.NewReader(`{}`))
	req1.Header.Set(httpapi.SessionTokenHeader, ts.token)
	mustDo(req1)

	req2, _ := http.NewRequest(http.MethodPost, "http://"+ts.server.Addr()+"/mutate", strings.NewReader(`{}`))
	req2.Header.Set(httpapi.SessionTokenHeader, "wrong-token")
	mustDo(req2)

	req3, _ := http.NewRequest(http.MethodGet, "http://"+ts.server.Addr()+"/echo-principal", nil)
	req3.Host = "evil.example.com:1"
	req3.Header.Set(httpapi.SessionTokenHeader, ts.token)
	mustDo(req3)

	bootstrapResp, err := client.Get("http://" + ts.server.Addr() + "/")
	if err != nil {
		t.Fatalf("Get bootstrap: %v", err)
	}
	bootstrapResp.Body.Close()

	logOutput := ts.logBuf.String()
	if strings.Contains(logOutput, ts.token) {
		t.Fatalf("log output contains the session token verbatim — this must never happen. Log output: %s", logOutput)
	}
}

func TestSecretScan_BootstrapHTMLNeverPutsTokenInAURLOrQueryString(t *testing.T) {
	ts := newSecurityTestServer(t, defaultTestPrincipal())
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + ts.server.Addr() + "/")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	html := string(body)

	// Sanity: the token really is embedded somewhere (otherwise the
	// assertions below would pass vacuously).
	if !strings.Contains(html, ts.token) {
		t.Fatalf("bootstrap HTML does not contain the token at all — test fixture is broken")
	}

	// The one legitimate place is inside the inline <script> JSON blob. It
	// must never appear inside an href/src attribute or after a "?"/"&" as
	// if it were a query parameter — those are the URL-like contexts ADR-016
	// explicitly forbids ("Token không được ghi vào URL").
	forbiddenContexts := []string{
		"href=\"" + ts.token,
		"src=\"" + ts.token,
		"?token=" + ts.token,
		"&token=" + ts.token,
		"?session=" + ts.token,
	}
	for _, ctx := range forbiddenContexts {
		if strings.Contains(html, ctx) {
			t.Fatalf("bootstrap HTML embeds the token in a URL-like context (%q) — forbidden by ADR-016", ctx)
		}
	}
}

func TestBootstrapHandler_SetsNoStoreAndCSP(t *testing.T) {
	ts := newSecurityTestServer(t, defaultTestPrincipal())
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + ts.server.Addr() + "/")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	csp := resp.Header.Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("Content-Security-Policy header is missing")
	}
	if !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("CSP = %q, want a default-src 'none' baseline", csp)
	}
}

func TestBootstrapHandler_RejectedByHostGuardLikeAnyOtherRoute(t *testing.T) {
	// The bootstrap route performs no Host check of its own — proving the
	// shared HostOriginGuard in front of it is what protects it, the same
	// as every other route, not a second parallel mechanism that could
	// drift out of sync.
	ts := newSecurityTestServer(t, defaultTestPrincipal())
	req, err := http.NewRequest(http.MethodGet, "http://"+ts.server.Addr()+"/", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Host = "evil.example.com:1"
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 — bootstrap must be rejected for a foreign Host exactly like any other route", resp.StatusCode)
	}
}

// splitHostPort is a t.Helper-wrapped net.SplitHostPort, failing the test
// immediately on a malformed address instead of making every call site
// check err.
func splitHostPort(t *testing.T, addr string) (host, port string, err error) {
	t.Helper()
	host, port, err = net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", addr, err)
	}
	return host, port, err
}
