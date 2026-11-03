// Package runapp serves V12-30 (ADR-062): a repository's Run app profile, and starting, stopping and watching its application in a
// repository workspace. `aw worker` runs it; these routes only record the request and read the state and the log tails.
package runapp

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	apprunapp "github.com/taQuangLing/agent-workflow/internal/app/runapp"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

const maxBodyBytes = 64 << 10

// SetProfileRequest is POST .../run-app's body.
type SetProfileRequest struct {
	Profile apprunapp.Profile `json:"profile"`
	// ExpectedVersion is the profile version read (0 for the first one).
	ExpectedVersion uint64 `json:"expectedVersion"`
}

// RegisterRoutes registers the five routes. host reads the log tails (nil: no log).
func RegisterRoutes(routes *httpapi.RouteRegistry, uow ports.UnitOfWork, host ports.RunAppHost) {
	deps := apprunapp.Deps{UnitOfWork: uow, Host: host}
	profile := "/projects/{projectId}/repositories/{repositoryId}/run-app"
	session := "/projects/{projectId}/repository-workspaces/{repositoryWorkspaceId}/run-app"
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: profile, OperationID: "getRunAppProfile", ScopeKind: httpapi.ScopeProject,
		RequestSchema: struct{}{}, ResponseSchema: apprunapp.ProfileView{},
		Handler: func(w http.ResponseWriter, r *http.Request) {
			view, err := apprunapp.GetProfile(r.Context(), deps, r.PathValue("projectId"), r.PathValue("repositoryId"))
			if err != nil {
				writeError(w, err)
				return
			}
			_ = httpapi.EncodeResult(w, http.StatusOK, view, "")
		},
	})
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: profile, OperationID: "setRunAppProfile", ScopeKind: httpapi.ScopeProject,
		RequestSchema: SetProfileRequest{}, ResponseSchema: apprunapp.ProfileView{},
		Handler: func(w http.ResponseWriter, r *http.Request) {
			var body SetProfileRequest
			cmd, ok := command(w, r, "SetRunAppProfile", r.PathValue("repositoryId"), &body)
			if !ok {
				return
			}
			view, err := apprunapp.SetProfile(r.Context(), deps, cmd, r.PathValue("projectId"), r.PathValue("repositoryId"), body.Profile, body.ExpectedVersion)
			if err != nil {
				writeError(w, err)
				return
			}
			_ = httpapi.EncodeResult(w, http.StatusOK, view, "")
		},
	})
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: session, OperationID: "getRunAppStatus", ScopeKind: httpapi.ScopeProject,
		RequestSchema: struct{}{}, ResponseSchema: apprunapp.Status{},
		Handler: func(w http.ResponseWriter, r *http.Request) {
			st, err := apprunapp.GetStatus(r.Context(), deps, r.PathValue("projectId"), r.PathValue("repositoryWorkspaceId"))
			if err != nil {
				writeError(w, err)
				return
			}
			_ = httpapi.EncodeResult(w, http.StatusOK, st, "")
		},
	})
	for _, op := range []struct {
		path, id, commandType string
		run                   func(r *http.Request, cmd ports.Command) (apprunapp.SessionView, error)
	}{
		{"/start", "startRunApp", "StartRunApp", func(r *http.Request, cmd ports.Command) (apprunapp.SessionView, error) {
			return apprunapp.Start(r.Context(), deps, cmd, r.PathValue("projectId"), r.PathValue("repositoryWorkspaceId"))
		}},
		{"/stop", "stopRunApp", "StopRunApp", func(r *http.Request, cmd ports.Command) (apprunapp.SessionView, error) {
			return apprunapp.Stop(r.Context(), deps, cmd, r.PathValue("projectId"), r.PathValue("repositoryWorkspaceId"))
		}},
	} {
		op := op
		routes.Register(httpapi.RouteDescriptor{
			Method: http.MethodPost, Path: session + op.path, OperationID: op.id, ScopeKind: httpapi.ScopeProject,
			RequestSchema: struct{}{}, ResponseSchema: apprunapp.SessionView{},
			Handler: func(w http.ResponseWriter, r *http.Request) {
				var body struct{}
				cmd, ok := command(w, r, op.commandType, r.PathValue("repositoryWorkspaceId"), &body)
				if !ok {
					return
				}
				view, err := op.run(r, cmd)
				if err != nil {
					writeError(w, err)
					return
				}
				_ = httpapi.EncodeResult(w, http.StatusOK, view, "")
			},
		})
	}
}

// command decodes the body and builds the command (its hash covers the canonical body and the target).
func command(w http.ResponseWriter, r *http.Request, commandType, target string, body any) (ports.Command, bool) {
	key, err := httpapi.RequireIdempotencyKey(r)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
		return ports.Command{}, false
	}
	canonical, err := httpapi.CanonicalizeJSON(r, maxBodyBytes, body)
	if err != nil {
		httpapi.WriteDecodeError(w, err)
		return ports.Command{}, false
	}
	projectID := strings.TrimSpace(r.PathValue("projectId"))
	scope := ports.ProjectScope(projectID)
	principal := httpapi.PrincipalFromContext(r.Context())
	return ports.Command{
		ID: commandType + ":" + key, IdempotencyKey: key, Actor: principal.Actor, ActorRoles: principal.Roles,
		CorrelationID: commandType + ":" + key, Scope: scope, RequestedAt: time.Now().UTC(), Type: commandType,
		RequestHash: httpapi.SemanticHash(commandType, scope, canonical, target, 0),
	}, true
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ports.ErrPersistenceNotFound):
		httpapi.WriteResourceHidden(w)
	case errors.Is(err, apprunapp.ErrInvalidProfile):
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), []httpapi.ErrorDetail{{Field: "profile", Message: err.Error()}})
	case errors.Is(err, apprunapp.ErrNoProfile), errors.Is(err, apprunapp.ErrNotReady), errors.Is(err, apprunapp.ErrWriterActive),
		errors.Is(err, apprunapp.ErrAlreadyActive), errors.Is(err, apprunapp.ErrNotActive), errors.Is(err, ports.ErrOptimisticConflict):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, err.Error(), nil)
	case errors.Is(err, ports.ErrReceiptConflict):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, "Idempotency-Key was already used for a request with a different body", nil)
	default:
		httpapi.WriteAppError(w, err)
	}
}
