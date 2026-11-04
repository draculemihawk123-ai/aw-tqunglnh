package catalog

import (
	"errors"
	"net/http"

	appcatalog "github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// taskDocumentsRequestBody is POST /projects/{id}/task-documents's body (V12-33, ADR-065).
type taskDocumentsRequestBody struct {
	// Location is REPO (docs/features/<feature>/tasks/<task>/, committed) or AW (.aw/features/…, kept out of git).
	Location string `json:"location"`
}

func registerTaskDocumentsRoutes(registry *httpapi.RouteRegistry, h *handler) {
	registry.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{id}/task-documents", OperationID: "getProjectTaskDocuments",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: appcatalog.TaskDocumentsView{},
		Handler: func(w http.ResponseWriter, r *http.Request) {
			view, err := appcatalog.GetTaskDocuments(r.Context(), h.uow, r.PathValue("id"))
			if err != nil {
				writeCatalogError(w, err)
				return
			}
			_ = httpapi.EncodeResult(w, http.StatusOK, view, "")
		},
	})
	registry.Register(httpapi.RouteDescriptor{
		Method: http.MethodPost, Path: "/projects/{id}/task-documents", OperationID: "setProjectTaskDocuments",
		ScopeKind: httpapi.ScopeProject, RequestSchema: taskDocumentsRequestBody{}, ResponseSchema: appcatalog.TaskDocumentsView{},
		Handler: func(w http.ResponseWriter, r *http.Request) {
			id := r.PathValue("id")
			var body taskDocumentsRequestBody
			cmd, ok := h.beginMutation(w, r, ports.ProjectScope(id), "SetProjectTaskDocuments", 0, &body)
			if !ok {
				return
			}
			view, err := appcatalog.SetTaskDocuments(r.Context(), h.uow, cmd, id, body.Location)
			if errors.Is(err, appcatalog.ErrInvalidTaskDocuments) {
				httpapi.WriteError(w, http.StatusUnprocessableEntity, httpapi.ErrorCodeInvalidRequest, err.Error(),
					[]httpapi.ErrorDetail{{Field: "location", Message: err.Error()}})
				return
			}
			if err != nil {
				writeCatalogError(w, err)
				return
			}
			_ = httpapi.EncodeResult(w, http.StatusOK, view, "")
		},
	})
}
