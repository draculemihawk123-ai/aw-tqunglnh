package projectionrebuild

import (
	"net/http"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	appprojectionrebuild "github.com/taQuangLing/agent-workflow/internal/app/projectionrebuild"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// projectionStatusView is GET /projects/{id}/projection's own wire
// response shape — an isolated HTTP-layer DTO (V6-09B's own "Verify:
// isolated schemas" line): it embeds the SHARED httpapi.Freshness envelope
// (freshness.go, V6-02A) rather than re-declaring Generation/
// AsOfJournalPosition/Status a second time, and never exposes
// appprojectionrebuild.ProjectionStatusResult or any ports type directly.
type projectionStatusView struct {
	ProjectID      string            `json:"projectId"`
	ProjectionName string            `json:"projectionName"`
	Freshness      httpapi.Freshness `json:"freshness"`
}

func newProjectionStatusView(result appprojectionrebuild.ProjectionStatusResult) projectionStatusView {
	return projectionStatusView{
		ProjectID: result.ProjectID, ProjectionName: result.ProjectionName,
		Freshness: httpapi.Freshness{
			Generation: int(result.Generation), AsOfJournalPosition: int64(result.Cursor),
			Status: httpapi.FreshnessStatus(result.Status),
		},
	}
}

// handleGetProjectionStatus implements GET /projects/{id}/projection?name=<projectionName>
// (operationId getProjectionStatus): reloads the Project named by {id} via
// catalog.GetProject FIRST (this route's own authoritative target — there
// is no narrower aggregate to reload, see this package's own top-of-file
// doc comment), then dispatches appprojectionrebuild.GetProjectionStatus.
// This never returns 404 for "no generation built yet" — an unbuilt
// projection is a real, expected, STALE freshness result (see
// GetProjectionStatus's own doc comment), never a route failure; only a
// genuinely unknown/unauthorized Project ID or a missing "name" query
// parameter is a failure here.
func handleGetProjectionStatus(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("id")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "id", "is required")
			return
		}
		if _, err := catalog.GetProject(r.Context(), deps.UnitOfWork, ports.ProjectScope(projectID), projectID); err != nil {
			writeQueryError(w, err)
			return
		}

		projectionName := r.URL.Query().Get("name")
		if strings.TrimSpace(projectionName) == "" {
			writeValidationError(w, "name", "is required")
			return
		}

		result, err := appprojectionrebuild.GetProjectionStatus(r.Context(), deps.UnitOfWork, appprojectionrebuild.ProjectionStatusRequest{
			ProjectID: projectID, ProjectionName: projectionName,
		})
		if err != nil {
			writeQueryError(w, err)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, newProjectionStatusView(result), "")
	}
}
