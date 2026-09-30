package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadBuiltUIIndex_EmptyUIDistIsNotAnError(t *testing.T) {
	indexHTML, assetsDir, err := loadBuiltUIIndex("")
	if err != nil {
		t.Fatalf("err = %v, want nil for an omitted --ui-dist", err)
	}
	if indexHTML != nil || assetsDir != "" {
		t.Fatalf("indexHTML=%v assetsDir=%q, want both zero values", indexHTML, assetsDir)
	}
}

func TestLoadBuiltUIIndex_RejectsNonexistentDirectory(t *testing.T) {
	_, _, err := loadBuiltUIIndex(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("expected an error for a non-existent --ui-dist directory")
	}
}

func TestLoadBuiltUIIndex_RejectsMissingIndexHTML(t *testing.T) {
	dir := t.TempDir()
	_, _, err := loadBuiltUIIndex(dir)
	if err == nil {
		t.Fatal("expected an error for a --ui-dist with no index.html")
	}
}

func TestLoadBuiltUIIndex_RejectsIndexHTMLWithoutCloseBodyTag(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html><body>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	_, _, err := loadBuiltUIIndex(dir)
	if err == nil {
		t.Fatal("expected an error for an index.html with no </body>")
	}
}

func TestLoadBuiltUIIndex_RejectsMissingAssetsSubdirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html><body></body></html>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	_, _, err := loadBuiltUIIndex(dir)
	if err == nil {
		t.Fatal("expected an error for a --ui-dist with no assets/ subdirectory")
	}
}

func TestLoadBuiltUIIndex_AcceptsAValidBuildDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html><body></body></html>"), 0o644); err != nil {
		t.Fatalf("write index.html: %v", err)
	}
	assetsDir := filepath.Join(dir, "assets")
	if err := os.Mkdir(assetsDir, 0o755); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}

	indexHTML, gotAssetsDir, err := loadBuiltUIIndex(dir)
	if err != nil {
		t.Fatalf("loadBuiltUIIndex: %v", err)
	}
	if string(indexHTML) != "<html><body></body></html>" {
		t.Fatalf("indexHTML = %q, want the file's real content", indexHTML)
	}
	if gotAssetsDir != assetsDir {
		t.Fatalf("assetsDir = %q, want %q", gotAssetsDir, assetsDir)
	}
}

// TestServe_RejectsMisconfiguredUIDist mirrors
// TestServe_RejectsMissingArtifactRootDirectory for the new, optional
// --ui-dist flag: given but wrong must fail closed at startup.
func TestServe_RejectsMisconfiguredUIDist(t *testing.T) {
	var stdout bytes.Buffer
	err := serve(context.Background(), []string{
		"--db", serveTestDB(t),
		"--artifact-root", t.TempDir(),
		"--workspace-root", t.TempDir(),
		"--ui-dist", filepath.Join(t.TempDir(), "does-not-exist"),
	}, &stdout)
	if err == nil {
		t.Fatal("expected an error for a non-existent --ui-dist")
	}
}

