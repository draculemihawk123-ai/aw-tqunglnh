package safesettings

import (
	"errors"
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	safesettingsapp "github.com/taQuangLing/agent-workflow/internal/app/safesettings"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
)

// writeCommandError classifies a non-nil error from
// internal/app/safesettings.UpdateSafeSettings, enumerated by reading that
// function's own source rather than guessed:
//
//   - ports.ErrOptimisticConflict: the real CAS inside UpdateSafeSettings'
//     own transaction lost — a genuine race that slipped past this
//     package's own pre-dispatch version check in handleUpdateSafeSettings
//     (two concurrent callers both observed the same current version).
//     Mapped to 409, not 412: unlike a stale If-Match this package's own
//     pre-check already catches (see writePreconditionFailed), the caller
//     here submitted a version that WAS current the moment it checked —
//     it merely lost a real race, exactly the same 409-not-412 distinction
//     internal/delivery/httpapi/workitem's own writeCommandError doc
//     comment already draws for ApproveScopeExpansion's identical CAS.
//   - ports.ErrReceiptConflict: defense-in-depth only — this package's own
//     replayOrProceed already reconciles the receipt hash before ever
//     dispatching; reachable only if a genuine race between that lookup and
//     UpdateSafeSettings' own internal recheck-then-commit surfaced the
//     identical condition first.
//   - safesettingsapp.ErrNotInstallationScoped: defense-in-depth only —
//     this package's own handler always builds cmd with
//     ports.InstallationScope() itself (there is no path-scoped project id
//     for a singleton, installation-wide resource), so this can only ever
//     fire from a programming error at this call site, never a caller
//     input.
//   - safesettings.Validate's own plain errors: unreachable in practice —
//     handleUpdateSafeSettings calls the identical Validate function itself
//     BEFORE ever building a command envelope (see that handler's own doc
//     comment), so a well-formed request that reaches this dispatch should
//     never trigger UpdateSafeSettings' own internal re-validation. Falls
//     through to the same safe 500 default as every other unmatched error,
//     never accidentally leaking a raw internal error string to a caller —
//     the pre-dispatch check is what a caller will actually observe as 400.
//
// Every unmatched error is a plain 500 INTERNAL — none of the errors this
// specific application function can return are wrapped as an
// *apperror.Error, so httpapi.WriteAppError's own StatusForAppErrorCode
// table (which only understands that type) would silently fall through to
// the same 500 default anyway; this function exists to name the two named
// sentinels that table cannot see.
func writeCommandError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ports.ErrOptimisticConflict):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, "safe settings changed concurrently; reload and retry", nil)
	case errors.Is(err, ports.ErrReceiptConflict):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, "idempotency key already used with a different request", nil)
	case errors.Is(err, safesettingsapp.ErrNotInstallationScoped):
		httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
	default:
		httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
	}
}

// writeValidationError writes a single field-level 400 — used both for
// this package's own path/header validation and for its pre-dispatch
// safesettings.Validate(desired) call (handleUpdateSafeSettings), mirroring
// internal/delivery/httpapi/workitem's own identically-named helper.
func writeValidationError(w http.ResponseWriter, field, message string) {
	httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "request validation failed",
		[]httpapi.ErrorDetail{{Field: field, Message: message}})
}

// writeReceiptHashConflict writes the response for
// httpapi.ErrReceiptHashConflict (receiptreplay.go's ReconcileReceipt): the
// same Idempotency-Key was reused with a semantically different request —
// this task's own "same key+different hash → 409 conflict" Verify line.
func writeReceiptHashConflict(w http.ResponseWriter) {
	httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, "idempotency key reused with a different request", nil)
}

// writePreconditionFailed writes the response for a stale If-Match: the
// caller's own claimed ExpectedVersion no longer matches this package's own
// freshly reloaded current SafeSettingsResult.Version — this task's own
// "If-Match mismatch → 412" Verify line. Routed through
// httpapi.StatusForAppErrorCode(errorcode.CodePreconditionFailed) rather
// than a hardcoded status/code pair, mirroring
// internal/delivery/httpapi/workitem's own identical helper.
func writePreconditionFailed(w http.ResponseWriter, message string) {
	status, code := httpapi.StatusForAppErrorCode(errorcode.CodePreconditionFailed)
	httpapi.WriteError(w, status, code, message, nil)
}
