package projectionrebuildworker_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/projection"
	"github.com/taQuangLing/agent-workflow/internal/app/projectionrebuildworker"
)

// This file is V6-09A's own "crash at snapshot/build/replay/before-during-
// after-swap" Verify bullet. Literally killing a process mid-transaction is
// not something a Go test can do to SQLite's own atomic commit — the SAME
// limitation V6-09's own crash-after-intent test already documents ("as
// this task's own brief notes, the proof instead comes from an INJECTED
// failure"). Since every phase transition in this package's own execute.go
// is its OWN separate, atomic, fenced transaction (never spanning two
// phases), "killed right after phase X's own transaction committed" is
// fully and precisely simulated by directly constructing, via the SAME
// PUBLIC ports.Tx accessor methods execute.go itself calls, the EXACT
// durable state that transaction would have left behind — then handing a
// FRESH ExecuteProjectionRebuild call (a new, independent "worker
// restarting after the crash") that state and confirming it resumes
// correctly to completion, with a result byte-identical to an
// uninterrupted run. This is the same "no partial state can exist by
// construction, verify what a crash leaves behind resumes cleanly"
// approach V6-09 itself already used.

// TestExecuteProjectionRebuild_ResumeAfterSnapshotting_ReachesSucceeded
// simulates a crash immediately after SNAPSHOTTING's own transaction
// committed (W0/ShadowGeneration persisted, shadow rows copied, shadow
// checkpoint seeded at W0) but before any BUILDING round ever ran. A fresh
// Execute call must resume in BUILDING (never re-run SNAPSHOTTING, never
// double-copy the rows) and reach SUCCEEDED.
func TestExecuteProjectionRebuild_ResumeAfterSnapshotting_ReachesSucceeded(t *testing.T) {
	fx := newWorkerFixture(t, "worker-resume-after-snapshotting.db")
	fx.appendEvent(t, "RootWorkItemCreated", 1, rootWorkItemCreatedPayload)
	fx.applyLive(t, "live-consumer")
	liveCheckpoint := fx.getCheckpoint(t, 1)

	created := fx.requestRebuild(t, "req-1")
	job, _ := fx.claimJob(t, "rebuild-worker", time.Hour)

	// A live write arrives AFTER the "crashed" worker's own snapshot but
	// BEFORE the resuming worker ever runs — proving BUILDING (not
	// SNAPSHOTTING again) is what picks it up.
	fx.simulateSnapshotCommitted(t, created.OperationID, 1, 2, liveCheckpoint.Cursor)
	fx.appendEvent(t, "WORK_ITEM_MARKED_READY", 1, markedReadyPayload)

	if err := projectionrebuildworker.ExecuteProjectionRebuild(fx.ctx, fx.deps(), job); err != nil {
		t.Fatalf("ExecuteProjectionRebuild (resume after SNAPSHOTTING): %v", err)
	}

	op := fx.getOperation(t, created.OperationID)
	if op.Phase != ports.ProjectionRebuildSucceeded {
		t.Fatalf("op.Phase = %s, want SUCCEEDED (op=%+v)", op.Phase, op)
	}
	if op.ShadowGeneration == nil || *op.ShadowGeneration != 2 {
		t.Fatalf("op.ShadowGeneration = %v, want 2 (the SAME shadow the simulated crash left behind, never re-picked)", op.ShadowGeneration)
	}
	active, ok := fx.getActiveGeneration(t)
	if !ok || active != 2 {
		t.Fatalf("active generation = (%d, %v), want (2, true)", active, ok)
	}
	row := fx.getRow(t, 2, "work-item-1")
	var decoded projection.WorkItemCardRow
	if err := json.Unmarshal([]byte(row.PayloadJSON), &decoded); err != nil {
		t.Fatalf("decode row: %v", err)
	}
	if decoded.Status != "READY" {
		t.Fatalf("row.Status = %s, want READY (the post-snapshot live write WAS picked up by BUILDING)", decoded.Status)
	}
}

