package releaseset

import (
	"errors"
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/releasesetcommit"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
)

// writeQueryError maps a query-side error from internal/app/work/
// release_set_queries.go to the shared httpapi envelope. Both "genuinely
// does not exist" (ports.ErrPersistenceNotFound) and "exists but belongs to
// a project/release-set the caller's own path scope does not name" (this
// package's own handlers check ProjectID/ReleaseSetID after reload, since
// neither GetReleaseSet/ListReleaseSetsForFamily/GetReleaseSetLocalCommitStatus
// take a scope parameter of their own to enforce it internally — see each
// handler's own doc comment) fold into the identical WriteResourceHidden
// response — V6-02A's own leakage-normalization policy: a guessed
// cross-project/cross-release-set ID and a genuinely nonexistent one must
// be indistinguishable to the caller. Mirrors workitem/errors.go's own
// identical helper.
func writeQueryError(w http.ResponseWriter, err error) {
	if errors.Is(err, ports.ErrPersistenceNotFound) || errors.Is(err, ports.ErrScopeMismatch) {
		httpapi.WriteResourceHidden(w)
		return
	}
	httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
}

// writeCommandError maps every named sentinel the four mutating commands
// this package wraps (internal/app/work/release_set.go's CreateReleaseSet/
// SealReleaseSet/AbandonReleaseSet, internal/app/releasesetcommit.
// RequestReleaseSetLocalCommit) can actually return — enumerated by reading
// every one of those functions' own source, not guessed:
//
//   - ports.ErrPersistenceNotFound / ports.ErrScopeMismatch /
//     ports.ErrCrossProjectReference: the named target (the family
//     CreateReleaseSet's own path already reloaded, or a RepositoryWorkspace
//     referenced by requestReleaseSetLocalCommit's own body) either does not
//     exist or belongs to a project/family the caller's path scope does not
//     name — the identical leakage-normalized WriteResourceHidden response
//     the query side already uses.
//   - ports.ErrReceiptConflict: a genuine concurrent race past this
//     package's own pre-dispatch LookupReceipt fast path (two callers raced
//     the identical Idempotency-Key with two different request bodies) —
//     the command's own internal receipt recheck, inside its
//     WithSerializedWrite transaction, is what actually caught it.
//   - ports.ErrOptimisticConflict: a stale ReleaseSet/RepositoryWorkspace
//     version fence lost against state that already moved — Seal/Abandon's
//     own version CAS, or RequestReleaseSetLocalCommit's own
//     ExpectedReleaseSetVersion/ExpectedWorkspaceVersion check.
//   - workapp.ErrReleaseSetNotOpen: Seal/AbandonReleaseSet targeted a
//     ReleaseSet that is already SEALED or ABANDONED — a real, visible
//     business-state conflict (the caller already has this project's own
//     access, so this is not a leakage concern the way a cross-project
//     reference is).
//   - releasesetcommit.ErrWorkspaceNotReady: the target RepositoryWorkspace
//     exists (in-scope) but is not currently READY — a real, visible
//     conflict.
//   - releasesetcommit.ErrReleaseSetEntryNotFound: the ReleaseSet named by
//     the path genuinely has no entry for the requested RepositoryWorkspace's
//     own repository — a request-shape problem, not a race.
//   - ports.ErrLocalCommitMarkerCollision: a genuinely distinct local-commit
//     request (different Idempotency-Key, so never treated as a replay of
//     an earlier one) happens to derive the identical deterministic
//     operation marker as an already-existing intent — a real, visible
//     conflict on the caller's own request, never silently accepted.
//
// Every unmatched error falls through to a plain 500 INTERNAL. In practice
// this default is defense-in-depth only: the bare `errors.New(...)`
// field-presence guards at the top of each wrapped command are always
// re-validated by this package's own handler before it ever dispatches (see
// each handler's own explicit field checks right after decoding the body),
// so a well-formed request that reaches a real command should never
// actually trigger one of those guards. Mirrors workitem/errors.go's own
// identical helper and its own identical reasoning.
func writeCommandError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ports.ErrPersistenceNotFound),
		errors.Is(err, ports.ErrScopeMismatch),
		errors.Is(err, ports.ErrCrossProjectReference):
		httpapi.WriteResourceHidden(w)
	case errors.Is(err, ports.ErrReceiptConflict),
		errors.Is(err, ports.ErrOptimisticConflict),
		errors.Is(err, workapp.ErrReleaseSetNotOpen),
		errors.Is(err, releasesetcommit.ErrWorkspaceNotReady),
		errors.Is(err, ports.ErrLocalCommitMarkerCollision):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, err.Error(), nil)
	case errors.Is(err, releasesetcommit.ErrReleaseSetEntryNotFound):
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
	default:
		httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
	}
}

// writeValidationError writes a single field-level 400 — this package's own
// pre-dispatch body validation (done before ever building a command
// envelope, so a malformed request never even computes a semantic hash).
// Mirrors workitem/errors.go's own identical helper.
func writeValidationError(w http.ResponseWriter, field, message string) {
	httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "request validation failed",
		[]httpapi.ErrorDetail{{Field: field, Message: message}})
}

// writeReceiptHashConflict writes the response for httpapi.
// ErrReceiptHashConflict (receiptreplay.go's ReconcileReceipt): the same
// Idempotency-Key was reused with a semantically different request — V6-02's
// own "different body conflict trước I/O", caught here before any real
// command ever dispatches. Mirrors workitem/errors.go's own identical
// helper.
func writeReceiptHashConflict(w http.ResponseWriter) {
	httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, "idempotency key reused with a different request", nil)
}

// writePreconditionFailed writes the response for a stale If-Match: the
// caller's own claimed ExpectedVersion no longer matches this package's own
// freshly reloaded authoritative target. Routed through
// httpapi.StatusForAppErrorCode(errorcode.CodePreconditionFailed) rather
// than a hardcoded status/code pair, so this stays byte-for-byte consistent
// with that shared table's own choice (412 Precondition Failed,
// ErrorCodeConflict) if it is ever revisited. Mirrors workitem/errors.go's
// own identical helper.
func writePreconditionFailed(w http.ResponseWriter, message string) {
	status, code := httpapi.StatusForAppErrorCode(errorcode.CodePreconditionFailed)
	httpapi.WriteError(w, status, code, message, nil)
}
