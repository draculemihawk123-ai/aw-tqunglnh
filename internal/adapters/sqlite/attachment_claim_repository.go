package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// This file gives attachmentClaimRepository its methods (V6-07A,
// docs/design/08-v6-api-projections.md V6-07A) — the durable upload-side
// prepare-claim row over migration 0038's own attachment_prepare_claims
// table. Follows artifact_repository.go's own Tx-composable pattern
// exactly: one xxxTx(ctx, tx *sql.Tx, ...) function per operation, plus a
// thin attachmentClaimRepository method that forwards to it.
type attachmentClaimRepository struct{ tx *sql.Tx }

var _ ports.AttachmentClaimRepository = attachmentClaimRepository{}

// ClaimAttachmentUpload implements ports.AttachmentClaimRepository:
// idempotent insert-or-return-existing on claim.UploadID's own PRIMARY KEY.
func (r attachmentClaimRepository) ClaimAttachmentUpload(ctx context.Context, claim ports.AttachmentPrepareClaim) (ports.AttachmentPrepareClaim, bool, error) {
	var attemptID any
	if claim.AttemptID != "" {
		attemptID = claim.AttemptID
	}
	_, err := r.tx.ExecContext(ctx, `
INSERT INTO attachment_prepare_claims (
    upload_id, project_id, work_item_id, attempt_id, actor, idempotency_key, role, content_type,
    sensitivity, retention_class, declared_sha256, state, locator, actual_sha256, size,
    claim_owner, claimed_at, updated_at, version
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL, NULL, ?, ?, ?, 1)`,
		claim.UploadID, claim.ProjectID, claim.WorkItemID, attemptID, claim.Actor, claim.IdempotencyKey,
		claim.Role, claim.ContentType, claim.Sensitivity, claim.RetentionClass, claim.DeclaredSHA256,
		string(ports.AttachmentClaimSpooling), claim.ClaimOwner,
		formatWorkflowTime(claim.ClaimedAt), formatWorkflowTime(claim.ClaimedAt),
	)
	if err == nil {
		created, loadErr := loadAttachmentClaimTx(ctx, r.tx, claim.UploadID)
		return created, true, loadErr
	}
	existing, loadErr := loadAttachmentClaimTx(ctx, r.tx, claim.UploadID)
	if loadErr != nil {
		return ports.AttachmentPrepareClaim{}, false, MapSQLiteError(fmt.Errorf("claim attachment upload %s: %w", claim.UploadID, err))
	}
	return existing, false, nil
}

// GetAttachmentClaim implements ports.AttachmentClaimRepository.
func (r attachmentClaimRepository) GetAttachmentClaim(ctx context.Context, uploadID string) (ports.AttachmentPrepareClaim, error) {
	return loadAttachmentClaimTx(ctx, r.tx, uploadID)
}

func loadAttachmentClaimTx(ctx context.Context, tx *sql.Tx, uploadID string) (ports.AttachmentPrepareClaim, error) {
	row := tx.QueryRowContext(ctx, `
SELECT upload_id, project_id, work_item_id, attempt_id, actor, idempotency_key, role, content_type,
       sensitivity, retention_class, declared_sha256, state, locator, actual_sha256, size,
       claim_owner, claimed_at, updated_at, version
FROM attachment_prepare_claims WHERE upload_id = ?`, uploadID)
	claim, err := scanAttachmentClaimRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.AttachmentPrepareClaim{}, fmt.Errorf("%w: attachment claim %s", ports.ErrPersistenceNotFound, uploadID)
	}
	return claim, err
}

func scanAttachmentClaimRow(row repositoryRowScanner) (ports.AttachmentPrepareClaim, error) {
	var uploadID, projectID, workItemID, actor, idempotencyKey, role, contentType string
	var sensitivity, retentionClass, declaredSHA256, state, claimOwner string
	var claimedAtRaw, updatedAtRaw string
	var attemptID, locator, actualSHA256 sql.NullString
	var size sql.NullInt64
	var version uint64
	if err := row.Scan(
		&uploadID, &projectID, &workItemID, &attemptID, &actor, &idempotencyKey, &role, &contentType,
		&sensitivity, &retentionClass, &declaredSHA256, &state, &locator, &actualSHA256, &size,
		&claimOwner, &claimedAtRaw, &updatedAtRaw, &version,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ports.AttachmentPrepareClaim{}, err
		}
		return ports.AttachmentPrepareClaim{}, MapSQLiteError(fmt.Errorf("scan attachment claim row: %w", err))
	}
	claimedAt, err := parseWorkflowTime(claimedAtRaw)
	if err != nil {
		return ports.AttachmentPrepareClaim{}, err
	}
	updatedAt, err := parseWorkflowTime(updatedAtRaw)
	if err != nil {
		return ports.AttachmentPrepareClaim{}, err
	}
	claim := ports.AttachmentPrepareClaim{
		UploadID: uploadID, ProjectID: projectID, WorkItemID: workItemID, Actor: actor,
		IdempotencyKey: idempotencyKey, Role: role, ContentType: contentType, Sensitivity: sensitivity,
		RetentionClass: retentionClass, DeclaredSHA256: declaredSHA256,
		State: ports.AttachmentPrepareClaimState(state), ClaimOwner: claimOwner,
		ClaimedAt: claimedAt, UpdatedAt: updatedAt, Version: version,
	}
	if attemptID.Valid {
		claim.AttemptID = attemptID.String
	}
	if locator.Valid {
		claim.Locator = locator.String
	}
	if actualSHA256.Valid {
		claim.ActualSHA256 = actualSHA256.String
	}
	if size.Valid {
		claim.Size = size.Int64
	}
	return claim, nil
}

