package projectionrebuildworker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// CleanupOrphanedShadowRequest is CleanupOrphanedShadowGeneration's own
// request.
type CleanupOrphanedShadowRequest struct {
	// OperationID names the ProjectionRebuildOperation whose own
	// ShadowGeneration this call considers discarding.
	OperationID string
	// GracePeriod bounds how long a NONTERMINAL operation may go without
	// ANY progress (Operation.UpdatedAt) before this call treats it as
	// abandoned — see this function's own doc comment for the full
	// eligibility rule. Ignored for an operation already FAILED (that
	// case has its own, unconditional eligibility: a FAILED operation's
	// shadow was never activated and is always safe to discard).
	GracePeriod time.Duration
	Now         time.Time
}

// CleanupOutcome is CleanupOrphanedShadowGeneration's own result.
type CleanupOutcome struct {
	Discarded bool
	// Reason names WHY Discarded is true (empty when false) —
	// "FAILED_NEVER_ACTIVATED" or "ABANDONED_GRACE_PERIOD_ELAPSED".
	Reason string
}

const (
	reasonFailedNeverActivated     = "FAILED_NEVER_ACTIVATED"
	reasonAbandonedGracePeriodPast = "ABANDONED_GRACE_PERIOD_ELAPSED"
)

// CleanupOrphanedShadowGeneration is V6-09A's own "shadow cleanup"
// primitive (docs/design/08-v6-api-projections.md V6-09A Phạm vi: "resume
// and shadow cleanup") — a real, tested primitive this task builds, but
// (matching the SAME scope precedent V6-07A/V6-08A already set for their
// own worker wiring, per this task's own brief) deliberately NOT wired to
// any continuously-running production sweep here; a later task owns
// composing a real "aw worker"/"aw serve" call to invoke this on a
// schedule.
//
// Deliberately does NOT touch the operation's own driving job at all
// (never completes, cancels or fences it) — this function takes no
// JobLease and has no legitimate one to fence a CompleteJob call with. A
// genuinely abandoned operation's own driving job has, by construction,
// already gone through the SAME generic lease-expiry/reclaim lifecycle
// every other job kind in this codebase already gets (ClaimJob/
// RecoverExpiredJobs/MaxClaims) — it either eventually reaches DEAD on its
// own once MaxClaims is exhausted, or a future claim of it re-runs
// ExecuteProjectionRebuild, which immediately no-ops on an already-terminal
// operation (Phase.IsTerminal() at the top of its own loop). Marking the
// OPERATION FAILED here is therefore sufficient by itself to make any such
// future claim harmless, without this function needing to reach into job
// lifecycle at all.
//
// Eligibility (checked fresh, inside ONE transaction, immediately before
// any deletion — never trusted from an earlier read):
//
//   - An operation with no ShadowGeneration yet (still REQUESTED, snapshot
//     never ran) has nothing to discard: Discarded=false, no error.
//   - A TERMINAL SUCCEEDED operation's own ShadowGeneration is, by
//     construction, the generation cutover just activated (or a LATER
//     rebuild has since superseded it) — this function never discards it:
//     "never clear the active generation" stays true even for a
//     successful rebuild's own now-historical shadow number. (A later
//     task may add a separate "garbage-collect a superseded-but-inactive
//     OLD generation after a LATER successful rebuild" sweep; this
//     function intentionally does not attempt that — see this task's own
//     Verify bullet, which asks specifically for "an orphaned shadow
//     generation — worker crashed and never resumed," not general
//     post-success garbage collection.)
//   - A TERMINAL FAILED operation's own ShadowGeneration was NEVER
//     activated (cutover, by construction, only ever runs immediately
//     before the SAME transaction that sets SUCCEEDED — a FAILED
//     operation's own transition never touches projection_generations at
//     all) — always safe to discard, unconditionally (no grace period
//     needed: a FAILED operation is already a permanent, settled fact).
//   - A NONTERMINAL operation (SNAPSHOTTING/BUILDING/CUTTING_OVER) is
//     eligible ONLY when Now.Sub(operation.UpdatedAt) >= GracePeriod. This
//     is a real, evidence-based signal, not a guess: EVERY BUILDING/
//     CUTTING_OVER round this package's own buildRound performs calls
//     AdvanceOperation (bumping UpdatedAt) as part of checkpointing its
//     own progress — a live, healthy worker's own operation row is
//     therefore never stale by more than roughly one round's own
//     duration. A long-stale UpdatedAt genuinely means no worker is
//     currently making progress, never a false positive against a slow
//     but alive worker (which would still be advancing ShadowCursor, and
//     therefore UpdatedAt, on its own normal cadence). When eligible, this
//     call FIRST fences the operation to FAILED (AdvanceOperation,
//     ExpectedVersion CAS — so a worker that turns out to still be alive
//     and racing this exact moment loses the CAS and this cleanup call
//     itself aborts cleanly rather than discarding a live shadow) and
//     ONLY THEN discards the generation, both in the SAME transaction —
//     "never a live one" holds because the SAME fenced write that
//     retires the operation is what gates the deletion that follows it.
func CleanupOrphanedShadowGeneration(ctx context.Context, deps Deps, req CleanupOrphanedShadowRequest) (CleanupOutcome, error) {
	deps, err := deps.validate()
	if err != nil {
		return CleanupOutcome{}, err
	}
	if req.OperationID == "" {
		return CleanupOutcome{}, errors.New("projectionrebuildworker: CleanupOrphanedShadowRequest.OperationID is required")
	}

	var outcome CleanupOutcome
	err = deps.UnitOfWork.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		op, err := tx.ProjectionRebuilds().GetOperation(ctx, req.OperationID)
		if err != nil {
			return err
		}
		if op.ShadowGeneration == nil {
			return nil
		}
		shadowGeneration := *op.ShadowGeneration

		switch {
		case op.Phase == ports.ProjectionRebuildSucceeded:
			// The shadow IS (or, if superseded since, WAS) activated —
			// never this function's to discard.
			return nil

		case op.Phase == ports.ProjectionRebuildFailed:
			if err := tx.Projections().DiscardGeneration(ctx, op.ProjectID, op.ProjectionName, shadowGeneration); err != nil {
				if errors.Is(err, ports.ErrCannotDiscardActiveGeneration) {
					// Defense-in-depth caught something this function's
					// own eligibility reasoning did not expect — refuse,
					// never discard.
					return nil
				}
				return err
			}
			outcome = CleanupOutcome{Discarded: true, Reason: reasonFailedNeverActivated}
			return nil

		default: // nonterminal: SNAPSHOTTING / BUILDING / CUTTING_OVER
			if req.Now.Sub(op.UpdatedAt) < req.GracePeriod {
				// Not stale enough yet — may still be a live worker.
				return nil
			}
			code, message := abandonedErrorCode, fmt.Sprintf(
				"rebuild worker made no progress for at least %s (grace period), operation last updated at %s",
				req.GracePeriod, op.UpdatedAt.UTC().Format(time.RFC3339))
			if _, err := tx.ProjectionRebuilds().AdvanceOperation(ctx, ports.AdvanceProjectionRebuildOperationRequest{
				ID: op.ID, ExpectedVersion: op.Version, NextPhase: ports.ProjectionRebuildFailed,
				NextErrorCode: &code, NextErrorMessage: &message, UpdatedAt: req.Now,
			}); err != nil {
				if errors.Is(err, ports.ErrOptimisticConflict) {
					// A worker (possibly the one we thought was dead)
					// advanced this operation between our read above and
					// this CAS — genuinely still live; abort cleanly,
					// discard nothing.
					return nil
				}
				return err
			}
			if err := tx.Projections().DiscardGeneration(ctx, op.ProjectID, op.ProjectionName, shadowGeneration); err != nil {
				if errors.Is(err, ports.ErrCannotDiscardActiveGeneration) {
					return nil
				}
				return err
			}
			outcome = CleanupOutcome{Discarded: true, Reason: reasonAbandonedGracePeriodPast}
			return nil
		}
	})
	if err != nil {
		return CleanupOutcome{}, err
	}
	return outcome, nil
}
