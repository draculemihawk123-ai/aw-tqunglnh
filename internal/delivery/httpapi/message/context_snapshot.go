package message

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/contextsnapshot"
)

// errMessageHasNoAttempt is returned by handleGetMessageContextSnapshot's
// own read when the target Message's AttemptID is nil — a legitimate,
// non-leaking "not applicable" outcome (the caller is already authorized
// to see this Message; there is simply no execution context to show),
// distinct from the leakage-normalized WriteResourceHidden case writeQueryError
// handles for a genuinely absent/foreign Message.
var errMessageHasNoAttempt = errors.New("message: message has no linked execution attempt")

// errContextSnapshotNotYetAvailable is returned when the Message's own
// AttemptID names a real ExecutionAttempt, but that Attempt has not (yet,
// or ever) produced a bound V5-04 ContextSnapshot — also non-leaking, for
// the identical reason errMessageHasNoAttempt is.
var errContextSnapshotNotYetAvailable = errors.New("message: linked execution attempt has not produced a context snapshot yet")

// handleGetMessageContextSnapshot implements
// GET /projects/{projectId}/work-items/{workItemId}/messages/{messageId}/context-snapshot
// (operationId getMessageContextSnapshot): a bounded, READ-ONLY view of the
// V5-04 ContextSnapshot bound to messageId's own AttemptID, if any — this
// task's own "Phạm vi: ... ContextSnapshot/message reference metadata"
// line. It builds no new write path onto internal/app/ports.
// ContextSnapshotRepository (that port already exists; this route only
// ever calls its two Get* reads) and never touches the separate, legacy
// internal/domain/runtime.ContextSnapshot mechanism at all (see that
// port's own doc comment for why the two are deliberately kept apart).
//
// The WorkItem named by {workItemId} is reloaded via workapp.GetWorkItem
// FIRST, unconditionally — the identical scope-verification discipline
// append.go/list.go already establish. The Message named by {messageId} is
// then loaded and cross-checked against BOTH projectId and workItemId
// (never trusted from the path alone) inside the same read-only
// transaction that resolves its ContextSnapshot, so the whole lookup
// observes one consistent snapshot of state.
func handleGetMessageContextSnapshot(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		projectID := r.PathValue("projectId")
		workItemID := r.PathValue("workItemId")
		messageID := r.PathValue("messageId")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "projectId", "is required")
			return
		}
		if strings.TrimSpace(workItemID) == "" {
			writeValidationError(w, "workItemId", "is required")
			return
		}
		if strings.TrimSpace(messageID) == "" {
			writeValidationError(w, "messageId", "is required")
			return
		}
		scope := ports.ProjectScope(projectID)
		if _, err := workapp.GetWorkItem(ctx, deps.UnitOfWork, scope, workItemID); err != nil {
			writeQueryError(w, err)
			return
		}

		var snapshot contextsnapshot.Snapshot
		err := deps.UnitOfWork.WithReadOnly(ctx, func(tx ports.Tx) error {
			m, err := tx.Messages().GetMessage(ctx, messageID)
			if err != nil {
				return err
			}
			if string(m.ProjectID) != projectID || string(m.WorkItemID) != workItemID {
				return fmt.Errorf("%w: message %s belongs to another work item/project", ports.ErrScopeMismatch, messageID)
			}
			if m.AttemptID == nil {
				return errMessageHasNoAttempt
			}
			snap, err := tx.ContextSnapshots().GetSnapshotByAttemptID(ctx, string(*m.AttemptID))
			if err != nil {
				if errors.Is(err, ports.ErrPersistenceNotFound) {
					return errContextSnapshotNotYetAvailable
				}
				return err
			}
			snapshot = snap
			return nil
		})
		if err != nil {
			switch {
			case errors.Is(err, errMessageHasNoAttempt):
				httpapi.WriteError(w, http.StatusNotFound, httpapi.ErrorCodeNotFound, "message has no linked execution attempt", nil)
			case errors.Is(err, errContextSnapshotNotYetAvailable):
				httpapi.WriteError(w, http.StatusNotFound, httpapi.ErrorCodeNotFound, "linked execution attempt has not produced a context snapshot yet", nil)
			default:
				writeQueryError(w, err)
			}
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, contextSnapshotToDetail(snapshot), "")
	}
}
