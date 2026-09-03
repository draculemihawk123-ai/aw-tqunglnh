// Scope expansion request/approval (V3-08,
// docs/design/05-v3-project-workspace.md; docs/architecture/04-go-core-spec.md
// §8's command table: "RequestScopeExpansion | Block node và tạo request
// add-only", "ApproveScopeExpansion | Tăng ScopeVersion, provision rồi append
// amendment/reactivate", "RejectScopeExpansion | Ghi decision, route block/
// escalation", "WithdrawScopeExpansion | Thu hồi request đang PENDING;
// idempotent, không tạo grant/amendment"; docs/design/01-system-design.md
// §6.1's own row: "scope_expansion_requests | id, family, requested grants,
// reason, status, actor/version | only add/upgrade; approval required";
// AK-ARCH-015A: "Scope được duyệt thêm không đổi quyền của Attempt cũ;
// activation mới pin đúng RunManifestAmendment và sibling không tự nhận
// quyền").
//
// This is a strictly ADD-only workflow layered on top of RepositoryScope's
// own already-add-only design (this file's sibling, work.go, and its own
// "Scope family là add-only khi family đang chạy" doc comment): a
// ScopeExpansionRequest never mutates or removes any existing
// RepositoryScope row, it only ever proposes candidate grants that, once
// approved, become brand-new RepositoryScope rows at a brand-new, strictly
// higher AddedInScopeVersion (internal/app/work.ApproveScopeExpansion, the
// real, I/O-capable command that turns a candidate into a real grant — this
// package's own constructors stay pure, exactly like NewRepositoryScope/
// NewTaskFamily/ValidateEffectiveScopes above).
//
// RequestedGrant deliberately does NOT reuse RepositoryScope's own type: a
// pending request's candidates are not yet real grants — RepositoryScope's
// own constructor (NewRepositoryScope) requires a concrete
// AddedInScopeVersion greater than zero, an AddedBy actor and an AddedAt
// timestamp, all three of which only become known at APPROVAL time (the
// actor/time that matter for the audit trail are the approver's, not the
// requester's — AK-ARCH-015A's own "activation mới pin đúng ... scope version
// mới" is exactly this: the grant's real identity is pinned by the decision,
// not the ask). Forcing a not-yet-decided candidate through NewRepositoryScope
// would mean fabricating an AddedInScopeVersion nobody has decided yet, or
// bypassing that constructor's own invariants outright — neither is honest.
// RequestedGrant's own shape (RepositoryID/Access/PathScopes/Reason) is
// exactly RepositoryScope's shape minus the three approval-time-only fields;
// internal/app/work.ApproveScopeExpansion is the one place a RequestedGrant
// ever turns into a real RepositoryScope, via the exact same NewRepositoryScope
// constructor CreateRootWorkItem/CreateChildWorkItem already call.
package work

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// ScopeExpansionRequestID identifies one ScopeExpansionRequest.
type ScopeExpansionRequestID string

// ScopeExpansionStatus is a ScopeExpansionRequest's own closed lifecycle —
// the same "closed enum via a plain string type + an explicit legal-edges
// map" discipline WorkItemStatus/RepositoryStatus/WorkspaceSetState already
// follow in this codebase, checked by CanTransitionScopeExpansionStatus
// below (mirroring project.CanTransitionRepositoryStatus's own exact
// pattern).
type ScopeExpansionStatus string

const (
	// ScopeExpansionPending is every ScopeExpansionRequest's starting status
	// (NewScopeExpansionRequest always returns this): a request exists, no
	// decision has been made yet.
	ScopeExpansionPending ScopeExpansionStatus = "PENDING"
	// ScopeExpansionApproved is a terminal decision: the requested grants
	// became real RepositoryScope rows at a new AddedInScopeVersion
	// (internal/app/work.ApproveScopeExpansion). Reachable only from PENDING.
	ScopeExpansionApproved ScopeExpansionStatus = "APPROVED"
	// ScopeExpansionRejected is a terminal decision: no scope change, the
	// decision itself is recorded as an audit fact. Reachable only from
	// PENDING.
	ScopeExpansionRejected ScopeExpansionStatus = "REJECTED"
	// ScopeExpansionWithdrawn is a terminal decision: the requester (or an
	// operator) revoked a still-undecided request before anyone approved or
	// rejected it. Reachable only from PENDING. Unlike Approved/Rejected,
	// WithdrawScopeExpansion's own contract ("idempotent") means a second
	// withdraw attempt against an already-WITHDRAWN request is a harmless
	// no-op at the application layer, not a rejected transition here — see
	// internal/app/work.WithdrawScopeExpansion's own doc comment for exactly
	// where that idempotency is handled (one layer above this package's own
	// strict transition-legality check, the same layering
	// ValidateReadinessGate/ValidateEffectiveScopes already establish between
	// "pure structural rule" and "the real command that applies it").
	ScopeExpansionWithdrawn ScopeExpansionStatus = "WITHDRAWN"
)

