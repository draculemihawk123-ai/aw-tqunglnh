package projectionrebuild

import (
	"net/http"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	appprojectionrebuild "github.com/taQuangLing/agent-workflow/internal/app/projectionrebuild"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// maxBodyBytes bounds the one POST body this package ever decodes — a
// single short string field, the same reasoning every sibling endpoint
// package's own identical constant documents (e.g.
// internal/delivery/httpapi/adapterbuild/commands.go).
const maxBodyBytes = 1 << 16

// requestProjectionRebuildBody is POST /projects/{id}/projection/rebuild's
// own wire request shape — deliberately without ProjectID (the path's own
// {id}) or OperationID (a caller must never choose its own operation ID;
// internal/app/projectionrebuild.RequestProjectionRebuild always mints it
// itself, the same reasoning every other create-shaped command's own body
// DTO in this codebase already follows).
type requestProjectionRebuildBody struct {
	ProjectionName string `json:"projectionName"`
}

// projectionRebuildResultView is POST /projects/{id}/projection/rebuild's
// own wire response shape — an isolated HTTP-layer DTO (V6-09B's own
// "Verify: isolated schemas" line) mirroring
// appprojectionrebuild.RequestProjectionRebuildResult field for field
// rather than returning that app-layer type on the wire directly, so this
// package's own response shape can never silently drift just because the
// app-layer result type happens to grow a field for some other caller
// (e.g. a future CLI use) in the future.
type projectionRebuildResultView struct {
	OperationID    string `json:"operationId"`
	ProjectID      string `json:"projectId"`
	ProjectionName string `json:"projectionName"`
	Phase          string `json:"phase"`
	JobID          string `json:"jobId"`
}

func newProjectionRebuildResultView(result appprojectionrebuild.RequestProjectionRebuildResult) projectionRebuildResultView {
	return projectionRebuildResultView{
		OperationID: result.OperationID, ProjectID: result.ProjectID,
		ProjectionName: result.ProjectionName, Phase: result.Phase, JobID: result.JobID,
	}
}

// handleRequestProjectionRebuild implements
// POST /projects/{id}/projection/rebuild (operationId
// requestProjectionRebuild): dispatches
// appprojectionrebuild.RequestProjectionRebuild — see this package's own
// top-of-file doc comment for why this handler never calls
// httpapi.LookupReceipt/ReconcileReceipt before dispatch (mirrors
// internal/delivery/httpapi/adapterbuild's own identical choice, for the
// identical reason: the real command already owns its own receipt
// lookup/replay internally).
//
// The Project named by {id} is reloaded via catalog.GetProject FIRST,
// unconditionally (before Idempotency-Key/body are even read) — the same
// "reload the route's real target before anything else" discipline every
// mutating handler in this codebase's HTTP layer follows.
//
// Response is always 202 Accepted — the real rebuild work has not
// happened yet, only been durably requested (V6-09B's own "POST trả
// accepted OperationID" line); a caller polls/waits on it via
// GET .../rebuild-operations/{operationId} (operation_status.go).
func handleRequestProjectionRebuild(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("id")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "id", "is required")
			return
		}
		scope := ports.ProjectScope(projectID)
		if _, err := catalog.GetProject(r.Context(), deps.UnitOfWork, scope, projectID); err != nil {
			writeQueryError(w, err)
			return
		}

		var body requestProjectionRebuildBody
		cmd, ok := prepareCommand(w, r, deps, commandTypeRequestProjectionRebuild, scope, &body)
		if !ok {
			return
		}
		if strings.TrimSpace(body.ProjectionName) == "" {
			writeValidationError(w, "projectionName", "is required")
			return
		}

		result, err := appprojectionrebuild.RequestProjectionRebuild(r.Context(), deps.UnitOfWork, deps.IDs, cmd, appprojectionrebuild.RequestProjectionRebuildRequest{
			ProjectID: projectID, ProjectionName: body.ProjectionName,
		})
		if err != nil {
			writeCommandError(w, err)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusAccepted, newProjectionRebuildResultView(result), "")
	}
}

// prepareCommand is this package's own shared preamble for its one
// mutating route (requestProjectionRebuild): require Idempotency-Key,
// strictly decode+canonicalize the body into dst, and build the resulting
// ports.Command — mirrors internal/delivery/httpapi/adapterbuild's own
// prepareCommand exactly (ExpectedVersion always 0: RequestProjectionRebuild
// is create-shaped, it preconditions on no prior version of its own
// target). scope must already be the AUTHORITATIVE scope this package's
// own caller derived by reloading the Project first (see
// handleRequestProjectionRebuild's own doc comment) — never built from the
// path alone.
func prepareCommand(w http.ResponseWriter, r *http.Request, deps Dependencies, commandType string, scope ports.CommandScope, dst any) (cmd ports.Command, ok bool) {
	idempotencyKey, err := httpapi.RequireIdempotencyKey(r)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
		return ports.Command{}, false
	}
	canonical, err := httpapi.CanonicalizeJSON(r, maxBodyBytes, dst)
	if err != nil {
		httpapi.WriteDecodeError(w, err)
		return ports.Command{}, false
	}
	hash := httpapi.SemanticHash(commandType, scope, canonical, "", 0)
	principal := httpapi.PrincipalFromContext(r.Context())
	cmd = ports.Command{
		ID: commandType + "-" + idempotencyKey, IdempotencyKey: idempotencyKey, Actor: principal.Actor,
		ActorRoles: principal.Roles, CorrelationID: httpapi.CorrelationIDFromContext(r.Context()),
		Scope: scope, RequestedAt: deps.Clock.Now(), Type: commandType, RequestHash: hash,
	}
	return cmd, true
}
