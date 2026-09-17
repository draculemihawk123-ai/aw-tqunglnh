package projectionrebuildworker_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/projection"
	"github.com/taQuangLing/agent-workflow/internal/app/projectionrebuildworker"
)

// TestExecuteProjectionRebuild_TwoWorkersRacing_ShadowLeaseFencesSecondBuilder
// is V6-09A's own "two workers racing" Verify bullet, isolating the SHADOW-
// GENERATION LEASE specifically as the deciding fence (independent of the
// job lease, which this scenario deliberately keeps valid for the ONE
// worker under test — see TestExecuteProjectionRebuild_CrashBeforeSwap_...
// in crash_recovery_sqlite_test.go for the JOB-lease-fenced race instead).
// A currently-held, unexpired shadow lease under a DIFFERENT owner blocks
// this worker's own buildRound cleanly (a real, retryable error) — never
// corrupting the operation or double-processing anything.
func TestExecuteProjectionRebuild_TwoWorkersRacing_ShadowLeaseFencesSecondBuilder(t *testing.T) {
	fx := newWorkerFixture(t, "worker-two-racing-shadow-lease.db")
	fx.appendEvent(t, "RootWorkItemCreated", 1, rootWorkItemCreatedPayload)
	fx.applyLive(t, "live-consumer")
	liveCheckpoint := fx.getCheckpoint(t, 1)

	created := fx.requestRebuild(t, "req-1")
	job, _ := fx.claimJob(t, "rebuild-worker", time.Hour)

	fx.simulateSnapshotCommitted(t, created.OperationID, 1, 2, liveCheckpoint.Cursor)

	// An "intruder" — some other process somehow also actively building
	// generation 2 right now — holds a real, unexpired lease under a
	// DIFFERENT owner.
	if err := fx.uow.WithSerializedWrite(fx.ctx, func(tx ports.Tx) error {
		_, err := tx.Projections().AcquireOrRenewConsumerLease(fx.ctx, ports.AcquireOrRenewConsumerLeaseRequest{
			ProjectID: "project-1", ProjectionName: testProjectionName, Generation: 2,
			Owner: "intruder-worker", TTL: time.Hour, Now: time.Now().UTC(),
		})
		return err
	}); err != nil {
		t.Fatalf("simulate intruder lease: %v", err)
	}

	// job's own JobLease is perfectly valid — the shadow lease alone must
	// be what rejects this attempt.
	err := projectionrebuildworker.ExecuteProjectionRebuild(fx.ctx, fx.deps(), job)
	if err == nil {
		t.Fatal("ExecuteProjectionRebuild while an intruder holds the shadow lease = nil error, want a fenced rejection")
	}
	if !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("ExecuteProjectionRebuild error = %v, want it to wrap ports.ErrOptimisticConflict", err)
	}

	// Nothing advanced — no partial/corrupted progress from the rejected
	// attempt.
	op := fx.getOperation(t, created.OperationID)
	if op.Phase != ports.ProjectionRebuildSnapshotting {
		t.Fatalf("op.Phase after rejected attempt = %s, want still SNAPSHOTTING (buildRound never got to advance it)", op.Phase)
	}
	if op.ShadowCursor != nil {
		t.Fatalf("op.ShadowCursor after rejected attempt = %v, want nil (no round ever committed)", op.ShadowCursor)
	}
	if active, ok := fx.getActiveGeneration(t); !ok || active != 1 {
		t.Fatalf("active generation after rejected attempt = (%d, %v), want still (1, true)", active, ok)
	}
	// The job stays claimable-again (this worker's own attempt returned an
	// error, so workerpool would leave it un-completed for retry) — the
	// job row itself was never touched by the rejected buildRound.
	stillLeased := fx.getOperation(t, created.OperationID)
	if stillLeased.JobID != created.JobID {
		t.Fatalf("operation JobID changed: %s, want %s", stillLeased.JobID, created.JobID)
	}
}

