// Package secrets serves the repository secret store (V12-29, ADR-061): list names, set values (one, or a .env file pasted once), delete.
// No route ever returns a value. The command hash covers the repository and the names, never the values, so a receipt holds nothing a
// value could be guessed back from.
package secrets

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/secretstore"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

const maxBodyBytes = 512 << 10

// SetRequest is POST .../secrets's body: values by name, and/or the text of a .env file (parsed on the server, kept nowhere).
type SetRequest struct {
	Values map[string]string `json:"values,omitempty"`
	DotEnv string            `json:"dotEnv,omitempty"`
}

// RegisterRoutes registers the three routes.
func RegisterRoutes(routes *httpapi.RouteRegistry, uow ports.UnitOfWork, keys ports.SecretKeyProvider) {
	deps := secretstore.Deps{UnitOfWork: uow, Keys: keys}
	base := "/projects/{projectId}/repositories/{repositoryId}/secrets"
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: base, OperationID: "listRepositorySecrets", ScopeKind: httpapi.ScopeProject,
		RequestSchema: struct{}{}, ResponseSchema: secretstore.SecretList{},
		Handler: func(w http.ResponseWriter, r *http.Request) {
			list, err := secretstore.List(r.Context(), deps, r.PathValue("projectId"), r.PathValue("repositoryId"))
			if err != nil {
				writeError(w, err)
				return
			}
			_ = httpapi.EncodeResult(w, http.StatusOK, list, "")
		},
	})
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: base, OperationID: "setRepositorySecrets", ScopeKind: httpapi.ScopeProject,
		RequestSchema: SetRequest{}, ResponseSchema: secretstore.SetResult{},
		Handler: func(w http.ResponseWriter, r *http.Request) {
			var body SetRequest
			if _, err := httpapi.CanonicalizeJSON(r, maxBodyBytes, &body); err != nil {
				httpapi.WriteDecodeError(w, err)
				return
			}
			values := map[string]string{}
			if strings.TrimSpace(body.DotEnv) != "" {
				parsed, err := secretstore.ParseDotEnv(body.DotEnv)
				if err != nil {
					httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), []httpapi.ErrorDetail{{Field: "dotEnv", Message: err.Error()}})
					return
				}
				for k, v := range parsed {
					values[k] = v
				}
			}
			for k, v := range body.Values {
				values[k] = v
			}
			names := make([]string, 0, len(values))
			for k := range values {
				names = append(names, k)
			}
			sort.Strings(names)
			cmd, ok := command(w, r, "SetRepositorySecrets", names)
			if !ok {
				return
			}
			result, err := secretstore.Set(r.Context(), deps, cmd, r.PathValue("projectId"), r.PathValue("repositoryId"), values)
			if err != nil {
				writeError(w, err)
				return
			}
			_ = httpapi.EncodeResult(w, http.StatusOK, result, "")
		},
	})
	routes.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: base + "/{name}/delete", OperationID: "deleteRepositorySecret", ScopeKind: httpapi.ScopeProject,
		RequestSchema: struct{}{}, ResponseSchema: secretstore.SetResult{},
		Handler: func(w http.ResponseWriter, r *http.Request) {
			var body struct{}
			if _, err := httpapi.CanonicalizeJSON(r, 1<<10, &body); err != nil {
				httpapi.WriteDecodeError(w, err)
				return
			}
			name := r.PathValue("name")
			cmd, ok := command(w, r, "DeleteRepositorySecret", []string{name})
			if !ok {
				return
			}
			result, err := secretstore.Delete(r.Context(), deps, cmd, r.PathValue("projectId"), r.PathValue("repositoryId"), name)
			if err != nil {
				writeError(w, err)
				return
			}
			_ = httpapi.EncodeResult(w, http.StatusOK, result, "")
		},
	})
}

// command builds the command; its hash covers the repository and the names only.
func command(w http.ResponseWriter, r *http.Request, commandType string, names []string) (ports.Command, bool) {
	key, err := httpapi.RequireIdempotencyKey(r)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
		return ports.Command{}, false
	}
	projectID := strings.TrimSpace(r.PathValue("projectId"))
	scope := ports.ProjectScope(projectID)
	hashed, _ := json.Marshal(struct {
		RepositoryID string   `json:"repositoryId"`
		Names        []string `json:"names"`
	}{r.PathValue("repositoryId"), names})
	principal := httpapi.PrincipalFromContext(r.Context())
	return ports.Command{
		ID: commandType + ":" + key, IdempotencyKey: key, Actor: principal.Actor, ActorRoles: principal.Roles,
		CorrelationID: commandType + ":" + key, Scope: scope, RequestedAt: time.Now().UTC(), Type: commandType,
		RequestHash: httpapi.SemanticHash(commandType, scope, hashed, "", 0),
	}, true
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ports.ErrPersistenceNotFound):
		httpapi.WriteResourceHidden(w)
	case errors.Is(err, secretstore.ErrNoSecret):
		httpapi.WriteError(w, http.StatusNotFound, httpapi.ErrorCodeNotFound, err.Error(), nil)
	case errors.Is(err, secretstore.ErrInvalidName), errors.Is(err, secretstore.ErrInvalidValue):
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
	case errors.Is(err, ports.ErrReceiptConflict):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, "Idempotency-Key was already used for a request with different names", nil)
	case errors.Is(err, ports.ErrSecretKeyUnavailable):
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.ErrorCodeUnavailable, err.Error(), nil)
	default:
		httpapi.WriteAppError(w, err)
	}
}
