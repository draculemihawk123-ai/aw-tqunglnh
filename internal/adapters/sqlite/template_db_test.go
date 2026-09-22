package sqlite

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// This file is the pre-migrated template database the package's tests share,
// so the package fits the "Linux race and stability (V0-12)" CI job's
// `go test -race` budget (.github/workflows/spike-gate.yml) without raising
// any timeout.
//
// Why it exists. Store.Open applies every embedded migration to a brand-new
// database file, and every migration commits in its own transaction under
// synchronous(FULL) — dozens of fsync'd commits plus the schema parse of
// every CREATE TABLE. That fixed cost (~150ms/open natively, ~15x under
// -race on modernc.org/sqlite's transpiled C) is paid by almost every test
// in this package, and CPU-profiling the package showed Open/Migrate is ~80%
// of its total CPU. The package's tests are about repositories, not about
// running migrations, so they should not each re-run them.
//
// What it does. The first caller builds ONE database through the real
// Open (real DSN, real pragmas, every real migration), folds its WAL into
// the main file, closes it, and keeps the resulting bytes in memory. Every
// test then gets its own private byte-for-byte copy at a path inside its own
// t.TempDir() and opens THAT with the normal, real Open — which still runs
// the full production path (DSN, pragma verification, Migrate re-reading
// schema_migrations and re-verifying every checksum); it simply finds
// nothing left to apply, exactly like a real process restart.
//
// What it deliberately does NOT cover. Tests whose subject is opening or
// migrating (db_test.go, migrations_test.go, migration_00NN_test.go, the
// seeded-by-migration and schema-shape checks) keep calling Open on a
// brand-new path so they still exercise the real first-open/migrate path.
//
// Isolation. The template is never opened by any test and never handed out;
// only immutable bytes are shared (read-only after the sync.Once below), and
// each call writes them to a fresh path under its own t.TempDir(), so two
// tests — parallel or not, in this process or a child process — can never
// observe each other's writes.

var (
	migratedTemplateOnce  sync.Once
	migratedTemplateBytes []byte
	migratedTemplateErr   error
)

// migratedTemplate returns the bytes of the fully-migrated template
// database, building it on first use. The returned slice is shared and
// MUST NOT be modified.
func migratedTemplate() ([]byte, error) {
	migratedTemplateOnce.Do(func() {
		migratedTemplateBytes, migratedTemplateErr = buildMigratedTemplate()
	})
	return migratedTemplateBytes, migratedTemplateErr
}

// buildMigratedTemplate builds the template through the real Open and proves
// the result is a single self-contained, fully-migrated file before any test
// is allowed to copy it. Every check fails loudly (the first test that asked
// for a template fails with the reason) rather than handing out a database
// that only looks migrated.
func buildMigratedTemplate() ([]byte, error) {
	dir, err := os.MkdirTemp("", "agentkit-sqlite-template-")
	if err != nil {
		return nil, fmt.Errorf("create template directory: %w", err)
	}
	// Best effort: everything of value is copied into memory below.
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "template.db")

	ctx := context.Background()
	store, err := Open(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("open template database: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = store.Close()
		}
	}()

	migrations, err := loadMigrations()
	if err != nil {
		return nil, fmt.Errorf("load migrations: %w", err)
	}
	var applied int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil {
		return nil, fmt.Errorf("count applied migrations: %w", err)
	}
	if applied != len(migrations) {
		return nil, fmt.Errorf("template database has %d applied migrations, want %d", applied, len(migrations))
	}

	// Fold every WAL frame into the main database file and truncate the WAL,
	// so the main file alone is the whole database.
	var busy, logFrames, checkpointed int
	if err := store.db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &checkpointed); err != nil {
		return nil, fmt.Errorf("checkpoint template database: %w", err)
	}
	if busy != 0 || logFrames != checkpointed {
		return nil, fmt.Errorf("template WAL checkpoint incomplete: busy=%d log=%d checkpointed=%d", busy, logFrames, checkpointed)
	}
	var integrity string
	if err := store.db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return nil, fmt.Errorf("integrity check template database: %w", err)
	}
	if !strings.EqualFold(integrity, "ok") {
		return nil, fmt.Errorf("template database integrity_check = %q, want ok", integrity)
	}

	closed = true
	if err := store.Close(); err != nil {
		return nil, fmt.Errorf("close template database: %w", err)
	}

	// After the last connection closes SQLite deletes the WAL; anything left
	// with content would be a committed page the main-file copy below misses.
	if info, err := os.Stat(path + "-wal"); err == nil && info.Size() > 0 {
		return nil, fmt.Errorf("template database left a non-empty WAL (%d bytes) beside its main file", info.Size())
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

// migratedDatabasePath returns a path named name inside t's own private
// temp directory that already holds a byte-for-byte copy of the fully
// migrated template. Callers pass it to the real Open exactly as they would
// a brand-new path: Open still runs its full production path, it just finds
// every migration already applied and re-verifies each checksum instead of
// re-running them.
func migratedDatabasePath(t testing.TB, name string) string {
	t.Helper()
	content, err := migratedTemplate()
	if err != nil {
		t.Fatalf("build pre-migrated template database: %v", err)
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("copy pre-migrated template database to %s: %v", path, err)
	}
	return path
}

// BenchmarkOpenFresh and BenchmarkOpenFromMigratedTemplate reproduce the
// measurement that justifies this file (copy+Open is what every converted
// test now pays instead of a full fresh migrate):
//
//	go test -run '^$' -bench 'BenchmarkOpen(Fresh|FromMigratedTemplate)$' ./internal/adapters/sqlite/
func BenchmarkOpenFresh(b *testing.B) {
	ctx := context.Background()
	for i := 0; i < b.N; i++ {
		store, err := Open(ctx, filepath.Join(b.TempDir(), "fresh.db"))
		if err != nil {
			b.Fatalf("Open: %v", err)
		}
		if err := store.Close(); err != nil {
			b.Fatalf("Close: %v", err)
		}
	}
}

func BenchmarkOpenFromMigratedTemplate(b *testing.B) {
	ctx := context.Background()
	if _, err := migratedTemplate(); err != nil {
		b.Fatalf("build pre-migrated template database: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store, err := Open(ctx, migratedDatabasePath(b, "copy.db"))
		if err != nil {
			b.Fatalf("Open: %v", err)
		}
		if err := store.Close(); err != nil {
			b.Fatalf("Close: %v", err)
		}
	}
}

// openFreshStore is migratedDatabasePath's counterpart for tests whose
// SUBJECT is what the migrations produce (schema shape, rows a migration
// seeds): it opens a brand-new path, so the real Open applies every
// migration inside that very test instead of reusing the template.
func openFreshStore(t testing.TB, name string) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