// TestServe_ServesBuiltUIWhenUIDistConfigured is V7-02's own Verify line
// end to end: "smoke khẳng định aw serve phục vụ được bundle đã build" — a
// real fixture build directory (index.html + assets/app.js), served by a
// real running `aw serve` process over a real HTTP round trip, with no
// dev server involved at all.
func TestServe_ServesBuiltUIWhenUIDistConfigured(t *testing.T) {
	uiDist := t.TempDir()
	if err := os.WriteFile(filepath.Join(uiDist, "index.html"), []byte(
		`<!doctype html><html><head><script type="module" src="/assets/app.js"></script></head><body><div id="root"></div></body></html>`,
	), 0o644); err != nil {
		t.Fatalf("write index.html fixture: %v", err)
	}
	assetsDir := filepath.Join(uiDist, "assets")
	if err := os.Mkdir(assetsDir, 0o755); err != nil {
		t.Fatalf("mkdir assets fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(assetsDir, "app.js"), []byte("console.log('built-ui-fixture')"), 0o644); err != nil {
		t.Fatalf("write app.js fixture: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var stdout syncBuffer
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- serve(ctx, []string{
			"--db", serveTestDB(t),
			"--artifact-root", t.TempDir(),
			"--workspace-root", t.TempDir(),
			"--host", "127.0.0.1",
			"--port", "0",
			"--ui-dist", uiDist,
		}, &stdout)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-serveDone:
		case <-time.After(10 * time.Second):
			t.Fatal("serve did not shut down within 10s of context cancellation")
		}
	})

	addr := waitForServeAddress(t, &stdout)
	client := &http.Client{Timeout: 5 * time.Second}

	indexResp, err := client.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	indexBody, err := io.ReadAll(indexResp.Body)
	indexResp.Body.Close()
	if err != nil {
		t.Fatalf("read / body: %v", err)
	}
	if indexResp.StatusCode != http.StatusOK {
		t.Fatalf("/ status = %d, want 200, body=%s", indexResp.StatusCode, indexBody)
	}
	if !strings.Contains(string(indexBody), `src="/assets/app.js"`) {
		t.Fatalf("/ body = %s, want the fixture's own built script tag", indexBody)
	}
	if !strings.Contains(string(indexBody), "window.__AW_BOOTSTRAP__=") {
		t.Fatalf("/ body = %s, want the injected bootstrap token script", indexBody)
	}

	assetResp, err := client.Get("http://" + addr + "/assets/app.js")
	if err != nil {
		t.Fatalf("GET /assets/app.js: %v", err)
	}
	assetBody, err := io.ReadAll(assetResp.Body)
	assetResp.Body.Close()
	if err != nil {
		t.Fatalf("read /assets/app.js body: %v", err)
	}
	if assetResp.StatusCode != http.StatusOK {
		t.Fatalf("/assets/app.js status = %d, want 200", assetResp.StatusCode)
	}
	if string(assetBody) != "console.log('built-ui-fixture')" {
		t.Fatalf("/assets/app.js body = %q, want the fixture file's real content", assetBody)
	}
}

// TestServe_NoUIDistAndNoEmbeddedUIFallsBackToNoUI is V8-08's own regression
// guard for the new embeddedui fallback (cmd/aw/serve.go's own "if
// uiIndexHTML == nil" branch, added alongside embeddedui.Extract): an
// omitted --ui-dist, run against THIS repo's own real compiled-in
// dist/index.html placeholder (embeddedui's own checked-in
// AGENT-WORKFLOW-EMBEDDED-UI-PLACEHOLDER — no cmd/aw-release-build ran
// before this test binary was compiled), must behave EXACTLY like aw
// serve's own pre-V8-08 "no UI" case, end to end through the real running
// process — never accidentally serve the placeholder's own throwaway HTML
// as if it were a real UI.
func TestServe_NoUIDistAndNoEmbeddedUIFallsBackToNoUI(t *testing.T) {
	dbPath := serveTestDB(t)
	artifactRoot := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	var stdout syncBuffer
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- serve(ctx, []string{
			"--db", dbPath,
			"--artifact-root", artifactRoot,
			"--workspace-root", t.TempDir(),
			"--host", "127.0.0.1",
			"--port", "0",
		}, &stdout)
	}()

	addr := waitForServeAddress(t, &stdout)
	client := &http.Client{Timeout: 5 * time.Second}

	assetResp, err := client.Get("http://" + addr + "/assets/app.js")
	if err != nil {
		t.Fatalf("GET /assets/app.js: %v", err)
	}
	assetBody, err := io.ReadAll(assetResp.Body)
	assetResp.Body.Close()
	if err != nil {
		t.Fatalf("read /assets/app.js body: %v", err)
	}
	if assetResp.StatusCode != http.StatusNotFound {
		t.Fatalf("/assets/app.js status = %d, want 404 (no UI installed), body=%s", assetResp.StatusCode, assetBody)
	}
	if !strings.Contains(string(assetBody), "no UI build is installed") {
		t.Fatalf("/assets/app.js body = %s, want the canonical \"no UI build is installed\" error", assetBody)
	}

	// Close the idle keep-alive connection ourselves rather than leaving
	// Shutdown to wait for it to go idle on its own (mirrors
	// TestServe_StartsServesHealthAndShutsDownGracefully's own identical
	// comment) — makes shutdown timing deterministic before t.TempDir's own
	// cleanup tries to remove the still-open DB file.
	client.CloseIdleConnections()
	cancel()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("serve returned an error after graceful shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not shut down within 10s of context cancellation")
	}
}
