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

// CompleteNodeAndDispatchNext is the fenced worker path that closes crash
// boundary "after outcome commit, before next dispatch": completing the
// current node, acknowledging the job that drove it and enqueuing the
// downstream job all commit in one SQLite transaction, or none of them do.
//
// A caller that replays the exact same NodeCompletionDispatch after a crash
// (recognized by NextJob.IdempotencyKey) is never told to write a second
// terminal node transition or a second domain event: it gets back the
// downstream job that first transaction already created.
func (s *Store) CompleteNodeAndDispatchNext(
	ctx context.Context,
	dispatch ports.NodeCompletionDispatch,
) (runtime.NodeRun, ports.DurableJob, error) {
	if dispatch.NodeRunID == "" || dispatch.EventID == "" || dispatch.OccurredAt.IsZero() {
		return runtime.NodeRun{}, ports.DurableJob{}, errors.New("node completion dispatch is incomplete")
	}
	if err := validateJobLease(dispatch.JobLease); err != nil {
		return runtime.NodeRun{}, ports.DurableJob{}, err
	}
	if err := validateEnqueueJob(dispatch.NextJob); err != nil {
		return runtime.NodeRun{}, ports.DurableJob{}, err
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return runtime.NodeRun{}, ports.DurableJob{}, fmt.Errorf("begin node completion dispatch: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	timestamp := formatWorkflowTime(dispatch.OccurredAt)
	result, err := tx.ExecContext(ctx, `
UPDATE node_runs
SET state = 'SUCCEEDED', selected_outcome = ?, version = version + 1, updated_at = ?
WHERE id = ? AND state = 'RUNNING' AND version = ?`,
		dispatch.SelectedOutcome, timestamp, dispatch.NodeRunID, dispatch.ExpectedVersion,
	)
	if err != nil {
		return runtime.NodeRun{}, ports.DurableJob{}, fmt.Errorf("complete node run %s: %w", dispatch.NodeRunID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return runtime.NodeRun{}, ports.DurableJob{}, fmt.Errorf("read node completion result: %w", err)
	}

	if affected != 1 {
		// Not a fresh completion. The only legitimate reason is that this
		// exact transaction already committed before a crash: prove it from
		// the downstream job's idempotency key rather than assuming either
		// way.
		existingJob, loadErr := loadDurableJobByIdempotencyKey(ctx, tx, dispatch.NextJob.IdempotencyKey)
		if loadErr != nil {
			return runtime.NodeRun{}, ports.DurableJob{}, fmt.Errorf(
				"%w: node run %s expected RUNNING@%d", ports.ErrOptimisticConflict, dispatch.NodeRunID, dispatch.ExpectedVersion)
		}
		nodeRun, err := loadNodeRunByID(ctx, tx, dispatch.NodeRunID)
		if err != nil {
			return runtime.NodeRun{}, ports.DurableJob{}, err
		}
		return nodeRun, existingJob, nil
	}

	jobResult, err := tx.ExecContext(ctx, `
UPDATE durable_jobs
SET state = 'SUCCEEDED', lease_until = ?, heartbeat_at = ?, version = version + 1, updated_at = ?
WHERE id = ? AND state = 'LEASED' AND lease_owner = ? AND lease_token = ?`,
		timestamp, timestamp, timestamp,
		dispatch.JobLease.JobID, dispatch.JobLease.Owner, dispatch.JobLease.Token,
	)
	if err != nil {
		return runtime.NodeRun{}, ports.DurableJob{}, fmt.Errorf("acknowledge node dispatch job %s: %w", dispatch.JobLease.JobID, err)
	}
	jobAffected, err := jobResult.RowsAffected()
	if err != nil {
		return runtime.NodeRun{}, ports.DurableJob{}, fmt.Errorf("read node dispatch job acknowledgement: %w", err)
	}
	if jobAffected != 1 {
		return runtime.NodeRun{}, ports.DurableJob{}, fmt.Errorf("%w: job %s", ports.ErrJobLeaseLost, dispatch.JobLease.JobID)
	}

	payload, err := json.Marshal(map[string]string{
		"nodeRunId":       string(dispatch.NodeRunID),
		"selectedOutcome": dispatch.SelectedOutcome,
	})
	if err != nil {
		return runtime.NodeRun{}, ports.DurableJob{}, fmt.Errorf("encode node completion event: %w", err)
	}
	var sequence uint64
	if err := tx.QueryRowContext(ctx, `
SELECT COALESCE(MAX(sequence), 0) + 1
FROM domain_events
WHERE aggregate_type = 'NodeRun' AND aggregate_id = ?`, dispatch.NodeRunID,
	).Scan(&sequence); err != nil {
		return runtime.NodeRun{}, ports.DurableJob{}, fmt.Errorf("allocate node completion event sequence: %w", err)
	}
	journalPosition, err := allocateJournalPosition(ctx, tx)
	if err != nil {
		return runtime.NodeRun{}, ports.DurableJob{}, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO domain_events(
    id, project_id, aggregate_type, aggregate_id, sequence, journal_position,
    event_type, schema_version, payload_json, correlation_id, created_at
)
SELECT ?, wr.project_id, 'NodeRun', nr.id, ?, ?,
       'NODE_RUN_COMPLETED', 1, ?, ?, ?
FROM node_runs nr
JOIN workflow_runs wr ON wr.id = nr.run_id
WHERE nr.id = ?`,
		dispatch.EventID, sequence, journalPosition, string(payload), dispatch.CorrelationID, timestamp, dispatch.NodeRunID,
	); err != nil {
		return runtime.NodeRun{}, ports.DurableJob{}, fmt.Errorf("append node completion event: %w", err)
	}

	newJob, err := insertDispatchedJob(ctx, tx, dispatch.NextJob)
	if err != nil {
		return runtime.NodeRun{}, ports.DurableJob{}, err
	}
	nodeRun, err := loadNodeRunByID(ctx, tx, dispatch.NodeRunID)
	if err != nil {
		return runtime.NodeRun{}, ports.DurableJob{}, err
	}
	if err := tx.Commit(); err != nil {
		return runtime.NodeRun{}, ports.DurableJob{}, fmt.Errorf("commit node completion dispatch: %w", err)
	}
	return nodeRun, newJob, nil
}

func insertDispatchedJob(ctx context.Context, tx *sql.Tx, request ports.EnqueueJobRequest) (ports.DurableJob, error) {
	payload := request.Payload
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	availableAt := ""
	if !request.AvailableAt.IsZero() {
		availableAt = request.AvailableAt.UTC().Format(time.RFC3339Nano)
	}
	row := tx.QueryRowContext(ctx, `
INSERT INTO durable_jobs (
    id, project_id, kind, aggregate_type, aggregate_id, payload_json, state,
    available_at, priority, claim_count, max_claims, lease_token,
    idempotency_key, version, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, 'AVAILABLE',
    COALESCE(NULLIF(?, ''), strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    ?, 0, ?, 0, ?, 1,
    strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
    strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
RETURNING `+durableJobColumns,
		request.ID,
		request.ProjectID,
		strings.TrimSpace(request.Kind),
		strings.TrimSpace(request.AggregateType),
		strings.TrimSpace(request.AggregateID),
		string(payload),
		availableAt,
		request.Priority,
		request.MaxClaims,
		strings.TrimSpace(request.IdempotencyKey),
	)
	job, err := scanDurableJob(row)
	if err != nil {
		return ports.DurableJob{}, fmt.Errorf("dispatch downstream job: %w", err)
	}
	return job, nil
}

func loadDurableJobByIdempotencyKey(ctx context.Context, tx *sql.Tx, idempotencyKey string) (ports.DurableJob, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+durableJobColumns+` FROM durable_jobs WHERE idempotency_key = ?`, idempotencyKey)
	job, err := scanDurableJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ports.DurableJob{}, fmt.Errorf("%w: job idempotency key %s", ports.ErrPersistenceNotFound, idempotencyKey)
	}
	if err != nil {
		return ports.DurableJob{}, fmt.Errorf("load durable job by idempotency key: %w", err)
	}
	return job, nil
}

// DispatchNodeIntent is the fenced application transaction that creates a
// WorkflowRun's first durable intent to execute: it transitions the run
// CREATED -> RUNNING, inserts its first NodeRun and enqueues the dispatching
// job, all in one SQLite transaction — closing crash boundaries 1-2 of
// SPK-04 ("before intent/job commit" and "after job commit, before claim").
//
// A caller that replays the exact same request after a crash (recognized by
// Job.IdempotencyKey) gets back the job that transaction already created,
// exactly as CompleteNodeAndDispatchNext does for later dispatches.
func (s *Store) DispatchNodeIntent(
	ctx context.Context,
	dispatch ports.NodeIntentDispatch,
) (runtime.NodeRun, ports.DurableJob, error) {
	if dispatch.RunID == "" || dispatch.NodeRunID == "" || strings.TrimSpace(dispatch.NodeKey) == "" ||
		dispatch.EventID == "" || dispatch.OccurredAt.IsZero() {
		return runtime.NodeRun{}, ports.DurableJob{}, errors.New("node intent dispatch is incomplete")
	}
	if err := validateEnqueueJob(dispatch.Job); err != nil {
		return runtime.NodeRun{}, ports.DurableJob{}, err
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return runtime.NodeRun{}, ports.DurableJob{}, fmt.Errorf("begin node intent dispatch: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	timestamp := formatWorkflowTime(dispatch.OccurredAt)
	result, err := tx.ExecContext(ctx, `
UPDATE workflow_runs
SET state = 'RUNNING',
    version = version + 1,
    started_at = CASE WHEN started_at IS NULL THEN ? ELSE started_at END,
    updated_at = ?
WHERE id = ? AND state = 'CREATED' AND version = ?`,
		timestamp, timestamp, dispatch.RunID, dispatch.ExpectedRunVersion,
	)
	if err != nil {
		return runtime.NodeRun{}, ports.DurableJob{}, fmt.Errorf("dispatch intent for run %s: %w", dispatch.RunID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return runtime.NodeRun{}, ports.DurableJob{}, fmt.Errorf("read node intent dispatch result: %w", err)
	}

	if affected != 1 {
		// Not a fresh dispatch. The only legitimate reason is that this
		// exact transaction already committed before a crash — prove it
		// from the job's idempotency key rather than assuming either way.
		existingJob, loadErr := loadDurableJobByIdempotencyKey(ctx, tx, dispatch.Job.IdempotencyKey)
		if loadErr != nil {
			return runtime.NodeRun{}, ports.DurableJob{}, fmt.Errorf(
				"%w: workflow run %s expected CREATED@%d", ports.ErrOptimisticConflict, dispatch.RunID, dispatch.ExpectedRunVersion)
		}
		nodeRun, err := loadNodeRunByID(ctx, tx, dispatch.NodeRunID)
		if err != nil {
			return runtime.NodeRun{}, ports.DurableJob{}, err
		}
		return nodeRun, existingJob, nil
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO node_runs(
    id, run_id, node_key, activation_sequence, iteration, state,
    input_state_hash, version, created_at, updated_at
) VALUES (?, ?, ?, ?, 0, 'RUNNING', ?, 1, ?, ?)`,
		dispatch.NodeRunID, dispatch.RunID, strings.TrimSpace(dispatch.NodeKey), dispatch.ActivationSequence,
		dispatch.InputStateHash, timestamp, timestamp,
	); err != nil {
		return runtime.NodeRun{}, ports.DurableJob{}, fmt.Errorf("insert dispatched node run %s: %w", dispatch.NodeRunID, err)
	}

	payload, err := json.Marshal(map[string]string{
		"runId":     string(dispatch.RunID),
		"nodeRunId": string(dispatch.NodeRunID),
		"nodeKey":   dispatch.NodeKey,
	})
	if err != nil {
		return runtime.NodeRun{}, ports.DurableJob{}, fmt.Errorf("encode node intent dispatch event: %w", err)
	}
	var sequence uint64
	if err := tx.QueryRowContext(ctx, `
SELECT COALESCE(MAX(sequence), 0) + 1
FROM domain_events
WHERE aggregate_type = 'NodeRun' AND aggregate_id = ?`, dispatch.NodeRunID,
	).Scan(&sequence); err != nil {
		return runtime.NodeRun{}, ports.DurableJob{}, fmt.Errorf("allocate node intent dispatch event sequence: %w", err)
	}
	journalPosition, err := allocateJournalPosition(ctx, tx)
	if err != nil {
		return runtime.NodeRun{}, ports.DurableJob{}, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO domain_events(
    id, project_id, aggregate_type, aggregate_id, sequence, journal_position,
    event_type, schema_version, payload_json, correlation_id, created_at
)
SELECT ?, project_id, 'NodeRun', ?, ?, ?,
       'NODE_RUN_DISPATCHED', 1, ?, ?, ?
FROM workflow_runs
WHERE id = ?`,
		dispatch.EventID, dispatch.NodeRunID, sequence, journalPosition, string(payload), dispatch.CorrelationID, timestamp, dispatch.RunID,
	); err != nil {
		return runtime.NodeRun{}, ports.DurableJob{}, fmt.Errorf("append node intent dispatch event: %w", err)
	}

	newJob, err := insertDispatchedJob(ctx, tx, dispatch.Job)
	if err != nil {
		return runtime.NodeRun{}, ports.DurableJob{}, err
	}
	nodeRun, err := loadNodeRunByID(ctx, tx, dispatch.NodeRunID)
	if err != nil {
		return runtime.NodeRun{}, ports.DurableJob{}, err
	}
	if err := tx.Commit(); err != nil {
		return runtime.NodeRun{}, ports.DurableJob{}, fmt.Errorf("commit node intent dispatch: %w", err)
	}
	return nodeRun, newJob, nil
}

func loadNodeRunByID(ctx context.Context, tx *sql.Tx, id runtime.NodeRunID) (runtime.NodeRun, error) {
	var nodeRun runtime.NodeRun
	var selectedOutcome sql.NullString
	err := tx.QueryRowContext(ctx, `
SELECT id, run_id, node_key, activation_sequence, iteration, state,
       selected_outcome, input_state_hash, version
FROM node_runs WHERE id = ?`, id,
	).Scan(
		&nodeRun.ID, &nodeRun.RunID, &nodeRun.NodeKey, &nodeRun.ActivationSequence,
		&nodeRun.Iteration, &nodeRun.State, &selectedOutcome, &nodeRun.InputStateHash, &nodeRun.Version,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return runtime.NodeRun{}, fmt.Errorf("%w: node run %s", ports.ErrPersistenceNotFound, id)
	}
	if err != nil {
		return runtime.NodeRun{}, fmt.Errorf("load node run: %w", err)
	}
	if selectedOutcome.Valid {
		nodeRun.SelectedOutcome = selectedOutcome.String
	}
	return nodeRun, nil
}