// TestExecuteProjectionRebuild_ResumeAfterPartialBuilding_FinishesWithNoDoubleApply
// simulates a crash after ONE BUILDING round committed (ShadowCursor
// advanced partway through a multi-event backlog) — a fresh Execute call
// must resume scanning from EXACTLY that checkpointed cursor (never
// re-applying the already-applied events, never skipping the remaining
// ones) and reach the SAME final state a single, uninterrupted run would.
func TestExecuteProjectionRebuild_ResumeAfterPartialBuilding_FinishesWithNoDoubleApply(t *testing.T) {
	fx := newWorkerFixture(t, "worker-resume-partial-building.db")
	fx.appendEvent(t, "RootWorkItemCreated", 1, rootWorkItemCreatedPayload)
	fx.appendEvent(t, "WORK_ITEM_MARKED_READY", 1, markedReadyPayload)
	fx.appendEvent(t, "ScopeExpansionRequested", 1, `{"requestId":"r-1","familyId":"family-1","projectId":"project-1","referencedWorkItemId":"work-item-1","grantCount":2}`)
	fx.applyLive(t, "live-consumer")
	liveCheckpoint := fx.getCheckpoint(t, 1)

	created := fx.requestRebuild(t, "req-1")
	job, _ := fx.claimJob(t, "rebuild-worker", time.Hour)

	// Simulate: SNAPSHOTTING committed (W0 = liveCheckpoint.Cursor, shadow
	// generation 2 seeded from generation 1's own rows) AND one BUILDING
	// round replayed the FIRST of the three post-rebuild-request events on
	// its own, then "crashed" — the operation is left mid-BUILDING with
	// ShadowCursor short of the journal's real tip. Backdated Now/TTL so
	// the simulated round's own shadow-generation lease has ALREADY
	// expired by the time the REAL resuming worker below tries to
	// acquire it under its own, differently-derived owner string — a
	// faithful stand-in for how much real wall-clock time (heartbeat
	// interval, job LeaseTTL, recovery-reaper interval) elapses before a
	// genuinely crashed worker's own job is ever reclaimed in production.
	fx.simulateSnapshotCommitted(t, created.OperationID, 1, 2, liveCheckpoint.Cursor)
	partialOutcome, err := projection.ReplayGenerationBatch(fx.ctx, fx.uow, fx.catalog, projection.ReplayGenerationBatchRequest{
		ProjectID: "project-1", ProjectionName: testProjectionName, Generation: 2,
		Owner: "crashed-worker-attempt", TTL: 30 * time.Second, BatchSize: 1, Now: time.Now().UTC().Add(-time.Hour), IDs: fx.ids,
	})
	if err != nil {
		t.Fatalf("simulated partial BUILDING round: %v", err)
	}
	if partialOutcome.EventsScanned == 0 {
		t.Fatal("simulated partial BUILDING round scanned 0 events — fixture assumption broken")
	}
	fx.simulateAdvanceOperation(t, created.OperationID, ports.ProjectionRebuildBuilding, nil, nil, &partialOutcome.NewCursor, nil, nil, nil)

	if err := projectionrebuildworker.ExecuteProjectionRebuild(fx.ctx, fx.deps(), job); err != nil {
		t.Fatalf("ExecuteProjectionRebuild (resume mid-BUILDING): %v", err)
	}

	op := fx.getOperation(t, created.OperationID)
	if op.Phase != ports.ProjectionRebuildSucceeded {
		t.Fatalf("op.Phase = %s, want SUCCEEDED", op.Phase)
	}

	// The resumed rebuild's own final row for work-item-1 must match a
	// completely fresh, uninterrupted replay of the identical event set —
	// proving the resume neither skipped nor double-applied anything.
	const freshGeneration = 999
	if _, err := projection.ReplayGenerationBatch(fx.ctx, fx.uow, fx.catalog, projection.ReplayGenerationBatchRequest{
		ProjectID: "project-1", ProjectionName: testProjectionName, Generation: freshGeneration,
		Owner: "fresh-reference-replay", TTL: 30 * time.Second, BatchSize: 100, Now: time.Now().UTC(), IDs: fx.ids,
	}); err != nil {
		t.Fatalf("fresh reference replay: %v", err)
	}
	resumedRow := fx.getRow(t, *op.ShadowGeneration, "work-item-1")
	freshRow := fx.getRow(t, freshGeneration, "work-item-1")
	var resumedDecoded, freshDecoded projection.WorkItemCardRow
	if err := json.Unmarshal([]byte(resumedRow.PayloadJSON), &resumedDecoded); err != nil {
		t.Fatalf("decode resumed row: %v", err)
	}
	if err := json.Unmarshal([]byte(freshRow.PayloadJSON), &freshDecoded); err != nil {
		t.Fatalf("decode fresh row: %v", err)
	}
	resumedHash, err := resumedDecoded.CanonicalRowHash()
	if err != nil {
		t.Fatalf("CanonicalRowHash(resumed): %v", err)
	}
	freshHash, err := freshDecoded.CanonicalRowHash()
	if err != nil {
		t.Fatalf("CanonicalRowHash(fresh): %v", err)
	}
	if resumedHash != freshHash {
		t.Fatalf("resumed-after-partial-crash row hash %s != fresh uninterrupted replay hash %s — resume double-applied or skipped an event", resumedHash, freshHash)
	}
}

