// WorkItemBlocker lifecycle plumbing shared by every producer/consumer in
// this package (V4-12C, docs/design/06-v4-runtime-engine.md; ADR-020):
// openWorkItemBlockerTx is called by transitionRunToCancelledTx
// (completion.go, RUN_CANCELLED) and requestScopeExpansionTx (finalize.go,
// SCOPE_EXPANSION_REQUIRED) — the two real blocker producers this task
// wires up (confirmed with the user before writing this file: the four
// admission reasons and COMPLETION_POLICY_FAILED stay type-only, no real
// producer exists yet, V5-08/V5-11's own future scope).
// closeWorkItemBlockerTx is called by ResolveWorkItemBlocker
// (resolve_work_item_blocker.go, the public command), reactivateBlockedNodeRunTx
// (scope_expansion.go, the SCOPE_EXPANSION_RECONCILE flow's own automatic
// resolution once a reactivation is actually created — the ONLY path that
// may ever resolve a SCOPE_EXPANSION_REQUIRED blocker, see
// workdomain.BlockerType.ResolvableViaCommand's own doc comment), and
// RetryBlockedActivationHandler.Retry (retry_blocked_activation.go, V5-08D
// — the ONLY path that may ever resolve an admission-reason blocker while
// its own Run is still live; ResolveWorkItemBlocker's own precondition
// requires the Run already be terminal, which a live retry's Run never is).
//
// Every event this file appends mints a fresh "WorkItemBlocker" aggregate
// identity keyed by the blocker's own ID (Sequence 1 for the opening event,
// Sequence 2 for the closing one) rather than reusing "WorkItem" — the same
// "mint a fresh, guaranteed-unique aggregate identity for a decision with no
// naturally-fresh Version to key off" discipline
// internal/app/work.ApproveScopeExpansion's own ScopeExpansionApproved event
// already establishes (its own doc comment explains why): a WorkItem can
// accumulate more than one OPEN blocker while its own Status stays BLOCKED
// throughout (no WorkItem.Version bump between them), so reusing "WorkItem"
// + WorkItem.Version as this event's own aggregate identity would collide
// against domain_events' own UNIQUE(aggregate_type, aggregate_id, sequence)
// constraint the moment a second blocker opened without an intervening
// WorkItem status change.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

const (
	WorkItemBlockedEventType     = "WORK_ITEM_BLOCKED"
	WorkItemBlockedSchemaVersion = 1

	WorkItemBlockerResolvedEventType     = "WORK_ITEM_BLOCKER_RESOLVED"
	WorkItemBlockerResolvedSchemaVersion = 1
)

// workItemBlockedEventPayload is WORK_ITEM_BLOCKED's own JSON shape
// (V4-12C): appended exactly once per opened blocker, alongside whichever
// event its own real producer already appends for the runtime-level side of
// the same transition (RUN_CANCELLED, EXECUTION_ATTEMPT_FINALIZED).
type workItemBlockedEventPayload struct {
	WorkItemID  string `json:"workItemId"`
	BlockerID   string `json:"blockerId"`
	BlockerType string `json:"blockerType"`
	SourceRunID string `json:"sourceRunId,omitempty"`
	JobID       string `json:"jobId,omitempty"`
}

// workItemBlockerResolvedEventPayload is WORK_ITEM_BLOCKER_RESOLVED's own
// JSON shape (V4-12C). WorkItemUnblocked/NewWorkItemStatus report whether
// this specific resolution was also the one that cleared the WorkItem's
// last remaining OPEN blocker (see closeWorkItemBlockerTx's own doc
// comment for the two separate conditions this reports).
type workItemBlockerResolvedEventPayload struct {
	WorkItemID        string `json:"workItemId"`
	BlockerID         string `json:"blockerId"`
	BlockerType       string `json:"blockerType"`
	ResolutionMode    string `json:"resolutionMode"`
	ResolvedBy        string `json:"resolvedBy"`
	WorkItemUnblocked bool   `json:"workItemUnblocked"`
	NewWorkItemStatus string `json:"newWorkItemStatus,omitempty"`
	JobID             string `json:"jobId,omitempty"`
}

