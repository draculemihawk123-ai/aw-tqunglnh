package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// This file gives workRepository the selection-record methods of V11-07 (ADR-040):
// migration 0050's release_set_local_commit_selections.

// CreateReleaseSetLocalCommitSelection implements ports.WorkRepository. Idempotent by operation
// id: a second insert of the same id leaves the first record untouched.
func (r workRepository) CreateReleaseSetLocalCommitSelection(ctx context.Context, selection work.ReleaseSetLocalCommitSelection) error {
	var exists int
	err := r.tx.QueryRowContext(ctx, `SELECT 1 FROM release_set_local_commits WHERE id = ?`, string(selection.ReleaseSetLocalCommitID)).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: release set local commit %s", ports.ErrPersistenceNotFound, selection.ReleaseSetLocalCommitID)
	}
	if err != nil {
		return MapSQLiteError(fmt.Errorf("resolve release set local commit for selection: %w", err))
	}
	filesJSON, err := json.Marshal(selection.Files)
	if err != nil {
		return fmt.Errorf("marshal selection files: %w", err)
	}
	truncated := 0
	if selection.PatchTruncated {
		truncated = 1
	}
	_, err = r.tx.ExecContext(ctx, `
INSERT OR IGNORE INTO release_set_local_commit_selections (
    release_set_local_commit_id, approval_request_id, work_item_id, run_id, files_json, change_set_digest,
    unselected, patch, patch_truncated, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(selection.ReleaseSetLocalCommitID), nullableString(selection.ApprovalRequestID), nullableString(selection.WorkItemID),
		nullableString(selection.RunID), string(filesJSON), selection.ChangeSetDigest, string(selection.Unselected),
		selection.Patch, truncated, formatWorkflowTime(selection.CreatedAt),
	)
	if err != nil {
		return MapSQLiteError(fmt.Errorf("create release set local commit selection: %w", err))
	}
	return nil
}

const selectionColumns = `release_set_local_commit_id, approval_request_id, work_item_id, run_id, files_json, change_set_digest, unselected,
       parked_ref, parked_paths_json, patch, patch_truncated, restored_at, created_at`

// GetReleaseSetLocalCommitSelection implements ports.WorkRepository.
func (r workRepository) GetReleaseSetLocalCommitSelection(ctx context.Context, id string) (work.ReleaseSetLocalCommitSelection, bool, error) {
	selection, err := scanSelection(r.tx.QueryRowContext(ctx,
		`SELECT `+selectionColumns+` FROM release_set_local_commit_selections WHERE release_set_local_commit_id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return work.ReleaseSetLocalCommitSelection{}, false, nil
	}
	if err != nil {
		return work.ReleaseSetLocalCommitSelection{}, false, err
	}
	return selection, true, nil
}

// ListReleaseSetLocalCommitSelectionsByApproval implements ports.WorkRepository.
func (r workRepository) ListReleaseSetLocalCommitSelectionsByApproval(ctx context.Context, approvalRequestID string) ([]work.ReleaseSetLocalCommitSelection, error) {
	rows, err := r.tx.QueryContext(ctx,
		`SELECT `+selectionColumns+` FROM release_set_local_commit_selections WHERE approval_request_id = ? ORDER BY release_set_local_commit_id`, approvalRequestID)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list selections of approval %s: %w", approvalRequestID, err))
	}
	defer rows.Close()
	var out []work.ReleaseSetLocalCommitSelection
	for rows.Next() {
		selection, err := scanSelection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, selection)
	}
	return out, MapSQLiteError(rows.Err())
}

