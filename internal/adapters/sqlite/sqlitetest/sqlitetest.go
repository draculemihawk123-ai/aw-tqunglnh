// Package sqlitetest gives tests outside internal/adapters/sqlite the same pre-migrated template database the sqlite
// package's own tests use (template_db_test.go, PR #84). Import it from _test.go files only.
//
// Why. sqlite.Open applies every embedded migration to a brand-new file, and each fresh migrate costs more with every
// migration added (whole-schema reparse, ALTER TABLE RENAME of table-rebuild migrations, one fsync'd commit each).
// Under `go test -race` that is about 4 seconds per fresh open on CI, and a full `go test ./...` made about 1,100 of
// them: most of the "Linux race detector (V0-12 part)" job's time, which reached its 35-minute cap in November 2026.
// A test about a repository, a handler or a CLI command does not need to re-run migrations to prove anything.
//
// What it does. The first call in a test binary builds ONE database through the real sqlite.Open (real DSN, pragmas
// and migrations), closes it — the last close folds the WAL into the main file and removes it — and keeps the bytes.
// MigratedPath writes a private copy of those bytes under the caller's own t.TempDir(); the caller passes that path to
// the real sqlite.Open, which runs its full production path and simply finds every migration already applied, exactly
// like a process restart. Only immutable bytes are shared, so tests (parallel or not) never see each other's writes.
//
// Not for tests whose subject is a first open or migration: those keep opening a brand-new path.
package sqlitetest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
)

var (
	templateOnce  sync.Once
	templateBytes []byte
	templateErr   error
)

func template() ([]byte, error) {
	templateOnce.Do(func() { templateBytes, templateErr = buildTemplate() })
	return templateBytes, templateErr
}

func buildTemplate() ([]byte, error) {
	dir, err := os.MkdirTemp("", "agentkit-sqlitetest-template-")
	if err != nil {
		return nil, fmt.Errorf("create template directory: %w", err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "template.db")
	store, err := sqlite.Open(context.Background(), path)
	if err != nil {
		return nil, fmt.Errorf("open template database: %w", err)
	}
	if err := store.Close(); err != nil {
		return nil, fmt.Errorf("close template database: %w", err)
	}
	// After the last connection closes SQLite checkpoints and deletes the WAL; a WAL left with content would hold
	// committed pages the main-file copy below would miss.
	if info, err := os.Stat(path + "-wal"); err == nil && info.Size() > 0 {
		return nil, fmt.Errorf("template database left a non-empty WAL (%d bytes)", info.Size())
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("stat template WAL: %w", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read template database: %w", err)
	}
	if len(content) == 0 {
		return nil, errors.New("template database file is empty")
	}
	return content, nil
}

// MigratedPath returns a path named name inside t's own temp directory that already holds a copy of the fully migrated
// template. Pass it to sqlite.Open (or as an `aw --db`) exactly as a brand-new path.
func MigratedPath(t testing.TB, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	WriteMigrated(t, path)
	return path
}

// WriteMigrated writes a copy of the fully migrated template at path, for a test whose database must sit at a given
// place (for example inside a directory the test later scans). path must not exist yet.
func WriteMigrated(t testing.TB, path string) {
	t.Helper()
	content, err := template()
	if err != nil {
		t.Fatalf("build pre-migrated template database: %v", err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("copy pre-migrated template database to %s: %v", path, err)
	}
}
