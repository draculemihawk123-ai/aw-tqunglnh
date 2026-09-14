package workitem

import (
	"net/http"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// workItemListResponse wraps a WorkItemDetail collection in an object
// (rather than a bare top-level JSON array) so a future field — a page
// cursor, a total count — can be added beside "items" without a breaking
// wire-shape change. Neither ListWorkItems nor ListChildWorkItems below
// implements V6-02A's own cursor pagination: no citation in this task's own
// "Phạm vi" line asks for filter/sort/cursor the way V6-10's own future
// projected Kanban explicitly does, and every comparable list already in
// this codebase (ListProjects, ListFamilyRepositoryScopes,
// ListReleaseSetsForFamily, ...) is the same plain, unbounded shape.
type workItemListResponse struct {
	Items []workapp.WorkItemDetail `json:"items"`
}

// handleGetWorkItem implements
// GET /projects/{projectId}/work-items/{workItemId} (operationId
// getWorkItem): the authoritative (non-projected) WorkItem detail — see
// internal/app/work/queries.go's own top-of-file doc comment for exactly how
// this differs from V6-10's future projected detail.
func handleGetWorkItem(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("projectId")
		workItemID := r.PathValue("workItemId")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "projectId", "is required")
			return
		}
		if strings.TrimSpace(workItemID) == "" {
			writeValidationError(w, "workItemId", "is required")
			return
		}
		detail, err := workapp.GetWorkItem(r.Context(), deps.UnitOfWork, ports.ProjectScope(projectID), workItemID)
		if err != nil {
			writeQueryError(w, err)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, detail, httpapi.ETagFromVersion(detail.Version))
	}
}

// handleListWorkItems implements GET /projects/{projectId}/work-items
// (operationId listWorkItems): every WorkItem in the project, every Kind,
// every Status — see workapp.ListWorkItems' own doc comment.
func handleListWorkItems(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("projectId")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "projectId", "is required")
			return
		}
		items, err := workapp.ListWorkItems(r.Context(), deps.UnitOfWork, ports.ProjectScope(projectID))
		if err != nil {
			writeQueryError(w, err)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, workItemListResponse{Items: items}, "")
	}
}

// handleListChildWorkItems implements
// GET /projects/{projectId}/work-items/{workItemId}/children (operationId
// listChildWorkItems): workItemId's own direct children — see
// workapp.ListChildWorkItems' own doc comment for why this deliberately
// does not recurse into grandchildren.
func handleListChildWorkItems(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("projectId")
		workItemID := r.PathValue("workItemId")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "projectId", "is required")
			return
		}
		if strings.TrimSpace(workItemID) == "" {
			writeValidationError(w, "workItemId", "is required")
			return
		}
		children, err := workapp.ListChildWorkItems(r.Context(), deps.UnitOfWork, ports.ProjectScope(projectID), workItemID)
		if err != nil {
			writeQueryError(w, err)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, workItemListResponse{Items: children}, "")
	}
}

// handleGetWorkItemReadiness implements
// GET /projects/{projectId}/work-items/{workItemId}/readiness (operationId
// getWorkItemReadiness): workapp.ExplainWorkItemReadiness's own real
// workdomain.ValidateReadinessGate explanation — never itself an attempt to
// transition the WorkItem (this task's own "Không làm: không generic
// status/family/workspace setter"; the narrow, named BACKLOG->READY command
// is V6-04A's own separate route, not this one).
func handleGetWorkItemReadiness(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("projectId")
		workItemID := r.PathValue("workItemId")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "projectId", "is required")
			return
		}
		if strings.TrimSpace(workItemID) == "" {
			writeValidationError(w, "workItemId", "is required")
			return
		}
		readiness, err := workapp.ExplainWorkItemReadiness(r.Context(), deps.UnitOfWork, ports.ProjectScope(projectID), workItemID)
		if err != nil {
			writeQueryError(w, err)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, readiness, httpapi.ETagFromVersion(readiness.Version))
	}
}

// handleGetTaskFamily implements
// GET /projects/{projectId}/task-families/{familyId} (operationId
// getTaskFamily): the authoritative TaskFamily detail — this task's own
// "family" concern (Mục tiêu: "expose root/child WorkItem, family/readiness
// và toàn bộ scope-expansion lifecycle"), read-only; no route in this
// package ever sets TaskFamily.Status/ScopeVersion directly (this task's own
// "Không làm: không generic ... family ... setter" — ScopeVersion only ever
// moves via ApproveScopeExpansion's own named transition).
func handleGetTaskFamily(deps Dependencies) http.HandlerFunc {
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
		detail, err := workapp.GetTaskFamily(r.Context(), deps.UnitOfWork, ports.ProjectScope(projectID), familyID)
		if err != nil {
			writeQueryError(w, err)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, detail, httpapi.ETagFromVersion(detail.Version))
	}
}
