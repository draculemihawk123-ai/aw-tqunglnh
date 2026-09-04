package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// GetWorkflowRun implements ports.RuntimeRepository (V4-03): reuses
// workflow_store.go's own loadWorkflowRun (Store.LoadWorkflowRun's own
// unexported half), composed here against the given Tx instead of Store's
// own connection pool — the same "reuse the spike-era loader, compose it
// against r.tx" discipline definitions.go's own GetWorkflowVersion already
// follows for loadWorkflowVersion.
func (r runtimeRepository) GetWorkflowRun(ctx context.Context, id string) (runtime.WorkflowRun, error) {
	return loadWorkflowRun(ctx, r.tx, runtime.WorkflowRunID(id))
}

// GetNodeRun implements ports.RuntimeRepository (V4-03): reuses
// node_dispatch.go's own loadNodeRunByID (already *sql.Tx-scoped, unlike
// loadWorkflowRun's Store-or-Tx-polymorphic queryer) directly.
func (r runtimeRepository) GetNodeRun(ctx context.Context, id string) (runtime.NodeRun, error) {
	return loadNodeRunByID(ctx, r.tx, runtime.NodeRunID(id))
}

// TransitionNodeRun implements ports.RuntimeRepository (V4-03): the CAS
// that closes a NodeRun's own routing decision, the node_runs counterpart
// of work.go's own transitionWorkItemStatusTx.
func (r runtimeRepository) TransitionNodeRun(ctx context.Context, req ports.TransitionNodeRunRequest) (runtime.NodeRun, error) {
	return transitionNodeRunTx(ctx, r.tx, req)
}

func transitionNodeRunTx(ctx context.Context, tx *sql.Tx, req ports.TransitionNodeRunRequest) (runtime.NodeRun, error) {
	if req.NodeRunID == "" {
		return runtime.NodeRun{}, errors.New("node run id is required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var selectedOutcome any
	if req.SelectedOutcome != "" {
		selectedOutcome = req.SelectedOutcome
	}
	result, err := tx.ExecContext(ctx, `
UPDATE node_runs
SET state = ?, selected_outcome = COALESCE(?, selected_outcome), version = version + 1, updated_at = ?
WHERE id = ? AND state = ? AND version = ?`,
		string(req.NextState), selectedOutcome, now, req.NodeRunID, string(req.ExpectedState), req.ExpectedVersion,
	)
	if err != nil {
		return runtime.NodeRun{}, MapSQLiteError(fmt.Errorf("transition node run %s: %w", req.NodeRunID, err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return runtime.NodeRun{}, fmt.Errorf("read node run transition result: %w", err)
	}
	if affected != 1 {
		var exists int
		lookupErr := tx.QueryRowContext(ctx, `SELECT 1 FROM node_runs WHERE id = ?`, req.NodeRunID).Scan(&exists)
		if errors.Is(lookupErr, sql.ErrNoRows) {
			return runtime.NodeRun{}, fmt.Errorf("%w: node run %s", ports.ErrPersistenceNotFound, req.NodeRunID)
		}
		if lookupErr != nil {
			return runtime.NodeRun{}, MapSQLiteError(fmt.Errorf("check stale node run transition: %w", lookupErr))
		}
		return runtime.NodeRun{}, fmt.Errorf(
			"%w: node run %s expected %s@%d",
			ports.ErrOptimisticConflict, req.NodeRunID, req.ExpectedState, req.ExpectedVersion,
		)
	}
	return loadNodeRunByID(ctx, tx, runtime.NodeRunID(req.NodeRunID))
}
