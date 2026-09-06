// WorkItemBlocker (V4-12C, docs/design/06-v4-runtime-engine.md; ADR-020)
// is the durable, typed reason a WorkItem sits BLOCKED, and the only path
// out: a WorkItem stays BLOCKED for as long as it has at least one blocker
// whose own State is OPEN, and CancelWorkItem/ResolveWorkItemBlocker are the
// only two commands with authority to change that.
//
// BlockerType deliberately stays a plain, package-local string type rather
// than importing runtime.TerminationReason for its values — the same reason
// WorkItem.SourceNodeRunID stays a package-local string instead of
// runtime.NodeRunID (see that field's own doc comment): internal/domain/runtime
// already imports this package, so this package importing runtime back
// would be a cycle. Every constant below is a literal duplicate of the
// matching runtime.TerminationReason value (ADR-020 §22's own "ma trận
// state-reason"), the identical "hardcoded literal duplicated rather than
// importing the owning package" treatment ports.ClassifyJobKind's own
// CONTROL allow-list (V4-12B, internal/app/ports/job_class.go) already
// established for the same cross-package-cycle reason.
package work

import (
	"errors"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// BlockerID identifies one durable WorkItemBlocker row.
type BlockerID string

// BlockerType is the closed set of reasons a blocker exists — ADR-020's own
// blocker-type table. Only RUN_CANCELLED and SCOPE_EXPANSION_REQUIRED have a
// real producer in this codebase today (V4-12C, V4-12B/V4-12A respectively);
// the remaining five are declared now so ResolveWorkItemBlocker's own
// resolution-mode authority matrix (Waivable/ResolvableViaCommand below) is
// correct ahead of whichever later task first produces one (V5-08 admission
// enforcement for the four admission reasons, V5-11 CompletionPolicy for
// COMPLETION_POLICY_FAILED) — the same "pin the whole reference enum up
// front" treatment runtime.TerminationReason's own doc comment already
// describes for itself.
type BlockerType string

const (
	// BlockerRunCancelled: CancelRun/CancelWorkItem quiesced this WorkItem's
	// own Run to CANCELLED (V4-12C's own real producer,
	// internal/app/runtime/completion.go's transitionRunToCancelledTx).
	BlockerRunCancelled BlockerType = "RUN_CANCELLED"
	// BlockerCompletionPolicyFailed: CompletionPolicy returned FAIL
	// (ADR-021 §23's own outcome table) — no real producer exists yet
	// (V5-11's own scope); declared now so the resolution-mode matrix
	// already has the right answer once one does.
	BlockerCompletionPolicyFailed BlockerType = "COMPLETION_POLICY_FAILED"
	// BlockerScopeExpansionRequired: a NodeRun/Attempt is BLOCKED awaiting
	// an approved scope expansion (V4-12A's own real producer,
	// internal/app/runtime/finalize.go's requestScopeExpansionTx, wired to
	// this blocker type now — V4-12C).
	BlockerScopeExpansionRequired BlockerType = "SCOPE_EXPANSION_REQUIRED"
	// BlockerIsolationEnforcementUnavailable: admission blocker group
	// (ADR-023) — no real producer yet (V5's own admission-enforcement
	// scope).
	BlockerIsolationEnforcementUnavailable BlockerType = "ISOLATION_ENFORCEMENT_UNAVAILABLE"
	// BlockerAdapterBuildDrift: admission blocker group (ADR-022) — no real
	// producer yet.
	BlockerAdapterBuildDrift BlockerType = "ADAPTER_BUILD_DRIFT"
	// BlockerCapabilityRequirementUnsatisfied: admission blocker group — no
	// real producer yet.
	BlockerCapabilityRequirementUnsatisfied BlockerType = "CAPABILITY_REQUIREMENT_UNSATISFIED"
	// BlockerWriteCapabilityOrGrantMissing: admission blocker group — no
	// real producer yet.
	BlockerWriteCapabilityOrGrantMissing BlockerType = "WRITE_CAPABILITY_OR_GRANT_MISSING"
)

var knownBlockerTypes = map[BlockerType]struct{}{
	BlockerRunCancelled:                     {},
	BlockerCompletionPolicyFailed:           {},
	BlockerScopeExpansionRequired:           {},
	BlockerIsolationEnforcementUnavailable:  {},
	BlockerAdapterBuildDrift:                {},
	BlockerCapabilityRequirementUnsatisfied: {},
	BlockerWriteCapabilityOrGrantMissing:    {},
}

// IsValid reports whether t is a member of the closed BlockerType set.
func (t BlockerType) IsValid() bool {
	_, ok := knownBlockerTypes[t]
	return ok
}

// Waivable reports whether ResolveWorkItemBlocker may ever WAIVE a blocker
// of type t — ADR-020's own resolution-mode × blocker-type table: only
// RUN_CANCELLED and COMPLETION_POLICY_FAILED can ever be waived. The four
// admission reasons and SCOPE_EXPANSION_REQUIRED can never be waived — an
// admission/isolation/build-pin failure or an in-flight scope negotiation is
// never something an operator can simply declare away; the only exits are
// fixing the underlying condition (admission reasons — a future
// RetryBlockedActivation, ADR-020's own §22 "RetryBlockedActivation") or
// letting the real approval/reconcile flow resolve it (scope expansion, see
// ResolvableViaCommand below).
func (t BlockerType) Waivable() bool {
	return t == BlockerRunCancelled || t == BlockerCompletionPolicyFailed
}

// ResolvableViaCommand reports whether ResolveWorkItemBlocker has any
// authority at all over a blocker of type t, in either resolution mode.
// SCOPE_EXPANSION_REQUIRED is the one type this rejects outright (confirmed
// with the user before writing this file): resolving it is never a generic
// operator decision — only the real approval/reconcile flow (ADR-011,
// internal/app/runtime's own reactivateBlockedNodeRunTx, V4-12A/V4-12C) may
// ever transition it OPEN->RESOLVED, the moment a reactivated NodeRun
// activation is actually created. Every other type (including the four
// admission reasons, which have no real producer yet) accepts a plain
// RESOLVED via this command; only Waivable above further restricts WAIVED.
func (t BlockerType) ResolvableViaCommand() bool {
	return t != BlockerScopeExpansionRequired
}

// BlockerState is a WorkItemBlocker's own three-state lifecycle.
type BlockerState string

const (
	BlockerOpen     BlockerState = "OPEN"
	BlockerResolved BlockerState = "RESOLVED"
	BlockerWaived   BlockerState = "WAIVED"
)

// WorkItemBlocker is one durable, typed reason a WorkItem sits BLOCKED
// (ADR-020: "Admission blocker được lưu thành một row `blockers` với type
// bằng chính TerminationReason"). SourceRunID/SourceNodeRunID/SourceAttemptID
// are optional — populated when a real runtime origin caused this blocker
// (RUN_CANCELLED always carries SourceRunID; SCOPE_EXPANSION_REQUIRED always
// carries all three), left empty for a blocker a future task creates from a
// context with no such origin. DecisionArtifactID is populated only for a
// WAIVED resolution (ResolveWorkItemBlocker's own required, immutable
// evidence record for that decision).
type WorkItemBlocker struct {
	ID                 BlockerID
	ProjectID          project.ProjectID
	WorkItemID         WorkItemID
	Type               BlockerType
	State              BlockerState
	SourceRunID        string
	SourceNodeRunID    string
	SourceAttemptID    string
	Reason             string
	OpenedAt           time.Time
	ResolvedAt         *time.Time
	ResolvedBy         string
	ResolutionNote     string
	DecisionArtifactID string
	Version            uint64
}

// NewWorkItemBlocker validates and builds a new, OPEN WorkItemBlocker.
func NewWorkItemBlocker(
	id BlockerID, projectID project.ProjectID, workItemID WorkItemID, blockerType BlockerType,
	sourceRunID, sourceNodeRunID, sourceAttemptID, reason string, openedAt time.Time,
) (WorkItemBlocker, error) {
	if id == "" || projectID == "" || workItemID == "" {
		return WorkItemBlocker{}, errors.New("work item blocker identities are required")
	}
	if !blockerType.IsValid() {
		return WorkItemBlocker{}, errors.New("work item blocker type is not a recognized value")
	}
	if strings.TrimSpace(reason) == "" {
		return WorkItemBlocker{}, errors.New("work item blocker reason is required")
	}
	if openedAt.IsZero() {
		return WorkItemBlocker{}, errors.New("work item blocker opened timestamp is required")
	}
	return WorkItemBlocker{
		ID: id, ProjectID: projectID, WorkItemID: workItemID, Type: blockerType, State: BlockerOpen,
		SourceRunID: sourceRunID, SourceNodeRunID: sourceNodeRunID, SourceAttemptID: sourceAttemptID,
		Reason: strings.TrimSpace(reason), OpenedAt: openedAt.UTC(), Version: 1,
	}, nil
}
