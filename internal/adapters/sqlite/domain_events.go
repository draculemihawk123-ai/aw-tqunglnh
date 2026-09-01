package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// maxDomainEventPayloadBytes bounds PayloadJSON size (V1-07's "payload
// size... limits"): domain_events/outbox are compact decision records,
// not artifact bodies — a large payload belongs in the artifact store
// (V1-08), with the event carrying a locator instead.
const maxDomainEventPayloadBytes = 256 * 1024

// allocateJournalPosition computes MAX(journal_position)+1 inside the
// caller's own transaction (V1-07, ADR-015's database-monotonic
// JournalPosition): RunSerializedWrite already serializes every writer
// (V1-04A's _txlock=immediate DSN parameter), so a plain aggregate query
// is race-free here without a separate sequence table or SQLite
// AUTOINCREMENT column. Every domain_events writer — the V1-06/V1-07
// ports.EventsRepository path and the pre-existing V0 spike writers in
// attempt_store.go/node_dispatch.go/workspace_lifecycle.go/
// workflow_store.go alike — calls this same helper, so journal_position
// stays a single global sequence across every aggregate type.
func allocateJournalPosition(ctx context.Context, tx *sql.Tx) (int64, error) {
	var journalPosition int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(journal_position), 0) + 1 FROM domain_events`,
	).Scan(&journalPosition); err != nil {
		return 0, MapSQLiteError(fmt.Errorf("allocate journal position: %w", err))
	}
	return journalPosition, nil
}

// Append implements ports.EventsRepository. Every appended event also
// gets exactly one outbox row in the same transaction (GC-INV-16). This
// is a stricter guarantee than the invariant strictly requires — not
// every domain event necessarily has an external side effect — chosen
// because ports.DomainEvent has no field yet for "this one needs no
// durable delivery"; always enqueueing can never violate GC-INV-16, only
// over-deliver, and a consumer that only cares about certain event types
// simply ignores topics it doesn't subscribe to. The outbox row's own id
// is derived from the event id (event.ID+"-outbox") rather than drawn
// from a separate id source, since it only ever needs to be unique
// per-event, which the event id already guarantees.
//
// journal_position is allocated as MAX(journal_position)+1 inside this
// same transaction: RunSerializedWrite already serializes every writer
// (V1-04A's _txlock=immediate DSN parameter), so this is race-free
// without a separate sequence table or SQLite AUTOINCREMENT column.
func (r eventsRepository) Append(ctx context.Context, event ports.DomainEvent) error {
	if len(event.PayloadJSON) > maxDomainEventPayloadBytes {
		return apperror.New(apperror.CodeInvalidArgument,
			fmt.Sprintf("domain event payload is %d bytes, exceeds the %d byte limit", len(event.PayloadJSON), maxDomainEventPayloadBytes),
			false)
	}

	journalPosition, err := allocateJournalPosition(ctx, r.tx)
	if err != nil {
		return err
	}

	createdAt := event.CreatedAt.UTC().Format(time.RFC3339Nano)
	_, err = r.tx.ExecContext(ctx, `
INSERT INTO domain_events (
    id, project_id, aggregate_type, aggregate_id, sequence, journal_position,
    event_type, schema_version, payload_json, correlation_id, causation_id, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID, nullableString(event.ProjectID), event.AggregateType, event.AggregateID, event.Sequence, journalPosition,
		event.EventType, event.SchemaVersion, event.PayloadJSON, event.CorrelationID, nullableString(event.CausationID),
		createdAt,
	)
	if err != nil {
		return MapSQLiteError(fmt.Errorf("append domain event: %w", err))
	}

	topic := event.Topic
	if topic == "" {
		topic = event.EventType
	}
	if _, err := r.tx.ExecContext(ctx, `
INSERT INTO outbox (id, event_id, topic, payload_json, status, available_at, created_at)
VALUES (?, ?, ?, ?, 'AVAILABLE', ?, ?)`,
		event.ID+"-outbox", event.ID, topic, event.PayloadJSON, createdAt, createdAt,
	); err != nil {
		return MapSQLiteError(fmt.Errorf("enqueue outbox message: %w", err))
	}
	return nil
}
