package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// This file gives projectionRepository its methods (V6-08,
// docs/design/08-v6-api-projections.md V6-08) — pure CRUD/CAS over
// migration 0039_projection_schema.sql's own projection_generations/
// projection_rows/projection_checkpoints/projection_poison tables. Follows
// attachment_claim_repository.go's own Tx-composable pattern exactly: one
// xxxTx(ctx, tx *sql.Tx, ...) function per operation, plus a thin
// projectionRepository method that forwards to it.
type projectionRepository struct{ tx *sql.Tx }

var _ ports.ProjectionRepository = projectionRepository{}

// GetActiveGeneration implements ports.ProjectionRepository.
func (r projectionRepository) GetActiveGeneration(ctx context.Context, projectID, projectionName string) (uint64, bool, error) {
	var generation uint64
	err := r.tx.QueryRowContext(ctx,
		`SELECT active_generation FROM projection_generations WHERE project_id = ? AND projection_name = ?`,
		projectID, projectionName,
	).Scan(&generation)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, MapSQLiteError(fmt.Errorf("get active projection generation %s/%s: %w", projectID, projectionName, err))
	}
	return generation, true, nil
}

// EnsureGeneration implements ports.ProjectionRepository: idempotent
// first-create, ErrOptimisticConflict if a DIFFERENT generation is already
// active.
func (r projectionRepository) EnsureGeneration(ctx context.Context, projectID, projectionName string, generation uint64, schemaVersion int, updatedAt time.Time) error {
	_, err := r.tx.ExecContext(ctx, `
INSERT INTO projection_generations (project_id, projection_name, active_generation, schema_version, updated_at)
VALUES (?, ?, ?, ?, ?)`,
		projectID, projectionName, generation, schemaVersion, formatWorkflowTime(updatedAt),
	)
	if err == nil {
		return nil
	}
	existing, ok, getErr := r.GetActiveGeneration(ctx, projectID, projectionName)
	if getErr != nil {
		return getErr
	}
	if !ok {
		return MapSQLiteError(fmt.Errorf("ensure projection generation %s/%s: %w", projectID, projectionName, err))
	}
	if existing == generation {
		return nil
	}
	return fmt.Errorf("%w: projection %s/%s active generation is %d, not %d", ports.ErrOptimisticConflict, projectID, projectionName, existing, generation)
}

// UpsertProjectionRow implements ports.ProjectionRepository: unconditional
// insert-or-replace on the full (ProjectID, ProjectionName, Generation,
// EntityKey) key — see ProjectionRow's own doc comment for why a caller
// always supplies the row's complete new PayloadJSON rather than a partial
// merge.
func (r projectionRepository) UpsertProjectionRow(ctx context.Context, row ports.ProjectionRow) error {
	_, err := r.tx.ExecContext(ctx, `
INSERT INTO projection_rows (
    project_id, projection_name, generation, entity_key, payload_json, last_applied_journal_position, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (project_id, projection_name, generation, entity_key) DO UPDATE SET
    payload_json = excluded.payload_json,
    last_applied_journal_position = excluded.last_applied_journal_position,
    updated_at = excluded.updated_at`,
		row.ProjectID, row.ProjectionName, row.Generation, row.EntityKey, row.PayloadJSON,
		row.LastAppliedJournalPosition, formatWorkflowTime(row.UpdatedAt),
	)
	if err != nil {
		return MapSQLiteError(fmt.Errorf("upsert projection row %s/%s/%d/%s: %w", row.ProjectID, row.ProjectionName, row.Generation, row.EntityKey, err))
	}
	return nil
}

// GetProjectionRow implements ports.ProjectionRepository.
func (r projectionRepository) GetProjectionRow(ctx context.Context, projectID, projectionName string, generation uint64, entityKey string) (ports.ProjectionRow, error) {
	row := r.tx.QueryRowContext(ctx, `
SELECT project_id, projection_name, generation, entity_key, payload_json, last_applied_journal_position, updated_at
FROM projection_rows WHERE project_id = ? AND projection_name = ? AND generation = ? AND entity_key = ?`,
		projectID, projectionName, generation, entityKey,
	)
	result, err := scanProjectionRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.ProjectionRow{}, fmt.Errorf("%w: projection row %s/%s/%d/%s", ports.ErrPersistenceNotFound, projectID, projectionName, generation, entityKey)
	}
	return result, err
}

