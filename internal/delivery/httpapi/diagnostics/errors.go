package diagnostics

import (
	"errors"
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// writeQueryError maps a GetRunDiagnostics error onto the shared httpapi
// envelope — mirrors internal/delivery/httpapi/evidence's own identical
// writeQueryError exactly (that package's own doc comment already explains
// why both ports.ErrPersistenceNotFound and ports.ErrScopeMismatch fold
// into the identical leakage-normalized WriteResourceHidden response,
// V6-02A's own policy): a Run that genuinely does not exist and a Run that
// exists but belongs to a different project than the path names must be
// indistinguishable to the caller.
func writeQueryError(w http.ResponseWriter, err error) {
	if errors.Is(err, ports.ErrPersistenceNotFound) || errors.Is(err, ports.ErrScopeMismatch) {
		httpapi.WriteResourceHidden(w)
		return
	}
	httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
}
