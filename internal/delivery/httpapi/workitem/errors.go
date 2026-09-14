package workitem

import (
	"errors"
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
)

// writeQueryError maps a query-side error from internal/app/work/queries.go
// to the shared httpapi envelope. Both "genuinely does not exist"
// (ports.ErrPersistenceNotFound) and "exists but belongs to a project the
// caller's own path scope does not name" (ports.ErrScopeMismatch,
// queries.go's own scopeMismatch helper) fold into the identical
// WriteResourceHidden response — V6-02A's own leakage-normalization policy:
// a guessed cross-project ID and a genuinely nonexistent one must be
// indistinguishable to the caller.
func writeQueryError(w http.ResponseWriter, err error) {
	if errors.Is(err, ports.ErrPersistenceNotFound) || errors.Is(err, ports.ErrScopeMismatch) {
		httpapi.WriteResourceHidden(w)
		return
	}
	httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
}

// writeCommandError maps every named sentinel the six mutating commands this
// package wraps (internal/app/work/commands.go's CreateRootWorkItem/
// CreateChildWorkItem, scope_expansion.go's RequestScopeExpansion/
// ApproveScopeExpansion/RejectScopeExpansion/WithdrawScopeExpansion) can
// actually return — enumerated by reading every one of those functions' own
// source, not guessed:
//
//   - ports.ErrPersistenceNotFound / ports.ErrScopeMismatch /
//     ports.ErrCrossProjectReference: the named target (a repository inside
//     the request body, in every case this package's own handlers reach
//     these commands with — the route's OWN primary target is always
//     reloaded and scope-checked by the handler itself before ever
//     dispatching, see workitem_commands.go/scope_expansion_commands.go)
//     either does not exist or belongs to a project the caller's path scope
//     does not name — the identical leakage-normalized WriteResourceHidden
//     response the query side already uses.
//   - ports.ErrReceiptConflict: a genuine concurrent race past this
//     package's own pre-dispatch LookupReceipt fast path (two callers raced
//     the identical Idempotency-Key with two different request bodies) —
//     the command's own internal receipt recheck, inside its
//     WithSerializedWrite transaction, is what actually caught it.
//   - ports.ErrOptimisticConflict: ApproveScopeExpansion's own fenced
//     TransitionTaskFamilyScopeVersion CAS lost against state that already
//     moved.
//   - workapp.ErrRepositoryNotActive: a named repository exists in this
//     project but is not currently ACTIVE — a real, visible business-state
//     conflict (the caller already has this project's own access, so this
//     is not a leakage concern the way a cross-project reference is).
//   - workapp.ErrScopeExpansionNotPending: the targeted ScopeExpansionRequest
//     already carries a terminal decision — a real conflict.
//   - workapp.ErrEffectiveScopeExceedsFamilyScope /
//     workapp.ErrCrossFamilyReference: the request, exactly as given, cannot
//     succeed against the family's own current approved scope/membership —
//     a request-shape problem, not a race.
//
// Every unmatched error falls through to a plain 500 INTERNAL. In practice
// this default is defense-in-depth only: the bare `errors.New(...)`
// field-presence guards at the top of each wrapped command are always
// re-validated by this package's own handler before it ever dispatches (see
// each handler's own explicit field checks right after decoding the body),
// so a well-formed request that reaches a real command should never
// actually trigger one of those guards.
func writeCommandError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ports.ErrPersistenceNotFound),
		errors.Is(err, ports.ErrScopeMismatch),
		errors.Is(err, ports.ErrCrossProjectReference):
		httpapi.WriteResourceHidden(w)
	case errors.Is(err, ports.ErrReceiptConflict),
		errors.Is(err, ports.ErrOptimisticConflict),
		errors.Is(err, workapp.ErrRepositoryNotActive),
		errors.Is(err, workapp.ErrScopeExpansionNotPending):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, err.Error(), nil)
	case errors.Is(err, workapp.ErrEffectiveScopeExceedsFamilyScope),
		errors.Is(err, workapp.ErrCrossFamilyReference):
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
	default:
		httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
	}
}

// writeValidationError writes a single field-level 400 — this package's own
// pre-dispatch body validation (done before ever building a command
// envelope, so a malformed request never even computes a semantic hash).
func writeValidationError(w http.ResponseWriter, field, message string) {
	httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "request validation failed",
		[]httpapi.ErrorDetail{{Field: field, Message: message}})
}

// writeReceiptHashConflict writes the response for httpapi.
// ErrReceiptHashConflict (receiptreplay.go's ReconcileReceipt): the same
// Idempotency-Key was reused with a semantically different request — V6-02's
// own "different body conflict trước I/O", caught here before any real
// command ever dispatches.
func writeReceiptHashConflict(w http.ResponseWriter) {
	httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, "idempotency key reused with a different request", nil)
}

// writePreconditionFailed writes the response for a stale If-Match: the
// caller's own claimed ExpectedVersion no longer matches this package's own
// freshly reloaded authoritative target. Routed through
// httpapi.StatusForAppErrorCode(errorcode.CodePreconditionFailed) rather
// than a hardcoded status/code pair, so this stays byte-for-byte consistent
// with that shared table's own choice (412 Precondition Failed,
// ErrorCodeConflict) if it is ever revisited.
func writePreconditionFailed(w http.ResponseWriter, message string) {
	status, code := httpapi.StatusForAppErrorCode(errorcode.CodePreconditionFailed)
	httpapi.WriteError(w, status, code, message, nil)
}
