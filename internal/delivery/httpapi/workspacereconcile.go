package httpapi

import (
	"net/http"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workspacereconcile"
)

// reconcileRequestBody mirrors releaseRequestBody's own reasoning exactly
// (workspacerelease.go): always empty, RepositoryWorkspaceID/ProjectID come
// from the URL path, ExpectedVersion comes from the required If-Match
// header.
type reconcileRequestBody struct{}

// requestWorkspaceReconciliationHandler is POST
// /projects/{projectId}/repository-workspaces/{repositoryWorkspaceId}/reconcile
// — see workspaceroutes.go's own doc comment for the full flow this
// follows. Unlike release, reconcile needs no
// ports.ReleaseEligibilityAuthority: workspacereconcile.RequestWorkspaceReconciliation
// depends on nothing beyond ports.UnitOfWork/idsource.Source.
func requestWorkspaceReconciliationHandler(uow ports.UnitOfWork, ids idsource.Source) http.HandlerFunc {
	const commandType = "RequestWorkspaceReconciliation"
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

		var body reconcileRequestBody
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

		cmd := newWorkspaceCommand(commandType, idempotencyKey, principal, scope, expectedVersion, hash)
		result, err := workspacereconcile.RequestWorkspaceReconciliation(r.Context(), uow, ids, cmd, workspacereconcile.RequestWorkspaceReconciliationRequest{
			RepositoryWorkspaceID: repositoryWorkspaceID, ProjectID: projectID,
		})
		if err != nil {
			writeWorkspaceCommandError(w, err)
			return
		}
		// RequestWorkspaceReconciliationResult carries no Version field either
		// (see requestWorkspaceSetReleaseHandler's own identical note) — "" skips
		// the ETag header rather than fabricating one.
		_ = EncodeResult(w, http.StatusOK, result, "")
	}
}
