package releaseset

import (
	"net/http"
	"strings"

	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// handleGetReleaseSetLocalCommitStatus implements
// GET /projects/{projectId}/release-sets/{releaseSetId}/local-commits/{localCommitId}
// (operationId getReleaseSetLocalCommitStatus): dispatches this task's own
// new workapp.GetReleaseSetLocalCommitStatus query
// (internal/app/work/release_set_queries.go) — the "per-entry local-commit
// ... status" half of V6-10F's own Phạm vi line, which did not exist
// anywhere in this codebase before this task (only the raw Tx-level
// ports.WorkRepository.GetReleaseSetLocalCommit existed, reachable only
// from inside an already-open transaction).
//
// Like handleGetReleaseSet, GetReleaseSetLocalCommitStatus takes no scope
// parameter of its own, so this handler confirms the loaded status
// actually belongs to BOTH {projectId} AND {releaseSetId} before ever
// returning it — the second check (ReleaseSetID) is this route's own
// extra layer beyond loadReleaseSetForUpdate's identical ProjectID check:
// a ReleaseSetLocalCommitID genuinely exists and belongs to the right
// project, but was requested against a DIFFERENT release set's own URL,
// must be just as invisible as a genuinely unknown ID — the identical
// leakage-normalization principle applied to the path's own nesting, not
// only its scope.
//
// This is deliberately a plain, per-operation status read — never an
// aggregate "some/all" summary across a ReleaseSet's own multiple
// local-commit operations (V6-10F's own "partial" Verify line) — see
// workapp.GetReleaseSetLocalCommitStatus's own doc comment for why that is
// the correct shape: a caller that wants to know where every repository in
// a ReleaseSet stands calls this once per operation ID it already holds.
func handleGetReleaseSetLocalCommitStatus(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("projectId")
		releaseSetID := r.PathValue("releaseSetId")
		localCommitID := r.PathValue("localCommitId")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "projectId", "is required")
			return
		}
		if strings.TrimSpace(releaseSetID) == "" {
			writeValidationError(w, "releaseSetId", "is required")
			return
		}
		if strings.TrimSpace(localCommitID) == "" {
			writeValidationError(w, "localCommitId", "is required")
			return
		}

		status, err := workapp.GetReleaseSetLocalCommitStatus(r.Context(), deps.UnitOfWork, localCommitID)
		if err != nil {
			writeQueryError(w, err)
			return
		}
		if status.ProjectID != projectID || status.ReleaseSetID != releaseSetID {
			httpapi.WriteResourceHidden(w)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, status, httpapi.ETagFromVersion(status.Version))
	}
}
