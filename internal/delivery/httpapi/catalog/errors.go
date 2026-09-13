package catalog

import (
	"errors"
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// writeCatalogError maps an error internal/app/catalog's own command/query
// functions can return onto the canonical httpapi error envelope. Every
// one of those functions returns a PLAIN, unwrapped sentinel
// (ports.ErrPersistenceNotFound/ErrOptimisticConflict/
// ErrCrossProjectReference/ErrScopeMismatch/ErrReceiptConflict) for every
// condition it itself classifies, and a plain errors.New(...) (e.g.
// project.NewProject/NewRepository/NewComponentPackAssignment's own
// validation, or catalog.CreateProject's own "Name is required") for a
// caller-input problem it does not classify any further —
// internal/adapters/sqlite's own MapSQLiteError (txrunner.go) is the ONLY
// place a genuine unexpected persistence failure becomes a real
// *apperror.Error (CodeUnavailable for lock contention, CodeInternal
// otherwise), and it deliberately never touches an already-classified
// condition (e.g. getRepositoryTx's own ErrPersistenceNotFound branch is
// returned directly, never routed through MapSQLiteError).
//
// This function's branch order follows exactly that three-bucket reality:
// a known ports sentinel first (each maps to its own specific status), a
// real *apperror.Error second (StatusForAppErrorCode already owns that
// table), and — only once neither matches — a plain, unclassified error is
// treated as the caller's own request being invalid (400 Bad Request).
// That default bucket is the only remaining condition a bare
// internal/app/catalog call in this package's own handlers can produce in
// practice: none of them perform filesystem/process/network I/O of their
// own that could fail in some fourth, unclassified way, so "not one of the
// known sentinels, not a wrapped persistence failure" leaves only "the
// domain layer rejected this input" as the realistic remaining cause.
func writeCatalogError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ports.ErrPersistenceNotFound):
		httpapi.WriteResourceHidden(w)
	case errors.Is(err, ports.ErrOptimisticConflict):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, err.Error(), nil)
	case errors.Is(err, ports.ErrCrossProjectReference):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, err.Error(), nil)
	case errors.Is(err, ports.ErrReceiptConflict):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, err.Error(), nil)
	case errors.Is(err, ports.ErrScopeMismatch):
		httpapi.WriteError(w, http.StatusForbidden, httpapi.ErrorCodeForbidden, err.Error(), nil)
	default:
		var appErr *apperror.Error
		if errors.As(err, &appErr) {
			httpapi.WriteAppError(w, err)
			return
		}
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
	}
}
