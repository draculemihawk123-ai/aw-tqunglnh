package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// GetWaitRegistrationByNodeRunID and GetApprovalRequestByNodeRunID are
// V4-14's own additive test-support queries (docs/design/06-v4-runtime-engine.md,
// Runtime engine acceptance gate): a caller driving a real WAIT/APPROVAL
// node through a real workerpool.Pool (rather than reading AdvanceRun's
// own in-memory hop.NextWaitRegistrationID/NextApprovalRequestID return
// value directly, which a job-dispatched run never gets to see) needs a
// durable way to find the registration/request a specific NodeRun
// activation produced. wait_registrations/approval_requests both carry a
// UNIQUE(node_run_id) constraint (V4-08/V4-09's own CreateWaitRegistration/
// CreateApprovalRequest doc comments) — at most one row per NodeRun
// activation — so this is never ambiguous.

func (s *Store) GetWaitRegistrationByNodeRunID(ctx context.Context, nodeRunID string) (runtime.WaitRegistration, error) {
	var id string
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM wait_registrations WHERE node_run_id = ?`, nodeRunID).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return runtime.WaitRegistration{}, fmt.Errorf("%w: no wait registration for node run %s", ports.ErrPersistenceNotFound, nodeRunID)
		}
		return runtime.WaitRegistration{}, MapSQLiteError(fmt.Errorf("find wait registration for node run %s: %w", nodeRunID, err))
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return runtime.WaitRegistration{}, fmt.Errorf("begin read wait registration %s: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()
	return loadWaitRegistrationByID(ctx, tx, id)
}

func (s *Store) GetApprovalRequestByNodeRunID(ctx context.Context, nodeRunID string) (runtime.ApprovalRequest, error) {
	var id string
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM approval_requests WHERE node_run_id = ?`, nodeRunID).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return runtime.ApprovalRequest{}, fmt.Errorf("%w: no approval request for node run %s", ports.ErrPersistenceNotFound, nodeRunID)
		}
		return runtime.ApprovalRequest{}, MapSQLiteError(fmt.Errorf("find approval request for node run %s: %w", nodeRunID, err))
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return runtime.ApprovalRequest{}, fmt.Errorf("begin read approval request %s: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()
	return loadApprovalRequestByID(ctx, tx, id)
}
