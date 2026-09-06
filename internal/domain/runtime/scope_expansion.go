// V4-12A's own domain layer (docs/design/06-v4-runtime-engine.md,
// AK-ARCH-015A): "apply scope expansion đã duyệt mà không sửa quyền lịch sử
// hoặc mở quyền cho sibling." ScopeExpansionProposal is what an executor
// hands back through ports.NodeExecutionResult when a running Attempt
// discovers it needs scope it was never granted; ScopeExpansionOrigin is
// the durable link — created inline, in the SAME fenced transaction that
// CASes the Attempt/NodeRun to BLOCKED — between that one Attempt and the
// real work.ScopeExpansionRequest raised on its behalf (V3-08 owns the
// request/approval commands themselves; this package only ever links to
// one by RequestID string, never imports internal/app/work or
// internal/domain/work's own ScopeExpansionRequest type, to avoid a
// runtime<->work domain coupling neither side needs).
package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

// ScopeGrantProposal is one repository grant an executor's own
// ScopeExpansionProposal asks for — the runtime-domain mirror of
// internal/app/work's own ScopeGrantRequest (that package's own request
// DTO), kept as its own type rather than imported: this package must never
// depend on internal/app/work (an app-layer package), only ever on sibling
// domain packages.
type ScopeGrantProposal struct {
	RepositoryID string
	Access       string
	PathScopes   []string
	Reason       string
}

// ScopeExpansionProposal is what ports.NodeExecutionResult carries when an
// executor reports State ExecutionAttemptBlocked with
// TerminationReason SCOPE_EXPANSION_REQUIRED (V4-12A, confirmed with the
// user before writing this task's code): the executor only ever PROPOSES —
// this is a candidate, canonicalized and validated by
// FinalizeExecutionAttempt before anything durable is built from it, never
// itself a grant. RequestedGrants must be non-empty; Reason is required
// (the same "block WorkItem/run reference nếu có... Reason" shape
// RequestScopeExpansionRequest already requires at the command layer).
type ScopeExpansionProposal struct {
	RequestedGrants []ScopeGrantProposal
	Reason          string
}

// Validate reports whether p is well-formed enough to become a real
// work.RequestScopeExpansion command — FinalizeExecutionAttempt's own
// BLOCKED branch rejects a malformed proposal as OUTCOME_REJECTED rather
// than ever creating a ScopeExpansionOrigin/durable BLOCKED state from it
// (confirmed with the user: "Shape sai → OUTCOME_REJECTED, không tạo scope
// request").
func (p *ScopeExpansionProposal) Validate() error {
	if p == nil {
		return errors.New("runtime: scope expansion proposal is required")
	}
	if strings.TrimSpace(p.Reason) == "" {
		return errors.New("runtime: scope expansion proposal reason is required")
	}
	if len(p.RequestedGrants) == 0 {
		return errors.New("runtime: scope expansion proposal requires at least one requested grant")
	}
	seen := make(map[string]bool, len(p.RequestedGrants))
	for _, grant := range p.RequestedGrants {
		if strings.TrimSpace(grant.RepositoryID) == "" {
			return errors.New("runtime: scope expansion proposal grant repository id is required")
		}
		if grant.Access != "READ" && grant.Access != "WRITE" {
			return errors.New("runtime: scope expansion proposal grant access must be READ or WRITE")
		}
		if seen[grant.RepositoryID] {
			return errors.New("runtime: scope expansion proposal has a duplicate repository id")
		}
		seen[grant.RepositoryID] = true
	}
	return nil
}

// CanonicalJSON returns the deterministic JSON encoding this package's own
// ScopeExpansionOrigin.ProposalHash is computed from — sorted grants by
// RepositoryID so two logically-identical proposals (same grants, built in
// a different slice order) always hash identically.
func (p ScopeExpansionProposal) CanonicalJSON() ([]byte, error) {
	sorted := append([]ScopeGrantProposal(nil), p.RequestedGrants...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j-1].RepositoryID > sorted[j].RepositoryID; j-- {
			sorted[j-1], sorted[j] = sorted[j], sorted[j-1]
		}
	}
	return json.Marshal(struct {
		RequestedGrants []ScopeGrantProposal `json:"requestedGrants"`
		Reason          string               `json:"reason"`
	}{RequestedGrants: sorted, Reason: p.Reason})
}

// ScopeExpansionReconcileStatus is ScopeExpansionOrigin's own closed
// status set (V4-12A) — the SCOPE_EXPANSION_RECONCILE job's own
// self-rescheduling state machine writes it, confirmed with the user
// before writing this task's code.
type ScopeExpansionReconcileStatus string

