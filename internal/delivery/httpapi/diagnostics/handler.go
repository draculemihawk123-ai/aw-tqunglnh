package diagnostics

import (
	"net/http"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// handleGetRunDiagnostics is GET
// /projects/{projectId}/runs/{runId}/diagnostics — dispatches
// runtime.GetRunDiagnostics and nothing else (this package's own doc
// comment; internal/archtest's own TestDiagnosticsHTTPNeverReachesSchedulerOrWorkerOrMutatesStateDirectly
// proves it mechanically). ETag reflects the Run's own current Version —
// the one entity every field in this aggregated response is ultimately
// scoped under.
func handleGetRunDiagnostics(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		projectID := strings.TrimSpace(r.PathValue("projectId"))
		runID := strings.TrimSpace(r.PathValue("runId"))
		if projectID == "" {
			httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "projectId path segment is required", nil)
			return
		}
		if runID == "" {
			httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "runId path segment is required", nil)
			return
		}

		diag, err := runtime.GetRunDiagnostics(ctx, deps.UOW, deps.Isolation, deps.Agents, ports.ProjectScope(projectID), runID)
		if err != nil {
			writeQueryError(w, err)
			return
		}

		_ = httpapi.EncodeResult(w, http.StatusOK, toRunDiagnosticsResponse(diag), httpapi.ETagFromVersion(diag.RunVersion))
	}
}
