// This file is V6-07A's own HTTP slice (docs/design/08-v6-api-projections.md
// V6-07A) — the binary-upload sibling of append.go's own handleAppendMessage,
// added to this package rather than a new one since AppendConversationAttachment
// (internal/app/message/attachment.go) is itself a sibling command in the
// SAME app package, and this route shares this package's own Dependencies/
// workapp.GetWorkItem-reload/writeQueryError/writeCommandError machinery
// wholesale.
//
// Unlike every other route in this package, the request body here is NOT
// JSON: it is the attachment's own raw content bytes, so this handler
// cannot reuse prepareCreateCommand/httpapi.CanonicalizeJSON (both assume a
// JSON `dst` to decode the body into) — the request's own identifying
// metadata (target/content-type/sensitivity/declared digest) travels via
// headers instead, mirrored into a small canonicalizable struct
// (attachmentMetadata below) purely so httpapi.SemanticHash has a
// normalizedPayload to hash, exactly the way that function's own doc
// comment already anticipates a future binary-upload caller doing
// ("extraContentDigest ... e.g. a future attachment upload").
package message

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	appmessage "github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	messagedomain "github.com/taQuangLing/agent-workflow/internal/domain/message"
)

// Header names this route's own wire contract uses to carry an
// attachment's identifying metadata alongside its raw body — there is no
// existing convention anywhere else in this codebase for a binary-upload
// route to mirror (append.go's own appendMessageBody explicitly says a
// future V6-07A attachment route is the first place raw bytes ever travel
// over this API at all), so these are this task's own considered choice:
// a small, closed, all-optional-except-digest-and-role header set, never a
// multipart/form-data body (which would reintroduce exactly the
// "fully-buffer-before-streaming" problem PrepareAttachment/this route's
// own bounded io.LimitReader usage is built to avoid).
const (
	// AttachmentSHA256Header carries the caller-declared raw content
	// SHA-256 digest, lowercase hex, no "sha256:" prefix. Required.
	AttachmentSHA256Header = "X-Attachment-Sha256"
	// AttachmentRoleHeader carries the same Role vocabulary
	// appendMessageBody.Role already uses (USER/ASSISTANT/SYSTEM/TOOL).
	// Required.
	AttachmentRoleHeader = "X-Attachment-Role"
	// AttachmentSensitivityHeader carries the same Sensitivity vocabulary
	// appendMessageBody.Sensitivity already uses. Optional — empty defaults
	// to PUBLIC, exactly like parseSensitivity's own zero-value default.
	AttachmentSensitivityHeader = "X-Attachment-Sensitivity"
	// AttachmentAttemptIDHeader carries the same optional AttemptID
	// linkage appendMessageBody.AttemptID already carries in its own JSON
	// body. Optional — empty means no execution-context linkage.
	AttachmentAttemptIDHeader = "X-Attachment-Attempt-Id"
)

// attachmentMetadata is the small canonicalizable struct
// prepareAttachmentCommand marshals to build httpapi.SemanticHash's own
// normalizedPayload argument — it is never itself the wire request body
// (this route's body is raw bytes), only an internal hashing convenience
// mirroring what appendMessageBody would canonicalize if this route's own
// metadata traveled as JSON instead of headers.
type attachmentMetadata struct {
	WorkItemID  string `json:"workItemId"`
	AttemptID   string `json:"attemptId,omitempty"`
	Role        string `json:"role"`
	ContentType string `json:"contentType"`
	Sensitivity string `json:"sensitivity"`
}

