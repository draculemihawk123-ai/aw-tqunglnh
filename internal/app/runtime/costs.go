// V12-23 (docs/design/15-v12-ui-operations.md, UX-35, UX-36, ADR-057): what the work of a project cost, by task, by batch (a root
// work item and every task under it) and in all, from what the provider CLI reported (USAGE_REPORTED agent events; aw's figures are
// the CLI's, not a bill); a spending cap per batch; and the provider usage limits that are hit right now (an open
// PROVIDER_QUOTA_EXCEEDED blocker and when aw resumes). The remaining quota is not read: the Claude CLI's rate_limit_event lines are
// recorded without their fields (their shape is unverified), so a limit is known when it is hit, with its reset time — not before.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/agentevents"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// BudgetWarnRatio: a batch that spent this share of its budget is "near" it.
const BudgetWarnRatio = 0.8

// Errors of SetWorkItemBudget.
var (
	ErrBudgetNotRoot = errors.New("runtime: a budget is set on a root work item (the batch)")
	ErrBudgetInvalid = errors.New("runtime: a budget must be a positive amount of US dollars")
)

// TaskCost is one work item's spend.
type TaskCost struct {
	WorkItemID string     `json:"workItemId"`
	Title      string     `json:"title"`
	Status     string     `json:"status"`
	RunCount   int        `json:"runCount"`
	Usage      *UsageView `json:"usage,omitempty"`
}

// BatchCost is a root work item and every task under it.
type BatchCost struct {
	RootWorkItemID string     `json:"rootWorkItemId"`
	Title          string     `json:"title"`
	Usage          UsageView  `json:"usage"`
	BudgetUSD      *float64   `json:"budgetUsd,omitempty"`
	BudgetVersion  uint64     `json:"budgetVersion,omitempty"`
	OverBudget     bool       `json:"overBudget"`
	NearBudget     bool       `json:"nearBudget"`
	Tasks          []TaskCost `json:"tasks"`
}

// ProviderLimit is a provider usage limit hit right now: an open PROVIDER_QUOTA_EXCEEDED blocker.
type ProviderLimit struct {
	WorkItemID string `json:"workItemId"`
	Title      string `json:"title"`
	NodeRunID  string `json:"nodeRunId,omitempty"`
	Message    string `json:"message"`
}

// ProjectCosts is the cost page.
type ProjectCosts struct {
	ProjectID      string          `json:"projectId"`
	Usage          UsageView       `json:"usage"`
	Batches        []BatchCost     `json:"batches"`
	ProviderLimits []ProviderLimit `json:"providerLimits"`
}

// workItemUsageTx sums what every provider attempt of every run of the work item reported.
func workItemUsageTx(ctx context.Context, tx ports.Tx, workItemID string) (ports.AgentUsage, bool, int, error) {
	var total ports.AgentUsage
	known := false
	runs, err := tx.Runtime().ListWorkflowRunsForWorkItem(ctx, workItemID)
	if err != nil {
		return total, false, 0, err
	}
	for _, run := range runs {
		attempts, err := tx.Runtime().ListExecutionAttemptsForRun(ctx, string(run.ID))
		if err != nil {
			return total, false, 0, err
		}
		for _, a := range attempts {
			if a.ProviderKey == "" {
				continue
			}
			records, err := tx.AgentEvents().ListByAttemptAndKind(ctx, string(a.ID), string(ports.AgentEventUsageReported))
			if err != nil {
				return total, false, 0, err
			}
			usage, ok := agentevents.UsageFromRecords(records)
			if !ok {
				continue
			}
			known = true
			total.InputTokens += usage.InputTokens
			total.CachedInputTokens += usage.CachedInputTokens
			total.OutputTokens += usage.OutputTokens
			total.CostUSD += usage.CostUSD
		}
	}
	return total, known, len(runs), nil
}

func addUsage(sum *UsageView, u ports.AgentUsage) {
	sum.InputTokens += u.InputTokens
	sum.CachedInputTokens += u.CachedInputTokens
	sum.OutputTokens += u.OutputTokens
	sum.CostUSD += u.CostUSD
}

