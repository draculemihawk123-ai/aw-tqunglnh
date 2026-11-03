package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// budgetRepository implements ports.BudgetRepository over work_item_budgets (V12-23, migration 0059).
type budgetRepository struct {
	tx *sql.Tx
}

const budgetColumns = `work_item_id, project_id, budget_usd, version, updated_by, updated_at`

func (r budgetRepository) GetWorkItemBudget(ctx context.Context, workItemID string) (ports.WorkItemBudget, error) {
	b, err := scanBudget(r.tx.QueryRowContext(ctx, `SELECT `+budgetColumns+` FROM work_item_budgets WHERE work_item_id = ?`, workItemID))
	if errors.Is(err, sql.ErrNoRows) {
		return ports.WorkItemBudget{}, fmt.Errorf("%w: budget of %s", ports.ErrPersistenceNotFound, workItemID)
	}
	return b, err
}

func (r budgetRepository) PutWorkItemBudget(ctx context.Context, b ports.WorkItemBudget, expectedVersion uint64) (ports.WorkItemBudget, error) {
	existing, err := r.GetWorkItemBudget(ctx, b.WorkItemID)
	switch {
	case errors.Is(err, ports.ErrPersistenceNotFound):
		if expectedVersion != 0 {
			return ports.WorkItemBudget{}, fmt.Errorf("%w: budget of %s", ports.ErrOptimisticConflict, b.WorkItemID)
		}
		b.Version = 1
		_, err := r.tx.ExecContext(ctx, `INSERT INTO work_item_budgets (`+budgetColumns+`) VALUES (?, ?, ?, ?, ?, ?)`,
			b.WorkItemID, b.ProjectID, b.BudgetUSD, b.Version, b.UpdatedBy, formatWorkflowTime(b.UpdatedAt))
		if err != nil {
			return ports.WorkItemBudget{}, MapSQLiteError(fmt.Errorf("insert budget: %w", err))
		}
		return b, nil
	case err != nil:
		return ports.WorkItemBudget{}, err
	}
	if existing.Version != expectedVersion {
		return ports.WorkItemBudget{}, fmt.Errorf("%w: budget of %s is at version %d", ports.ErrOptimisticConflict, b.WorkItemID, existing.Version)
	}
	b.Version = existing.Version + 1
	if _, err := r.tx.ExecContext(ctx, `UPDATE work_item_budgets SET budget_usd = ?, version = ?, updated_by = ?, updated_at = ? WHERE work_item_id = ? AND version = ?`,
		b.BudgetUSD, b.Version, b.UpdatedBy, formatWorkflowTime(b.UpdatedAt), b.WorkItemID, expectedVersion); err != nil {
		return ports.WorkItemBudget{}, MapSQLiteError(fmt.Errorf("update budget: %w", err))
	}
	return b, nil
}

func (r budgetRepository) DeleteWorkItemBudget(ctx context.Context, workItemID string) error {
	if _, err := r.tx.ExecContext(ctx, `DELETE FROM work_item_budgets WHERE work_item_id = ?`, workItemID); err != nil {
		return MapSQLiteError(fmt.Errorf("delete budget: %w", err))
	}
	return nil
}

func (r budgetRepository) ListWorkItemBudgetsForProject(ctx context.Context, projectID string) ([]ports.WorkItemBudget, error) {
	rows, err := r.tx.QueryContext(ctx, `SELECT `+budgetColumns+` FROM work_item_budgets WHERE project_id = ? ORDER BY work_item_id`, projectID)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list budgets: %w", err))
	}
	defer rows.Close()
	var out []ports.WorkItemBudget
	for rows.Next() {
		b, err := scanBudget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func scanBudget(row repositoryRowScanner) (ports.WorkItemBudget, error) {
	var b ports.WorkItemBudget
	var updatedAt string
	if err := row.Scan(&b.WorkItemID, &b.ProjectID, &b.BudgetUSD, &b.Version, &b.UpdatedBy, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ports.WorkItemBudget{}, err
		}
		return ports.WorkItemBudget{}, MapSQLiteError(fmt.Errorf("scan budget: %w", err))
	}
	var err error
	b.UpdatedAt, err = parseWorkflowTime(updatedAt)
	return b, err
}