// TestExecuteProjectionRebuild_ResumeAfterEnteringCuttingOver_ReachesSucceeded
// simulates a crash after the operation reached CUTTING_OVER (BUILDING had
// already fully caught up) but before the final swap transaction ever ran
// — "before... swap." A fresh Execute call must do its own bounded
// catch-up (finding nothing new, since BUILDING already caught up) and
// complete the swap.
func TestExecuteProjectionRebuild_ResumeAfterEnteringCuttingOver_ReachesSucceeded(t *testing.T) {
	fx := newWorkerFixture(t, "worker-resume-cutting-over.db")
	fx.appendEvent(t, "RootWorkItemCreated", 1, rootWorkItemCreatedPayload)
	fx.applyLive(t, "live-consumer")
	liveCheckpoint := fx.getCheckpoint(t, 1)

	created := fx.requestRebuild(t, "req-1")
	job, _ := fx.claimJob(t, "rebuild-worker", time.Hour)

	fx.simulateSnapshotCommitted(t, created.OperationID, 1, 2, liveCheckpoint.Cursor)
	// BUILDING already fully caught up (no new events past W0) — simulate
	// that round's own commit, then the phase-only advance into
	// CUTTING_OVER (enterCuttingOver's own transaction), exactly what a
	// real worker would have left durable right before "crashing".
	fx.simulateAdvanceOperation(t, created.OperationID, ports.ProjectionRebuildBuilding, nil, nil, nil, nil, nil, nil)
	fx.simulateAdvanceOperation(t, created.OperationID, ports.ProjectionRebuildCuttingOver, nil, nil, nil, nil, nil, nil)

	if err := projectionrebuildworker.ExecuteProjectionRebuild(fx.ctx, fx.deps(), job); err != nil {
		t.Fatalf("ExecuteProjectionRebuild (resume from CUTTING_OVER): %v", err)
	}
	op := fx.getOperation(t, created.OperationID)
	if op.Phase != ports.ProjectionRebuildSucceeded {
		t.Fatalf("op.Phase = %s, want SUCCEEDED", op.Phase)
	}
	active, ok := fx.getActiveGeneration(t)
	if !ok || active != 2 {
		t.Fatalf("active generation = (%d, %v), want (2, true)", active, ok)
	}
}

