package catalog

import (
	"net/http"

	appcatalog "github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// createProject handles `POST /projects`. Scope is always
// ports.InstallationScope() — ADR-025's own closed installation-scoped
// command set names CreateProject explicitly, and
// internal/app/catalog.CreateProject itself rejects anything else with
// ports.ErrScopeMismatch before ever touching a receipt.
//
// The request body decodes directly into appcatalog.CreateProjectRequest
// (no separate HTTP-only DTO needed): that struct's one field, Name, has
// no JSON tag, so encoding/json's own case-insensitive fallback matching
// binds a `{"name": "..."}` body to it exactly the way
// internal/delivery/httpapi/receiptreplay_test.go's own
// buildCreateProjectCommand already proves — this handler is that same
// precedent turned into a real, reachable HTTP route.
func (h *handler) createProject(w http.ResponseWriter, r *http.Request) {
	var req appcatalog.CreateProjectRequest
	cmd, ok := h.beginMutation(w, r, ports.InstallationScope(), "CreateProject", 0, &req)
	if !ok {
		return
	}

	result, err := appcatalog.CreateProject(r.Context(), h.uow, h.ids, cmd, req)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	_ = httpapi.EncodeResult(w, http.StatusCreated, result, "")
}

// listProjects handles `GET /projects` — installation-scoped, per ADR-025
// (ListProjects is explicitly in the closed installation-scoped query
// set).
func (h *handler) listProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := appcatalog.ListProjects(r.Context(), h.uow, ports.InstallationScope())
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	views := make([]projectView, 0, len(projects))
	for _, p := range projects {
		views = append(views, newProjectView(p))
	}
	_ = httpapi.EncodeResult(w, http.StatusOK, projectListView{Projects: views}, "")
}

// getProject handles `GET /projects/{id}` — the route itself already
// names the exact Project being requested, so scope is built directly
// from the path (ports.ProjectScope(id)), never reloaded from anywhere
// else first; appcatalog.GetProject itself still re-enforces that this
// exact scope names this exact projectID before returning anything
// (ports.ErrScopeMismatch otherwise), the same discipline every other
// project-nested route in this package reuses.
func (h *handler) getProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := appcatalog.GetProject(r.Context(), h.uow, ports.ProjectScope(id), id)
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	_ = httpapi.EncodeResult(w, http.StatusOK, newProjectView(p), "")
}
