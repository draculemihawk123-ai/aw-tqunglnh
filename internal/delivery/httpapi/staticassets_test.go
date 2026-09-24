package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

func TestStaticAssetHandler_NotConfiguredReturns404TypedError(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	rec := httptest.NewRecorder()
	httpapi.StaticAssetHandler("")(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json (the canonical error envelope)", got)
	}
	if !strings.Contains(rec.Body.String(), `"code":"NOT_FOUND"`) {
		t.Fatalf("body = %s, want the canonical NOT_FOUND error envelope", rec.Body.String())
	}
}

func TestStaticAssetHandler_ServesAFileFromTheConfiguredDirectory(t *testing.T) {
	assetsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(assetsDir, "app.js"), []byte("console.log('hi')"), 0o644); err != nil {
		t.Fatalf("write fixture asset: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	rec := httptest.NewRecorder()
	httpapi.StaticAssetHandler(assetsDir)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "console.log('hi')" {
		t.Fatalf("body = %q, want the fixture file's content", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", got)
	}
}

func TestStaticAssetHandler_MissingFileReturns404(t *testing.T) {
	assetsDir := t.TempDir()

	req := httptest.NewRequest(http.MethodGet, "/assets/does-not-exist.js", nil)
	rec := httptest.NewRecorder()
	httpapi.StaticAssetHandler(assetsDir)(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for a file that does not exist under assetsDir", rec.Code)
	}
}

// TestStaticAssetHandler_CannotEscapeAssetsDir proves the stdlib
// http.Dir/http.FileServer traversal guard this handler relies on (rather
// than a second, hand-rolled check) actually holds: a request that tries
// to walk up out of assetsDir via "../" must never read a file that lives
// outside it.
func TestStaticAssetHandler_CannotEscapeAssetsDir(t *testing.T) {
	root := t.TempDir()
	assetsDir := filepath.Join(root, "assets")
	if err := os.Mkdir(assetsDir, 0o755); err != nil {
		t.Fatalf("Mkdir assetsDir: %v", err)
	}
	secretPath := filepath.Join(root, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("do-not-serve-me"), 0o644); err != nil {
		t.Fatalf("write secret fixture: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/assets/../secret.txt", nil)
	rec := httptest.NewRecorder()
	httpapi.StaticAssetHandler(assetsDir)(rec, req)

	if strings.Contains(rec.Body.String(), "do-not-serve-me") {
		t.Fatalf("static asset handler served a file outside assetsDir: %s", rec.Body.String())
	}
}
