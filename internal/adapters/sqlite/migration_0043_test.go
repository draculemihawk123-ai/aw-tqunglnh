package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

// TestMigration43_UpgradeWithRealData_PreservesRowsAndWidensCheck mirrors
// migration_0032_test.go's real-data rebuild proof for V9-09's migration
// (release_set_local_commits.failure_reason gains NO_CHANGES): apply every
// migration through 42, seed a release set plus one COMMITTED and one
// FAILED/MARKER_DRIFT local-commit row, apply migration 43, then prove every
// row survived byte-for-byte, both indexes were recreated, no foreign key is
// violated, the widened CHECK accepts NO_CHANGES and an unknown reason is
// still rejected.
func TestMigration43_UpgradeWithRealData_PreservesRowsAndWidensCheck(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "agentkit-migration43-upgrade.db")
	store := openWithoutMigrating(t, databasePath)
	defer store.Close()
	applyMigrationsThrough(t, ctx, store, 42)

	const projectID, familyID, workItemID = "p1", "f1", "wi1"
	if err := SeedFixtureOwners(ctx, store, projectID, familyID, workItemID); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	if err := SeedFixtureRepositoryWorkspace(ctx, store, projectID, familyID, "set-1", "repo-1", "rw-1"); err != nil {
		t.Fatalf("SeedFixtureRepositoryWorkspace: %v", err)
	}

	const now = "2026-10-01T00:00:00Z"
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO release_sets (id, project_id, family_id, state, content_hash, created_at, version)
VALUES ('rs-1', ?, ?, 'CREATED', 'sha256:abc', ?, 1);`, projectID, familyID, now); err != nil {
		t.Fatalf("seed release_sets row: %v", err)
	}
	insert := func(id, marker, state, reason, result string) {
		t.Helper()
		var reasonValue, resultValue any
		if reason != "" {
			reasonValue = reason
		}
		if result != "" {
			resultValue = result
		}
		if _, err := store.db.ExecContext(ctx, `
INSERT INTO release_set_local_commits (
    id, project_id, release_set_id, expected_release_set_version, repository_workspace_id, repository_id,
    expected_generation, expected_workspace_version, actor, message, author_name, author_email, message_hash,
    marker, state, failure_reason, parent_vcs_object_id, result_vcs_object_id, job_id, created_at, completed_at, version)
VALUES (?, ?, 'rs-1', 1, 'rw-1', 'repo-1', 1, 1, 'actor-1', 'msg', 'Bot', 'bot@example.invalid', 'sha256:m',
        ?, ?, ?, 'parent-1', ?, 'job-1', ?, ?, 3);`,
			id, projectID, marker, state, reasonValue, resultValue, now, now); err != nil {
			t.Fatalf("seed release_set_local_commits %s: %v", id, err)
		}
	}
	insert("lc-committed", "marker-committed", "COMMITTED", "", "result-1")
	insert("lc-drift", "marker-drift", "FAILED", "MARKER_DRIFT", "")

	type row struct{ state, reason, parent, result, marker, completed string }
	snapshot := func() map[string]row {
		t.Helper()
		rows, err := store.db.QueryContext(ctx, `
SELECT id, state, COALESCE(failure_reason, ''), COALESCE(parent_vcs_object_id, ''), COALESCE(result_vcs_object_id, ''),
       marker, COALESCE(completed_at, '') FROM release_set_local_commits`)
		if err != nil {
			t.Fatalf("snapshot rows: %v", err)
		}
		defer rows.Close()
		out := map[string]row{}
		for rows.Next() {
			var id string
			var r row
			if err := rows.Scan(&id, &r.state, &r.reason, &r.parent, &r.result, &r.marker, &r.completed); err != nil {
				t.Fatalf("scan: %v", err)
			}
			out[id] = r
		}
		return out
	}
	before := snapshot()
	if len(before) != 2 {
		t.Fatalf("seeded row count = %d, want 2", len(before))
	}

	// Before the migration NO_CHANGES is rejected by the old CHECK — the
	// exact reason the worker could never record the typed outcome.
	if _, err := store.db.ExecContext(ctx,
		`UPDATE release_set_local_commits SET state = 'FAILED', failure_reason = 'NO_CHANGES' WHERE id = 'lc-committed'`); err == nil {
		t.Fatal("the pre-migration CHECK accepted NO_CHANGES; the migration would be unnecessary")
	}

	m43 := findMigration(t, 43)
	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer conn.Close()
	if err := applyOneMigration(ctx, conn, m43); err != nil {
		t.Fatalf("apply migration 43: %v", err)
	}

	after := snapshot()
	if len(after) != len(before) {
		t.Fatalf("row count after migration 43 = %d, want unchanged %d", len(after), len(before))
	}
	for id, want := range before {
		if after[id] != want {
			t.Fatalf("row %s after migration 43 = %+v, want unchanged %+v", id, after[id], want)
		}
	}

	var indexes int
	if err := store.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND tbl_name = 'release_set_local_commits'
  AND name IN ('idx_release_set_local_commits_release_set', 'idx_release_set_local_commits_workspace')`).Scan(&indexes); err != nil {
		t.Fatalf("count indexes: %v", err)
	}
	if indexes != 2 {
		t.Fatalf("release_set_local_commits indexes after migration 43 = %d, want 2", indexes)
	}

	rows, err := store.db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	violations := 0
	for rows.Next() {
		violations++
	}
	rows.Close()
	if violations != 0 {
		t.Fatalf("foreign_key_check violations after migration 43 = %d, want 0", violations)
	}

	// The widened CHECK accepts NO_CHANGES (on a REQUESTED row this is the
	// shape TransitionReleaseSetLocalCommitToFailed writes)...
	insert("lc-requested", "marker-requested", "REQUESTED", "", "")
	if _, err := store.db.ExecContext(ctx,
		`UPDATE release_set_local_commits SET state = 'FAILED', failure_reason = 'NO_CHANGES', version = version + 1 WHERE id = 'lc-requested'`); err != nil {
		t.Fatalf("FAILED/NO_CHANGES should be accepted by the widened CHECK: %v", err)
	}
	// ...but an unknown reason is still rejected.
	if _, err := store.db.ExecContext(ctx,
		`UPDATE release_set_local_commits SET failure_reason = 'BOGUS' WHERE id = 'lc-drift'`); err == nil {
		t.Fatal("an unknown failure_reason should still be rejected by the CHECK constraint")
	}
}
