package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

type waitRepository struct{ tx *sql.Tx }

var _ ports.WaitRepository = waitRepository{}

// CreateWaitRegistration implements ports.WaitRepository (V4-08): a plain
// insert, ErrPersistenceAlreadyExists for a reused ID or NodeRunID
// (UNIQUE(node_run_id) — at most one registration per NodeRun activation).
func (r waitRepository) CreateWaitRegistration(ctx context.Context, registration runtime.WaitRegistration) (runtime.WaitRegistration, error) {
	if registration.ID == "" || registration.RunID == "" || registration.NodeRunID == "" {
		return runtime.WaitRegistration{}, errors.New("wait registration id, run id and node run id are required")
	}
	var dueAt any
	if registration.DueAt != nil {
		dueAt = formatWorkflowTime(*registration.DueAt)
	}
	var consumedSignalID any
	if registration.ConsumedSignalID != nil {
		consumedSignalID = string(*registration.ConsumedSignalID)
	}
	now := formatWorkflowTime(time.Now())
	if _, err := r.tx.ExecContext(ctx, `
INSERT INTO wait_registrations (
    id, project_id, run_id, node_run_id, node_key, signal_name, due_at,
    completion_outcome, timeout_outcome, state, consumed_signal_id, version, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(registration.ID), string(registration.ProjectID), string(registration.RunID), string(registration.NodeRunID),
		registration.NodeKey, registration.SignalName, dueAt, registration.CompletionOutcome, registration.TimeoutOutcome,
		string(registration.State), consumedSignalID, registration.Version, now, now,
	); err != nil {
		var existingByID int
		if lookupErr := r.tx.QueryRowContext(ctx, `SELECT 1 FROM wait_registrations WHERE id = ?`, registration.ID).Scan(&existingByID); lookupErr == nil {
			return runtime.WaitRegistration{}, fmt.Errorf("%w: wait registration %s", ports.ErrPersistenceAlreadyExists, registration.ID)
		}
		var existingByNodeRun int
		if lookupErr := r.tx.QueryRowContext(ctx, `SELECT 1 FROM wait_registrations WHERE node_run_id = ?`, registration.NodeRunID).Scan(&existingByNodeRun); lookupErr == nil {
			return runtime.WaitRegistration{}, fmt.Errorf("%w: node run %s already has a wait registration", ports.ErrPersistenceAlreadyExists, registration.NodeRunID)
		}
		return runtime.WaitRegistration{}, MapSQLiteError(fmt.Errorf("create wait registration %s: %w", registration.ID, err))
	}
	return registration, nil
}

// GetWaitRegistration implements ports.WaitRepository (V4-08).
func (r waitRepository) GetWaitRegistration(ctx context.Context, id string) (runtime.WaitRegistration, error) {
	return loadWaitRegistrationByID(ctx, r.tx, id)
}

func loadWaitRegistrationByID(ctx context.Context, tx *sql.Tx, id string) (runtime.WaitRegistration, error) {
	var registration runtime.WaitRegistration
	var projectID, runID, nodeRunID string
	var dueAt, consumedSignalID sql.NullString
	err := tx.QueryRowContext(ctx, `
SELECT id, project_id, run_id, node_run_id, node_key, signal_name, due_at,
       completion_outcome, timeout_outcome, state, consumed_signal_id, version
FROM wait_registrations WHERE id = ?`, id,
	).Scan(
		&registration.ID, &projectID, &runID, &nodeRunID, &registration.NodeKey, &registration.SignalName, &dueAt,
		&registration.CompletionOutcome, &registration.TimeoutOutcome, &registration.State, &consumedSignalID, &registration.Version,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return runtime.WaitRegistration{}, fmt.Errorf("%w: wait registration %s", ports.ErrPersistenceNotFound, id)
	}
	if err != nil {
		return runtime.WaitRegistration{}, MapSQLiteError(fmt.Errorf("load wait registration %s: %w", id, err))
	}
	registration.ProjectID = project.ProjectID(projectID)
	registration.RunID = runtime.WorkflowRunID(runID)
	registration.NodeRunID = runtime.NodeRunID(nodeRunID)
	if dueAt.Valid {
		parsed, parseErr := parseWorkflowTime(dueAt.String)
		if parseErr != nil {
			return runtime.WaitRegistration{}, fmt.Errorf("parse wait registration %s due_at: %w", id, parseErr)
		}
		registration.DueAt = &parsed
	}
	if consumedSignalID.Valid {
		ref := runtime.WaitSignalID(consumedSignalID.String)
		registration.ConsumedSignalID = &ref
	}
	return registration, nil
}

// RecordWaitSignal implements ports.WaitRepository (V4-08): see that
// method's own doc comment for the full idempotent-replay/conflict
// contract.
func (r waitRepository) RecordWaitSignal(ctx context.Context, signal runtime.WaitSignal) (runtime.WaitSignal, bool, error) {
	if signal.ID == "" || signal.WaitRegistrationID == "" || signal.SignalKey == "" {
		return runtime.WaitSignal{}, false, errors.New("wait signal id, wait registration id and signal key are required")
	}
	payloadJSON := string(signal.PayloadJSON)
	if payloadJSON == "" {
		payloadJSON = "{}"
	}
	now := formatWorkflowTime(signal.ReceivedAt)
	if _, err := r.tx.ExecContext(ctx, `
INSERT INTO wait_signals (id, wait_registration_id, signal_key, payload_json, payload_hash, actor, received_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		string(signal.ID), string(signal.WaitRegistrationID), signal.SignalKey, payloadJSON, signal.PayloadHash, signal.Actor, now,
	); err != nil {
		existing, lookupErr := loadWaitSignalByRegistrationAndKey(ctx, r.tx, string(signal.WaitRegistrationID), signal.SignalKey)
		if lookupErr != nil {
			return runtime.WaitSignal{}, false, MapSQLiteError(fmt.Errorf("record wait signal for registration %s: %w", signal.WaitRegistrationID, err))
		}
		if existing.PayloadHash != signal.PayloadHash {
			return runtime.WaitSignal{}, false, apperror.New(
				errorcode.CodeIdempotencyConflict,
				fmt.Sprintf("wait signal key %q already recorded with a different payload for registration %s", signal.SignalKey, signal.WaitRegistrationID),
				false,
			)
		}
		return existing, true, nil
	}
	return signal, false, nil
}

func loadWaitSignalByRegistrationAndKey(ctx context.Context, tx *sql.Tx, waitRegistrationID, signalKey string) (runtime.WaitSignal, error) {
	var signal runtime.WaitSignal
	var id, registrationID, payloadJSON, receivedAt string
	err := tx.QueryRowContext(ctx, `
SELECT id, wait_registration_id, signal_key, payload_json, payload_hash, actor, received_at
FROM wait_signals WHERE wait_registration_id = ? AND signal_key = ?`, waitRegistrationID, signalKey,
	).Scan(&id, &registrationID, &signal.SignalKey, &payloadJSON, &signal.PayloadHash, &signal.Actor, &receivedAt)
	if err != nil {
		return runtime.WaitSignal{}, fmt.Errorf("load wait signal for registration %s key %s: %w", waitRegistrationID, signalKey, err)
	}
	signal.ID = runtime.WaitSignalID(id)
	signal.WaitRegistrationID = runtime.WaitRegistrationID(registrationID)
	signal.PayloadJSON = []byte(payloadJSON)
	parsed, parseErr := parseWorkflowTime(receivedAt)
	if parseErr != nil {
		return runtime.WaitSignal{}, fmt.Errorf("parse wait signal received_at: %w", parseErr)
	}
	signal.ReceivedAt = parsed
	return signal, nil
}

// TransitionWaitRegistration implements ports.WaitRepository (V4-08): the
// fenced CAS SignalWait and the timer job both race against.
func (r waitRepository) TransitionWaitRegistration(ctx context.Context, req ports.TransitionWaitRegistrationRequest) (runtime.WaitRegistration, error) {
	if req.WaitRegistrationID == "" || req.NextState == "" {
		return runtime.WaitRegistration{}, errors.New("wait registration id and next state are required")
	}
	now := formatWorkflowTime(time.Now())
	var consumedSignalID any
	if req.ConsumedSignalID != "" {
		consumedSignalID = req.ConsumedSignalID
	}
	result, err := r.tx.ExecContext(ctx, `
UPDATE wait_registrations
SET state = ?, consumed_signal_id = COALESCE(?, consumed_signal_id), version = version + 1, updated_at = ?
WHERE id = ? AND state = ? AND version = ?`,
		string(req.NextState), consumedSignalID, now, req.WaitRegistrationID, string(req.ExpectedState), req.ExpectedVersion,
	)
	if err != nil {
		return runtime.WaitRegistration{}, MapSQLiteError(fmt.Errorf("transition wait registration %s: %w", req.WaitRegistrationID, err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return runtime.WaitRegistration{}, fmt.Errorf("read wait registration transition result: %w", err)
	}
	if affected != 1 {
		var exists int
		lookupErr := r.tx.QueryRowContext(ctx, `SELECT 1 FROM wait_registrations WHERE id = ?`, req.WaitRegistrationID).Scan(&exists)
		if errors.Is(lookupErr, sql.ErrNoRows) {
			return runtime.WaitRegistration{}, fmt.Errorf("%w: wait registration %s", ports.ErrPersistenceNotFound, req.WaitRegistrationID)
		}
		if lookupErr != nil {
			return runtime.WaitRegistration{}, MapSQLiteError(fmt.Errorf("check stale wait registration transition: %w", lookupErr))
		}
		return runtime.WaitRegistration{}, fmt.Errorf(
			"%w: wait registration %s expected %s@%d",
			ports.ErrOptimisticConflict, req.WaitRegistrationID, req.ExpectedState, req.ExpectedVersion,
		)
	}
	return loadWaitRegistrationByID(ctx, r.tx, req.WaitRegistrationID)
}
