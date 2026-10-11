package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/message"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// Stable codes for why a redo command would refuse. The HTTP layer returns them in the error details and the redo preview returns them
// next to a sentence, so a client can explain a refusal in words without parsing an error message.
const (
	RedoRefusalNotRetryable        = "NOT_RETRYABLE"
	RedoRefusalNeverRan            = "NEVER_RAN"
	RedoRefusalRunInProgress       = "RUN_IN_PROGRESS"
	RedoRefusalLatestRunSucceeded  = "LATEST_RUN_SUCCEEDED"
	RedoRefusalStillBlocked        = "STILL_BLOCKED"
	RedoRefusalWorkspaceQuarantine = "WORKSPACE_QUARANTINED"
	RedoRefusalRootUnsupported     = "ROOT_UNSUPPORTED"
	RedoRefusalNotRecreatable      = "NOT_RECREATABLE"
	RedoRefusalAlreadySuperseded   = "ALREADY_SUPERSEDED"
	RedoRefusalNoPinnedVersion     = "NO_PINNED_VERSION"
)

// RedoRefusalCode maps an error returned by RetryWorkItem or RecreateWorkItem to its stable code, or "" when err is not one of their
// typed refusals.
func RedoRefusalCode(err error) string {
	switch {
	case errors.Is(err, ErrWorkItemNotRetryable):
		return RedoRefusalNotRetryable
	case errors.Is(err, ErrWorkItemNeverRan):
		return RedoRefusalNeverRan
	case errors.Is(err, ErrWorkItemHasNonTerminalRun):
		return RedoRefusalRunInProgress
	case errors.Is(err, ErrLatestRunNotRetryable):
		return RedoRefusalLatestRunSucceeded
	case errors.Is(err, ErrWorkItemStillBlocked):
		return RedoRefusalStillBlocked
	case errors.Is(err, ErrWorkspaceQuarantined):
		return RedoRefusalWorkspaceQuarantine
	case errors.Is(err, ErrRecreateRootUnsupported):
		return RedoRefusalRootUnsupported
	case errors.Is(err, ErrWorkItemNotRecreatable):
		return RedoRefusalNotRecreatable
	case errors.Is(err, ErrWorkItemAlreadySuperseded):
		return RedoRefusalAlreadySuperseded
	case errors.Is(err, ErrNoPinnedWorkflowVersion):
		return RedoRefusalNoPinnedVersion
	default:
		return ""
	}
}

// RedoRefusal is a reason a redo command would refuse right now.
type RedoRefusal struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func refusalOf(err error) (*RedoRefusal, error) {
	if err == nil {
		return nil, nil
	}
	if code := RedoRefusalCode(err); code != "" {
		return &RedoRefusal{Code: code, Message: err.Error()}, nil
	}
	return nil, err
}

// RedoBlocker is an open blocker a retry would resolve.
type RedoBlocker struct {
	BlockerID string `json:"blockerId"`
	Type      string `json:"type"`
	Reason    string `json:"reason"`
}

// RedoAssessment is what an operator needs to decide between running a WorkItem again and redoing it with the newest workflow version,
// read in one transaction without changing anything. Retry and Recreate are nil when that command would be accepted now; otherwise
// they say why not. The cost and the leftover files are not here (they need usage events and Git): internal/app/workitemredo adds them.
type RedoAssessment struct {
	WorkItemID string
	ProjectID  string
	FamilyID   string
	Title      string
	Status     string
	IsChild    bool

	Retry    *RedoRefusal
	Recreate *RedoRefusal

	PinnedWorkflowVersionID string
	PinnedVersionNumber     uint64
	LatestWorkflowVersionID string
	LatestVersionNumber     uint64

	BlockersToResolve    []RedoBlocker
	OperatorMessageCount int
	// RedoNumber is the number the next redo would carry in its title (2 for the first redo).
	RedoNumber int
	// LineageRunIDs lists the runs of this WorkItem and of every WorkItem it replaced, oldest first: what repeating the task has run so far.
	LineageRunIDs          []string
	SupersedesWorkItemID   string
	SupersededByWorkItemID string
	// Scopes is the WorkItem's effective scope, for deciding which changed files are out of it (scopeguard.PathAllowed).
	Scopes []workdomain.RepositoryScope
}

