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

	// PutApprovalChecklist (V12-31, ADR-063) stores the QA checklist of a decided checklist approval; ErrPersistenceAlreadyExists
	// when the request already has one.
	PutApprovalChecklist(ctx context.Context, record ApprovalChecklistRecord) error
	// ListApprovalChecklistsForRun returns the checklists of a run's approvals, oldest first.
	ListApprovalChecklistsForRun(ctx context.Context, runID string) ([]ApprovalChecklistRecord, error)
}

// ApprovalChecklistRecord is the QA_CHECKLIST record of one checklist approval (V12-31). ItemsJSON is the items as decided.
type ApprovalChecklistRecord struct {
	ApprovalRequestID string
	ProjectID         string
	RunID             string
	NodeRunID         string
	WorkItemID        string
	// Verdict is PASSED (every item passed) or FAILED.
	Verdict   string
	ItemsJSON string
	DecidedBy string
	CreatedAt time.Time
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
