package projectionrebuildworker_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/projection"
	"github.com/taQuangLing/agent-workflow/internal/app/projectionrebuildworker"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// TestExecuteProjectionRebuild_HappyPath_SnapshotBuildCutoverSucceeds is
// this task's own end-to-end happy path: REQUESTED -> SNAPSHOTTING ->
// BUILDING -> CUTTING_OVER -> SUCCEEDED, one call, and "restart or race
// always leaves exactly one COMPLETE active generation with an exact
// cursor" (V6-09A's own Hoàn thành khi) holds for the ordinary,
// uninterrupted case.
func TestExecuteProjectionRebuild_HappyPath_SnapshotBuildCutoverSucceeds(t *testing.T) {
	fx := newWorkerFixture(t, "worker-happy-path.db")
	fx.appendEvent(t, "RootWorkItemCreated", 1, rootWorkItemCreatedPayload)
	fx.appendEvent(t, "WORK_ITEM_MARKED_READY", 1, markedReadyPayload)
	liveOutcome := fx.applyLive(t, "live-consumer")
	if liveOutcome.Generation != 1 {
		t.Fatalf("live generation = %d, want 1", liveOutcome.Generation)
	}
	liveCheckpointBeforeRebuild := fx.getCheckpoint(t, 1)

	created := fx.requestRebuild(t, "req-1")
	job, _ := fx.claimJob(t, "rebuild-worker", time.Hour)

	if err := projectionrebuildworker.ExecuteProjectionRebuild(fx.ctx, fx.deps(), job); err != nil {
		t.Fatalf("ExecuteProjectionRebuild: %v", err)
	}

	op := fx.getOperation(t, created.OperationID)
	if op.Phase != ports.ProjectionRebuildSucceeded {
		t.Fatalf("op.Phase = %s, want SUCCEEDED (op=%+v)", op.Phase, op)
	}
	if op.W0 == nil || *op.W0 != liveCheckpointBeforeRebuild.Cursor {
		t.Fatalf("op.W0 = %v, want %d (the live generation's own checkpoint cursor at snapshot time)", op.W0, liveCheckpointBeforeRebuild.Cursor)
	}
	if op.ShadowGeneration == nil || *op.ShadowGeneration != 2 {
		t.Fatalf("op.ShadowGeneration = %v, want 2", op.ShadowGeneration)
	}
	if op.ShadowCursor == nil || op.CutoverCursor == nil || *op.ShadowCursor != *op.CutoverCursor {
		t.Fatalf("op.ShadowCursor/CutoverCursor = %v/%v, want equal (no live writes arrived after this rebuild's own last round)", op.ShadowCursor, op.CutoverCursor)
	}
	if op.ErrorCode != "" || op.ErrorMessage != "" {
		t.Fatalf("op.ErrorCode/ErrorMessage = %q/%q, want both empty on SUCCEEDED", op.ErrorCode, op.ErrorMessage)
	}

	active, ok := fx.getActiveGeneration(t)
	if !ok || active != 2 {
		t.Fatalf("active generation = (%d, %v), want (2, true) — cutover swapped it", active, ok)
	}

	// Generation 1 (the OLD one) is completely untouched — "never clear
	// the active generation" and "never modify authority/events" both hold:
	// its own row still exists, unchanged.
	oldRow := fx.getRow(t, 1, "work-item-1")
	newRow := fx.getRow(t, 2, "work-item-1")
	if oldRow.PayloadJSON != newRow.PayloadJSON {
		t.Fatalf("old generation row %q != new generation row %q, want byte-identical (same events, same reducers)", oldRow.PayloadJSON, newRow.PayloadJSON)
	}

	// The job this operation's own RequestProjectionRebuild enqueued was
	// completed inside the SAME transaction as the terminal SUCCEEDED
	// transition — re-claiming it must fail (nothing AVAILABLE).
	if _, _, err := fx.store.ClaimJob(fx.ctx, "someone-else", time.Second); !errors.Is(err, ports.ErrNoJobAvailable) {
		t.Fatalf("ClaimJob after completion: err = %v, want ErrNoJobAvailable", err)
	}
}