// TestExecuteProjectionRebuild_CrashBeforeSwap_StaleJobLease_NeverSwapsAndResumeCompletesIt
// is V6-09A's own "before/during-swap" case from the OTHER angle: a worker
// reaches CUTTING_OVER and attempts the final swap transaction, but its OWN
// job lease is already gone (the driving `job` value handed to
// ExecuteProjectionRebuild is stale — simulating "this worker's process was
// about to commit the swap but its lease had already been reclaimed"). The
// cutover transaction's own tx.Jobs().ValidateActiveJob check must reject
// it BEFORE the generation ever swaps — proving "during the swap" a crash
// (or a lease loss at the worst possible moment) can never leave a mixed
// generation, only "before" (nothing committed) — and a SEPARATE, valid
// worker resumes and completes it cleanly afterward.
func TestExecuteProjectionRebuild_CrashBeforeSwap_StaleJobLease_NeverSwapsAndResumeCompletesIt(t *testing.T) {
	fx := newWorkerFixture(t, "worker-crash-before-swap.db")
	fx.appendEvent(t, "RootWorkItemCreated", 1, rootWorkItemCreatedPayload)
	fx.applyLive(t, "live-consumer")
	liveCheckpoint := fx.getCheckpoint(t, 1)

	created := fx.requestRebuild(t, "req-1")
	staleJob, _ := fx.claimJob(t, "worker-about-to-die", 50*time.Millisecond)

	fx.simulateSnapshotCommitted(t, created.OperationID, 1, 2, liveCheckpoint.Cursor)
	fx.simulateAdvanceOperation(t, created.OperationID, ports.ProjectionRebuildBuilding, nil, nil, nil, nil, nil, nil)
	fx.simulateAdvanceOperation(t, created.OperationID, ports.ProjectionRebuildCuttingOver, nil, nil, nil, nil, nil, nil)

	// staleJob's own lease is now expired AND reclaimed by someone else —
	// exactly what "already gone by the time this worker tries to commit
	// the swap" means concretely.
	time.Sleep(100 * time.Millisecond)
	if _, err := fx.store.RecoverExpiredJobs(fx.ctx); err != nil {
		t.Fatalf("RecoverExpiredJobs: %v", err)
	}
	reclaimedJob, _ := fx.claimJob(t, "worker-b-reclaimed-it", time.Hour)
	if reclaimedJob.ID != staleJob.ID {
		t.Fatalf("reclaimedJob.ID = %s, want the SAME job %s (just reclaimed under a new lease)", reclaimedJob.ID, staleJob.ID)
	}

	// The STALE worker's own attempt (still holding the OLD, now-invalid
	// job snapshot) must fail — never swap.
	if err := projectionrebuildworker.ExecuteProjectionRebuild(fx.ctx, fx.deps(), staleJob); err == nil {
		t.Fatal("ExecuteProjectionRebuild with a stale/reclaimed job lease = nil error, want a fenced rejection")
	}
	if active, ok := fx.getActiveGeneration(t); !ok || active != 1 {
		t.Fatalf("active generation after the STALE worker's rejected attempt = (%d, %v), want STILL (1, true) — never swapped", active, ok)
	}
	op := fx.getOperation(t, created.OperationID)
	if op.Phase != ports.ProjectionRebuildCuttingOver {
		t.Fatalf("op.Phase after the stale worker's rejected attempt = %s, want still CUTTING_OVER", op.Phase)
	}

	// The worker that legitimately reclaimed the job completes it cleanly.
	if err := projectionrebuildworker.ExecuteProjectionRebuild(fx.ctx, fx.deps(), reclaimedJob); err != nil {
		t.Fatalf("ExecuteProjectionRebuild (legitimate reclaim): %v", err)
	}
	op = fx.getOperation(t, created.OperationID)
	if op.Phase != ports.ProjectionRebuildSucceeded {
		t.Fatalf("op.Phase after legitimate reclaim = %s, want SUCCEEDED", op.Phase)
	}
	if active, ok := fx.getActiveGeneration(t); !ok || active != 2 {
		t.Fatalf("active generation after legitimate reclaim = (%d, %v), want (2, true)", active, ok)
	}
}

// TestExecuteProjectionRebuild_RedeliveredAfterSucceeded_IsANoOp proves a
// duplicate/redelivered job claim for an ALREADY-terminal operation is a
// safe, idempotent no-op — never a second cutover attempt, never an error.
func TestExecuteProjectionRebuild_RedeliveredAfterSucceeded_IsANoOp(t *testing.T) {
	fx := newWorkerFixture(t, "worker-redelivered-after-succeeded.db")
	fx.appendEvent(t, "RootWorkItemCreated", 1, rootWorkItemCreatedPayload)
	fx.applyLive(t, "live-consumer")

	created := fx.requestRebuild(t, "req-1")
	job, _ := fx.claimJob(t, "rebuild-worker", time.Hour)
	if err := projectionrebuildworker.ExecuteProjectionRebuild(fx.ctx, fx.deps(), job); err != nil {
		t.Fatalf("first ExecuteProjectionRebuild: %v", err)
	}
	activeAfterFirst, _ := fx.getActiveGeneration(t)

	// A redelivered call with the SAME (now-stale, job already completed)
	// snapshot must be a safe no-op — the top-level Phase.IsTerminal()
	// check catches it before touching anything.
	if err := projectionrebuildworker.ExecuteProjectionRebuild(fx.ctx, fx.deps(), job); err != nil {
		t.Fatalf("redelivered ExecuteProjectionRebuild: %v", err)
	}
	op := fx.getOperation(t, created.OperationID)
	if op.Phase != ports.ProjectionRebuildSucceeded {
		t.Fatalf("op.Phase after redelivery = %s, want still SUCCEEDED", op.Phase)
	}
	activeAfterSecond, _ := fx.getActiveGeneration(t)
	if activeAfterSecond != activeAfterFirst {
		t.Fatalf("active generation changed by a redelivered call: %d -> %d", activeAfterFirst, activeAfterSecond)
	}
}

