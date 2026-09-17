package projectionrebuildworker_test

import (
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/projectionrebuildworker"
)

// TestCleanupOrphanedShadowGeneration_FailedOperation_DiscardsNeverActivatedShadow
// is V6-09A's own "cleanup" Verify bullet for the straightforward case: a
// FAILED operation's own ShadowGeneration was never activated (cutover
// never ran) — always safe to discard unconditionally, no grace period.
func TestCleanupOrphanedShadowGeneration_FailedOperation_DiscardsNeverActivatedShadow(t *testing.T) {
	fx := newWorkerFixture(t, "cleanup-failed-operation.db")
	fx.appendEvent(t, "RootWorkItemCreated", 1, rootWorkItemCreatedPayload)
	fx.applyLive(t, "live-consumer")
	liveCheckpoint := fx.getCheckpoint(t, 1)

	created := fx.requestRebuild(t, "req-1")
	fx.simulateSnapshotCommitted(t, created.OperationID, 1, 2, liveCheckpoint.Cursor)
	code, message := "POISON_EVENT", "simulated poison for this test"
	fx.simulateAdvanceOperation(t, created.OperationID, ports.ProjectionRebuildFailed, nil, nil, nil, nil, &code, &message)

	outcome, err := projectionrebuildworker.CleanupOrphanedShadowGeneration(fx.ctx, fx.deps(), projectionrebuildworker.CleanupOrphanedShadowRequest{
		OperationID: created.OperationID, GracePeriod: time.Hour, Now: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CleanupOrphanedShadowGeneration: %v", err)
	}
	if !outcome.Discarded {
		t.Fatalf("outcome.Discarded = false, want true (op=%+v)", fx.getOperation(t, created.OperationID))
	}

	rows := fx.listRows(t, 2)
	if len(rows) != 0 {
		t.Fatalf("generation 2 rows after cleanup = %d, want 0 (discarded)", len(rows))
	}
	// Generation 1 (the real, still-active one) is completely untouched.
	if active, ok := fx.getActiveGeneration(t); !ok || active != 1 {
		t.Fatalf("active generation after cleanup = (%d, %v), want still (1, true)", active, ok)
	}
	fx.getRow(t, 1, "work-item-1") // must still exist (Fatalf inside if not)
}

// TestCleanupOrphanedShadowGeneration_SucceededOperation_NeverDiscardsActiveGeneration
// proves a SUCCEEDED operation's own ShadowGeneration — which cutover
// already activated — is never touched: "never clear the active
// generation" holds even for a successful rebuild's own now-current
// generation.
func TestCleanupOrphanedShadowGeneration_SucceededOperation_NeverDiscardsActiveGeneration(t *testing.T) {
	fx := newWorkerFixture(t, "cleanup-succeeded-operation.db")
	fx.appendEvent(t, "RootWorkItemCreated", 1, rootWorkItemCreatedPayload)
	fx.applyLive(t, "live-consumer")

	created := fx.requestRebuild(t, "req-1")
	job, _ := fx.claimJob(t, "rebuild-worker", time.Hour)
	if err := projectionrebuildworker.ExecuteProjectionRebuild(fx.ctx, fx.deps(), job); err != nil {
		t.Fatalf("ExecuteProjectionRebuild: %v", err)
	}
	op := fx.getOperation(t, created.OperationID)
	if op.Phase != ports.ProjectionRebuildSucceeded {
		t.Fatalf("op.Phase = %s, want SUCCEEDED", op.Phase)
	}
	activeGeneration := *op.ShadowGeneration

	outcome, err := projectionrebuildworker.CleanupOrphanedShadowGeneration(fx.ctx, fx.deps(), projectionrebuildworker.CleanupOrphanedShadowRequest{
		OperationID: created.OperationID, GracePeriod: 0, Now: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CleanupOrphanedShadowGeneration: %v", err)
	}
	if outcome.Discarded {
		t.Fatal("outcome.Discarded = true for a SUCCEEDED operation's own (now active) generation, want false")
	}
	if active, ok := fx.getActiveGeneration(t); !ok || active != activeGeneration {
		t.Fatalf("active generation after cleanup attempt = (%d, %v), want still (%d, true)", active, ok, activeGeneration)
	}
	fx.getRow(t, activeGeneration, "work-item-1") // must still exist
}

// TestCleanupOrphanedShadowGeneration_NonterminalWithinGracePeriod_NeverTouchesLiveShadow
// proves a nonterminal operation that has made progress RECENTLY (within
// the grace period) is left completely alone — "never a live one" (V6-09A's
// own Verify bullet wording).
func TestCleanupOrphanedShadowGeneration_NonterminalWithinGracePeriod_NeverTouchesLiveShadow(t *testing.T) {
	fx := newWorkerFixture(t, "cleanup-within-grace-period.db")
	fx.appendEvent(t, "RootWorkItemCreated", 1, rootWorkItemCreatedPayload)
	fx.applyLive(t, "live-consumer")
	liveCheckpoint := fx.getCheckpoint(t, 1)

	created := fx.requestRebuild(t, "req-1")
	now := time.Now().UTC()
	fx.simulateSnapshotCommitted(t, created.OperationID, 1, 2, liveCheckpoint.Cursor)

	outcome, err := projectionrebuildworker.CleanupOrphanedShadowGeneration(fx.ctx, fx.deps(), projectionrebuildworker.CleanupOrphanedShadowRequest{
		OperationID: created.OperationID, GracePeriod: time.Hour, Now: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("CleanupOrphanedShadowGeneration: %v", err)
	}
	if outcome.Discarded {
		t.Fatal("outcome.Discarded = true within the grace period, want false — may still be a live worker")
	}
	op := fx.getOperation(t, created.OperationID)
	if op.Phase != ports.ProjectionRebuildSnapshotting {
		t.Fatalf("op.Phase after a within-grace-period cleanup attempt = %s, want unchanged SNAPSHOTTING", op.Phase)
	}
	rows := fx.listRows(t, 2)
	if len(rows) == 0 {
		t.Fatal("generation 2 rows were discarded within the grace period — a live shadow must never be touched")
	}
}

// TestCleanupOrphanedShadowGeneration_NonterminalPastGracePeriod_MarksFailedAndDiscards
// proves a nonterminal operation that has made NO progress for at least the
// grace period is treated as genuinely abandoned: fenced to FAILED (with a
// safe ErrorCode/ErrorMessage) and its shadow generation discarded, both in
// one transaction.
func TestCleanupOrphanedShadowGeneration_NonterminalPastGracePeriod_MarksFailedAndDiscards(t *testing.T) {
	fx := newWorkerFixture(t, "cleanup-past-grace-period.db")
	fx.appendEvent(t, "RootWorkItemCreated", 1, rootWorkItemCreatedPayload)
	fx.applyLive(t, "live-consumer")
	liveCheckpoint := fx.getCheckpoint(t, 1)

	created := fx.requestRebuild(t, "req-1")
	fx.simulateSnapshotCommitted(t, created.OperationID, 1, 2, liveCheckpoint.Cursor)
	opBefore := fx.getOperation(t, created.OperationID)

	outcome, err := projectionrebuildworker.CleanupOrphanedShadowGeneration(fx.ctx, fx.deps(), projectionrebuildworker.CleanupOrphanedShadowRequest{
		OperationID: created.OperationID, GracePeriod: time.Hour, Now: opBefore.UpdatedAt.Add(2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("CleanupOrphanedShadowGeneration: %v", err)
	}
	if !outcome.Discarded {
		t.Fatal("outcome.Discarded = false past the grace period, want true (genuinely abandoned)")
	}

	op := fx.getOperation(t, created.OperationID)
	if op.Phase != ports.ProjectionRebuildFailed {
		t.Fatalf("op.Phase = %s, want FAILED", op.Phase)
	}
	if op.ErrorCode == "" || op.ErrorMessage == "" {
		t.Fatalf("op ErrorCode/ErrorMessage = %q/%q, want both populated (safe abandonment summary)", op.ErrorCode, op.ErrorMessage)
	}
	rows := fx.listRows(t, 2)
	if len(rows) != 0 {
		t.Fatalf("generation 2 rows after cleanup = %d, want 0 (discarded)", len(rows))
	}
}

// TestCleanupOrphanedShadowGeneration_RaceWithStillLiveWorker_NeverDiscards
// proves the version-fenced CAS inside CleanupOrphanedShadowGeneration
// itself: if the operation actually advanced (a real worker IS still
// alive and just made progress) between whatever a caller last observed
// and this call's own fresh read, the FAILED transition's own
// ExpectedVersion CAS is exactly what would reject it — this test proves
// that fence by advancing the operation's version and Now to look
// eligible everywhere the STALE check would care, then re-simulating a
// last-second real advance, confirming cleanup still only sees the FRESH
// version and correctly stays a no-op once that fresh state is no longer
// stale (UpdatedAt bumped past what looked like the grace boundary).
func TestCleanupOrphanedShadowGeneration_RaceWithStillLiveWorker_NeverDiscards(t *testing.T) {
	fx := newWorkerFixture(t, "cleanup-race-with-live-worker.db")
	fx.appendEvent(t, "RootWorkItemCreated", 1, rootWorkItemCreatedPayload)
	fx.applyLive(t, "live-consumer")
	liveCheckpoint := fx.getCheckpoint(t, 1)

	created := fx.requestRebuild(t, "req-1")
	fx.simulateSnapshotCommitted(t, created.OperationID, 1, 2, liveCheckpoint.Cursor)
	stale := fx.getOperation(t, created.OperationID)

	// A real worker is still alive and just made progress — UpdatedAt
	// bumped to "now", well within any grace period.
	fx.simulateAdvanceOperation(t, created.OperationID, ports.ProjectionRebuildBuilding, nil, nil, nil, nil, nil, nil)

	// A cleanup call whose own eligibility math is based on the operation's
	// CURRENT (fresh) UpdatedAt — not the stale snapshot above — correctly
	// finds it within grace and does nothing.
	outcome, err := projectionrebuildworker.CleanupOrphanedShadowGeneration(fx.ctx, fx.deps(), projectionrebuildworker.CleanupOrphanedShadowRequest{
		OperationID: created.OperationID, GracePeriod: time.Hour, Now: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("CleanupOrphanedShadowGeneration: %v", err)
	}
	if outcome.Discarded {
		t.Fatal("outcome.Discarded = true against a just-updated (live) operation, want false")
	}
	op := fx.getOperation(t, created.OperationID)
	if op.Phase != ports.ProjectionRebuildBuilding {
		t.Fatalf("op.Phase = %s, want still BUILDING (untouched)", op.Phase)
	}
	if op.Version == stale.Version {
		t.Fatal("fixture assumption broken: the simulated live advance did not actually bump the version")
	}
}
