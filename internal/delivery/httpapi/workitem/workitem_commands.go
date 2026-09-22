package workitem

import (
	"net/http"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// createRootWorkItemBody is POST /projects/{projectId}/work-items' own wire
// request shape — deliberately WITHOUT a projectId field: the project is
// always the path's own {projectId}, reloaded and confirmed to exist before
// this body is even decoded (Contract chung §3: "không tin ID shape, payload
// hoặc projection"), never re-declared by the caller's own body.
type createRootWorkItemBody struct {
	Title        string           `json:"title"`
	InitialScope []scopeGrantBody `json:"initialScope"`
	// Contract is the optional readiness contract (V6-04B); omitted means the
	// WorkItem is created BACKLOG with an empty contract, exactly as before.
	// It is a pointer with omitempty so a body without it canonicalizes to the
	// same bytes — and therefore the same idempotency hash — it always did.
	Contract *workItemContractBody `json:"contract,omitempty"`
}

// handleCreateRootWorkItem implements POST /projects/{projectId}/work-items
// (operationId createRootWorkItem): dispatches internal/app/work.
// CreateRootWorkItem, which atomically creates the root WorkItem, its owning
// TaskFamily, a WorkspaceSet intent, the initial RepositoryScope grants and
// one WORKSPACE_PROVISION job per repository — see that function's own doc
// comment for the full contract this handler is a thin transport over. The
// project named in the path is reloaded via catalog.GetProject FIRST,
// unconditionally (before Idempotency-Key/body are even read) — V6-02's own
// "authorization chạy lại cả khi receipt replay" applied to the one thing
// there is to authorize against here: the project must actually exist.
func handleCreateRootWorkItem(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("projectId")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "projectId", "is required")
			return
		}
		scope := ports.ProjectScope(projectID)
		if _, err := catalog.GetProject(r.Context(), deps.UnitOfWork, scope, projectID); err != nil {
			writeQueryError(w, err)
			return
		}

		var body createRootWorkItemBody
		cmd, ok := prepareCreateCommand(w, r, deps, "CreateRootWorkItem", scope, &body)
		if !ok {
			return
		}
		if strings.TrimSpace(body.Title) == "" {
			writeValidationError(w, "title", "is required")
			return
		}
		if !validateScopeGrantBodies(w, "initialScope", body.InitialScope) {
			return
		}
		contract := body.Contract.toRequest()
		if err := contract.Validate(); err != nil {
			writeCommandError(w, err)
			return
		}

		if replayOrProceed(r.Context(), w, deps, cmd) {
			return
		}

		result, err := workapp.CreateRootWorkItem(r.Context(), deps.UnitOfWork, deps.IDs, cmd, workapp.CreateRootWorkItemRequest{
			ProjectID: projectID, Title: body.Title, InitialScope: toScopeGrantRequests(body.InitialScope),
			Contract: contract,
		})
		if err != nil {
			writeCommandError(w, err)
			return
		}
		// A freshly created WorkItem/TaskFamily/WorkspaceSet always starts at
		// Version 1 (work.NewRootWorkItem/NewTaskFamily/workspace.NewWorkspaceSet
		// all hardcode it) — no extra reload is needed to know the ETag.
		_ = httpapi.EncodeResult(w, http.StatusCreated, result, httpapi.ETagFromVersion(1))
	}
}

// createChildWorkItemBody is POST /projects/{projectId}/work-items/{workItemId}/children's
// own wire request shape — deliberately without parentWorkItemId (the
// path's own {workItemId}) or projectId (the path's own {projectId}).
type createChildWorkItemBody struct {
	Title            string           `json:"title"`
	ParentJoinPolicy string           `json:"parentJoinPolicy"`
	SourceNodeRunID  string           `json:"sourceNodeRunId,omitempty"`
	EffectiveScope   []scopeGrantBody `json:"effectiveScope"`
	// Contract is the child's own optional readiness contract (V6-04B), never
	// inherited from the parent — see createRootWorkItemBody.Contract.
	Contract *workItemContractBody `json:"contract,omitempty"`
}

// handleCreateChildWorkItem implements
// POST /projects/{projectId}/work-items/{workItemId}/children (operationId
// createChildWorkItem): dispatches internal/app/work.CreateChildWorkItem.
// The parent named by {workItemId} is reloaded via workapp.GetWorkItem FIRST
// (this package's own authoritative query, which itself rejects a parent
// belonging to another project) — the identical "reload before
// Idempotency-Key/body are even read" discipline handleCreateRootWorkItem
// above already establishes, applied here to a WorkItem target instead of a
// Project.
func handleCreateChildWorkItem(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("projectId")
		parentWorkItemID := r.PathValue("workItemId")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "projectId", "is required")
			return
		}
		if strings.TrimSpace(parentWorkItemID) == "" {
			writeValidationError(w, "workItemId", "is required")
			return
		}
		scope := ports.ProjectScope(projectID)
		if _, err := workapp.GetWorkItem(r.Context(), deps.UnitOfWork, scope, parentWorkItemID); err != nil {
			writeQueryError(w, err)
			return
		}

		var body createChildWorkItemBody
		cmd, ok := prepareCreateCommand(w, r, deps, "CreateChildWorkItem", scope, &body)
		if !ok {
			return
		}
		if strings.TrimSpace(body.Title) == "" {
			writeValidationError(w, "title", "is required")
			return
		}
		if strings.TrimSpace(body.ParentJoinPolicy) == "" {
			writeValidationError(w, "parentJoinPolicy", "is required")
			return
		}
		if !validateScopeGrantBodies(w, "effectiveScope", body.EffectiveScope) {
			return
		}
		contract := body.Contract.toRequest()
		if err := contract.Validate(); err != nil {
			writeCommandError(w, err)
			return
		}

		if replayOrProceed(r.Context(), w, deps, cmd) {
			return
		}

		result, err := workapp.CreateChildWorkItem(r.Context(), deps.UnitOfWork, deps.IDs, cmd, workapp.CreateChildWorkItemRequest{
			ParentWorkItemID: parentWorkItemID, Title: body.Title, ParentJoinPolicy: body.ParentJoinPolicy,
			SourceNodeRunID: body.SourceNodeRunID, EffectiveScope: toScopeGrantRequests(body.EffectiveScope),
			Contract: contract,
		})
		if err != nil {
			writeCommandError(w, err)
			return
		}
		// A freshly created child WorkItem always starts at Version 1
		// (work.NewChildWorkItem hardcodes it) — no extra reload needed.
		_ = httpapi.EncodeResult(w, http.StatusCreated, result, httpapi.ETagFromVersion(1))
	}
}
