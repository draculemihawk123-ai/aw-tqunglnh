package runtime

import (
	"errors"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// ApprovalRequestID identifies one approval_requests row.
type ApprovalRequestID string

// ApprovalRequestState is the closed set of states an ApprovalRequest may
// reach (V4-09, HE-14-S03/GC-INV-11/HE-08-M08). PENDING is the only
// non-terminal one — durable_jobs (the timer job) only ever wakes
// processing up at DueAt; it is never itself the authority over whether a
// request is still PENDING, which a fenced CAS on this row's own
// State/Version always is (the same GC-INV-31-style discipline V4-08's own
// WaitRegistrationState already established for a different node type).
type ApprovalRequestState string

const (
	ApprovalRequestPending ApprovalRequestState = "PENDING"
	// ApprovalRequestDecided is a request resolved by a real, authorized
	// operator decision — DecidedBy/DecidedRole/DecidedOutcome/DecidedAt
	// are always set here.
	ApprovalRequestDecided ApprovalRequestState = "DECIDED"
	// ApprovalRequestEscalated is a request whose own TimeoutSeconds
	// ceiling elapsed with no decision ever recorded — routed via
	// EscalationOutcome. Decided* fields stay unset: an escalation has no
	// deciding actor by definition.
	ApprovalRequestEscalated ApprovalRequestState = "ESCALATED"
	ApprovalRequestCancelled ApprovalRequestState = "CANCELLED"
)

// ApprovalRequest is the durable authority for one APPROVAL NodeRun
// activation's own pending decision (V4-09, HE-14-S03/GC-INV-11/HE-08-M08).
// At most one exists per NodeRunID — a fresh NodeRun activation (a
// rework/reactivation, V4-07) always gets its own fresh request, never a
// resurrected one.
//
// AuthorizedRoles/RequestedEvidenceKinds/EscalationOutcome are the exact
// values this request's own node pinned in its compiled WorkflowVersion at
// the moment this request was created (workflow.ApprovalNodeConfig) — a
// ResolveApproval command never re-reads the WorkflowVersion to re-derive
// them. DueAt is always set (ApprovalNodeConfig.TimeoutSeconds is itself
// always required and positive, unlike WAIT's own optional ceiling), so
// every request always gets a timer job.
type ApprovalRequest struct {
	ID                     ApprovalRequestID
	ProjectID              project.ProjectID
	RunID                  WorkflowRunID
	NodeRunID              NodeRunID
	NodeKey                string
	AuthorizedRoles        []string
	RequestedEvidenceKinds []string
	DueAt                  time.Time
	EscalationOutcome      string
	State                  ApprovalRequestState
	DecidedBy              string
	DecidedRole            string
	DecidedOutcome         string
	Reason                 string
	DecidedAt              *time.Time
	Version                uint64
}

// NewApprovalRequest validates and builds a new PENDING ApprovalRequest at
// version 1.
func NewApprovalRequest(
	id ApprovalRequestID, projectID project.ProjectID, runID WorkflowRunID, nodeRunID NodeRunID, nodeKey string,
	authorizedRoles, requestedEvidenceKinds []string, dueAt time.Time, escalationOutcome string,
) (ApprovalRequest, error) {
	nodeKey = strings.TrimSpace(nodeKey)
	escalationOutcome = strings.TrimSpace(escalationOutcome)
	if id == "" || projectID == "" || runID == "" || nodeRunID == "" || nodeKey == "" {
		return ApprovalRequest{}, errors.New("approval request identities are required")
	}
	if len(authorizedRoles) == 0 {
		return ApprovalRequest{}, errors.New("approval request must have at least one authorized role")
	}
	if escalationOutcome == "" {
		return ApprovalRequest{}, errors.New("approval request escalation outcome is required")
	}
	if dueAt.IsZero() {
		return ApprovalRequest{}, errors.New("approval request due time is required")
	}
	return ApprovalRequest{
		ID: id, ProjectID: projectID, RunID: runID, NodeRunID: nodeRunID, NodeKey: nodeKey,
		AuthorizedRoles: append([]string(nil), authorizedRoles...), RequestedEvidenceKinds: append([]string(nil), requestedEvidenceKinds...),
		DueAt: dueAt.UTC(), EscalationOutcome: escalationOutcome, State: ApprovalRequestPending, Version: 1,
	}, nil
}
