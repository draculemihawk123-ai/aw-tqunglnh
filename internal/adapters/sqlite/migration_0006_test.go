package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// migrateUpTo applies bootstrapSchema plus every embedded migration with
// Version <= upToVersion, in order, on store's own connection — a manual
// stand-in for Store.Migrate that stops short of the full history, so a
// test can inspect (or seed fixture data into) an intermediate schema
// version. It intentionally does not use RunSerializedWrite/a single
// transaction the way Store.Migrate itself does: each statement commits
// immediately, so a test can freely interleave fixture inserts between
// migrateUpTo and a later, separately-applied migration.
func migrateUpTo(t *testing.T, store *Store, upToVersion int) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.db.ExecContext(ctx, bootstrapSchema); err != nil {
		t.Fatalf("bootstrap schema: %v", err)
	}
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	for _, m := range migrations {
		if m.Version > upToVersion {
			continue
		}
		if _, err := store.db.ExecContext(ctx, m.SQL); err != nil {
			t.Fatalf("apply migration %d (%s): %v", m.Version, m.Name, err)
		}
		if _, err := store.db.ExecContext(ctx,
			`INSERT INTO schema_migrations(version, checksum, applied_at) VALUES(?, ?, ?)`,
			m.Version, m.Checksum, "2026-01-01T00:00:00Z"); err != nil {
			t.Fatalf("record migration %d: %v", m.Version, err)
		}
	}
}

// migrationByVersion returns the one embedded migration matching version,
// failing the test if none matches.
func migrationByVersion(t *testing.T, version int) migration {
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
	t.Fatalf("no embedded migration with version %d", version)
	return migration{}
}

