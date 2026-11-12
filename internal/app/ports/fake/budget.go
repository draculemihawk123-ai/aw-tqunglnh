package fake

import (
	"context"
	"fmt"
	"sort"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// BudgetRepository is the in-memory ports.BudgetRepository (V12-23).
type BudgetRepository struct {
	budgets map[string]ports.WorkItemBudget
}

func (r *BudgetRepository) clone() *BudgetRepository {
	budgets := make(map[string]ports.WorkItemBudget, len(r.budgets))
	for k, v := range r.budgets {
		budgets[k] = v
	}
	return &BudgetRepository{budgets: budgets}
}

// GetWorkItemBudget mirrors the SQLite repository.
func (r *BudgetRepository) GetWorkItemBudget(_ context.Context, workItemID string) (ports.WorkItemBudget, error) {
	b, ok := r.budgets[workItemID]
	if !ok {
		return ports.WorkItemBudget{}, fmt.Errorf("fake: %w: budget of %s", ports.ErrPersistenceNotFound, workItemID)
	}
	return b, nil
}

// PutWorkItemBudget mirrors the SQLite repository.
func (r *BudgetRepository) PutWorkItemBudget(_ context.Context, b ports.WorkItemBudget, expectedVersion uint64) (ports.WorkItemBudget, error) {
	if r.budgets == nil {
		r.budgets = map[string]ports.WorkItemBudget{}
	}
	existing, ok := r.budgets[b.WorkItemID]
	if (!ok && expectedVersion != 0) || (ok && existing.Version != expectedVersion) {
		return ports.WorkItemBudget{}, fmt.Errorf("fake: %w: budget of %s", ports.ErrOptimisticConflict, b.WorkItemID)
	}
	b.Version = expectedVersion + 1
	r.budgets[b.WorkItemID] = b
	return b, nil
}

// DeleteWorkItemBudget mirrors the SQLite repository.
func (r *BudgetRepository) DeleteWorkItemBudget(_ context.Context, workItemID string) error {
	delete(r.budgets, workItemID)
	return nil
}

// ListWorkItemBudgetsForProject mirrors the SQLite repository.
func (r *BudgetRepository) ListWorkItemBudgetsForProject(_ context.Context, projectID string) ([]ports.WorkItemBudget, error) {
	var out []ports.WorkItemBudget
	for _, b := range r.budgets {
		if b.ProjectID == projectID {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].WorkItemID < out[j].WorkItemID })
	return out, nil
}