// TestExecuteProjectionRebuild_BootstrapNoActiveGenerationYet proves
// CutoverProjectionGeneration's own "no active generation exists yet"
// bootstrap path (nil ExpectedGeneration) works correctly end to end: a
// rebuild requested before the live consumer ever lazily created
// generation 1 still succeeds, W0=0, and the FIRST generation this
// projection ever has is the one this rebuild itself built.
func TestExecuteProjectionRebuild_BootstrapNoActiveGenerationYet(t *testing.T) {
	fx := newWorkerFixture(t, "worker-bootstrap.db")
	fx.appendEvent(t, "RootWorkItemCreated", 1, rootWorkItemCreatedPayload)

	if _, ok := fx.getActiveGeneration(t); ok {
		t.Fatal("active generation already exists before this test's own rebuild — fixture assumption broken")
	}

	created := fx.requestRebuild(t, "req-1")
	job, _ := fx.claimJob(t, "rebuild-worker", time.Hour)
	if err := projectionrebuildworker.ExecuteProjectionRebuild(fx.ctx, fx.deps(), job); err != nil {
		t.Fatalf("ExecuteProjectionRebuild: %v", err)
	}

	op := fx.getOperation(t, created.OperationID)
	if op.Phase != ports.ProjectionRebuildSucceeded {
		t.Fatalf("op.Phase = %s, want SUCCEEDED", op.Phase)
	}
	if op.ShadowGeneration == nil || *op.ShadowGeneration != 1 {
		t.Fatalf("op.ShadowGeneration = %v, want 1 (bootstrap: no prior generation to number past)", op.ShadowGeneration)
	}
	active, ok := fx.getActiveGeneration(t)
	if !ok || active != 1 {
		t.Fatalf("active generation = (%d, %v), want (1, true)", active, ok)
	}
	row := fx.getRow(t, 1, "work-item-1")
	var decoded projection.WorkItemCardRow
	if err := json.Unmarshal([]byte(row.PayloadJSON), &decoded); err != nil {
		t.Fatalf("decode row: %v", err)
	}
	if decoded.Status != "BACKLOG" {
		t.Fatalf("row.Status = %s, want BACKLOG", decoded.Status)
	}
}

// TestExecuteProjectionRebuild_Poison_DuringBuild_FailsSafelyAndLeavesOldGenerationActive
// is V6-09A's own "poison" Verify bullet: a poison event encountered while
// BUILDING the shadow marks the operation FAILED with a safe ErrorCode/
// ErrorMessage — never a raw internal error — and the OLD (still active)
// generation is left completely untouched, since this worker never had any
// reason to write to it.
func TestExecuteProjectionRebuild_Poison_DuringBuild_FailsSafelyAndLeavesOldGenerationActive(t *testing.T) {
	fx := newWorkerFixture(t, "worker-poison.db")
	fx.appendEvent(t, "RootWorkItemCreated", 1, rootWorkItemCreatedPayload)
	fx.applyLive(t, "live-consumer")
	beforeRow := fx.getRow(t, 1, "work-item-1")

	created := fx.requestRebuild(t, "req-1")
	// A poison event: unregistered/unclassified — BUILDING's own
	// ReplayGenerationBatch round will hit this deterministically.
	fx.appendEvent(t, "SOME_EVENT_NO_CATALOG_ENTRY_EXISTS", 1, `{}`)

	job, _ := fx.claimJob(t, "rebuild-worker", time.Hour)
	if err := projectionrebuildworker.ExecuteProjectionRebuild(fx.ctx, fx.deps(), job); err != nil {
		t.Fatalf("ExecuteProjectionRebuild (poison): %v", err)
	}

	op := fx.getOperation(t, created.OperationID)
	if op.Phase != ports.ProjectionRebuildFailed {
		t.Fatalf("op.Phase = %s, want FAILED", op.Phase)
	}
	if op.ErrorCode != "POISON_EVENT" {
		t.Fatalf("op.ErrorCode = %q, want POISON_EVENT", op.ErrorCode)
	}
	if op.ErrorMessage == "" {
		t.Fatal("op.ErrorMessage is empty, want a safe diagnostic summary")
	}

	active, ok := fx.getActiveGeneration(t)
	if !ok || active != 1 {
		t.Fatalf("active generation after a FAILED rebuild = (%d, %v), want (1, true) — untouched", active, ok)
	}
	afterRow := fx.getRow(t, 1, "work-item-1")
	if beforeRow.PayloadJSON != afterRow.PayloadJSON {
		t.Fatalf("generation 1's own row changed by a FAILED rebuild: before=%q after=%q", beforeRow.PayloadJSON, afterRow.PayloadJSON)
	}

	// The job is completed (FAILED is terminal, same "complete the job in
	// the same fenced transaction" discipline as SUCCEEDED) — never left
	// stuck for a pointless retry loop.
	if _, _, err := fx.store.ClaimJob(fx.ctx, "someone-else", time.Second); !errors.Is(err, ports.ErrNoJobAvailable) {
		t.Fatalf("ClaimJob after FAILED completion: err = %v, want ErrNoJobAvailable", err)
	}
}

