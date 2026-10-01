package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/contextsnapshot"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// GetExecutionAttempt implements ports.RuntimeRepository (V4-05).
func (r runtimeRepository) GetExecutionAttempt(ctx context.Context, id string) (runtime.ExecutionAttempt, error) {
	return loadExecutionAttemptByID(ctx, r.tx, runtime.ExecutionAttemptID(id))
}

func loadExecutionAttemptByID(ctx context.Context, tx *sql.Tx, id runtime.ExecutionAttemptID) (runtime.ExecutionAttempt, error) {
	var attempt runtime.ExecutionAttempt
	var providerKey, terminationReason, failureCode, contextSnapshotID, lastCheckpointID, inputTreesJSON sql.NullString
	var inputRevisionSetJSON string
	err := tx.QueryRowContext(ctx, `
SELECT id, node_run_id, attempt_no, state, provider_key, execution_profile_hash,
       context_snapshot_id, input_revision_set_json, termination_reason, failure_code, last_checkpoint_id, version,
       input_trees_json
FROM execution_attempts WHERE id = ?`, id,
	).Scan(
		&attempt.ID, &attempt.NodeRunID, &attempt.AttemptNumber, &attempt.State, &providerKey,
		&attempt.ExecutionProfileHash, &contextSnapshotID, &inputRevisionSetJSON, &terminationReason, &failureCode, &lastCheckpointID, &attempt.Version,
		&inputTreesJSON,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return runtime.ExecutionAttempt{}, fmt.Errorf("%w: execution attempt %s", ports.ErrPersistenceNotFound, id)
	}
	if err != nil {
		return runtime.ExecutionAttempt{}, fmt.Errorf("load execution attempt: %w", err)
	}
	if providerKey.Valid {
		attempt.ProviderKey = providerKey.String
	}
	if contextSnapshotID.Valid {
		id := contextsnapshot.ID(contextSnapshotID.String)
		attempt.ContextSnapshotID = &id
	}
	if lastCheckpointID.Valid {
		checkpointID := runtime.CheckpointID(lastCheckpointID.String)
		attempt.LastCheckpointID = &checkpointID
	}
	if terminationReason.Valid {
		attempt.TerminationReason = runtime.TerminationReason(terminationReason.String)
	}
	if failureCode.Valid {
		attempt.FailureCode = errorcode.Code(failureCode.String)
	}
	var revisions []workspace.Revision
	if inputRevisionSetJSON != "" && inputRevisionSetJSON != "[]" {
		if err := json.Unmarshal([]byte(inputRevisionSetJSON), &revisions); err != nil {
			return runtime.ExecutionAttempt{}, fmt.Errorf("decode execution attempt %s input revision set: %w", id, err)
		}
	}
	if len(revisions) > 0 {
		revisionSet, err := workspace.NewRevisionSet(revisions)
		if err != nil {
			return runtime.ExecutionAttempt{}, fmt.Errorf("reconstruct execution attempt %s input revision set: %w", id, err)
		}
		attempt.InputRevisionSet = &revisionSet
	}
	if inputTreesJSON.Valid && inputTreesJSON.String != "" {
		if err := json.Unmarshal([]byte(inputTreesJSON.String), &attempt.InputTrees); err != nil {
			return runtime.ExecutionAttempt{}, fmt.Errorf("decode execution attempt %s input trees: %w", id, err)
		}
	}
	return attempt, nil
}

