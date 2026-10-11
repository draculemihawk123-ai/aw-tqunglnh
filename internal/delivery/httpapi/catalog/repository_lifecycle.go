package catalog

import (
	"errors"
	"net/http"
	"strings"

	appcatalog "github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// V11-12 (ADR-043): correct, retire and remove a repository, and check a path before registering it.

// updateRepositoryRequestBody is `PATCH /repositories/{id}`'s body. An absent field keeps the current value, so a client that fixes only the path
// sends only the path.
type updateRepositoryRequestBody struct {
	Name          *string `json:"name"`
	RemoteLocator *string `json:"remoteLocator"`
	DefaultRef    *string `json:"defaultRef"`
}

// mutationTarget loads the repository and the If-Match version a repository mutation needs, writing the error itself when it cannot.
func (h *handler) mutationTarget(w http.ResponseWriter, r *http.Request) (repositoryID, projectID string, expectedVersion uint64, ok bool) {
	repositoryID = r.PathValue("id")
	repo, err := appcatalog.GetRepository(r.Context(), h.uow, repositoryID)
	if err != nil {
		writeCatalogError(w, err)
		return "", "", 0, false
	}
	ifMatch, err := httpapi.RequireIfMatch(r)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
		return "", "", 0, false
	}
	expectedVersion, err = httpapi.VersionFromETag(ifMatch)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
		return "", "", 0, false
	}
	return repositoryID, string(repo.ProjectID), expectedVersion, true
}

// updateRepository handles `PATCH /repositories/{id}`.
func (h *handler) updateRepository(w http.ResponseWriter, r *http.Request) {
	repositoryID, projectID, expectedVersion, ok := h.mutationTarget(w, r)
	if !ok {
		return
	}
	var body updateRepositoryRequestBody
	cmd, ok := h.beginMutation(w, r, ports.ProjectScope(projectID), "UpdateRepository", expectedVersion, &body)
	if !ok {
		return
	}
	repo, err := appcatalog.GetRepository(r.Context(), h.uow, repositoryID)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	req := appcatalog.UpdateRepositoryRequest{
		RepositoryID: repositoryID, ProjectID: projectID, RemoteLocator: repo.RemoteLocator, DefaultRef: repo.DefaultRef,
	}
	if body.Name != nil {
		req.Name = *body.Name
	}
	if body.RemoteLocator != nil {
		req.RemoteLocator = *body.RemoteLocator
	}
	if body.DefaultRef != nil {
		req.DefaultRef = *body.DefaultRef
	}
	result, err := appcatalog.UpdateRepository(r.Context(), h.uow, h.ids, cmd, req)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	_ = httpapi.EncodeResult(w, http.StatusAccepted, result, httpapi.ETagFromVersion(result.Version))
}

// reasonRequestBody is the body of disable and delete.
type reasonRequestBody struct {
	Reason string `json:"reason"`
}

// disableRepository handles `POST /repositories/{id}/disable`.
func (h *handler) disableRepository(w http.ResponseWriter, r *http.Request) {
	repositoryID, projectID, expectedVersion, ok := h.mutationTarget(w, r)
	if !ok {
		return
	}
	var body reasonRequestBody
	cmd, ok := h.beginMutation(w, r, ports.ProjectScope(projectID), "DisableRepository", expectedVersion, &body)
	if !ok {
		return
	}
	result, err := appcatalog.DisableRepository(r.Context(), h.uow, cmd, appcatalog.DisableRepositoryRequest{
		RepositoryID: repositoryID, ProjectID: projectID, Reason: strings.TrimSpace(body.Reason),
	})
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	_ = httpapi.EncodeResult(w, http.StatusOK, result, httpapi.ETagFromVersion(result.Version))
}

// deleteRepository handles `DELETE /repositories/{id}`. The reason travels in the body: a permanent removal is recorded with why.
func (h *handler) deleteRepository(w http.ResponseWriter, r *http.Request) {
	repositoryID, projectID, expectedVersion, ok := h.mutationTarget(w, r)
	if !ok {
		return
	}
	var body reasonRequestBody
	cmd, ok := h.beginMutation(w, r, ports.ProjectScope(projectID), "DeleteRepository", expectedVersion, &body)
	if !ok {
		return
	}
	result, err := appcatalog.DeleteRepository(r.Context(), h.uow, cmd, appcatalog.DeleteRepositoryRequest{
		RepositoryID: repositoryID, ProjectID: projectID, Reason: strings.TrimSpace(body.Reason),
	})
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	_ = httpapi.EncodeResult(w, http.StatusOK, result, "")
}

// checkRepository handles `GET /projects/{id}/repositories/check?path=&ref=`: the same probe a registration runs, writing nothing, so a wrong path is
// caught before it is registered.
func (h *handler) checkRepository(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if _, err := appcatalog.GetProject(r.Context(), h.uow, ports.ProjectScope(projectID), projectID); err != nil {
		writeCatalogError(w, err)
		return
	}
	path := r.URL.Query().Get("path")
	if strings.TrimSpace(path) == "" {
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "query parameter path is required",
			[]httpapi.ErrorDetail{{Field: "path", Message: "required"}})
		return
	}
	if h.prober == nil {
		httpapi.WriteError(w, http.StatusServiceUnavailable, httpapi.ErrorCodeUnavailable, "no repository prober is configured on this server (is Git installed?)", nil)
		return
	}
	result, err := appcatalog.CheckRepository(r.Context(), h.prober, path, r.URL.Query().Get("ref"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	_ = httpapi.EncodeResult(w, http.StatusOK, result, "")
}

// writeRepositoryRefusal answers a typed refusal (nothing changed) with 409 and the code and items in the details. It reports whether err was one.
func writeRepositoryRefusal(w http.ResponseWriter, err error) bool {
	code := appcatalog.RepositoryRefusalCode(err)
	if code == "" {
		return false
	}
	details := []httpapi.ErrorDetail{{Field: "repository", Message: code}}
	for _, item := range appcatalog.RepositoryRefusalItems(err) {
		details = append(details, httpapi.ErrorDetail{Field: "items", Message: item})
	}
	httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, err.Error(), details)
	return true
}

// writeRepositoryDeleted answers a registration of an id that was deleted.
func writeRepositoryDeleted(w http.ResponseWriter, err error) bool {
	if !errors.Is(err, ports.ErrRepositoryDeleted) {
		return false
	}
	httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, err.Error(),
		[]httpapi.ErrorDetail{{Field: "repository", Message: appcatalog.RefusalDeleted}})
	return true
}
