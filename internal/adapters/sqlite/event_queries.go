package sqlite

import (
	"context"
	"fmt"
)

// DomainEventRecord is one domain_events row, read back for a test's own
// observability — never used by any production write path (EventsRepository
// stays append-only, ports/unitofwork.go's own doc comment). V4-14
// (docs/design/06-v4-runtime-engine.md, Runtime engine acceptance gate) is
// this method's first real caller: its own "deterministic event/activation
// golden" comparison needs the complete, ordered causal event log for a
// Project, not just individual aggregate slices, mirroring the same
// "small, direct, test-support query on *Store" shape LoadDurableJobState/
// CountDurableJobsByKind/CountWorkspaceSets already established.
type DomainEventRecord struct {
	ID              string
	ProjectID       string
	AggregateType   string
	AggregateID     string
	Sequence        int64
	JournalPosition int64
	EventType       string
	SchemaVersion   int
	PayloadJSON     string
	CorrelationID   string
	CausationID     string
	CreatedAt       string
}

// ListDomainEventsForProject returns every domain_events row for projectID,
// ordered by journal_position — the installation-wide causal order every
// event this project's own aggregates ever produced was actually appended
// in, the same total order allocateJournalPosition's own doc comment
// describes.
// DebugJobRow is a minimal debug-only projection of one durable_jobs row —
// never used by production code, only by V4-14's own test diagnostics.
type DebugJobRow struct {
	ID            string
	Kind          string
	AggregateID   string
	State         string
	ClaimCount    int
	LastErrorCode string
	AvailableAt   string
}

func (s *Store) DebugListJobsByKind(ctx context.Context, kind string) ([]DebugJobRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, aggregate_id, state, claim_count, COALESCE(last_error_code, ''), available_at FROM durable_jobs WHERE kind = ? ORDER BY created_at`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DebugJobRow
	for rows.Next() {
		var r DebugJobRow
		if err := rows.Scan(&r.ID, &r.Kind, &r.AggregateID, &r.State, &r.ClaimCount, &r.LastErrorCode, &r.AvailableAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) ListDomainEventsForProject(ctx context.Context, projectID string) ([]DomainEventRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, project_id, aggregate_type, aggregate_id, sequence, journal_position,
       event_type, schema_version, payload_json, correlation_id, causation_id, created_at
FROM domain_events
WHERE project_id = ?
ORDER BY journal_position`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list domain events for project %s: %w", projectID, err)
	}
	defer rows.Close()

	var records []DomainEventRecord
	for rows.Next() {
		var r DomainEventRecord
		var causationID *string
		if err := rows.Scan(
			&r.ID, &r.ProjectID, &r.AggregateType, &r.AggregateID, &r.Sequence, &r.JournalPosition,
			&r.EventType, &r.SchemaVersion, &r.PayloadJSON, &r.CorrelationID, &causationID, &r.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan domain event row: %w", err)
		}
		if causationID != nil {
			r.CausationID = *causationID
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate domain events for project %s: %w", projectID, err)
	}
	return records, nil
}
