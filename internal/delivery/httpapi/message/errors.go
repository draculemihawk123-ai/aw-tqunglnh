package message

import (
	"errors"
	"net/http"

	appmessage "github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// writeQueryError maps a query-side error (workapp.GetWorkItem,
// appmessage.ListMessages, tx.Messages().GetMessage) to the shared httpapi
// envelope — mirrors internal/delivery/httpapi/workitem's own
// writeQueryError exactly: both "genuinely does not exist"
// (ports.ErrPersistenceNotFound) and "exists but belongs to a project the
// caller's own path scope does not name" (ports.ErrScopeMismatch) fold into
// the identical WriteResourceHidden response — V6-02A's own
// leakage-normalization policy, so a guessed cross-project/cross-workitem ID
// and a genuinely nonexistent one are indistinguishable to the caller.
func writeQueryError(w http.ResponseWriter, err error) {
	if errors.Is(err, ports.ErrPersistenceNotFound) || errors.Is(err, ports.ErrScopeMismatch) {
		httpapi.WriteResourceHidden(w)
		return
	}
	httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
}

// writeCommandError maps every named sentinel AppendMessage can actually
// return (enumerated by reading internal/app/message/commands.go's own
// source, not guessed):
//
//   - ports.ErrPersistenceNotFound / ports.ErrScopeMismatch /
//     ports.ErrCrossProjectReference / ports.ErrCrossWorkItemReference: the
//     request's own AttemptID (when non-empty) either does not name a real
//     ExecutionAttempt, or names one that belongs to a different
//     WorkItem/Project than the route's own already-verified target — the
//     identical leakage-normalized WriteResourceHidden response the query
//     side already uses (the route's OWN primary target, the WorkItem, is
//     always reloaded and scope-checked by the handler itself before ever
//     dispatching — append.go's own handleAppendMessage).
//   - ports.ErrReceiptConflict: a genuine concurrent race past this
//     package's own pre-dispatch LookupReceipt fast path.
//   - appmessage.ErrContentTooLarge: Content exceeds
//     appmessage.MaxContentSize — a real, visible request-shape problem
//     mapped to 413, distinct from a generic 400 (V6-02A's own error
//     envelope still applies: ErrorCodeInvalidRequest, just a status that
//     names the actual reason).
//
// Every unmatched error falls through to a plain 500 INTERNAL — in
// practice defense-in-depth only, since a well-formed request that reaches
// AppendMessage should never trigger anything else (this package's own
// handler already re-validates every field AppendMessage itself also
// guards, before ever dispatching).
func writeCommandError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ports.ErrPersistenceNotFound),
		errors.Is(err, ports.ErrScopeMismatch),
		errors.Is(err, ports.ErrCrossProjectReference),
		errors.Is(err, ports.ErrCrossWorkItemReference):
		httpapi.WriteResourceHidden(w)
	case errors.Is(err, ports.ErrReceiptConflict):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, err.Error(), nil)
	case errors.Is(err, appmessage.ErrContentTooLarge):
		httpapi.WriteError(w, http.StatusRequestEntityTooLarge, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
	default:
		httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
	}
}

// writeContentError maps an error from ports.ArtifactStore.Verify/Open
// (handleGetMessageContent's own read path, after authorization has already
// succeeded via writeQueryError's identical sibling checks above) — mirrors
// internal/delivery/httpapi/evidence/errors.go's own identical
// writeContentError exactly: a tampered artifact (bytes no longer matching
// their own recorded hash) is a typed error either way, never silently
// served.
func writeContentError(w http.ResponseWriter, err error) {
	httpapi.WriteAppError(w, err)
}

// writeValidationError writes a single field-level 400 — this package's
// own pre-dispatch body validation, done before ever building a command
// envelope.
func writeValidationError(w http.ResponseWriter, field, message string) {
	httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "request validation failed",
		[]httpapi.ErrorDetail{{Field: field, Message: message}})
}

// writeReceiptHashConflict writes the response for httpapi.
// ErrReceiptHashConflict (receiptreplay.go's ReconcileReceipt): the same
// Idempotency-Key was reused with a semantically different request.
func writeReceiptHashConflict(w http.ResponseWriter) {
	httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, "idempotency key reused with a different request", nil)
}
