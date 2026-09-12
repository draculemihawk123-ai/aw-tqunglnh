package httpapi

import (
	"crypto/subtle"
	"net/http"
)

// SessionTokenHeader is the header a mutating request must carry this
// server's own per-start session token in (ADR-016: "Server chỉ đưa token
// vào bootstrap HTML... UI giữ token trong memory và gửi bằng header").
// The header NAME is not secret — only the token VALUE RequireSessionToken
// compares against is.
const SessionTokenHeader = "X-Aw-Session-Token"

// LocalOrigin is the exact host:port and scheme://host:port this server is
// actually bound to, computed once from the real bound listener address —
// never from a request's own Host header, which would make HostOriginGuard
// a tautology. NewServer is the only constructor: a composition root never
// builds one by hand.
type LocalOrigin struct {
	// Host is the exact "host:port" form a request's own Host header must
	// equal (e.g. "127.0.0.1:54321", "[::1]:54321", "localhost:54321" —
	// whichever literal Config.Host the operator configured, joined with the
	// actual bound port so an ephemeral Config.Port:0 still matches).
	Host string
	// Origin is Host's "http://" form, the exact value a request's Origin
	// header must equal when present.
	Origin string
}

// HostOriginGuard is ADR-016's deeper layer over loopback-only bind
// (validateLoopbackHost in server.go already proves the bind address itself
// can only ever be loopback; this middleware proves every individual
// request actually reached this process by that exact address, not merely
// some other address that happens to route to the same loopback interface).
//
// Two independent checks, both exact-match, both fail-closed:
//
//   - Host: a DNS-rebinding attacker's page has the browser resolve some
//     hostname it controls (e.g. evil.example.com) to 127.0.0.1 after the
//     page has already loaded from the real (non-loopback) address; the
//     browser then issues what it believes is a same-origin request, but
//     the HTTP Host header it sends is still "evil.example.com:PORT" — a
//     raw TCP peer being 127.0.0.1 proves nothing about what the request
//     itself claims to be talking to. Rejecting any Host that is not
//     byte-for-byte this server's own bound host:port defeats that
//     regardless of DNS.
//   - Origin: a browser sets Origin on every cross-origin-capable request
//     (and on most same-origin fetch/XHR too), and it cannot be spoofed by
//     page script — only by the network attacker also controlling Host, the
//     case above. A request's Origin must exactly equal this server's own
//     origin OR be entirely absent (a plain top-level navigation, or a
//     non-browser client such as `curl`/the `aw` CLI's own HTTP transport,
//     legitimately sends no Origin at all) — an Origin present but not
//     equal is always a forged/foreign one and is rejected.
//
// This handler never sets any Access-Control-Allow-* response header, on
// any request outcome, including a rejected one — ADR-016's own "CORS
// deny-by-default không thay thế các kiểm tra này" as actual code: CORS
// deny-by-default here is achieved by omission (no permissive header is
// ever emitted for a browser to honor), not by first allowing and then
// trying to narrow a preflight response.
func HostOriginGuard(local LocalOrigin) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Host != local.Host {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "invalid_host"})
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" && origin != local.Origin {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "invalid_origin"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// isSafeMethod reports whether method never mutates state — the same
// GET/HEAD/OPTIONS set every route this package has registered so far
// (health, bootstrap) actually uses. RequireSessionToken never applies to
// these: a safe method is protected by HostOriginGuard alone, matching
// ADR-016's own "Mutation cần per-start local session token" — it says
// mutation, not every request.
func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

// RequireSessionToken rejects every mutating request that does not carry
// token, byte-for-byte, in SessionTokenHeader (ADR-016: "request browser
// không có token bị từ chối để chống DNS rebinding/CSRF"). The comparison
// is constant-time (crypto/subtle) so response timing cannot leak how many
// leading bytes of a guessed token were correct. token is generated fresh
// once per process start by NewServer (via Config.IDs) and lives only in
// this closure's memory — it is never written to disk, never logged, and
// never placed in a URL.
func RequireSessionToken(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isSafeMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			got := r.Header.Get(SessionTokenHeader)
			if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_session_token"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