// ErrIllegalScopeExpansionTransition is returned by
// CanTransitionScopeExpansionStatus for any (from, to) pair that is not one
// of PENDING's three legal exits.
var ErrIllegalScopeExpansionTransition = errors.New("work: illegal scope expansion request status transition")

// legalScopeExpansionTransitions is the complete, closed set of
// ScopeExpansionStatus transitions this task declares legal: PENDING is the
// only non-terminal status, and it has exactly three legal exits —
// APPROVED, REJECTED, WITHDRAWN — mirroring
// project.legalRepositoryTransitions' own exact map-of-sets shape.
var legalScopeExpansionTransitions = map[ScopeExpansionStatus]map[ScopeExpansionStatus]bool{
	ScopeExpansionPending: {
		ScopeExpansionApproved:  true,
		ScopeExpansionRejected:  true,
		ScopeExpansionWithdrawn: true,
	},
}

// CanTransitionScopeExpansionStatus reports whether (from, to) is one of
// this lifecycle's explicitly allowed edges — see
// project.CanTransitionRepositoryStatus's own doc comment for the identical
// reasoning, applied here to ScopeExpansionStatus instead of
// RepositoryStatus.
func CanTransitionScopeExpansionStatus(from, to ScopeExpansionStatus) error {
	edges, known := legalScopeExpansionTransitions[from]
	if !known {
		return fmt.Errorf("%w: unknown status %q", ErrIllegalScopeExpansionTransition, from)
	}
	if from == to {
		return fmt.Errorf("%w: %s -> %s is a no-op, not a transition", ErrIllegalScopeExpansionTransition, from, to)
	}
	if edges[to] {
		return nil
	}
	return fmt.Errorf("%w: %s -> %s", ErrIllegalScopeExpansionTransition, from, to)
}

// RequestedGrant is one candidate repository grant named by a
// ScopeExpansionRequest — see this file's own top-of-file doc comment for
// why this is its own shape rather than a not-yet-approved RepositoryScope.
// PathScopes is already normalized (path.Clean'd, deduplicated, sorted) by
// NewScopeExpansionRequest below, the identical normalizePathScopes helper
// NewRepositoryScope already uses.
type RequestedGrant struct {
	RepositoryID project.RepositoryID `json:"repositoryId"`
	Access       RepositoryAccess     `json:"access"`
	PathScopes   []string             `json:"pathScopes,omitempty"`
	Reason       string               `json:"reason"`
}

// ScopeExpansionRequest is a task family's own add-only scope expansion ask
// (docs/design/01-system-design.md §6.1's own row). ReferencedWorkItemID is
// this task's own "block WorkItem/run reference nếu có" judgment call: the
// WorkItem (in the same family) whose own activity needs the wider scope,
// when the caller names one. It is deliberately optional — a "run
// reference" (a live NodeRun/Attempt) cannot exist yet in this codebase, no
// runtime engine exists until V4 — and, when present, is only ever a
// structural pointer this package can validate resolves to a real WorkItem
// in the same family (internal/app/work.RequestScopeExpansion's own I/O-
// capable job); this package never itself drives that WorkItem's own
// WorkItemStatus toward BLOCKED, and never creates any blockers-table row —
// that readiness/blocker evidence mechanism belongs to a different,
// concurrently-built package entirely (V3-07), never internal/domain/work.
type ScopeExpansionRequest struct {
	ID              ScopeExpansionRequestID
	FamilyID        TaskFamilyID
	ProjectID       project.ProjectID
	RequestedGrants []RequestedGrant
	Reason          string
	// ReferencedWorkItemID is nil unless the request named one — see this
	// struct's own doc comment above.
	ReferencedWorkItemID *WorkItemID

	Status      ScopeExpansionStatus
	RequestedBy string
	RequestedAt time.Time

	// DecidedBy/DecidedAt/DecisionNote are all zero-valued until a decision
	// is recorded (ApproveScopeExpansion/RejectScopeExpansion/
	// WithdrawScopeExpansion) — mirroring RepositoryWorkspace.
	// LastProvisionErrorCode's own "nil until the one moment it applies"
	// discipline, applied here to a decision instead of a failure.
	DecidedBy    string
	DecidedAt    *time.Time
	DecisionNote string
	// ApprovedScopeVersion is set only once, only by ApproveScopeExpansion,
	// to the TaskFamily's own new ScopeVersion this request's grants were
	// persisted at (internal/app/work.ApproveScopeExpansion's own doc
	// comment names the exact bump-then-write ordering that makes this
	// value correct). nil for every PENDING/REJECTED/WITHDRAWN request,
	// forever.
	ApprovedScopeVersion *uint64

	Version uint64
}

