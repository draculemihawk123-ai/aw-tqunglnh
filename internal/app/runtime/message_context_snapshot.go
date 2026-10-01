package runtime

import (
	"context"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// ErrMessageHasNoAttempt is returned by GetContextSnapshotForMessage when the
// Message's own AttemptID is nil — a legitimate, non-leaking "not applicable"
// outcome (the caller is already authorized to see this Message; there is
// simply no execution context to show), distinct from the leakage-normalized
// not-found/scope-mismatch a genuinely absent or foreign Message produces.
var ErrMessageHasNoAttempt = errors.New("message: message has no linked execution attempt")

// ErrContextSnapshotNotYetAvailable is returned when the Message's AttemptID
// names a real ExecutionAttempt but that Attempt has not (yet, or ever)
// produced a bound ContextSnapshot — also non-leaking, for the same reason.
var ErrContextSnapshotNotYetAvailable = errors.New("message: linked execution attempt has not produced a context snapshot yet")

// GetContextSnapshotForMessage is the by-message form of GetContextSnapshot: it
// resolves the ContextSnapshot bound to messageID's own AttemptID, if any, as a
// bounded, READ-ONLY detail. It is the single application query behind both the
// HTTP route getMessageContextSnapshot and the `aw message context-snapshot`
// leaf — before it existed the HTTP handler performed this lookup itself, in
// the delivery layer, so no public application operation stood behind the route
// (ADR-028 wants every public query on one).
//
// The WorkItem is verified to belong to the scope's project FIRST, and the
// Message is then loaded and cross-checked against BOTH the project and the
// work item (never trusted from the id alone) inside the same read-only
// transaction that resolves the snapshot, so the whole lookup observes one
// consistent view of state.
func GetContextSnapshotForMessage(ctx context.Context, uow ports.UnitOfWork, scope ports.CommandScope, workItemID, messageID string) (ContextSnapshotDetail, error) {
	projectID, err := requireProjectScope(scope)
	if err != nil {
		return ContextSnapshotDetail{}, err
	}
	var detail ContextSnapshotDetail
	err = uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		if err := verifyWorkItemInScope(ctx, tx, projectID, workItemID); err != nil {
			return err
		}
		m, err := tx.Messages().GetMessage(ctx, messageID)
		if err != nil {
			return err
		}
		if string(m.ProjectID) != projectID || string(m.WorkItemID) != workItemID {
			return fmt.Errorf("%w: message %s belongs to another work item/project", ports.ErrScopeMismatch, messageID)
		}
		if m.AttemptID == nil {
			return ErrMessageHasNoAttempt
		}
		snapshot, err := tx.ContextSnapshots().GetSnapshotByAttemptID(ctx, string(*m.AttemptID))
		if err != nil {
			if errors.Is(err, ports.ErrPersistenceNotFound) {
				return ErrContextSnapshotNotYetAvailable
			}
			return err
		}
		detail = ContextSnapshotToDetail(snapshot)
		return nil
	})
	return detail, err
}
