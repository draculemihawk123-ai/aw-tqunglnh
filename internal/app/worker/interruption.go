package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
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

// InterruptionRecoveryStore is the store surface a replacement worker needs
// to move a crashed attempt out of RUNNING: classify it from WriteLease
// history (WriteLeaseHistory), then commit that classification as a fenced
// terminal transition.
type InterruptionRecoveryStore interface {
	WriteLeaseHistory
	TerminateInterruptedAttempt(context.Context, ports.AttemptTerminationUpdate) error
}

// WorkspaceReconciler is the narrow store surface needed to reconcile, and —
// only when a mutation cannot be ruled out — quarantine the one repository
// workspace a mutating interrupted attempt held a WriteLease against
// (docs/design/02-v0-spike-verdict.md V0-07's QuarantineRepositoryWorkspace).
type WorkspaceReconciler interface {
	LoadRepositoryWorkspaceRevision(ctx context.Context, repositoryWorkspaceID string) (string, error)
	QuarantineRepositoryWorkspace(context.Context, ports.QuarantineRepositoryWorkspaceUpdate) error
}

// InterruptedAttemptRecoveryRequest is everything ReconcileInterruptedAttempt
// needs to fully recover one crashed attempt: the fenced termination
// transition, plus — only consulted when the attempt turns out to be
// mutating/INDETERMINATE — the one repository workspace it held a WriteLease
// against and the exact revision it was pinned to.
type InterruptedAttemptRecoveryRequest struct {
	AttemptID              runtime.ExecutionAttemptID
	ExpectedAttemptVersion uint64
	TerminationEventID     string
	CorrelationID          string
	OccurredAt             time.Time

	// Consulted only when classification comes back INDETERMINATE.
	RepositoryWorkspaceID    string
	PinnedRevision           string
	ExpectedWorkspaceVersion uint64
	QuarantineEventID        string
}

// InterruptedAttemptRecoveryResult is the evidence-backed outcome of one
// ReconcileInterruptedAttempt call. Reconciliation is the zero value for a
// read-only (LOST) attempt: reconciliation never applies to it.
type InterruptedAttemptRecoveryResult struct {
	NextState      runtime.ExecutionAttemptState
	Reason         runtime.TerminationReason
	Reconciliation ReconciliationVerdict
	Quarantined    bool
}

// ReconcileInterruptedAttempt is the real crash-recovery path a replacement
// worker runs against every attempt it finds still at RUNNING after a hard
// crash. It composes the primitives already proven independently (SPK-04's
// ClassifyInterruptedAttempt/TerminateInterruptedAttempt, SPK-09's
// ReconcileMutatingAttempt/QuarantineRepositoryWorkspace) into the one flow a
// real replacement worker follows, so an interrupted attempt is never left
// stranded at RUNNING (docs/design/02-v0-spike-verdict.md V0-10C): a
// read-only attempt is terminated LOST directly; a mutating attempt is
// terminated INDETERMINATE, then reconciled against the workspace's current
// on-disk revision, quarantining it whenever a mutation cannot be ruled out.
// It never infers success or failure from a process exit code — every
// decision comes from durable WriteLease/revision evidence.
func ReconcileInterruptedAttempt(
	ctx context.Context,
	store InterruptionRecoveryStore,
	workspaces WorkspaceReconciler,
	request InterruptedAttemptRecoveryRequest,
) (InterruptedAttemptRecoveryResult, error) {
	nextState, reason, err := ClassifyInterruptedAttempt(ctx, store, request.AttemptID)
	if err != nil {
		return InterruptedAttemptRecoveryResult{}, err
	}
	if err := store.TerminateInterruptedAttempt(ctx, ports.AttemptTerminationUpdate{
		AttemptID: request.AttemptID, ExpectedVersion: request.ExpectedAttemptVersion,
		NextState: nextState, Reason: reason,
		EventID: request.TerminationEventID, CorrelationID: request.CorrelationID, OccurredAt: request.OccurredAt,
	}); err != nil {
		return InterruptedAttemptRecoveryResult{}, fmt.Errorf("terminate interrupted attempt: %w", err)
	}

	result := InterruptedAttemptRecoveryResult{NextState: nextState, Reason: reason}
	if nextState != runtime.ExecutionAttemptIndeterminate {
		return result, nil
	}
	if workspaces == nil || request.RepositoryWorkspaceID == "" {
		return result, errors.New("a mutating/INDETERMINATE attempt requires a repository workspace to reconcile")
	}
	currentRevision, err := workspaces.LoadRepositoryWorkspaceRevision(ctx, request.RepositoryWorkspaceID)
	if err != nil {
		return result, fmt.Errorf("load repository workspace revision: %w", err)
	}
	verdict, err := ReconcileMutatingAttempt(request.PinnedRevision, currentRevision)
	if err != nil {
		return result, err
	}
	result.Reconciliation = verdict
	if verdict == ReconciliationClean {
		return result, nil
	}
	if err := workspaces.QuarantineRepositoryWorkspace(ctx, ports.QuarantineRepositoryWorkspaceUpdate{
		RepositoryWorkspaceID: workspace.RepositoryWorkspaceID(request.RepositoryWorkspaceID),
		ExpectedVersion:       request.ExpectedWorkspaceVersion,
		Reason:                string(verdict),
		EventID:               request.QuarantineEventID, CorrelationID: request.CorrelationID, OccurredAt: request.OccurredAt,
	}); err != nil {
		return result, fmt.Errorf("quarantine mutating workspace: %w", err)
	}
	result.Quarantined = true
	return result, nil
}
