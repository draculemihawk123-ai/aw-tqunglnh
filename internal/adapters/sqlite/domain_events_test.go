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

// TestEventsRepository_Append_JournalPositionMonotonicAcrossAggregates
// proves journal_position is one global, monotonically increasing
// sequence (ADR-015) shared across every aggregate — not reset or scoped
// per aggregate the way `sequence` is.
func TestEventsRepository_Append_JournalPositionMonotonicAcrossAggregates(t *testing.T) {
	ctx := context.Background()
	store := openReceiptsStore(t, "agentkit-events-journal-position.db")

	events := []ports.DomainEvent{
		{ID: "evt-1", AggregateType: "Project", AggregateID: "proj-1", Sequence: 1,
			EventType: "ProjectCreated", SchemaVersion: 1, PayloadJSON: `{}`, CorrelationID: "corr-1", CreatedAt: time.Now().UTC()},
		{ID: "evt-2", AggregateType: "Project", AggregateID: "proj-2", Sequence: 1,
			EventType: "ProjectCreated", SchemaVersion: 1, PayloadJSON: `{}`, CorrelationID: "corr-1", CreatedAt: time.Now().UTC()},
		{ID: "evt-3", AggregateType: "Project", AggregateID: "proj-1", Sequence: 2,
			EventType: "ProjectRenamed", SchemaVersion: 1, PayloadJSON: `{}`, CorrelationID: "corr-1", CreatedAt: time.Now().UTC()},
	}
	for _, event := range events {
		if err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
			return appendEvent(ctx, tx, event)
		}); err != nil {
			t.Fatalf("Append(%s): %v", event.ID, err)
		}
	}

	rows, err := store.db.QueryContext(ctx, `SELECT id, journal_position FROM domain_events ORDER BY journal_position`)
	if err != nil {
		t.Fatalf("query journal positions: %v", err)
	}
	defer rows.Close()
	var gotIDs []string
	var gotPositions []int64
	for rows.Next() {
		var id string
		var position int64
		if err := rows.Scan(&id, &position); err != nil {
			t.Fatalf("scan: %v", err)
		}
		gotIDs = append(gotIDs, id)
		gotPositions = append(gotPositions, position)
	}
	wantIDs := []string{"evt-1", "evt-2", "evt-3"}
	wantPositions := []int64{1, 2, 3}
	for i := range wantIDs {
		if i >= len(gotIDs) || gotIDs[i] != wantIDs[i] || gotPositions[i] != wantPositions[i] {
			t.Fatalf("row %d = (id=%v, journal_position=%v), want (id=%s, journal_position=%d)",
				i, gotIDs, gotPositions, wantIDs[i], wantPositions[i])
		}
	}
}

func TestEventsRepository_Append_CausationIDOptional(t *testing.T) {
	ctx := context.Background()
	store := openReceiptsStore(t, "agentkit-events-causation.db")

	withCausation := ports.DomainEvent{
		ID: "evt-1", AggregateType: "Project", AggregateID: "proj-1", Sequence: 1,
		EventType: "ProjectCreated", SchemaVersion: 1, PayloadJSON: `{}`,
		CorrelationID: "corr-1", CausationID: "cmd-1", CreatedAt: time.Now().UTC(),
	}
	withoutCausation := ports.DomainEvent{
		ID: "evt-2", AggregateType: "Project", AggregateID: "proj-2", Sequence: 1,
		EventType: "ProjectCreated", SchemaVersion: 1, PayloadJSON: `{}`,
		CorrelationID: "corr-1", CreatedAt: time.Now().UTC(),
	}
	for _, event := range []ports.DomainEvent{withCausation, withoutCausation} {
		if err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
			return appendEvent(ctx, tx, event)
		}); err != nil {
			t.Fatalf("Append(%s): %v", event.ID, err)
		}
	}

	var causationID sql.NullString
	if err := store.db.QueryRowContext(ctx, `SELECT causation_id FROM domain_events WHERE id = ?`, "evt-1").Scan(&causationID); err != nil {
		t.Fatalf("read back evt-1 causation_id: %v", err)
	}
	if !causationID.Valid || causationID.String != "cmd-1" {
		t.Fatalf("evt-1 causation_id = %+v, want valid \"cmd-1\"", causationID)
	}
	if err := store.db.QueryRowContext(ctx, `SELECT causation_id FROM domain_events WHERE id = ?`, "evt-2").Scan(&causationID); err != nil {
		t.Fatalf("read back evt-2 causation_id: %v", err)
	}
	if causationID.Valid {
		t.Fatalf("evt-2 causation_id = %q, want NULL", causationID.String)
	}
}

