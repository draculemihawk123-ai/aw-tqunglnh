package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

// TestMigration50_UpgradeWithRealData_PreservesRowsAndAddsSelectionAndReleaseFailed proves
// migration 0050 (V11-07, ADR-040) on a database that already holds rows written before it,
// mirroring migration_0049_test.go: apply every migration through 49, seed a release set with
// local-commit rows in three states and blockers in three states, apply migration 50, then prove
// every row survived column for column, the indexes were recreated, no foreign key is violated,
// the widened CHECKs accept CHANGESET_DRIFT / RELEASE_FAILED and still reject unknown values,
// and the new selections table works (one row per operation, KEEP/PARK only).
func TestMigration50_UpgradeWithRealData_PreservesRowsAndAddsSelectionAndReleaseFailed(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "agentkit-migration50-upgrade.db")
	store := openWithoutMigrating(t, databasePath)
	defer store.Close()
	applyMigrationsThrough(t, ctx, store, 49)

	const projectID, familyID, workItemID = "p1", "f1", "wi1"
	if err := SeedFixtureOwners(ctx, store, projectID, familyID, workItemID); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	if err := SeedFixtureRepositoryWorkspace(ctx, store, projectID, familyID, "set-1", "repo-1", "rw-1"); err != nil {
		t.Fatalf("SeedFixtureRepositoryWorkspace: %v", err)
	}
	if err := SeedFixtureExecutionAttempt(ctx, store, projectID, familyID, workItemID, "attempt-a"); err != nil {
		t.Fatalf("SeedFixtureExecutionAttempt: %v", err)
	}

	const now = "2026-10-10T00:00:00Z"
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO release_sets (id, project_id, family_id, state, content_hash, created_at, version)
VALUES ('rs-1', ?, ?, 'CREATED', 'sha256:abc', ?, 1);`, projectID, familyID, now); err != nil {
		t.Fatalf("seed release_sets row: %v", err)
	}
	insertOp := func(id, marker, state, reason, result string) {
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
	insertOp("lc-committed", "marker-committed", "COMMITTED", "", "result-1")
	insertOp("lc-head", "marker-head", "FAILED", "HEAD_DRIFT", "")
	insertOp("lc-nochanges", "marker-nochanges", "FAILED", "NO_CHANGES", "")

	if _, err := store.db.ExecContext(ctx, `
INSERT INTO blockers (id, project_id, work_item_id, type, state, source_run_id, reason, opened_at, resolved_at, resolved_by, resolution_note, version)
VALUES
  ('b-failed', ?, ?, 'RUN_FAILED', 'OPEN', 'wf-run-attempt-a', 'run failed', ?, NULL, NULL, NULL, 1),
  ('b-cancelled', ?, ?, 'RUN_CANCELLED', 'RESOLVED', 'wf-run-attempt-a', 'run cancelled', ?, ?, 'operator-1', 'ok', 2);`,
		projectID, workItemID, now, projectID, workItemID, now, now); err != nil {
		t.Fatalf("seed blockers: %v", err)
	}

	type opRow struct{ state, reason, parent, result, marker, completed string }
	snapshotOps := func() map[string]opRow {
		t.Helper()
		rows, err := store.db.QueryContext(ctx, `
SELECT id, state, COALESCE(failure_reason, ''), COALESCE(parent_vcs_object_id, ''), COALESCE(result_vcs_object_id, ''),
       marker, COALESCE(completed_at, '') FROM release_set_local_commits`)
		if err != nil {
			t.Fatalf("snapshot ops: %v", err)
		}
		defer rows.Close()
		out := map[string]opRow{}
		for rows.Next() {
			var id string
			var r opRow
			if err := rows.Scan(&id, &r.state, &r.reason, &r.parent, &r.result, &r.marker, &r.completed); err != nil {
				t.Fatalf("scan op: %v", err)
			}
			out[id] = r
		}
		return out
	}
	type blockerRow struct{ typ, state, source, reason, resolvedBy, note string }
	snapshotBlockers := func() map[string]blockerRow {
		t.Helper()
		rows, err := store.db.QueryContext(ctx, `
