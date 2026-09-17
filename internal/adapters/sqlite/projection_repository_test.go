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

func TestProjectionRepository_AcquireOrRenewConsumerLease_FirstAcquireCreatesCheckpoint(t *testing.T) {
	store := openCatalogTestStore(t, "projection-lease-first.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

	var lease ports.ConsumerLease
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		lease, err = projectionRepository{tx: tx}.AcquireOrRenewConsumerLease(ctx, ports.AcquireOrRenewConsumerLeaseRequest{
			ProjectID: "project-1", ProjectionName: "workitem", Generation: 1,
			Owner: "consumer-a", TTL: 30 * time.Second, Now: now,
		})
		return err
	})
	if lease.FenceToken != 1 || lease.Cursor != 0 || lease.Status != ports.ProjectionLive {
		t.Fatalf("first acquire lease = %+v, want FenceToken=1 Cursor=0 Status=LIVE", lease)
	}
	if !lease.LeaseUntil.Equal(now.Add(30 * time.Second)) {
		t.Fatalf("lease.LeaseUntil = %v, want %v", lease.LeaseUntil, now.Add(30*time.Second))
	}

	var checkpoint ports.ProjectionCheckpoint
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		checkpoint, err = projectionRepository{tx: tx}.GetProjectionCheckpoint(ctx, "project-1", "workitem", 1)
		return err
	})
	if checkpoint.FenceToken != 1 {
		t.Fatalf("checkpoint.FenceToken = %d, want 1 (the lease-acquire call is what creates the checkpoint row)", checkpoint.FenceToken)
	}
}

func TestProjectionRepository_AcquireOrRenewConsumerLease_ConflictWhileHeldThenStealAfterExpiry(t *testing.T) {
	store := openCatalogTestStore(t, "projection-lease-conflict.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	t0 := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := projectionRepository{tx: tx}.AcquireOrRenewConsumerLease(ctx, ports.AcquireOrRenewConsumerLeaseRequest{
			ProjectID: "project-1", ProjectionName: "workitem", Generation: 1,
			Owner: "consumer-a", TTL: 30 * time.Second, Now: t0,
		})
		return err
	})

	// A second consumer trying to acquire while consumer-a's lease is
	// still live (t0+10s, well inside the 30s TTL) is a real conflict —
	// this is the "two consumers" case: only one may ever hold an
	// unexpired lease at a time.
	err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		_, err := projectionRepository{tx: tx}.AcquireOrRenewConsumerLease(ctx, ports.AcquireOrRenewConsumerLeaseRequest{
			ProjectID: "project-1", ProjectionName: "workitem", Generation: 1,
			Owner: "consumer-b", TTL: 30 * time.Second, Now: t0.Add(10 * time.Second),
		})
		return err
	})
	if !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("consumer-b acquire while consumer-a's lease is live: err = %v, want ErrOptimisticConflict", err)
	}

	// Once consumer-a's lease has genuinely expired (t0+31s, past the 30s
	// TTL), consumer-b can steal it — fence_token increments, proving any
	// batch consumer-a might still try to commit under its own now-stale
	// fence_token will be rejected by UpsertProjectionCheckpoint's own
	// ExpectedFenceToken check.
	var stolen ports.ConsumerLease
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		stolen, err = projectionRepository{tx: tx}.AcquireOrRenewConsumerLease(ctx, ports.AcquireOrRenewConsumerLeaseRequest{
			ProjectID: "project-1", ProjectionName: "workitem", Generation: 1,
			Owner: "consumer-b", TTL: 30 * time.Second, Now: t0.Add(31 * time.Second),
		})
		return err
	})
	if stolen.FenceToken != 2 {
		t.Fatalf("stolen.FenceToken = %d, want 2 (incremented on steal)", stolen.FenceToken)
	}
}

func TestProjectionRepository_AcquireOrRenewConsumerLease_SameOwnerCanRenewBeforeExpiry(t *testing.T) {
	store := openCatalogTestStore(t, "projection-lease-renew.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	t0 := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := projectionRepository{tx: tx}.AcquireOrRenewConsumerLease(ctx, ports.AcquireOrRenewConsumerLeaseRequest{
			ProjectID: "project-1", ProjectionName: "workitem", Generation: 1,
			Owner: "consumer-a", TTL: 30 * time.Second, Now: t0,
		})
		return err
	})

	// consumer-a renews its OWN lease well before it would expire (t0+5s,
	// inside the 30s TTL) — must succeed, not conflict, since a
	// long-running consumer's own self-rescheduling loop needs to
	// heartbeat itself repeatedly.
	var renewed ports.ConsumerLease
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		renewed, err = projectionRepository{tx: tx}.AcquireOrRenewConsumerLease(ctx, ports.AcquireOrRenewConsumerLeaseRequest{
			ProjectID: "project-1", ProjectionName: "workitem", Generation: 1,
			Owner: "consumer-a", TTL: 30 * time.Second, Now: t0.Add(5 * time.Second),
		})
		return err
	})
	if renewed.FenceToken != 2 {
		t.Fatalf("renewed.FenceToken = %d, want 2 (a renewal still bumps the fence, even for the same owner)", renewed.FenceToken)
	}
	if !renewed.LeaseUntil.Equal(t0.Add(5 * time.Second).Add(30 * time.Second)) {
		t.Fatalf("renewed.LeaseUntil = %v, want the extended deadline from the renewal's own Now+TTL", renewed.LeaseUntil)
	}
}

