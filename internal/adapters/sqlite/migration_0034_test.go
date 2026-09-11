package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

// TestMigration34_UpgradeWithRealData_PreservesRowsAndWidensCheck mirrors
// migration_0025_test.go's own identical real-data rebuild proof, this
// time for ARTIFACT_SWEEP (V5-14) joining the same two CHECK constraints
// migration 25 first widened for RECOVERY_REAPER.
func TestMigration34_UpgradeWithRealData_PreservesRowsAndWidensCheck(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "agentkit-migration34-upgrade.db")
	store := openWithoutMigrating(t, databasePath)
	defer store.Close()
	applyMigrationsThrough(t, ctx, store, 33)

	const projectID, familyID, workItemID = "p1", "f1", "wi1"
	if err := SeedFixtureOwners(ctx, store, projectID, familyID, workItemID); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}

	const now = "2026-09-11T00:00:00Z"
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO durable_jobs(id, project_id, kind, aggregate_type, aggregate_id, payload_json, state, available_at, max_claims, idempotency_key, version, created_at, updated_at, job_class)
VALUES ('job-plain-1', ?, 'ADVANCE_RUN', 'WorkflowRun', 'run-x', '{}', 'AVAILABLE', ?, 3, 'idem-plain-1', 1, ?, ?, 'RUN_WORK');`,
		projectID, now, now, now,
	); err != nil {
		t.Fatalf("seed plain durable_jobs row: %v", err)
	}

	var beforeCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM durable_jobs`).Scan(&beforeCount); err != nil {
		t.Fatalf("count durable_jobs before: %v", err)
	}
	if beforeCount != 1 {
		t.Fatalf("seeded durable_jobs row count = %d, want 1", beforeCount)
	}

	m34 := findMigration(t, 34)
	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer conn.Close()
	if err := applyOneMigration(ctx, conn, m34); err != nil {
		t.Fatalf("apply migration 34: %v", err)
	}

	var fk int
	if err := store.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatalf("query foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Fatalf("foreign_keys pragma = %d after migration 34, want 1 (re-enabled)", fk)
	}

	var afterCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM durable_jobs`).Scan(&afterCount); err != nil {
		t.Fatalf("count durable_jobs after: %v", err)
	}
	if afterCount != 1 {
		t.Fatalf("durable_jobs row count after migration 34 = %d, want unchanged 1", afterCount)
	}

	var plainJobProjectID string
	if err := store.db.QueryRowContext(ctx, `SELECT project_id FROM durable_jobs WHERE id = 'job-plain-1'`).Scan(&plainJobProjectID); err != nil {
		t.Fatalf("read job-plain-1 project_id after rebuild: %v", err)
	}
	if plainJobProjectID != projectID {
		t.Fatalf("job-plain-1 project_id after rebuild = %q, want unchanged %q", plainJobProjectID, projectID)
	}

	var violations int
	rows, err := store.db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatalf("foreign_key_check after commit: %v", err)
	}
	for rows.Next() {
		violations++
	}
	rows.Close()
	if violations != 0 {
		t.Fatalf("foreign_key_check violations after migration 34 = %d, want 0", violations)
	}

	// The widened CHECK now accepts ARTIFACT_SWEEP as CONTROL, but ONLY
	// with project_id/run_id/cancel_epoch all NULL.
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO durable_jobs(id, project_id, kind, aggregate_type, aggregate_id, payload_json, state, available_at, max_claims, idempotency_key, version, created_at, updated_at, job_class)
VALUES ('job-sweep-1', NULL, 'ARTIFACT_SWEEP', 'ArtifactSweep', 'singleton', '{}', 'AVAILABLE', ?, 3, 'idem-sweep-1', 1, ?, ?, 'CONTROL');`,
		now, now, now,
	); err != nil {
		t.Fatalf("insert ARTIFACT_SWEEP+CONTROL+NULL project/run/cancel_epoch should be accepted by the widened CHECK: %v", err)
	}
	// ...but rejects an ARTIFACT_SWEEP row that carries a real ProjectID.
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO durable_jobs(id, project_id, kind, aggregate_type, aggregate_id, payload_json, state, available_at, max_claims, idempotency_key, version, created_at, updated_at, job_class)
VALUES ('job-sweep-with-project', ?, 'ARTIFACT_SWEEP', 'ArtifactSweep', 'singleton', '{}', 'AVAILABLE', ?, 3, 'idem-sweep-with-project', 1, ?, ?, 'CONTROL');`,
		projectID, now, now, now,
	); err == nil {
		t.Fatal("insert ARTIFACT_SWEEP+CONTROL with a non-NULL project_id should be rejected by the CHECK constraint, want error")
	}
	// ...and still rejects the wrong-direction pairing.
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO durable_jobs(id, project_id, kind, aggregate_type, aggregate_id, payload_json, state, available_at, max_claims, idempotency_key, version, created_at, updated_at, job_class)
VALUES ('job-sweep-2', ?, 'ARTIFACT_SWEEP', 'ArtifactSweep', 'singleton', '{}', 'AVAILABLE', ?, 3, 'idem-sweep-2', 1, ?, ?, 'RUN_WORK');`,
		projectID, now, now, now,
	); err == nil {
		t.Fatal("insert ARTIFACT_SWEEP+RUN_WORK should be rejected by the CHECK constraint, want error")
	}
}

// TestMigration34_FailureMidRebuild_OldTableIntactNoRecord mirrors
// migration_0025_test.go's own identical sabotage proof.
func TestMigration34_FailureMidRebuild_OldTableIntactNoRecord(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "agentkit-migration34-fail.db")
	store := openWithoutMigrating(t, databasePath)
	defer store.Close()
	applyMigrationsThrough(t, ctx, store, 33)

	if _, err := store.db.ExecContext(ctx, `CREATE TABLE durable_jobs_new (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("sabotage durable_jobs_new: %v", err)
	}

	m34 := findMigration(t, 34)
	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer conn.Close()
	if err := applyOneMigration(ctx, conn, m34); err == nil {
		t.Fatal("migration 34 should fail: durable_jobs_new already exists")
	}

	var recordedCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 34`).Scan(&recordedCount); err != nil {
		t.Fatalf("count schema_migrations for version 34: %v", err)
	}
	if recordedCount != 0 {
		t.Fatal("migration 34 recorded as applied despite failing, want no row")
	}

	var fk int
	if err := store.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatalf("query foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Fatalf("foreign_keys pragma = %d after failed migration 34, want restored to 1", fk)
	}

	// The original table's own OLD, narrower CHECK is still in force —
	// ARTIFACT_SWEEP must still be rejected.
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO durable_jobs(id, project_id, kind, aggregate_type, aggregate_id, payload_json, state, available_at, max_claims, idempotency_key, version, created_at, updated_at, job_class)
VALUES ('job-sweep-precheck', NULL, 'ARTIFACT_SWEEP', 'X', 'y', '{}', 'AVAILABLE', '2026-09-11T00:00:00Z', 3, 'idem-precheck', 1, '2026-09-11T00:00:00Z', '2026-09-11T00:00:00Z', 'CONTROL');`,
	); err == nil {
		t.Fatal("ARTIFACT_SWEEP+CONTROL should still be rejected by the OLD CHECK after a failed migration 34")
	}
}
