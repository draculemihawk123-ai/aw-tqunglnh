package rundetail

import (
	"errors"
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// writeQueryError maps a query-side error from internal/app/runtime's own
// GetRunDetail/GetRunGraph/GetRunTimeline to the shared httpapi envelope —
// mirrors internal/delivery/httpapi/evidence and
// internal/delivery/httpapi/message's own identical writeQueryError: an
// unknown RunID (ports.ErrPersistenceNotFound) is leakage-normalized to the
// same WriteResourceHidden response V6-02A's own policy requires.
func writeQueryError(w http.ResponseWriter, err error) {
	if errors.Is(err, ports.ErrPersistenceNotFound) {
		httpapi.WriteResourceHidden(w)
		return
	}
	httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
}

// writeValidationError writes a single field-level 400 — this package's own
// pre-dispatch path/query-parameter validation.
func writeValidationError(w http.ResponseWriter, field, message string) {
	httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "request validation failed",
		[]httpapi.ErrorDetail{{Field: field, Message: message}})
}
