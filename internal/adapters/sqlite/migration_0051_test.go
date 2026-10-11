package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// TestMigration51_AddsWorkItemSupersessions proves migration 0051 (V11-10, ADR-041) on a database that already holds work items
// written before it: they survive, the new table accepts a link between two of them, and its constraints hold (a WorkItem is
// replaced at most once and replaces at most one other; neither side may be a missing WorkItem; not itself; actor and reason
// not blank).
func TestMigration51_AddsWorkItemSupersessions(t *testing.T) {
	ctx := context.Background()
	store := openWithoutMigrating(t, filepath.Join(t.TempDir(), "agentkit-migration51.db"))
	defer store.Close()
	applyMigrationsThrough(t, ctx, store, 50)

	const projectID, familyID = "p1", "f1"
	if err := SeedFixtureOwners(ctx, store, projectID, familyID, "wi-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	for _, id := range []string{"wi-2", "wi-3"} {
		if _, err := store.db.ExecContext(ctx, `
INSERT INTO work_items(id, project_id, kind, parent_id, family_id, title, status, version, created_at, updated_at)
VALUES (?, ?, 'CHILD', 'wi-1', ?, 'child', 'READY', 1, '2026-08-28T16:00:00Z', '2026-08-28T16:00:00Z');`, id, projectID, familyID); err != nil {
			t.Fatalf("seed work item %s: %v", id, err)
		}
	}

	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	if err := applyOneMigration(ctx, conn, findMigration(t, 51)); err != nil {
		conn.Close()
		t.Fatalf("apply migration 51: %v", err)
	}
	conn.Close()

	var survivors int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM work_items`).Scan(&survivors); err != nil || survivors != 3 {
		t.Fatalf("work items after migration = %d, %v; want 3", survivors, err)
	}
	insert := func(newID, oldID, actor, reason string) error {
		_, err := store.db.ExecContext(ctx, `
INSERT INTO work_item_supersessions (new_work_item_id, old_work_item_id, project_id, actor, reason, created_at)
VALUES (?, ?, ?, ?, ?, '2026-10-11T00:00:00Z')`, newID, oldID, projectID, actor, reason)
		return err
	}
	if err := insert("wi-2", "wi-1", "operator-1", "kit fixed"); err != nil {
		t.Fatalf("insert a valid supersession: %v", err)
	}
	for name, err := range map[string]error{
		"a second replacement of the same old work item": insert("wi-3", "wi-1", "operator-1", "again"),
		"the same new work item replacing two":           insert("wi-2", "wi-3", "operator-1", "again"),
		"a work item replacing itself":                   insert("wi-3", "wi-3", "operator-1", "x"),
		"a missing old work item":                        insert("wi-3", "wi-missing", "operator-1", "x"),
		"a blank actor":                                  insert("wi-3", "wi-2", "  ", "x"),
		"a blank reason":                                 insert("wi-3", "wi-2", "operator-1", " "),
	} {
		if err == nil {
			t.Errorf("%s was accepted, want it rejected", name)
		}
	}
	if err := insert("wi-3", "wi-2", "operator-1", "second redo"); err != nil {
		t.Fatalf("a chain (wi-3 replaces wi-2 which replaces wi-1) must be allowed: %v", err)
	}
}

// TestWorkItemSupersession_RepositoryRoundTripAndGuards covers the repository methods on a fully migrated store.
func TestWorkItemSupersession_RepositoryRoundTripAndGuards(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "agentkit-supersession.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	if err := SeedFixtureOwners(ctx, store, "p1", "f1", "wi-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	for _, id := range []string{"wi-2", "wi-3"} {
		if _, err := store.db.ExecContext(ctx, `
INSERT INTO work_items(id, project_id, kind, parent_id, family_id, title, status, version, created_at, updated_at)
VALUES (?, 'p1', 'CHILD', 'wi-1', 'f1', 'child', 'READY', 1, '2026-08-28T16:00:00Z', '2026-08-28T16:00:00Z');`, id); err != nil {
			t.Fatalf("seed work item %s: %v", id, err)
		}
	}
	uow := NewUnitOfWork(store)
	at := time.Date(2026, 10, 11, 1, 2, 3, 0, time.UTC)
	link, err := work.NewWorkItemSupersession("p1", "wi-2", "wi-3", "operator-1", "redo", at)
	if err != nil {
		t.Fatalf("NewWorkItemSupersession: %v", err)
	}

	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		stored, err := tx.Work().RecordWorkItemSupersession(ctx, link)
		if err != nil || stored != link {
			t.Fatalf("Record = %+v, %v; want %+v", stored, err, link)
		}
		again, err := tx.Work().RecordWorkItemSupersession(ctx, link)
		if err != nil || again != link {
			t.Fatalf("a second identical Record = %+v, %v; want the stored row", again, err)
		}
		// another old work item for the same new one, and another new one for the same old one: refused
		other, _ := work.NewWorkItemSupersession("p1", "wi-1", "wi-3", "operator-1", "redo", at)
		if _, err := tx.Work().RecordWorkItemSupersession(ctx, other); !errors.Is(err, ports.ErrPersistenceAlreadyExists) {
			t.Fatalf("Record for an already-replacing work item = %v, want ErrPersistenceAlreadyExists", err)
		}
		other, _ = work.NewWorkItemSupersession("p1", "wi-2", "wi-1", "operator-1", "redo", at)
		if _, err := tx.Work().RecordWorkItemSupersession(ctx, other); !errors.Is(err, ports.ErrPersistenceAlreadyExists) {
			t.Fatalf("Record for an already-replaced work item = %v, want ErrPersistenceAlreadyExists", err)
		}
		missing, _ := work.NewWorkItemSupersession("p1", "wi-2", "wi-missing", "operator-1", "redo", at)
		if _, err := tx.Work().RecordWorkItemSupersession(ctx, missing); !errors.Is(err, ports.ErrPersistenceNotFound) {
			t.Fatalf("Record naming a missing work item = %v, want ErrPersistenceNotFound", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		byOld, err := tx.Work().GetWorkItemSupersessionByOld(ctx, "wi-2")
		if err != nil || byOld != link {
			t.Fatalf("ByOld = %+v, %v; want %+v", byOld, err, link)
		}
		byNew, err := tx.Work().GetWorkItemSupersessionByNew(ctx, "wi-3")
		if err != nil || byNew != link {
			t.Fatalf("ByNew = %+v, %v; want %+v", byNew, err, link)
		}
		if _, err := tx.Work().GetWorkItemSupersessionByOld(ctx, "wi-3"); !errors.Is(err, ports.ErrPersistenceNotFound) {
			t.Fatalf("ByOld for a work item nothing replaced = %v, want ErrPersistenceNotFound", err)
		}
		if _, err := tx.Work().GetWorkItemSupersessionByNew(ctx, "wi-2"); !errors.Is(err, ports.ErrPersistenceNotFound) {
			t.Fatalf("ByNew for a work item that replaced nothing = %v, want ErrPersistenceNotFound", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("read: %v", err)
	}
}
