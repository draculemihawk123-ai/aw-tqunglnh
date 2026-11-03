package runtime_test

// V12-23 (ADR-057): spend by task and batch from the provider's reported usage; a batch budget stops the family queue.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

func (f *rerunSQLite) reportUsage(attemptID string, costUSD float64) {
	f.t.Helper()
	if err := f.uow.WithSerializedWrite(f.ctx, func(tx ports.Tx) error {
		return tx.AgentEvents().AppendBatch(f.ctx, []ports.AgentEventRecord{{
			ID: "usage-" + attemptID, AttemptID: attemptID, Sequence: 1, Kind: string(ports.AgentEventUsageReported), SchemaVersion: 1,
			PayloadJSON: fmt.Sprintf(`{"observedAt":"2026-11-03T10:00:00Z","usage":{"inputTokens":1000,"outputTokens":200,"costUsd":%g}}`, costUSD),
			CreatedAt:   time.Now().UTC(),
		}})
	}); err != nil {
		f.t.Fatalf("append usage: %v", err)
	}
}

func TestProjectCosts_SumByTaskAndBatchAndABudgetStopsTheQueue(t *testing.T) {
	f, rootID := newRecreateSQLite(t)
	child := f.workItemID
	started := f.mustStart("idem-start-cost")
	attempt := f.runToRunningAttempt(started)
	f.reportUsage(attempt.attemptID, 1.5)

	costs, err := runtime.GetProjectCosts(context.Background(), f.uow, redact.NewMatcher(), "project-1")
	if err != nil {
		t.Fatalf("GetProjectCosts: %v", err)
	}
	if len(costs.Batches) != 1 || costs.Batches[0].RootWorkItemID != rootID || costs.Batches[0].Usage.CostUSD != 1.5 || costs.Usage.CostUSD != 1.5 {
		t.Fatalf("costs = %+v, want one batch (the root) that spent 1.5 USD", costs)
	}
	var childCost *runtime.TaskCost
	for i := range costs.Batches[0].Tasks {
		if costs.Batches[0].Tasks[i].WorkItemID == child {
			childCost = &costs.Batches[0].Tasks[i]
		}
	}
	if childCost == nil || childCost.Usage == nil || childCost.Usage.CostUSD != 1.5 || childCost.Usage.InputTokens != 1000 || childCost.RunCount != 1 {
		t.Fatalf("child cost = %+v, want 1.5 USD over one run", childCost)
	}

	one := 1.0
	if _, err := runtime.SetWorkItemBudget(f.ctx, f.uow, redoCommand("idem-budget-child", "SetWorkItemBudget"), "project-1", child, &one); !errors.Is(err, runtime.ErrBudgetNotRoot) {
		t.Fatalf("budget on a child: err = %v, want ErrBudgetNotRoot", err)
	}
	if view, err := runtime.SetWorkItemBudget(f.ctx, f.uow, redoCommand("idem-budget", "SetWorkItemBudget"), "project-1", rootID, &one); err != nil || view.BudgetUSD == nil || *view.BudgetUSD != 1 {
		t.Fatalf("SetWorkItemBudget = %+v, %v", view, err)
	}
	costs, _ = runtime.GetProjectCosts(context.Background(), f.uow, redact.NewMatcher(), "project-1")
	if !costs.Batches[0].OverBudget || costs.Batches[0].BudgetUSD == nil {
		t.Fatalf("batch = %+v, want over its 1 USD budget", costs.Batches[0])
	}

	// The first task ends; the next queued one does not start while the batch is over budget.
	f.setRunState(started.RunID, runtimedomain.WorkflowRunFailed)
	f.setStatus(child, workdomain.WorkItemDone)
	second := f.readyChild("cost-2", "T-02: second")
	f.queue("cost-2", second, "")
	report := f.sweepQueues()
	if len(report.Started) != 0 {
		t.Fatalf("the queue started %v over budget", report.Started)
	}
	family := queueFamily(t, f, second)
	if !strings.Contains(report.Waiting[family], "budget") {
		t.Fatalf("waiting = %q, want the budget named", report.Waiting[family])
	}
	// Removing the budget lets it go on.
	if _, err := runtime.SetWorkItemBudget(f.ctx, f.uow, redoCommand("idem-budget-off", "SetWorkItemBudget"), "project-1", rootID, nil); err != nil {
		t.Fatalf("remove budget: %v", err)
	}
	if report := f.sweepQueues(); len(report.Started) != 1 || report.Started[0] != second {
		t.Fatalf("after removing the budget the queue started %v, want %s", report.Started, second)
	}
}
