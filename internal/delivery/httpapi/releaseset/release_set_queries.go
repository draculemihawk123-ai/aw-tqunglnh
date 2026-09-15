package releaseset

import (
	"net/http"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// releaseSetListResponse wraps a ReleaseSetDetail collection in an object
// (rather than a bare top-level JSON array) so a future field — a page
// cursor, a total count — can be added beside "items" without a breaking
// wire-shape change. Mirrors workitem's own identical workItemListResponse
// reasoning: ListReleaseSetsForFamily implements no V6-02A cursor
// pagination, the same plain, unbounded shape every comparable list already
// in this codebase (ListWorkItems, ListFamilyRepositoryScopes, ...) uses.
type releaseSetListResponse struct {
	Items []workapp.ReleaseSetDetail `json:"items"`
}

// handleGetReleaseSet implements
// GET /projects/{projectId}/release-sets/{releaseSetId} (operationId
// getReleaseSet): the authoritative ReleaseSet detail. workapp.GetReleaseSet
// takes no scope parameter of its own, so this handler itself confirms the
// loaded detail actually belongs to {projectId} before ever returning it —
// the identical leakage-normalization loadReleaseSetForUpdate
// (release_set_commands.go) already performs for the mutating routes, done
// here inline since this route has no If-Match precondition to also check
// against the same reload.
func handleGetReleaseSet(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("projectId")
		releaseSetID := r.PathValue("releaseSetId")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "projectId", "is required")
			return
		}
		if strings.TrimSpace(releaseSetID) == "" {
			writeValidationError(w, "releaseSetId", "is required")
			return
		}
		detail, ok := loadReleaseSetForUpdate(w, r, deps, projectID, releaseSetID)
		if !ok {
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, detail, httpapi.ETagFromVersion(detail.Version))
	}
}

// handleListReleaseSetsForFamily implements
// GET /projects/{projectId}/task-families/{familyId}/release-sets
// (operationId listReleaseSetsForFamily): every ReleaseSet ever created for
// {familyId}, ordered by (CreatedAt, ID) — see
// workapp.ListReleaseSetsForFamily's own doc comment. The family named by
// {familyId} is reloaded via workapp.GetTaskFamily FIRST — the identical
// "reload the route's real target, scoped" discipline
// handleCreateReleaseSet (release_set_commands.go) already establishes for
// this same path shape.
func handleListReleaseSetsForFamily(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("projectId")
		familyID := r.PathValue("familyId")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "projectId", "is required")
			return
		}
		if strings.TrimSpace(familyID) == "" {
			writeValidationError(w, "familyId", "is required")
			return
		}
		scope := ports.ProjectScope(projectID)
		if _, err := workapp.GetTaskFamily(r.Context(), deps.UnitOfWork, scope, familyID); err != nil {
			writeQueryError(w, err)
			return
		}
		items, err := workapp.ListReleaseSetsForFamily(r.Context(), deps.UnitOfWork, familyID)
		if err != nil {
			writeQueryError(w, err)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, releaseSetListResponse{Items: items}, "")
	}
}