// openWorkItemBlockerTx creates a new, OPEN WorkItemBlocker and, unless the
// WorkItem is already BLOCKED (an earlier, still-open blocker of some other
// type), CASes it ACTIVE -> BLOCKED — all inside the caller's own already-open
// transaction. blockerID is caller-supplied and MUST be deterministic (minted
// from the originating aggregate's own ID — RunID, AttemptID) so a duplicate
// delivery of the same underlying transition is idempotent
// (CreateWorkItemBlocker's own insert-or-return-existing contract) rather
// than opening a second blocker for the identical cause.
func openWorkItemBlockerTx(
	ctx context.Context, tx ports.Tx, projectID project.ProjectID, workItemID, blockerID string,
	blockerType workdomain.BlockerType, sourceRunID, sourceNodeRunID, sourceAttemptID, reason string,
	correlationID, jobID string,
) (workdomain.WorkItemBlocker, error) {
	blocker, err := workdomain.NewWorkItemBlocker(
		workdomain.BlockerID(blockerID), projectID, workdomain.WorkItemID(workItemID), blockerType,
		sourceRunID, sourceNodeRunID, sourceAttemptID, reason, time.Now().UTC(),
	)
	if err != nil {
		return workdomain.WorkItemBlocker{}, err
	}
	created, err := tx.Work().CreateWorkItemBlocker(ctx, blocker)
	if err != nil {
		return workdomain.WorkItemBlocker{}, err
	}

	item, err := tx.Work().GetWorkItem(ctx, workItemID)
	if err != nil {
		return workdomain.WorkItemBlocker{}, err
	}
	if item.Status == workdomain.WorkItemActive {
		if _, err := tx.Work().TransitionWorkItemStatus(ctx, ports.TransitionWorkItemStatusRequest{
			WorkItemID: workItemID, ExpectedStatus: workdomain.WorkItemActive, ExpectedVersion: item.Version,
			NextStatus: workdomain.WorkItemBlocked,
		}); err != nil {
			return workdomain.WorkItemBlocker{}, err
		}
	}
	// item.Status already BLOCKED: nothing to transition — the new blocker
	// above is enough; item.Status anything else (READY/BACKLOG/DONE/CANCELLED)
	// should be structurally unreachable (a blocker only ever opens for a Run
	// or Attempt actively belonging to this WorkItem's own current ACTIVE
	// span) and is left as-is rather than guessed at.

	payload, err := json.Marshal(workItemBlockedEventPayload{
		WorkItemID: workItemID, BlockerID: blockerID, BlockerType: string(blockerType),
		SourceRunID: sourceRunID, JobID: jobID,
	})
	if err != nil {
		return workdomain.WorkItemBlocker{}, fmt.Errorf("marshal %s event payload: %w", WorkItemBlockedEventType, err)
	}
	if err := tx.Events().Append(ctx, ports.DomainEvent{
		ID: blockerID + "-opened", ProjectID: string(projectID),
		AggregateType: "WorkItemBlocker", AggregateID: blockerID, Sequence: 1,
		EventType: WorkItemBlockedEventType, SchemaVersion: WorkItemBlockedSchemaVersion, PayloadJSON: string(payload),
		CorrelationID: correlationID, CreatedAt: time.Now().UTC(),
	}); err != nil {
		return workdomain.WorkItemBlocker{}, err
	}
	return created, nil
}

// closeWorkItemBlockerTx CASes an OPEN WorkItemBlocker to nextState
// (RESOLVED or WAIVED) and, separately, unlocks the WorkItem when — and only
// when — BOTH of two independent conditions hold (ADR-020's own explicit
// "hai điều kiện tách rời": "blocker target luôn được chuyển, nhưng WorkItem
// chỉ BLOCKED -> READY khi số blocker OPEN còn lại bằng 0"):
//
//  1. Zero OPEN blockers remain for this WorkItem after this transition (a
//     WorkItem with several blockers stays BLOCKED until the last one
//     clears).
//  2. No still-REQUESTED WorkItemCancellationIntent exists for this WorkItem
//     (this file's own extension of that same rule, confirmed with the user:
//     a WorkItem an operator has already asked to cancel must never be
//     "revived" back to an active status just because its last blocker
//     happened to clear first — CancelWorkItem's own later quiesce/terminalize
//     is what closes it out instead, see reconcileWorkItemCancellationTx,
//     cancel_work_item.go).
//
// unlockedStatus is the caller's own choice of where an unlocked WorkItem
// lands — ResolveWorkItemBlocker (the public command, RUN_CANCELLED/
// COMPLETION_POLICY_FAILED blockers, or an admission-reason blocker whose
// own Run has ALREADY gone terminal some other way) always names READY (the
// Run that caused the block is already gone; a NEW run is what comes next),
// while reactivateBlockedNodeRunTx's own automatic SCOPE_EXPANSION_REQUIRED
// resolution and RetryBlockedActivationHandler.Retry's own admission-reason
// resolution (retry_blocked_activation.go, V5-08D) both always name ACTIVE
// (the SAME Run resumes, it never stopped).
// closeWorkItemBlockerResult reports what closeWorkItemBlockerTx actually
// did — in particular the REAL Unblocked/NewStatus signal, computed once
// inside this function from the two-separate-conditions check above. A
// caller must never re-derive "was this the resolution that unblocked the
// WorkItem" from the WorkItem's own final status alone (e.g. "status !=
// BLOCKED"): the WorkItem could already have left BLOCKED for a completely
// unrelated reason (most importantly CancelWorkItem terminalizing it to
// CANCELLED while this exact blocker was still OPEN) before this call ever
// ran, which is not this resolution's own doing.
type closeWorkItemBlockerResult struct {
	Blocker   workdomain.WorkItemBlocker
	Unblocked bool
	NewStatus workdomain.WorkItemStatus
}

