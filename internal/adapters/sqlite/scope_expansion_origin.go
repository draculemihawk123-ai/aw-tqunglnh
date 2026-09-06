package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// CreateScopeExpansionOrigin implements ports.RuntimeRepository (V4-12A).
func (r runtimeRepository) CreateScopeExpansionOrigin(ctx context.Context, origin runtimedomain.ScopeExpansionOrigin) (runtimedomain.ScopeExpansionOrigin, error) {
	proposalJSON, err := origin.Proposal.CanonicalJSON()
	if err != nil {
		return runtimedomain.ScopeExpansionOrigin{}, fmt.Errorf("canonicalize scope expansion proposal: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = r.tx.ExecContext(ctx, `
INSERT INTO attempt_scope_expansion_origins (
	attempt_id, node_run_id, run_id, work_item_id, family_id, request_id,
	proposal_json, proposal_hash, reconcile_status, poll_generation, version, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		string(origin.AttemptID), string(origin.NodeRunID), string(origin.RunID), origin.WorkItemID, origin.FamilyID,
		origin.RequestID, string(proposalJSON), origin.ProposalHash, string(origin.ReconcileStatus), origin.PollGeneration,
		origin.Version, now, now,
	)
	if err != nil {
		return runtimedomain.ScopeExpansionOrigin{}, MapSQLiteError(fmt.Errorf("create scope expansion origin %s: %w", origin.AttemptID, err))
	}
	return origin, nil
}

// GetScopeExpansionOriginByAttemptID implements ports.RuntimeRepository (V4-12A).
func (r runtimeRepository) GetScopeExpansionOriginByAttemptID(ctx context.Context, attemptID string) (runtimedomain.ScopeExpansionOrigin, error) {
	return loadScopeExpansionOrigin(ctx, r.tx, "attempt_id", attemptID)
}

// GetScopeExpansionOriginByRequestID implements ports.RuntimeRepository (V4-12A).
func (r runtimeRepository) GetScopeExpansionOriginByRequestID(ctx context.Context, requestID string) (runtimedomain.ScopeExpansionOrigin, error) {
	return loadScopeExpansionOrigin(ctx, r.tx, "request_id", requestID)
}

func loadScopeExpansionOrigin(ctx context.Context, tx *sql.Tx, column, value string) (runtimedomain.ScopeExpansionOrigin, error) {
	var (
		origin               runtimedomain.ScopeExpansionOrigin
		proposalJSON         string
		reactivatedNodeRunID sql.NullString
	)
	row := tx.QueryRowContext(ctx, fmt.Sprintf(`
SELECT attempt_id, node_run_id, run_id, work_item_id, family_id, request_id,
       proposal_json, proposal_hash, reactivated_node_run_id, reconcile_status, poll_generation, version
FROM attempt_scope_expansion_origins WHERE %s = ?`, column), value)
	err := row.Scan(
		&origin.AttemptID, &origin.NodeRunID, &origin.RunID, &origin.WorkItemID, &origin.FamilyID, &origin.RequestID,
		&proposalJSON, &origin.ProposalHash, &reactivatedNodeRunID, &origin.ReconcileStatus, &origin.PollGeneration, &origin.Version,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return runtimedomain.ScopeExpansionOrigin{}, fmt.Errorf("%w: scope expansion origin %s=%s", ports.ErrPersistenceNotFound, column, value)
	}
	if err != nil {
		return runtimedomain.ScopeExpansionOrigin{}, fmt.Errorf("load scope expansion origin: %w", err)
	}
	var decodedProposal struct {
		RequestedGrants []runtimedomain.ScopeGrantProposal `json:"requestedGrants"`
		Reason          string                             `json:"reason"`
	}
	if err := json.Unmarshal([]byte(proposalJSON), &decodedProposal); err != nil {
		return runtimedomain.ScopeExpansionOrigin{}, fmt.Errorf("decode scope expansion origin proposal: %w", err)
	}
	origin.Proposal = runtimedomain.ScopeExpansionProposal{RequestedGrants: decodedProposal.RequestedGrants, Reason: decodedProposal.Reason}
	if reactivatedNodeRunID.Valid {
		ref := runtimedomain.NodeRunID(reactivatedNodeRunID.String)
		origin.ReactivatedNodeRunID = &ref
	}
	return origin, nil
}

// TransitionScopeExpansionOrigin implements ports.RuntimeRepository (V4-12A).
func (r runtimeRepository) TransitionScopeExpansionOrigin(ctx context.Context, req ports.TransitionScopeExpansionOriginRequest) (runtimedomain.ScopeExpansionOrigin, error) {
	if req.AttemptID == "" {
		return runtimedomain.ScopeExpansionOrigin{}, errors.New("scope expansion origin attempt id is required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var nextPollGeneration, nextReactivatedNodeRunID any
	if req.NextPollGeneration != nil {
		nextPollGeneration = *req.NextPollGeneration
	}
	if req.NextReactivatedNodeRunID != nil {
		nextReactivatedNodeRunID = *req.NextReactivatedNodeRunID
	}
	result, err := r.tx.ExecContext(ctx, `
UPDATE attempt_scope_expansion_origins
SET reconcile_status = ?, poll_generation = COALESCE(?, poll_generation),
    reactivated_node_run_id = COALESCE(?, reactivated_node_run_id), version = version + 1, updated_at = ?
WHERE attempt_id = ? AND version = ?`,
		string(req.NextReconcileStatus), nextPollGeneration, nextReactivatedNodeRunID, now, req.AttemptID, req.ExpectedVersion,
	)
	if err != nil {
		return runtimedomain.ScopeExpansionOrigin{}, MapSQLiteError(fmt.Errorf("transition scope expansion origin %s: %w", req.AttemptID, err))
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return runtimedomain.ScopeExpansionOrigin{}, fmt.Errorf("read scope expansion origin transition result: %w", err)
	}
	if affected != 1 {
		var exists int
		lookupErr := r.tx.QueryRowContext(ctx, `SELECT 1 FROM attempt_scope_expansion_origins WHERE attempt_id = ?`, req.AttemptID).Scan(&exists)
		if errors.Is(lookupErr, sql.ErrNoRows) {
			return runtimedomain.ScopeExpansionOrigin{}, fmt.Errorf("%w: scope expansion origin %s", ports.ErrPersistenceNotFound, req.AttemptID)
		}
		if lookupErr != nil {
			return runtimedomain.ScopeExpansionOrigin{}, MapSQLiteError(fmt.Errorf("check stale scope expansion origin transition: %w", lookupErr))
		}
		return runtimedomain.ScopeExpansionOrigin{}, fmt.Errorf(
			"%w: scope expansion origin %s expected version %d",
			ports.ErrOptimisticConflict, req.AttemptID, req.ExpectedVersion,
		)
	}
	return loadScopeExpansionOrigin(ctx, r.tx, "attempt_id", req.AttemptID)
}
