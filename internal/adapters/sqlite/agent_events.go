package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// maxAgentEventPayloadBytes mirrors maxDomainEventPayloadBytes
// (domain_events.go): agent_events is the same shape of compact,
// structured record — a large tool output/artifact body belongs in the
// artifact store with the event carrying a locator, never inline here.
const maxAgentEventPayloadBytes = 256 * 1024

type agentEventsRepository struct{ tx *sql.Tx }

var _ ports.AgentEventsRepository = agentEventsRepository{}

// AppendBatch inserts every record inside the caller's existing
// transaction. Unlike checkpoints (whose StoreCheckpoint re-loads and
// compares on conflict, since more than one independent caller can
// legitimately race to store the identical checkpoint), agent_events
// duplicate avoidance is the caller's own job: agentevents.Sink tracks the
// last Sequence it has accepted per Attempt in memory and never presents
// this method with a (AttemptID, Sequence) pair it has already flushed — a
// genuine UNIQUE(attempt_id, sequence) violation reaching here is therefore
// a real bug, surfaced as an ordinary error exactly like domain_events.Append
// already does for its own analogous UNIQUE(aggregate_id, sequence).
func (r agentEventsRepository) AppendBatch(ctx context.Context, records []ports.AgentEventRecord) error {
	for _, record := range records {
		if len(record.PayloadJSON) > maxAgentEventPayloadBytes {
			return fmt.Errorf("%w: agent event payload is %d bytes, exceeds the %d byte limit",
				ErrAgentEventPayloadTooLarge, len(record.PayloadJSON), maxAgentEventPayloadBytes)
		}
		if _, err := r.tx.ExecContext(ctx, `
INSERT INTO agent_events (id, attempt_id, sequence, kind, schema_version, payload_json, artifact_refs_json, created_at)
VALUES (?, ?, ?, ?, ?, ?, '[]', ?)`,
			record.ID, record.AttemptID, record.Sequence, record.Kind, record.SchemaVersion, record.PayloadJSON,
			formatWorkflowTime(record.CreatedAt),
		); err != nil {
			return MapSQLiteError(fmt.Errorf("append agent event: %w", err))
		}
	}
	return nil
}

func (r agentEventsRepository) ListByAttempt(ctx context.Context, attemptID string) ([]ports.AgentEventRecord, error) {
	rows, err := r.tx.QueryContext(ctx, `
SELECT id, attempt_id, sequence, kind, schema_version, payload_json, created_at
FROM agent_events
WHERE attempt_id = ?
ORDER BY sequence ASC`, attemptID)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list agent events: %w", err))
	}
	defer rows.Close()

	var records []ports.AgentEventRecord
	for rows.Next() {
		var (
			record    ports.AgentEventRecord
			createdAt string
		)
		if err := rows.Scan(&record.ID, &record.AttemptID, &record.Sequence, &record.Kind, &record.SchemaVersion, &record.PayloadJSON, &createdAt); err != nil {
			return nil, fmt.Errorf("scan agent event: %w", err)
		}
		parsed, err := parseWorkflowTime(createdAt)
		if err != nil {
			return nil, err
		}
		record.CreatedAt = parsed
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, MapSQLiteError(err)
	}
	return records, nil
}

// ErrAgentEventPayloadTooLarge is returned by AppendBatch when a record's
// PayloadJSON exceeds maxAgentEventPayloadBytes.
var ErrAgentEventPayloadTooLarge = errors.New("sqlite: agent event payload too large")