SELECT id, type, state, COALESCE(source_run_id, ''), reason, COALESCE(resolved_by, ''), COALESCE(resolution_note, '') FROM blockers`)
		if err != nil {
			t.Fatalf("snapshot blockers: %v", err)
		}
		defer rows.Close()
		out := map[string]blockerRow{}
		for rows.Next() {
			var id string
			var r blockerRow
			if err := rows.Scan(&id, &r.typ, &r.state, &r.source, &r.reason, &r.resolvedBy, &r.note); err != nil {
				t.Fatalf("scan blocker: %v", err)
			}
			out[id] = r
		}
		return out
	}
	opsBefore, blockersBefore := snapshotOps(), snapshotBlockers()
	if len(opsBefore) != 3 || len(blockersBefore) != 2 {
		t.Fatalf("seeded %d operations and %d blockers, want 3 and 2", len(opsBefore), len(blockersBefore))
	}

	// The old CHECKs reject the new values: the reason the migration exists.
	if _, err := store.db.ExecContext(ctx,
		`UPDATE release_set_local_commits SET state = 'FAILED', failure_reason = 'CHANGESET_DRIFT' WHERE id = 'lc-committed'`); err == nil {
		t.Fatal("the pre-migration CHECK accepted CHANGESET_DRIFT; the migration would be unnecessary")
	}
	const insertReleaseFailed = `
INSERT INTO blockers (id, project_id, work_item_id, type, state, source_run_id, reason, opened_at, version)
VALUES (?, 'p1', 'wi1', 'RELEASE_FAILED', 'OPEN', 'wf-run-attempt-a', 'release failed', ?, 1)`
	if _, err := store.db.ExecContext(ctx, insertReleaseFailed, "b-release-pre", now); err == nil {
		t.Fatal("the pre-migration CHECK accepted RELEASE_FAILED; the migration would be unnecessary")
	}

	m50 := findMigration(t, 50)
	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer conn.Close()
	if err := applyOneMigration(ctx, conn, m50); err != nil {
		t.Fatalf("apply migration 50: %v", err)
	}

	for id, want := range opsBefore {
		if got := snapshotOps()[id]; got != want {
			t.Fatalf("operation %s after migration 50 = %+v, want unchanged %+v", id, got, want)
		}
	}
	for id, want := range blockersBefore {
		if got := snapshotBlockers()[id]; got != want {
			t.Fatalf("blocker %s after migration 50 = %+v, want unchanged %+v", id, got, want)
		}
	}

	var indexes int
	if err := store.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name IN (
  'idx_release_set_local_commits_release_set', 'idx_release_set_local_commits_workspace',
  'idx_blockers_work_item', 'idx_release_set_local_commit_selections_approval')`).Scan(&indexes); err != nil {
		t.Fatalf("count indexes: %v", err)
	}
	if indexes != 4 {
		t.Fatalf("indexes after migration 50 = %d, want 4", indexes)
	}
	violations := 0
	rows, err := store.db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	for rows.Next() {
		violations++
	}
	rows.Close()
	if violations != 0 {
		t.Fatalf("foreign_key_check violations after migration 50 = %d, want 0", violations)
	}

	// Widened CHECKs accept the new values and still reject unknown ones.
	if _, err := store.db.ExecContext(ctx,
		`UPDATE release_set_local_commits SET state = 'FAILED', failure_reason = 'CHANGESET_DRIFT', version = version + 1 WHERE id = 'lc-committed'`); err != nil {
		t.Fatalf("FAILED/CHANGESET_DRIFT should be accepted: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE release_set_local_commits SET failure_reason = 'BOGUS' WHERE id = 'lc-head'`); err == nil {
		t.Fatal("an unknown failure_reason should still be rejected")
	}
	if _, err := store.db.ExecContext(ctx, insertReleaseFailed, "b-release", now); err != nil {
		t.Fatalf("RELEASE_FAILED should be accepted: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO blockers (id, project_id, work_item_id, type, state, reason, opened_at, version)
VALUES ('b-bogus', 'p1', 'wi1', 'BOGUS', 'OPEN', 'x', ?, 1)`, now); err == nil {
		t.Fatal("an unknown blocker type should still be rejected")
	}

	// The selections table: one row per operation, KEEP/PARK only, the operation must exist.
	insertSelection := func(opID, unselected string) error {
		_, err := store.db.ExecContext(ctx, `
INSERT INTO release_set_local_commit_selections (release_set_local_commit_id, files_json, change_set_digest, unselected, created_at)
VALUES (?, '[{"path":"a.txt"}]', 'digest', ?, ?)`, opID, unselected, now)
		return err
	}
	if err := insertSelection("lc-nochanges", "PARK"); err != nil {
		t.Fatalf("insert selection: %v", err)
	}
	if err := insertSelection("lc-nochanges", "KEEP"); err == nil {
		t.Fatal("a second selection for the same operation should be rejected")
	}
	if err := insertSelection("lc-head", "DELETE"); err == nil {
		t.Fatal("an unselected handling other than KEEP/PARK should be rejected")
	}
	if err := insertSelection("lc-does-not-exist", "KEEP"); err == nil {
		t.Fatal("a selection for a missing operation should be rejected by the foreign key")
	}
}
