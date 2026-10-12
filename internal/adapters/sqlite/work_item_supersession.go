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

// This file gives workRepository its WorkItemSupersession methods (V11-10, docs/design/14-v11-bugfix.md, ADR-041): the
// link between a WorkItem and the one that replaced it when the operator redid the task with the newest workflow version.

// RecordWorkItemSupersession implements ports.WorkRepository.
func (r workRepository) RecordWorkItemSupersession(ctx context.Context, supersession work.WorkItemSupersession) (work.WorkItemSupersession, error) {
	return recordWorkItemSupersessionTx(ctx, r.tx, supersession)
}

func recordWorkItemSupersessionTx(ctx context.Context, tx *sql.Tx, supersession work.WorkItemSupersession) (work.WorkItemSupersession, error) {
	for _, id := range []work.WorkItemID{supersession.OldWorkItemID, supersession.NewWorkItemID} {
		var exists int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM work_items WHERE id = ?`, string(id)).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return work.WorkItemSupersession{}, fmt.Errorf("%w: work item %s", ports.ErrPersistenceNotFound, id)
		}
		if err != nil {
			return work.WorkItemSupersession{}, MapSQLiteError(fmt.Errorf("resolve work item supersession work item: %w", err))
		}
	}
	existing, err := loadWorkItemSupersessionTx(ctx, tx, "new_work_item_id", string(supersession.NewWorkItemID))
	switch {
	case err == nil:
		if existing.OldWorkItemID != supersession.OldWorkItemID {
			return work.WorkItemSupersession{}, fmt.Errorf("%w: work item %s already replaces %s", ports.ErrPersistenceAlreadyExists, existing.NewWorkItemID, existing.OldWorkItemID)
		}
		return existing, nil
	case !errors.Is(err, ports.ErrPersistenceNotFound):
		return work.WorkItemSupersession{}, err
	}
	if replaced, err := loadWorkItemSupersessionTx(ctx, tx, "old_work_item_id", string(supersession.OldWorkItemID)); err == nil {
		return work.WorkItemSupersession{}, fmt.Errorf("%w: work item %s is already replaced by %s", ports.ErrPersistenceAlreadyExists, replaced.OldWorkItemID, replaced.NewWorkItemID)
	} else if !errors.Is(err, ports.ErrPersistenceNotFound) {
		return work.WorkItemSupersession{}, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO work_item_supersessions (new_work_item_id, old_work_item_id, project_id, actor, reason, created_at)
VALUES (?, ?, ?, ?, ?, ?)`,
		string(supersession.NewWorkItemID), string(supersession.OldWorkItemID), string(supersession.ProjectID),
		supersession.Actor, supersession.Reason, formatWorkflowTime(supersession.CreatedAt)); err != nil {
		return work.WorkItemSupersession{}, MapSQLiteError(fmt.Errorf("record work item supersession: %w", err))
	}
	return supersession, nil
}

// GetWorkItemSupersessionByOld implements ports.WorkRepository.
func (r workRepository) GetWorkItemSupersessionByOld(ctx context.Context, workItemID string) (work.WorkItemSupersession, error) {
	return loadWorkItemSupersessionTx(ctx, r.tx, "old_work_item_id", workItemID)
}

// GetWorkItemSupersessionByNew implements ports.WorkRepository.
func (r workRepository) GetWorkItemSupersessionByNew(ctx context.Context, workItemID string) (work.WorkItemSupersession, error) {
	return loadWorkItemSupersessionTx(ctx, r.tx, "new_work_item_id", workItemID)
}

// loadWorkItemSupersessionTx reads by one of the two unique columns; column is only ever a literal from this file.
func loadWorkItemSupersessionTx(ctx context.Context, tx *sql.Tx, column, workItemID string) (work.WorkItemSupersession, error) {
	var oldID, newID, projectID, actor, reason, createdAtRaw string
	err := tx.QueryRowContext(ctx, `
SELECT old_work_item_id, new_work_item_id, project_id, actor, reason, created_at
FROM work_item_supersessions WHERE `+column+` = ?`, workItemID).Scan(&oldID, &newID, &projectID, &actor, &reason, &createdAtRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return work.WorkItemSupersession{}, fmt.Errorf("%w: work item supersession for work item %s", ports.ErrPersistenceNotFound, workItemID)
	}
	if err != nil {
		return work.WorkItemSupersession{}, MapSQLiteError(fmt.Errorf("load work item supersession: %w", err))
	}
	createdAt, err := parseWorkflowTime(createdAtRaw)
	if err != nil {
		return work.WorkItemSupersession{}, err
	}
	return work.WorkItemSupersession{
		ProjectID: project.ProjectID(projectID), OldWorkItemID: work.WorkItemID(oldID), NewWorkItemID: work.WorkItemID(newID),
		Actor: actor, Reason: reason, CreatedAt: createdAt,
	}, nil
}

// ListWorkItemSupersessions implements ports.WorkRepository.
func (r workRepository) ListWorkItemSupersessions(ctx context.Context, projectID string) ([]work.WorkItemSupersession, error) {
	rows, err := r.tx.QueryContext(ctx, `
SELECT old_work_item_id, new_work_item_id, project_id, actor, reason, created_at
FROM work_item_supersessions WHERE project_id = ? ORDER BY new_work_item_id`, projectID)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list work item supersessions for project %s: %w", projectID, err))
	}
	defer rows.Close()
	var out []work.WorkItemSupersession
	for rows.Next() {
		var oldID, newID, projectColumn, actor, reason, createdAtRaw string
		if err := rows.Scan(&oldID, &newID, &projectColumn, &actor, &reason, &createdAtRaw); err != nil {
			return nil, fmt.Errorf("scan work item supersession: %w", err)
		}
		createdAt, err := parseWorkflowTime(createdAtRaw)
		if err != nil {
			return nil, err
		}
		out = append(out, work.WorkItemSupersession{
			ProjectID: project.ProjectID(projectColumn), OldWorkItemID: work.WorkItemID(oldID), NewWorkItemID: work.WorkItemID(newID),
			Actor: actor, Reason: reason, CreatedAt: createdAt,
		})
	}
	return out, rows.Err()
}