// NewScopeExpansionRequest validates and constructs a brand-new PENDING
// ScopeExpansionRequest. family must already be a real, loaded TaskFamily
// (the same "caller already had to build/load the domain value for its own
// purposes before persisting it" discipline ports.WorkRepository's own doc
// comment already establishes for this package's other constructors) —
// same-project/ACTIVE-repository validation for each requested repository is
// deliberately NOT this constructor's job, for the identical reason
// CreateRootWorkItem's own doc comment gives for its own ACTIVE check: it
// needs a real Repository lookup this pure package has no I/O for.
// internal/app/work.RequestScopeExpansion is the one real caller, running
// that check first via tx.Catalog() exactly like CreateRootWorkItem already
// does.
func NewScopeExpansionRequest(
	id ScopeExpansionRequestID,
	family TaskFamily,
	grants []RequestedGrant,
	reason string,
	referencedWorkItemID *WorkItemID,
	requestedBy string,
	requestedAt time.Time,
) (ScopeExpansionRequest, error) {
	if id == "" {
		return ScopeExpansionRequest{}, errors.New("scope expansion request id is required")
	}
	if family.ID == "" || family.ProjectID == "" {
		return ScopeExpansionRequest{}, errors.New("scope expansion request task family is invalid")
	}
	if len(grants) == 0 {
		return ScopeExpansionRequest{}, errors.New("scope expansion request requires at least one requested grant")
	}
	reason = strings.TrimSpace(reason)
	requestedBy = strings.TrimSpace(requestedBy)
	if reason == "" || requestedBy == "" || requestedAt.IsZero() {
		return ScopeExpansionRequest{}, errors.New("scope expansion request reason, actor and timestamp are required")
	}
	if referencedWorkItemID != nil && strings.TrimSpace(string(*referencedWorkItemID)) == "" {
		return ScopeExpansionRequest{}, errors.New("scope expansion request referenced work item id is blank")
	}

	normalizedGrants := make([]RequestedGrant, 0, len(grants))
	seen := make(map[project.RepositoryID]bool, len(grants))
	for _, grant := range grants {
		if grant.RepositoryID == "" {
			return ScopeExpansionRequest{}, errors.New("scope expansion request grant repository id is required")
		}
		if seen[grant.RepositoryID] {
			return ScopeExpansionRequest{}, fmt.Errorf("duplicate repository %q in requested grants", grant.RepositoryID)
		}
		seen[grant.RepositoryID] = true
		if grant.Access != RepositoryRead && grant.Access != RepositoryWrite {
			return ScopeExpansionRequest{}, fmt.Errorf("unsupported repository access %q", grant.Access)
		}
		normalizedPaths, err := normalizePathScopes(grant.PathScopes)
		if err != nil {
			return ScopeExpansionRequest{}, err
		}
		grantReason := strings.TrimSpace(grant.Reason)
		if grantReason == "" {
			return ScopeExpansionRequest{}, errors.New("scope expansion request grant reason is required")
		}
		normalizedGrants = append(normalizedGrants, RequestedGrant{
			RepositoryID: grant.RepositoryID, Access: grant.Access, PathScopes: normalizedPaths, Reason: grantReason,
		})
	}

	return ScopeExpansionRequest{
		ID: id, FamilyID: family.ID, ProjectID: family.ProjectID, RequestedGrants: normalizedGrants,
		Reason: reason, ReferencedWorkItemID: referencedWorkItemID,
		Status: ScopeExpansionPending, RequestedBy: requestedBy, RequestedAt: requestedAt.UTC(),
		Version: 1,
	}, nil
}
