package releaseset

import (
	"net/http"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// createReleaseSetBody is
// POST /projects/{projectId}/task-families/{familyId}/release-sets' own
// wire request shape — deliberately without familyId (the path's own
// {familyId}) or projectId (the path's own {projectId}), mirroring
// workitem's requestScopeExpansionBody's own identical exclusion of its own
// path segment.
type createReleaseSetBody struct {
	Repositories []repositoryReleaseBody `json:"repositories"`
}

// handleCreateReleaseSet implements
// POST /projects/{projectId}/task-families/{familyId}/release-sets
// (operationId createReleaseSet): dispatches internal/app/work.
// CreateReleaseSet. The family named by {familyId} is reloaded via
// workapp.GetTaskFamily FIRST — the same "reload the route's real target
// before Idempotency-Key/body are even read" discipline every mutating
// handler in this codebase's HTTP layer follows (workitem's
// handleRequestScopeExpansion is the direct precedent for a create whose
// primary target is named entirely by the path, never re-declared by the
// caller's own body).
func handleCreateReleaseSet(deps Dependencies) http.HandlerFunc {
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

		var body createReleaseSetBody
		cmd, ok := prepareCreateCommand(w, r, deps, "CreateReleaseSet", scope, &body)
		if !ok {
			return
		}
		if !validateRepositoryReleaseBodies(w, body.Repositories) {
			return
		}

		if replayOrProceed(r.Context(), w, deps, cmd) {
			return
		}

		result, err := workapp.CreateReleaseSet(r.Context(), deps.UnitOfWork, deps.IDs, cmd, workapp.CreateReleaseSetRequest{
			ProjectID: projectID, FamilyID: familyID, Repositories: toRepositoryReleaseRequests(body.Repositories),
		})
		if err != nil {
			writeCommandError(w, err)
			return
		}
		// A freshly created ReleaseSet always starts at Version 1
		// (workdomain.NewReleaseSet hardcodes it).
		_ = httpapi.EncodeResult(w, http.StatusCreated, result, httpapi.ETagFromVersion(1))
	}
}

// loadReleaseSetForUpdate reloads releaseSetID via workapp.GetReleaseSet and
// confirms it actually belongs to the project named by scope — that query
// takes no scope parameter of its own (release_set_queries.go's own doc
// comment: "V6-10F ... future HTTP/CLI layer" reads it directly), so this
// package's own handler is the one place that check happens, folding both
// "not found" and "wrong project" into the identical leakage-normalized
// WriteResourceHidden response (mirrors workitem's own
// loadScopeExpansionRequestForUpdate exactly). Every one of Seal/
// AbandonReleaseSet's own handlers, plus requestReleaseSetLocalCommit and
// getReleaseSetLocalCommitStatus (local_commit_commands.go,
// local_commit_queries.go), calls this exactly once, reusing the same
// loaded detail both to authorize (this call) and, for the two update
// routes, to check the caller's own If-Match precondition further down —
// never a second reload.
func loadReleaseSetForUpdate(w http.ResponseWriter, r *http.Request, deps Dependencies, projectID, releaseSetID string) (workapp.ReleaseSetDetail, bool) {
	detail, err := workapp.GetReleaseSet(r.Context(), deps.UnitOfWork, releaseSetID)
	if err != nil {
		writeQueryError(w, err)
		return workapp.ReleaseSetDetail{}, false
	}
	if detail.ProjectID != projectID {
		httpapi.WriteResourceHidden(w)
		return workapp.ReleaseSetDetail{}, false
	}
	return detail, true
}

// handleSealReleaseSet implements
// POST /projects/{projectId}/release-sets/{releaseSetId}/seal (operationId
// sealReleaseSet): dispatches internal/app/work.SealReleaseSet.
func handleSealReleaseSet(deps Dependencies) http.HandlerFunc {
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

		var body emptyBody
		cmd, expectedVersion, ok := prepareUpdateCommand(w, r, deps, "SealReleaseSet", ports.ProjectScope(projectID), &body)
		if !ok {
			return
		}

		if replayOrProceed(r.Context(), w, deps, cmd) {
			return
		}
		// "nếu absent mới kiểm current version" (V6-02's own flow) — checked
		// here, AFTER the replay decision, against the target already
		// reloaded above.
		if detail.Version != expectedVersion {
			writePreconditionFailed(w, "If-Match does not match the current version; reload and retry")
			return
		}

		result, err := workapp.SealReleaseSet(r.Context(), deps.UnitOfWork, cmd, workapp.SealReleaseSetRequest{ReleaseSetID: releaseSetID})
		if err != nil {
			writeCommandError(w, err)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, result, httpapi.ETagFromVersion(result.Version))
	}
}

// handleAbandonReleaseSet implements
// POST /projects/{projectId}/release-sets/{releaseSetId}/abandon
// (operationId abandonReleaseSet): dispatches internal/app/work.
// AbandonReleaseSet.
func handleAbandonReleaseSet(deps Dependencies) http.HandlerFunc {
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

		var body emptyBody
		cmd, expectedVersion, ok := prepareUpdateCommand(w, r, deps, "AbandonReleaseSet", ports.ProjectScope(projectID), &body)
		if !ok {
			return
		}

		if replayOrProceed(r.Context(), w, deps, cmd) {
			return
		}
		if detail.Version != expectedVersion {
			writePreconditionFailed(w, "If-Match does not match the current version; reload and retry")
			return
		}

		result, err := workapp.AbandonReleaseSet(r.Context(), deps.UnitOfWork, cmd, workapp.AbandonReleaseSetRequest{ReleaseSetID: releaseSetID})
		if err != nil {
			writeCommandError(w, err)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, result, httpapi.ETagFromVersion(result.Version))
	}
}
