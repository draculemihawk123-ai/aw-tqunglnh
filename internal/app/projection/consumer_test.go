package projection

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
)

func appendTestEvent(t *testing.T, uow ports.UnitOfWork, projectID, eventType string, schemaVersion int, payloadJSON string) {
	t.Helper()
	if err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		return tx.Events().Append(context.Background(), ports.DomainEvent{
			ID: idsource.Random{}.NewID(), ProjectID: projectID, AggregateType: "WorkItem", AggregateID: idsource.Random{}.NewID(),
			Sequence: 1, EventType: eventType, SchemaVersion: schemaVersion, PayloadJSON: payloadJSON,
			CorrelationID: idsource.Random{}.NewID(), CreatedAt: time.Now().UTC(),
		})
	}); err != nil {
		t.Fatalf("append test event %s: %v", eventType, err)
	}
}

func getRow(t *testing.T, uow ports.UnitOfWork, projectID string, generation uint64, entityKey string) WorkItemCardRow {
	t.Helper()
	var row WorkItemCardRow
	if err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		stored, err := tx.Projections().GetProjectionRow(context.Background(), projectID, ProjectionName, generation, entityKey)
		if err != nil {
			return err
		}
		return json.Unmarshal([]byte(stored.PayloadJSON), &row)
	}); err != nil {
		t.Fatalf("get row %s: %v", entityKey, err)
	}
	return row
}

func TestApplyBatch_AppliesRootCreationAndAdvancesCursorAtomically(t *testing.T) {
	uow := fake.New()
	appendTestEvent(t, uow, "project-1", "RootWorkItemCreated", 1,
		`{"workItemId":"wi-1","projectId":"project-1","familyId":"f-1","workspaceSetId":"ws-1","title":"Root"}`)

	catalog := NewCatalog()
	outcome, err := ApplyBatch(context.Background(), uow, catalog, ApplyBatchRequest{
		ProjectID: "project-1", ProjectionName: ProjectionName, Owner: "consumer-a",
		TTL: 30 * time.Second, BatchSize: 100, Now: time.Now().UTC(), IDs: idsource.Random{},
	})
	if err != nil {
		t.Fatalf("ApplyBatch: %v", err)
	}
	if outcome.EventsScanned != 1 || outcome.RowsApplied != 1 || outcome.Poisoned {
		t.Fatalf("outcome = %+v, want EventsScanned=1 RowsApplied=1 Poisoned=false", outcome)
	}
	if outcome.NewCursor != 1 {
		t.Fatalf("outcome.NewCursor = %d, want 1", outcome.NewCursor)
	}

	row := getRow(t, uow, "project-1", outcome.Generation, "wi-1")
	if row.Status != statusBacklog || row.Title != "Root" || row.IsRoot != true {
		t.Fatalf("row = %+v, want Status=BACKLOG Title=Root IsRoot=true", row)
	}
}

func TestApplyBatch_ReplayIsANoOp_CursorAlreadyPastAppliedEvents(t *testing.T) {
	uow := fake.New()
	appendTestEvent(t, uow, "project-1", "RootWorkItemCreated", 1,
		`{"workItemId":"wi-1","projectId":"project-1","familyId":"f-1","workspaceSetId":"ws-1","title":"Root"}`)

	catalog := NewCatalog()
	req := ApplyBatchRequest{
		ProjectID: "project-1", ProjectionName: ProjectionName, Owner: "consumer-a",
		TTL: 30 * time.Second, BatchSize: 100, Now: time.Now().UTC(), IDs: idsource.Random{},
	}
	first, err := ApplyBatch(context.Background(), uow, catalog, req)
	if err != nil {
		t.Fatalf("first ApplyBatch: %v", err)
	}

	// A second round (same or a fresh Now) sees zero new events — the
	// cursor already covers them. This is what makes "duplicate
	// position<=cursor no-op" true by construction: ScanJournal(cursor,
	// ...) simply never returns an already-applied position again.
	second, err := ApplyBatch(context.Background(), uow, catalog, req)
	if err != nil {
		t.Fatalf("second ApplyBatch (replay): %v", err)
	}
	if second.EventsScanned != 0 || second.RowsApplied != 0 {
		t.Fatalf("replay outcome = %+v, want EventsScanned=0 RowsApplied=0", second)
	}
	if second.NewCursor != first.NewCursor {
		t.Fatalf("replay NewCursor = %d, want unchanged %d", second.NewCursor, first.NewCursor)
	}

	// The row itself is unchanged too — replaying never double-applies.
	row := getRow(t, uow, "project-1", first.Generation, "wi-1")
	if row.Status != statusBacklog {
		t.Fatalf("row.Status = %q after replay, want unchanged BACKLOG", row.Status)
	}
}

