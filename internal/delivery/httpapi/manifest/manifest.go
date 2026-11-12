// Package manifest serves the project's definition manifest to the UI (V12-21, ADR-056): whether the operator configured one for the
// project (aw serve --project-manifest) and runs of the kit's publisher over it — Check (nothing published) or Publish. The manifest
// path is the operator's, never a request's; the run goes through ports.ManifestPublisher.
package manifest

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/manifestpublish"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

const maxBodyBytes = 1 << 10

// RunRequest is POST /projects/{projectId}/definition-manifest/run's body.
type RunRequest struct {
	// Mode is CHECK (the kit's checks only) or PUBLISH.
	Mode string `json:"mode"`
}

// RegisterRoutes registers the two routes. publisher may be nil (the routes then answer that nothing is configured).
func RegisterRoutes(routes *httpapi.RouteRegistry, uow ports.UnitOfWork, publisher ports.ManifestPublisher) {
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{projectId}/definition-manifest", OperationID: "getDefinitionManifest",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: ports.ManifestStatus{},
		Handler: func(w http.ResponseWriter, r *http.Request) {
			projectID := strings.TrimSpace(r.PathValue("projectId"))
			if !projectExists(w, r, uow, projectID) {
				return
			}
			_ = httpapi.EncodeResult(w, http.StatusOK, manifestpublish.Status(publisher, projectID), "")
		},
	})
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/projects/{projectId}/definition-manifest/run", OperationID: "runDefinitionManifest",
		ScopeKind: httpapi.ScopeProject, RequestSchema: RunRequest{}, ResponseSchema: ports.ManifestRun{},
		Handler: runHandler(uow, publisher),
	})
}

func projectExists(w http.ResponseWriter, r *http.Request, uow ports.UnitOfWork, projectID string) bool {
	err := uow.WithReadOnly(r.Context(), func(tx ports.Tx) error {
		_, err := tx.Catalog().GetProject(r.Context(), projectID)
		return err
	})
	if err != nil {
		if errors.Is(err, ports.ErrPersistenceNotFound) {
			httpapi.WriteResourceHidden(w)
		} else {
			httpapi.WriteAppError(w, err)
		}
		return false
	}
	return true
}

func runHandler(uow ports.UnitOfWork, publisher ports.ManifestPublisher) http.HandlerFunc {
	const commandType = "RunDefinitionManifest"
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := strings.TrimSpace(r.PathValue("projectId"))
		idempotencyKey, err := httpapi.RequireIdempotencyKey(r)
		if err != nil {
			httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
			return
		}
		var body RunRequest
		canonical, err := httpapi.CanonicalizeJSON(r, maxBodyBytes, &body)
		if err != nil {
			httpapi.WriteDecodeError(w, err)
			return
		}
		if body.Mode != ports.ManifestCheck && body.Mode != ports.ManifestPublish {
			httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "mode must be CHECK or PUBLISH",
				[]httpapi.ErrorDetail{{Field: "mode", Message: "must be CHECK or PUBLISH"}})
			return
		}
		principal := httpapi.PrincipalFromContext(r.Context())
		scope := ports.ProjectScope(projectID)
		hash := httpapi.SemanticHash(commandType, scope, canonical, "", 0)
		cmd := ports.Command{
			ID: commandType + ":" + idempotencyKey, IdempotencyKey: idempotencyKey, Actor: principal.Actor, ActorRoles: principal.Roles,
			CorrelationID: commandType + ":" + idempotencyKey, Scope: scope, RequestedAt: time.Now().UTC(), Type: commandType, RequestHash: hash,
		}
		run, _, err := manifestpublish.RunCommand(r.Context(), uow, publisher, cmd, projectID, body.Mode)
		switch {
		case errors.Is(err, ports.ErrPersistenceNotFound):
			httpapi.WriteResourceHidden(w)
			return
		case errors.Is(err, ports.ErrReceiptConflict):
			httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, "Idempotency-Key was already used for a request with a different body", nil)
			return
		case errors.Is(err, ports.ErrManifestNotConfigured):
			httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, err.Error()+" (aw serve --project-manifest)", nil)
			return
		case errors.Is(err, ports.ErrManifestRunBusy):
			httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, err.Error(), nil)
			return
		case err != nil:
			httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.ErrorCodeUnavailable, err.Error(), nil)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, run, "")
	}
}
