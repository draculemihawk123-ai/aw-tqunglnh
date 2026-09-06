package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

// applyMigrationsThrough applies every embedded migration with
// Version <= maxVersion, in order, directly against conn — test-only
// surgical control over exactly how far Migrate's own real machinery
// progresses, so a test can seed real rows against an intermediate schema
// before letting a specific later migration run.
func applyMigrationsThrough(t *testing.T, ctx context.Context, store *Store, maxVersion int) *Store {
	t.Helper()
	if _, err := store.db.ExecContext(ctx, bootstrapSchema); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer conn.Close()
	for _, m := range migrations {
		if m.Version > maxVersion {
			continue
		}
		if err := applyOneMigration(ctx, conn, m); err != nil {
			t.Fatalf("apply migration %d (%s): %v", m.Version, m.Name, err)
		}
	}
	return store
}

func findMigration(t *testing.T, version int) migration {
	t.Helper()
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	for _, m := range migrations {
		if m.Version == version {
			return m
		}
	}
	t.Fatalf("no migration with version %d found", version)
	return migration{}
}

// seedThreeReferencingRows inserts one real row into EACH of the three
// tables that hold a live foreign key into durable_jobs(id)
// (write_leases.holder_job_id, repository_probe_attempts.job_id,
// readiness_baseline_attempts.job_id) — the exact real-data shape migration
// 25's own rebuild must survive without SQLite's FK enforcement ever
// rejecting the DROP TABLE step (see migrations.go's own
// migrationsRequiringForeignKeysOff doc comment for why this table
// specifically needed that treatment). Returns the number of durable_jobs
// rows this seeded (including a plain RUN_WORK one with no referencing
// child, so row-count preservation covers an unreferenced row too), for the
// caller's own post-rebuild assertions — the probe/readiness/lease rows
// themselves are looked up by their own fixed IDs afterward instead.
func seedThreeReferencingRows(t *testing.T, ctx context.Context, store *Store, projectID, familyID, workItemID, repositoryID, repositoryWorkspaceID, workspaceSetID string) (durableJobRows int) {
	t.Helper()
	if err := SeedFixtureOwners(ctx, store, projectID, familyID, workItemID); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	if err := SeedFixtureRepositoryWorkspace(ctx, store, projectID, familyID, workspaceSetID, repositoryID, repositoryWorkspaceID); err != nil {
		t.Fatalf("SeedFixtureRepositoryWorkspace: %v", err)
	}
	// write_leases.holder_job_id, via the real EnqueueJob/ClaimJob/
	// AcquireWriteLeases production path.
	if err := SeedFixtureWriteLease(ctx, store, projectID, familyID, workItemID, repositoryID, repositoryWorkspaceID, 1); err != nil {
		t.Fatalf("SeedFixtureWriteLease: %v", err)
	}

	now := "2026-09-06T00:00:00Z"
	probeJobID := "job-probe-1"
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO durable_jobs(id, project_id, kind, aggregate_type, aggregate_id, payload_json, state, available_at, max_claims, idempotency_key, version, created_at, updated_at, job_class)
VALUES (?, ?, 'REPOSITORY_PROBE', 'Repository', ?, '{}', 'SUCCEEDED', ?, 3, 'idem-probe-1', 1, ?, ?, 'RUN_WORK');`,
		probeJobID, projectID, repositoryID, now, now, now,
	); err != nil {
		t.Fatalf("seed probe durable_jobs row: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO repository_probe_attempts(id, project_id, repository_id, job_id, state, result, base_commit, dirty, created_at)
VALUES ('probe-attempt-1', ?, ?, ?, 'SUCCEEDED', 'ACTIVE', 'cafebabecafebabecafebabecafebabecafebabe', 0, ?);`,
		projectID, repositoryID, probeJobID, now,
	); err != nil {
		t.Fatalf("seed repository_probe_attempts row: %v", err)
	}

	readinessJobID := "job-readiness-1"
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO durable_jobs(id, project_id, kind, aggregate_type, aggregate_id, payload_json, state, available_at, max_claims, idempotency_key, version, created_at, updated_at, job_class)
VALUES (?, ?, 'BASELINE_EVIDENCE', 'RepositoryWorkspace', ?, '{}', 'SUCCEEDED', ?, 3, 'idem-readiness-1', 1, ?, ?, 'RUN_WORK');`,
		readinessJobID, projectID, repositoryWorkspaceID, now, now, now,
	); err != nil {
		t.Fatalf("seed readiness durable_jobs row: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO readiness_baseline_attempts(id, project_id, repository_workspace_id, repository_id, job_id, stage, outcome, duration_ms, stdout_excerpt, stderr_excerpt, created_at)
VALUES ('readiness-attempt-1', ?, ?, ?, ?, 'SETUP', 'GREEN', 100, '', '', ?);`,
		projectID, repositoryWorkspaceID, repositoryID, readinessJobID, now,
	); err != nil {
		t.Fatalf("seed readiness_baseline_attempts row: %v", err)
	}

	// A plain, unreferenced RUN_WORK durable_jobs row too, so row-count
	// preservation below is not only ever proven for referenced rows.
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO durable_jobs(id, project_id, kind, aggregate_type, aggregate_id, payload_json, state, available_at, max_claims, idempotency_key, version, created_at, updated_at, job_class)
VALUES ('job-plain-1', ?, 'ADVANCE_RUN', 'WorkflowRun', 'run-x', '{}', 'AVAILABLE', ?, 3, 'idem-plain-1', 1, ?, ?, 'RUN_WORK');`,
		projectID, now, now, now,
	); err != nil {
		t.Fatalf("seed plain durable_jobs row: %v", err)
	}

	return 4 // write-lease job + probe job + readiness job + plain job
}

// TestMigration25_UpgradeWithRealData_PreservesRowsAndWidensCheck is the
// real, non-synthetic proof V4-13 requires (confirmed with the user before
// writing this file): apply every migration through 24 for real, seed real
// rows in durable_jobs AND all three tables that hold a live foreign key
// into it, THEN apply migration 25 and prove every row survived byte-for-
// byte, PRAGMA foreign_key_check reports zero violations, and the widened
// CHECK constraint now accepts RECOVERY_REAPER+CONTROL while still
// rejecting both wrong-direction pairings.
func TestMigration25_UpgradeWithRealData_PreservesRowsAndWidensCheck(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "agentkit-migration25-upgrade.db")
	store := openWithoutMigrating(t, databasePath)
	defer store.Close()
	applyMigrationsThrough(t, ctx, store, 24)

	const projectID, familyID, workItemID = "p1", "f1", "wi1"
	const repositoryID, repositoryWorkspaceID, workspaceSetID = "repo1", "rw1", "ws1"
	wantDurableJobsRows := seedThreeReferencingRows(t, ctx, store, projectID, familyID, workItemID, repositoryID, repositoryWorkspaceID, workspaceSetID)

	var beforeCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM durable_jobs`).Scan(&beforeCount); err != nil {
		t.Fatalf("count durable_jobs before: %v", err)
	}
	if beforeCount != wantDurableJobsRows {
		t.Fatalf("seeded durable_jobs row count = %d, want %d", beforeCount, wantDurableJobsRows)
	}

	m25 := findMigration(t, 25)
	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer conn.Close()
	if err := applyOneMigration(ctx, conn, m25); err != nil {
		t.Fatalf("apply migration 25: %v", err)
	}

	var fk int
	if err := store.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatalf("query foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Fatalf("foreign_keys pragma = %d after migration 25, want 1 (re-enabled)", fk)
	}

	var afterCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM durable_jobs`).Scan(&afterCount); err != nil {
		t.Fatalf("count durable_jobs after: %v", err)
	}
	if afterCount != wantDurableJobsRows {
		t.Fatalf("durable_jobs row count after migration 25 = %d, want unchanged %d", afterCount, wantDurableJobsRows)
	}

	// Every old job's own ProjectID survives the rebuild unchanged (V4-13,
	// confirmed with the user: "giữ nguyên ProjectID của mọi job cũ").
	var plainJobProjectID string
	if err := store.db.QueryRowContext(ctx, `SELECT project_id FROM durable_jobs WHERE id = 'job-plain-1'`).Scan(&plainJobProjectID); err != nil {
		t.Fatalf("read job-plain-1 project_id after rebuild: %v", err)
	}
	if plainJobProjectID != projectID {
		t.Fatalf("job-plain-1 project_id after rebuild = %q, want unchanged %q", plainJobProjectID, projectID)
	}

	// Every referencing child row must still resolve its own parent.
	var probeState string
	if err := store.db.QueryRowContext(ctx,
		`SELECT j.state FROM durable_jobs j JOIN repository_probe_attempts p ON p.job_id = j.id WHERE p.id = 'probe-attempt-1'`,
	).Scan(&probeState); err != nil {
		t.Fatalf("resolve repository_probe_attempts -> durable_jobs after rebuild: %v", err)
	}
	if probeState != "SUCCEEDED" {
		t.Fatalf("probe job state after rebuild = %s, want unchanged SUCCEEDED", probeState)
	}
	var readinessState string
	if err := store.db.QueryRowContext(ctx,
		`SELECT j.state FROM durable_jobs j JOIN readiness_baseline_attempts r ON r.job_id = j.id WHERE r.id = 'readiness-attempt-1'`,
	).Scan(&readinessState); err != nil {
		t.Fatalf("resolve readiness_baseline_attempts -> durable_jobs after rebuild: %v", err)
	}
	if readinessState != "SUCCEEDED" {
		t.Fatalf("readiness job state after rebuild = %s, want unchanged SUCCEEDED", readinessState)
	}
	var leaseHolder string
	if err := store.db.QueryRowContext(ctx,
		`SELECT j.kind FROM durable_jobs j JOIN write_leases w ON w.holder_job_id = j.id WHERE j.id = w.holder_job_id LIMIT 1`,
	).Scan(&leaseHolder); err != nil {
		t.Fatalf("resolve write_leases -> durable_jobs after rebuild: %v", err)
	}
	if leaseHolder != "EXECUTE_NODE" {
		t.Fatalf("write-lease holder job kind after rebuild = %s, want unchanged EXECUTE_NODE", leaseHolder)
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
		t.Fatalf("foreign_key_check violations after migration 25 = %d, want 0", violations)
	}

	// The widened CHECK now accepts RECOVERY_REAPER as CONTROL, but ONLY
	// with project_id/run_id/cancel_epoch all NULL (V4-13, confirmed with
	// the user: a nullable project_id with a strict per-kind invariant, not
	// a blanket "CONTROL jobs may omit ProjectID" — see migration 25's own
	// updated doc comment).
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO durable_jobs(id, project_id, kind, aggregate_type, aggregate_id, payload_json, state, available_at, max_claims, idempotency_key, version, created_at, updated_at, job_class)
VALUES ('job-reaper-1', NULL, 'RECOVERY_REAPER', 'RecoveryReaper', 'singleton', '{}', 'AVAILABLE', ?, 3, 'idem-reaper-1', 1, ?, ?, 'CONTROL');`,
		"2026-09-06T00:00:00Z", "2026-09-06T00:00:00Z", "2026-09-06T00:00:00Z",
	); err != nil {
		t.Fatalf("insert RECOVERY_REAPER+CONTROL+NULL project/run/cancel_epoch should be accepted by the widened CHECK: %v", err)
	}
	// ...but rejects a RECOVERY_REAPER row that carries a real ProjectID —
	// that job kind is installation-global, never scoped to one Project.
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO durable_jobs(id, project_id, kind, aggregate_type, aggregate_id, payload_json, state, available_at, max_claims, idempotency_key, version, created_at, updated_at, job_class)
VALUES ('job-reaper-with-project', ?, 'RECOVERY_REAPER', 'RecoveryReaper', 'singleton', '{}', 'AVAILABLE', ?, 3, 'idem-reaper-with-project', 1, ?, ?, 'CONTROL');`,
		projectID, "2026-09-06T00:00:00Z", "2026-09-06T00:00:00Z", "2026-09-06T00:00:00Z",
	); err == nil {
		t.Fatal("insert RECOVERY_REAPER+CONTROL with a non-NULL project_id should be rejected by the CHECK constraint, want error")
	}
	// ...and rejects a RECOVERY_REAPER row that carries a RunID — also
	// installation-global, never scoped to one Run.
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO durable_jobs(id, project_id, kind, aggregate_type, aggregate_id, payload_json, state, available_at, max_claims, idempotency_key, version, created_at, updated_at, job_class, run_id)
VALUES ('job-reaper-with-run', NULL, 'RECOVERY_REAPER', 'RecoveryReaper', 'singleton', '{}', 'AVAILABLE', ?, 3, 'idem-reaper-with-run', 1, ?, ?, 'CONTROL', 'run-x');`,
		"2026-09-06T00:00:00Z", "2026-09-06T00:00:00Z", "2026-09-06T00:00:00Z",
	); err == nil {
		t.Fatal("insert RECOVERY_REAPER+CONTROL with a non-NULL run_id should be rejected by the CHECK constraint, want error")
	}
	// ...and rejects a RECOVERY_REAPER row with a cancel_epoch — CONTROL is
	// never fenced, and RECOVERY_REAPER doubly so (installation-global).
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO durable_jobs(id, project_id, kind, aggregate_type, aggregate_id, payload_json, state, available_at, max_claims, idempotency_key, version, created_at, updated_at, job_class, cancel_epoch)
VALUES ('job-reaper-with-epoch', NULL, 'RECOVERY_REAPER', 'RecoveryReaper', 'singleton', '{}', 'AVAILABLE', ?, 3, 'idem-reaper-with-epoch', 1, ?, ?, 'CONTROL', 1);`,
		"2026-09-06T00:00:00Z", "2026-09-06T00:00:00Z", "2026-09-06T00:00:00Z",
	); err == nil {
		t.Fatal("insert RECOVERY_REAPER+CONTROL with a non-NULL cancel_epoch should be rejected by the CHECK constraint, want error")
	}
	// ...and still rejects both wrong-direction pairings.
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO durable_jobs(id, project_id, kind, aggregate_type, aggregate_id, payload_json, state, available_at, max_claims, idempotency_key, version, created_at, updated_at, job_class)
VALUES ('job-reaper-2', ?, 'RECOVERY_REAPER', 'RecoveryReaper', 'singleton', '{}', 'AVAILABLE', ?, 3, 'idem-reaper-2', 1, ?, ?, 'RUN_WORK');`,
		projectID, "2026-09-06T00:00:00Z", "2026-09-06T00:00:00Z", "2026-09-06T00:00:00Z",
	); err == nil {
		t.Fatal("insert RECOVERY_REAPER+RUN_WORK should be rejected by the CHECK constraint, want error")
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO durable_jobs(id, project_id, kind, aggregate_type, aggregate_id, payload_json, state, available_at, max_claims, idempotency_key, version, created_at, updated_at, job_class)
VALUES ('job-unknown-1', ?, 'SOME_UNKNOWN_KIND', 'X', 'y', '{}', 'AVAILABLE', ?, 3, 'idem-unknown-1', 1, ?, ?, 'CONTROL');`,
		projectID, "2026-09-06T00:00:00Z", "2026-09-06T00:00:00Z", "2026-09-06T00:00:00Z",
	); err == nil {
		t.Fatal("insert unknown-kind+CONTROL should be rejected by the CHECK constraint, want error")
	}
	// ...and rejects a non-RECOVERY_REAPER kind with a NULL project_id —
	// the exemption is narrow, never a general "CONTROL may omit Project".
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO durable_jobs(id, project_id, kind, aggregate_type, aggregate_id, payload_json, state, available_at, max_claims, idempotency_key, version, created_at, updated_at, job_class)
VALUES ('job-cancel-coordinator-no-project', NULL, 'CANCEL_RUN_COORDINATOR', 'WorkflowRun', 'run-x', '{}', 'AVAILABLE', ?, 3, 'idem-cancel-coordinator-no-project', 1, ?, ?, 'CONTROL');`,
		"2026-09-06T00:00:00Z", "2026-09-06T00:00:00Z", "2026-09-06T00:00:00Z",
	); err == nil {
		t.Fatal("insert CANCEL_RUN_COORDINATOR+CONTROL with a NULL project_id should be rejected by the CHECK constraint, want error")
	}
}

