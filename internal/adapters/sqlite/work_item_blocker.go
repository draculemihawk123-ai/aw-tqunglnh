package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// This file gives workRepository its WorkItemBlocker methods (V4-12C,
// docs/design/06-v4-runtime-engine.md; ADR-020) — the durable, typed reason
// a WorkItem sits BLOCKED, and CancelWorkItem/ResolveWorkItemBlocker's own
// persistence surface. Follows work.go's own Tx-composable pattern exactly:
// one xxxTx(ctx, tx *sql.Tx, ...) function per operation, plus a thin
// workRepository method that forwards to it.

// CreateWorkItemBlocker implements ports.WorkRepository (V4-12C). Idempotent
// by ID: a duplicate insert (the same deterministic ID a real producer
// mints from its own originating aggregate — see
// ports.WorkRepository.CreateWorkItemBlocker's own doc comment) returns the
// already-stored row rather than erroring, the identical
// insert-then-load-on-conflict discipline recordWorkItemCancellationIntentTx
// already establishes.
func (r workRepository) CreateWorkItemBlocker(ctx context.Context, blocker work.WorkItemBlocker) (work.WorkItemBlocker, error) {
	return createWorkItemBlockerTx(ctx, r.tx, blocker)
}

func createWorkItemBlockerTx(ctx context.Context, tx *sql.Tx, blocker work.WorkItemBlocker) (work.WorkItemBlocker, error) {
	var workItemExists int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM work_items WHERE id = ?`, string(blocker.WorkItemID)).Scan(&workItemExists)
	if errors.Is(err, sql.ErrNoRows) {
		return work.WorkItemBlocker{}, fmt.Errorf("%w: work item %s", ports.ErrPersistenceNotFound, blocker.WorkItemID)
	}
	if err != nil {
		return work.WorkItemBlocker{}, MapSQLiteError(fmt.Errorf("resolve work item blocker work item: %w", err))
	}

	nullable := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}

	_, insertErr := tx.ExecContext(ctx, `
INSERT INTO blockers (id, project_id, work_item_id, type, state, source_run_id, source_node_run_id, source_attempt_id, reason, opened_at, version)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(blocker.ID), string(blocker.ProjectID), string(blocker.WorkItemID), string(blocker.Type), string(blocker.State),
		nullable(blocker.SourceRunID), nullable(blocker.SourceNodeRunID), nullable(blocker.SourceAttemptID),
		blocker.Reason, formatWorkflowTime(blocker.OpenedAt), blocker.Version,
	)
	if insertErr == nil {
		return blocker, nil
	}
	existing, loadErr := loadWorkItemBlockerTx(ctx, tx, string(blocker.ID))
	if loadErr != nil {
		return work.WorkItemBlocker{}, MapSQLiteError(fmt.Errorf("create work item blocker: %w", insertErr))
	}
	return existing, nil
}

// GetWorkItemBlocker implements ports.WorkRepository.
func (r workRepository) GetWorkItemBlocker(ctx context.Context, id string) (work.WorkItemBlocker, error) {
	return loadWorkItemBlockerTx(ctx, r.tx, id)
}

func loadWorkItemBlockerTx(ctx context.Context, tx *sql.Tx, id string) (work.WorkItemBlocker, error) {
	row := tx.QueryRowContext(ctx, `
SELECT id, project_id, work_item_id, type, state, source_run_id, source_node_run_id, source_attempt_id,
       reason, opened_at, resolved_at, resolved_by, resolution_note, decision_artifact_id, version
FROM blockers WHERE id = ?`, id)
	blocker, err := scanWorkItemBlockerRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return work.WorkItemBlocker{}, fmt.Errorf("%w: work item blocker %s", ports.ErrPersistenceNotFound, id)
	}
	return blocker, err
}

