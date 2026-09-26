package securitymatrix

// Transport-layer half of V6-13's own route×scope×role matrix: loopback
// bind, Host, Origin, deny-by-default CORS and the per-start session token
// (ADR-016, AK-ARCH-025A, docs/design/08-v6-api-projections.md V6-01A).
// Every case here runs against EVERY route the real production composition
// registers — the row set comes from
// httpapi.RouteRegistry.Descriptors() on a registry built by the real
// internal/delivery/httpcompose.ComposeRoutes, never a hand-written list.

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// pathParamPattern matches one `{param}` placeholder in a
// RouteDescriptor.Path — mirrors
// internal/delivery/httpapi/apicontract/routeinventory_test.go's own
// identical helper.
var pathParamPattern = regexp.MustCompile(`\{[^{}]+\}`)

// fabricatedPath instantiates a registration-time path pattern into a real,
// requestable URL by substituting EVERY {param} with fabricatedID — an
// identifier this server has never issued for anything.
//
// Using a fabricated identifier (rather than a real seeded one) is
// deliberate for every transport-layer case below: a request that the
// Host/Origin/token guard is supposed to reject must be rejected BEFORE
// routing ever happens, so the path's own resolvability is irrelevant to
// the assertion — and substituting a never-issued id guarantees that a
// case which is NOT rejected (the positive control,
// TestTransportGuard_ValidRequestIsNotRejectedByTheGuard) still cannot
// mutate any real state on its way to the handler's own not-found branch.
func fabricatedPath(pattern string) string {
	return pathParamPattern.ReplaceAllString(pattern, fabricatedID)
}

// corsResponseHeaders is every Access-Control-* response header a browser
// would honor. ADR-016's own "CORS deny-by-default" is implemented by
// OMISSION in internal/delivery/httpapi/security.go (HostOriginGuard never
// sets one, on any outcome) — so the real, checkable assertion is that NONE
// of these is ever present, on ANY response, for ANY route.
var corsResponseHeaders = []string{
	"Access-Control-Allow-Origin",
	"Access-Control-Allow-Credentials",
	"Access-Control-Allow-Methods",
	"Access-Control-Allow-Headers",
	"Access-Control-Expose-Headers",
	"Access-Control-Max-Age",
}

func assertNoCORSHeaders(t *testing.T, label string, r resp) {
	t.Helper()
	for _, h := range corsResponseHeaders {
		if v := r.Header.Get(h); v != "" {
			t.Errorf("%s: response carries %s: %q — this server must never emit a CORS allow header (ADR-016 deny-by-default)", label, h, v)
		}
	}
}

