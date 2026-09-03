package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenMigratesAndEnablesForeignKeys(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "agentkit.db")

	store, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer store.Close()

	var migrationCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatalf("query migrations: %v", err)
	}
	if migrationCount != 13 {
		t.Fatalf("migration count = %d, want 13", migrationCount)
	}

	var foreignKeys int
	if err := store.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatalf("query foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeys)
	}
}

func TestMigrationIsIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "agentkit.db")

	first, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}

	second, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	defer second.Close()

	var migrationCount int
	if err := second.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatalf("query migrations: %v", err)
	}
	if migrationCount != 13 {
		t.Fatalf("migration count = %d, want 13", migrationCount)
	}
}

// TestOpen_PragmasActuallyApplyToConnection is V1-04A's own "assert
// WAL/pragma thực sự áp cho connection mới": Open already fails closed via
// verifyDurabilityPragmas if any of these are wrong (every other test in
// this package exercises that path implicitly), but this test re-reads
// each pragma directly and explicitly names what "correct" means instead
// of only trusting Open's own internal check.
func TestOpen_PragmasActuallyApplyToConnection(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "agentkit-pragmas.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	var foreignKeys int
	if err := store.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatalf("read foreign_keys pragma: %v", err)
	}
	if foreignKeys != 1 {
		t.Errorf("foreign_keys = %d, want 1 (enabled)", foreignKeys)
	}

	var journalMode string
	if err := store.db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("read journal_mode pragma: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want wal", journalMode)
	}

	var synchronous int
	if err := store.db.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&synchronous); err != nil {
		t.Fatalf("read synchronous pragma: %v", err)
	}
	if synchronous == sqliteSynchronousOff {
		t.Errorf("synchronous = %d (OFF), must never be lowered to OFF", synchronous)
	}
}

func TestVerifyDurabilityPragmas_AcceptsARealOpenConnection(t *testing.T) {
	ctx := context.Background()
	store := openWithoutMigrating(t, filepath.Join(t.TempDir(), "agentkit-pragma-check.db"))
	defer store.Close()

	if err := verifyDurabilityPragmas(ctx, store.db); err != nil {
		t.Fatalf("verifyDurabilityPragmas on a real connection opened with the same DSN pragmas Open uses: %v", err)
	}
}

func TestVerifyDurabilityPragmas_RejectsSynchronousOff(t *testing.T) {
	ctx := context.Background()
	store := openWithoutMigrating(t, filepath.Join(t.TempDir(), "agentkit-pragma-off.db"))
	defer store.Close()

	if _, err := store.db.ExecContext(ctx, `PRAGMA synchronous = OFF`); err != nil {
		t.Fatalf("lower synchronous to OFF: %v", err)
	}

	err := verifyDurabilityPragmas(ctx, store.db)
	if err == nil {
		t.Fatal("verifyDurabilityPragmas should reject a connection with synchronous=OFF")
	}
}