const (
	// ScopeExpansionReconcilePending: still waiting on a human decision
	// (RequestScopeExpansion/ApproveScopeExpansion/RejectScopeExpansion),
	// or on WorkspaceSet provisioning after approval.
	ScopeExpansionReconcilePending ScopeExpansionReconcileStatus = "PENDING"
	// ScopeExpansionReconcileReactivated: a new NodeRun activation exists
	// (ReactivatedNodeRunID is set) — terminal, successful outcome.
	ScopeExpansionReconcileReactivated ScopeExpansionReconcileStatus = "REACTIVATED"
	// ScopeExpansionReconcileRejected: the underlying request was
	// REJECTED or WITHDRAWN — terminal; the Attempt/NodeRun/Run stay
	// BLOCKED (Alpha has no automatic resume path — CancelRun is the
	// only way out).
	ScopeExpansionReconcileRejected ScopeExpansionReconcileStatus = "REJECTED"
	// ScopeExpansionReconcileNeedsRecovery: the family's own WorkspaceSet
	// reached a terminal, non-READY state (BLOCKED/RELEASING/RELEASED) —
	// terminal from this job's own perspective; never polled again,
	// needs operator/recovery attention instead.
	ScopeExpansionReconcileNeedsRecovery ScopeExpansionReconcileStatus = "NEEDS_RECOVERY"
)

// ScopeExpansionOriginID names one ScopeExpansionOrigin by the
// ExecutionAttemptID it is keyed on — a distinct named type (rather than a
// bare ExecutionAttemptID reuse at every call site) purely for readability
// at this package's own call sites; the underlying value is always exactly
// the origin Attempt's own ID.
type ScopeExpansionOriginID = ExecutionAttemptID

// ScopeExpansionOrigin is the durable link between one BLOCKED
// ExecutionAttempt and the real work.ScopeExpansionRequest raised on its
// behalf (V4-12A). RequestID is RESERVED — minted and persisted in the SAME
// transaction that CASes the Attempt/NodeRun to BLOCKED, before the
// REQUEST_SCOPE_EXPANSION job ever calls the real
// work.RequestScopeExpansion command with that exact ID — confirmed with
// the user before writing this task's code: backfilling RequestID only
// after a real request existed would leave a crash window where an
// approved request has no way back to the Attempt that needed it.
type ScopeExpansionOrigin struct {
	AttemptID            ScopeExpansionOriginID
	NodeRunID            NodeRunID
	RunID                WorkflowRunID
	WorkItemID           string
	FamilyID             string
	RequestID            string
	Proposal             ScopeExpansionProposal
	ProposalHash         string
	ReactivatedNodeRunID *NodeRunID
	ReconcileStatus      ScopeExpansionReconcileStatus
	PollGeneration       uint64
	Version              uint64
}

// NewScopeExpansionOrigin validates and builds a new origin record, always
// starting ScopeExpansionReconcilePending with PollGeneration 0 and no
// ReactivatedNodeRunID — every other state is reached only through
// TransitionScopeExpansionOrigin's own fenced CAS.
func NewScopeExpansionOrigin(
	attemptID ScopeExpansionOriginID, nodeRunID NodeRunID, runID WorkflowRunID, workItemID, familyID, requestID string,
	proposal ScopeExpansionProposal,
) (ScopeExpansionOrigin, error) {
	if attemptID == "" || nodeRunID == "" || runID == "" || strings.TrimSpace(workItemID) == "" || strings.TrimSpace(familyID) == "" {
		return ScopeExpansionOrigin{}, errors.New("runtime: scope expansion origin identities are required")
	}
	if strings.TrimSpace(requestID) == "" {
		return ScopeExpansionOrigin{}, errors.New("runtime: scope expansion origin request id is required")
	}
	if err := proposal.Validate(); err != nil {
		return ScopeExpansionOrigin{}, err
	}
	canonical, err := proposal.CanonicalJSON()
	if err != nil {
		return ScopeExpansionOrigin{}, err
	}
	digest := sha256.Sum256(canonical)
	return ScopeExpansionOrigin{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, WorkItemID: workItemID, FamilyID: familyID,
		RequestID: requestID, Proposal: proposal, ProposalHash: "sha256:" + hex.EncodeToString(digest[:]),
		ReconcileStatus: ScopeExpansionReconcilePending, Version: 1,
	}, nil
}