// isMutating mirrors internal/delivery/httpapi/security.go's own
// isSafeMethod exactly (GET/HEAD/OPTIONS are safe; everything else
// mutates). Restated here rather than exported from that package so this
// test proves the POLICY independently instead of asking the code under
// test what its own answer is.
func isMutating(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

// TestTransportGuardMatrix_EveryRoute is the transport half of the matrix:
// for every registered route, a request with a foreign Host, a foreign
// Origin, or (for a mutating method) a missing/wrong session token must be
// rejected with the exact documented status and body, and must never carry
// a CORS allow header.
func TestTransportGuardMatrix_EveryRoute(t *testing.T) {
	e := newEnv(t)
	descriptors := e.routes.Descriptors()
	if len(descriptors) == 0 {
		t.Fatal("ComposeRoutes registered no routes — the matrix has no rows")
	}

	for _, d := range descriptors {
		t.Run(d.OperationID, func(t *testing.T) {
			path := fabricatedPath(d.Path)

			// AK-ARCH-025A / ADR-016: wrong Host. This is the DNS-rebinding
			// defense — the raw TCP peer is still loopback here (the request
			// really does reach this server), only the Host header the
			// request itself claims differs, exactly as a rebound browser
			// request would.
			for _, badHost := range []string{"evil.example.com:80", "localhost:1", "127.0.0.1:1"} {
				got := e.do(t, req{Method: d.Method, Path: path, Host: badHost})
				if got.Status != http.StatusForbidden {
					t.Errorf("Host %q: status = %d, want %d (HostOriginGuard must reject any Host but this server's own %q)",
						badHost, got.Status, http.StatusForbidden, e.host)
				}
				if !strings.Contains(got.Body, "invalid_host") {
					t.Errorf("Host %q: body = %q, want the invalid_host rejection", badHost, got.Body)
				}
				assertNoCORSHeaders(t, "wrong Host "+badHost, got)
			}

			// ADR-016: foreign Origin. An Origin header a browser set for
			// some other page's origin must be rejected even when the Host
			// is correct.
			for _, badOrigin := range []string{"http://evil.example.com", "https://" + e.host, "null"} {
				got := e.do(t, req{Method: d.Method, Path: path, Origin: badOrigin})
				if got.Status != http.StatusForbidden {
					t.Errorf("Origin %q: status = %d, want %d", badOrigin, got.Status, http.StatusForbidden)
				}
				if !strings.Contains(got.Body, "invalid_origin") {
					t.Errorf("Origin %q: body = %q, want the invalid_origin rejection", badOrigin, got.Body)
				}
				assertNoCORSHeaders(t, "foreign Origin "+badOrigin, got)
			}

			if !isMutating(d.Method) {
				return
			}

			// AK-ARCH-025A: "any mutation missing a session token" is
			// rejected. Both the absent-header and the wrong-value case
			// produce the identical 401.
			for _, mode := range []struct {
				name string
				tok  tokenMode
			}{{"missing", tokenMissing}, {"wrong", tokenWrong}} {
				got := e.do(t, req{Method: d.Method, Path: path, Token: mode.tok, IdempotencyKey: "sm-transport-" + d.OperationID, Body: `{}`})
				if got.Status != http.StatusUnauthorized {
					t.Errorf("%s session token: status = %d, want %d — a mutating route must never run without this server's own per-start token",
						mode.name, got.Status, http.StatusUnauthorized)
				}
				if !strings.Contains(got.Body, "invalid_session_token") {
					t.Errorf("%s session token: body = %q, want the invalid_session_token rejection", mode.name, got.Body)
				}
				assertNoCORSHeaders(t, mode.name+" token", got)
			}
		})
	}
}

// TestTransportGuard_ValidRequestIsNotRejectedByTheGuard is the positive
// control that keeps every rejection above meaningful: with the correct
// Host, no Origin (a non-browser client such as the `aw` CLI's own
// transport) and the real session token, no route may answer with either of
// the guard's own two rejections. It deliberately says nothing about what
// the handler then answers — a fabricated identifier legitimately produces
// 400/404/409 — only that the request got PAST the transport guard.
func TestTransportGuard_ValidRequestIsNotRejectedByTheGuard(t *testing.T) {
	e := newEnv(t)
	for _, d := range e.routes.Descriptors() {
		t.Run(d.OperationID, func(t *testing.T) {
			got := e.do(t, req{
				Method: d.Method, Path: fabricatedPath(d.Path),
				IdempotencyKey: "sm-positive-" + d.OperationID, Body: bodyForMethod(d.Method),
			})
			if got.Status == http.StatusForbidden && strings.Contains(got.Body, "invalid_host") {
				t.Errorf("a request with this server's own bound Host %q was rejected as invalid_host", e.host)
			}
			if got.Status == http.StatusUnauthorized && strings.Contains(got.Body, "invalid_session_token") {
				t.Errorf("a request carrying this server's own per-start session token was rejected as invalid_session_token")
			}
			assertNoCORSHeaders(t, "valid request", got)
		})
	}
}

// TestTransportGuard_SameOriginRequestIsAccepted proves the Origin check is
// an exact-match ALLOW of this server's own origin, not a blanket rejection
// of every Origin header (which would break the real bootstrap UI).
func TestTransportGuard_SameOriginRequestIsAccepted(t *testing.T) {
	e := newEnv(t)
	got := e.do(t, req{Method: http.MethodGet, Path: "/health/live", Origin: e.origin})
	if got.Status != http.StatusNoContent {
		t.Fatalf("GET /health/live with this server's own Origin: status = %d body = %q, want %d",
			got.Status, got.Body, http.StatusNoContent)
	}
	assertNoCORSHeaders(t, "same-origin request", got)
}

// TestCORSPreflightIsNeverAnswered proves deny-by-default CORS end to end: a
// real browser preflight (OPTIONS + Origin + Access-Control-Request-Method)
// from a foreign origin is rejected by the Host/Origin guard, and even a
// same-origin preflight gets no allow header — nothing in this server ever
// answers a preflight affirmatively, so no browser can ever be persuaded to
// send a cross-origin request to it.
func TestCORSPreflightIsNeverAnswered(t *testing.T) {
	e := newEnv(t)

	foreign := e.do(t, req{
		Method: http.MethodOptions, Path: "/projects", Origin: "http://evil.example.com",
		Headers: map[string]string{"Access-Control-Request-Method": "POST", "Access-Control-Request-Headers": httpapi.SessionTokenHeader},
	})
	if foreign.Status != http.StatusForbidden || !strings.Contains(foreign.Body, "invalid_origin") {
		t.Errorf("foreign-origin preflight: status = %d body = %q, want 403 invalid_origin", foreign.Status, foreign.Body)
	}
	assertNoCORSHeaders(t, "foreign-origin preflight", foreign)

	same := e.do(t, req{
		Method: http.MethodOptions, Path: "/projects", Origin: e.origin,
		Headers: map[string]string{"Access-Control-Request-Method": "POST"},
	})
	if same.Status == http.StatusOK || same.Status == http.StatusNoContent {
		t.Errorf("same-origin preflight was answered affirmatively with status %d — no OPTIONS route is registered anywhere, so this must not succeed", same.Status)
	}
	assertNoCORSHeaders(t, "same-origin preflight", same)
}

// bodyForMethod returns a minimal JSON object for a mutating method and an
// empty body for a safe one — enough for a handler to get as far as its own
// validation/not-found branch without this test having to know 60+ routes'
// individual request DTOs.
func bodyForMethod(method string) string {
	if isMutating(method) {
		return `{}`
	}
	return ""
}

// wantRouteCount pins how many routes the real production composition
// registers, so "every route has an explicit security proof" (V6-13's own
// "Hoàn thành khi") cannot quietly become "every route except the new one".
// It duplicates internal/delivery/httpapi/apicontract/routeinventory_test.go's
// own identical constant ON PURPOSE: a Go test constant cannot be shared
// across packages, and the whole point of both is to force a conscious
// review when the route set changes — a shared value that one task updates
// for both would defeat exactly half of that.
const wantRouteCount = 92

// TestEveryRegisteredRouteIsCoveredByTheMatrix is V6-13's own completion
// gate: the matrix's row set IS RouteRegistry.Descriptors(), so coverage is
// total by construction — what this test adds is the guard against that
// construction silently covering FEWER routes than actually ship (a
// descriptor list that stopped being the composed one, a leaf package
// dropped from ComposeRoutes), plus an explicit, reviewable scope tally.
func TestEveryRegisteredRouteIsCoveredByTheMatrix(t *testing.T) {
	e := newEnv(t)
	descriptors := e.routes.Descriptors()

	if len(descriptors) != wantRouteCount {
		t.Errorf("the real composition registers %d routes, this suite pins %d — if a route was intentionally added/removed, "+
			"update wantRouteCount here (and in internal/delivery/httpapi/apicontract) after confirming the new route is "+
			"genuinely covered by every scenario in this package", len(descriptors), wantRouteCount)
	}

	scopes := map[httpapi.ScopeKind]int{}
	seen := map[string]string{}
	for _, d := range descriptors {
		if d.ScopeKind != httpapi.ScopeInstallation && d.ScopeKind != httpapi.ScopeProject {
			t.Errorf("%s %s (%s): ScopeKind %q is outside ADR-025's closed INSTALLATION|PROJECT set",
				d.Method, d.Path, d.OperationID, d.ScopeKind)
		}
		scopes[d.ScopeKind]++
		key := d.Method + " " + d.Path
		if prev, dup := seen[key]; dup {
			t.Errorf("%s is registered twice (%s and %s)", key, prev, d.OperationID)
		}
		seen[key] = d.OperationID
	}
	if scopes[httpapi.ScopeInstallation] == 0 || scopes[httpapi.ScopeProject] == 0 {
		t.Fatalf("expected both scope kinds to be represented, got %v", scopes)
	}
	t.Logf("security matrix row set: %d routes (%d INSTALLATION, %d PROJECT)",
		len(descriptors), scopes[httpapi.ScopeInstallation], scopes[httpapi.ScopeProject])
}
