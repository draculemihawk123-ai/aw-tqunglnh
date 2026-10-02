package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

// TestMigration45_UpgradeWithRealData_PreservesBlockersAndAcceptsRunFailed
// proves migration 0045 (V9-06, ADR-033) on a database that already holds
// blockers written before it, mirroring migration_0043_test.go: apply every
// migration through 44, seed one OPEN RUN_CANCELLED blocker, one RESOLVED
// SCOPE_EXPANSION_REQUIRED blocker carrying all three source ids and one WAIVED
// COMPLETION_POLICY_FAILED blocker carrying a decision artifact, apply
// migration 45, then prove every row survived column for column, the
// work-item index was recreated, no foreign key is violated, the widened CHECK
// accepts RUN_FAILED and an unknown type, and a state outside the three, are
// still rejected.
func TestMigration45_UpgradeWithRealData_PreservesBlockersAndAcceptsRunFailed(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "agentkit-migration45-upgrade.db")
	store := openWithoutMigrating(t, databasePath)
	defer store.Close()
	applyMigrationsThrough(t, ctx, store, 44)

	const projectID, familyID, workItemID = "p1", "f1", "wi1"
	if err := SeedFixtureOwners(ctx, store, projectID, familyID, workItemID); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	// Two run/node-run/attempt chains: blockers carry real foreign keys into
	// workflow_runs, node_runs, execution_attempts and decision_artifacts.
	for _, attemptID := range []string{"attempt-a", "attempt-b"} {
		if err := SeedFixtureExecutionAttempt(ctx, store, projectID, familyID, workItemID, attemptID); err != nil {
			t.Fatalf("SeedFixtureExecutionAttempt(%s): %v", attemptID, err)
		}
	}

	const openedAt = "2026-10-02T00:00:00Z"
	const resolvedAt = "2026-10-02T01:00:00Z"
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO decision_artifacts (id, project_id, kind, policy_version, input_json, result_json, created_at)
VALUES ('waiver-1', ?, 'WorkItemBlockerWaiver', 'grant-1', '{}', '{}', ?);`, projectID, openedAt); err != nil {
		t.Fatalf("seed decision artifact: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO blockers (id, project_id, work_item_id, type, state, source_run_id, source_node_run_id, source_attempt_id,
                      reason, opened_at, resolved_at, resolved_by, resolution_note, decision_artifact_id, version)
VALUES
  ('b-cancelled', ?, ?, 'RUN_CANCELLED', 'OPEN', 'wf-run-attempt-a', NULL, NULL,
   'workflow run wf-run-attempt-a was cancelled', ?, NULL, NULL, NULL, NULL, 1),
  ('b-scope', ?, ?, 'SCOPE_EXPANSION_REQUIRED', 'RESOLVED', 'wf-run-attempt-b', 'node-run-attempt-b', 'attempt-b',
   'scope expansion', ?, ?, 'operator-1', 'approved', NULL, 3),
  ('b-policy', ?, ?, 'COMPLETION_POLICY_FAILED', 'WAIVED', 'wf-run-attempt-a', NULL, NULL,
   'completion policy failed', ?, ?, 'operator-2', 'waived by grant', 'waiver-1', 4);`,
		projectID, workItemID, openedAt,
		projectID, workItemID, openedAt, resolvedAt,
		projectID, workItemID, openedAt, resolvedAt); err != nil {
		t.Fatalf("seed blockers: %v", err)
	}

	type row struct {
		workItemID, blockerType, state, sourceRun, sourceNodeRun, sourceAttempt    string
		reason, openedAt, resolvedAt, resolvedBy, resolutionNote, decisionArtifact string
		version                                                                    int
	}
	snapshot := func() map[string]row {
		t.Helper()
		rows, err := store.db.QueryContext(ctx, `
SELECT id, work_item_id, type, state, COALESCE(source_run_id, ''), COALESCE(source_node_run_id, ''), COALESCE(source_attempt_id, ''),
       reason, opened_at, COALESCE(resolved_at, ''), COALESCE(resolved_by, ''), COALESCE(resolution_note, ''),
       COALESCE(decision_artifact_id, ''), version
FROM blockers`)
		if err != nil {
			t.Fatalf("snapshot blockers: %v", err)
		}
		defer rows.Close()
		out := map[string]row{}
		for rows.Next() {
			var id string
			var r row
			if err := rows.Scan(&id, &r.workItemID, &r.blockerType, &r.state, &r.sourceRun, &r.sourceNodeRun, &r.sourceAttempt,
				&r.reason, &r.openedAt, &r.resolvedAt, &r.resolvedBy, &r.resolutionNote, &r.decisionArtifact, &r.version); err != nil {
				t.Fatalf("scan blocker: %v", err)
			}
			out[id] = r
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("iterate blockers: %v", err)
		}
		return out
	}
	before := snapshot()
	if len(before) != 3 {
		t.Fatalf("seeded blocker count = %d, want 3", len(before))
	}

	// Before the migration RUN_FAILED is rejected by the old CHECK — the exact
	// reason a failed Run could never open a blocker.
	const insertRunFailed = `
INSERT INTO blockers (id, project_id, work_item_id, type, state, source_run_id, reason, opened_at, version)
VALUES (?, 'p1', 'wi1', 'RUN_FAILED', 'OPEN', 'wf-run-attempt-b', 'workflow run wf-run-attempt-b failed (RUN_FAILED)', ?, 1)`
	if _, err := store.db.ExecContext(ctx, insertRunFailed, "b-failed-pre", openedAt); err == nil {
		t.Fatal("the pre-migration CHECK accepted RUN_FAILED; the migration would be unnecessary")
	}

	m45 := findMigration(t, 45)
	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer conn.Close()
	if err := applyOneMigration(ctx, conn, m45); err != nil {
		t.Fatalf("apply migration 45: %v", err)
	}

	after := snapshot()
	if len(after) != len(before) {
		t.Fatalf("blocker count after migration 45 = %d, want unchanged %d", len(after), len(before))
	}
	for id, want := range before {
		if after[id] != want {
			t.Fatalf("blocker %s after migration 45 = %+v, want unchanged %+v", id, after[id], want)
		}
	}

	var indexes int
	if err := store.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND tbl_name = 'blockers' AND name = 'idx_blockers_work_item'`).Scan(&indexes); err != nil {
		t.Fatalf("count indexes: %v", err)
	}
	if indexes != 1 {
		t.Fatalf("idx_blockers_work_item after migration 45 = %d, want 1", indexes)
	}
	var leftover int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name = 'blockers_new'`).Scan(&leftover); err != nil {
		t.Fatalf("count leftover rebuild table: %v", err)
	}
	if leftover != 0 {
		t.Fatalf("the rebuild left blockers_new behind (%d)", leftover)
	}

	fkRows, err := store.db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	violations := 0
	for fkRows.Next() {
		violations++
	}
	fkRows.Close()
	if violations != 0 {
		t.Fatalf("foreign_key_check violations after migration 45 = %d, want 0", violations)
	}

	// The widened CHECK accepts RUN_FAILED...
	if _, err := store.db.ExecContext(ctx, insertRunFailed, "b-failed", openedAt); err != nil {
		t.Fatalf("RUN_FAILED should be accepted by the widened CHECK: %v", err)
	}
	// ...the foreign keys the rebuilt table kept still bite...
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO blockers (id, project_id, work_item_id, type, state, source_run_id, reason, opened_at, version)
VALUES ('b-dangling', 'p1', 'wi1', 'RUN_FAILED', 'OPEN', 'no-such-run', 'dangling', ?, 1)`, openedAt); err == nil {
		t.Fatal("the rebuilt table accepted a source_run_id that references no workflow run")
	}
	// ...but an unknown type and an unknown state are still rejected.
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO blockers (id, project_id, work_item_id, type, state, reason, opened_at, version)
VALUES ('b-bogus', 'p1', 'wi1', 'BOGUS', 'OPEN', 'bogus', ?, 1)`, openedAt); err == nil {
		t.Fatal("an unknown blocker type should still be rejected by the CHECK constraint")
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO blockers (id, project_id, work_item_id, type, state, reason, opened_at, version)
VALUES ('b-bogus-state', 'p1', 'wi1', 'RUN_FAILED', 'BOGUS', 'bogus', ?, 1)`, openedAt); err == nil {
		t.Fatal("an unknown blocker state should still be rejected by the CHECK constraint")
	}
	// The pre-existing types remain valid.
	for _, blockerType := range []string{
		"RUN_CANCELLED", "COMPLETION_POLICY_FAILED", "SCOPE_EXPANSION_REQUIRED", "ISOLATION_ENFORCEMENT_UNAVAILABLE",
		"ADAPTER_BUILD_DRIFT", "CAPABILITY_REQUIREMENT_UNSATISFIED", "WRITE_CAPABILITY_OR_GRANT_MISSING",
	} {
		if _, err := store.db.ExecContext(ctx, `
INSERT INTO blockers (id, project_id, work_item_id, type, state, reason, opened_at, version)
VALUES (?, 'p1', 'wi1', ?, 'OPEN', 'still valid', ?, 1)`, "b-type-"+blockerType, blockerType, openedAt); err != nil {
			t.Fatalf("type %s should still be accepted after migration 45: %v", blockerType, err)
		}
	}
}
