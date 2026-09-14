package definitions

import (
	"net/http"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// handleGetDefinitionVersion implements
// GET /definitions/versions/{versionId} (operationId getDefinitionVersion):
// one published Version by its own immutable VersionID — deliberately no
// {kind} path segment (a VersionID alone is already globally unique and
// self-describing; see routes.go's own doc comment for why this route, and
// diff.go's, are the two exceptions to every other route in this package
// carrying {kind}).
func handleGetDefinitionVersion(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		getDefinitionVersionCore(w, r, deps, definition.GlobalScope())
	}
}

// handleGetProjectDefinitionVersion implements
// GET /projects/{projectId}/definitions/versions/{versionId} (operationId
// getProjectDefinitionVersion): the project-scoped half.
func handleGetProjectDefinitionVersion(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("projectId")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "projectId", "is required")
			return
		}
		getDefinitionVersionCore(w, r, deps, definitionScopeFromProjectID(projectID))
	}
}

func getDefinitionVersionCore(w http.ResponseWriter, r *http.Request, deps Dependencies, routeScope definition.Scope) {
	versionID := r.PathValue("versionId")
	if strings.TrimSpace(versionID) == "" {
		writeValidationError(w, "versionId", "is required")
		return
	}
	version, ok := loadVersionInScope(r.Context(), w, deps, versionID, routeScope)
	if !ok {
		return
	}
	_ = httpapi.EncodeResult(w, http.StatusOK, newVersionFieldsView(version), "")
}