// TestExecuteProjectionRebuild_CanonicalOldNewDiff_MatchesFreshFullReplay
// is V6-09A's own "canonical old/new diff and no mixed generation" Verify
// bullet: after cutover, the NEW (now-active) generation's own row hash
// matches EXACTLY what an entirely independent, from-scratch full replay
// of the SAME events produces — proving no drift between the snapshot-
// copy+catch-up path this worker takes and a plain from-position-0 replay.
func TestExecuteProjectionRebuild_CanonicalOldNewDiff_MatchesFreshFullReplay(t *testing.T) {
	fx := newWorkerFixture(t, "worker-canonical-diff.db")
	fx.appendEvent(t, "RootWorkItemCreated", 1, rootWorkItemCreatedPayload)
	fx.appendEvent(t, "WORK_ITEM_MARKED_READY", 1, markedReadyPayload)
	fx.appendEvent(t, "ScopeExpansionRequested", 1, `{"requestId":"r-1","familyId":"family-1","projectId":"project-1","referencedWorkItemId":"work-item-1","grantCount":2}`)
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

	// An entirely independent, from-scratch full replay into a THIRD
	// generation number this worker never touched — the "what a from-
	// scratch full replay of the same events would produce" reference.
	const freshGeneration = 999
	freshOutcome, err := projection.ReplayGenerationBatch(fx.ctx, fx.uow, fx.catalog, projection.ReplayGenerationBatchRequest{
		ProjectID: "project-1", ProjectionName: testProjectionName, Generation: freshGeneration,
		Owner: "fresh-replay", TTL: 30 * time.Second, BatchSize: 100, Now: time.Now().UTC(), IDs: fx.ids,
	})
	if err != nil {
		t.Fatalf("fresh ReplayGenerationBatch: %v", err)
	}
	if freshOutcome.Poisoned {
		t.Fatalf("fresh replay poisoned: %s", freshOutcome.PoisonReason)
	}

	activeRow := fx.getRow(t, activeGeneration, "work-item-1")
	freshRow := fx.getRow(t, freshGeneration, "work-item-1")

	var activeDecoded, freshDecoded projection.WorkItemCardRow
	if err := json.Unmarshal([]byte(activeRow.PayloadJSON), &activeDecoded); err != nil {
		t.Fatalf("decode active row: %v", err)
	}
	if err := json.Unmarshal([]byte(freshRow.PayloadJSON), &freshDecoded); err != nil {
		t.Fatalf("decode fresh row: %v", err)
	}
	activeHash, err := activeDecoded.CanonicalRowHash()
	if err != nil {
		t.Fatalf("CanonicalRowHash(active): %v", err)
	}
	freshHash, err := freshDecoded.CanonicalRowHash()
	if err != nil {
		t.Fatalf("CanonicalRowHash(fresh): %v", err)
	}
	if activeHash != freshHash {
		t.Fatalf("active generation's own canonical row hash %s != fresh from-scratch replay's own hash %s — drift between the snapshot-copy+catch-up rebuild path and a plain full replay", activeHash, freshHash)
	}
}

// TestExecuteProjectionRebuild_ReaderPagingAcrossCutover_TypedResync is
// V6-09A's own "reader paging" Verify bullet: a cursor minted while
// generation 1 was active gets httpapi's own typed
// ResyncReasonGenerationChanged once cutover has moved the active
// generation to 2 — proving the EXISTING httpapi.Bind mechanism (V6-02A,
// already keyed on CursorState.Generation) needs no new code from this
// task to correctly reject a stale-generation cursor; only a real cutover
// to exercise it.
func TestExecuteProjectionRebuild_ReaderPagingAcrossCutover_TypedResync(t *testing.T) {
	fx := newWorkerFixture(t, "worker-reader-paging.db")
	fx.appendEvent(t, "RootWorkItemCreated", 1, rootWorkItemCreatedPayload)
	fx.applyLive(t, "live-consumer")

	activeBefore, ok := fx.getActiveGeneration(t)
	if !ok {
		t.Fatal("no active generation before rebuild")
	}
	staleCursor := httpapi.CursorState{
		ProjectID: "project-1", QueryFingerprint: "fp-1", Generation: int(activeBefore),
		UpperWatermark: 1, LastKey: "work-item-1",
	}

	created := fx.requestRebuild(t, "req-1")
	job, _ := fx.claimJob(t, "rebuild-worker", time.Hour)
	if err := projectionrebuildworker.ExecuteProjectionRebuild(fx.ctx, fx.deps(), job); err != nil {
		t.Fatalf("ExecuteProjectionRebuild: %v", err)
	}
	op := fx.getOperation(t, created.OperationID)
	if op.Phase != ports.ProjectionRebuildSucceeded {
		t.Fatalf("op.Phase = %s, want SUCCEEDED", op.Phase)
	}

	activeAfter, ok := fx.getActiveGeneration(t)
	if !ok || activeAfter == activeBefore {
		t.Fatalf("active generation after cutover = (%d, %v), want a DIFFERENT generation than before (%d)", activeAfter, ok, activeBefore)
	}

	currentRequest := httpapi.CursorState{
		ProjectID: "project-1", QueryFingerprint: "fp-1", Generation: int(activeAfter),
	}
	err := httpapi.Bind(staleCursor, currentRequest)
	var resyncErr *httpapi.ResyncError
	if !errors.As(err, &resyncErr) {
		t.Fatalf("Bind(stale generation cursor) = %v, want a *httpapi.ResyncError", err)
	}
	if resyncErr.Reason != httpapi.ResyncReasonGenerationChanged {
		t.Fatalf("resyncErr.Reason = %s, want %s", resyncErr.Reason, httpapi.ResyncReasonGenerationChanged)
	}

	// A cursor minted AFTER cutover, naming the new generation, binds fine.
	freshCursor := httpapi.CursorState{ProjectID: "project-1", QueryFingerprint: "fp-1", Generation: int(activeAfter)}
	if err := httpapi.Bind(freshCursor, currentRequest); err != nil {
		t.Fatalf("Bind(fresh post-cutover cursor) = %v, want nil", err)
	}
}
