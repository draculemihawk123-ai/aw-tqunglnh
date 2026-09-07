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
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// GetExecutionAttempt implements ports.RuntimeRepository (V4-05).
func (r runtimeRepository) GetExecutionAttempt(ctx context.Context, id string) (runtime.ExecutionAttempt, error) {
	return loadExecutionAttemptByID(ctx, r.tx, runtime.ExecutionAttemptID(id))
}

func loadExecutionAttemptByID(ctx context.Context, tx *sql.Tx, id runtime.ExecutionAttemptID) (runtime.ExecutionAttempt, error) {
	var attempt runtime.ExecutionAttempt
	var providerKey, terminationReason, failureCode, contextSnapshotID sql.NullString
	var inputRevisionSetJSON string
	err := tx.QueryRowContext(ctx, `
SELECT id, node_run_id, attempt_no, state, provider_key, execution_profile_hash,
       context_snapshot_id, input_revision_set_json, termination_reason, failure_code, version
FROM execution_attempts WHERE id = ?`, id,
	).Scan(
		&attempt.ID, &attempt.NodeRunID, &attempt.AttemptNumber, &attempt.State, &providerKey,
		&attempt.ExecutionProfileHash, &contextSnapshotID, &inputRevisionSetJSON, &terminationReason, &failureCode, &attempt.Version,
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
	return attempt, nil
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
