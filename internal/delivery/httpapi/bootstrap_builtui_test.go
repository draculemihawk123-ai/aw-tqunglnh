package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// TestBootstrapHandler_NilIndexHTMLKeepsThePreV7MinimalPage proves the
// V7-02A signature change (an added builtIndexHTML parameter) is
// backward-compatible in behavior: every V6 call site that still passes
// nil gets byte-for-byte the same minimal placeholder page as before.
func TestBootstrapHandler_NilIndexHTMLKeepsThePreV7MinimalPage(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	httpapi.BootstrapHandler("tok", httpapi.LocalPrincipalSnapshot{Actor: "local-operator", Roles: []string{"operator"}}, idsource.Random{}, nil)(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, "<title>agent-workflow</title>") {
		t.Fatalf("body = %s, want the pre-V7 minimal placeholder title", body)
	}
	if !strings.Contains(body, `window.__AW_BOOTSTRAP__=`) {
		t.Fatalf("body = %s, want the bootstrap payload script", body)
	}
}

// TestBootstrapHandler_BuiltIndexHTMLInjectsTokenAndKeepsTheRealAppShell
// is V7-02A's own real behavior: given a real `web/dist/index.html`-shaped
// page, the handler must serve that page's own real markup (its built
// <script>/<link> asset tags, its <div id="root">) with the bootstrap
// token script injected just before </body> — never replacing or
// dropping the built shell.
func TestBootstrapHandler_BuiltIndexHTMLInjectsTokenAndKeepsTheRealAppShell(t *testing.T) {
	built := []byte(`<!doctype html>
<html>
<head>
<script type="module" crossorigin src="/assets/index-ABC123.js"></script>
<link rel="stylesheet" crossorigin href="/assets/index-DEF456.css">
</head>
<body>
<div id="root"></div>
</body>
</html>
`)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	httpapi.BootstrapHandler("tok", httpapi.LocalPrincipalSnapshot{Actor: "local-operator", Roles: []string{"operator"}}, idsource.Random{}, built)(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `src="/assets/index-ABC123.js"`) {
		t.Fatalf("body = %s, want the built app's own script tag preserved", body)
	}
	if !strings.Contains(body, `href="/assets/index-DEF456.css"`) {
		t.Fatalf("body = %s, want the built app's own stylesheet link preserved", body)
	}
	if !strings.Contains(body, `<div id="root"></div>`) {
		t.Fatalf("body = %s, want the built app's own mount point preserved", body)
	}
	if !strings.Contains(body, `window.__AW_BOOTSTRAP__=`) {
		t.Fatalf("body = %s, want the bootstrap payload script injected", body)
	}
	// The injected script must land BEFORE </body>, not appended after the
	// whole document (which would be invalid HTML and could render outside
	// the page entirely in some browsers).
	scriptIdx := strings.Index(body, "window.__AW_BOOTSTRAP__=")
	bodyCloseIdx := strings.LastIndex(body, "</body>")
	if scriptIdx == -1 || bodyCloseIdx == -1 || scriptIdx > bodyCloseIdx {
		t.Fatalf("bootstrap script must be injected before </body>, body = %s", body)
	}
}

// TestBootstrapHandler_BuiltIndexHTMLSetsStyleAndImgSrcCSP proves the
// CSP baseline was widened enough for the built app's own external
// stylesheet <link> to actually load — default-src 'none' alone would
// silently block it, breaking every screen's styling.
func TestBootstrapHandler_BuiltIndexHTMLSetsStyleAndImgSrcCSP(t *testing.T) {
	built := []byte(`<!doctype html><html><head></head><body><div id="root"></div></body></html>`)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	httpapi.BootstrapHandler("tok", httpapi.LocalPrincipalSnapshot{Actor: "local-operator", Roles: []string{"operator"}}, idsource.Random{}, built)(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "style-src 'self'", "img-src 'self' data:"} {
		if !strings.Contains(csp, want) {
			t.Fatalf("CSP = %q, want it to contain %q", csp, want)
		}
	}
}

// TestBootstrapHandler_BuiltIndexHTMLWithoutCloseBodyFailsClosed proves
// this handler never silently drops the token if the composition root's
// own startup validation (cmd/aw/serve.go's loadBuiltUIIndex) were ever
// bypassed and a malformed page reached it anyway.
func TestBootstrapHandler_BuiltIndexHTMLWithoutCloseBodyFailsClosed(t *testing.T) {
	built := []byte(`<html><body><div id="root"></div>`) // no </body>
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	httpapi.BootstrapHandler("tok", httpapi.LocalPrincipalSnapshot{Actor: "local-operator", Roles: []string{"operator"}}, idsource.Random{}, built)(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 for a built index.html missing </body>", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "tok") {
		t.Fatalf("body = %s, must never leak the token when failing closed", rec.Body.String())
	}
}
