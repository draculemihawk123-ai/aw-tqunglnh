package definitions

import (
	"net/http"
	"strings"

	appdefinitions "github.com/taQuangLing/agent-workflow/internal/app/definitions"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// createDefinitionBody is POST .../definitions/{kind}'s own wire request
// shape — deliberately without a scope/projectId field of its own: scope
// is always this route's own (global prefix, or a real path {projectId}),
// never re-declared by the caller's body (V6-05's own "Không làm: ... tin
// scope từ payload").
type createDefinitionBody struct {
	DefinitionID string `json:"definitionId"`
	Name         string `json:"name"`
}

// handleCreateDefinition implements POST /definitions/{kind} (operationId
// createDefinition): the installation-scoped half of
// internal/app/definitions.CreateDefinition.
func handleCreateDefinition(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		kind, ok := pathKind(w, r)
		if !ok {
			return
		}
		createDefinitionCore(w, r, deps, kind, definition.GlobalScope(), ports.InstallationScope())
	}
}

// handleCreateProjectDefinition implements
// POST /projects/{projectId}/definitions/{kind} (operationId
// createProjectDefinition): the project-scoped half.
func handleCreateProjectDefinition(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("projectId")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "projectId", "is required")
			return
		}
		kind, ok := pathKind(w, r)
		if !ok {
			return
		}
		createDefinitionCore(w, r, deps, kind, definitionScopeFromProjectID(projectID), ports.ProjectScope(projectID))
	}
}

// createDefinitionCore is both create routes' shared body: a fresh
// Definition (DRAFT, generation 1) for any of the nine DefinitionKinds —
// the one manual step validate/publish need a real Definition row to
// exist against, mirroring
// internal/app/definitions.CreateDefinition's own doc comment.
func createDefinitionCore(w http.ResponseWriter, r *http.Request, deps Dependencies, kind definition.Kind, defScope definition.Scope, cmdScope ports.CommandScope) {
	var body createDefinitionBody
	cmd, ok := prepareCommand(w, r, deps, "CreateDefinition", cmdScope, &body)
	if !ok {
		return
	}
	if strings.TrimSpace(body.DefinitionID) == "" {
		writeValidationError(w, "definitionId", "is required")
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		writeValidationError(w, "name", "is required")
		return
	}

	if replayOrProceed(r.Context(), w, deps, cmd) {
		return
	}

	result, err := appdefinitions.CreateDefinition(r.Context(), deps.UnitOfWork, cmd, appdefinitions.CreateDefinitionRequest{
		DefinitionID: body.DefinitionID, Kind: kind, Scope: defScope, Name: body.Name,
	})
	if err != nil {
		writeCommandError(w, err)
		return
	}
	// A freshly created Definition always starts at generation 1
	// (definition.Create hardcodes it) — no extra reload needed.
	_ = httpapi.EncodeResult(w, http.StatusCreated, result, httpapi.ETagFromVersion(1))
}