// rootOf walks a work item up to its root.
func rootOf(items map[string]workdomain.WorkItem, id string) string {
	for i := 0; i < 64; i++ {
		item, ok := items[id]
		if !ok || item.ParentID == nil {
			return id
		}
		id = string(*item.ParentID)
	}
	return id
}

// GetProjectCosts reads the cost page of projectID.
func GetProjectCosts(ctx context.Context, uow ports.UnitOfWork, matcher redact.Matcher, projectID string) (ProjectCosts, error) {
	out := ProjectCosts{ProjectID: projectID, Batches: []BatchCost{}, ProviderLimits: []ProviderLimit{}}
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		if _, err := tx.Catalog().GetProject(ctx, projectID); err != nil {
			return err
		}
		items, err := tx.Work().ListWorkItemsByProject(ctx, projectID)
		if err != nil {
			return err
		}
		byID := map[string]workdomain.WorkItem{}
		for _, item := range items {
			byID[string(item.ID)] = item
		}
		budgets, err := tx.Budgets().ListWorkItemBudgetsForProject(ctx, projectID)
		if err != nil {
			return err
		}
		budgetOf := map[string]ports.WorkItemBudget{}
		for _, b := range budgets {
			budgetOf[b.WorkItemID] = b
		}
		batches := map[string]*BatchCost{}
		var order []string
		for _, item := range items {
			root := rootOf(byID, string(item.ID))
			batch, ok := batches[root]
			if !ok {
				batch = &BatchCost{RootWorkItemID: root, Title: byID[root].Title, Tasks: []TaskCost{}}
				if b, ok := budgetOf[root]; ok {
					usd := b.BudgetUSD
					batch.BudgetUSD, batch.BudgetVersion = &usd, b.Version
				}
				batches[root] = batch
				order = append(order, root)
			}
			usage, known, runs, err := workItemUsageTx(ctx, tx, string(item.ID))
			if err != nil {
				return err
			}
			task := TaskCost{WorkItemID: string(item.ID), Title: item.Title, Status: string(item.Status), RunCount: runs}
			if known {
				task.Usage = usageView(usage)
				addUsage(&batch.Usage, usage)
				addUsage(&out.Usage, usage)
			}
			batch.Tasks = append(batch.Tasks, task)

			blockers, err := tx.Work().ListWorkItemBlockersForWorkItem(ctx, string(item.ID))
			if err != nil {
				return err
			}
			for _, b := range blockers {
				if b.State == workdomain.BlockerOpen && b.Type == workdomain.BlockerProviderQuotaExceeded {
					out.ProviderLimits = append(out.ProviderLimits, ProviderLimit{
						WorkItemID: string(item.ID), Title: item.Title, NodeRunID: b.SourceNodeRunID, Message: bound(redactText(matcher, b.Reason)),
					})
				}
			}
		}
		for _, root := range order {
			batch := batches[root]
			if batch.BudgetUSD != nil {
				batch.OverBudget = batch.Usage.CostUSD >= *batch.BudgetUSD
				batch.NearBudget = !batch.OverBudget && batch.Usage.CostUSD >= *batch.BudgetUSD*BudgetWarnRatio
			}
			sort.SliceStable(batch.Tasks, func(i, j int) bool { return costOf(batch.Tasks[i]) > costOf(batch.Tasks[j]) })
			out.Batches = append(out.Batches, *batch)
		}
		sort.SliceStable(out.Batches, func(i, j int) bool { return out.Batches[i].Usage.CostUSD > out.Batches[j].Usage.CostUSD })
		return nil
	})
	return out, err
}

func costOf(t TaskCost) float64 {
	if t.Usage == nil {
		return 0
	}
	return t.Usage.CostUSD
}

// BudgetView is a root work item's budget after SetWorkItemBudget (BudgetUSD nil: removed).
type BudgetView struct {
	WorkItemID string   `json:"workItemId"`
	BudgetUSD  *float64 `json:"budgetUsd,omitempty"`
	Version    uint64   `json:"version,omitempty"`
}

