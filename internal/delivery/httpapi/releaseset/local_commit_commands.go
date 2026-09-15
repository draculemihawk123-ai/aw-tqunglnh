package releaseset

import (
	"net/http"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/releasesetcommit"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// requestReleaseSetLocalCommitBody is
// POST /projects/{projectId}/release-sets/{releaseSetId}/local-commits' own
// wire request shape — deliberately without ProjectID/ReleaseSetID (the
// path's own {projectId}/{releaseSetId}) or ReleaseSetLocalCommitID (a
// caller must never choose its own operation ID — internal/app/
// releasesetcommit.RequestReleaseSetLocalCommit always mints it itself, the
// same reasoning workitem's requestScopeExpansionBody already gives for
// omitting RequestID). ExpectedReleaseSetVersion/ExpectedWorkspaceVersion
// are the fences a stale caller trips (releasesetcommit.
// RequestReleaseSetLocalCommitRequest's own doc comment: "request pins
// ReleaseSet entry/version, workspace generation/fence") — travel as
// ordinary body fields, never as an HTTP If-Match precondition, since this
// route creates a brand-new resource (a ReleaseSetLocalCommit operation)
// with no prior version of its own to precondition against.
type requestReleaseSetLocalCommitBody struct {
	ExpectedReleaseSetVersion uint64 `json:"expectedReleaseSetVersion"`
	RepositoryWorkspaceID     string `json:"repositoryWorkspaceId"`
	ExpectedWorkspaceVersion  uint64 `json:"expectedWorkspaceVersion"`
	Message                   string `json:"message"`
	AuthorName                string `json:"authorName"`
	AuthorEmail               string `json:"authorEmail"`
}

// handleRequestReleaseSetLocalCommit implements
// POST /projects/{projectId}/release-sets/{releaseSetId}/local-commits
// (operationId requestReleaseSetLocalCommit): dispatches
// internal/app/releasesetcommit.RequestReleaseSetLocalCommit — the
// producer half of that package's own producer/consumer split (see its own
// doc comment). This handler NEVER calls the real Git adapter or the
// package's own worker (ExecuteReleaseSetLocalCommit, execute.go) — that
// happens later, entirely outside this request, inside a durable job a
// worker claims (V6-10F's own "Không làm": "handler must never call a real
// Git adapter or worker directly"). The response is therefore always an
// ACCEPTED-style 202 carrying the new operation's own ID/State/JobID/
// Marker, never a synchronous "done" result — a client polls/waits on it
// via GET .../local-commits/{localCommitId} (local_commit_queries.go).
//
// The ReleaseSet named by {releaseSetId} is reloaded via
// loadReleaseSetForUpdate FIRST (release_set_commands.go) — the same
// "reload the route's real target, scoped, before Idempotency-Key/body are
// even read" discipline every mutating handler in this package follows —
// even though this route has no If-Match of its own to check the reload
// against (ExpectedReleaseSetVersion is a body field the real command
// itself fences, not this handler).
func handleRequestReleaseSetLocalCommit(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("projectId")
		releaseSetID := r.PathValue("releaseSetId")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "projectId", "is required")
			return
		}
		if strings.TrimSpace(releaseSetID) == "" {
			writeValidationError(w, "releaseSetId", "is required")
			return
		}
		if _, ok := loadReleaseSetForUpdate(w, r, deps, projectID, releaseSetID); !ok {
			return
		}

		scope := ports.ProjectScope(projectID)
		var body requestReleaseSetLocalCommitBody
		cmd, ok := prepareCreateCommand(w, r, deps, "RequestReleaseSetLocalCommit", scope, &body)
		if !ok {
			return
		}
		if body.ExpectedReleaseSetVersion == 0 {
			writeValidationError(w, "expectedReleaseSetVersion", "is required and must be greater than zero")
			return
		}
		if strings.TrimSpace(body.RepositoryWorkspaceID) == "" {
			writeValidationError(w, "repositoryWorkspaceId", "is required")
			return
		}
		if body.ExpectedWorkspaceVersion == 0 {
			writeValidationError(w, "expectedWorkspaceVersion", "is required and must be greater than zero")
			return
		}
		if strings.TrimSpace(body.Message) == "" {
			writeValidationError(w, "message", "is required")
			return
		}
		if strings.TrimSpace(body.AuthorName) == "" {
			writeValidationError(w, "authorName", "is required")
			return
		}
		if strings.TrimSpace(body.AuthorEmail) == "" {
			writeValidationError(w, "authorEmail", "is required")
			return
		}

		if replayOrProceed(r.Context(), w, deps, cmd) {
			return
		}

		result, err := releasesetcommit.RequestReleaseSetLocalCommit(r.Context(), deps.UnitOfWork, deps.IDs, cmd, releasesetcommit.RequestReleaseSetLocalCommitRequest{
			ProjectID: projectID, ReleaseSetID: releaseSetID, ExpectedReleaseSetVersion: body.ExpectedReleaseSetVersion,
			RepositoryWorkspaceID: body.RepositoryWorkspaceID, ExpectedWorkspaceVersion: body.ExpectedWorkspaceVersion,
			Message: body.Message, AuthorName: body.AuthorName, AuthorEmail: body.AuthorEmail,
		})
		if err != nil {
			writeCommandError(w, err)
			return
		}
		// A freshly requested ReleaseSetLocalCommit always starts at Version 1
		// (workdomain.NewReleaseSetLocalCommit hardcodes it). 202 Accepted,
		// never 201/200: the real Git work has not happened yet, only been
		// durably requested — see this handler's own doc comment.
		_ = httpapi.EncodeResult(w, http.StatusAccepted, result, httpapi.ETagFromVersion(1))
	}
}
