package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

// TestMigration48_AddsTheInstructionFilesColumn proves migration 0048 (V9-10) on
// a database migrated through 47: attempt_context_snapshots gains a nullable
// repository_instruction_files_json, so a snapshot written before it loads with
// none and recomputes the hash it was written with (the round trip and the
// tamper check are in contextsnapshot_repository_test.go).
func TestMigration48_AddsTheInstructionFilesColumn(t *testing.T) {
	ctx := context.Background()
	store := openWithoutMigrating(t, filepath.Join(t.TempDir(), "agentkit-migration48.db"))
	defer store.Close()
	applyMigrationsThrough(t, ctx, store, 47)

	hasColumn := func() bool {
		t.Helper()
		var n int
		if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('attempt_context_snapshots') WHERE name = 'repository_instruction_files_json'`).Scan(&n); err != nil {
			t.Fatalf("table_info: %v", err)
		}
		return n == 1
	}
	if hasColumn() {
		t.Fatal("the column exists before migration 48")
	}
	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer conn.Close()
	if err := applyOneMigration(ctx, conn, findMigration(t, 48)); err != nil {
		t.Fatalf("apply migration 48: %v", err)
	}
	if !hasColumn() {
		t.Fatal("attempt_context_snapshots has no repository_instruction_files_json after migration 48")
	}
}
