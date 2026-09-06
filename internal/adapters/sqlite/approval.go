package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

type approvalRepository struct{ tx *sql.Tx }

var _ ports.ApprovalRepository = approvalRepository{}

// CreateApprovalRequest implements ports.ApprovalRepository (V4-09): a
// plain insert, ErrPersistenceAlreadyExists for a reused ID or NodeRunID
// (UNIQUE(node_run_id) — at most one request per NodeRun activation).
func (r approvalRepository) CreateApprovalRequest(ctx context.Context, request runtime.ApprovalRequest) (runtime.ApprovalRequest, error) {
	if request.ID == "" || request.RunID == "" || request.NodeRunID == "" {
		return runtime.ApprovalRequest{}, errors.New("approval request id, run id and node run id are required")
	}
	authorizedRolesJSON, err := json.Marshal(request.AuthorizedRoles)
	if err != nil {
		return runtime.ApprovalRequest{}, fmt.Errorf("marshal approval request authorized roles: %w", err)
	}
	requestedEvidenceKindsJSON, err := json.Marshal(request.RequestedEvidenceKinds)
	if err != nil {
		return runtime.ApprovalRequest{}, fmt.Errorf("marshal approval request requested evidence kinds: %w", err)
	}
	now := formatWorkflowTime(time.Now())
	if _, err := r.tx.ExecContext(ctx, `
INSERT INTO approval_requests (
    id, project_id, run_id, node_run_id, node_key, authorized_roles_json, requested_evidence_kinds_json,
    due_at, escalation_outcome, state, version, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(request.ID), string(request.ProjectID), string(request.RunID), string(request.NodeRunID), request.NodeKey,
		string(authorizedRolesJSON), string(requestedEvidenceKindsJSON), formatWorkflowTime(request.DueAt),
		request.EscalationOutcome, string(request.State), request.Version, now, now,
	); err != nil {
		var existingByID int
		if lookupErr := r.tx.QueryRowContext(ctx, `SELECT 1 FROM approval_requests WHERE id = ?`, request.ID).Scan(&existingByID); lookupErr == nil {
			return runtime.ApprovalRequest{}, fmt.Errorf("%w: approval request %s", ports.ErrPersistenceAlreadyExists, request.ID)
		}
		var existingByNodeRun int
		if lookupErr := r.tx.QueryRowContext(ctx, `SELECT 1 FROM approval_requests WHERE node_run_id = ?`, request.NodeRunID).Scan(&existingByNodeRun); lookupErr == nil {
			return runtime.ApprovalRequest{}, fmt.Errorf("%w: node run %s already has an approval request", ports.ErrPersistenceAlreadyExists, request.NodeRunID)
		}
		return runtime.ApprovalRequest{}, MapSQLiteError(fmt.Errorf("create approval request %s: %w", request.ID, err))
	}
	return request, nil
}

// GetApprovalRequest implements ports.ApprovalRepository (V4-09).
func (r approvalRepository) GetApprovalRequest(ctx context.Context, id string) (runtime.ApprovalRequest, error) {
	return loadApprovalRequestByID(ctx, r.tx, id)
}

func loadApprovalRequestByID(ctx context.Context, tx *sql.Tx, id string) (runtime.ApprovalRequest, error) {
	var request runtime.ApprovalRequest
	var projectID, runID, nodeRunID, authorizedRolesJSON, requestedEvidenceKindsJSON, dueAt string
	var decidedBy, decidedRole, decidedOutcome, reason, decidedAt sql.NullString
	err := tx.QueryRowContext(ctx, `
SELECT id, project_id, run_id, node_run_id, node_key, authorized_roles_json, requested_evidence_kinds_json,
       due_at, escalation_outcome, state, decided_by, decided_role, decided_outcome, reason, decided_at, version
FROM approval_requests WHERE id = ?`, id,
	).Scan(
		&request.ID, &projectID, &runID, &nodeRunID, &request.NodeKey, &authorizedRolesJSON, &requestedEvidenceKindsJSON,
		&dueAt, &request.EscalationOutcome, &request.State, &decidedBy, &decidedRole, &decidedOutcome, &reason, &decidedAt, &request.Version,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return runtime.ApprovalRequest{}, fmt.Errorf("%w: approval request %s", ports.ErrPersistenceNotFound, id)
	}
	if err != nil {
		return runtime.ApprovalRequest{}, MapSQLiteError(fmt.Errorf("load approval request %s: %w", id, err))
	}
	request.ProjectID = project.ProjectID(projectID)
	request.RunID = runtime.WorkflowRunID(runID)
	request.NodeRunID = runtime.NodeRunID(nodeRunID)
	if err := json.Unmarshal([]byte(authorizedRolesJSON), &request.AuthorizedRoles); err != nil {
		return runtime.ApprovalRequest{}, fmt.Errorf("decode approval request %s authorized roles: %w", id, err)
	}
	if err := json.Unmarshal([]byte(requestedEvidenceKindsJSON), &request.RequestedEvidenceKinds); err != nil {
		return runtime.ApprovalRequest{}, fmt.Errorf("decode approval request %s requested evidence kinds: %w", id, err)
	}
	parsedDueAt, err := parseWorkflowTime(dueAt)
	if err != nil {
		return runtime.ApprovalRequest{}, fmt.Errorf("parse approval request %s due_at: %w", id, err)
	}
	request.DueAt = parsedDueAt
	if decidedBy.Valid {
		request.DecidedBy = decidedBy.String
	}
	if decidedRole.Valid {
		request.DecidedRole = decidedRole.String
	}
	if decidedOutcome.Valid {
		request.DecidedOutcome = decidedOutcome.String
	}
	if reason.Valid {
		request.Reason = reason.String
	}
	if decidedAt.Valid {
		parsed, parseErr := parseWorkflowTime(decidedAt.String)
		if parseErr != nil {
			return runtime.ApprovalRequest{}, fmt.Errorf("parse approval request %s decided_at: %w", id, parseErr)
		}
		request.DecidedAt = &parsed
	}
	return request, nil
}

// ListApprovalRequestsForRun implements ports.ApprovalRepository (V4-12B):
// every ApprovalRequest for runID, reusing loadApprovalRequestByID's own
// column set via a plain SELECT of ids — the same pattern
// ListNodeRunsForRun already established.
func (r approvalRepository) ListApprovalRequestsForRun(ctx context.Context, runID string) ([]runtime.ApprovalRequest, error) {
	rows, err := r.tx.QueryContext(ctx, `SELECT id FROM approval_requests WHERE run_id = ? ORDER BY id`, runID)
	if err != nil {
		return nil, MapSQLiteError(fmt.Errorf("list approval requests for run %s: %w", runID, err))
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan approval request id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate approval request ids: %w", err)
	}
	rows.Close()

	requests := make([]runtime.ApprovalRequest, 0, len(ids))
	for _, id := range ids {
		request, err := loadApprovalRequestByID(ctx, r.tx, id)
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	return requests, nil
}

// TransitionApprovalRequest implements ports.ApprovalRepository (V4-09):
// the fenced CAS a ResolveApproval command and the timer job both race
// against.
func (r approvalRepository) TransitionApprovalRequest(ctx context.Context, req ports.TransitionApprovalRequestRequest) (runtime.ApprovalRequest, error) {
	if req.ApprovalRequestID == "" || req.NextState == "" {
		return runtime.ApprovalRequest{}, errors.New("approval request id and next state are required")
	}
	now := formatWorkflowTime(time.Now())
	var decidedBy, decidedRole, decidedOutcome, reason, decidedAt any
	if req.DecidedBy != "" {
		decidedBy = req.DecidedBy
	}
	if req.DecidedRole != "" {
		decidedRole = req.DecidedRole
	}
	if req.DecidedOutcome != "" {
		decidedOutcome = req.DecidedOutcome
	}
	if req.Reason != "" {
		reason = req.Reason
	}
	if !req.DecidedAt.IsZero() {
		decidedAt = formatWorkflowTime(req.DecidedAt)
	}
	result, err := r.tx.ExecContext(ctx, `
UPDATE approval_requests
SET state = ?, decided_by = COALESCE(?, decided_by), decided_role = COALESCE(?, decided_role),
    decided_outcome = COALESCE(?, decided_outcome), reason = COALESCE(?, reason), decided_at = COALESCE(?, decided_at),
    version = version + 1, updated_at = ?
WHERE id = ? AND state = ? AND version = ?`,
		string(req.NextState), decidedBy, decidedRole, decidedOutcome, reason, decidedAt, now,
		req.ApprovalRequestID, string(req.ExpectedState), req.ExpectedVersion,
	)
	if err != nil {
		return runtime.ApprovalRequest{}, MapSQLiteError(fmt.Errorf("transition approval request %s: %w", req.ApprovalRequestID, err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return runtime.ApprovalRequest{}, fmt.Errorf("read approval request transition result: %w", err)
	}
	if affected != 1 {
		var exists int
		lookupErr := r.tx.QueryRowContext(ctx, `SELECT 1 FROM approval_requests WHERE id = ?`, req.ApprovalRequestID).Scan(&exists)
		if errors.Is(lookupErr, sql.ErrNoRows) {
			return runtime.ApprovalRequest{}, fmt.Errorf("%w: approval request %s", ports.ErrPersistenceNotFound, req.ApprovalRequestID)
		}
		if lookupErr != nil {
			return runtime.ApprovalRequest{}, MapSQLiteError(fmt.Errorf("check stale approval request transition: %w", lookupErr))
		}
		return runtime.ApprovalRequest{}, fmt.Errorf(
			"%w: approval request %s expected %s@%d",
			ports.ErrOptimisticConflict, req.ApprovalRequestID, req.ExpectedState, req.ExpectedVersion,
		)
	}
	return loadApprovalRequestByID(ctx, r.tx, req.ApprovalRequestID)
}
