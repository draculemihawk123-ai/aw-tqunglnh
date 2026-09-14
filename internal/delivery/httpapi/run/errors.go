package run

import (
	"errors"
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// writeStartWorkflowRunError classifies a non-nil error from
// runtime.StartWorkflowRun into the canonical httpapi error envelope. Every
// one of these sentinels (internal/app/runtime/commands.go) is a plain
// error, not an *apperror.Error — httpapi.WriteAppError's own errors.As
// check would never match any of them and would fall through to a
// misleading generic 500 for what are really ordinary, expected 409/404
// outcomes. This function is the "precise mapping before the generic
// fallback" every route task owns for its own command's own error
// vocabulary (errors.go's own StatusForAppErrorCode doc comment: a shared
// generic table "must never be used for a scoped resource lookup's own
// not-found/unauthorized branches" — the identical principle applies to any
// other route-specific sentinel set with its own meaning).
//
// Messages are deliberately re-worded, safe, generic text — never err.Error()
// verbatim — matching apperror.Error's own "Message... safe, public-facing
// half" discipline (internal/app/apperror/apperror.go) even though these
// particular sentinels happen to be raw errors, not *apperror.Error values.
func writeStartWorkflowRunError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ports.ErrReceiptConflict):
		// A genuine race: two concurrent callers with the same
		// Idempotency-Key both passed this handler's own httpapi.LookupReceipt
		// fast path (neither had committed yet) and one lost
		// StartWorkflowRun's own authoritative recheck-then-commit race
		// inside its transaction — a different request body under the same
		// key, never a false alarm on a genuine replay (see start.go's own
		// NOTE on the safe, non-error concurrent-replay case).
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict,
			"idempotency key already used with a different request", nil)
	case errors.Is(err, ports.ErrPersistenceNotFound):
		// A race between this handler's own pre-check reload
		// (loadWorkItemProjectID) and StartWorkflowRun's own internal
		// reload — e.g. a WorkItem/WorkflowVersion/WorkspaceSet reference
		// that stopped resolving in between. Same leakage-normalized shape
		// as the pre-check's own not-found branch.
		httpapi.WriteResourceHidden(w)
	case errors.Is(err, runtime.ErrWorkItemNotReady):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict,
			"the work item is not READY; a run can only be started for a READY work item", nil)
	case errors.Is(err, runtime.ErrWorkspaceNotReady):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict,
			"the work item's workspace set is not READY", nil)
	case errors.Is(err, runtime.ErrWorkflowVersionMismatch):
		// This task's own "pinned conflict" Verify bullet: the work item is
		// already pinned (StartWorkflowRun's own "start pins manifest/
		// version" Thực hiện line) to a different WorkflowVersionID than
		// requested.
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict,
			"the work item is already pinned to a different workflow version", nil)
	case errors.Is(err, runtime.ErrWorkItemCancellationPending):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict,
			"the work item has a pending cancellation request", nil)
	case errors.Is(err, runtime.ErrMissingStartNode):
		// Defensive/should-be-unreachable per commands.go's own doc comment
		// (the workflow compiler already refuses to publish a document
		// without exactly one START node) — a corrupted/foreign
		// WorkflowVersion reaching here is a genuine internal-error case,
		// not a client mistake.
		httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
	default:
		httpapi.WriteAppError(w, err)
	}
}

// writeCancelRunError classifies a non-nil error from runtime.CancelRun.
// Duplicate-call and already-CANCELLING/-CANCELLED are NOT errors at all
// (cancelRunTx's own AlreadyRequested branch returns a nil error) — this
// function only ever sees the genuine failure cases.
func writeCancelRunError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ports.ErrPersistenceNotFound):
		// A race between requireRunExists' own pre-check and CancelRun's
		// own internal reload (the Run would have to disappear in between —
		// this codebase never deletes a WorkflowRun row, so this is a
		// defensive branch, not an expected path).
		httpapi.WriteResourceHidden(w)
	case errors.Is(err, runtime.ErrRunAlreadyTerminal):
		// ADR-020's own "chỉ PASS và FAIL làm Run terminal": a genuinely
		// finished Run refuses cancellation outright rather than accepting
		// it as a silent no-op — never conflated with the CANCELLING/
		// CANCELLED "already requested" case, which is not an error.
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict,
			"the run has already finished (SUCCEEDED or FAILED) and cannot be cancelled", nil)
	default:
		httpapi.WriteAppError(w, err)
	}
}