// handleAppendConversationAttachment implements
// POST /projects/{projectId}/work-items/{workItemId}/attachments
// (operationId appendConversationAttachment): dispatches
// internal/app/message.AppendConversationAttachment. Mirrors
// handleAppendMessage's own flow exactly (reload the route's own
// authoritative WorkItem FIRST, unconditionally, before Idempotency-Key or
// any header is even read; build the command envelope; receipt lookup/
// replay; dispatch; encode), adapted only for a raw-body request instead of
// a JSON one.
func handleAppendConversationAttachment(deps Dependencies) http.HandlerFunc {
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

		role := messagedomain.Role(r.Header.Get(AttachmentRoleHeader))
		if !role.Valid() {
			writeValidationError(w, AttachmentRoleHeader, "must be one of USER, ASSISTANT, SYSTEM, TOOL")
			return
		}
		contentType := strings.TrimSpace(r.Header.Get("Content-Type"))
		if contentType == "" {
			writeValidationError(w, "Content-Type", "is required")
			return
		}
		sensitivity, err := parseSensitivity(r.Header.Get(AttachmentSensitivityHeader))
		if err != nil {
			writeValidationError(w, AttachmentSensitivityHeader, err.Error())
			return
		}
		declaredSHA256 := strings.ToLower(strings.TrimSpace(r.Header.Get(AttachmentSHA256Header)))
		if declaredSHA256 == "" {
			writeValidationError(w, AttachmentSHA256Header, "is required")
			return
		}
		attemptID := strings.TrimSpace(r.Header.Get(AttachmentAttemptIDHeader))

		cmd, ok := prepareAttachmentCommand(w, r, deps, scope, attachmentMetadata{
			WorkItemID: workItemID, AttemptID: attemptID, Role: string(role),
			ContentType: contentType, Sensitivity: string(sensitivityWireFor(sensitivity)),
		}, declaredSHA256)
		if !ok {
			return
		}

		if replayOrProceed(r.Context(), w, deps, cmd) {
			return
		}

		body := http.MaxBytesReader(w, r.Body, appmessage.MaxAttachmentSize+1)
		result, err := appmessage.AppendConversationAttachment(r.Context(), deps.UnitOfWork, deps.ArtifactStore, deps.IDs, deps.Clock, cmd, appmessage.AppendConversationAttachmentRequest{
			ProjectID: projectID, WorkItemID: workItemID, AttemptID: attemptID, Role: role,
			Body: body, ContentType: contentType, Sensitivity: sensitivity, DeclaredSHA256: declaredSHA256,
		})
		if err != nil {
			writeAttachmentCommandError(w, err)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusCreated, result, "")
	}
}

// prepareAttachmentCommand is handleAppendConversationAttachment's own
// preamble — the raw-body counterpart of prepareCreateCommand (envelope.go):
// require Idempotency-Key, canonicalize metadata (never the body itself) to
// build the command's own RequestHash via httpapi.SemanticHash with
// declaredSHA256 as its own extraContentDigest argument — exactly the
// "future attachment upload" case that function's own doc comment already
// names. On any failure this writes the appropriate error response itself
// and returns ok=false.
func prepareAttachmentCommand(w http.ResponseWriter, r *http.Request, deps Dependencies, scope ports.CommandScope, metadata attachmentMetadata, declaredSHA256 string) (cmd ports.Command, ok bool) {
	idempotencyKey, err := httpapi.RequireIdempotencyKey(r)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
		return ports.Command{}, false
	}
	canonical, err := json.Marshal(metadata)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
		return ports.Command{}, false
	}
	commandType := appmessage.AttachmentCommandType
	hash := httpapi.SemanticHash(commandType, scope, canonical, declaredSHA256, 0)
	principal := httpapi.PrincipalFromContext(r.Context())
	cmd = ports.Command{
		ID: commandType + "-" + idempotencyKey, IdempotencyKey: idempotencyKey, Actor: principal.Actor,
		ActorRoles: principal.Roles, CorrelationID: httpapi.CorrelationIDFromContext(r.Context()),
		Scope: scope, RequestedAt: deps.Clock.Now(), Type: commandType, RequestHash: hash,
	}
	return cmd, true
}

// sensitivityWireFor is parseSensitivity's own inverse — used here only to
// re-derive the SAME wire string (PUBLIC/SENSITIVE/SECRET) a header already
// carried (after its zero-value default was already resolved) so
// attachmentMetadata's own canonical JSON always carries an explicit,
// resolved value rather than an empty string that would hash differently
// than "PUBLIC" for what is semantically the identical request.
func sensitivityWireFor(s redact.Sensitivity) sensitivityWire {
	switch s {
	case redact.Sensitive:
		return sensitivitySensitive
	case redact.Secret:
		return sensitivitySecret
	default:
		return sensitivityPublic
	}
}

// writeAttachmentCommandError maps every named sentinel
// AppendConversationAttachment can return — every sentinel writeCommandError
// (errors.go) already handles PLUS this command's own five attachment-
// specific ones.
func writeAttachmentCommandError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, appmessage.ErrAttachmentDigestRequired),
		errors.Is(err, appmessage.ErrAttachmentDigestMalformed),
		errors.Is(err, appmessage.ErrAttachmentDigestMismatch):
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
	case errors.Is(err, appmessage.ErrAttachmentTooLarge):
		httpapi.WriteError(w, http.StatusRequestEntityTooLarge, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
	case errors.Is(err, appmessage.ErrAttachmentUploadConflict):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, err.Error(), nil)
	default:
		writeCommandError(w, err)
	}
}
