package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

func appendEvent(ctx context.Context, tx *sql.Tx, event ports.DomainEvent) error {
	return eventsRepository{tx: tx}.Append(ctx, event)
}

func TestEventsRepository_Append_PersistsRow(t *testing.T) {
	ctx := context.Background()
	store := openReceiptsStore(t, "agentkit-events-append.db")

	event := ports.DomainEvent{
		ID:            "evt-1",
		ProjectID:     "proj-1",
		AggregateType: "Project",
		AggregateID:   "proj-1",
		Sequence:      1,
		EventType:     "ProjectCreated",
		SchemaVersion: 1,
		PayloadJSON:   `{"name":"demo"}`,
		CorrelationID: "corr-1",
		CreatedAt:     time.Now().UTC(),
	}

	err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		return appendEvent(ctx, tx, event)
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}

	var gotEventType, gotAggregateID string
	var gotSequence int64
	err = store.db.QueryRowContext(ctx,
		`SELECT event_type, aggregate_id, sequence FROM domain_events WHERE id = ?`, event.ID,
	).Scan(&gotEventType, &gotAggregateID, &gotSequence)
	if err != nil {
		t.Fatalf("read back appended event: %v", err)
	}
	if gotEventType != event.EventType || gotAggregateID != event.AggregateID || gotSequence != event.Sequence {
		t.Fatalf("got (event_type=%q aggregate_id=%q sequence=%d), want (%q %q %d)",
			gotEventType, gotAggregateID, gotSequence, event.EventType, event.AggregateID, event.Sequence)
	}
}

func TestEventsRepository_Append_InstallationScopedEventHasNullProjectID(t *testing.T) {
	ctx := context.Background()
	store := openReceiptsStore(t, "agentkit-events-null-project.db")

	event := ports.DomainEvent{
		ID: "evt-installation", ProjectID: "", AggregateType: "Installation", AggregateID: "singleton",
		Sequence: 1, EventType: "InstallationConfigured", SchemaVersion: 1,
		PayloadJSON: `{}`, CorrelationID: "corr-1", CreatedAt: time.Now().UTC(),
	}
	if err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		return appendEvent(ctx, tx, event)
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	var projectID sql.NullString
	if err := store.db.QueryRowContext(ctx,
		`SELECT project_id FROM domain_events WHERE id = ?`, event.ID).Scan(&projectID); err != nil {
		t.Fatalf("read back project_id: %v", err)
	}
	if projectID.Valid {
		t.Fatalf("project_id = %q, want NULL for an installation-scoped event", projectID.String)
	}
}

// TestEventsRepository_Append_DuplicateAggregateSequence_Rejected proves the
// table's own UNIQUE(aggregate_type, aggregate_id, sequence) constraint is
// still enforced through the repository, mapped through MapSQLiteError like
// any other adapter error rather than silently overwriting or duplicating.
func TestEventsRepository_Append_DuplicateAggregateSequence_Rejected(t *testing.T) {
	ctx := context.Background()
	store := openReceiptsStore(t, "agentkit-events-dup-sequence.db")

	first := ports.DomainEvent{
		ID: "evt-1", AggregateType: "Project", AggregateID: "proj-1", Sequence: 1,
		EventType: "ProjectCreated", SchemaVersion: 1, PayloadJSON: `{}`,
		CorrelationID: "corr-1", CreatedAt: time.Now().UTC(),
	}
	second := first
	second.ID = "evt-2" // different event id, same aggregate+sequence

	if err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		return appendEvent(ctx, tx, first)
	}); err != nil {
		t.Fatalf("Append first: %v", err)
	}

	err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		return appendEvent(ctx, tx, second)
	})
	if err == nil {
		t.Fatal("Append with a duplicate (aggregate_type, aggregate_id, sequence) should fail")
	}
	if apperror.CodeOf(err) == "" {
		t.Fatalf("err = %v, want it mapped through MapSQLiteError to a typed apperror.Error", err)
	}
}
