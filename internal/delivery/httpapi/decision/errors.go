package decision

import (
	"errors"
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/approvalrelease"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/releasesetcommit"
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
	case errors.Is(err, runtime.ErrCommitNotOffered), errors.Is(err, runtime.ErrCommitRequired):
		// V11-07: the approval node's release policy refused the decision as asked; nothing was written.
		httpapi.WriteError(w, http.StatusUnprocessableEntity, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
	case errors.Is(err, releasesetcommit.ErrCommitIdentityMissing):
		writeIdentityMissing(w, err)
	case errors.Is(err, ports.ErrOptimisticConflict), errors.Is(err, releasesetcommit.ErrWorkspaceNotReady):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict,
			"the repository workspace changed since the changes were reviewed; reload and review again", nil)
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

// writeCommitPrepareError classifies an error from approvalrelease.Preparer.Prepare.
func writeCommitPrepareError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, releasesetcommit.ErrCommitIdentityMissing):
		writeIdentityMissing(w, err)
	case errors.Is(err, approvalrelease.ErrInvalidCommit):
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
	case errors.Is(err, ports.ErrPersistenceNotFound):
		httpapi.WriteResourceHidden(w)
	case errors.Is(err, ports.ErrWorkingTreeTooLarge):
		httpapi.WriteError(w, http.StatusUnprocessableEntity, httpapi.ErrorCodeInvalidRequest,
			"the worktree has too many changed paths to commit; add the generated files to .gitignore or remove them", nil)
	default:
		httpapi.WriteAppError(w, err)
	}
}

// writeIdentityMissing answers 422 with a machine-readable COMMIT_IDENTITY_MISSING detail per repository, so a
// client can tell the approver exactly which repository needs git configured and with which commands.
func writeIdentityMissing(w http.ResponseWriter, err error) {
	var missing *approvalrelease.IdentityMissingError
	var details []httpapi.ErrorDetail
	if errors.As(err, &missing) {
		for _, m := range missing.Missing {
			details = append(details, httpapi.ErrorDetail{Field: "commit.repositories." + m.RepositoryWorkspaceID, Message: "COMMIT_IDENTITY_MISSING: " + m.RepositoryID})
		}
	}
	httpapi.WriteError(w, http.StatusUnprocessableEntity, httpapi.ErrorCodeInvalidRequest, err.Error(), details)
}
