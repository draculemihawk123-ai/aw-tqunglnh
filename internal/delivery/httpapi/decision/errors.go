package decision

import (
	"errors"
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// writeResolveApprovalError classifies a non-nil error from
// runtime.ResolveApproval, enumerated by reading that function's own
// source rather than guessed. errorcode.CodePolicyDenied (this task's own
// "unauthorized role" Verify bullet — the cmd.ActorRoles/AuthorizedRoles
// intersection is empty) and any other *apperror.Error ResolveApproval
// returns are handled generically by httpapi.WriteAppError's own
// StatusForAppErrorCode table below (CodePolicyDenied -> 403 Forbidden) —
// this function only names the plain-error sentinels that table cannot
// see.
func writeResolveApprovalError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, runtime.ErrNodeRunMismatch), errors.Is(err, ports.ErrPersistenceNotFound):
		// Defensive: approval.go's own loadApprovalRequestForUpdate reload
		// already catches a stale/cross-run ApprovalRequestID before this
		// command is ever dispatched — reachable only if a genuine race
		// between that reload and this dispatch surfaced one of
		// ResolveApproval's own identical internal checks instead. Same
		// leakage-normalized shape as that reload's own not-found/mismatch
		// branch.
		httpapi.WriteResourceHidden(w)
	case errors.Is(err, ports.ErrReceiptConflict):
		// A genuine race: two concurrent callers with the same
		// Idempotency-Key both passed this handler's own LookupReceipt fast
		// path (neither had committed yet) and one lost ResolveApproval's
		// own authoritative recheck-then-commit race inside its
		// transaction — a different request body under the same key, never
		// a false alarm on a genuine replay.
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict,
			"idempotency key already used with a different request", nil)
	default:
		httpapi.WriteAppError(w, err)
	}
}

// writeSignalWaitError classifies a non-nil error from runtime.SignalWait,
// enumerated by reading that function's own source rather than guessed.
// errorcode.CodeIdempotencyConflict (the SAME SignalKey reused for a
// genuinely different payload — ports.WaitRepository.RecordWaitSignal's
// own contract, wrapped as a real *apperror.Error) is handled generically
// by httpapi.WriteAppError below — this function only names the
// plain-error sentinels that table cannot see.
func writeSignalWaitError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, runtime.ErrNodeRunMismatch), errors.Is(err, ports.ErrPersistenceNotFound):
		// Defensive: wait.go's own loadWaitRegistrationForUpdate reload
		// already catches a stale/cross-run WaitRegistrationID before this
		// command is ever dispatched — same reasoning as
		// writeResolveApprovalError's own identical branch.
		httpapi.WriteResourceHidden(w)
	case errors.Is(err, ports.ErrReceiptConflict):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict,
			"idempotency key already used with a different request", nil)
	default:
		httpapi.WriteAppError(w, err)
	}
}