// TestMigration25_FailureMidRebuild_OldTableIntactNoRecord sabotages
// migration 25's own real SQL (pre-creating durable_jobs_new so its own
// CREATE TABLE fails partway through) and proves: no schema_migrations row
// for version 25, foreign_keys is restored to ON regardless, and the
// ORIGINAL durable_jobs table (with its OLD, narrower CHECK — RECOVERY_REAPER
// still rejected as CONTROL) is completely untouched.
func TestMigration25_FailureMidRebuild_OldTableIntactNoRecord(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "agentkit-migration25-fail.db")
	store := openWithoutMigrating(t, databasePath)
	defer store.Close()
	applyMigrationsThrough(t, ctx, store, 24)

	if _, err := store.db.ExecContext(ctx, `CREATE TABLE durable_jobs_new (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("sabotage durable_jobs_new: %v", err)
	}

	m25 := findMigration(t, 25)
	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer conn.Close()
	if err := applyOneMigration(ctx, conn, m25); err == nil {
		t.Fatal("migration 25 should fail: durable_jobs_new already exists")
	}

	var recordedCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 25`).Scan(&recordedCount); err != nil {
		t.Fatalf("count schema_migrations for version 25: %v", err)
	}
	if recordedCount != 0 {
		t.Fatal("migration 25 recorded as applied despite failing, want no row")
	}

	var fk int
	if err := store.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatalf("query foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Fatalf("foreign_keys pragma = %d after failed migration 25, want restored to 1", fk)
	}

	// The original table's own OLD, narrower CHECK is still in force —
	// RECOVERY_REAPER+CONTROL must still be rejected.
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO durable_jobs(id, project_id, kind, aggregate_type, aggregate_id, payload_json, state, available_at, max_claims, idempotency_key, version, created_at, updated_at, job_class)
VALUES ('job-reaper-precheck', 'does-not-exist', 'RECOVERY_REAPER', 'X', 'y', '{}', 'AVAILABLE', '2026-09-06T00:00:00Z', 3, 'idem-precheck', 1, '2026-09-06T00:00:00Z', '2026-09-06T00:00:00Z', 'CONTROL');`,
	); err == nil {
		t.Fatal("RECOVERY_REAPER+CONTROL should still be rejected by the OLD CHECK after a failed migration 25")
	}
}
