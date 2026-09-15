package evidence

import (
	"net/http"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	runtimeapp "github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// handleGetContextSnapshot implements
// GET /projects/{projectId}/work-items/{workItemId}/context-snapshots/{snapshotId}
// (operationId getContextSnapshot): the standalone, message-independent
// ContextSnapshot detail this task's own "ContextSnapshot detail" line
// names — reachable by snapshotId alone, unlike V6-07's own narrower
// getMessageContextSnapshot (message-scoped, resolves a Snapshot from a
// Message's own AttemptID). Both routes share ONE conversion
// (runtimeapp.ContextSnapshotToDetail — see that function's own doc comment
// for why: docs/design/11-v6-00-ux-artifact.md's own Screen 12 row 4,
// "authority dùng chung với Screen 11 hàng 5 cho chi tiết đầy đủ").
func handleGetContextSnapshot(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		projectID := r.PathValue("projectId")
		workItemID := r.PathValue("workItemId")
		snapshotID := r.PathValue("snapshotId")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "projectId", "is required")
			return
		}
		if strings.TrimSpace(workItemID) == "" {
			writeValidationError(w, "workItemId", "is required")
			return
		}
		if strings.TrimSpace(snapshotID) == "" {
			writeValidationError(w, "snapshotId", "is required")
			return
		}
		detail, err := runtimeapp.GetContextSnapshot(ctx, deps.UnitOfWork, ports.ProjectScope(projectID), workItemID, snapshotID)
		if err != nil {
			writeQueryError(w, err)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, detail, "")
	}
}
