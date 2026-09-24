package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
)

// bootstrapPayload is the exact shape BootstrapHandler embeds into the
// page: the per-start session token plus the trusted LocalPrincipalSnapshot
// the browser needs to display ("signed in as ...") but must never be able
// to influence — both values a caller could otherwise never legitimately
// learn any other way, which is exactly why this one response is the only
// place either is ever written out.
type bootstrapPayload struct {
	Token string   `json:"token"`
	Actor string   `json:"actor"`
	Roles []string `json:"roles"`
}

// BootstrapHandler renders the one HTML response allowed to carry the
// per-start session token and the trusted LocalPrincipalSnapshot
// (docs/design/08-v6-api-projections.md V6-01A: "inject chỉ vào bootstrap
// HTML no-store/CSP"). It relies entirely on the Server's own middleware
// chain (HostOriginGuard) to have already rejected any request whose Host
// did not validate as this server's own bound loopback address before this
// handler ever runs — it performs no Host check of its own, on purpose,
// so there is exactly one place in the whole codebase that decides "is this
// Host trusted", not two that could drift apart.
//
// Every response:
//   - Cache-Control: no-store — never cached to disk by a browser, proxy or
//     the OS ("Token không được ghi vào... browser storage").
//   - Content-Security-Policy — default-src 'none' plus a fresh per-response
//     nonce that allow-lists only the one inline <script> this handler
//     itself writes, so an attacker who somehow got a second script tag
//     into the response (a very different bug this CSP is not the fix for,
//     but defense-in-depth for) could not have it execute.
//   - The token/actor/roles are embedded via encoding/json.Marshal, which
//     HTML-escapes '<', '>' and '&' by default — the standard-library
//     mechanism for safely embedding a JSON value inside a <script> tag
//     without a "</script>" breakout, so no additional manual escaping is
//     needed here.
//
// ids mints the per-response CSP nonce (the same idsource.Source primitive
// already required by Config.IDs) — the nonce itself is not secret (CSP
// nonces are meant to be visible; their unpredictability only prevents an
// attacker from pre-guessing one to smuggle in a competing script tag), so
// reusing the same generator that mints correlation IDs is fine.
//
// builtIndexHTML is V7-02A's own addition: the real UI's built index.html
// (`web/dist/index.html`), read once by the composition root at startup
// when `--ui-dist` is configured, or nil when it is not. Nil preserves the
// exact pre-V7 minimal placeholder page byte-for-byte — every V6 test that
// calls this constructor without a fifth argument keeps working unchanged.
// When non-nil, the composition root has already validated it contains a
// "</body>" close tag (see cmd/aw/serve.go's own loadBuiltUIIndex) — this
// handler still fails closed with 500 rather than silently dropping the
// token if that assumption is ever violated, exactly like the Marshal
// error path below.
func BootstrapHandler(token string, principal LocalPrincipalSnapshot, ids idsource.Source, builtIndexHTML []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nonce := ids.NewID()

		data, err := json.Marshal(bootstrapPayload{
			Token: token,
			Actor: principal.Actor,
			Roles: principal.Roles,
		})
		if err != nil {
			// The payload is three plain strings/a string slice — Marshal can
			// only fail here from a fault in this package's own type, not from
			// anything a caller controls. Fail closed rather than ever serve a
			// bootstrap page with a half-written token.
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
			return
		}
		script := fmt.Appendf(nil, `<script nonce=%q>window.__AW_BOOTSTRAP__=%s;</script>`, nonce, data)

		var page []byte
		if builtIndexHTML == nil {
			page = fmt.Appendf(nil, `<!doctype html>
<html>
<head><meta charset="utf-8"><title>agent-workflow</title></head>
<body>
%s
</body>
</html>
`, script)
		} else {
			const closeBody = "</body>"
			idx := bytes.LastIndex(builtIndexHTML, []byte(closeBody))
			if idx == -1 {
				// The composition root already validated this at startup — a
				// missing </body> here means that guarantee broke somewhere,
				// not a per-request condition. Fail closed rather than ever
				// serve the built page without the token it needs to work.
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal_error"})
				return
			}
			page = make([]byte, 0, len(builtIndexHTML)+len(script))
			page = append(page, builtIndexHTML[:idx]...)
			page = append(page, script...)
			page = append(page, builtIndexHTML[idx:]...)
		}

		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", fmt.Sprintf(
			"default-src 'none'; script-src 'self' 'nonce-%s'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'",
			nonce,
		))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write(page)
	}
}
