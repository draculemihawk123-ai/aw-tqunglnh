package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// CreateWorkflowRun implements ports.RuntimeRepository (V4-02): the
// Tx-composable counterpart of WorkflowPersistence.StartWorkflowRun
// (workflow_store.go). Unlike that spike-era method, it does not re-load
// and re-verify the pinned WorkflowVersion: its only caller
// (internal/app/runtime.StartWorkflowRun) already resolved the exact
// published WorkflowVersion via DefinitionsRepository.GetWorkflowVersion
// and built run from it with runtime.NewWorkflowRun, so
// run.WorkflowVersionHash/PinnedDependencies are already derived from that
// same authoritative source by construction — re-deriving them again here
// would just repeat work already done earlier in the same transaction.
func (r runtimeRepository) CreateWorkflowRun(ctx context.Context, run runtime.WorkflowRun) (runtime.WorkflowRun, error) {
	return createWorkflowRunTx(ctx, r.tx, run)
}

func createWorkflowRunTx(ctx context.Context, tx *sql.Tx, run runtime.WorkflowRun) (runtime.WorkflowRun, error) {
	if run.ID == "" || run.ProjectID == "" || run.WorkItemID == "" || run.FamilyID == "" ||
		run.WorkflowVersionID == "" || run.ScopeVersion == 0 {
		return runtime.WorkflowRun{}, errors.New("workflow run identities, pinned version and scope are required")
	}
	sharedState := run.SharedState
	if len(sharedState) == 0 {
		sharedState = json.RawMessage(`{}`)
	}
	if !json.Valid(sharedState) {
		return runtime.WorkflowRun{}, errors.New("workflow run shared state must be valid JSON")
	}

	startedAt := sql.NullString{}
	if run.StartedAt != nil {
		startedAt = sql.NullString{String: formatWorkflowTime(*run.StartedAt), Valid: true}
	}
	finishedAt := sql.NullString{}
	if run.FinishedAt != nil {
		finishedAt = sql.NullString{String: formatWorkflowTime(*run.FinishedAt), Valid: true}
	}
	now := formatWorkflowTime(time.Now())

	_, err := tx.ExecContext(ctx, `
INSERT INTO workflow_runs (
    id, project_id, work_item_id, workflow_version_id, family_id,
    scope_version, state, shared_state_json, version,
    started_at, finished_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(run.ID), string(run.ProjectID), string(run.WorkItemID), string(run.WorkflowVersionID), string(run.FamilyID),
		run.ScopeVersion, string(run.State), string(sharedState), run.Version,
		startedAt, finishedAt, now, now,
	)
	if err != nil {
		var existing int
		lookupErr := tx.QueryRowContext(ctx, `SELECT 1 FROM workflow_runs WHERE id = ?`, run.ID).Scan(&existing)
		if lookupErr == nil {
			return runtime.WorkflowRun{}, fmt.Errorf("%w: workflow run %s", ports.ErrPersistenceAlreadyExists, run.ID)
		}
		return runtime.WorkflowRun{}, MapSQLiteError(fmt.Errorf("create workflow run %s: %w", run.ID, err))
	}
	return run, nil
}

// CreateNodeRun implements ports.RuntimeRepository (V4-02): inserts a new
// NodeRun activation after verifying nodeRun.RunID names a WorkflowRun that
// exists. See this method's own ports.RuntimeRepository doc comment for
// why only node_runs' pre-existing columns are persisted.
func (r runtimeRepository) CreateNodeRun(ctx context.Context, nodeRun runtime.NodeRun) (runtime.NodeRun, error) {
	return createNodeRunTx(ctx, r.tx, nodeRun)
}

func createNodeRunTx(ctx context.Context, tx *sql.Tx, nodeRun runtime.NodeRun) (runtime.NodeRun, error) {
	if nodeRun.ID == "" || nodeRun.RunID == "" || strings.TrimSpace(nodeRun.NodeKey) == "" {
		return runtime.NodeRun{}, errors.New("node run id, workflow run id and node key are required")
	}
	if nodeRun.ActivationSequence == 0 {
		return runtime.NodeRun{}, errors.New("node run activation sequence must be greater than zero")
	}
	if strings.TrimSpace(nodeRun.InputStateHash) == "" {
		return runtime.NodeRun{}, errors.New("node run input state hash is required")
	}

	var runExists int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM workflow_runs WHERE id = ?`, string(nodeRun.RunID)).Scan(&runExists)
	if errors.Is(err, sql.ErrNoRows) {
		return runtime.NodeRun{}, fmt.Errorf("%w: workflow run %s", ports.ErrPersistenceNotFound, nodeRun.RunID)
	}
	if err != nil {
		return runtime.NodeRun{}, MapSQLiteError(fmt.Errorf("resolve node run's workflow run: %w", err))
	}

	var selectedOutcome any
	if nodeRun.SelectedOutcome != "" {
		selectedOutcome = nodeRun.SelectedOutcome
	}
	now := formatWorkflowTime(time.Now())
	if _, err := tx.ExecContext(ctx, `
INSERT INTO node_runs (
    id, run_id, node_key, activation_sequence, iteration, state,
    selected_outcome, input_state_hash, version, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(nodeRun.ID), string(nodeRun.RunID), nodeRun.NodeKey, nodeRun.ActivationSequence, nodeRun.Iteration,
		string(nodeRun.State), selectedOutcome, nodeRun.InputStateHash, nodeRun.Version, now, now,
	); err != nil {
		var existing int
		lookupErr := tx.QueryRowContext(ctx, `SELECT 1 FROM node_runs WHERE id = ?`, nodeRun.ID).Scan(&existing)
		if lookupErr == nil {
			return runtime.NodeRun{}, fmt.Errorf("%w: node run %s", ports.ErrPersistenceAlreadyExists, nodeRun.ID)
		}
		return runtime.NodeRun{}, MapSQLiteError(fmt.Errorf("create node run %s: %w", nodeRun.ID, err))
	}
	return nodeRun, nil
}
