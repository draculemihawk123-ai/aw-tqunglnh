package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

// TestMigration0007_AppliesCleanlyAndAddsNullableColumns proves migration
// 7 (WorkItem's own contract columns, V3-03) applies cleanly on top of a
// freshly-migrated 1..6 database, that the pre-existing fixture-style
// INSERT (explicit column list, no contract fields — exactly what
// internal/adapters/sqlite/fixtures.go and crashworker_fixtures.go still
// do) keeps working unmodified, and that every new column defaults to
// NULL rather than rejecting the insert.
func TestMigration0007_AppliesCleanlyAndAddsNullableColumns(t *testing.T) {
	ctx := context.Background()
	store := openWithoutMigrating(t, filepath.Join(t.TempDir(), "agentkit-migration-0007-clean.db"))
	defer store.Close()

	migrateUpTo(t, store, 6)
	seven := migrationByVersion(t, 7)
	if _, err := store.db.ExecContext(ctx, seven.SQL); err != nil {
		t.Fatalf("apply migration 7 on a freshly-migrated database: %v", err)
	}

	if _, err := store.db.ExecContext(ctx,
		`INSERT INTO projects(id, name, status, version, created_at, updated_at) VALUES('p1','proj','ACTIVE',1,'x','x')`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := store.db.ExecContext(ctx,
		`INSERT INTO task_families(id, project_id, root_work_item_id, scope_version, status, version, created_at, updated_at)
		 VALUES('fam1','p1','wi1',1,'ACTIVE',1,'x','x')`); err != nil {
		t.Fatalf("insert task family: %v", err)
	}

	// The exact pre-V3-03 fixture shape (fixtures.go/crashworker_fixtures.go):
	// an explicit column list that never mentions any of the new contract
	// columns. This must keep working unmodified after migration 7.
	if _, err := store.db.ExecContext(ctx,
		`INSERT INTO work_items(id, project_id, kind, parent_id, family_id, title, status, version, created_at, updated_at)
		 VALUES('wi1', 'p1', 'ROOT', NULL, 'fam1', 'Legacy fixture work item', 'ACTIVE', 1, 'x', 'x')`); err != nil {
		t.Fatalf("insert work item using the pre-V3-03 column list: %v", err)
	}

	var schemaVersion, behavior, acceptanceJSON, verificationJSON, risk, exclusionsJSON, workflowVersionID any
	if err := store.db.QueryRowContext(ctx,
		`SELECT schema_version, behavior, acceptance_json, verification_json, risk, exclusions_json, workflow_version_id
		 FROM work_items WHERE id = 'wi1'`,
	).Scan(&schemaVersion, &behavior, &acceptanceJSON, &verificationJSON, &risk, &exclusionsJSON, &workflowVersionID); err != nil {
		t.Fatalf("read new contract columns: %v", err)
	}
	for name, got := range map[string]any{
		"schema_version": schemaVersion, "behavior": behavior, "acceptance_json": acceptanceJSON,
		"verification_json": verificationJSON, "risk": risk, "exclusions_json": exclusionsJSON,
		"workflow_version_id": workflowVersionID,
	} {
		if got != nil {
			t.Fatalf("%s = %v, want NULL by default on a row inserted through the legacy column list", name, got)
		}
	}
}

// TestMigrate_FullHistory_AppliesToVersion7 mirrors
// TestMigrate_FullHistory_AppliesToVersion6's own style: proves
// Store.Migrate's public entry point carries a fresh database all the way
// to migration 7 and records it.
func TestMigrate_FullHistory_AppliesToVersion7(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "agentkit-migration-full-history-7.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version = 7`).Scan(&count); err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if count != 1 {
		t.Fatalf("schema_migrations rows for version 7 = %d, want 1", count)
	}

	var columnCount int
	if err := store.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('work_items') WHERE name IN
		 ('schema_version','behavior','acceptance_json','verification_json','risk','exclusions_json','workflow_version_id')`,
	).Scan(&columnCount); err != nil {
		t.Fatalf("check new columns exist: %v", err)
	}
	if columnCount != 7 {
		t.Fatalf("work_items new contract column count = %d, want 7", columnCount)
	}
}