func closeWorkItemBlockerTx(
	ctx context.Context, tx ports.Tx, blocker workdomain.WorkItemBlocker, nextState workdomain.BlockerState,
	resolvedBy, resolutionNote, decisionArtifactID string, unlockedStatus workdomain.WorkItemStatus,
	correlationID, jobID string,
) (closeWorkItemBlockerResult, error) {
	updated, err := tx.Work().TransitionWorkItemBlockerState(ctx, ports.TransitionWorkItemBlockerStateRequest{
		BlockerID: string(blocker.ID), ExpectedState: workdomain.BlockerOpen, ExpectedVersion: blocker.Version,
		NextState: nextState, ResolvedAt: time.Now().UTC(), ResolvedBy: resolvedBy, ResolutionNote: resolutionNote,
		DecisionArtifactID: decisionArtifactID,
	})
	if err != nil {
		return closeWorkItemBlockerResult{}, err
	}

	workItemID := string(updated.WorkItemID)
	remaining, err := tx.Work().ListWorkItemBlockersForWorkItem(ctx, workItemID)
	if err != nil {
		return closeWorkItemBlockerResult{}, err
	}
	openRemaining := 0
	for _, other := range remaining {
		if other.State == workdomain.BlockerOpen {
			openRemaining++
		}
	}

	unblocked := false
	var newStatus workdomain.WorkItemStatus
	if openRemaining == 0 {
		pendingCancellation, err := workItemCancellationPending(ctx, tx, workItemID)
		if err != nil {
			return closeWorkItemBlockerResult{}, err
		}
		if !pendingCancellation {
			item, err := tx.Work().GetWorkItem(ctx, workItemID)
			if err != nil {
				return closeWorkItemBlockerResult{}, err
			}
			if item.Status == workdomain.WorkItemBlocked {
				if _, err := tx.Work().TransitionWorkItemStatus(ctx, ports.TransitionWorkItemStatusRequest{
					WorkItemID: workItemID, ExpectedStatus: workdomain.WorkItemBlocked, ExpectedVersion: item.Version,
					NextStatus: unlockedStatus,
				}); err != nil {
					return closeWorkItemBlockerResult{}, err
				}
				unblocked = true
				newStatus = unlockedStatus
			}
		}
	}

	payload, err := json.Marshal(workItemBlockerResolvedEventPayload{
		WorkItemID: workItemID, BlockerID: string(updated.ID), BlockerType: string(updated.Type),
		ResolutionMode: string(nextState), ResolvedBy: resolvedBy, WorkItemUnblocked: unblocked,
		NewWorkItemStatus: string(newStatus), JobID: jobID,
	})
	if err != nil {
		return closeWorkItemBlockerResult{}, fmt.Errorf("marshal %s event payload: %w", WorkItemBlockerResolvedEventType, err)
	}
	if err := tx.Events().Append(ctx, ports.DomainEvent{
		ID: string(updated.ID) + "-resolved", ProjectID: string(updated.ProjectID),
		AggregateType: "WorkItemBlocker", AggregateID: string(updated.ID), Sequence: 2,
		EventType: WorkItemBlockerResolvedEventType, SchemaVersion: WorkItemBlockerResolvedSchemaVersion, PayloadJSON: string(payload),
		CorrelationID: correlationID, CreatedAt: time.Now().UTC(),
	}); err != nil {
		return closeWorkItemBlockerResult{}, err
	}
	return closeWorkItemBlockerResult{Blocker: updated, Unblocked: unblocked, NewStatus: newStatus}, nil
}

// workItemCancellationPending reports whether workItemID currently has a
// still-REQUESTED WorkItemCancellationIntent — the shared read
// closeWorkItemBlockerTx and reconcileWorkItemCancellationTx
// (cancel_work_item.go) both need.
func workItemCancellationPending(ctx context.Context, tx ports.Tx, workItemID string) (bool, error) {
	intent, err := tx.Runtime().GetWorkItemCancellationIntent(ctx, workItemID)
	if errors.Is(err, ports.ErrPersistenceNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return intent.State == runtimedomain.CancellationIntentRequested, nil
}