func TestEventsRepository_Append_PayloadExceedsSizeLimit_Rejected(t *testing.T) {
	ctx := context.Background()
	store := openReceiptsStore(t, "agentkit-events-payload-too-big.db")

	oversized := make([]byte, maxDomainEventPayloadBytes+1)
	for i := range oversized {
		oversized[i] = 'a'
	}
	event := ports.DomainEvent{
		ID: "evt-1", AggregateType: "Project", AggregateID: "proj-1", Sequence: 1,
		EventType: "ProjectCreated", SchemaVersion: 1, PayloadJSON: string(oversized),
		CorrelationID: "corr-1", CreatedAt: time.Now().UTC(),
	}

	err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		return appendEvent(ctx, tx, event)
	})
	if apperror.CodeOf(err) != apperror.CodeInvalidArgument {
		t.Fatalf("err = %v, want apperror.CodeInvalidArgument", err)
	}

	var count int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM domain_events`).Scan(&count); err != nil {
		t.Fatalf("count domain_events: %v", err)
	}
	if count != 0 {
		t.Fatalf("count = %d, want 0 (a rejected oversized payload must never partially commit)", count)
	}
}

// TestEventsRepository_Append_AlsoEnqueuesOutboxMessage is V1-07's own
// "outbox same transaction" requirement (GC-INV-16): appending a domain
// event must always leave a matching outbox row committed alongside it.
func TestEventsRepository_Append_AlsoEnqueuesOutboxMessage(t *testing.T) {
	ctx := context.Background()
	store := openReceiptsStore(t, "agentkit-events-outbox-enqueue.db")

	event := ports.DomainEvent{
		ID: "evt-1", AggregateType: "Project", AggregateID: "proj-1", Sequence: 1,
		EventType: "ProjectCreated", SchemaVersion: 1, PayloadJSON: `{"name":"demo"}`,
		CorrelationID: "corr-1", CreatedAt: time.Now().UTC(),
	}
	if err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		return appendEvent(ctx, tx, event)
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	var eventID, topic, payload, status string
	err := store.db.QueryRowContext(ctx,
		`SELECT event_id, topic, payload_json, status FROM outbox WHERE event_id = ?`, event.ID,
	).Scan(&eventID, &topic, &payload, &status)
	if err != nil {
		t.Fatalf("read back outbox row: %v", err)
	}
	if eventID != event.ID || topic != event.EventType || payload != event.PayloadJSON || status != string(ports.OutboxAvailable) {
		t.Fatalf("outbox row = (event_id=%q topic=%q payload=%q status=%q), want (%q %q %q %q)",
			eventID, topic, payload, status, event.ID, event.EventType, event.PayloadJSON, ports.OutboxAvailable)
	}
}

func TestEventsRepository_Append_ExplicitTopicOverridesEventTypeDefault(t *testing.T) {
	ctx := context.Background()
	store := openReceiptsStore(t, "agentkit-events-topic-override.db")

	event := ports.DomainEvent{
		ID: "evt-1", AggregateType: "Project", AggregateID: "proj-1", Sequence: 1,
		EventType: "ProjectCreated", Topic: "project.lifecycle", SchemaVersion: 1, PayloadJSON: `{}`,
		CorrelationID: "corr-1", CreatedAt: time.Now().UTC(),
	}
	if err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		return appendEvent(ctx, tx, event)
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	var topic string
	if err := store.db.QueryRowContext(ctx, `SELECT topic FROM outbox WHERE event_id = ?`, event.ID).Scan(&topic); err != nil {
		t.Fatalf("read back outbox topic: %v", err)
	}
	if topic != "project.lifecycle" {
		t.Fatalf("topic = %q, want %q (explicit Topic must win over the EventType default)", topic, "project.lifecycle")
	}
}

func TestEventsRepository_ScanJournal_OrderedGlobalAcrossProjectsAndRespectsLimit(t *testing.T) {
	ctx := context.Background()
	store := openReceiptsStore(t, "agentkit-events-scan-journal.db")

	now := time.Now().UTC()
	events := []ports.DomainEvent{
		{ID: "evt-a", ProjectID: "proj-1", AggregateType: "Project", AggregateID: "proj-1", Sequence: 1, EventType: "ProjectCreated", SchemaVersion: 1, PayloadJSON: `{"n":1}`, CorrelationID: "c1", CreatedAt: now},
		{ID: "evt-b", ProjectID: "proj-2", AggregateType: "Project", AggregateID: "proj-2", Sequence: 1, EventType: "ProjectCreated", SchemaVersion: 1, PayloadJSON: `{"n":2}`, CorrelationID: "c2", CreatedAt: now},
		{ID: "evt-c", ProjectID: "", AggregateType: "Installation", AggregateID: "singleton", Sequence: 1, EventType: "SafeSettingsUpdated", SchemaVersion: 1, PayloadJSON: `{"n":3}`, CorrelationID: "c3", CreatedAt: now},
	}
	for _, event := range events {
		if err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
			return appendEvent(ctx, tx, event)
		}); err != nil {
			t.Fatalf("Append(%s): %v", event.ID, err)
		}
	}

	var scanned []ports.JournalEvent
	if err := store.RunReadOnly(ctx, func(tx *sql.Tx) error {
		var err error
		scanned, err = eventsRepository{tx: tx}.ScanJournal(ctx, 0, 10)
		return err
	}); err != nil {
		t.Fatalf("ScanJournal: %v", err)
	}
	if len(scanned) != 3 {
		t.Fatalf("len(scanned) = %d, want 3 (global scan sees every project, not just one)", len(scanned))
	}
	wantOrder := []string{"ProjectCreated", "ProjectCreated", "SafeSettingsUpdated"}
	wantProjects := []string{"proj-1", "proj-2", ""}
	for i, want := range wantOrder {
		if scanned[i].EventType != want {
			t.Fatalf("scanned[%d].EventType = %q, want %q (append order == journal_position order)", i, scanned[i].EventType, want)
		}
		if scanned[i].ProjectID != wantProjects[i] {
			t.Fatalf("scanned[%d].ProjectID = %q, want %q (installation-scoped decodes to empty string, not NULL leaking through)", i, scanned[i].ProjectID, wantProjects[i])
		}
	}
	if scanned[0].JournalPosition == 0 {
		t.Fatal("scanned[0].JournalPosition should be a real, non-zero allocated position")
	}

	// afterPosition excludes already-seen rows — the exact mechanism that
	// makes "Duplicate position<=cursor no-op" true by construction: a
	// caller re-scanning from its own already-advanced cursor simply never
	// sees a row it already applied.
	var afterFirst []ports.JournalEvent
	if err := store.RunReadOnly(ctx, func(tx *sql.Tx) error {
		var err error
		afterFirst, err = eventsRepository{tx: tx}.ScanJournal(ctx, scanned[0].JournalPosition, 10)
		return err
	}); err != nil {
		t.Fatalf("ScanJournal(afterPosition=%d): %v", scanned[0].JournalPosition, err)
	}
	if len(afterFirst) != 2 {
		t.Fatalf("len(afterFirst) = %d, want 2", len(afterFirst))
	}

	// limit bounds the batch size.
	var limited []ports.JournalEvent
	if err := store.RunReadOnly(ctx, func(tx *sql.Tx) error {
		var err error
		limited, err = eventsRepository{tx: tx}.ScanJournal(ctx, 0, 1)
		return err
	}); err != nil {
		t.Fatalf("ScanJournal(limit=1): %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("len(limited) = %d, want 1", len(limited))
	}
}
