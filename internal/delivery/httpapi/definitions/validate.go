package definitions

import (
	"net/http"
	"strings"

	appdefinitions "github.com/taQuangLing/agent-workflow/internal/app/definitions"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// handleValidateDefinitionDraft implements
// POST /definitions/{kind}/{id}/validate (operationId
// validateDefinitionDraft): a read-only dry-run compile — no Command
// envelope (internal/app/definitions.ValidateDraft's own doc comment: "a
// dry run has no side effect to make idempotent"), so, unlike create/
// publish, this route requires no Idempotency-Key and never touches the
// receipt store.
func handleValidateDefinitionDraft(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		validateDefinitionDraftCore(w, r, deps, definition.GlobalScope())
	}
}

// handleValidateProjectDefinitionDraft implements
// POST /projects/{projectId}/definitions/{kind}/{id}/validate (operationId
// validateProjectDefinitionDraft): the project-scoped half.
func handleValidateProjectDefinitionDraft(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("projectId")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "projectId", "is required")
			return
		}
		validateDefinitionDraftCore(w, r, deps, definitionScopeFromProjectID(projectID))
	}
}

func validateDefinitionDraftCore(w http.ResponseWriter, r *http.Request, deps Dependencies, routeScope definition.Scope) {
	kind, ok := pathKind(w, r)
	if !ok {
		return
	}
	definitionID := r.PathValue("id")
	if strings.TrimSpace(definitionID) == "" {
		writeValidationError(w, "id", "is required")
		return
	}

	// Authoritative reload: the Definition must already exist (via
	// 'create') and must actually belong to THIS route's own derived
	// scope — never trusted from the request body — before a dry-run
	// candidate is ever compiled against it.
	fields, ok := loadDefinitionInScope(r.Context(), w, deps, kind, definitionID, routeScope)
	if !ok {
		return
	}

	var body authorDocumentBody
	if err := httpapi.DecodeJSON(r, maxBodyBytes, &body); err != nil {
		httpapi.WriteDecodeError(w, err)
		return
	}

	principal := httpapi.PrincipalFromContext(r.Context())
	publishedAt := deps.Clock.Now()
	versionID := deps.IDs.NewID()

	compile, wfDefinition, wfRequest, ok := buildCandidate(w, kind, definitionID, fields, body, versionID, 1, principal.Actor, publishedAt)
	if !ok {
		return
	}

	result, err := appdefinitions.ValidateDraft(r.Context(), deps.UnitOfWork, appdefinitions.ValidateDraftRequest{
		Kind: kind, Compile: compile, WorkflowDefinition: wfDefinition, WorkflowRequest: wfRequest,
	})
	if err != nil {
		writeCommandError(w, err)
		return
	}
	_ = httpapi.EncodeResult(w, http.StatusOK, newVersionFieldsView(result), "")
}
