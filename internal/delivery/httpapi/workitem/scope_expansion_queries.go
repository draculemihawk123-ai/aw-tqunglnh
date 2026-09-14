package workitem

import (
	"net/http"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// handleGetScopeExpansionRequest implements
// GET /projects/{projectId}/scope-expansions/{requestId} (operationId
// getScopeExpansionRequest): the authoritative ScopeExpansionRequest detail
// — everything a caller needs to decide (approve/reject) or withdraw it,
// including its own current Version echoed as this response's ETag, which a
// later approve/reject/withdraw call supplies back as If-Match.
func handleGetScopeExpansionRequest(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("projectId")
		requestID := r.PathValue("requestId")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "projectId", "is required")
			return
		}
		if strings.TrimSpace(requestID) == "" {
			writeValidationError(w, "requestId", "is required")
			return
		}
		detail, err := workapp.GetScopeExpansionRequest(r.Context(), deps.UnitOfWork, ports.ProjectScope(projectID), requestID)
		if err != nil {
			writeQueryError(w, err)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, detail, httpapi.ETagFromVersion(detail.Version))
	}
}