func scanProjectionRow(row repositoryRowScanner) (ports.ProjectionRow, error) {
	var projectID, projectionName, entityKey, payloadJSON, updatedAtRaw string
	var generation, lastAppliedJournalPosition uint64
	if err := row.Scan(&projectID, &projectionName, &generation, &entityKey, &payloadJSON, &lastAppliedJournalPosition, &updatedAtRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ports.ProjectionRow{}, err
		}
		return ports.ProjectionRow{}, MapSQLiteError(fmt.Errorf("scan projection row: %w", err))
	}
	updatedAt, err := parseWorkflowTime(updatedAtRaw)
	if err != nil {
		return ports.ProjectionRow{}, err
	}
	return ports.ProjectionRow{
		ProjectID: projectID, ProjectionName: projectionName, Generation: generation, EntityKey: entityKey,
		PayloadJSON: payloadJSON, LastAppliedJournalPosition: lastAppliedJournalPosition, UpdatedAt: updatedAt,
	}, nil
}

// ListProjectionRows implements ports.ProjectionRepository, EntityKey
// ascending.
func (r projectionRepository) ListProjectionRows(ctx context.Context, projectID, projectionName string, generation uint64) ([]ports.ProjectionRow, error) {
	rows, err := r.tx.QueryContext(ctx, `
SELECT project_id, projection_name, generation, entity_key, payload_json, last_applied_journal_position, updated_at
FROM projection_rows WHERE project_id = ? AND projection_name = ? AND generation = ? ORDER BY entity_key`,
		projectID, projectionName, generation,
	)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list projection rows %s/%s/%d: %w", projectID, projectionName, generation, err))
	}
	defer rows.Close()
	var result []ports.ProjectionRow
	for rows.Next() {
		row, err := scanProjectionRow(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate projection rows %s/%s/%d: %w", projectID, projectionName, generation, err)
	}
	return result, nil
}

// GetProjectionCheckpoint implements ports.ProjectionRepository.
func (r projectionRepository) GetProjectionCheckpoint(ctx context.Context, projectID, projectionName string, generation uint64) (ports.ProjectionCheckpoint, error) {
	row := r.tx.QueryRowContext(ctx, `
SELECT project_id, projection_name, generation, cursor, status, fence_token, updated_at
FROM projection_checkpoints WHERE project_id = ? AND projection_name = ? AND generation = ?`,
		projectID, projectionName, generation,
	)
	checkpoint, err := scanProjectionCheckpoint(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.ProjectionCheckpoint{}, fmt.Errorf("%w: projection checkpoint %s/%s/%d", ports.ErrPersistenceNotFound, projectID, projectionName, generation)
	}
	return checkpoint, err
}

func scanProjectionCheckpoint(row repositoryRowScanner) (ports.ProjectionCheckpoint, error) {
	var projectID, projectionName, status, updatedAtRaw string
	var generation, cursor, fenceToken uint64
	if err := row.Scan(&projectID, &projectionName, &generation, &cursor, &status, &fenceToken, &updatedAtRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ports.ProjectionCheckpoint{}, err
		}
		return ports.ProjectionCheckpoint{}, MapSQLiteError(fmt.Errorf("scan projection checkpoint: %w", err))
	}
	updatedAt, err := parseWorkflowTime(updatedAtRaw)
	if err != nil {
		return ports.ProjectionCheckpoint{}, err
	}
	return ports.ProjectionCheckpoint{
		ProjectID: projectID, ProjectionName: projectionName, Generation: generation,
		Cursor: cursor, Status: ports.ProjectionStatus(status), FenceToken: fenceToken, UpdatedAt: updatedAt,
	}, nil
}

