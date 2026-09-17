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

// TestProjectionRebuildRepository_AdvanceOperation_FencedAndNilLeavesUnchanged
// exercises V6-09A's own AdvanceOperation CAS: version fencing rejects a
// stale caller, and every "Next*" field's nil-means-unchanged convention
// really does leave an already-populated column alone across a LATER
// unrelated advance.
func TestProjectionRebuildRepository_AdvanceOperation_FencedAndNilLeavesUnchanged(t *testing.T) {
	store := openCatalogTestStore(t, "projection-rebuild-advance.db")
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

	// SNAPSHOTTING sets W0 and ShadowGeneration together.
	w0 := uint64(10)
	shadowGen := uint64(2)
	var afterSnapshot ports.ProjectionRebuildOperation
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		afterSnapshot, err = projectionRebuildRepository{tx: tx}.AdvanceOperation(ctx, ports.AdvanceProjectionRebuildOperationRequest{
			ID: "op-1", ExpectedVersion: 1, NextPhase: ports.ProjectionRebuildSnapshotting,
			NextW0: &w0, NextShadowGeneration: &shadowGen, UpdatedAt: now.Add(time.Second),
		})
		return err
	})
	if afterSnapshot.Phase != ports.ProjectionRebuildSnapshotting || afterSnapshot.Version != 2 {
		t.Fatalf("afterSnapshot = %+v, want Phase=SNAPSHOTTING Version=2", afterSnapshot)
	}
	if afterSnapshot.W0 == nil || *afterSnapshot.W0 != 10 || afterSnapshot.ShadowGeneration == nil || *afterSnapshot.ShadowGeneration != 2 {
		t.Fatalf("afterSnapshot W0/ShadowGeneration = %v/%v, want 10/2", afterSnapshot.W0, afterSnapshot.ShadowGeneration)
	}

	// A STALE caller — still presenting ExpectedVersion=1, now superseded —
	// is rejected with ErrOptimisticConflict, never a silent overwrite
	// ("two workers racing" — V6-09A's own Verify line).
	if err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		_, err := projectionRebuildRepository{tx: tx}.AdvanceOperation(ctx, ports.AdvanceProjectionRebuildOperationRequest{
			ID: "op-1", ExpectedVersion: 1, NextPhase: ports.ProjectionRebuildBuilding, UpdatedAt: now.Add(2 * time.Second),
		})
		return err
	}); !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("stale AdvanceOperation: err = %v, want ErrOptimisticConflict", err)
	}

	// BUILDING advances ShadowCursor repeatedly, WITHOUT ever touching the
	// already-populated W0/ShadowGeneration (nil for those fields on this
	// call).
	cursor1 := uint64(50)
	var afterBuild1 ports.ProjectionRebuildOperation
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		afterBuild1, err = projectionRebuildRepository{tx: tx}.AdvanceOperation(ctx, ports.AdvanceProjectionRebuildOperationRequest{
			ID: "op-1", ExpectedVersion: 2, NextPhase: ports.ProjectionRebuildBuilding,
			NextShadowCursor: &cursor1, UpdatedAt: now.Add(3 * time.Second),
		})
		return err
	})
	if afterBuild1.ShadowCursor == nil || *afterBuild1.ShadowCursor != 50 {
		t.Fatalf("afterBuild1.ShadowCursor = %v, want 50", afterBuild1.ShadowCursor)
	}
	if afterBuild1.W0 == nil || *afterBuild1.W0 != 10 || afterBuild1.ShadowGeneration == nil || *afterBuild1.ShadowGeneration != 2 {
		t.Fatalf("afterBuild1 W0/ShadowGeneration changed by an unrelated advance: %v/%v, want unchanged 10/2", afterBuild1.W0, afterBuild1.ShadowGeneration)
	}

	cursor2 := uint64(90)
	var afterBuild2 ports.ProjectionRebuildOperation
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		afterBuild2, err = projectionRebuildRepository{tx: tx}.AdvanceOperation(ctx, ports.AdvanceProjectionRebuildOperationRequest{
			ID: "op-1", ExpectedVersion: 3, NextPhase: ports.ProjectionRebuildBuilding,
			NextShadowCursor: &cursor2, UpdatedAt: now.Add(4 * time.Second),
		})
		return err
	})
	if afterBuild2.ShadowCursor == nil || *afterBuild2.ShadowCursor != 90 {
		t.Fatalf("afterBuild2.ShadowCursor = %v, want 90 (advanced from 50)", afterBuild2.ShadowCursor)
	}

	// A terminal FAILED transition sets ErrorCode/ErrorMessage, both nil
	// on every earlier call.
	errCode, errMsg := "POISON_EVENT", "an unrecoverable event was found during shadow replay"
	var afterFail ports.ProjectionRebuildOperation
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		afterFail, err = projectionRebuildRepository{tx: tx}.AdvanceOperation(ctx, ports.AdvanceProjectionRebuildOperationRequest{
			ID: "op-1", ExpectedVersion: 4, NextPhase: ports.ProjectionRebuildFailed,
			NextErrorCode: &errCode, NextErrorMessage: &errMsg, UpdatedAt: now.Add(5 * time.Second),
		})
		return err
	})
	if afterFail.Phase != ports.ProjectionRebuildFailed || afterFail.ErrorCode != errCode || afterFail.ErrorMessage != errMsg {
		t.Fatalf("afterFail = %+v, want Phase=FAILED ErrorCode=%q ErrorMessage=%q", afterFail, errCode, errMsg)
	}
	if afterFail.ShadowCursor == nil || *afterFail.ShadowCursor != 90 {
		t.Fatalf("afterFail.ShadowCursor changed by the FAILED transition: %v, want unchanged 90", afterFail.ShadowCursor)
	}

	// AdvanceOperation on an unknown ID is ErrPersistenceNotFound (via the
	// post-CAS-miss reload), never a bare "0 rows affected" silently
	// treated as success.
	if err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		_, err := projectionRebuildRepository{tx: tx}.AdvanceOperation(ctx, ports.AdvanceProjectionRebuildOperationRequest{
			ID: "no-such-op", ExpectedVersion: 1, NextPhase: ports.ProjectionRebuildSnapshotting, UpdatedAt: now,
		})
		return err
	}); !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("AdvanceOperation(unknown id): err = %v, want ErrPersistenceNotFound", err)
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
