package adapterbuild

import (
	"net/http"
	"strings"

	appadapterbuild "github.com/taQuangLing/agent-workflow/internal/app/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// handleListAdapterBuilds implements GET /adapter-builds (operationId
// listAdapterBuilds): a plain public query, dispatching
// appadapterbuild.ListAdapterBuilds verbatim — no filter/sort/cursor, the
// same plain unbounded shape ListProjects/ListWorkItems already use on the
// wire.
func handleListAdapterBuilds(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		builds, err := appadapterbuild.ListAdapterBuilds(r.Context(), deps.UnitOfWork)
		if err != nil {
			writeQueryError(w, err)
			return
		}
		views := make([]adapterBuildView, len(builds))
		for i, build := range builds {
			views[i] = newAdapterBuildView(build)
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, adapterBuildListResponse{Builds: views}, "")
	}
}

// handleGetAdapterBuild implements GET /adapter-builds/{id} (operationId
// getAdapterBuild): a plain public query, dispatching
// appadapterbuild.GetAdapterBuild verbatim. An unknown id maps to the same
// leakage-normalized WriteResourceHidden response every other query handler
// in this codebase uses for its own not-found case (writeQueryError) —
// there is no cross-tenant leakage concern for an installation-scoped
// resource the way there is for a cross-project reference, but reusing the
// one shared funnel keeps every "not found" response in this codebase
// identical in shape regardless of why.
func handleGetAdapterBuild(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if strings.TrimSpace(id) == "" {
			writeValidationError(w, "id", "is required")
			return
		}
		build, err := appadapterbuild.GetAdapterBuild(r.Context(), deps.UnitOfWork, id)
		if err != nil {
			writeQueryError(w, err)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, newAdapterBuildView(build), "")
	}
}
