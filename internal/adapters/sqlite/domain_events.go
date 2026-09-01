package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// Append implements ports.EventsRepository. It is intentionally minimal
// (V1-06's own illustrative proof, not V1-07's full outbox contract): the
// caller supplies Sequence explicitly, since real per-aggregate sequence
// allocation under concurrent writers is V1-07's job (GC-INV-15). A
// duplicate (aggregate_type, aggregate_id, sequence) violates the
// table's own UNIQUE constraint and is reported through MapSQLiteError
// like any other adapter error.
func (r eventsRepository) Append(ctx context.Context, event ports.DomainEvent) error {
	_, err := r.tx.ExecContext(ctx, `
INSERT INTO domain_events (
    id, project_id, aggregate_type, aggregate_id, sequence,
    event_type, schema_version, payload_json, correlation_id, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID, nullableString(event.ProjectID), event.AggregateType, event.AggregateID, event.Sequence,
		event.EventType, event.SchemaVersion, event.PayloadJSON, event.CorrelationID,
		event.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return MapSQLiteError(fmt.Errorf("append domain event: %w", err))
	}
	return nil
}