func TestApplyBatch_TwoConsumers_SecondConflictsWhileFirstHoldsLiveLease(t *testing.T) {
	uow := fake.New()
	appendTestEvent(t, uow, "project-1", "RootWorkItemCreated", 1,
		`{"workItemId":"wi-1","projectId":"project-1","familyId":"f-1","workspaceSetId":"ws-1","title":"Root"}`)

	catalog := NewCatalog()
	now := time.Now().UTC()
	_, err := ApplyBatch(context.Background(), uow, catalog, ApplyBatchRequest{
		ProjectID: "project-1", ProjectionName: ProjectionName, Owner: "consumer-a",
		TTL: 30 * time.Second, BatchSize: 100, Now: now, IDs: idsource.Random{},
	})
	if err != nil {
		t.Fatalf("consumer-a ApplyBatch: %v", err)
	}

	// consumer-b tries a round 5s later — well inside consumer-a's own 30s
	// lease — must conflict, never silently proceed alongside consumer-a.
	_, err = ApplyBatch(context.Background(), uow, catalog, ApplyBatchRequest{
		ProjectID: "project-1", ProjectionName: ProjectionName, Owner: "consumer-b",
		TTL: 30 * time.Second, BatchSize: 100, Now: now.Add(5 * time.Second), IDs: idsource.Random{},
	})
	if !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("consumer-b ApplyBatch while consumer-a holds a live lease: err = %v, want ErrOptimisticConflict", err)
	}
}

func TestApplyBatch_UnclassifiedEvent_RollsBackRowsAndCursorAndDegradesAtLastGoodCursor(t *testing.T) {
	uow := fake.New()
	// First a real, classified event that SHOULD apply successfully.
	appendTestEvent(t, uow, "project-1", "RootWorkItemCreated", 1,
		`{"workItemId":"wi-1","projectId":"project-1","familyId":"f-1","workspaceSetId":"ws-1","title":"Root"}`)
	// Then an event this Catalog has never heard of — the "relevant
	// unknown schema" gap.
	appendTestEvent(t, uow, "project-1", "SOME_EVENT_NO_CATALOG_ENTRY_EXISTS_FOR", 1, `{}`)
	// And a THIRD real event that, if the batch were NOT atomic, could
	// wrongly slip through after the poison.
	appendTestEvent(t, uow, "project-1", "WORK_ITEM_MARKED_READY", 1,
		`{"workItemId":"wi-1","projectId":"project-1","familyId":"f-1","markedBy":"operator"}`)

	catalog := NewCatalog()
	now := time.Now().UTC()
	outcome, err := ApplyBatch(context.Background(), uow, catalog, ApplyBatchRequest{
		ProjectID: "project-1", ProjectionName: ProjectionName, Owner: "consumer-a",
		TTL: 30 * time.Second, BatchSize: 100, Now: now, IDs: idsource.Random{},
	})
	if err != nil {
		t.Fatalf("ApplyBatch: %v", err)
	}
	if !outcome.Poisoned {
		t.Fatalf("outcome.Poisoned = false, want true (an unclassified event was in the batch)")
	}
	if outcome.RowsApplied != 0 {
		t.Fatalf("outcome.RowsApplied = %d, want 0 — the FIRST event's own row upsert (RootWorkItemCreated, which by itself would have succeeded) must roll back together with the whole poisoned batch, never partially commit", outcome.RowsApplied)
	}
	if outcome.NewCursor != 0 {
		t.Fatalf("outcome.NewCursor = %d, want 0 (last-good cursor — nothing in THIS poisoned batch ever advanced it)", outcome.NewCursor)
	}

	// The row must not exist at all — RootWorkItemCreated's own apply was
	// rolled back along with everything else in the same transaction.
	err = uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Projections().GetProjectionRow(context.Background(), "project-1", ProjectionName, outcome.Generation, "wi-1")
		return err
	})
	if !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("GetProjectionRow after a poisoned batch: err = %v, want ErrPersistenceNotFound (no partial row survives)", err)
	}

	// The checkpoint itself is DEGRADED — a poison event never silently
	// vanishes.
	var checkpoint ports.ProjectionCheckpoint
	if err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		checkpoint, err = tx.Projections().GetProjectionCheckpoint(context.Background(), "project-1", ProjectionName, outcome.Generation)
		return err
	}); err != nil {
		t.Fatalf("GetProjectionCheckpoint: %v", err)
	}
	if checkpoint.Status != ports.ProjectionDegraded {
		t.Fatalf("checkpoint.Status = %q, want DEGRADED", checkpoint.Status)
	}
	if checkpoint.Cursor != 0 {
		t.Fatalf("checkpoint.Cursor = %d, want 0 (frozen at last-good — never advanced past the poison event)", checkpoint.Cursor)
	}

	var poisonRecords []ports.ProjectionPoisonRecord
	if err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		poisonRecords, err = tx.Projections().ListProjectionPoison(context.Background(), "project-1", ProjectionName, outcome.Generation)
		return err
	}); err != nil {
		t.Fatalf("ListProjectionPoison: %v", err)
	}
	if len(poisonRecords) != 1 || poisonRecords[0].EventType != "SOME_EVENT_NO_CATALOG_ENTRY_EXISTS_FOR" {
		t.Fatalf("poisonRecords = %+v, want exactly 1 record naming the unclassified event", poisonRecords)
	}
}

