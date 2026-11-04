// Package awdocuments serves V12-33 (ADR-065): a worktree's aw-private task documents, and exporting them into the repository.
package awdocuments

import (
	"errors"
	"net/http"
	"strings"
	"time"

	appawdocs "github.com/taQuangLing/agent-workflow/internal/app/awdocuments"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// ExportRequest is POST .../aw-documents/export's body.
type ExportRequest struct {
	// Overwrite replaces documents that already exist in docs/features/ with other content.
	Overwrite bool `json:"overwrite,omitempty"`
}

// RegisterRoutes registers the two routes. store is the Git workspace provider (nil: they answer UNAVAILABLE).
func RegisterRoutes(routes *httpapi.RouteRegistry, uow ports.UnitOfWork, store ports.AwDocumentStore) {
	deps := appawdocs.Deps{UnitOfWork: uow, Store: store}
	base := "/projects/{projectId}/repository-workspaces/{repositoryWorkspaceId}/aw-documents"
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: base, OperationID: "listAwDocuments", ScopeKind: httpapi.ScopeProject,
		RequestSchema: struct{}{}, ResponseSchema: appawdocs.List{},
		Handler: func(w http.ResponseWriter, r *http.Request) {
			list, err := appawdocs.ListDocuments(r.Context(), deps, r.PathValue("projectId"), r.PathValue("repositoryWorkspaceId"))
			if err != nil {
				writeError(w, err)
				return
			}
			_ = httpapi.EncodeResult(w, http.StatusOK, list, "")
		},
	})
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: base + "/export", OperationID: "exportAwDocuments", ScopeKind: httpapi.ScopeProject,
		RequestSchema: ExportRequest{}, ResponseSchema: appawdocs.ExportResult{},
		Handler: func(w http.ResponseWriter, r *http.Request) {
			key, err := httpapi.RequireIdempotencyKey(r)
			if err != nil {
				httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
				return
			}
			var body ExportRequest
			canonical, err := httpapi.CanonicalizeJSON(r, 1<<10, &body)
			if err != nil {
				httpapi.WriteDecodeError(w, err)
				return
			}
			projectID := strings.TrimSpace(r.PathValue("projectId"))
			scope := ports.ProjectScope(projectID)
			principal := httpapi.PrincipalFromContext(r.Context())
			const commandType = "ExportAwDocuments"
			cmd := ports.Command{
				ID: commandType + ":" + key, IdempotencyKey: key, Actor: principal.Actor, ActorRoles: principal.Roles,
				CorrelationID: commandType + ":" + key, Scope: scope, RequestedAt: time.Now().UTC(), Type: commandType,
				RequestHash: httpapi.SemanticHash(commandType, scope, canonical, r.PathValue("repositoryWorkspaceId"), 0),
			}
			result, err := appawdocs.Export(r.Context(), deps, cmd, projectID, r.PathValue("repositoryWorkspaceId"), body.Overwrite)
			if errors.Is(err, appawdocs.ErrConflicts) {
				details := make([]httpapi.ErrorDetail, 0, len(result.Conflicts))
				for _, c := range result.Conflicts {
					details = append(details, httpapi.ErrorDetail{Field: c, Message: "exists in the repository with other content"})
				}
				httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, err.Error(), details)
				return
			}
			if err != nil {
				writeError(w, err)
				return
			}
			_ = httpapi.EncodeResult(w, http.StatusOK, result, "")
		},
	})
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ports.ErrPersistenceNotFound):
		httpapi.WriteResourceHidden(w)
	case errors.Is(err, appawdocs.ErrNotReady), errors.Is(err, appawdocs.ErrWriterActive):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, err.Error(), nil)
	case errors.Is(err, appawdocs.ErrUnavailable):
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.ErrorCodeUnavailable, err.Error(), nil)
	case errors.Is(err, ports.ErrReceiptConflict):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, "Idempotency-Key was already used for a request with a different body", nil)
	default:
		httpapi.WriteAppError(w, err)
	}
}
