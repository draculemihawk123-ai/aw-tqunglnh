package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

func TestProjectionRepository_EnsureGeneration_IdempotentAndConflict(t *testing.T) {
	store := openCatalogTestStore(t, "projection-ensure-generation.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		return projectionRepository{tx: tx}.EnsureGeneration(ctx, "project-1", "workitem", 1, 1, now)
	})
	// A second call with the SAME generation is a no-op, never an error.
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		return projectionRepository{tx: tx}.EnsureGeneration(ctx, "project-1", "workitem", 1, 1, now)
	})

	var generation uint64
	var ok bool
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		generation, ok, err = projectionRepository{tx: tx}.GetActiveGeneration(ctx, "project-1", "workitem")
		return err
	})
	if !ok || generation != 1 {
		t.Fatalf("GetActiveGeneration = (%d, %v), want (1, true)", generation, ok)
	}

	// A DIFFERENT generation than the one already active is
	// ErrOptimisticConflict — swapping the active generation is
	// V6-09A's own cutover job, never a plain EnsureGeneration call.
	err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		return projectionRepository{tx: tx}.EnsureGeneration(ctx, "project-1", "workitem", 2, 1, now)
	})
	if !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("EnsureGeneration(2) after active=1: err = %v, want ErrOptimisticConflict", err)
	}
}

func TestProjectionRepository_UpsertAndGetProjectionRow_ReplacesWholesale(t *testing.T) {
	store := openCatalogTestStore(t, "projection-row-upsert.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	t1 := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Minute)

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		return projectionRepository{tx: tx}.UpsertProjectionRow(ctx, ports.ProjectionRow{
			ProjectID: "project-1", ProjectionName: "workitem", Generation: 1, EntityKey: "work-item-1",
			PayloadJSON: `{"status":"BACKLOG"}`, LastAppliedJournalPosition: 10, UpdatedAt: t1,
		})
	})
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		return projectionRepository{tx: tx}.UpsertProjectionRow(ctx, ports.ProjectionRow{
			ProjectID: "project-1", ProjectionName: "workitem", Generation: 1, EntityKey: "work-item-1",
			PayloadJSON: `{"status":"READY"}`, LastAppliedJournalPosition: 11, UpdatedAt: t2,
		})
	})

	var row ports.ProjectionRow
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		row, err = projectionRepository{tx: tx}.GetProjectionRow(ctx, "project-1", "workitem", 1, "work-item-1")
		return err
	})
	if row.PayloadJSON != `{"status":"READY"}` || row.LastAppliedJournalPosition != 11 {
		t.Fatalf("row = %+v, want the SECOND upsert's payload (wholesale replace, not merge)", row)
	}
	if !row.UpdatedAt.Equal(t2) {
		t.Fatalf("row.UpdatedAt = %v, want %v", row.UpdatedAt, t2)
	}

	err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		_, err := projectionRepository{tx: tx}.GetProjectionRow(ctx, "project-1", "workitem", 1, "no-such-entity")
		return err
	})
	if !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("GetProjectionRow(unknown entity): err = %v, want ErrPersistenceNotFound", err)
	}
}

func TestProjectionRepository_ListProjectionRows_EntityKeyAscending(t *testing.T) {
	store := openCatalogTestStore(t, "projection-row-list.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		repo := projectionRepository{tx: tx}
		for _, key := range []string{"work-item-c", "work-item-a", "work-item-b"} {
			if err := repo.UpsertProjectionRow(ctx, ports.ProjectionRow{
				ProjectID: "project-1", ProjectionName: "workitem", Generation: 1, EntityKey: key,
				PayloadJSON: `{}`, UpdatedAt: now,
			}); err != nil {
				return err
			}
		}
		return nil
	})

	var rows []ports.ProjectionRow
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		rows, err = projectionRepository{tx: tx}.ListProjectionRows(ctx, "project-1", "workitem", 1)
		return err
	})
	if len(rows) != 3 {
		t.Fatalf("len(rows) = %d, want 3", len(rows))
	}
	for i, want := range []string{"work-item-a", "work-item-b", "work-item-c"} {
		if rows[i].EntityKey != want {
			t.Fatalf("rows[%d].EntityKey = %q, want %q (EntityKey ascending)", i, rows[i].EntityKey, want)
		}
	}
}

