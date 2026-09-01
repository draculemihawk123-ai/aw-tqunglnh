package worker

import (
	"context"
	"errors"

	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// WriteLeaseHistory answers whether an attempt ever held a WriteLease. This
// is how a replacement worker distinguishes a read-only interrupted attempt
// (no side effect was ever possible) from a mutating one (a side effect is
// ambiguous and must not be assumed either way).
type WriteLeaseHistory interface {
	AttemptHeldAnyWriteLease(context.Context, runtime.ExecutionAttemptID) (bool, error)
}

// ClassifyInterruptedAttempt decides the terminal state a crashed attempt
// must move to. A read-only attempt becomes LOST: nothing durable could
// depend on it. A mutating attempt becomes INDETERMINATE: its side effect
// cannot be assumed to have happened or not from a bare process exit code,
// and must be reconciled (see ReconcileMutatingAttempt) before anything
// relies on it.
func ClassifyInterruptedAttempt(
	ctx context.Context,
	history WriteLeaseHistory,
	attemptID runtime.ExecutionAttemptID,
) (runtime.ExecutionAttemptState, runtime.TerminationReason, error) {
	if history == nil {
		return "", "", errors.New("write lease history is required")
	}
	if attemptID == "" {
		return "", "", errors.New("interrupted attempt id is required")
	}
	mutating, err := history.AttemptHeldAnyWriteLease(ctx, attemptID)
	if err != nil {
		return "", "", err
	}
	if mutating {
		return runtime.ExecutionAttemptIndeterminate, runtime.TerminationReasonProcessExitBeforeOutcomeCommit, nil
	}
	return runtime.ExecutionAttemptLost, runtime.TerminationReasonProcessExitBeforeOutcomeCommit, nil
}

// ReconciliationVerdict is the evidence-backed result of comparing a
// mutating, INDETERMINATE attempt's pinned input revision against the
// current workspace revision. It never itself quarantines or recreates a
// workspace (see docs/design/02-v0-spike-verdict.md V0-07); it only records
// whether a side effect is provably absent, so nothing downstream has to
// guess from a process exit code.
type ReconciliationVerdict string

const (
	ReconciliationClean            ReconciliationVerdict = "CLEAN_NO_OBSERVED_MUTATION"
	ReconciliationMutationObserved ReconciliationVerdict = "MUTATION_OBSERVED_REQUIRES_QUARANTINE"
)

// ReconcileMutatingAttempt compares the exact revision an attempt was
// pinned to against the workspace's current revision. Equality is the only
// evidence a fresh worker has that no side effect landed; anything else must
// be treated as an unresolved mutation, never as success.
func ReconcileMutatingAttempt(pinnedRevision, currentRevision string) (ReconciliationVerdict, error) {
	if pinnedRevision == "" || currentRevision == "" {
		return "", errors.New("pinned and current revision are both required")
	}
	if pinnedRevision == currentRevision {
		return ReconciliationClean, nil
	}
	return ReconciliationMutationObserved, nil
}
