package rundetail

import (
	"net/http"
	"strings"

	runtimeapp "github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// handleGetRunDetail implements GET /runs/{id} (operationId getRunDetail):
// runtimeapp.GetRunDetail's own bounded, single-resource view — the Run's
// own row, its pinned ExecutionManifest and its RunManifestAmendment
// history. Not paginated (this task's own "manifest revision" scope is
// naturally bounded — see run_detail_queries.go's own RunDetail doc
// comment).
func handleGetRunDetail(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		runID := r.PathValue("id")
		if strings.TrimSpace(runID) == "" {
			writeValidationError(w, "id", "is required")
			return
		}
		detail, err := runtimeapp.GetRunDetail(ctx, deps.UnitOfWork, runID)
		if err != nil {
			writeQueryError(w, err)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, detail, "")
	}
}
