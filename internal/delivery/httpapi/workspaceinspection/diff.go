package workspaceinspection

import (
	"net/http"

	appinspection "github.com/taQuangLing/agent-workflow/internal/app/workspaceinspection"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// workingTreeResult is the value of the result query parameter that selects the
// worktree's uncommitted state (V11-06) instead of a result commit.
const workingTreeResult = "WORKING_TREE"

// handleGetDiff implements GET
// /projects/{projectId}/repository-workspaces/{repositoryWorkspaceId}/diff
// (operationId getWorkspaceDiff): dispatches appinspection.Queries.GetDiff
// and nothing else, returning a JSON diffContentResponse (dto.go) — unlike
// getWorkspaceSource, the result here is inherently structured (a file
// summary alongside the raw patch bytes), so this route stays a plain JSON
// response through the shared httpapi.EncodeResult rather than a raw content
// stream.
func handleGetDiff(deps Dependencies) http.HandlerFunc {
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
		baseRevisionID, ok := requireQueryParam(w, query, "base")
		if !ok {
			return
		}
		baseGeneration, ok := requireUint64QueryParam(w, query, "baseGeneration")
		if !ok {
			return
		}
		resultRevisionID, ok := requireQueryParam(w, query, "result")
		if !ok {
			return
		}
		// V11-06: result=WORKING_TREE asks for the worktree's uncommitted state on
		// top of base instead of the difference between two commits; no
		// resultGeneration then. A 40/64-hex commit id can never equal the word.
		workingTree := resultRevisionID == workingTreeResult
		var resultGeneration uint64
		if !workingTree {
			resultGeneration, ok = requireUint64QueryParam(w, query, "resultGeneration")
			if !ok {
				return
			}
		}
		byteLimit, ok := optionalInt64QueryParam(w, query, "byteLimit")
		if !ok {
			return
		}
		fileLimit, ok := optionalIntQueryParam(w, query, "fileLimit")
		if !ok {
			return
		}

		if workingTree {
			uncommitted, err := deps.Queries.GetWorkingTreeDiff(ctx, appinspection.GetWorkingTreeDiffRequest{
				Scope: appinspection.WorkspaceScope{
					ProjectID: projectID, RepositoryID: repositoryID,
					WorkspaceSetID: workspaceSetID, RepositoryWorkspaceID: repositoryWorkspaceID,
				},
				BaseRevision: workspace.Revision{
					RepositoryID: project.RepositoryID(repositoryID), VCSObjectID: baseRevisionID, WorkspaceGeneration: baseGeneration,
				},
				ByteLimit: byteLimit, FileLimit: fileLimit,
			})
			if err != nil {
				writeQueryError(w, err)
				return
			}
			_ = httpapi.EncodeResult(w, http.StatusOK, toWorkingTreeDiffResponse(uncommitted), "")
			return
		}

		result, err := deps.Queries.GetDiff(ctx, appinspection.GetDiffRequest{
			Scope: appinspection.WorkspaceScope{
				ProjectID: projectID, RepositoryID: repositoryID,
				WorkspaceSetID: workspaceSetID, RepositoryWorkspaceID: repositoryWorkspaceID,
			},
			BaseRevision: workspace.Revision{
				RepositoryID: project.RepositoryID(repositoryID), VCSObjectID: baseRevisionID, WorkspaceGeneration: baseGeneration,
			},
			ResultRevision: workspace.Revision{
				RepositoryID: project.RepositoryID(repositoryID), VCSObjectID: resultRevisionID, WorkspaceGeneration: resultGeneration,
			},
			ByteLimit: byteLimit, FileLimit: fileLimit,
		})
		if err != nil {
			writeQueryError(w, err)
			return
		}

		_ = httpapi.EncodeResult(w, http.StatusOK, toDiffContentResponse(result), "")
	}
}
