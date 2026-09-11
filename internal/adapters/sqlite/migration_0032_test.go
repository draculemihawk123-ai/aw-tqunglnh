package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

// TestMigration32_UpgradeWithRealData_PreservesRowsAndWidensCheck mirrors
// migration_0025_test.go's own identical real-data rebuild proof: apply
// every migration through 31, seed a real artifacts row AND a real
// messages row holding a live foreign key into it
// (messages.content_artifact_id REFERENCES artifacts(id), 0028_messages.sql
// — the reason this migration needs PRAGMA foreign_keys=OFF, exactly like
// migration 25 needed it for durable_jobs), then apply migration 32 and
// prove every row survived byte-for-byte, PRAGMA foreign_key_check reports
// zero violations, and the widened CHECK now accepts PURGED while still
// rejecting an unknown value.
func TestMigration32_UpgradeWithRealData_PreservesRowsAndWidensCheck(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "agentkit-migration32-upgrade.db")
	store := openWithoutMigrating(t, databasePath)
	defer store.Close()
	applyMigrationsThrough(t, ctx, store, 31)

	const projectID, familyID, workItemID = "p1", "f1", "wi1"
	if err := SeedFixtureOwners(ctx, store, projectID, familyID, workItemID); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}

	const now = "2026-09-11T00:00:00Z"
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO artifacts (id, project_id, locator, content_hash, size, media_type, sensitivity, redacted,
                        retention_class, attach_state, hold, expires_at, created_at, version)
VALUES ('artifact-referenced', ?, 'sha256:`+testMigration32Digest+`', 'sha256:`+testMigration32Digest+`', 5, 'text/plain', 'PUBLIC', 0,
        'CANONICAL_CONTEXT', 'ATTACHED', 0, NULL, ?, 1);`,
		projectID, now,
	); err != nil {
		t.Fatalf("seed referenced artifacts row: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO artifacts (id, project_id, locator, content_hash, size, media_type, sensitivity, redacted,
                        retention_class, attach_state, hold, expires_at, created_at, version)
VALUES ('artifact-orphan', ?, 'sha256:`+testMigration32DigestTwo+`', 'sha256:`+testMigration32DigestTwo+`', 9, 'text/plain', 'PUBLIC', 0,
        'RAW_OUTPUT_TEMP', 'ORPHAN', 0, ?, ?, 1);`,
		projectID, now, now,
	); err != nil {
		t.Fatalf("seed orphan artifacts row: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO messages (id, project_id, work_item_id, attempt_id, sequence, actor, role, content_artifact_id, correlation_id, created_at)
VALUES ('message-1', ?, ?, NULL, 1, 'actor-1', 'USER', 'artifact-referenced', 'corr-1', ?);`,
		projectID, workItemID, now,
	); err != nil {
		t.Fatalf("seed referencing messages row: %v", err)
	}

	var beforeCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM artifacts`).Scan(&beforeCount); err != nil {
		t.Fatalf("count artifacts before: %v", err)
	}
	if beforeCount != 2 {
		t.Fatalf("seeded artifacts row count = %d, want 2", beforeCount)
	}

	m32 := findMigration(t, 32)
	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer conn.Close()
	if err := applyOneMigration(ctx, conn, m32); err != nil {
		t.Fatalf("apply migration 32: %v", err)
	}

	var fk int
	if err := store.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatalf("query foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Fatalf("foreign_keys pragma = %d after migration 32, want 1 (re-enabled)", fk)
	}

	var afterCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM artifacts`).Scan(&afterCount); err != nil {
		t.Fatalf("count artifacts after: %v", err)
	}
	if afterCount != 2 {
		t.Fatalf("artifacts row count after migration 32 = %d, want unchanged 2", afterCount)
	}

	var referencedState string
	if err := store.db.QueryRowContext(ctx,
		`SELECT a.attach_state FROM artifacts a JOIN messages m ON m.content_artifact_id = a.id WHERE m.id = 'message-1'`,
	).Scan(&referencedState); err != nil {
		t.Fatalf("resolve messages -> artifacts after rebuild: %v", err)
	}
	if referencedState != "ATTACHED" {
		t.Fatalf("referenced artifact state after rebuild = %s, want unchanged ATTACHED", referencedState)
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
		t.Fatalf("foreign_key_check violations after migration 32 = %d, want 0", violations)
	}

	// The widened CHECK now accepts PURGED...
	if _, err := store.db.ExecContext(ctx, `UPDATE artifacts SET attach_state = 'PURGED', version = version + 1 WHERE id = 'artifact-orphan'`); err != nil {
		t.Fatalf("transition orphan artifact to PURGED should be accepted by the widened CHECK: %v", err)
	}
	// ...but still rejects an unknown value.
	if _, err := store.db.ExecContext(ctx, `UPDATE artifacts SET attach_state = 'BOGUS', version = version + 1 WHERE id = 'artifact-referenced'`); err == nil {
		t.Fatal("transition to an unknown attach_state should be rejected by the CHECK constraint, want error")
	}
}

// TestMigration32_FailureMidRebuild_OldTableIntactNoRecord mirrors
// migration_0025_test.go's own identical sabotage proof.
func TestMigration32_FailureMidRebuild_OldTableIntactNoRecord(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "agentkit-migration32-fail.db")
	store := openWithoutMigrating(t, databasePath)
	defer store.Close()
	applyMigrationsThrough(t, ctx, store, 31)

	if _, err := store.db.ExecContext(ctx, `CREATE TABLE artifacts_new (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("sabotage artifacts_new: %v", err)
	}

	m32 := findMigration(t, 32)
	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	defer conn.Close()
	if err := applyOneMigration(ctx, conn, m32); err == nil {
		t.Fatal("migration 32 should fail: artifacts_new already exists")
	}

	var recordedCount int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 32`).Scan(&recordedCount); err != nil {
		t.Fatalf("count schema_migrations for version 32: %v", err)
	}
	if recordedCount != 0 {
		t.Fatal("migration 32 recorded as applied despite failing, want no row")
	}

	var fk int
	if err := store.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatalf("query foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Fatalf("foreign_keys pragma = %d after failed migration 32, want restored to 1", fk)
	}

	// The original table's own OLD, narrower CHECK is still in force —
	// PURGED must still be rejected.
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO artifacts (id, project_id, locator, content_hash, size, media_type, sensitivity, redacted,
                        retention_class, attach_state, hold, expires_at, created_at, version)
VALUES ('artifact-precheck', 'does-not-exist', 'sha256:`+testMigration32Digest+`', 'sha256:`+testMigration32Digest+`', 5, 'text/plain', 'PUBLIC', 0,
        'CANONICAL_CONTEXT', 'PURGED', 0, NULL, '2026-09-11T00:00:00Z', 1);`,
	); err == nil {
		t.Fatal("PURGED should still be rejected by the OLD CHECK after a failed migration 32")
	}
}

const (
	testMigration32Digest    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testMigration32DigestTwo = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)
