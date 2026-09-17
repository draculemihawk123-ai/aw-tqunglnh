package projectionrebuild

import (
	"net/http"
	"strings"
	"time"

	appprojectionrebuild "github.com/taQuangLing/agent-workflow/internal/app/projectionrebuild"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// projectionRebuildOperationStatusView is
// GET /projects/{id}/projection/rebuild-operations/{operationId}'s own
// wire response shape — an isolated HTTP-layer DTO (V6-09B's own "Verify:
// isolated schemas" line) mirroring
// appprojectionrebuild.ProjectionRebuildStatus field for field rather than
// returning that app-layer type on the wire directly.
type projectionRebuildOperationStatusView struct {
	OperationID      string    `json:"operationId"`
	ProjectID        string    `json:"projectId"`
	ProjectionName   string    `json:"projectionName"`
	Phase            string    `json:"phase"`
	W0               *uint64   `json:"w0,omitempty"`
	ShadowGeneration *uint64   `json:"shadowGeneration,omitempty"`
	ShadowCursor     *uint64   `json:"shadowCursor,omitempty"`
	CutoverCursor    *uint64   `json:"cutoverCursor,omitempty"`
	ErrorCode        string    `json:"errorCode,omitempty"`
	ErrorMessage     string    `json:"errorMessage,omitempty"`
	JobID            string    `json:"jobId"`
	RequestedAt      time.Time `json:"requestedAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
	Version          uint64    `json:"version"`
}

func newProjectionRebuildOperationStatusView(status appprojectionrebuild.ProjectionRebuildStatus) projectionRebuildOperationStatusView {
	return projectionRebuildOperationStatusView{
		OperationID: status.OperationID, ProjectID: status.ProjectID, ProjectionName: status.ProjectionName,
		Phase: status.Phase, W0: status.W0, ShadowGeneration: status.ShadowGeneration,
		ShadowCursor: status.ShadowCursor, CutoverCursor: status.CutoverCursor,
		ErrorCode: status.ErrorCode, ErrorMessage: status.ErrorMessage, JobID: status.JobID,
		RequestedAt: status.RequestedAt, UpdatedAt: status.UpdatedAt, Version: status.Version,
	}
}

// handleGetProjectionRebuildOperationStatus implements
// GET /projects/{id}/projection/rebuild-operations/{operationId}
// (operationId getProjectionRebuildOperationStatus): dispatches
// appprojectionrebuild.GetProjectionRebuildStatus — a plain, EXACT, by-ID
// lookup (V6-09B's own "Không làm: ... không suy operation mới nhất" line;
// that function's own doc comment: "it never infers 'the latest'
// operation").
//
// GetProjectionRebuildStatus takes no scope parameter of its own, so this
// handler confirms the loaded status actually belongs to {id} before ever
// returning it — mirrors
// internal/delivery/httpapi/releaseset/local_commit_queries.go's own
// handleGetReleaseSetLocalCommitStatus exactly: the by-ID-loaded record's
// own ProjectID field IS this route's authoritative reload/authorize step
// (Contract chung §3), there is no separate catalog.GetProject call to
// make on top of it. A cross-project operationId (one that genuinely
// exists, just under a different project) is folded into the identical
// leakage-normalized WriteResourceHidden response a truly nonexistent
// operationId gets.
func handleGetProjectionRebuildOperationStatus(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("id")
		operationID := r.PathValue("operationId")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "id", "is required")
			return
		}
		if strings.TrimSpace(operationID) == "" {
			writeValidationError(w, "operationId", "is required")
			return
		}

		status, err := appprojectionrebuild.GetProjectionRebuildStatus(r.Context(), deps.UnitOfWork, operationID)
		if err != nil {
			writeQueryError(w, err)
			return
		}
		if status.ProjectID != projectID {
			httpapi.WriteResourceHidden(w)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, newProjectionRebuildOperationStatusView(status), "")
	}
}
