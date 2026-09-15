package workspaceinspection

import (
	"net/http"

	appinspection "github.com/taQuangLing/agent-workflow/internal/app/workspaceinspection"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// handleGetRepositoryLog implements GET
// /projects/{projectId}/repository-workspaces/{repositoryWorkspaceId}/repository-log
// (operationId getWorkspaceRepositoryLog): dispatches
// appinspection.Queries.GetRepositoryLog and nothing else. cursor is passed
// through completely unmodified — never decoded, re-signed, or otherwise
// reasoned about by this package the way httpapi's own opaque
// CursorCodec-based pagination (cursor.go) works for other list routes: this
// cursor is already a real, meaningful, independently-adapter-revalidated
// domain value (a commit object id that must be req.Anchor itself or one of
// its own ancestors, checked via `git merge-base --is-ancestor` one layer
// below this package — see
// internal/app/ports/workspaceinspection.go's own ReadRepositoryLogRequest
// doc comment), not a generic offset/sort-key token this package would need
// to protect from caller tampering itself.
func handleGetRepositoryLog(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		projectID, ok := requirePathParam(w, r, "projectId")
		if !ok {
			return
		}
		repositoryWorkspaceID, ok := requirePathParam(w, r, "repositoryWorkspaceId")
		if !ok {
			return
		}

		query := r.URL.Query()
		repositoryID, ok := requireQueryParam(w, query, "repositoryId")
		if !ok {
			return
		}
		workspaceSetID, ok := requireQueryParam(w, query, "workspaceSetId")
		if !ok {
			return
		}
		anchorRevisionID, ok := requireQueryParam(w, query, "anchor")
		if !ok {
			return
		}
		anchorGeneration, ok := requireUint64QueryParam(w, query, "anchorGeneration")
		if !ok {
			return
		}
		cursor := query.Get("cursor")
		limit, ok := optionalIntQueryParam(w, query, "limit")
		if !ok {
			return
		}
		byteLimit, ok := optionalInt64QueryParam(w, query, "byteLimit")
		if !ok {
			return
		}

		result, err := deps.Queries.GetRepositoryLog(ctx, appinspection.GetRepositoryLogRequest{
			Scope: appinspection.WorkspaceScope{
				ProjectID: projectID, RepositoryID: repositoryID,
				WorkspaceSetID: workspaceSetID, RepositoryWorkspaceID: repositoryWorkspaceID,
			},
			Anchor: workspace.Revision{
				RepositoryID: project.RepositoryID(repositoryID), VCSObjectID: anchorRevisionID, WorkspaceGeneration: anchorGeneration,
			},
			Cursor: cursor, Limit: limit, ByteLimit: byteLimit,
		})
		if err != nil {
			writeQueryError(w, err)
			return
		}

		_ = httpapi.EncodeResult(w, http.StatusOK, toRepositoryLogPageResponse(result), "")
	}
}
