package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
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

// GetMaxNodeIteration implements ports.RuntimeRepository (V4-07,
// docs/design/06-v4-runtime-engine.md): the highest Iteration any existing
// node_runs row for (runID, nodeKey) already carries. found is false (and
// iteration is meaningless) when no row for this exact key exists yet in
// this Run — see the ports interface's own doc comment for why this is a
// real MAX query, never a COUNT(*), so a future V4-12A scope-expansion
// reactivation that copies Iteration forward (rather than incrementing it)
// never gets silently double-counted here.
func (r runtimeRepository) GetMaxNodeIteration(ctx context.Context, runID, nodeKey string) (uint32, bool, error) {
	var iteration sql.NullInt64
	err := r.tx.QueryRowContext(ctx, `
SELECT MAX(iteration) FROM node_runs WHERE run_id = ? AND node_key = ?`, runID, nodeKey,
	).Scan(&iteration)
	if err != nil {
		return 0, false, fmt.Errorf("get max node iteration for run %s node %s: %w", runID, nodeKey, err)
	}
	if !iteration.Valid {
		return 0, false, nil
	}
	return uint32(iteration.Int64), true, nil
}

// ListNodeRunsForRun implements ports.RuntimeRepository (V4-12): every
// NodeRun activation for runID, reusing loadNodeRunByID's own column set
// via a plain SELECT of ids rather than duplicating its full column list a
// second time.
func (r runtimeRepository) ListNodeRunsForRun(ctx context.Context, runID string) ([]runtime.NodeRun, error) {
	rows, err := r.tx.QueryContext(ctx, `SELECT id FROM node_runs WHERE run_id = ? ORDER BY activation_sequence`, runID)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list node runs for run %s: %w", runID, err))
	}
	var ids []runtime.NodeRunID
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan node run id: %w", err)
		}
		ids = append(ids, runtime.NodeRunID(id))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate node run ids: %w", err)
	}
	rows.Close()

	nodeRuns := make([]runtime.NodeRun, 0, len(ids))
	for _, id := range ids {
		nodeRun, err := loadNodeRunByID(ctx, r.tx, id)
		if err != nil {
			return nil, err
		}
		nodeRuns = append(nodeRuns, nodeRun)
	}
	return nodeRuns, nil
}

// TransitionWorkflowRunState implements ports.RuntimeRepository (V4-12):
// the fenced CAS over workflow_runs.state, mirroring transitionNodeRunTx's
// own exact shape. Sets finished_at when NextState is a genuinely terminal
// state (FAILED) — VERIFYING is deliberately NOT terminal (ADR-011: it is
// only a completion CANDIDATE, still pending V5-11's own CompletionPolicy
// decision), so finished_at stays unset for that transition.
func (r runtimeRepository) TransitionWorkflowRunState(ctx context.Context, req ports.TransitionWorkflowRunStateRequest) (runtime.WorkflowRun, error) {
	if req.RunID == "" {
		return runtime.WorkflowRun{}, errors.New("workflow run id is required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var finishedAt any
	if req.NextState == runtime.WorkflowRunFailed {
		finishedAt = now
	}
	result, err := r.tx.ExecContext(ctx, `
UPDATE workflow_runs
SET state = ?, finished_at = COALESCE(?, finished_at), version = version + 1, updated_at = ?
WHERE id = ? AND state = ? AND version = ?`,
		string(req.NextState), finishedAt, now, req.RunID, string(req.ExpectedState), req.ExpectedVersion,
	)
	if err != nil {
		return runtime.WorkflowRun{}, MapSQLiteError(fmt.Errorf("transition workflow run %s state: %w", req.RunID, err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return runtime.WorkflowRun{}, fmt.Errorf("read workflow run state transition result: %w", err)
	}
	if affected != 1 {
		var exists int
		lookupErr := r.tx.QueryRowContext(ctx, `SELECT 1 FROM workflow_runs WHERE id = ?`, req.RunID).Scan(&exists)
		if errors.Is(lookupErr, sql.ErrNoRows) {
			return runtime.WorkflowRun{}, fmt.Errorf("%w: workflow run %s", ports.ErrPersistenceNotFound, req.RunID)
		}
		if lookupErr != nil {
			return runtime.WorkflowRun{}, MapSQLiteError(fmt.Errorf("check stale workflow run state transition: %w", lookupErr))
		}
		return runtime.WorkflowRun{}, fmt.Errorf(
			"%w: workflow run %s expected %s@%d",
			ports.ErrOptimisticConflict, req.RunID, req.ExpectedState, req.ExpectedVersion,
		)
	}
	return loadWorkflowRun(ctx, r.tx, runtime.WorkflowRunID(req.RunID))
}

// TransitionNodeRun implements ports.RuntimeRepository (V4-03): the CAS
// that closes a NodeRun's own routing decision, the node_runs counterpart
// of work.go's own transitionWorkItemStatusTx.
func (r runtimeRepository) TransitionNodeRun(ctx context.Context, req ports.TransitionNodeRunRequest) (runtime.NodeRun, error) {
	return transitionNodeRunTx(ctx, r.tx, req)
}

// UpdateWorkflowRunSharedState implements ports.RuntimeRepository (V4-03
// correction): a narrow CAS over just workflow_runs.shared_state_json —
// see this method's own ports interface doc comment for why it does not
// reuse the spike-era combined State+SharedState CAS.
func (r runtimeRepository) UpdateWorkflowRunSharedState(ctx context.Context, req ports.UpdateWorkflowRunSharedStateRequest) (runtime.WorkflowRun, error) {
	return updateWorkflowRunSharedStateTx(ctx, r.tx, req)
}

func updateWorkflowRunSharedStateTx(ctx context.Context, tx *sql.Tx, req ports.UpdateWorkflowRunSharedStateRequest) (runtime.WorkflowRun, error) {
	if req.RunID == "" {
		return runtime.WorkflowRun{}, errors.New("workflow run id is required")
	}
	sharedState := req.SharedState
	if len(sharedState) == 0 {
		sharedState = json.RawMessage(`{}`)
	}
	if !json.Valid(sharedState) {
		return runtime.WorkflowRun{}, errors.New("workflow run shared state must be valid JSON")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `
UPDATE workflow_runs
SET shared_state_json = ?, version = version + 1, updated_at = ?
WHERE id = ? AND version = ?`,
		string(sharedState), now, req.RunID, req.ExpectedVersion,
	)
	if err != nil {
		return runtime.WorkflowRun{}, MapSQLiteError(fmt.Errorf("update workflow run %s shared state: %w", req.RunID, err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return runtime.WorkflowRun{}, fmt.Errorf("read workflow run shared state update result: %w", err)
	}
	if affected != 1 {
		var exists int
		lookupErr := tx.QueryRowContext(ctx, `SELECT 1 FROM workflow_runs WHERE id = ?`, req.RunID).Scan(&exists)
		if errors.Is(lookupErr, sql.ErrNoRows) {
			return runtime.WorkflowRun{}, fmt.Errorf("%w: workflow run %s", ports.ErrPersistenceNotFound, req.RunID)
		}
		if lookupErr != nil {
			return runtime.WorkflowRun{}, MapSQLiteError(fmt.Errorf("check stale workflow run shared state update: %w", lookupErr))
		}
		return runtime.WorkflowRun{}, fmt.Errorf(
			"%w: workflow run %s expected version %d",
			ports.ErrOptimisticConflict, req.RunID, req.ExpectedVersion,
		)
	}
	return loadWorkflowRun(ctx, tx, runtime.WorkflowRunID(req.RunID))
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
