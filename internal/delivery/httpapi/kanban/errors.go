package kanban

import (
	"errors"
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// writeQueryError maps a query-side error (tx.Work().GetWorkItem,
// tx.Projections().Get*/List*, internal/app/work.ExplainWorkItemReadiness)
// to the shared httpapi envelope — mirrors
// internal/delivery/httpapi/workitem's own writeQueryError exactly: both
// "genuinely does not exist" (ports.ErrPersistenceNotFound) and "exists but
// belongs to a project the caller's own path scope does not name"
// (ports.ErrScopeMismatch) fold into the identical WriteResourceHidden
// response — V6-02A's own leakage-normalization policy.
func writeQueryError(w http.ResponseWriter, err error) {
	if errors.Is(err, ports.ErrPersistenceNotFound) || errors.Is(err, ports.ErrScopeMismatch) {
		httpapi.WriteResourceHidden(w)
		return
	}
	httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
}

// writeValidationError writes a single field-level 400 for a missing/blank
// required path segment.
func writeValidationError(w http.ResponseWriter, field, message string) {
	httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "request validation failed",
		[]httpapi.ErrorDetail{{Field: field, Message: message}})
}