func scanWorkItemBlockerRow(row repositoryRowScanner) (work.WorkItemBlocker, error) {
	var id, projectID, workItemID, blockerType, state, reason, openedAtRaw string
	var sourceRunID, sourceNodeRunID, sourceAttemptID, resolvedAtRaw, resolvedBy, resolutionNote, decisionArtifactID sql.NullString
	var version uint64
	if err := row.Scan(
		&id, &projectID, &workItemID, &blockerType, &state, &sourceRunID, &sourceNodeRunID, &sourceAttemptID,
		&reason, &openedAtRaw, &resolvedAtRaw, &resolvedBy, &resolutionNote, &decisionArtifactID, &version,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return work.WorkItemBlocker{}, err
		}
		return work.WorkItemBlocker{}, MapSQLiteError(fmt.Errorf("scan work item blocker row: %w", err))
	}
	openedAt, err := parseWorkflowTime(openedAtRaw)
	if err != nil {
		return work.WorkItemBlocker{}, err
	}
	blocker := work.WorkItemBlocker{
		ID: work.BlockerID(id), ProjectID: project.ProjectID(projectID), WorkItemID: work.WorkItemID(workItemID),
		Type: work.BlockerType(blockerType), State: work.BlockerState(state), SourceRunID: sourceRunID.String,
		SourceNodeRunID: sourceNodeRunID.String, SourceAttemptID: sourceAttemptID.String, Reason: reason,
		OpenedAt: openedAt, ResolvedBy: resolvedBy.String, ResolutionNote: resolutionNote.String,
		DecisionArtifactID: decisionArtifactID.String, Version: version,
	}
	if resolvedAtRaw.Valid {
		resolvedAt, err := parseWorkflowTime(resolvedAtRaw.String)
		if err != nil {
			return work.WorkItemBlocker{}, err
		}
		blocker.ResolvedAt = &resolvedAt
	}
	return blocker, nil
}

// ListWorkItemBlockersForWorkItem implements ports.WorkRepository (V4-12C),
// ordered by (opened_at, id) for a stable, deterministic result a test can
// assert on exactly.
func (r workRepository) ListWorkItemBlockersForWorkItem(ctx context.Context, workItemID string) ([]work.WorkItemBlocker, error) {
	rows, err := r.tx.QueryContext(ctx, `SELECT id FROM blockers WHERE work_item_id = ? ORDER BY opened_at, id`, workItemID)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list work item blockers for work item %s: %w", workItemID, err))
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan work item blocker id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate work item blocker ids: %w", err)
	}
	rows.Close()

	blockers := make([]work.WorkItemBlocker, 0, len(ids))
	for _, id := range ids {
		blocker, err := loadWorkItemBlockerTx(ctx, r.tx, id)
		if err != nil {
			return nil, err
		}
		blockers = append(blockers, blocker)
	}
	return blockers, nil
}

// TransitionWorkItemBlockerState implements ports.WorkRepository (V4-12C):
// the fenced CAS that closes a blocker's own lifecycle — OPEN -> RESOLVED or
// OPEN -> WAIVED.
func (r workRepository) TransitionWorkItemBlockerState(ctx context.Context, req ports.TransitionWorkItemBlockerStateRequest) (work.WorkItemBlocker, error) {
	var resolutionNote, decisionArtifactID any
	if req.ResolutionNote != "" {
		resolutionNote = req.ResolutionNote
	}
	if req.DecisionArtifactID != "" {
		decisionArtifactID = req.DecisionArtifactID
	}
	result, err := r.tx.ExecContext(ctx, `
UPDATE blockers
SET state = ?, resolved_at = ?, resolved_by = ?, resolution_note = ?, decision_artifact_id = ?, version = version + 1
WHERE id = ? AND state = ? AND version = ?`,
		string(req.NextState), formatWorkflowTime(req.ResolvedAt), req.ResolvedBy, resolutionNote, decisionArtifactID,
		req.BlockerID, string(req.ExpectedState), req.ExpectedVersion,
	)
	if err != nil {
		return work.WorkItemBlocker{}, MapSQLiteError(fmt.Errorf("transition work item blocker %s: %w", req.BlockerID, err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return work.WorkItemBlocker{}, fmt.Errorf("read work item blocker transition result: %w", err)
	}
	if affected != 1 {
		var exists int
		lookupErr := r.tx.QueryRowContext(ctx, `SELECT 1 FROM blockers WHERE id = ?`, req.BlockerID).Scan(&exists)
		if errors.Is(lookupErr, sql.ErrNoRows) {
			return work.WorkItemBlocker{}, fmt.Errorf("%w: work item blocker %s", ports.ErrPersistenceNotFound, req.BlockerID)
		}
		if lookupErr != nil {
			return work.WorkItemBlocker{}, MapSQLiteError(fmt.Errorf("check stale work item blocker transition: %w", lookupErr))
		}
		return work.WorkItemBlocker{}, fmt.Errorf(
			"%w: work item blocker %s expected %s@%d", ports.ErrOptimisticConflict, req.BlockerID, req.ExpectedState, req.ExpectedVersion,
		)
	}
	return loadWorkItemBlockerTx(ctx, r.tx, req.BlockerID)
}