// RecordAttemptInputTrees implements ports.RuntimeRepository (V9-01,
// ADR-030): the set-once write of an attempt's InputTrees. The single UPDATE
// is a compare-and-set on `input_trees_json IS NULL`, so of two writers
// racing for the same attempt exactly one applies and the other gets
// applied=false — a recorded tree is never overwritten. It deliberately does
// not touch execution_attempts.version (see migration 0042): recording
// annotates the attempt's input and must not invalidate the version the
// executor's later terminal CAS expects.
func (r runtimeRepository) RecordAttemptInputTrees(ctx context.Context, attemptID string, trees map[project.RepositoryID]string) (bool, error) {
	if attemptID == "" {
		return false, errors.New("execution attempt id is required")
	}
	if len(trees) == 0 {
		return false, errors.New("at least one input tree is required")
	}
	for repositoryID, treeID := range trees {
		if repositoryID == "" || treeID == "" {
			return false, errors.New("input trees need a non-empty repository id and tree id")
		}
	}
	encoded, err := json.Marshal(trees)
	if err != nil {
		return false, fmt.Errorf("encode execution attempt %s input trees: %w", attemptID, err)
	}
	result, err := r.tx.ExecContext(ctx, `
UPDATE execution_attempts SET input_trees_json = ?, updated_at = ?
WHERE id = ? AND input_trees_json IS NULL`,
		string(encoded), formatWorkflowTime(time.Now().UTC()), attemptID,
	)
	if err != nil {
		return false, MapSQLiteError(fmt.Errorf("record input trees for execution attempt %s: %w", attemptID, err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read input trees record result: %w", err)
	}
	if affected == 1 {
		return true, nil
	}
	var exists int
	lookupErr := r.tx.QueryRowContext(ctx, `SELECT 1 FROM execution_attempts WHERE id = ?`, attemptID).Scan(&exists)
	if errors.Is(lookupErr, sql.ErrNoRows) {
		return false, fmt.Errorf("%w: execution attempt %s", ports.ErrPersistenceNotFound, attemptID)
	}
	if lookupErr != nil {
		return false, MapSQLiteError(fmt.Errorf("check execution attempt %s for input trees: %w", attemptID, lookupErr))
	}
	return false, nil
}

// TransitionExecutionAttempt implements ports.RuntimeRepository (V4-05): the
// plain, unfenced CAS TransitionExecutionAttemptRequest's own doc comment
// describes. Fencing (JobLease/WriteLease) is FinalizeExecutionAttempt's own
// concern (internal/app/runtime/finalize.go), composed alongside this method
// inside the same transaction — this method itself never checks a lease.
func (r runtimeRepository) TransitionExecutionAttempt(ctx context.Context, req ports.TransitionExecutionAttemptRequest) (runtime.ExecutionAttempt, error) {
	return transitionExecutionAttemptTx(ctx, r.tx, req)
}

func transitionExecutionAttemptTx(ctx context.Context, tx *sql.Tx, req ports.TransitionExecutionAttemptRequest) (runtime.ExecutionAttempt, error) {
	if req.AttemptID == "" || req.NextState == "" {
		return runtime.ExecutionAttempt{}, errors.New("execution attempt id and next state are required")
	}
	now := formatWorkflowTime(time.Now().UTC())
	var finishedAt any
	if isTerminalExecutionAttemptState(req.NextState) {
		finishedAt = now
	}
	var terminationReason any
	if req.TerminationReason != "" {
		terminationReason = string(req.TerminationReason)
	}
	var failureCode any
	if req.FailureCode != "" {
		failureCode = string(req.FailureCode)
	}
	result, err := tx.ExecContext(ctx, `
UPDATE execution_attempts
SET state = ?, termination_reason = COALESCE(?, termination_reason),
    failure_code = COALESCE(?, failure_code),
    finished_at = COALESCE(?, finished_at), version = version + 1, updated_at = ?
WHERE id = ? AND state = ? AND version = ?`,
		string(req.NextState), terminationReason, failureCode, finishedAt, now,
		req.AttemptID, string(req.ExpectedState), req.ExpectedVersion,
	)
	if err != nil {
		return runtime.ExecutionAttempt{}, MapSQLiteError(fmt.Errorf("transition execution attempt %s: %w", req.AttemptID, err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return runtime.ExecutionAttempt{}, fmt.Errorf("read execution attempt transition result: %w", err)
	}
	if affected != 1 {
		var exists int
		lookupErr := tx.QueryRowContext(ctx, `SELECT 1 FROM execution_attempts WHERE id = ?`, req.AttemptID).Scan(&exists)
		if errors.Is(lookupErr, sql.ErrNoRows) {
			return runtime.ExecutionAttempt{}, fmt.Errorf("%w: execution attempt %s", ports.ErrPersistenceNotFound, req.AttemptID)
		}
		if lookupErr != nil {
			return runtime.ExecutionAttempt{}, MapSQLiteError(fmt.Errorf("check stale execution attempt transition: %w", lookupErr))
		}
		return runtime.ExecutionAttempt{}, fmt.Errorf(
			"%w: execution attempt %s expected %s@%d", ports.ErrOptimisticConflict, req.AttemptID, req.ExpectedState, req.ExpectedVersion,
		)
	}
	return loadExecutionAttemptByID(ctx, tx, runtime.ExecutionAttemptID(req.AttemptID))
}

func isTerminalExecutionAttemptState(state runtime.ExecutionAttemptState) bool {
	switch state {
	case runtime.ExecutionAttemptSucceeded, runtime.ExecutionAttemptFailed, runtime.ExecutionAttemptTimedOut,
		runtime.ExecutionAttemptCancelled, runtime.ExecutionAttemptLost, runtime.ExecutionAttemptIndeterminate,
		runtime.ExecutionAttemptBlocked:
		return true
	default:
		return false
	}
}

// ValidateWriteLeaseFencing implements ports.RuntimeRepository (V4-05) by
// reusing validateActiveWriteLeaseInTx (workflow_store.go) — the exact same
// fencing SQL FinalizeWorkflowRun's own spike-era finalize already proved
// out, extracted as a shared internal helper rather than duplicated (per
// the review decision: don't migrate FinalizeWorkflowRun, only reuse its
// SQL semantics).
func (r runtimeRepository) ValidateWriteLeaseFencing(ctx context.Context, lease ports.JobLease, grant ports.WriteLeaseGrant) error {
	return validateActiveWriteLeaseInTx(ctx, r.tx, lease, grant)
}
