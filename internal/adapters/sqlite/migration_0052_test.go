package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

// TestMigration52_AddsMessageActivityColumns proves migration 0052 (V11-11, ADR-042) on a database that already holds a message: it
// survives with display_only 0 and no node run, and the new columns accept an activity message and refuse a bad flag.
func TestMigration52_AddsMessageActivityColumns(t *testing.T) {
	ctx := context.Background()
	store := openWithoutMigrating(t, filepath.Join(t.TempDir(), "agentkit-migration52.db"))
	defer store.Close()
	applyMigrationsThrough(t, ctx, store, 51)
	if err := SeedFixtureOwners(ctx, store, "p1", "f1", "wi-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO artifacts (id, project_id, locator, content_hash, size, media_type, sensitivity, redacted, retention_class, attach_state, hold, created_at, version)
VALUES ('art-1', 'p1', 'loc', 'sha256:x', 1, 'text/plain', 'PUBLIC', 1, 'CANONICAL_CONTEXT', 'ATTACHED', 0, '2026-10-11T00:00:00Z', 1)`); err != nil {
		t.Fatalf("seed an artifact: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO messages (id, project_id, work_item_id, sequence, actor, role, content_artifact_id, correlation_id, created_at, pinned)
VALUES ('m-old', 'p1', 'wi-1', 1, 'op', 'USER', 'art-1', 'c', '2026-10-11T00:00:00Z', 0)`); err != nil {
		t.Fatalf("seed a message before the migration: %v", err)
	}
	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	if err := applyOneMigration(ctx, conn, findMigration(t, 52)); err != nil {
		conn.Close()
		t.Fatalf("apply migration 52: %v", err)
	}
	conn.Close()

	var displayOnly int
	var nodeRun *string
	if err := store.db.QueryRowContext(ctx, `SELECT display_only, node_run_id FROM messages WHERE id = 'm-old'`).Scan(&displayOnly, &nodeRun); err != nil || displayOnly != 0 || nodeRun != nil {
		t.Fatalf("old message = display_only %d node_run_id %v, %v; want 0 and NULL", displayOnly, nodeRun, err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO messages (id, project_id, work_item_id, sequence, actor, role, content_artifact_id, correlation_id, created_at, display_only)
VALUES ('m-act', 'p1', 'wi-1', 2, 'aw', 'SYSTEM', 'art-1', 'c', '2026-10-11T00:00:01Z', 1)`); err != nil {
		t.Fatalf("insert an activity message: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO messages (id, project_id, work_item_id, sequence, actor, role, content_artifact_id, correlation_id, created_at, display_only)
VALUES ('m-bad', 'p1', 'wi-1', 3, 'aw', 'SYSTEM', 'art-1', 'c', '2026-10-11T00:00:02Z', 2)`); err == nil {
		t.Fatal("a display_only value other than 0 or 1 was accepted")
	}
}
