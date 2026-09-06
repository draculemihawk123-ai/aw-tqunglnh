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

// This file gives runtimeRepository its V4-13 recovery-reaper methods
// (docs/design/06-v4-runtime-engine.md, ADR-020) — the singleton generation
// cursor, the stranded-cancellation-intent queries, and the orphaned-
// RUNNING-attempt query the recovery reaper's own sweep is built from.
// Follows this package's own Tx-composable pattern exactly.

// GetRecoveryReaperState implements ports.RuntimeRepository.
func (r runtimeRepository) GetRecoveryReaperState(ctx context.Context) (ports.RecoveryReaperState, error) {
	var generation, version uint64
	err := r.tx.QueryRowContext(ctx, `SELECT generation, version FROM recovery_reaper_state WHERE id = 'singleton'`).Scan(&generation, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.RecoveryReaperState{}, fmt.Errorf("%w: recovery reaper state", ports.ErrPersistenceNotFound)
	}
	if err != nil {
		return ports.RecoveryReaperState{}, MapSQLiteError(fmt.Errorf("load recovery reaper state: %w", err))
	}
	return ports.RecoveryReaperState{Generation: generation, Version: version}, nil
}

// AdvanceRecoveryReaperGeneration implements ports.RuntimeRepository: the
// fenced CAS that bumps generation by exactly one.
func (r runtimeRepository) AdvanceRecoveryReaperGeneration(ctx context.Context, req ports.AdvanceRecoveryReaperGenerationRequest) (ports.RecoveryReaperState, error) {
	now := formatWorkflowTime(time.Now().UTC())
	result, err := r.tx.ExecContext(ctx, `
UPDATE recovery_reaper_state
SET generation = generation + 1, version = version + 1, updated_at = ?
WHERE id = 'singleton' AND generation = ? AND version = ?`,
		now, req.ExpectedGeneration, req.ExpectedVersion,
	)
	if err != nil {
		return ports.RecoveryReaperState{}, MapSQLiteError(fmt.Errorf("advance recovery reaper generation: %w", err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return ports.RecoveryReaperState{}, fmt.Errorf("read recovery reaper generation advance result: %w", err)
	}
	if affected != 1 {
		return ports.RecoveryReaperState{}, fmt.Errorf(
			"%w: recovery reaper state expected generation=%d version=%d",
			ports.ErrOptimisticConflict, req.ExpectedGeneration, req.ExpectedVersion,
		)
	}
	return r.GetRecoveryReaperState(ctx)
}

// ListRunCancellationIntentsByState implements ports.RuntimeRepository,
// ordered by run_id for a stable, deterministic result a test can assert on
// exactly.
func (r runtimeRepository) ListRunCancellationIntentsByState(ctx context.Context, state runtime.CancellationIntentState) ([]runtime.RunCancellationIntent, error) {
	rows, err := r.tx.QueryContext(ctx, `SELECT run_id FROM run_cancellation_intents WHERE state = ? ORDER BY run_id`, string(state))
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list run cancellation intents by state %s: %w", state, err))
	}
	var runIDs []string
	for rows.Next() {
		var runID string
		if err := rows.Scan(&runID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan run cancellation intent run id: %w", err)
		}
		runIDs = append(runIDs, runID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate run cancellation intent run ids: %w", err)
	}
	rows.Close()

	intents := make([]runtime.RunCancellationIntent, 0, len(runIDs))
	for _, runID := range runIDs {
		intent, err := loadRunCancellationIntentTx(ctx, r.tx, runID)
		if err != nil {
			return nil, err
		}
		intents = append(intents, intent)
	}
	return intents, nil
}

// ListWorkItemCancellationIntentsByState implements ports.RuntimeRepository,
// mirroring ListRunCancellationIntentsByState at the WorkItem level.
func (r runtimeRepository) ListWorkItemCancellationIntentsByState(ctx context.Context, state runtime.CancellationIntentState) ([]runtime.WorkItemCancellationIntent, error) {
	rows, err := r.tx.QueryContext(ctx, `SELECT work_item_id FROM work_item_cancellation_intents WHERE state = ? ORDER BY work_item_id`, string(state))
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list work item cancellation intents by state %s: %w", state, err))
	}
	var workItemIDs []string
	for rows.Next() {
		var workItemID string
		if err := rows.Scan(&workItemID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan work item cancellation intent work item id: %w", err)
		}
		workItemIDs = append(workItemIDs, workItemID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate work item cancellation intent work item ids: %w", err)
	}
	rows.Close()

	intents := make([]runtime.WorkItemCancellationIntent, 0, len(workItemIDs))
	for _, workItemID := range workItemIDs {
		intent, err := loadWorkItemCancellationIntentTx(ctx, r.tx, workItemID)
		if err != nil {
			return nil, err
		}
		intents = append(intents, intent)
	}
	return intents, nil
}

// ListOrphanedRunningExecutionAttempts implements ports.RuntimeRepository:
// every RUNNING ExecutionAttempt whose own driving EXECUTE_NODE job
// (AggregateType='ExecutionAttempt', AggregateID=the attempt's own ID) is no
// longer actively, provably held — missing entirely, not currently LEASED,
// or LEASED with an already-past lease_until as of asOf. A LEFT JOIN against
// durable_jobs (rather than an INNER JOIN) is deliberate: an Attempt whose
// own driving job row has already been reaped/archived by some other path
// still counts as orphaned, never silently excluded just because the row
// happens to be gone.
func (r runtimeRepository) ListOrphanedRunningExecutionAttempts(ctx context.Context, asOf time.Time) ([]runtime.ExecutionAttempt, error) {
	rows, err := r.tx.QueryContext(ctx, `
SELECT ea.id
FROM execution_attempts ea
LEFT JOIN durable_jobs dj ON dj.aggregate_type = 'ExecutionAttempt' AND dj.aggregate_id = ea.id
WHERE ea.state = 'RUNNING'
  AND (dj.id IS NULL OR dj.state != 'LEASED' OR dj.lease_until IS NULL OR dj.lease_until <= ?)
ORDER BY ea.id`,
		formatWorkflowTime(asOf.UTC()),
	)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list orphaned running execution attempts: %w", err))
	}
	var attemptIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan orphaned execution attempt id: %w", err)
		}
		attemptIDs = append(attemptIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate orphaned execution attempt ids: %w", err)
	}
	rows.Close()

	attempts := make([]runtime.ExecutionAttempt, 0, len(attemptIDs))
	for _, id := range attemptIDs {
		attempt, err := loadExecutionAttemptByID(ctx, r.tx, runtime.ExecutionAttemptID(id))
		if err != nil {
			return nil, err
		}
		attempts = append(attempts, attempt)
	}
	return attempts, nil
}

// GetWriteLeaseRepositoryWorkspaceForAttempt implements
// ports.RuntimeRepository (V4-13). write_leases carries no explicit
// "released" marker of its own (a released lease's own row is deleted, per
// V3/V4's own established write-lease lifecycle) — any remaining row for
// attemptID is therefore still its own real, current lease.
func (r runtimeRepository) GetWriteLeaseRepositoryWorkspaceForAttempt(ctx context.Context, attemptID string) (string, bool, error) {
	var repositoryWorkspaceID string
	err := r.tx.QueryRowContext(ctx,
		`SELECT repository_workspace_id FROM write_leases WHERE holder_attempt_id = ? LIMIT 1`, attemptID,
	).Scan(&repositoryWorkspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, MapSQLiteError(fmt.Errorf("resolve write lease repository workspace for attempt %s: %w", attemptID, err))
	}
	return repositoryWorkspaceID, true, nil
}
