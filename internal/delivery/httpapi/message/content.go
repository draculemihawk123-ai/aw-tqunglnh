package message

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	appmessage "github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// handleGetMessageContent implements
// GET /projects/{projectId}/work-items/{workItemId}/messages/{messageId}/content
// (operationId getMessageContent) — a real, previously-missing route: every
// other route this package registers returns only messageRefDTO's own
// bounded ContentArtifactID reference (dto.go's own doc comment: "a caller
// that needs the actual bytes fetches them by ContentArtifactID through a
// future V6-07B GetArtifactContent route"), but no such route existed for a
// Message's own content specifically — only evidence.handleGetArtifactContent
// (Evidence-scoped, authorizing against an Evidence row's own
// ArtifactReferences) did, which a chat Message can never satisfy. Without
// this route, an ASSISTANT/SYSTEM/TOOL-authored message's own real text is
// permanently unreadable by any caller — the operator's own USER-authored
// messages are the one case that never needed this (the caller already
// holds the text it just sent), but V7-15's own chat log has to render
// every role's own message.
//
// The WorkItem named by {workItemId} is reloaded via workapp.GetWorkItem
// FIRST, unconditionally — the identical scope-verification discipline
// every other route in this package establishes — before this handler ever
// calls appmessage.ResolveMessageContent (internal/app/message/content.go),
// the real public application operation ADR-028 wants behind this route
// (that function reloads the Message itself and cross-checks it against
// BOTH projectId and workItemId, never trusted from the path alone). The
// rest of this handler mirrors evidence.handleGetArtifactContent's own
// Verify-before-Open/Range/ApplyContentHeaders/ETag streaming discipline —
// see that handler's own doc comment for the full reasoning (verify-open as
// one guarantee against a tampered artifact; Content-Disposition's own
// closed inline-safe allow-list is what actually prevents a raw HTML/script
// message body from ever executing, not this handler).
func handleGetMessageContent(deps Dependencies) http.HandlerFunc {
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

		ref, err := appmessage.ResolveMessageContent(ctx, deps.UnitOfWork, projectID, workItemID, messageID)
		if err != nil {
			writeQueryError(w, err)
			return
		}

		if err := deps.ArtifactStore.Verify(ctx, ref); err != nil {
			writeContentError(w, err)
			return
		}
		reader, err := deps.ArtifactStore.Open(ctx, ref)
		if err != nil {
			writeContentError(w, err)
			return
		}
		defer reader.Close()

		rng, present, rangeErr := httpapi.ParseRange(r.Header.Get("Range"), ref.Size)
		if rangeErr != nil {
			httpapi.WriteRangeNotSatisfiable(w, ref.Size)
			return
		}

		httpapi.ApplyContentHeaders(w, ref.ContentType, messageID)
		w.Header().Set("ETag", fmt.Sprintf("%q", ref.SHA256))

		if present {
			if err := skipMessageContentBytes(reader, rng.Start); err != nil {
				httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
				return
			}
			httpapi.ApplyPartialContentHeaders(w, rng, ref.Size)
			w.WriteHeader(http.StatusPartialContent)
			_, _ = io.CopyN(w, reader, rng.Length())
			return
		}

		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Length", strconv.FormatInt(ref.Size, 10))
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, reader)
	}
}

// skipMessageContentBytes mirrors evidence's own identical skipBytes
// helper — kept as a private duplicate rather than a shared export since
// neither package depends on the other and the logic is three lines.
func skipMessageContentBytes(reader io.Reader, n int64) error {
	if n == 0 {
		return nil
	}
	if seeker, ok := reader.(io.Seeker); ok {
		_, err := seeker.Seek(n, io.SeekStart)
		return err
	}
	_, err := io.CopyN(io.Discard, reader, n)
	return err
}
