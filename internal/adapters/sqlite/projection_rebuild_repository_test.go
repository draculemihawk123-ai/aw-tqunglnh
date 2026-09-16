package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

func TestProjectionRebuildRepository_CreateOperation_IdempotentByID(t *testing.T) {
	store := openCatalogTestStore(t, "projection-rebuild-create-idempotent.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	op := ports.ProjectionRebuildOperation{
		ID: "op-1", ProjectID: "project-1", ProjectionName: "workitem", Phase: ports.ProjectionRebuildRequested,
		JobID: "job-1", RequestedAt: now, UpdatedAt: now, Version: 1,
	}

	var first ports.ProjectionRebuildOperation
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		first, err = projectionRebuildRepository{tx: tx}.CreateOperation(ctx, op)
		return err
	})
	if first.ID != "op-1" || first.Phase != ports.ProjectionRebuildRequested {
		t.Fatalf("first CreateOperation = %+v, want the inserted row back", first)
	}

	// A duplicate insert of the SAME ID — even with different field values —
	// returns the ORIGINAL stored row unchanged, mirroring
	// createReleaseSetLocalCommitTx's own identical idempotent-by-ID
	// discipline: this is defensive symmetry, never a real update path.
	differentPhase := op
	differentPhase.JobID = "job-DIFFERENT"
	var second ports.ProjectionRebuildOperation
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		second, err = projectionRebuildRepository{tx: tx}.CreateOperation(ctx, differentPhase)
		return err
	})
	if second.JobID != "job-1" {
		t.Fatalf("second CreateOperation (duplicate id) = %+v, want the ORIGINAL stored row (JobID job-1), not the second call's differing JobID", second)
	}
}

func TestProjectionRebuildRepository_GetOperation_NotFound(t *testing.T) {
	store := openCatalogTestStore(t, "projection-rebuild-get-not-found.db")
	ctx := context.Background()

	err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		_, err := projectionRebuildRepository{tx: tx}.GetOperation(ctx, "no-such-operation")
		return err
	})
	if !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("GetOperation(missing) error = %v, want ErrPersistenceNotFound", err)
	}
}

func TestProjectionRebuildRepository_GetActiveOperation_ExcludesTerminalPhases(t *testing.T) {
	store := openCatalogTestStore(t, "projection-rebuild-active-terminal.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := projectionRebuildRepository{tx: tx}.CreateOperation(ctx, ports.ProjectionRebuildOperation{
			ID: "op-1", ProjectID: "project-1", ProjectionName: "workitem", Phase: ports.ProjectionRebuildRequested,
			JobID: "job-1", RequestedAt: now, UpdatedAt: now, Version: 1,
		})
		return err
	})

	// While op-1 is REQUESTED (nonterminal), it IS the active operation.
	active, ok, err := getActiveOperationForTest(t, ctx, store, "project-1", "workitem")
	if err != nil {
		t.Fatalf("GetActiveOperation (nonterminal): %v", err)
	}
	if !ok || active.ID != "op-1" {
		t.Fatalf("GetActiveOperation (nonterminal) = (%+v, %v), want (op-1, true)", active, ok)
	}

	// Simulate a future worker (V6-09A, not yet built by this task) moving
	// op-1 to a terminal phase — this repository deliberately has no public
	// transition method yet (see ports.ProjectionRebuildRepository's own
	// doc comment: pure CRUD, no rebuild-work decision belongs here), so
	// the test reaches around it with a raw UPDATE, exactly standing in for
	// that not-yet-built worker's own eventual CAS.
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE projection_rebuild_operations SET phase = 'SUCCEEDED' WHERE id = 'op-1'`)
		return err
	})

	_, ok, err = getActiveOperationForTest(t, ctx, store, "project-1", "workitem")
	if err != nil {
		t.Fatalf("GetActiveOperation (after terminal): %v", err)
	}
	if ok {
		t.Fatal("GetActiveOperation (after terminal) = true, want false — a SUCCEEDED operation must never be reported active")
	}

	// A brand-new nonterminal operation can now be created for the SAME
	// (project, projectionName): the partial unique index only ever blocks
	// a SECOND nonterminal row, never a nonterminal row that follows a
	// now-terminal one.
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := projectionRebuildRepository{tx: tx}.CreateOperation(ctx, ports.ProjectionRebuildOperation{
			ID: "op-2", ProjectID: "project-1", ProjectionName: "workitem", Phase: ports.ProjectionRebuildRequested,
			JobID: "job-2", RequestedAt: now, UpdatedAt: now, Version: 1,
		})
		return err
	})
	active, ok, err = getActiveOperationForTest(t, ctx, store, "project-1", "workitem")
	if err != nil {
		t.Fatalf("GetActiveOperation (op-2): %v", err)
	}
	if !ok || active.ID != "op-2" {
		t.Fatalf("GetActiveOperation (op-2) = (%+v, %v), want (op-2, true)", active, ok)
	}
}

// TestProjectionRebuildRepository_ActiveUniqueIndex_BlocksSecondNonterminal
// is migration 0041's own idx_projection_rebuild_operations_active partial
// unique index proving itself as a real, schema-level backstop — never
// relied on for the TYPED conflict (that is
// internal/app/projectionrebuild's own application-level
// GetActiveOperation pre-check, tested at that layer), but a genuine
// invariant this table itself must never violate even if some future
// caller bypassed that pre-check.
func TestProjectionRebuildRepository_ActiveUniqueIndex_BlocksSecondNonterminal(t *testing.T) {
	store := openCatalogTestStore(t, "projection-rebuild-unique-index.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := projectionRebuildRepository{tx: tx}.CreateOperation(ctx, ports.ProjectionRebuildOperation{
			ID: "op-1", ProjectID: "project-1", ProjectionName: "workitem", Phase: ports.ProjectionRebuildRequested,
			JobID: "job-1", RequestedAt: now, UpdatedAt: now, Version: 1,
		})
		return err
	})

	err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		_, err := projectionRebuildRepository{tx: tx}.CreateOperation(ctx, ports.ProjectionRebuildOperation{
			ID: "op-2", ProjectID: "project-1", ProjectionName: "workitem", Phase: ports.ProjectionRebuildRequested,
			JobID: "job-2", RequestedAt: now, UpdatedAt: now, Version: 1,
		})
		return err
	})
	if err == nil {
		t.Fatal("second nonterminal CreateOperation for the same (project, projectionName) = nil error, want a UNIQUE constraint failure")
	}
}

func getActiveOperationForTest(t *testing.T, ctx context.Context, store *Store, projectID, projectionName string) (ports.ProjectionRebuildOperation, bool, error) {
	t.Helper()
	var op ports.ProjectionRebuildOperation
	var ok bool
	err := store.RunReadOnly(ctx, func(tx *sql.Tx) error {
		var err error
		op, ok, err = projectionRebuildRepository{tx: tx}.GetActiveOperation(ctx, projectID, projectionName)
		return err
	})
	return op, ok, err
}
