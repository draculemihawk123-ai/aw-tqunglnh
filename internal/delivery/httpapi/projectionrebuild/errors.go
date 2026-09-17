package projectionrebuild

import (
	"errors"
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	appprojectionrebuild "github.com/taQuangLing/agent-workflow/internal/app/projectionrebuild"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// activeOperationIDErrorField is the ErrorDetail.Field name this package
// writes when a POST rebuild request conflicts with an already-active
// rebuild — see writeCommandError's own doc comment for the full design
// choice this implements.
const activeOperationIDErrorField = "activeOperationId"

// writeQueryError maps a query-side error from catalog.GetProject or
// appprojectionrebuild.GetProjectionStatus/GetProjectionRebuildStatus to
// the shared httpapi envelope. Mirrors
// internal/delivery/httpapi/workitem/errors.go's own identical
// writeQueryError exactly (that package calls the SAME catalog.GetProject
// this one does, with the SAME "not found and wrong scope are
// indistinguishable to the caller" leakage-normalization reasoning).
func writeQueryError(w http.ResponseWriter, err error) {
	if errors.Is(err, ports.ErrPersistenceNotFound) || errors.Is(err, ports.ErrScopeMismatch) {
		httpapi.WriteResourceHidden(w)
		return
	}
	httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
}

// writeCommandError maps every error
// appprojectionrebuild.RequestProjectionRebuild can actually return
// (enumerated by reading that function's own source, not guessed):
//
//   - the typed active-rebuild conflict (newActiveProjectionRebuildConflictError,
//     internal/app/projectionrebuild/commands.go) — extracted via
//     appprojectionrebuild.ActiveProjectionRebuildOperationID. Design
//     choice, per this task's own brief (confirmed by reading
//     internal/delivery/httpapi/errors.go's full WriteError/WriteAppError
//     shape first, exactly as instructed): httpapi.ErrorBody.Details is
//     already a []ErrorDetail{Field, Message} slice with no generic "extra
//     structured payload" slot, and httpapi.WriteAppError itself never
//     forwards an *apperror.Error's own Details map onto the wire (see
//     that function's own source — it passes literal nil for details,
//     unconditionally). Rather than extending the shared ErrorBody/
//     WriteAppError contract itself (which every OTHER endpoint package in
//     this codebase already depends on staying exactly as-is — V6-02A's own
//     "Hoàn thành khi: endpoint task không phải tự quyết DTO/... error/...
//     convention" line locks that shape), this package populates ONE
//     ErrorDetail{Field: "activeOperationId", Message: <the active
//     operation's own ID>} itself, alongside the existing human-readable
//     Message — the same {Field, Message} shape cursor.go's own
//     WriteResyncRequired already uses to carry one specific structured
//     value (a resync Reason) through this exact mechanism, applied here to
//     a different specific value (an operation ID) rather than a free-form
//     detail list. A caller that wants the ID programmatically reads
//     Details[0].Message (Field == "activeOperationId"); the envelope's own
//     top-level Message stays human-readable prose, unchanged.
//   - ports.ErrReceiptConflict: a genuine concurrent race (two callers used
//     the identical Idempotency-Key with two different request bodies) —
//     RequestProjectionRebuild's own internal receipt recheck, inside its
//     WithSerializedWrite transaction, is what actually caught it (this
//     package never runs its own pre-dispatch replay check — see
//     projectionrebuild.go's own doc comment).
//   - ports.ErrPersistenceNotFound / ports.ErrScopeMismatch: defensive only
//     in practice (this handler already reloaded the Project via
//     catalog.GetProject before ever dispatching) — folded into the same
//     leakage-normalized WriteResourceHidden response writeQueryError uses.
//
// Every unmatched error falls through to a plain 500 INTERNAL — the bare
// `errors.New(...)` field-presence guards inside RequestProjectionRebuild
// are always re-validated by this handler's own explicit checks first (see
// handleRequestProjectionRebuild), so a well-formed request should never
// actually trigger one of those guards.
func writeCommandError(w http.ResponseWriter, err error) {
	if activeID, ok := appprojectionrebuild.ActiveProjectionRebuildOperationID(err); ok {
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, err.Error(),
			[]httpapi.ErrorDetail{{Field: activeOperationIDErrorField, Message: activeID}})
		return
	}
	switch {
	case errors.Is(err, ports.ErrPersistenceNotFound), errors.Is(err, ports.ErrScopeMismatch):
		httpapi.WriteResourceHidden(w)
	case errors.Is(err, ports.ErrReceiptConflict):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, err.Error(), nil)
	default:
		httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
	}
}

// writeValidationError writes a single field-level 400 — this package's own
// pre-dispatch body/path/query validation (done before ever building a
// command envelope, so a malformed request never even computes a semantic
// hash). Mirrors every sibling endpoint package's own identical helper.
func writeValidationError(w http.ResponseWriter, field, message string) {
	httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "request validation failed",
		[]httpapi.ErrorDetail{{Field: field, Message: message}})
}
