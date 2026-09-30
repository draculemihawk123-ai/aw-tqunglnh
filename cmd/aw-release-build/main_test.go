package main

// This file deliberately does NOT exercise run() against this repo's own
// real --repo-root: run() replaces internal/adapters/embeddedui/dist's own
// contents on disk before invoking `go build`, and `go test ./...` runs
// different packages' tests concurrently by default — a test here mutating
// that shared source directory could race with an unrelated package's own
// `go build`/`go test` reading it at the same time. The real end-to-end
// path (embed a fixture UI, build, checksum, run `version --json`, confirm
// uiEmbedded=true, serve the embedded UI over real HTTP) was verified
// manually against an isolated fixture --ui-dist and this repo's own real
// --repo-root, then restored — see baocaov8checklist.md's own V8-08
// section for that real run's captured output. This file covers everything
// safely testable in isolation instead: the pure directory-replacement
// helper and run()'s own flag validation (both exit before touching any
// repo path).

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceDir_ReplacesContentsPreservingSubtree(t *testing.T) {
	destDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(destDir, "stale.txt"), []byte("old"), 0o644); err != nil {
		t.Fatalf("seed stale file: %v", err)
	}

	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "index.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatalf("write src index.html: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(srcDir, "assets", "nested"), 0o755); err != nil {
		t.Fatalf("mkdir src assets/nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "assets", "app.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatalf("write src assets/app.js: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "assets", "nested", "x.css"), []byte("body{}"), 0o644); err != nil {
		t.Fatalf("write src assets/nested/x.css: %v", err)
	}

	if err := replaceDir(destDir, srcDir); err != nil {
		t.Fatalf("replaceDir: %v", err)
	}

	if _, err := os.Stat(filepath.Join(destDir, "stale.txt")); !os.IsNotExist(err) {
		t.Fatalf("stale.txt still exists after replaceDir, err=%v", err)
	}
	index, err := os.ReadFile(filepath.Join(destDir, "index.html"))
	if err != nil || string(index) != "<html></html>" {
		t.Fatalf("destDir/index.html = %q, err=%v, want the src content", index, err)
	}
	nested, err := os.ReadFile(filepath.Join(destDir, "assets", "nested", "x.css"))
	if err != nil || string(nested) != "body{}" {
		t.Fatalf("destDir/assets/nested/x.css = %q, err=%v, want the src content", nested, err)
	}
}

func TestRun_RequiresUIDist(t *testing.T) {
	var stdout bytes.Buffer
	err := run([]string{"--out", filepath.Join(t.TempDir(), "aw")}, &stdout)
	if err == nil {
		t.Fatal("run() with no --ui-dist succeeded, want an error")
	}
}

func TestRun_RequiresOut(t *testing.T) {
	var stdout bytes.Buffer
	err := run([]string{"--ui-dist", t.TempDir()}, &stdout)
	if err == nil {
		t.Fatal("run() with no --out succeeded, want an error")
	}
}