// TestExecuteProjectionRebuild_ShadowLeaseExpiry_AllowsTakeoverAfterTTL is
// V6-09A's own "lease expiry" Verify bullet: once an EARLIER holder's own
// shadow-generation lease has genuinely expired (its own TTL elapsed), a
// DIFFERENT, currently-valid worker CAN take it over and continue —
// recovery is never permanently blocked by a holder that is actually gone,
// only ever by one that is still genuinely live.
func TestExecuteProjectionRebuild_ShadowLeaseExpiry_AllowsTakeoverAfterTTL(t *testing.T) {
	fx := newWorkerFixture(t, "worker-shadow-lease-expiry.db")
	fx.appendEvent(t, "RootWorkItemCreated", 1, rootWorkItemCreatedPayload)
	fx.applyLive(t, "live-consumer")
	liveCheckpoint := fx.getCheckpoint(t, 1)

	created := fx.requestRebuild(t, "req-1")
	job, _ := fx.claimJob(t, "rebuild-worker", time.Hour)

	fx.simulateSnapshotCommitted(t, created.OperationID, 1, 2, liveCheckpoint.Cursor)

	// A PAST holder's lease, already expired by the time this test's own
	// real ExecuteProjectionRebuild call runs (Now backdated well past any
	// TTL this fixture uses).
	if err := fx.uow.WithSerializedWrite(fx.ctx, func(tx ports.Tx) error {
		_, err := tx.Projections().AcquireOrRenewConsumerLease(fx.ctx, ports.AcquireOrRenewConsumerLeaseRequest{
			ProjectID: "project-1", ProjectionName: testProjectionName, Generation: 2,
			Owner: "long-gone-worker", TTL: time.Second, Now: time.Now().UTC().Add(-time.Hour),
		})
		return err
	}); err != nil {
		t.Fatalf("simulate expired past holder: %v", err)
	}

	if err := projectionrebuildworker.ExecuteProjectionRebuild(fx.ctx, fx.deps(), job); err != nil {
		t.Fatalf("ExecuteProjectionRebuild after past holder's lease expired: %v", err)
	}
	op := fx.getOperation(t, created.OperationID)
	if op.Phase != ports.ProjectionRebuildSucceeded {
		t.Fatalf("op.Phase = %s, want SUCCEEDED (expired holder must not permanently block recovery)", op.Phase)
	}
	if active, ok := fx.getActiveGeneration(t); !ok || active != 2 {
		t.Fatalf("active generation = (%d, %v), want (2, true)", active, ok)
	}
}

// TestExecuteProjectionRebuild_LiveWritesConcurrentDuringShadowBuild is
// V6-09A's own "live writes happening concurrently" Verify bullet: the
// live consumer keeps advancing the OLD (still active) generation
// normally, entirely unaffected, WHILE this worker concurrently builds and
// cuts over the shadow generation in a real, separate goroutine against
// the SAME real sqlite database.
func TestExecuteProjectionRebuild_LiveWritesConcurrentDuringShadowBuild(t *testing.T) {
	fx := newWorkerFixture(t, "worker-live-writes-concurrent.db")
	fx.appendEvent(t, "RootWorkItemCreated", 1, rootWorkItemCreatedPayload)
	fx.applyLive(t, "live-consumer")
	before := fx.getCheckpoint(t, 1)

	created := fx.requestRebuild(t, "req-1")
	job, _ := fx.claimJob(t, "rebuild-worker", time.Hour)

	var wg sync.WaitGroup
	wg.Add(2)

	var rebuildErr error
	go func() {
		defer wg.Done()
		rebuildErr = projectionrebuildworker.ExecuteProjectionRebuild(fx.ctx, fx.deps(), job)
	}()

	liveErrs := make([]error, 0, 5)
	var liveErrsMu sync.Mutex
	go func() {
		defer wg.Done()
		for i := 0; i < 5; i++ {
			fx.appendEvent(t, "ScopeExpansionRequested", 1, `{"requestId":"live-`+string(rune('a'+i))+`","familyId":"family-1","projectId":"project-1","referencedWorkItemId":"work-item-1","grantCount":1}`)
			_, err := projection.ApplyBatch(fx.ctx, fx.uow, fx.catalog, projection.ApplyBatchRequest{
				ProjectID: "project-1", ProjectionName: testProjectionName, Owner: "live-consumer",
				TTL: 30 * time.Second, BatchSize: 100, Now: time.Now().UTC(), IDs: fx.ids,
			})
			if err != nil {
				liveErrsMu.Lock()
				liveErrs = append(liveErrs, err)
				liveErrsMu.Unlock()
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()

	wg.Wait()

	if rebuildErr != nil {
		t.Fatalf("concurrent ExecuteProjectionRebuild: %v", rebuildErr)
	}
	// A live round that lands in the SHORT window right after cutover —
	// before the rebuild worker's own now-orphaned lease on the (now
	// newly-active) generation naturally expires — gets the documented,
	// retryable ErrOptimisticConflict (see this package's own doc
	// comment, "Design decision: the cutover lease IS the projection
	// consumer lease," on this exact, bounded, self-healing handoff gap).
	// Only some OTHER, undocumented error would be a real bug.
	for _, err := range liveErrs {
		if !errors.Is(err, ports.ErrOptimisticConflict) {
			t.Fatalf("concurrent live ApplyBatch call errored with something other than the documented post-cutover handoff conflict: %v", err)
		}
	}

	op := fx.getOperation(t, created.OperationID)
	if op.Phase != ports.ProjectionRebuildSucceeded {
		t.Fatalf("op.Phase = %s, want SUCCEEDED", op.Phase)
	}

	// Generation 1's own checkpoint kept advancing throughout — the live
	// consumer was never blocked, delayed-into-error, or corrupted by the
	// concurrently-running shadow build.
	after := fx.getCheckpoint(t, 1)
	if after.Cursor <= before.Cursor {
		t.Fatalf("generation 1 checkpoint cursor = %d, want > %d (the live consumer's own concurrent rounds made real progress)", after.Cursor, before.Cursor)
	}
}