func TestApplyBatch_DegradedGenerationStaysFrozen_LeaseStillRenewsAsHeartbeat(t *testing.T) {
	uow := fake.New()
	appendTestEvent(t, uow, "project-1", "SOME_EVENT_NO_CATALOG_ENTRY_EXISTS_FOR", 1, `{}`)

	catalog := NewCatalog()
	now := time.Now().UTC()
	first, err := ApplyBatch(context.Background(), uow, catalog, ApplyBatchRequest{
		ProjectID: "project-1", ProjectionName: ProjectionName, Owner: "consumer-a",
		TTL: 30 * time.Second, BatchSize: 100, Now: now, IDs: idsource.Random{},
	})
	if err != nil || !first.Poisoned {
		t.Fatalf("first ApplyBatch: outcome=%+v err=%v, want Poisoned=true nil error", first, err)
	}

	// A later round (the self-rescheduling loop's own next tick) must
	// never re-attempt the poisoned event — it just renews its lease
	// (LeaseAcquired=true, a real heartbeat) and does nothing else.
	second, err := ApplyBatch(context.Background(), uow, catalog, ApplyBatchRequest{
		ProjectID: "project-1", ProjectionName: ProjectionName, Owner: "consumer-a",
		TTL: 30 * time.Second, BatchSize: 100, Now: now.Add(10 * time.Second), IDs: idsource.Random{},
	})
	if err != nil {
		t.Fatalf("second ApplyBatch on a DEGRADED generation: %v", err)
	}
	if !second.LeaseAcquired || second.EventsScanned != 0 || second.RowsApplied != 0 || second.Poisoned {
		t.Fatalf("second outcome = %+v, want LeaseAcquired=true EventsScanned=0 RowsApplied=0 Poisoned=false (frozen, heartbeat only)", second)
	}
}

func TestApplyBatch_FullBatchMarksStale(t *testing.T) {
	uow := fake.New()
	appendTestEvent(t, uow, "project-1", "RootWorkItemCreated", 1,
		`{"workItemId":"wi-1","projectId":"project-1","familyId":"f-1","workspaceSetId":"ws-1","title":"Root"}`)
	appendTestEvent(t, uow, "project-1", "WORK_ITEM_MARKED_READY", 1,
		`{"workItemId":"wi-1","projectId":"project-1","familyId":"f-1","markedBy":"operator"}`)

	catalog := NewCatalog()
	// BatchSize=1 forces the very first round's own batch to come back
	// completely full (1 event returned out of the 2 actually pending) —
	// the STALE signal.
	outcome, err := ApplyBatch(context.Background(), uow, catalog, ApplyBatchRequest{
		ProjectID: "project-1", ProjectionName: ProjectionName, Owner: "consumer-a",
		TTL: 30 * time.Second, BatchSize: 1, Now: time.Now().UTC(), IDs: idsource.Random{},
	})
	if err != nil {
		t.Fatalf("ApplyBatch: %v", err)
	}
	var checkpoint ports.ProjectionCheckpoint
	if err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		checkpoint, err = tx.Projections().GetProjectionCheckpoint(context.Background(), "project-1", ProjectionName, outcome.Generation)
		return err
	}); err != nil {
		t.Fatalf("GetProjectionCheckpoint: %v", err)
	}
	if checkpoint.Status != ports.ProjectionStale {
		t.Fatalf("checkpoint.Status = %q, want STALE (batch came back completely full, more work is waiting)", checkpoint.Status)
	}
}

