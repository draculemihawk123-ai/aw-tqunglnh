package ports

import (
	"context"
	"time"
)

// WorkItemBudget is the spending cap of a root work item and every task under it (V12-23, ADR-057), in US dollars as the provider
// CLI reports cost.
type WorkItemBudget struct {
	WorkItemID string
	ProjectID  string
	BudgetUSD  float64
	Version    uint64
	UpdatedBy  string
	UpdatedAt  time.Time
}

// BudgetRepository is the Tx accessor of work item budgets.
type BudgetRepository interface {
	// GetWorkItemBudget returns the budget, or ErrPersistenceNotFound when the work item has none.
	GetWorkItemBudget(ctx context.Context, workItemID string) (WorkItemBudget, error)
	// PutWorkItemBudget inserts (expectedVersion 0) or replaces (expectedVersion = the stored version, ErrOptimisticConflict
	// otherwise) the budget, returning the stored row.
	PutWorkItemBudget(ctx context.Context, budget WorkItemBudget, expectedVersion uint64) (WorkItemBudget, error)
	// DeleteWorkItemBudget removes the budget (no error when there is none).
	DeleteWorkItemBudget(ctx context.Context, workItemID string) error
	// ListWorkItemBudgetsForProject returns every budget of the project.
	ListWorkItemBudgetsForProject(ctx context.Context, projectID string) ([]WorkItemBudget, error)
}
