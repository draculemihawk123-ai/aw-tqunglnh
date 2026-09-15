package safesettings

import (
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/safesettings"
)

// RegisterRoutes registers both routes this package owns onto reg — a
// composition root (cmd/aw/serve.go) calls this once, alongside every
// sibling endpoint task's own RegisterRoutes.
func RegisterRoutes(reg *httpapi.RouteRegistry, deps Dependencies) {
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/settings/safe", OperationID: "getSafeSettings",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: struct{}{}, ResponseSchema: responseDTO{},
		Handler: handleGetSafeSettings(deps),
	})
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodPut, Path: "/settings/safe", OperationID: "updateSafeSettings",
		ScopeKind: httpapi.ScopeInstallation, RequestSchema: safesettings.SafeSettings{}, ResponseSchema: responseDTO{},
		Handler: handleUpdateSafeSettings(deps),
	})
}
