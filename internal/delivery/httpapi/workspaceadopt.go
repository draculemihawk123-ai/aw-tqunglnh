package httpapi

import (
	"net/http"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/workspacereconcile"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
)

// adoptHeadRequestBody mirrors reconcileRequestBody's own reasoning exactly:
// always empty — RepositoryWorkspaceID/ProjectID come from the URL path and
// ExpectedVersion from the required If-Match header.
type adoptHeadRequestBody struct{}

// adoptWorkspaceHeadHandler is POST
// /projects/{projectId}/repository-workspaces/{repositoryWorkspaceId}/adopt-head
// (V11-01): record commits made in the worktree outside aw as the workspace's
// current revision — see workspacereconcile.AdoptWorkspaceHead for the full
// contract. It follows V6-02's mutating-route flow like every handler in
// workspaceroutes.go; the Git reads happen inside the application command
// through the ports.WorkspaceHeadAuditor it is handed (never in this package).
// auditor is nil only in a composition that has no Git workspace provider; the
// route then answers UNAVAILABLE rather than pretending to adopt.
func adoptWorkspaceHeadHandler(uow ports.UnitOfWork, auditor ports.WorkspaceHeadAuditor, matcher redact.Matcher) http.HandlerFunc {
	const commandType = "AdoptWorkspaceHead"
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := strings.TrimSpace(r.PathValue("projectId"))
		repositoryWorkspaceID := strings.TrimSpace(r.PathValue("repositoryWorkspaceId"))
		if projectID == "" || repositoryWorkspaceID == "" {
			WriteError(w, http.StatusBadRequest, ErrorCodeInvalidRequest, "projectId and repositoryWorkspaceId path segments are required", nil)
			return
		}

		idempotencyKey, err := RequireIdempotencyKey(r)
		if err != nil {
			WriteError(w, http.StatusBadRequest, ErrorCodeInvalidRequest, err.Error(), nil)
			return
		}
		ifMatch, err := RequireIfMatch(r)
		if err != nil {
			WriteError(w, http.StatusBadRequest, ErrorCodeInvalidRequest, err.Error(), nil)
			return
		}
		expectedVersion, err := VersionFromETag(ifMatch)
		if err != nil || expectedVersion == 0 {
			WriteError(w, http.StatusBadRequest, ErrorCodeInvalidRequest,
				"If-Match must be a strong ETag naming the repository workspace's current version (GET the resource first)", nil)
			return
		}

		var body adoptHeadRequestBody
		canonical, err := CanonicalizeJSON(r, maxWorkspaceCommandBodyBytes, &body)
		if err != nil {
			WriteDecodeError(w, err)
			return
		}

		principal := PrincipalFromContext(r.Context())
		scope := ports.ProjectScope(projectID)
		hash := SemanticHash(commandType, scope, canonical, "", expectedVersion)

		receipt, found, err := LookupReceipt(r.Context(), uow, principal.Actor, scope, idempotencyKey, commandType)
		if err != nil {
			WriteAppError(w, err)
			return
		}
		if found {
			if err := ReconcileReceipt(receipt, hash); err != nil {
				WriteError(w, http.StatusConflict, ErrorCodeConflict, "Idempotency-Key was already used for a request with a different body", nil)
				return
			}
			WriteReceiptReplay(w, receipt)
			return
		}

		if auditor == nil {
			WriteAppError(w, apperror.New(errorcode.CodeUnavailable, "this server has no Git workspace provider, so it cannot adopt a worktree HEAD", false))
			return
		}

		cmd := newWorkspaceCommand(commandType, idempotencyKey, principal, scope, expectedVersion, hash)
		result, err := workspacereconcile.AdoptWorkspaceHead(r.Context(),
			workspacereconcile.AdoptWorkspaceHeadDeps{UnitOfWork: uow, Auditor: auditor, Matcher: matcher}, cmd,
			workspacereconcile.AdoptWorkspaceHeadRequest{RepositoryWorkspaceID: repositoryWorkspaceID, ProjectID: projectID})
		if err != nil {
			writeWorkspaceCommandError(w, err)
			return
		}
		// The result carries the workspace's new version, so the ETag is real.
		_ = EncodeResult(w, http.StatusOK, result, ETagFromVersion(result.Version))
	}
}
