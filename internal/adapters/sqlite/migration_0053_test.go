package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

// TestMigration53_RebuildsRepositoriesAndKeepsEveryReference proves migration 0053 (V11-12, ADR-043) on a database that already holds a repository
// that a workspace and a probe attempt reference: the rebuild keeps the rows and the foreign keys, a DISABLED repository gives its name back, two live
// repositories still cannot share one, and the new probe-attempt columns and the tombstone table work.
func TestMigration53_RebuildsRepositoriesAndKeepsEveryReference(t *testing.T) {
	ctx := context.Background()
	store := openWithoutMigrating(t, filepath.Join(t.TempDir(), "agentkit-migration53.db"))
	defer store.Close()
	applyMigrationsThrough(t, ctx, store, 52)
	if err := SeedFixtureOwners(ctx, store, "p1", "f1", "wi-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	if err := SeedFixtureRepositoryWorkspace(ctx, store, "p1", "f1", "ws-set-1", "repo-1", "rw-1"); err != nil {
		t.Fatalf("SeedFixtureRepositoryWorkspace: %v", err)
	}
	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	if err := applyOneMigration(ctx, conn, findMigration(t, 53)); err != nil {
		conn.Close()
		t.Fatalf("apply migration 53: %v", err)
	}
	conn.Close()

	var name, status string
	if err := store.db.QueryRowContext(ctx, `SELECT name, status FROM repositories WHERE id = 'repo-1'`).Scan(&name, &status); err != nil || name != "repo-1" || status != "ACTIVE" {
		t.Fatalf("repository after the rebuild = %q %q, %v", name, status, err)
	}
	var violations int
	rows, err := store.db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	for rows.Next() {
		violations++
	}
	rows.Close()
	if violations != 0 {
		t.Fatalf("%d foreign key violation(s) after the rebuild", violations)
	}
	var workspaces int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM repository_workspaces WHERE repository_id = 'repo-1'`).Scan(&workspaces); err != nil || workspaces != 1 {
		t.Fatalf("workspace rows referencing the repository = %d, %v; want 1", workspaces, err)
	}
	// the foreign key still bites: a workspace for a repository that does not exist is refused
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO repository_workspaces(id, project_id, workspace_set_id, family_id, repository_id, generation, locator, base_revision, state, version, created_at, updated_at)
VALUES ('rw-x', 'p1', 'ws-set-1', 'f1', 'no-such-repo', 2, 'l', 'b', 'READY', 1, 't', 't')`); err == nil {
		t.Fatal("a workspace for an unknown repository was accepted: the foreign key to repositories is gone")
	}

	insert := func(id, name, status string) error {
		_, err := store.db.ExecContext(ctx, `
INSERT INTO repositories(id, project_id, name, local_path, default_ref, status, version, created_at, updated_at)
VALUES (?, 'p1', ?, 'D:/x', 'main', ?, 1, 't', 't')`, id, name, status)
		return err
	}
	if err := insert("dup-live", "repo-1", "BLOCKED"); err == nil {
		t.Fatal("two live repositories with the same name were accepted")
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE repositories SET status = 'DISABLED' WHERE id = 'repo-1'`); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if err := insert("again", "repo-1", "BLOCKED"); err != nil {
		t.Fatalf("a DISABLED repository must give its name back: %v", err)
	}
	if err := insert("repo-1", "other", "BLOCKED"); err == nil {
		t.Fatal("an id must stay unique even after the repository was disabled")
	}

	if _, err := store.db.ExecContext(ctx, `
INSERT INTO deleted_repositories(id, project_id, name, local_path, reason, actor, deleted_at) VALUES ('gone', 'p1', 'n', 'D:/x', 'typo', 'me', 't')`); err != nil {
		t.Fatalf("tombstone: %v", err)
	}
	var kind *string
	if err := store.db.QueryRowContext(ctx, `SELECT failure_kind FROM repository_probe_attempts LIMIT 1`).Scan(&kind); err != nil && err.Error() != "sql: no rows in result set" {
		t.Fatalf("failure_kind column: %v", err)
	}
}
