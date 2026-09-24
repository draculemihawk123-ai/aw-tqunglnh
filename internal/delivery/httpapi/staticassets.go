package httpapi

import "net/http"

// StaticAssetHandler serves the built UI's hashed asset files
// (`web/dist/assets/*`, V7-01's own React+Vite bundle) under `/assets/` —
// V7-02A's own addition, registered alongside BootstrapHandler at the same
// `--ui-dist` composition point (cmd/aw/serve.go). assetsDir is the real
// `<ui-dist>/assets` directory the composition root already validated
// exists; "" means no `--ui-dist` was configured, matching
// BootstrapHandler's own nil-means-not-configured convention.
//
// Sub-resource responses (JS/CSS) do not carry their own
// Content-Security-Policy — CSP only governs the top-level document that
// loads them (BootstrapHandler's own response), so none is set here.
// http.Dir/http.FileServer already refuse to resolve outside assetsDir
// (path.Clean + rejecting any element that could escape the root), so no
// separate traversal guard is needed on top of it.
func StaticAssetHandler(assetsDir string) http.HandlerFunc {
	if assetsDir == "" {
		return func(w http.ResponseWriter, r *http.Request) {
			WriteError(w, http.StatusNotFound, ErrorCodeNotFound, "no UI build is installed on this server (aw serve was started without --ui-dist)", nil)
		}
	}
	fileServer := http.StripPrefix("/assets/", http.FileServer(http.Dir(assetsDir)))
	return func(w http.ResponseWriter, r *http.Request) {
		// The built bundle is re-served fresh from disk on every `aw serve`
		// restart and never changes while one process is running, but a
		// browser tab left open across a restart must not keep using a stale
		// cached copy from a previous build — same "fresh per process start"
		// discipline as the session token itself, just for assets instead of
		// the bootstrap page.
		w.Header().Set("Cache-Control", "no-cache")
		fileServer.ServeHTTP(w, r)
	}
}
