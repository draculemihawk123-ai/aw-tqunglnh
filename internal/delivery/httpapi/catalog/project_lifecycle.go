package catalog

import (
	"net/http"
	"strings"

	appcatalog "github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// V11-13 (ADR-044): rename, archive and restore a project.

// projectVersion reads the If-Match version a project mutation needs, writing the error itself when it is missing or malformed.
func projectVersion(w http.ResponseWriter, r *http.Request) (uint64, bool) {
	ifMatch, err := httpapi.RequireIfMatch(r)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
		return 0, false
	}
	version, err := httpapi.VersionFromETag(ifMatch)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
		return 0, false
	}
	return version, true
}

// renameProjectRequestBody is `PATCH /projects/{id}`'s body.
type renameProjectRequestBody struct {
	Name string `json:"name"`
}

// renameProject handles `PATCH /projects/{id}`.
func (h *handler) renameProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	version, ok := projectVersion(w, r)
	if !ok {
		return
	}
	var body renameProjectRequestBody
	cmd, ok := h.beginMutation(w, r, ports.ProjectScope(id), "RenameProject", version, &body)
	if !ok {
		return
	}
	result, err := appcatalog.RenameProject(r.Context(), h.uow, cmd, appcatalog.RenameProjectRequest{ProjectID: id, Name: body.Name})
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	_ = httpapi.EncodeResult(w, http.StatusOK, result, httpapi.ETagFromVersion(result.Version))
}

// archiveProject handles `POST /projects/{id}/archive`.
func (h *handler) archiveProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	version, ok := projectVersion(w, r)
	if !ok {
		return
	}
	var body reasonRequestBody
	cmd, ok := h.beginMutation(w, r, ports.ProjectScope(id), "ArchiveProject", version, &body)
	if !ok {
		return
	}
	result, err := appcatalog.ArchiveProject(r.Context(), h.uow, cmd, appcatalog.ArchiveProjectRequest{ProjectID: id, Reason: strings.TrimSpace(body.Reason)})
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	_ = httpapi.EncodeResult(w, http.StatusOK, result, httpapi.ETagFromVersion(result.Version))
}

// emptyRequestBody is the body of restore: nothing, but still a strictly decoded JSON object like every other mutation.
type emptyRequestBody struct{}

// restoreProject handles `POST /projects/{id}/restore`.
func (h *handler) restoreProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	version, ok := projectVersion(w, r)
	if !ok {
		return
	}
	var body emptyRequestBody
	cmd, ok := h.beginMutation(w, r, ports.ProjectScope(id), "RestoreProject", version, &body)
	if !ok {
		return
	}
	result, err := appcatalog.RestoreProject(r.Context(), h.uow, cmd, appcatalog.RestoreProjectRequest{ProjectID: id})
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	_ = httpapi.EncodeResult(w, http.StatusOK, result, httpapi.ETagFromVersion(result.Version))
}

// projectValidActions lists the lifecycle operations a project allows: an ACTIVE one can be renamed or archived, an ARCHIVED one can only be restored.
func projectValidActions(p project.Project) []httpapi.ValidAction {
	var actions []httpapi.ValidAction
	for _, id := range appcatalog.ProjectActionIDs(p) {
		actions = append(actions, httpapi.ValidAction{OperationID: id, ScopeKind: httpapi.ScopeProject, TargetVersion: int64(p.Version)})
	}
	return actions
}
