package embeddedui

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestExtractFS_PlaceholderIndex_ReportsNotEmbedded(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!-- AGENT-WORKFLOW-EMBEDDED-UI-PLACEHOLDER -->")},
	}
	destDir := filepath.Join(t.TempDir(), "extracted")

	indexHTML, embedded, err := extractFS(fsys, destDir)
	if err != nil {
		t.Fatalf("extractFS: %v", err)
	}
	if embedded {
		t.Fatal("embedded = true for a placeholder index.html, want false")
	}
	if indexHTML != nil {
		t.Fatalf("indexHTML = %q, want nil for a placeholder", indexHTML)
	}
	if _, statErr := os.Stat(destDir); statErr == nil {
		t.Fatalf("destDir %s was created for a placeholder — Extract must leave it untouched", destDir)
	}
}

func TestExtractFS_RealBuild_ExtractsIndexAndAssets(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html":          &fstest.MapFile{Data: []byte("<!doctype html><html><body>real UI</body></html>")},
		"assets/app.js":       &fstest.MapFile{Data: []byte("console.log('hi')")},
		"assets/nested/x.css": &fstest.MapFile{Data: []byte("body{}")},
	}
	destDir := filepath.Join(t.TempDir(), "extracted")

	indexHTML, embedded, err := extractFS(fsys, destDir)
	if err != nil {
		t.Fatalf("extractFS: %v", err)
	}
	if !embedded {
		t.Fatal("embedded = false for a real build's index.html, want true")
	}
	if string(indexHTML) != "<!doctype html><html><body>real UI</body></html>" {
		t.Fatalf("indexHTML = %q, want the real build's own content", indexHTML)
	}

	appJS, readErr := os.ReadFile(filepath.Join(destDir, "assets", "app.js"))
	if readErr != nil {
		t.Fatalf("read extracted assets/app.js: %v", readErr)
	}
	if string(appJS) != "console.log('hi')" {
		t.Fatalf("extracted assets/app.js = %q, want the fixture content", appJS)
	}
	nested, readErr := os.ReadFile(filepath.Join(destDir, "assets", "nested", "x.css"))
	if readErr != nil {
		t.Fatalf("read extracted assets/nested/x.css: %v", readErr)
	}
	if string(nested) != "body{}" {
		t.Fatalf("extracted assets/nested/x.css = %q, want the fixture content", nested)
	}
}

func TestIsEmbedded_RealCompiledInPlaceholder_ReportsFalse(t *testing.T) {
	embedded, err := IsEmbedded()
	if err != nil {
		t.Fatalf("IsEmbedded: %v", err)
	}
	if embedded {
		t.Fatal("IsEmbedded() = true for this repo's own checked-in placeholder dist/index.html, want false")
	}
}

func TestExtract_RealCompiledInPlaceholder_ReportsNotEmbedded(t *testing.T) {
	// Exercises the real //go:embed distFS this package actually ships —
	// proves the checked-in dist/index.html placeholder is correctly
	// recognized by the real embed, not just by extractFS's own unit tests
	// against a fake fs.FS.
	destDir := filepath.Join(t.TempDir(), "extracted")
	indexHTML, embedded, err := Extract(destDir)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if embedded {
		t.Fatal("embedded = true for this repo's own checked-in placeholder dist/index.html, want false (no pnpm build was run before this test compiled)")
	}
	if indexHTML != nil {
		t.Fatalf("indexHTML = %q, want nil", indexHTML)
	}
}