// scanSelection reads one selectionColumns row; sql.ErrNoRows is returned bare so Get can answer found=false.
func scanSelection(row rowScanner) (work.ReleaseSetLocalCommitSelection, error) {
	var selection work.ReleaseSetLocalCommitSelection
	var opID, unselected, filesJSON, createdRaw string
	var approvalRequestID, workItemID, runID, parkedRef, parkedPathsJSON, restoredRaw sql.NullString
	var patch []byte
	var truncated int
	if err := row.Scan(&opID, &approvalRequestID, &workItemID, &runID, &filesJSON, &selection.ChangeSetDigest, &unselected,
		&parkedRef, &parkedPathsJSON, &patch, &truncated, &restoredRaw, &createdRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return work.ReleaseSetLocalCommitSelection{}, err
		}
		return work.ReleaseSetLocalCommitSelection{}, MapSQLiteError(fmt.Errorf("scan release set local commit selection: %w", err))
	}
	if err := json.Unmarshal([]byte(filesJSON), &selection.Files); err != nil {
		return work.ReleaseSetLocalCommitSelection{}, fmt.Errorf("decode selection files: %w", err)
	}
	if parkedPathsJSON.Valid && parkedPathsJSON.String != "" {
		if err := json.Unmarshal([]byte(parkedPathsJSON.String), &selection.ParkedPaths); err != nil {
			return work.ReleaseSetLocalCommitSelection{}, fmt.Errorf("decode parked paths: %w", err)
		}
	}
	createdAt, err := parseWorkflowTime(createdRaw)
	if err != nil {
		return work.ReleaseSetLocalCommitSelection{}, err
	}
	if restoredRaw.Valid {
		restoredAt, err := parseWorkflowTime(restoredRaw.String)
		if err != nil {
			return work.ReleaseSetLocalCommitSelection{}, err
		}
		selection.RestoredAt = &restoredAt
	}
	selection.ReleaseSetLocalCommitID = work.ReleaseSetLocalCommitID(opID)
	selection.ApprovalRequestID, selection.WorkItemID, selection.RunID = approvalRequestID.String, workItemID.String, runID.String
	selection.Unselected = work.UnselectedHandling(unselected)
	selection.ParkedRef = parkedRef.String
	selection.Patch, selection.PatchTruncated, selection.CreatedAt = patch, truncated == 1, createdAt
	return selection, nil
}

// RecordReleaseSetLocalCommitParked implements ports.WorkRepository.
func (r workRepository) RecordReleaseSetLocalCommitParked(ctx context.Context, id, ref string, paths []string) error {
	pathsJSON, err := json.Marshal(paths)
	if err != nil {
		return fmt.Errorf("marshal parked paths: %w", err)
	}
	result, err := r.tx.ExecContext(ctx, `
UPDATE release_set_local_commit_selections SET parked_ref = ?, parked_paths_json = ?
WHERE release_set_local_commit_id = ?`, ref, string(pathsJSON), id)
	if err != nil {
		return MapSQLiteError(fmt.Errorf("record parked changes of %s: %w", id, err))
	}
	return requireOneSelectionRow(result, id)
}

// MarkReleaseSetLocalCommitParkedRestored implements ports.WorkRepository.
func (r workRepository) MarkReleaseSetLocalCommitParkedRestored(ctx context.Context, id string, at time.Time) error {
	result, err := r.tx.ExecContext(ctx, `
UPDATE release_set_local_commit_selections SET restored_at = ?
WHERE release_set_local_commit_id = ? AND parked_ref IS NOT NULL`, formatWorkflowTime(at), id)
	if err != nil {
		return MapSQLiteError(fmt.Errorf("mark parked changes of %s restored: %w", id, err))
	}
	return requireOneSelectionRow(result, id)
}

func requireOneSelectionRow(result sql.Result, id string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read selection update result: %w", err)
	}
	if affected != 1 {
		return fmt.Errorf("%w: release set local commit selection %s", ports.ErrPersistenceNotFound, id)
	}
	return nil
}

// ListParkedChanges implements ports.WorkRepository.
func (r workRepository) ListParkedChanges(ctx context.Context, familyID string) ([]work.ParkedChange, error) {
	rows, err := r.tx.QueryContext(ctx, `
SELECT sel.release_set_local_commit_id, op.release_set_id, op.repository_workspace_id, op.repository_id,
       sel.parked_ref, sel.parked_paths_json, sel.created_at
FROM release_set_local_commit_selections AS sel
JOIN release_set_local_commits AS op ON op.id = sel.release_set_local_commit_id
JOIN release_sets AS rs ON rs.id = op.release_set_id
WHERE rs.family_id = ? AND sel.parked_ref IS NOT NULL AND sel.restored_at IS NULL
ORDER BY sel.created_at, sel.release_set_local_commit_id`, familyID)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list parked changes: %w", err))
	}
	defer rows.Close()
	var out []work.ParkedChange
	for rows.Next() {
		var change work.ParkedChange
		var opID, releaseSetID, pathsJSON, createdRaw string
		if err := rows.Scan(&opID, &releaseSetID, &change.RepositoryWorkspaceID, &change.RepositoryID, &change.Ref, &pathsJSON, &createdRaw); err != nil {
			return nil, MapSQLiteError(fmt.Errorf("scan parked change: %w", err))
		}
		if err := json.Unmarshal([]byte(pathsJSON), &change.Paths); err != nil {
			return nil, fmt.Errorf("decode parked paths: %w", err)
		}
		parkedAt, err := parseWorkflowTime(createdRaw)
		if err != nil {
			return nil, err
		}
		change.ReleaseSetLocalCommitID, change.ReleaseSetID, change.ParkedAt = work.ReleaseSetLocalCommitID(opID), work.ReleaseSetID(releaseSetID), parkedAt
		out = append(out, change)
	}
	return out, MapSQLiteError(rows.Err())
}
