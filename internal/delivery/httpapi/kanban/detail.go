package kanban

import (
	"net/http"
	"strings"

	kanbanapp "github.com/taQuangLing/agent-workflow/internal/app/kanban"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// handleGetWorkItemDetail implements GET /work-items/{workItemId}/detail
// (operationId getWorkItemProjectedDetail): the projected Card (possibly
// stale) plus a FRESH authoritative Readiness — see routes.go's own doc
// comment for why that ordering is what makes the "action race" Verify bullet
// hold.
//
// Both halves are internal/app/kanban's GetWorkItemProjectedDetail, the same
// operation `aw work-item detail` runs; this handler leaves ProjectID empty on
// purpose so the project is derived solely from the WorkItem's own stored row
// (the route has no {projectId} segment — contract point 3, "không tin ID
// shape, payload hoặc projection"). ValidActions is the one thing computed
// here: an HTTP-client affordance (its TargetVersion populates the client's
// next If-Match), derived from the FRESH readiness, never from the card.
func handleGetWorkItemDetail(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workItemID := r.PathValue("workItemId")
		if strings.TrimSpace(workItemID) == "" {
			writeValidationError(w, "workItemId", "is required")
			return
		}

		detail, err := kanbanapp.GetWorkItemProjectedDetail(r.Context(), deps.UnitOfWork, kanbanapp.DetailRequest{WorkItemID: workItemID})
		if err != nil {
			writeQueryError(w, err)
			return
		}

		response := workItemDetailResponse{
			Card: cardToDTO(detail.Card), Readiness: detail.Readiness, Freshness: freshnessToHTTP(detail.Freshness),
			ValidActions: validActionsForReadiness(detail.Readiness),
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, response, httpapi.ETagFromVersion(detail.Readiness.Version))
	}
}
