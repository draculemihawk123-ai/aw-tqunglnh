package catalog

import (
	"errors"
	"net/http"

	appcatalog "github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// commitTemplateRequestBody is POST /projects/{id}/commit-template's body (V12-25, ADR-058): the template, or "" to remove it.
type commitTemplateRequestBody struct {
	Template string `json:"template"`
}

func registerCommitTemplateRoutes(registry *httpapi.RouteRegistry, h *handler) {
	registry.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{id}/commit-template", OperationID: "getProjectCommitTemplate",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: appcatalog.CommitTemplateView{},
		Handler: h.getCommitTemplate,
	})
	registry.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/projects/{id}/commit-template", OperationID: "setProjectCommitTemplate",
		ScopeKind: httpapi.ScopeProject, RequestSchema: commitTemplateRequestBody{}, ResponseSchema: appcatalog.CommitTemplateView{},
		Handler: h.setCommitTemplate,
	})
}

func (h *handler) getCommitTemplate(w http.ResponseWriter, r *http.Request) {
	view, err := appcatalog.GetCommitTemplate(r.Context(), h.uow, r.PathValue("id"))
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	_ = httpapi.EncodeResult(w, http.StatusOK, view, "")
}

func (h *handler) setCommitTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body commitTemplateRequestBody
	cmd, ok := h.beginMutation(w, r, ports.ProjectScope(id), "SetProjectCommitTemplate", 0, &body)
	if !ok {
		return
	}
	view, err := appcatalog.SetCommitTemplate(r.Context(), h.uow, cmd, id, body.Template)
	if errors.Is(err, appcatalog.ErrInvalidCommitTemplate) {
		httpapi.WriteError(w, http.StatusUnprocessableEntity, httpapi.ErrorCodeInvalidRequest, err.Error(),
			[]httpapi.ErrorDetail{{Field: "template", Message: err.Error()}})
		return
	}
	if err != nil {
		writeCatalogError(w, err)
		return
	}
	_ = httpapi.EncodeResult(w, http.StatusOK, view, "")
}
