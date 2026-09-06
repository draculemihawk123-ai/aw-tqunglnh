package ports

import (
	"context"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// ApprovalRepository is V4-09's own Tx accessor (docs/design/06-v4-runtime-engine.md
// V4-09, HE-14-S03/GC-INV-11/HE-08-M08): the durable authority for an
// APPROVAL NodeRun's own pending decision — see runtime.ApprovalRequest's
// own doc comment for the full contract. This task owns approval_requests
// end to end, the same "gets a real interface from the start" treatment
// V4-08's own WaitRepository already established for a concern its own
// task owned outright.
type ApprovalRepository interface {
	// CreateApprovalRequest inserts a new PENDING ApprovalRequest.
	// ErrPersistenceAlreadyExists for a reused ID or a NodeRunID that
	// already has a request (UNIQUE(node_run_id) — at most one request per
	// NodeRun activation, ever).
	CreateApprovalRequest(ctx context.Context, request runtime.ApprovalRequest) (runtime.ApprovalRequest, error)
	// GetApprovalRequest returns the ApprovalRequest named by id, or
	// ErrPersistenceNotFound.
	GetApprovalRequest(ctx context.Context, id string) (runtime.ApprovalRequest, error)
	// ListApprovalRequestsForRun is populated now (V4-12B,
	// docs/design/06-v4-runtime-engine.md): every ApprovalRequest whose own
	// RunID matches — the CANCEL_RUN_COORDINATOR job's own sweep uses this
	// to find every still-PENDING request of a cancelling Run and CAS it
	// CANCELLED. Deliberately unfiltered by state.
	ListApprovalRequestsForRun(ctx context.Context, runID string) ([]runtime.ApprovalRequest, error)

	// TransitionApprovalRequest is the fenced CAS that closes an
	// ApprovalRequest's own race: exactly one of a ResolveApproval command
	// and a firing timer job may ever win it for a given request.
	// ExpectedState/ExpectedVersion mismatch is ErrOptimisticConflict — the
	// exact mechanism a concurrent loser observes, never a silent no-op.
	TransitionApprovalRequest(ctx context.Context, req TransitionApprovalRequestRequest) (runtime.ApprovalRequest, error)
}

// TransitionApprovalRequestRequest is the CAS request for
// ApprovalRepository.TransitionApprovalRequest.
type TransitionApprovalRequestRequest struct {
	ApprovalRequestID string
	ExpectedState     runtime.ApprovalRequestState
	ExpectedVersion   uint64
	NextState         runtime.ApprovalRequestState
	// DecidedBy/DecidedRole/DecidedOutcome/Reason/DecidedAt are required
	// when NextState is runtime.ApprovalRequestDecided, and must be empty/
	// zero otherwise (HE-08-M08's own "MUST ghi actor, cause, previous/new
	// state, time và evidence/approval refs").
	DecidedBy      string
	DecidedRole    string
	DecidedOutcome string
	Reason         string
	DecidedAt      time.Time
}
