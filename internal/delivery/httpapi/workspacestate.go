package httpapi

import (
	"net/http"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workspacestate"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// repositoryWorkspaceStateResponse is the wire DTO for one
// workspacestate.RepositoryWorkspaceState — a delivery-owned shape distinct
// from the application-layer type, following V6-02A's own convention that
// this package owns its own wire vocabulary rather than serializing an
// internal/app type verbatim. ValidActions is always populated (never
// omitted, even when empty) so a client never has to special-case "field
// absent" versus "no actions right now".
type repositoryWorkspaceStateResponse struct {
	RepositoryWorkspaceID  string        `json:"repositoryWorkspaceId"`
	WorkspaceSetID         string        `json:"workspaceSetId"`
	RepositoryID           string        `json:"repositoryId"`
	Generation             uint64        `json:"generation"`
	State                  string        `json:"state"`
	Version                uint64        `json:"version"`
	BranchRef              string        `json:"branchRef,omitempty"`
	CurrentRevision        string        `json:"currentRevision,omitempty"`
	LastProvisionErrorCode *string       `json:"lastProvisionErrorCode,omitempty"`
	HasActiveWriteLease    bool          `json:"hasActiveWriteLease"`
	ValidActions           []ValidAction `json:"validActions"`
}

// workspaceSetStateResponse is the wire DTO for a whole
// workspacestate.WorkspaceSetState, including every one of its own
// RepositoryWorkspace children.
type workspaceSetStateResponse struct {
	WorkspaceSetID       string                             `json:"workspaceSetId"`
	FamilyID             string                             `json:"familyId"`
	ProjectID            string                             `json:"projectId"`
	State                string                             `json:"state"`
	Version              uint64                             `json:"version"`
	HasBaseRevisionSet   bool                               `json:"hasBaseRevisionSet"`
	RepositoryWorkspaces []repositoryWorkspaceStateResponse `json:"repositoryWorkspaces"`
	ValidActions         []ValidAction                      `json:"validActions"`
}

// reconcileValidActions returns the advisory
// requestWorkspaceReconciliation ValidAction for rw — this mirrors
// workspacereconcile.RequestWorkspaceReconciliation's own eligibility check
// EXACTLY (state must be READY or QUARANTINED, ports.ErrWorkspaceNotReconcilable
// otherwise): unlike the release action below, no other live state (lease,
// job) gates reconcile eligibility, so this heuristic is not an
// approximation of the real check, it IS the real check's own state
// predicate, restated. Still advisory, never authority (ValidAction's own
// doc comment, action.go): the real command re-validates
// cmd.ExpectedVersion/current state itself regardless.
func reconcileValidActions(rw workspacestate.RepositoryWorkspaceState) []ValidAction {
	if rw.State != workspace.RepositoryWorkspaceReady && rw.State != workspace.RepositoryWorkspaceQuarantined {
		return []ValidAction{}
	}
	return []ValidAction{{
		OperationID: "requestWorkspaceReconciliation", ScopeKind: ScopeProject, TargetVersion: int64(rw.Version),
	}}
}

func toRepositoryWorkspaceStateResponse(rw workspacestate.RepositoryWorkspaceState) repositoryWorkspaceStateResponse {
	return repositoryWorkspaceStateResponse{
		RepositoryWorkspaceID: rw.RepositoryWorkspaceID, WorkspaceSetID: rw.WorkspaceSetID, RepositoryID: rw.RepositoryID,
		Generation: rw.Generation, State: string(rw.State), Version: rw.Version, BranchRef: rw.BranchRef,
		CurrentRevision: rw.CurrentRevision, LastProvisionErrorCode: rw.LastProvisionErrorCode, HasActiveWriteLease: rw.HasActiveWriteLease,
		ValidActions: reconcileValidActions(rw),
	}
}

// releaseValidActions returns the advisory requestWorkspaceSetRelease
// ValidAction for state — a best-effort, DELIBERATELY INCOMPLETE mirror of
// workspacerelease.RequestWorkspaceSetRelease's own eligibility gate: it
// reuses the two conditions already loaded by this same read (no
// RepositoryWorkspace is QUARANTINED, none currently HasActiveWriteLease)
// plus the terminal-state check, but it never queries
// ports.JobsRepository.HasActiveJobForAggregateIDs or
// ports.ReleaseEligibilityAuthority.IsReleaseAuthorized — doing either here
// would mean this read-only state query starts consulting an external
// authority/second table on every GET merely to populate an advisory hint,
// which is exactly the extra I/O ValidAction's own contract says a client
// must never rely on being exhaustive for. A caller whose real POST still
// gets ErrWorkspaceSetHasActiveJob or ErrReleaseNotAuthorized despite seeing
// this action offered is exactly the expected, documented "advisory, not
// authority" case (action.go's own doc comment) — not a bug in this
// heuristic.
func releaseValidActions(state workspacestate.WorkspaceSetState) []ValidAction {
	if state.State == workspace.WorkspaceSetReleased || state.State == workspace.WorkspaceSetReleasing {
		return []ValidAction{}
	}
	for _, rw := range state.RepositoryWorkspaces {
		if rw.State == workspace.RepositoryWorkspaceQuarantined || rw.HasActiveWriteLease {
			return []ValidAction{}
		}
	}
	return []ValidAction{{
		OperationID: "requestWorkspaceSetRelease", ScopeKind: ScopeProject, TargetVersion: int64(state.Version),
	}}
}

func toWorkspaceSetStateResponse(state workspacestate.WorkspaceSetState) workspaceSetStateResponse {
	repoResponses := make([]repositoryWorkspaceStateResponse, 0, len(state.RepositoryWorkspaces))
	for _, rw := range state.RepositoryWorkspaces {
		repoResponses = append(repoResponses, toRepositoryWorkspaceStateResponse(rw))
	}
	return workspaceSetStateResponse{
		WorkspaceSetID: state.WorkspaceSetID, FamilyID: state.FamilyID, ProjectID: state.ProjectID,
		State: string(state.State), Version: state.Version, HasBaseRevisionSet: state.HasBaseRevisionSet,
		RepositoryWorkspaces: repoResponses, ValidActions: releaseValidActions(state),
	}
}

// getWorkspaceSetStateHandler is GET
// /projects/{projectId}/workspace-sets/{familyId} — dispatches
// workspacestate.GetWorkspaceSetState and nothing else; see this package's
// own workspaceroutes.go doc comment and
// internal/archtest's own TestDeliveryWorkspaceRoutesNeverReachWorkspaceIOOrExecutor
// for what "nothing else" is enforced to mean.
func getWorkspaceSetStateHandler(uow ports.UnitOfWork) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := strings.TrimSpace(r.PathValue("projectId"))
		familyID := strings.TrimSpace(r.PathValue("familyId"))
		if projectID == "" || familyID == "" {
			WriteError(w, http.StatusBadRequest, ErrorCodeInvalidRequest, "projectId and familyId path segments are required", nil)
			return
		}

		state, err := workspacestate.GetWorkspaceSetState(r.Context(), uow, workspacestate.GetWorkspaceSetStateRequest{
			ProjectID: projectID, FamilyID: familyID,
		})
		if err != nil {
			writeWorkspaceCommandError(w, err)
			return
		}
		_ = EncodeResult(w, http.StatusOK, toWorkspaceSetStateResponse(state), ETagFromVersion(state.Version))
	}
}

// getRepositoryWorkspaceStateHandler is GET
// /projects/{projectId}/repository-workspaces/{repositoryWorkspaceId}.
func getRepositoryWorkspaceStateHandler(uow ports.UnitOfWork) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := strings.TrimSpace(r.PathValue("projectId"))
		repositoryWorkspaceID := strings.TrimSpace(r.PathValue("repositoryWorkspaceId"))
		if projectID == "" || repositoryWorkspaceID == "" {
			WriteError(w, http.StatusBadRequest, ErrorCodeInvalidRequest, "projectId and repositoryWorkspaceId path segments are required", nil)
			return
		}

		state, err := workspacestate.GetRepositoryWorkspaceState(r.Context(), uow, workspacestate.GetRepositoryWorkspaceStateRequest{
			ProjectID: projectID, RepositoryWorkspaceID: repositoryWorkspaceID,
		})
		if err != nil {
			writeWorkspaceCommandError(w, err)
			return
		}
		_ = EncodeResult(w, http.StatusOK, toRepositoryWorkspaceStateResponse(state), ETagFromVersion(state.Version))
	}
}