// SetWorkItemBudget sets (budgetUSD > 0) or removes (nil) the budget of a root work item, idempotently by cmd's receipt.
func SetWorkItemBudget(ctx context.Context, uow ports.UnitOfWork, cmd ports.Command, projectID, workItemID string, budgetUSD *float64) (BudgetView, error) {
	if budgetUSD != nil && (*budgetUSD <= 0 || math.IsNaN(*budgetUSD) || math.IsInf(*budgetUSD, 0)) {
		return BudgetView{}, ErrBudgetInvalid
	}
	var result BudgetView
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		receipt, found, err := tx.Receipts().Load(ctx, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
		if err != nil {
			return err
		}
		if found {
			if receipt.RequestHash != cmd.RequestHash {
				return ports.ErrReceiptConflict
			}
			return json.Unmarshal([]byte(receipt.ResultJSON), &result)
		}
		item, err := tx.Work().GetWorkItem(ctx, workItemID)
		if err != nil {
			return err
		}
		if string(item.ProjectID) != projectID {
			return fmt.Errorf("%w: work item %s", ports.ErrPersistenceNotFound, workItemID)
		}
		if item.ParentID != nil {
			return ErrBudgetNotRoot
		}
		existing, err := tx.Budgets().GetWorkItemBudget(ctx, workItemID)
		var expected uint64
		switch {
		case err == nil:
			expected = existing.Version
		case !errors.Is(err, ports.ErrPersistenceNotFound):
			return err
		}
		result = BudgetView{WorkItemID: workItemID}
		if budgetUSD == nil {
			if err := tx.Budgets().DeleteWorkItemBudget(ctx, workItemID); err != nil {
				return err
			}
		} else {
			stored, err := tx.Budgets().PutWorkItemBudget(ctx, ports.WorkItemBudget{
				WorkItemID: workItemID, ProjectID: projectID, BudgetUSD: *budgetUSD, UpdatedBy: cmd.Actor, UpdatedAt: cmd.RequestedAt,
			}, expected)
			if err != nil {
				return err
			}
			usd := stored.BudgetUSD
			result.BudgetUSD, result.Version = &usd, stored.Version
		}
		resultJSON, err := json.Marshal(result)
		if err != nil {
			return err
		}
		return tx.Receipts().Record(ctx, ports.Receipt{
			Actor: cmd.Actor, Scope: cmd.Scope, IdempotencyKey: cmd.IdempotencyKey, CommandType: cmd.Type,
			RequestHash: cmd.RequestHash, ResultJSON: string(resultJSON), CreatedAt: cmd.RequestedAt,
		})
	})
	return result, err
}

// queueSpendingStopTx says why the family queue must not start another task for spending reasons: the batch is over its budget, or a
// provider usage limit is hit in the project (another task would hit it too). Empty when the queue may go on.
func queueSpendingStopTx(ctx context.Context, tx ports.Tx, projectID, familyID string) (string, error) {
	family, err := tx.Work().GetTaskFamily(ctx, familyID)
	if err != nil {
		return "", err
	}
	root := string(family.RootWorkItemID)
	if budget, err := tx.Budgets().GetWorkItemBudget(ctx, root); err == nil {
		items, err := tx.Work().ListWorkItemsByProject(ctx, projectID)
		if err != nil {
			return "", err
		}
		byID := map[string]workdomain.WorkItem{}
		for _, item := range items {
			byID[string(item.ID)] = item
		}
		var spent float64
		for _, item := range items {
			if rootOf(byID, string(item.ID)) != root {
				continue
			}
			usage, _, _, err := workItemUsageTx(ctx, tx, string(item.ID))
			if err != nil {
				return "", err
			}
			spent += usage.CostUSD
		}
		if spent >= budget.BudgetUSD {
			return fmt.Sprintf("paused: the batch spent %.2f USD of its %.2f USD budget; raise or remove the budget to go on", spent, budget.BudgetUSD), nil
		}
	} else if !errors.Is(err, ports.ErrPersistenceNotFound) {
		return "", err
	}
	// A provider account's limit is the installation's, not the project's: any open one stops every queue.
	blockers, err := tx.Work().ListOpenBlockersOfType(ctx, workdomain.BlockerProviderQuotaExceeded, 1)
	if err != nil {
		return "", err
	}
	if len(blockers) > 0 {
		return "paused: a provider usage limit is hit (" + strings.TrimSpace(firstLine(blockers[0].Reason, 200)) + "); the queue goes on after it resets", nil
	}
	return "", nil
}