// AssessRedo reports whether RetryWorkItem and RecreateWorkItem would be accepted for the WorkItem now, and the facts an operator weighs.
// It is advisory: each command re-derives all of it inside its own transaction. An error is only returned for a missing WorkItem, a
// project mismatch (ports.ErrScopeMismatch) or a storage failure; a refusal is data.
func AssessRedo(ctx context.Context, uow ports.UnitOfWork, projectID, workItemID string) (RedoAssessment, error) {
	var out RedoAssessment
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		item, err := loadRedoTarget(ctx, tx, projectID, workItemID)
		if err != nil {
			return err
		}
		out = RedoAssessment{
			WorkItemID: workItemID, ProjectID: projectID, FamilyID: string(item.FamilyID), Title: item.Title,
			Status: string(item.Status), IsChild: item.ParentID != nil, RedoNumber: 2,
		}

		if _, err := planRetryTx(ctx, tx, projectID, workItemID); err != nil {
			if out.Retry, err = refusalOf(err); err != nil {
				return err
			}
		}
		_, recreateErr := planRecreateTx(ctx, tx, projectID, workItemID)
		if out.Recreate, err = refusalOf(recreateErr); err != nil {
			return err
		}
		runs, err := tx.Runtime().ListWorkflowRunsForWorkItem(ctx, workItemID)
		if err != nil {
			return err
		}
		// The versions are shown even when a redo is refused, so the operator sees what a retry would run.
		if versions, err := redoVersionsTx(ctx, tx, item, runs); err == nil {
			out.PinnedWorkflowVersionID, out.PinnedVersionNumber = versions.pinnedID, versions.pinnedNumber
			out.LatestWorkflowVersionID, out.LatestVersionNumber = versions.latestID, versions.latestNumber
		} else if !errors.Is(err, ErrNoPinnedWorkflowVersion) {
			return err
		}
		blockers, err := tx.Work().ListWorkItemBlockersForWorkItem(ctx, workItemID)
		if err != nil {
			return err
		}
		for _, blocker := range blockers {
			if blocker.State == workdomain.BlockerOpen && retryResolvableBlockerType(blocker.Type) {
				out.BlockersToResolve = append(out.BlockersToResolve, RedoBlocker{BlockerID: string(blocker.ID), Type: string(blocker.Type), Reason: blocker.Reason})
			}
		}
		messages, err := tx.Messages().ListMessagesForWorkItem(ctx, workItemID)
		if err != nil {
			return err
		}
		for _, m := range messages {
			if m.Role == message.RoleUser {
				out.OperatorMessageCount++
			}
		}
		scopes, err := tx.Work().ListWorkItemEffectiveScopes(ctx, workItemID)
		if err != nil {
			return err
		}
		out.Scopes = scopes

		if successor, err := tx.Work().GetWorkItemSupersessionByOld(ctx, workItemID); err == nil {
			out.SupersededByWorkItemID = string(successor.NewWorkItemID)
		} else if !errors.Is(err, ports.ErrPersistenceNotFound) {
			return err
		}
		// Walk back through the WorkItems this one replaced: their runs are part of what repeating the task has cost.
		chain := []string{workItemID}
		for cursor := workItemID; len(chain) < 1000; {
			previous, err := tx.Work().GetWorkItemSupersessionByNew(ctx, cursor)
			if errors.Is(err, ports.ErrPersistenceNotFound) {
				break
			}
			if err != nil {
				return err
			}
			if cursor == workItemID {
				out.SupersedesWorkItemID = string(previous.OldWorkItemID)
			}
			cursor = string(previous.OldWorkItemID)
			chain = append([]string{cursor}, chain...)
			out.RedoNumber++
		}
		for _, id := range chain {
			runs, err := tx.Runtime().ListWorkflowRunsForWorkItem(ctx, id)
			if err != nil {
				return fmt.Errorf("list runs of %s: %w", id, err)
			}
			for _, run := range runs {
				out.LineageRunIDs = append(out.LineageRunIDs, string(run.ID))
			}
		}
		return nil
	})
	return out, err
}

// RedoReceiptExists reports whether cmd's idempotency key already has a receipt, that is, whether the command already ran. It returns
// ports.ErrReceiptConflict when the key was used for a different request. A caller that does work before the command (parking files)
// uses it to skip that work on a replay.
func RedoReceiptExists(ctx context.Context, uow ports.UnitOfWork, cmd ports.Command) (bool, error) {
	var found bool
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		receipt, ok, err := tx.Receipts().Load(ctx, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
		if err != nil || !ok {
			return err
		}
		if receipt.RequestHash != cmd.RequestHash {
			return ports.ErrReceiptConflict
		}
		found = true
		return nil
	})
	return found, err
}