// TestMigration0006_AppliesCleanlyOnAFreshlyMigratedDatabase proves the
// realistic case this migration actually ever runs against: a database
// migrated 1..5 with empty repositories/family_repository_scopes/
// repository_workspaces tables (exactly what sqlite.Open produces for
// every test fixture and every real cmd/agentkit invocation today, since
// nothing before this task ever writes a repositories row and nothing
// before V3-04/V3-06 ever writes to the other two — see migration 6's own
// comment for the full argument). Applying it standalone must succeed,
// leave the widened CHECK constraint in place, and add the new nullable
// column.
func TestMigration0006_AppliesCleanlyOnAFreshlyMigratedDatabase(t *testing.T) {
	ctx := context.Background()
	store := openWithoutMigrating(t, filepath.Join(t.TempDir(), "agentkit-migration-0006-clean.db"))
	defer store.Close()

	migrateUpTo(t, store, 5)
	six := migrationByVersion(t, 6)
	if _, err := store.db.ExecContext(ctx, six.SQL); err != nil {
		t.Fatalf("apply migration 6 on a freshly-migrated (empty) database: %v", err)
	}

	if _, err := store.db.ExecContext(ctx,
		`INSERT INTO projects(id, name, status, version, created_at, updated_at) VALUES('p1','proj','ACTIVE',1,'x','x')`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	for _, status := range []string{"REGISTERING", "PROBING", "ACTIVE", "BLOCKED", "DISABLED"} {
		id := "repo-" + status
		if _, err := store.db.ExecContext(ctx, `
INSERT INTO repositories(id, project_id, name, local_path, default_ref, status, version, created_at, updated_at)
VALUES(?, 'p1', ?, '/tmp/x', 'main', ?, 1, 'x', 'x')`,
			id, "svc-"+status, status,
		); err != nil {
			t.Fatalf("insert repository with widened status %s: %v", status, err)
		}
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO repositories(id, project_id, name, local_path, default_ref, status, version, created_at, updated_at)
VALUES('repo-bad', 'p1', 'svc-bad', '/tmp/x', 'main', 'NOT_A_REAL_STATUS', 1, 'x', 'x')`); err == nil {
		t.Fatal("insert with an invalid status should still fail the CHECK constraint")
	}

	var lastProbeErrorCode any
	if err := store.db.QueryRowContext(ctx,
		`SELECT last_probe_error_code FROM repositories WHERE id = 'repo-REGISTERING'`,
	).Scan(&lastProbeErrorCode); err != nil {
		t.Fatalf("read last_probe_error_code: %v", err)
	}
	if lastProbeErrorCode != nil {
		t.Fatalf("last_probe_error_code = %v, want NULL by default", lastProbeErrorCode)
	}
}

// TestMigration0006_PopulatedCrossReference_FailsLoudNotSilentCorruption
// documents and regression-tests the exact empirical finding migration
// 6's own comment explains: if repositories ever DID already have a row
// referenced by a real family_repository_scopes row before this migration
// ran (a scenario this task's own research found impossible in this
// codebase's actual lifecycle — see the migration's comment for why),
// DROP TABLE repositories fails outright with a genuine
// "FOREIGN KEY constraint failed" under PRAGMA foreign_keys=ON, rather
// than silently succeeding and leaving the child row's reference
// dangling. Store.Migrate wraps every pending migration in one
// transaction (TestMigrate_FailureRollsBackEverything already proves that
// whole-attempt rollback), so this failure mode is fail-safe: the entire
// migration attempt rolls back atomically, never a half-migrated or
// silently-corrupted database.
func TestMigration0006_PopulatedCrossReference_FailsLoudNotSilentCorruption(t *testing.T) {
	ctx := context.Background()
	store := openWithoutMigrating(t, filepath.Join(t.TempDir(), "agentkit-migration-0006-populated.db"))
	defer store.Close()

	migrateUpTo(t, store, 5)
	if _, err := store.db.ExecContext(ctx,
		`INSERT INTO projects(id, name, status, version, created_at, updated_at) VALUES('p1','proj','ACTIVE',1,'x','x')`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := store.db.ExecContext(ctx,
		`INSERT INTO task_families(id, project_id, root_work_item_id, scope_version, status, version, created_at, updated_at)
		 VALUES('fam1','p1','wi1',1,'ACTIVE',1,'x','x')`); err != nil {
		t.Fatalf("insert task family: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO repositories(id, project_id, name, local_path, default_ref, status, version, created_at, updated_at)
VALUES('repo1','p1','svc','/tmp/svc','main','ACTIVE',1,'x','x')`); err != nil {
		t.Fatalf("insert repository: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO family_repository_scopes(family_id, project_id, repository_id, scope_version, access, paths_json, reason, created_at)
VALUES('fam1','p1','repo1',1,'READ','[]','test scope','x')`); err != nil {
		t.Fatalf("insert family_repository_scopes referencing the repository: %v", err)
	}

	six := migrationByVersion(t, 6)
	_, err := store.db.ExecContext(ctx, six.SQL)
	if err == nil {
		t.Fatal("migration 6 succeeded against a populated cross-referenced repositories table — want a loud FOREIGN KEY failure, not silent success")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "foreign key") {
		t.Fatalf("err = %v, want it to mention a foreign key constraint failure", err)
	}

	// The failure must not have partially applied: DROP TABLE runs before
	// CREATE TABLE in the migration source, so a failed DROP must leave
	// the original (pre-migration-6) repositories row completely intact.
	var name, status string
	if err := store.db.QueryRowContext(ctx, `SELECT name, status FROM repositories WHERE id = 'repo1'`).Scan(&name, &status); err != nil {
		t.Fatalf("original repository row should survive a failed migration 6: %v", err)
	}
	if name != "svc" || status != "ACTIVE" {
		t.Fatalf("repository row after failed migration = (name=%q status=%q), want unchanged (name=svc status=ACTIVE)", name, status)
	}
}

// TestMigrate_FullHistory_AppliesToVersion6 proves Store.Migrate's own
// public entry point (not the manual migrateUpTo test helper above)
// carries a fresh database all the way to migration 6, and that the
// migration is correctly recorded — the same style
// TestMigrate_EmptyDatabase already uses for migration 1.
func TestMigrate_FullHistory_AppliesToVersion6(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "agentkit-migration-full-history.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 6`).Scan(&count); err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if count != 1 {
		t.Fatalf("schema_migrations rows for version 6 = %d, want 1", count)
	}

	var tableCount int
	if err := store.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name IN ('components','component_pack_assignments')`,
	).Scan(&tableCount); err != nil {
		t.Fatalf("check new tables exist: %v", err)
	}
	if tableCount != 2 {
		t.Fatalf("components/component_pack_assignments table count = %d, want 2", tableCount)
	}
}
