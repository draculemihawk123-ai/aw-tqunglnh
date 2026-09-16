package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// This file gives projectionRebuildRepository its methods (V6-09,
// docs/design/08-v6-api-projections.md V6-09) — pure CRUD over migration
// 0041_projection_rebuild_operations.sql's own table. Follows
// projectionRepository's (projection_repository.go) own Tx-composable
// pattern exactly: one xxxTx(ctx, tx *sql.Tx, ...) function per operation,
// plus a thin projectionRebuildRepository method that forwards to it.
type projectionRebuildRepository struct{ tx *sql.Tx }

var _ ports.ProjectionRebuildRepository = projectionRebuildRepository{}

// CreateOperation implements ports.ProjectionRebuildRepository: idempotent
// by ID only (mirrors createReleaseSetLocalCommitTx's own identical
// "insert; on any failure, reload by id and return that instead" shape) —
// RequestProjectionRebuild's own receipt-replay check is what actually
// prevents this from being called twice for the same logical request; this
// is only the same defensive symmetry every other Create* method in this
// codebase keeps.
func (r projectionRebuildRepository) CreateOperation(ctx context.Context, op ports.ProjectionRebuildOperation) (ports.ProjectionRebuildOperation, error) {
	return createProjectionRebuildOperationTx(ctx, r.tx, op)
}

func createProjectionRebuildOperationTx(ctx context.Context, tx *sql.Tx, op ports.ProjectionRebuildOperation) (ports.ProjectionRebuildOperation, error) {
	_, err := tx.ExecContext(ctx, `
INSERT INTO projection_rebuild_operations (
    id, project_id, projection_name, phase, w0, shadow_generation, shadow_cursor, cutover_cursor,
    error_code, error_message, job_id, requested_at, updated_at, version
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		op.ID, op.ProjectID, op.ProjectionName, string(op.Phase),
		nullableUint64(op.W0), nullableUint64(op.ShadowGeneration), nullableUint64(op.ShadowCursor), nullableUint64(op.CutoverCursor),
		nullableString(op.ErrorCode), nullableString(op.ErrorMessage), op.JobID,
		formatWorkflowTime(op.RequestedAt), formatWorkflowTime(op.UpdatedAt), op.Version,
	)
	if err != nil {
		existing, loadErr := loadProjectionRebuildOperationTx(ctx, tx, op.ID)
		if loadErr != nil {
			return ports.ProjectionRebuildOperation{}, MapSQLiteError(fmt.Errorf("create projection rebuild operation: %w", err))
		}
		return existing, nil
	}
	return op, nil
}

// GetOperation implements ports.ProjectionRebuildRepository.
func (r projectionRebuildRepository) GetOperation(ctx context.Context, id string) (ports.ProjectionRebuildOperation, error) {
	return loadProjectionRebuildOperationTx(ctx, r.tx, id)
}

func loadProjectionRebuildOperationTx(ctx context.Context, tx *sql.Tx, id string) (ports.ProjectionRebuildOperation, error) {
	row := tx.QueryRowContext(ctx, `
SELECT id, project_id, projection_name, phase, w0, shadow_generation, shadow_cursor, cutover_cursor,
       error_code, error_message, job_id, requested_at, updated_at, version
FROM projection_rebuild_operations WHERE id = ?`, id)
	return scanProjectionRebuildOperation(row, fmt.Sprintf("projection rebuild operation %s", id))
}

// GetActiveOperation implements ports.ProjectionRebuildRepository: the
// single nonterminal (ports.NonterminalProjectionRebuildPhases) operation
// for (projectID, projectionName), if any — migration 0041's own
// idx_projection_rebuild_operations_active partial unique index guarantees
// at most one row can ever match this WHERE clause at a time.
func (r projectionRebuildRepository) GetActiveOperation(ctx context.Context, projectID, projectionName string) (ports.ProjectionRebuildOperation, bool, error) {
	row := r.tx.QueryRowContext(ctx, `
SELECT id, project_id, projection_name, phase, w0, shadow_generation, shadow_cursor, cutover_cursor,
       error_code, error_message, job_id, requested_at, updated_at, version
FROM projection_rebuild_operations
WHERE project_id = ? AND projection_name = ? AND phase IN ('REQUESTED', 'SNAPSHOTTING', 'BUILDING', 'CUTTING_OVER')`,
		projectID, projectionName,
	)
	op, err := scanProjectionRebuildOperation(row, fmt.Sprintf("active projection rebuild operation %s/%s", projectID, projectionName))
	if errors.Is(err, ports.ErrPersistenceNotFound) {
		return ports.ProjectionRebuildOperation{}, false, nil
	}
	if err != nil {
		return ports.ProjectionRebuildOperation{}, false, err
	}
	return op, true, nil
}

func scanProjectionRebuildOperation(row *sql.Row, notFoundLabel string) (ports.ProjectionRebuildOperation, error) {
	var op ports.ProjectionRebuildOperation
	var phase string
	var w0, shadowGeneration, shadowCursor, cutoverCursor sql.NullInt64
	var errorCode, errorMessage sql.NullString
	var requestedAtRaw, updatedAtRaw string
	if err := row.Scan(
		&op.ID, &op.ProjectID, &op.ProjectionName, &phase, &w0, &shadowGeneration, &shadowCursor, &cutoverCursor,
		&errorCode, &errorMessage, &op.JobID, &requestedAtRaw, &updatedAtRaw, &op.Version,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ports.ProjectionRebuildOperation{}, fmt.Errorf("%w: %s", ports.ErrPersistenceNotFound, notFoundLabel)
		}
		return ports.ProjectionRebuildOperation{}, MapSQLiteError(fmt.Errorf("scan projection rebuild operation row: %w", err))
	}
	op.Phase = ports.ProjectionRebuildPhase(phase)
	op.W0 = uint64PtrFromNull(w0)
	op.ShadowGeneration = uint64PtrFromNull(shadowGeneration)
	op.ShadowCursor = uint64PtrFromNull(shadowCursor)
	op.CutoverCursor = uint64PtrFromNull(cutoverCursor)
	op.ErrorCode = errorCode.String
	op.ErrorMessage = errorMessage.String

	requestedAt, err := parseWorkflowTime(requestedAtRaw)
	if err != nil {
		return ports.ProjectionRebuildOperation{}, err
	}
	updatedAt, err := parseWorkflowTime(updatedAtRaw)
	if err != nil {
		return ports.ProjectionRebuildOperation{}, err
	}
	op.RequestedAt = requestedAt
	op.UpdatedAt = updatedAt
	return op, nil
}

// nullableUint64 converts a possibly-nil *uint64 field
// (ProjectionRebuildOperation.W0/ShadowGeneration/ShadowCursor/
// CutoverCursor) into a driver value: nil stays a real SQL NULL rather
// than a coerced 0 — 0 is itself a meaningful future watermark/cursor
// value V6-09A's own worker can legitimately write, so it must stay
// distinguishable from "not populated yet."
func nullableUint64(value *uint64) any {
	if value == nil {
		return nil
	}
	return int64(*value)
}

// uint64PtrFromNull is nullableUint64's own inverse for the scan side.
func uint64PtrFromNull(value sql.NullInt64) *uint64 {
	if !value.Valid {
		return nil
	}
	v := uint64(value.Int64)
	return &v
}
