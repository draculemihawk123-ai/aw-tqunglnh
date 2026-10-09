package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// releaseFailedBlockerID is the deterministic ID of the one RELEASE_FAILED blocker an operation
// ever opens, minted from the operation's own ID (never ids.NewID()) like runFailedBlockerID, so a
// duplicate delivery of the same FAILED transition inserts-or-returns the same blocker.
func releaseFailedBlockerID(operationID workdomain.ReleaseSetLocalCommitID) string {
	return string(operationID) + "-release-failed-blocker"
}

// OpenReleaseFailedBlockerTx (V11-07, ADR-040) is the releasesetcommit worker's OnFailed hook:
// inside the transaction that closes a local-commit operation FAILED, it opens RELEASE_FAILED on the
// WorkItem whose approval requested that commit. The decision already moved the run on and cannot be
// rolled back, so the failure must stay visible on the Board until an operator commits again and
// resolves the blocker. It opens nothing for an operation no decision created (a manual commit has
// no selection record, or one without a WorkItem): the operator who asked sees the failure directly.
func OpenReleaseFailedBlockerTx(
	ctx context.Context, tx ports.Tx, intent workdomain.ReleaseSetLocalCommit, reason workdomain.ReleaseSetLocalCommitFailureReason,
) error {
	selection, found, err := tx.Work().GetReleaseSetLocalCommitSelection(ctx, string(intent.ID))
	if err != nil {
		return err
	}
	if !found || selection.WorkItemID == "" {
		return nil
	}
	if cancelling, err := workItemCancellationIntentExists(ctx, tx, selection.WorkItemID); err != nil || cancelling {
		return err
	}
	blockerID := releaseFailedBlockerID(intent.ID)
	switch _, getErr := tx.Work().GetWorkItemBlocker(ctx, blockerID); {
	case getErr == nil:
		return nil // already opened by an earlier delivery of this same FAILED transition
	case !errors.Is(getErr, ports.ErrPersistenceNotFound):
		return getErr
	}
	_, err = openWorkItemBlockerTx(
		ctx, tx, intent.ProjectID, selection.WorkItemID, blockerID, workdomain.BlockerReleaseFailed,
		selection.RunID, "", "",
		fmt.Sprintf("the commit requested with approval %s failed (%s) for repository workspace %s", selection.ApprovalRequestID, reason, intent.RepositoryWorkspaceID),
		"", string(intent.ID),
	)
	return err
}
