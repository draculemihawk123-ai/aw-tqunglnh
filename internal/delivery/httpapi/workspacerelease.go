package httpapi

import (
	"net/http"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workspacerelease"
)

// releaseRequestBody is POST .../release's own request body shape: always
// empty. FamilyID/ProjectID come from the URL path (the resource this route
// already names) and ExpectedVersion comes from the required If-Match
// header (commandenvelope.go's own VersionFromETag) — nothing about this
// mutation's own target or fence is caller-suppliable in the body, so the
// body exists only so CanonicalizeJSON/SemanticHash have a well-defined
// (empty) payload to canonicalize/hash, exactly like every other command
// envelope in this codebase.
type releaseRequestBody struct{}

// requestWorkspaceSetReleaseHandler is POST
// /projects/{projectId}/workspace-sets/{familyId}/release — see
// workspaceroutes.go's own doc comment for the full flow this follows.
// authority is work.NewEligibilityAuthority, constructed once by
// RegisterWorkspaceRoutes and threaded through here; this handler never
// constructs its own.
func requestWorkspaceSetReleaseHandler(uow ports.UnitOfWork, ids idsource.Source, authority ports.ReleaseEligibilityAuthority) http.HandlerFunc {
	const commandType = "RequestWorkspaceSetRelease"
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := strings.TrimSpace(r.PathValue("projectId"))
		familyID := strings.TrimSpace(r.PathValue("familyId"))
		if projectID == "" || familyID == "" {
			WriteError(w, http.StatusBadRequest, ErrorCodeInvalidRequest, "projectId and familyId path segments are required", nil)
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
				"If-Match must be a strong ETag naming the workspace set's current version (GET the resource first)", nil)
			return
		}

		var body releaseRequestBody
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
		result, err := workspacerelease.RequestWorkspaceSetRelease(r.Context(), uow, ids, authority, cmd, workspacerelease.RequestWorkspaceSetReleaseRequest{
			FamilyID: familyID, ProjectID: projectID,
		})
		if err != nil {
			writeWorkspaceCommandError(w, err)
			return
		}
		// result carries no version of its own (RequestWorkspaceSetReleaseResult
		// has no Version field — the release-request intent moves the
		// WorkspaceSet's own state but this command's own result type does not
		// echo the resulting version), so there is no fresh value to encode as
		// this response's own ETag; EncodeResult's etag parameter accepts ""
		// for exactly this case rather than this handler fabricating one.
		_ = EncodeResult(w, http.StatusOK, result, "")
	}
}