// UpsertProjectionCheckpoint implements ports.ProjectionRepository — see
// ports.UpsertProjectionCheckpointRequest's own doc comment for the CAS
// rule (nil ExpectedCursor = first insert, non-nil = fenced advance).
func (r projectionRepository) UpsertProjectionCheckpoint(ctx context.Context, req ports.UpsertProjectionCheckpointRequest) error {
	if req.ExpectedCursor == nil {
		_, err := r.tx.ExecContext(ctx, `
INSERT INTO projection_checkpoints (project_id, projection_name, generation, cursor, status, updated_at)
VALUES (?, ?, ?, ?, ?, ?)`,
			req.ProjectID, req.ProjectionName, req.Generation, req.NewCursor, string(req.NewStatus), formatWorkflowTime(req.UpdatedAt),
		)
		if err == nil {
			return nil
		}
		if _, getErr := r.GetProjectionCheckpoint(ctx, req.ProjectID, req.ProjectionName, req.Generation); getErr == nil {
			return fmt.Errorf("%w: projection checkpoint %s/%s/%d already exists", ports.ErrOptimisticConflict, req.ProjectID, req.ProjectionName, req.Generation)
		}
		return MapSQLiteError(fmt.Errorf("insert projection checkpoint %s/%s/%d: %w", req.ProjectID, req.ProjectionName, req.Generation, err))
	}

	query := `
UPDATE projection_checkpoints SET cursor = ?, status = ?, updated_at = ?
WHERE project_id = ? AND projection_name = ? AND generation = ? AND cursor = ?`
	args := []any{
		req.NewCursor, string(req.NewStatus), formatWorkflowTime(req.UpdatedAt),
		req.ProjectID, req.ProjectionName, req.Generation, *req.ExpectedCursor,
	}
	if req.ExpectedFenceToken != nil {
		query += ` AND fence_token = ?`
		args = append(args, *req.ExpectedFenceToken)
	}
	result, err := r.tx.ExecContext(ctx, query, args...)
	if err != nil {
		return MapSQLiteError(fmt.Errorf("advance projection checkpoint %s/%s/%d: %w", req.ProjectID, req.ProjectionName, req.Generation, err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read projection checkpoint CAS result: %w", err)
	}
	if affected == 1 {
		return nil
	}
	if _, getErr := r.GetProjectionCheckpoint(ctx, req.ProjectID, req.ProjectionName, req.Generation); getErr != nil {
		return getErr
	}
	return fmt.Errorf("%w: projection checkpoint %s/%s/%d expected cursor %d", ports.ErrOptimisticConflict, req.ProjectID, req.ProjectionName, req.Generation, *req.ExpectedCursor)
}

// AcquireOrRenewConsumerLease implements ports.ProjectionRepository —
// mirrors write_leases' own acquireWriteLeasesOnce single-statement
// acquire-or-steal-if-expired shape, with one addition write_leases does
// not need: the SAME owner may always renew its own still-live lease
// (write_leases never renews — it is claimed once per Attempt — but
// V6-08A's own live scanner is a long-running, repeatedly-invoked loop
// that must be able to heartbeat itself before its own lease would
// otherwise expire). INSERT creates the checkpoint row (fence_token 1) if
// none exists yet; ON CONFLICT DO UPDATE fires when the currently stored
// lease is unheld, already expired, OR already owned by THIS SAME owner —
// incrementing fence_token every time (a renewal is still a real state
// change: a later ApplyBatch round for this owner must never accidentally
// reuse an earlier round's own now-stale fence_token). A live, unexpired
// lease held by a DIFFERENT owner blocks the update, so RETURNING yields
// no row and this reports ErrOptimisticConflict, never a silent
// double-grant — the "two consumers" case.
func (r projectionRepository) AcquireOrRenewConsumerLease(ctx context.Context, req ports.AcquireOrRenewConsumerLeaseRequest) (ports.ConsumerLease, error) {
	now := formatWorkflowTime(req.Now)
	leaseUntil := formatWorkflowTime(req.Now.Add(req.TTL))
	var fenceToken, cursor uint64
	var status string
	var leaseUntilRaw string
	err := r.tx.QueryRowContext(ctx, `
INSERT INTO projection_checkpoints (
    project_id, projection_name, generation, cursor, status, fence_token, lease_owner, lease_until, heartbeat_at, updated_at
) VALUES (?, ?, ?, 0, ?, 1, ?, ?, ?, ?)
ON CONFLICT (project_id, projection_name, generation) DO UPDATE SET
    fence_token = projection_checkpoints.fence_token + 1,
    lease_owner = excluded.lease_owner,
    lease_until = excluded.lease_until,
    heartbeat_at = excluded.heartbeat_at
WHERE projection_checkpoints.lease_until IS NULL
   OR julianday(projection_checkpoints.lease_until) <= julianday(?)
   OR projection_checkpoints.lease_owner = ?
RETURNING fence_token, cursor, status, lease_until`,
		req.ProjectID, req.ProjectionName, req.Generation, string(ports.ProjectionLive), req.Owner, leaseUntil, now, now,
		now, req.Owner,
	).Scan(&fenceToken, &cursor, &status, &leaseUntilRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.ConsumerLease{}, fmt.Errorf("%w: projection consumer lease %s/%s/%d already held", ports.ErrOptimisticConflict, req.ProjectID, req.ProjectionName, req.Generation)
	}
	if err != nil {
		return ports.ConsumerLease{}, MapSQLiteError(fmt.Errorf("acquire projection consumer lease %s/%s/%d: %w", req.ProjectID, req.ProjectionName, req.Generation, err))
	}
	parsedLeaseUntil, err := parseWorkflowTime(leaseUntilRaw)
	if err != nil {
		return ports.ConsumerLease{}, err
	}
	return ports.ConsumerLease{
		FenceToken: fenceToken, Cursor: cursor, Status: ports.ProjectionStatus(status), LeaseUntil: parsedLeaseUntil,
	}, nil
}

// RecordProjectionPoison implements ports.ProjectionRepository. Never
// updates an existing row — a duplicate ID is the caller's own bug, not a
// legitimate retry to absorb (see ProjectionRepository's own interface doc
// comment: ID-minting/idempotency strategy is deliberately left to V6-08A,
// not built or assumed here).
func (r projectionRepository) RecordProjectionPoison(ctx context.Context, record ports.ProjectionPoisonRecord) error {
	_, err := r.tx.ExecContext(ctx, `
INSERT INTO projection_poison (
    id, project_id, projection_name, generation, journal_position, event_type, schema_version, reason, recorded_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, record.ProjectID, record.ProjectionName, record.Generation, record.JournalPosition,
		record.EventType, record.SchemaVersion, record.Reason, formatWorkflowTime(record.RecordedAt),
	)
	if err != nil {
		return MapSQLiteError(fmt.Errorf("record projection poison %s: %w", record.ID, err))
	}
	return nil
}

// ListProjectionPoison implements ports.ProjectionRepository,
// JournalPosition ascending.
func (r projectionRepository) ListProjectionPoison(ctx context.Context, projectID, projectionName string, generation uint64) ([]ports.ProjectionPoisonRecord, error) {
	rows, err := r.tx.QueryContext(ctx, `
SELECT id, project_id, projection_name, generation, journal_position, event_type, schema_version, reason, recorded_at
FROM projection_poison WHERE project_id = ? AND projection_name = ? AND generation = ? ORDER BY journal_position`,
		projectID, projectionName, generation,
	)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list projection poison %s/%s/%d: %w", projectID, projectionName, generation, err))
	}
	defer rows.Close()
	var result []ports.ProjectionPoisonRecord
	for rows.Next() {
		var id, pID, pName, eventType, reason, recordedAtRaw string
		var generationCol uint64
		var journalPosition uint64
		var schemaVersion int
		if err := rows.Scan(&id, &pID, &pName, &generationCol, &journalPosition, &eventType, &schemaVersion, &reason, &recordedAtRaw); err != nil {
			return nil, fmt.Errorf("scan projection poison row: %w", err)
		}
		recordedAt, err := parseWorkflowTime(recordedAtRaw)
		if err != nil {
			return nil, err
		}
		result = append(result, ports.ProjectionPoisonRecord{
			ID: id, ProjectID: pID, ProjectionName: pName, Generation: generationCol, JournalPosition: journalPosition,
			EventType: eventType, SchemaVersion: schemaVersion, Reason: reason, RecordedAt: recordedAt,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate projection poison rows %s/%s/%d: %w", projectID, projectionName, generation, err)
	}
	return result, nil
}