func TestProjectionRepository_UpsertProjectionCheckpoint_FirstInsertThenFencedAdvance(t *testing.T) {
	store := openCatalogTestStore(t, "projection-checkpoint.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	t1 := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Minute)

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		return projectionRepository{tx: tx}.UpsertProjectionCheckpoint(ctx, ports.UpsertProjectionCheckpointRequest{
			ProjectID: "project-1", ProjectionName: "workitem", Generation: 1,
			NewCursor: 5, NewStatus: ports.ProjectionLive, UpdatedAt: t1,
		})
	})

	// A second "first insert" (nil ExpectedCursor) against an existing
	// checkpoint is ErrOptimisticConflict, never a silent overwrite.
	err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		return projectionRepository{tx: tx}.UpsertProjectionCheckpoint(ctx, ports.UpsertProjectionCheckpointRequest{
			ProjectID: "project-1", ProjectionName: "workitem", Generation: 1,
			NewCursor: 6, NewStatus: ports.ProjectionLive, UpdatedAt: t2,
		})
	})
	if !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("second nil-ExpectedCursor insert: err = %v, want ErrOptimisticConflict", err)
	}

	// A fenced advance with the correct ExpectedCursor succeeds.
	expected := uint64(5)
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		return projectionRepository{tx: tx}.UpsertProjectionCheckpoint(ctx, ports.UpsertProjectionCheckpointRequest{
			ProjectID: "project-1", ProjectionName: "workitem", Generation: 1,
			ExpectedCursor: &expected, NewCursor: 12, NewStatus: ports.ProjectionLive, UpdatedAt: t2,
		})
	})

	// A stale ExpectedCursor is ErrOptimisticConflict, not a silent
	// overwrite — this is the exact race two concurrent apply attempts
	// racing to advance the same generation's cursor must never resolve
	// by letting the stale one win.
	staleExpected := uint64(5)
	err = store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		return projectionRepository{tx: tx}.UpsertProjectionCheckpoint(ctx, ports.UpsertProjectionCheckpointRequest{
			ProjectID: "project-1", ProjectionName: "workitem", Generation: 1,
			ExpectedCursor: &staleExpected, NewCursor: 99, NewStatus: ports.ProjectionLive, UpdatedAt: t2,
		})
	})
	if !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("stale ExpectedCursor advance: err = %v, want ErrOptimisticConflict", err)
	}

	var checkpoint ports.ProjectionCheckpoint
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		checkpoint, err = projectionRepository{tx: tx}.GetProjectionCheckpoint(ctx, "project-1", "workitem", 1)
		return err
	})
	if checkpoint.Cursor != 12 {
		t.Fatalf("checkpoint.Cursor = %d, want 12 (the successful fenced advance, not the rejected stale one)", checkpoint.Cursor)
	}
}

func TestProjectionRepository_RecordAndListProjectionPoison_JournalPositionAscending(t *testing.T) {
	store := openCatalogTestStore(t, "projection-poison.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		repo := projectionRepository{tx: tx}
		for i, position := range []uint64{30, 10, 20} {
			if err := repo.RecordProjectionPoison(ctx, ports.ProjectionPoisonRecord{
				ID: "poison-" + string(rune('a'+i)), ProjectID: "project-1", ProjectionName: "workitem", Generation: 1,
				JournalPosition: position, EventType: "SOME_EVENT", SchemaVersion: 1, Reason: "corrupt payload", RecordedAt: now,
			}); err != nil {
				return err
			}
		}
		return nil
	})

	var records []ports.ProjectionPoisonRecord
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		records, err = projectionRepository{tx: tx}.ListProjectionPoison(ctx, "project-1", "workitem", 1)
		return err
	})
	if len(records) != 3 {
		t.Fatalf("len(records) = %d, want 3", len(records))
	}
	for i, want := range []uint64{10, 20, 30} {
		if records[i].JournalPosition != want {
			t.Fatalf("records[%d].JournalPosition = %d, want %d (ascending)", i, records[i].JournalPosition, want)
		}
	}

	// A DUPLICATE poison record ID is a real error, never silently
	// absorbed — ID-minting/idempotency strategy is deliberately left to
	// V6-08A (see ProjectionRepository's own interface doc comment), not
	// assumed by this repository.
	err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		return projectionRepository{tx: tx}.RecordProjectionPoison(ctx, ports.ProjectionPoisonRecord{
			ID: "poison-a", ProjectID: "project-1", ProjectionName: "workitem", Generation: 1,
			JournalPosition: 40, EventType: "SOME_EVENT", SchemaVersion: 1, Reason: "corrupt payload", RecordedAt: now,
		})
	})
	if err == nil {
		t.Fatal("RecordProjectionPoison with a duplicate ID: want an error, got nil")
	}
}
