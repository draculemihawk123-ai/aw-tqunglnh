package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

// TestMigration47_AddsProfileVersionAndTheExceptionTable proves migration 0047
// (V9-08) on a database migrated through 46: baseline attempts gain a nullable
// profile_version (an attempt written before it vouches for no profile), and
// readiness_baseline_exceptions exists with exactly one exception per attempt.
// The behavior built on both is covered end to end in
// internal/app/readinesscheck/operator_sqlite_test.go.
func TestMigration47_AddsProfileVersionAndTheExceptionTable(t *testing.T) {
	ctx := context.Background()
	store := openWithoutMigrating(t, filepath.Join(t.TempDir(), "agentkit-migration47.db"))
	defer store.Close()
	applyMigrationsThrough(t, ctx, store, 46)

	columns := func(table string) map[string]bool {
		t.Helper()
		rows, err := store.db.QueryContext(ctx, `SELECT name FROM pragma_table_info(?)`, table)
		if err != nil {
			t.Fatalf("table_info(%s): %v", table, err)
		}
		defer rows.Close()
		found := map[string]bool{}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatal(err)
			}
			found[name] = true
		}
		return found
	}
	if columns("readiness_baseline_attempts")["profile_version"] {
		t.Fatal("profile_version exists before migration 47")
	}

	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer conn.Close()
	if err := applyOneMigration(ctx, conn, findMigration(t, 47)); err != nil {
		t.Fatalf("apply migration 47: %v", err)
	}

	if !columns("readiness_baseline_attempts")["profile_version"] {
		t.Fatal("readiness_baseline_attempts has no profile_version after migration 47")
	}
	exceptions := columns("readiness_baseline_exceptions")
	for _, want := range []string{"id", "project_id", "baseline_attempt_id", "repository_workspace_id", "repository_id", "reason", "accepted_by", "accepted_at"} {
		if !exceptions[want] {
			t.Fatalf("readiness_baseline_exceptions has no column %s: %v", want, exceptions)
		}
	}
	var unique int
	if err := store.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM pragma_index_list('readiness_baseline_exceptions') WHERE "unique" = 1 AND origin = 'u'`).Scan(&unique); err != nil {
		t.Fatalf("index_list: %v", err)
	}
	if unique != 1 {
		t.Fatalf("readiness_baseline_exceptions has %d UNIQUE constraints, want the one on baseline_attempt_id", unique)
	}
}