func TestProjectionRepository_UpsertProjectionCheckpoint_StaleFenceTokenRejected(t *testing.T) {
	store := openCatalogTestStore(t, "projection-checkpoint-stale-fence.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

	// consumer-a acquires (fence_token 1), then consumer-b steals after
	// expiry (fence_token 2) — simulating consumer-a having been
	// mid-batch when its own lease expired and got stolen.
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := projectionRepository{tx: tx}.AcquireOrRenewConsumerLease(ctx, ports.AcquireOrRenewConsumerLeaseRequest{
			ProjectID: "project-1", ProjectionName: "workitem", Generation: 1,
			Owner: "consumer-a", TTL: 1 * time.Second, Now: now,
		})
		return err
	})
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := projectionRepository{tx: tx}.AcquireOrRenewConsumerLease(ctx, ports.AcquireOrRenewConsumerLeaseRequest{
			ProjectID: "project-1", ProjectionName: "workitem", Generation: 1,
			Owner: "consumer-b", TTL: 30 * time.Second, Now: now.Add(2 * time.Second),
		})
		return err
	})

	// consumer-a now tries to commit its own batch, still believing it
	// holds fence_token 1 — must be rejected, never silently applied.
	staleFence := uint64(1)
	expectedCursor := uint64(0)
	err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		return projectionRepository{tx: tx}.UpsertProjectionCheckpoint(ctx, ports.UpsertProjectionCheckpointRequest{
			ProjectID: "project-1", ProjectionName: "workitem", Generation: 1,
			ExpectedCursor: &expectedCursor, ExpectedFenceToken: &staleFence,
			NewCursor: 5, NewStatus: ports.ProjectionLive, UpdatedAt: now,
		})
	})
	if !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("advance with stale fence_token: err = %v, want ErrOptimisticConflict", err)
	}

	// The CURRENT fence_token (2, consumer-b's) succeeds.
	currentFence := uint64(2)
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		return projectionRepository{tx: tx}.UpsertProjectionCheckpoint(ctx, ports.UpsertProjectionCheckpointRequest{
			ProjectID: "project-1", ProjectionName: "workitem", Generation: 1,
			ExpectedCursor: &expectedCursor, ExpectedFenceToken: &currentFence,
			NewCursor: 5, NewStatus: ports.ProjectionLive, UpdatedAt: now,
		})
	})

	var checkpoint ports.ProjectionCheckpoint
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		checkpoint, err = projectionRepository{tx: tx}.GetProjectionCheckpoint(ctx, "project-1", "workitem", 1)
		return err
	})
	if checkpoint.Cursor != 5 {
		t.Fatalf("checkpoint.Cursor = %d, want 5 (consumer-b's own valid advance)", checkpoint.Cursor)
	}
}

