package projection_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/projection"
)

// TestApplyBatch_RealSQLite_AppliesReplaysAndPoisonsCorrectly is the real,
// non-fake-backed counterpart to consumer_test.go's own fake-UnitOfWork
// suite — proving the actual SQL this task wrote (the write_leases-style
// acquire-or-steal-if-expired UPSERT, the fence-token-guarded checkpoint
// CAS, the ON CONFLICT DO UPDATE row upsert) behaves exactly like the
// fake's own Go-level simulation of it against a real SQLite database, not
// just against a mental model of what that SQL should do.
func TestApplyBatch_RealSQLite_AppliesReplaysAndPoisonsCorrectly(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "agentkit-projection-consumer.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()
	uow := sqlite.NewUnitOfWork(store)

	if err := sqlite.SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}

	appendEvent := func(eventType string, schemaVersion int, payloadJSON string) {
		t.Helper()
		if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
			return tx.Events().Append(ctx, ports.DomainEvent{
				ID: idsource.Random{}.NewID(), ProjectID: "project-1", AggregateType: "WorkItem",
				AggregateID: idsource.Random{}.NewID(), Sequence: 1, EventType: eventType, SchemaVersion: schemaVersion,
				PayloadJSON: payloadJSON, CorrelationID: idsource.Random{}.NewID(), CreatedAt: time.Now().UTC(),
			})
		}); err != nil {
			t.Fatalf("append %s: %v", eventType, err)
		}
	}

	appendEvent("RootWorkItemCreated", 1, `{"workItemId":"work-item-1","projectId":"project-1","familyId":"family-1","workspaceSetId":"ws-1","title":"Root"}`)
	appendEvent("WORK_ITEM_MARKED_READY", 1, `{"workItemId":"work-item-1","projectId":"project-1","familyId":"family-1","markedBy":"operator"}`)

	catalog := projection.NewCatalog()
	now := time.Now().UTC()
	outcome, err := projection.ApplyBatch(ctx, uow, catalog, projection.ApplyBatchRequest{
		ProjectID: "project-1", ProjectionName: projection.ProjectionName, Owner: "consumer-a",
		TTL: 30 * time.Second, BatchSize: 100, Now: now, IDs: idsource.Random{},
	})
	if err != nil {
		t.Fatalf("ApplyBatch: %v", err)
	}
	if outcome.EventsScanned != 2 || outcome.RowsApplied != 2 || outcome.Poisoned {
		t.Fatalf("outcome = %+v, want EventsScanned=2 RowsApplied=2 Poisoned=false", outcome)
	}

	var row ports.ProjectionRow
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		row, err = tx.Projections().GetProjectionRow(ctx, "project-1", projection.ProjectionName, outcome.Generation, "work-item-1")
		return err
	}); err != nil {
		t.Fatalf("GetProjectionRow: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(row.PayloadJSON), &decoded); err != nil {
		t.Fatalf("decode row payload: %v", err)
	}
	if decoded["status"] != "READY" {
		t.Fatalf("row status = %v, want READY (both events applied in order: BACKLOG then READY)", decoded["status"])
	}

	// Replay: a second round against the real database sees zero new
	// events and leaves the row untouched.
	replay, err := projection.ApplyBatch(ctx, uow, catalog, projection.ApplyBatchRequest{
		ProjectID: "project-1", ProjectionName: projection.ProjectionName, Owner: "consumer-a",
		TTL: 30 * time.Second, BatchSize: 100, Now: now.Add(time.Second), IDs: idsource.Random{},
	})
	if err != nil {
		t.Fatalf("replay ApplyBatch: %v", err)
	}
	if replay.EventsScanned != 0 || replay.RowsApplied != 0 {
		t.Fatalf("replay outcome = %+v, want EventsScanned=0 RowsApplied=0", replay)
	}

	// A poisoned event, against the real database, really does roll back
	// (no partial row survives) and really does record a poison row.
	appendEvent("SOME_EVENT_NO_CATALOG_ENTRY_EXISTS_FOR_REAL_SQLITE_TEST", 1, `{}`)
	poisonedOutcome, err := projection.ApplyBatch(ctx, uow, catalog, projection.ApplyBatchRequest{
		ProjectID: "project-1", ProjectionName: projection.ProjectionName, Owner: "consumer-a",
		TTL: 30 * time.Second, BatchSize: 100, Now: now.Add(2 * time.Second), IDs: idsource.Random{},
	})
	if err != nil {
		t.Fatalf("poisoned ApplyBatch: %v", err)
	}
	if !poisonedOutcome.Poisoned {
		t.Fatalf("poisonedOutcome.Poisoned = false, want true")
	}

	var checkpoint ports.ProjectionCheckpoint
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		checkpoint, err = tx.Projections().GetProjectionCheckpoint(ctx, "project-1", projection.ProjectionName, outcome.Generation)
		return err
	}); err != nil {
		t.Fatalf("GetProjectionCheckpoint: %v", err)
	}
	if checkpoint.Status != ports.ProjectionDegraded {
		t.Fatalf("checkpoint.Status = %q, want DEGRADED", checkpoint.Status)
	}
	if checkpoint.Cursor != 2 {
		t.Fatalf("checkpoint.Cursor = %d, want 2 (frozen at the last-good position from the earlier successful round, not advanced past the poison event)", checkpoint.Cursor)
	}

	var poisonRecords []ports.ProjectionPoisonRecord
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		poisonRecords, err = tx.Projections().ListProjectionPoison(ctx, "project-1", projection.ProjectionName, outcome.Generation)
		return err
	}); err != nil {
		t.Fatalf("ListProjectionPoison: %v", err)
	}
	if len(poisonRecords) != 1 {
		t.Fatalf("len(poisonRecords) = %d, want 1", len(poisonRecords))
	}

	// A second consumer cannot proceed while this one's lease is live.
	_, err = projection.ApplyBatch(ctx, uow, catalog, projection.ApplyBatchRequest{
		ProjectID: "project-1", ProjectionName: projection.ProjectionName, Owner: "consumer-b",
		TTL: 30 * time.Second, BatchSize: 100, Now: now.Add(3 * time.Second), IDs: idsource.Random{},
	})
	if !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("consumer-b ApplyBatch while consumer-a holds a live lease: err = %v, want ErrOptimisticConflict", err)
	}
}
