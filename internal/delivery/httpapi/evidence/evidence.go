package evidence

import (
	"net/http"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	runtimeapp "github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// handleListEvidence implements
// GET /projects/{projectId}/work-items/{workItemId}/evidence (operationId
// listEvidence): every Evidence row workItemId has ever produced, reloading
// and scope-checking the WorkItem first (runtimeapp.ListEvidenceForWorkItem's
// own doc comment) — this task's own "Danh sách evidence theo
// WorkItem/Run/criterion" line. `runId`/`kind` are optional query
// parameters that narrow the result client-side (empty matches everything);
// `kind` names the same value Evidence.Kind carries (a MACHINE_GATE
// criterion's own EvidenceKey, or "COMMAND_EXECUTION" for a Command/Agent
// execution row) — the design doc's own "criterion" axis.
func handleListEvidence(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
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
		filter := runtimeapp.EvidenceFilter{
			RunID: strings.TrimSpace(r.URL.Query().Get("runId")),
			Kind:  strings.TrimSpace(r.URL.Query().Get("kind")),
		}
		items, err := runtimeapp.ListEvidenceForWorkItem(ctx, deps.UnitOfWork, ports.ProjectScope(projectID), workItemID, filter)
		if err != nil {
			writeQueryError(w, err)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, listEvidenceResponse{Items: items}, "")
	}
}

// handleGetEvidence implements
// GET /projects/{projectId}/work-items/{workItemId}/evidence/{evidenceId}
// (operationId getEvidence): one Evidence row's own bounded detail/verify
// metadata — this task's own "Chi tiết/verify metadata evidence (online)"
// line, distinct from the separate, offline `aw evidence verify` CLI_LOCAL
// leaf (docs/design/11-v6-00-ux-artifact.md's own Screen 11 row 2: "hai leaf
// khác nhau cho hai nhu cầu khác nhau, không phải trùng authority") — this
// route never itself re-verifies artifact bytes, it only ever returns the
// metadata verification needs.
func handleGetEvidence(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		projectID := r.PathValue("projectId")
		workItemID := r.PathValue("workItemId")
		evidenceID := r.PathValue("evidenceId")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "projectId", "is required")
			return
		}
		if strings.TrimSpace(workItemID) == "" {
			writeValidationError(w, "workItemId", "is required")
			return
		}
		if strings.TrimSpace(evidenceID) == "" {
			writeValidationError(w, "evidenceId", "is required")
			return
		}
		detail, err := runtimeapp.GetEvidence(ctx, deps.UnitOfWork, ports.ProjectScope(projectID), workItemID, evidenceID)
		if err != nil {
			writeQueryError(w, err)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, detail, "")
	}
}
