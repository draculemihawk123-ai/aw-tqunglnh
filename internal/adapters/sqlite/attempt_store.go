package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// AttemptHeldAnyWriteLease reports whether attemptID is or ever was the
// holder of a WriteLease. This is the durable signal a replacement worker
// uses to tell a read-only interrupted attempt from a mutating one: scope
// alone is not evidence that a side effect was ever possible.
func (s *Store) AttemptHeldAnyWriteLease(ctx context.Context, attemptID runtime.ExecutionAttemptID) (bool, error) {
	if attemptID == "" {
		return false, errors.New("attempt id is required")
	}
	var held int
	if err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM write_leases WHERE holder_attempt_id = ?)`, attemptID,
	).Scan(&held); err != nil {
		return false, fmt.Errorf("check write lease history for attempt %s: %w", attemptID, err)
	}
	return held == 1, nil
}

// ErrLegacyTerminationReason is returned when a caller tries to write
// TerminationReasonProcessExitBeforeOutcomeCommit onto a new LOST or
// INDETERMINATE transition (V4-13, confirmed with the user before writing
// this check): that reason is legacy-only now, kept in the closed enum
// solely to read historical/evidence rows a pre-ADR-020 caller already wrote
// with it — ClassifyInterruptedAttempt (internal/app/worker/interruption.go)
// never emits it anymore, and this is the defense-in-depth backstop against
// any other caller reintroducing it, at the one fenced transition that ever
// produces either state.
var ErrLegacyTerminationReason = errors.New("sqlite: TerminationReasonProcessExitBeforeOutcomeCommit is legacy-only and must not be written to a new LOST/INDETERMINATE transition")

// TerminateInterruptedAttempt is the fenced recovery-time transition that
// moves a crashed attempt from RUNNING to LOST or INDETERMINATE. It commits
// the state change and its correlated domain event in one transaction, CAS
// guarded on (id, state=RUNNING, version): a second caller racing on the
// same attempt gets ports.ErrOptimisticConflict, never a second terminal
// write or a second domain event for it.
func (s *Store) TerminateInterruptedAttempt(ctx context.Context, update ports.AttemptTerminationUpdate) error {
	if update.AttemptID == "" || update.NextState == "" || update.Reason == "" || update.EventID == "" {
		return errors.New("attempt termination update is incomplete")
	}
	if (update.NextState == runtime.ExecutionAttemptLost || update.NextState == runtime.ExecutionAttemptIndeterminate) &&
		update.Reason == runtime.TerminationReasonProcessExitBeforeOutcomeCommit {
		return ErrLegacyTerminationReason
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin attempt termination: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	timestamp := formatWorkflowTime(update.OccurredAt)
	result, err := tx.ExecContext(ctx, `
UPDATE execution_attempts
SET state = ?, termination_reason = ?, version = version + 1, finished_at = ?, updated_at = ?
WHERE id = ? AND state = ? AND version = ?`,
		string(update.NextState),
		string(update.Reason),
		timestamp,
		timestamp,
		update.AttemptID,
		string(runtime.ExecutionAttemptRunning),
		update.ExpectedVersion,
	)
	if err != nil {
		return fmt.Errorf("terminate interrupted attempt %s: %w", update.AttemptID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read attempt termination result: %w", err)
	}
	if affected != 1 {
		return fmt.Errorf("%w: attempt %s expected RUNNING@%d",
			ports.ErrOptimisticConflict, update.AttemptID, update.ExpectedVersion)
	}

	payload, err := json.Marshal(map[string]string{
		"attemptId": string(update.AttemptID),
		"nextState": string(update.NextState),
		"reason":    string(update.Reason),
	})
	if err != nil {
		return fmt.Errorf("encode attempt termination event: %w", err)
	}
	var sequence uint64
	if err := tx.QueryRowContext(ctx, `
SELECT COALESCE(MAX(sequence), 0) + 1
FROM domain_events
WHERE aggregate_type = 'ExecutionAttempt' AND aggregate_id = ?`, update.AttemptID,
	).Scan(&sequence); err != nil {
		return fmt.Errorf("allocate attempt termination event sequence: %w", err)
	}
	journalPosition, err := allocateJournalPosition(ctx, tx)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO domain_events(
    id, project_id, aggregate_type, aggregate_id, sequence, journal_position,
    event_type, schema_version, payload_json, correlation_id, created_at
)
SELECT ?, wr.project_id, 'ExecutionAttempt', ea.id, ?, ?,
       'EXECUTION_ATTEMPT_TERMINATED', 1, ?, ?, ?
FROM execution_attempts ea
JOIN node_runs nr ON nr.id = ea.node_run_id
JOIN workflow_runs wr ON wr.id = nr.run_id
WHERE ea.id = ?`,
		update.EventID, sequence, journalPosition, string(payload), update.CorrelationID, timestamp, update.AttemptID,
	); err != nil {
		return fmt.Errorf("append attempt termination event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit attempt termination: %w", err)
	}
	return nil
}