func TestProjectionRepository_CutoverProjectionGeneration_BootstrapAndCAS(t *testing.T) {
	store := openCatalogTestStore(t, "projection-cutover-bootstrap.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

	// Bootstrap: no active generation exists yet, ExpectedGeneration nil
	// is a plain first-ever insert.
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		return projectionRepository{tx: tx}.CutoverProjectionGeneration(ctx, ports.CutoverProjectionGenerationRequest{
			ProjectID: "project-1", ProjectionName: "workitem", ExpectedGeneration: nil, NewGeneration: 1, SchemaVersion: 1, UpdatedAt: now,
		})
	})
	// A second bootstrap call for the SAME already-stored generation is
	// idempotent, mirroring EnsureGeneration's own convention.
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		return projectionRepository{tx: tx}.CutoverProjectionGeneration(ctx, ports.CutoverProjectionGenerationRequest{
			ProjectID: "project-1", ProjectionName: "workitem", ExpectedGeneration: nil, NewGeneration: 1, SchemaVersion: 1, UpdatedAt: now,
		})
	})
	// A bootstrap call for a DIFFERENT generation than what is already
	// stored is ErrOptimisticConflict.
	if err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		return projectionRepository{tx: tx}.CutoverProjectionGeneration(ctx, ports.CutoverProjectionGenerationRequest{
			ProjectID: "project-1", ProjectionName: "workitem", ExpectedGeneration: nil, NewGeneration: 2, SchemaVersion: 1, UpdatedAt: now,
		})
	}); !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("bootstrap(2) after active=1: err = %v, want ErrOptimisticConflict", err)
	}

	// A real cutover: CAS from active=1 to a shadow generation 2 succeeds
	// exactly once.
	old := uint64(1)
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		return projectionRepository{tx: tx}.CutoverProjectionGeneration(ctx, ports.CutoverProjectionGenerationRequest{
			ProjectID: "project-1", ProjectionName: "workitem", ExpectedGeneration: &old, NewGeneration: 2, SchemaVersion: 1, UpdatedAt: now,
		})
	})
	var active uint64
	var ok bool
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		active, ok, err = projectionRepository{tx: tx}.GetActiveGeneration(ctx, "project-1", "workitem")
		return err
	})
	if !ok || active != 2 {
		t.Fatalf("active generation after cutover = (%d, %v), want (2, true)", active, ok)
	}

	// A STALE worker's own cutover attempt — still presenting the OLD
	// (now superseded) ExpectedGeneration=1 — fails closed: "a stale
	// worker cannot swap" (V6-09A's own Thực hiện line), not a silent
	// double-apply.
	if err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		return projectionRepository{tx: tx}.CutoverProjectionGeneration(ctx, ports.CutoverProjectionGenerationRequest{
			ProjectID: "project-1", ProjectionName: "workitem", ExpectedGeneration: &old, NewGeneration: 2, SchemaVersion: 1, UpdatedAt: now,
		})
	}); !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("stale cutover retry: err = %v, want ErrOptimisticConflict", err)
	}
}

func TestProjectionRepository_DiscardGeneration_RefusesActiveDeletesOthers(t *testing.T) {
	store := openCatalogTestStore(t, "projection-discard-generation.db")
	ctx := context.Background()
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		repo := projectionRepository{tx: tx}
		if err := repo.EnsureGeneration(ctx, "project-1", "workitem", 1, 1, now); err != nil {
			return err
		}
		if err := repo.UpsertProjectionRow(ctx, ports.ProjectionRow{
			ProjectID: "project-1", ProjectionName: "workitem", Generation: 1, EntityKey: "work-item-1",
			PayloadJSON: `{"status":"BACKLOG"}`, LastAppliedJournalPosition: 1, UpdatedAt: now,
		}); err != nil {
			return err
		}
		// Generation 2 exists (e.g. an abandoned shadow build) but was
		// never cut over — active_generation stays 1.
		return repo.UpsertProjectionRow(ctx, ports.ProjectionRow{
			ProjectID: "project-1", ProjectionName: "workitem", Generation: 2, EntityKey: "work-item-1",
			PayloadJSON: `{"status":"BACKLOG"}`, LastAppliedJournalPosition: 1, UpdatedAt: now,
		})
	})

	// Refuses to discard the currently ACTIVE generation — "never clear
	// the active generation" (V6-09A's own Không làm line), enforced here
	// as defense in depth beneath whatever eligibility check a caller
	// already performed.
	if err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		return projectionRepository{tx: tx}.DiscardGeneration(ctx, "project-1", "workitem", 1)
	}); !errors.Is(err, ports.ErrCannotDiscardActiveGeneration) {
		t.Fatalf("DiscardGeneration(active=1): err = %v, want ErrCannotDiscardActiveGeneration", err)
	}
	// Generation 1's own row must still exist — refused, not partially
	// applied.
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := projectionRepository{tx: tx}.GetProjectionRow(ctx, "project-1", "workitem", 1, "work-item-1")
		if err != nil {
			t.Fatalf("generation 1 row missing after a refused discard: %v", err)
		}
		return nil
	})

	// Discarding the NON-active generation 2 succeeds and removes its
	// rows/checkpoint.
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		return projectionRepository{tx: tx}.DiscardGeneration(ctx, "project-1", "workitem", 2)
	})
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := projectionRepository{tx: tx}.GetProjectionRow(ctx, "project-1", "workitem", 2, "work-item-1")
		if !errors.Is(err, ports.ErrPersistenceNotFound) {
			t.Fatalf("generation 2 row after discard: err = %v, want ErrPersistenceNotFound", err)
		}
		return nil
	})
	// Generation 1 (still active) is completely untouched by discarding 2.
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		row, err := projectionRepository{tx: tx}.GetProjectionRow(ctx, "project-1", "workitem", 1, "work-item-1")
		if err != nil {
			t.Fatalf("generation 1 row after discarding generation 2: %v", err)
		}
		if row.PayloadJSON != `{"status":"BACKLOG"}` {
			t.Fatalf("generation 1 row payload changed: %q", row.PayloadJSON)
		}
		return nil
	})
}