// RecordAttachmentBlobReady implements ports.AttachmentClaimRepository: the
// fenced CAS SPOOLING -> BLOB_READY.
func (r attachmentClaimRepository) RecordAttachmentBlobReady(ctx context.Context, req ports.RecordAttachmentBlobReadyRequest) (ports.AttachmentPrepareClaim, error) {
	result, err := r.tx.ExecContext(ctx, `
UPDATE attachment_prepare_claims
SET state = ?, locator = ?, actual_sha256 = ?, size = ?, updated_at = ?, version = version + 1
WHERE upload_id = ? AND state = ? AND version = ?`,
		string(ports.AttachmentClaimBlobReady), req.Locator, req.ActualSHA256, req.Size, formatWorkflowTime(req.UpdatedAt),
		req.UploadID, string(ports.AttachmentClaimSpooling), req.ExpectedVersion,
	)
	if err != nil {
		return ports.AttachmentPrepareClaim{}, MapSQLiteError(fmt.Errorf("record attachment blob ready %s: %w", req.UploadID, err))
	}
	return checkAttachmentClaimCAS(ctx, r.tx, result, req.UploadID, req.ExpectedVersion)
}

// TakeOverAttachmentClaim implements ports.AttachmentClaimRepository: the
// fenced CAS that updates ClaimOwner/ClaimedAt without touching State/
// Locator/ActualSHA256/Size.
func (r attachmentClaimRepository) TakeOverAttachmentClaim(ctx context.Context, req ports.TakeOverAttachmentClaimRequest) (ports.AttachmentPrepareClaim, error) {
	result, err := r.tx.ExecContext(ctx, `
UPDATE attachment_prepare_claims
SET claim_owner = ?, claimed_at = ?, updated_at = ?, version = version + 1
WHERE upload_id = ? AND version = ?`,
		req.NewClaimOwner, formatWorkflowTime(req.ClaimedAt), formatWorkflowTime(req.ClaimedAt),
		req.UploadID, req.ExpectedVersion,
	)
	if err != nil {
		return ports.AttachmentPrepareClaim{}, MapSQLiteError(fmt.Errorf("take over attachment claim %s: %w", req.UploadID, err))
	}
	return checkAttachmentClaimCAS(ctx, r.tx, result, req.UploadID, req.ExpectedVersion)
}

func checkAttachmentClaimCAS(ctx context.Context, tx *sql.Tx, result sql.Result, uploadID string, expectedVersion uint64) (ports.AttachmentPrepareClaim, error) {
	affected, err := result.RowsAffected()
	if err != nil {
		return ports.AttachmentPrepareClaim{}, fmt.Errorf("read attachment claim CAS result: %w", err)
	}
	if affected != 1 {
		var exists int
		lookupErr := tx.QueryRowContext(ctx, `SELECT 1 FROM attachment_prepare_claims WHERE upload_id = ?`, uploadID).Scan(&exists)
		if errors.Is(lookupErr, sql.ErrNoRows) {
			return ports.AttachmentPrepareClaim{}, fmt.Errorf("%w: attachment claim %s", ports.ErrPersistenceNotFound, uploadID)
		}
		if lookupErr != nil {
			return ports.AttachmentPrepareClaim{}, MapSQLiteError(fmt.Errorf("check stale attachment claim CAS: %w", lookupErr))
		}
		return ports.AttachmentPrepareClaim{}, fmt.Errorf(
			"%w: attachment claim %s expected version %d", ports.ErrOptimisticConflict, uploadID, expectedVersion,
		)
	}
	return loadAttachmentClaimTx(ctx, tx, uploadID)
}

// ReleaseAttachmentClaim implements ports.AttachmentClaimRepository.
// Idempotent: an UploadID with no open claim is left exactly as it is (zero
// rows affected is not an error).
func (r attachmentClaimRepository) ReleaseAttachmentClaim(ctx context.Context, uploadID string) error {
	if _, err := r.tx.ExecContext(ctx, `DELETE FROM attachment_prepare_claims WHERE upload_id = ?`, uploadID); err != nil {
		return MapSQLiteError(fmt.Errorf("release attachment claim %s: %w", uploadID, err))
	}
	return nil
}

// ListStaleAttachmentClaims implements ports.AttachmentClaimRepository,
// ordered by (claimed_at, upload_id) for a stable, deterministic result.
func (r attachmentClaimRepository) ListStaleAttachmentClaims(ctx context.Context, olderThan time.Time) ([]ports.AttachmentPrepareClaim, error) {
	rows, err := r.tx.QueryContext(ctx,
		`SELECT upload_id FROM attachment_prepare_claims WHERE claimed_at <= ? ORDER BY claimed_at, upload_id`,
		formatWorkflowTime(olderThan),
	)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list stale attachment claims: %w", err))
	}
	var uploadIDs []string
	for rows.Next() {
		var uploadID string
		if err := rows.Scan(&uploadID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan stale attachment claim upload id: %w", err)
		}
		uploadIDs = append(uploadIDs, uploadID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate stale attachment claim upload ids: %w", err)
	}
	rows.Close()

	claims := make([]ports.AttachmentPrepareClaim, 0, len(uploadIDs))
	for _, uploadID := range uploadIDs {
		claim, err := loadAttachmentClaimTx(ctx, r.tx, uploadID)
		if err != nil {
			return nil, err
		}
		claims = append(claims, claim)
	}
	return claims, nil
}
