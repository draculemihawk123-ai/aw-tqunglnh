package definitions

import (
	"net/http"
	"strings"

	appdefinitions "github.com/taQuangLing/agent-workflow/internal/app/definitions"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// handleGetDefinition implements GET /definitions/{kind}/{id} (operationId
// getDefinition): the authoritative (non-projected) Definition detail —
// reloaded, never a client-echoed value.
func handleGetDefinition(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		getDefinitionCore(w, r, deps, definition.GlobalScope())
	}
}

// handleGetProjectDefinition implements
// GET /projects/{projectId}/definitions/{kind}/{id} (operationId
// getProjectDefinition): the project-scoped half.
func handleGetProjectDefinition(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("projectId")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "projectId", "is required")
			return
		}
		getDefinitionCore(w, r, deps, definitionScopeFromProjectID(projectID))
	}
}

func getDefinitionCore(w http.ResponseWriter, r *http.Request, deps Dependencies, routeScope definition.Scope) {
	kind, ok := pathKind(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if strings.TrimSpace(id) == "" {
		writeValidationError(w, "id", "is required")
		return
	}
	fields, ok := loadDefinitionInScope(r.Context(), w, deps, kind, id, routeScope)
	if !ok {
		return
	}
	_ = httpapi.EncodeResult(w, http.StatusOK, newDefinitionView(id, fields), httpapi.ETagFromVersion(fields.Version))
}

// handleListDefinitions implements GET /definitions/{kind} (operationId
// listDefinitions): every Definition of that Kind in the global scope,
// closing the parity ledger gap internal/delivery/parity/ledger.go's own
// "definition list: a CLI_LOCAL leaf with no route" entries name
// (internal/app/definitions.ListDefinitions existed since V6-15E with no
// HTTP route ever calling it). No prior lookup is needed — ListDefinitions
// itself never errors for an empty/nonexistent scope, only ever returns an
// empty, non-nil slice (that function's own doc comment).
func handleListDefinitions(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		listDefinitionsCore(w, r, deps, definition.GlobalScope())
	}
}

// handleListProjectDefinitions implements
// GET /projects/{projectId}/definitions/{kind} (operationId
// listProjectDefinitions): the project-scoped half.
func handleListProjectDefinitions(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("projectId")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "projectId", "is required")
			return
		}
		listDefinitionsCore(w, r, deps, definitionScopeFromProjectID(projectID))
	}
}

func listDefinitionsCore(w http.ResponseWriter, r *http.Request, deps Dependencies, routeScope definition.Scope) {
	kind, ok := pathKind(w, r)
	if !ok {
		return
	}
	summaries, err := appdefinitions.ListDefinitions(r.Context(), deps.UnitOfWork, kind, routeScope)
	if err != nil {
		writeQueryError(w, err)
		return
	}
	views := make([]definitionView, 0, len(summaries))
	for _, s := range summaries {
		views = append(views, newDefinitionView(s.ID, s.Fields))
	}
	_ = httpapi.EncodeResult(w, http.StatusOK, definitionListView{Definitions: views}, "")
}

// handleListDefinitionVersions implements
// GET /definitions/{kind}/{id}/versions (operationId
// listDefinitionVersions): every Version this Definition has published,
// oldest first — internal/app/definitions.ListVersions' own order.
func handleListDefinitionVersions(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		listDefinitionVersionsCore(w, r, deps, definition.GlobalScope())
	}
}

// handleListProjectDefinitionVersions implements
// GET /projects/{projectId}/definitions/{kind}/{id}/versions (operationId
// listProjectDefinitionVersions): the project-scoped half.
func handleListProjectDefinitionVersions(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("projectId")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "projectId", "is required")
			return
		}
		listDefinitionVersionsCore(w, r, deps, definitionScopeFromProjectID(projectID))
	}
}

func listDefinitionVersionsCore(w http.ResponseWriter, r *http.Request, deps Dependencies, routeScope definition.Scope) {
	kind, ok := pathKind(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if strings.TrimSpace(id) == "" {
		writeValidationError(w, "id", "is required")
		return
	}
	// Authoritative reload/scope check first — a caller must not be able
	// to enumerate a wrong-scope DefinitionID's published version count
	// (or even whether it exists) via this list route.
	if _, ok := loadDefinitionInScope(r.Context(), w, deps, kind, id, routeScope); !ok {
		return
	}
	versions, err := appdefinitions.ListVersions(r.Context(), deps.UnitOfWork, kind, id)
	if err != nil {
		writeQueryError(w, err)
		return
	}
	views := make([]versionFieldsView, 0, len(versions))
	for _, v := range versions {
		views = append(views, newVersionFieldsView(v))
	}
	_ = httpapi.EncodeResult(w, http.StatusOK, versionListResponse{Items: views}, "")
}