func TestApplyBatch_ForeignProjectEventAdvancesCursorWithoutRowChanges(t *testing.T) {
	uow := fake.New()
	appendTestEvent(t, uow, "project-2", "RootWorkItemCreated", 1,
		`{"workItemId":"wi-foreign","projectId":"project-2","familyId":"f-1","workspaceSetId":"ws-1","title":"Foreign"}`)
	appendTestEvent(t, uow, "project-1", "RootWorkItemCreated", 1,
		`{"workItemId":"wi-1","projectId":"project-1","familyId":"f-1","workspaceSetId":"ws-1","title":"Root"}`)

	catalog := NewCatalog()
	outcome, err := ApplyBatch(context.Background(), uow, catalog, ApplyBatchRequest{
		ProjectID: "project-1", ProjectionName: ProjectionName, Owner: "consumer-a",
		TTL: 30 * time.Second, BatchSize: 100, Now: time.Now().UTC(), IDs: idsource.Random{},
	})
	if err != nil {
		t.Fatalf("ApplyBatch: %v", err)
	}
	if outcome.EventsScanned != 2 || outcome.RowsApplied != 1 {
		t.Fatalf("outcome = %+v, want EventsScanned=2 (both project-1 and project-2's own events) RowsApplied=1 (only project-1's own)", outcome)
	}
	if outcome.NewCursor != 2 {
		t.Fatalf("outcome.NewCursor = %d, want 2 (the foreign project-2 event still advances the cursor)", outcome.NewCursor)
	}

	err = uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Projections().GetProjectionRow(context.Background(), "project-1", ProjectionName, outcome.Generation, "wi-foreign")
		return err
	})
	if !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("GetProjectionRow for a foreign-project entity under project-1's own scope: err = %v, want ErrPersistenceNotFound", err)
	}
}

func TestApplyBatch_FallbackMatch_ScopeExpansionRejectedResolvesToFamilyRoot(t *testing.T) {
	uow := fake.New()
	appendTestEvent(t, uow, "project-1", "RootWorkItemCreated", 1,
		`{"workItemId":"wi-1","projectId":"project-1","familyId":"f-1","workspaceSetId":"ws-1","title":"Root"}`)
	appendTestEvent(t, uow, "project-1", "ScopeExpansionRequested", 1,
		`{"requestId":"r-1","familyId":"f-1","projectId":"project-1","grantCount":1}`)
	appendTestEvent(t, uow, "project-1", "ScopeExpansionRejected", 1,
		`{"requestId":"r-1","familyId":"f-1","decisionNote":"no","rejectedBy":"operator"}`)

	catalog := NewCatalog()
	outcome, err := ApplyBatch(context.Background(), uow, catalog, ApplyBatchRequest{
		ProjectID: "project-1", ProjectionName: ProjectionName, Owner: "consumer-a",
		TTL: 30 * time.Second, BatchSize: 100, Now: time.Now().UTC(), IDs: idsource.Random{},
	})
	if err != nil {
		t.Fatalf("ApplyBatch: %v", err)
	}
	if outcome.Poisoned {
		t.Fatalf("outcome.Poisoned = true (%s), want false — FallbackMatch should have resolved both ScopeExpansion events to the family root row", outcome.PoisonReason)
	}
	row := getRow(t, uow, "project-1", outcome.Generation, "wi-1")
	if row.PendingScopeExpansionCount != 0 {
		t.Fatalf("row.PendingScopeExpansionCount = %d, want 0 (Requested then Rejected, resolved via FallbackMatch to the same family-root row both times)", row.PendingScopeExpansionCount)
	}
}

func TestApplyBatch_FallbackMatch_WorkflowRunFinalizedResolvesByActiveRunID(t *testing.T) {
	uow := fake.New()
	appendTestEvent(t, uow, "project-1", "RootWorkItemCreated", 1,
		`{"workItemId":"wi-1","projectId":"project-1","familyId":"f-1","workspaceSetId":"ws-1","title":"Root"}`)
	appendTestEvent(t, uow, "project-1", "WorkflowRunStarted", 1,
		`{"runId":"run-1","workItemId":"wi-1","nodeRunId":"nr-1","nodeKey":"start"}`)
	appendTestEvent(t, uow, "project-1", "WORKFLOW_RUN_FINALIZED", 1,
		`{"jobId":"job-1","jobLeaseOwner":"owner-1","runId":"run-1","terminalState":"COMPLETED"}`)

	catalog := NewCatalog()
	outcome, err := ApplyBatch(context.Background(), uow, catalog, ApplyBatchRequest{
		ProjectID: "project-1", ProjectionName: ProjectionName, Owner: "consumer-a",
		TTL: 30 * time.Second, BatchSize: 100, Now: time.Now().UTC(), IDs: idsource.Random{},
	})
	if err != nil {
		t.Fatalf("ApplyBatch: %v", err)
	}
	if outcome.Poisoned {
		t.Fatalf("outcome.Poisoned = true (%s), want false", outcome.PoisonReason)
	}
	row := getRow(t, uow, "project-1", outcome.Generation, "wi-1")
	if row.Status != statusDone {
		t.Fatalf("row.Status = %q, want DONE (WORKFLOW_RUN_FINALIZED resolved via FallbackMatch by ActiveRunID)", row.Status)
	}
}
