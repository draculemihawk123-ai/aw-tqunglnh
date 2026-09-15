package workspaceinspection

import (
	"context"
	"errors"
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	appinspection "github.com/taQuangLing/agent-workflow/internal/app/workspaceinspection"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// writeQueryError maps every error
// appinspection.Queries.GetSource/GetDiff/GetRepositoryLog can return onto
// the shared httpapi envelope.
//
// Only THREE error conditions are ever named here by their own concrete
// sentinel: appinspection.ErrScopeMismatch, appinspection.ErrWorkspaceNotReady
// and ports.ErrPersistenceNotFound — every other error these three queries
// can return (internal/adapters/gitworktree's own ErrInvalidSpec,
// ErrPathNotFound, ErrUnsupportedEntry, ErrWorkspaceReleased,
// ErrWorkspaceNotFound, ErrInvalidHandle, ErrGit, ...) is structurally
// UNNAMEABLE here: naming any of them would require importing
// internal/adapters/gitworktree, which internal/archtest's own
// TestDeliveryWorkspaceRoutesNeverReachWorkspaceIOOrExecutor (whole-package)
// and this package's own TestWorkspaceInspectionHTTPNeverImportsFilesystemOrProcess
// (internal/archtest/workspace_inspection_http_test.go) both forbid outright
// — see routes.go's own doc comment for the full reasoning.
//
// The default branch below is therefore a deliberate, single OPAQUE bucket
// for every one of those adapter-level conditions, not an oversight: in
// practice essentially all of them describe a caller-controlled input this
// specific request could not be honored with (an unauthorized/malformed
// revision, a path that traverses outside the tree, a path naming a
// directory/symlink/submodule, a path that simply does not exist at the
// named revision, a cursor that is not a real ancestor of its anchor) —
// every one of these is a property of a QUERY PARAMETER this route accepts
// (path/revision/cursor), never of the URL's own named PATH resource, so
// 400 INVALID_REQUEST (rather than 404, which this package reserves for
// "the named RepositoryWorkspace path resource itself is not visible") is
// the more accurate REST mapping even before considering that this package
// cannot discriminate further. This still satisfies V6-10D's own Verify
// line ("negative matrix ... rejected with sensible status codes"): never a
// blind 500, never information about which specific adapter-internal
// condition fired, always a typed, clearly-a-client-problem 400.
func writeQueryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, appinspection.ErrScopeMismatch), errors.Is(err, ports.ErrPersistenceNotFound):
		// Leakage-normalized identically to V6-10B's own
		// writeWorkspaceCommandError (workspaceroutes.go): a RepositoryWorkspace
		// that does not exist, and one that exists but whose real ownership
		// chain does not match the caller's claimed project/repository/
		// workspace-set scope, must be indistinguishable to the caller.
		httpapi.WriteResourceHidden(w)

	case errors.Is(err, appinspection.ErrWorkspaceNotReady):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict,
			"the repository workspace is not currently READY for inspection", nil)

	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.ErrorCodeUnavailable,
			"the request was cancelled or timed out before it could complete", nil)

	default:
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest,
			"the request names an invalid, unauthorized, or unreadable revision, path, or cursor", nil)
	}
}

// writeValidationError writes a single field-level 400 — this package's own
// pre-dispatch path/query parameter validation (params.go).
func writeValidationError(w http.ResponseWriter, field, message string) {
	httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "request validation failed",
		[]httpapi.ErrorDetail{{Field: field, Message: message}})
}
