package message

import (
	"errors"
	"net/http"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	runtimeapp "github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

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
		detail, err := runtimeapp.GetContextSnapshotForMessage(ctx, deps.UnitOfWork, ports.ProjectScope(projectID), workItemID, messageID)
		if err != nil {
			switch {
			case errors.Is(err, runtimeapp.ErrMessageHasNoAttempt):
				httpapi.WriteError(w, http.StatusNotFound, httpapi.ErrorCodeNotFound, "message has no linked execution attempt", nil)
			case errors.Is(err, runtimeapp.ErrContextSnapshotNotYetAvailable):
				httpapi.WriteError(w, http.StatusNotFound, httpapi.ErrorCodeNotFound, "linked execution attempt has not produced a context snapshot yet", nil)
			default:
				writeQueryError(w, err)
			}
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, detail, "")
	}
}
