package message

import (
	"net/http"
	"strings"

	appmessage "github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	messagedomain "github.com/taQuangLing/agent-workflow/internal/domain/message"
)

// appendMessageBody is POST .../messages' own wire request shape —
// deliberately without projectId/workItemId (the path's own values). This
// task's own "Không làm: KHÔNG binary upload" line means Content is always
// UTF-8 text carried as a plain JSON string, never a base64/multipart
// field — a future V6-07A attachment route is the only place raw
// arbitrary bytes ever travel over this API. Sensitivity is optional
// (parseSensitivity defaults an empty value to PUBLIC); AttemptID is
// optional (empty means no execution-context linkage, exactly
// appmessage.AppendMessageRequest.AttemptID's own "empty = no attempt
// linkage" contract).
type appendMessageBody struct {
	AttemptID   string `json:"attemptId,omitempty"`
	Role        string `json:"role"`
	Content     string `json:"content"`
	ContentType string `json:"contentType"`
	Sensitivity string `json:"sensitivity,omitempty"`
}

// handleAppendMessage implements
// POST /projects/{projectId}/work-items/{workItemId}/messages (operationId
// appendMessage): dispatches internal/app/message.AppendMessage. The
// WorkItem named by {workItemId} is reloaded via workapp.GetWorkItem FIRST,
// unconditionally (before Idempotency-Key/body are even read) — the
// identical "reload the route's own primary target and confirm its real
// project scope before anything else" discipline
// internal/delivery/httpapi/workitem's own handleCreateChildWorkItem
// already establishes, applied here to a Message's own owning WorkItem.
// This is also this task's own "Thực hiện: reload the project-owned
// conversation" line in practice — an unverified WorkItemID can never
// reach appmessage.AppendMessage at all.
func handleAppendMessage(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("projectId")
		workItemID := r.PathValue("workItemId")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "projectId", "is required")
			return
		}
		if strings.TrimSpace(workItemID) == "" {
			writeValidationError(w, "workItemId", "is required")
			return
		}
		scope := ports.ProjectScope(projectID)
		if _, err := workapp.GetWorkItem(r.Context(), deps.UnitOfWork, scope, workItemID); err != nil {
			writeQueryError(w, err)
			return
		}

		var body appendMessageBody
		cmd, ok := prepareCreateCommand(w, r, deps, "AppendMessage", scope, &body)
		if !ok {
			return
		}
		role := messagedomain.Role(body.Role)
		if !role.Valid() {
			writeValidationError(w, "role", "must be one of USER, ASSISTANT, SYSTEM, TOOL")
			return
		}
		if body.Content == "" {
			writeValidationError(w, "content", "is required")
			return
		}
		if strings.TrimSpace(body.ContentType) == "" {
			writeValidationError(w, "contentType", "is required")
			return
		}
		sensitivity, err := parseSensitivity(body.Sensitivity)
		if err != nil {
			writeValidationError(w, "sensitivity", err.Error())
			return
		}

		if replayOrProceed(r.Context(), w, deps, cmd) {
			return
		}

		result, err := appmessage.AppendMessage(r.Context(), deps.UnitOfWork, deps.ArtifactStore, deps.IDs, deps.Clock, cmd, appmessage.AppendMessageRequest{
			ProjectID: projectID, WorkItemID: workItemID, AttemptID: body.AttemptID, Role: role,
			Content: []byte(body.Content), ContentType: body.ContentType, Sensitivity: sensitivity, Matcher: deps.Matcher,
		})
		if err != nil {
			writeCommandError(w, err)
			return
		}
		// A Message row is immutable/append-only — it carries no version
		// counter to encode as an ETag (unlike a WorkItem/TaskFamily), so
		// EncodeResult's own etag argument is deliberately empty here.
		_ = httpapi.EncodeResult(w, http.StatusCreated, result, "")
	}
}