// simulateSnapshotCommitted directly performs, via the SAME public
// ports.Tx accessor methods snapshotStep itself calls (see this file's own
// doc comment), the exact durable writes a real SNAPSHOTTING transaction
// commits: copy oldGeneration's own rows into shadowGeneration, seed
// shadowGeneration's own checkpoint at w0, and advance the operation to
// SNAPSHOTTING with W0/ShadowGeneration set.
func (fx *workerFixture) simulateSnapshotCommitted(t *testing.T, operationID string, oldGeneration, shadowGeneration, w0 uint64) {
	t.Helper()
	now := time.Now().UTC()
	if err := fx.uow.WithSerializedWrite(fx.ctx, func(tx ports.Tx) error {
		rows, err := tx.Projections().ListProjectionRows(fx.ctx, "project-1", testProjectionName, oldGeneration)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := tx.Projections().UpsertProjectionRow(fx.ctx, ports.ProjectionRow{
				ProjectID: "project-1", ProjectionName: testProjectionName, Generation: shadowGeneration,
				EntityKey: row.EntityKey, PayloadJSON: row.PayloadJSON,
				LastAppliedJournalPosition: row.LastAppliedJournalPosition, UpdatedAt: now,
			}); err != nil {
				return err
			}
		}
		if err := tx.Projections().UpsertProjectionCheckpoint(fx.ctx, ports.UpsertProjectionCheckpointRequest{
			ProjectID: "project-1", ProjectionName: testProjectionName, Generation: shadowGeneration,
			ExpectedCursor: nil, NewCursor: w0, NewStatus: ports.ProjectionLive, UpdatedAt: now,
		}); err != nil {
			return err
		}
		op, err := tx.ProjectionRebuilds().GetOperation(fx.ctx, operationID)
		if err != nil {
			return err
		}
		_, err = tx.ProjectionRebuilds().AdvanceOperation(fx.ctx, ports.AdvanceProjectionRebuildOperationRequest{
			ID: operationID, ExpectedVersion: op.Version, NextPhase: ports.ProjectionRebuildSnapshotting,
			NextW0: &w0, NextShadowGeneration: &shadowGeneration, UpdatedAt: now,
		})
		return err
	}); err != nil {
		t.Fatalf("simulateSnapshotCommitted: %v", err)
	}
}

// simulateAdvanceOperation directly performs one fenced AdvanceOperation
// call, exactly mirroring what buildRound/enterCuttingOver/failOperation
// themselves each commit as their own single-purpose transaction.
func (fx *workerFixture) simulateAdvanceOperation(
	t *testing.T, operationID string, nextPhase ports.ProjectionRebuildPhase,
	w0, shadowGeneration, shadowCursor, cutoverCursor *uint64, errorCode, errorMessage *string,
) {
	t.Helper()
	now := time.Now().UTC()
	if err := fx.uow.WithSerializedWrite(fx.ctx, func(tx ports.Tx) error {
		op, err := tx.ProjectionRebuilds().GetOperation(fx.ctx, operationID)
		if err != nil {
			return err
		}
		_, err = tx.ProjectionRebuilds().AdvanceOperation(fx.ctx, ports.AdvanceProjectionRebuildOperationRequest{
			ID: operationID, ExpectedVersion: op.Version, NextPhase: nextPhase,
			NextW0: w0, NextShadowGeneration: shadowGeneration, NextShadowCursor: shadowCursor,
			NextCutoverCursor: cutoverCursor, NextErrorCode: errorCode, NextErrorMessage: errorMessage, UpdatedAt: now,
		})
		return err
	}); err != nil {
		t.Fatalf("simulateAdvanceOperation(%s): %v", nextPhase, err)
	}
}
