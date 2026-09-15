package definitions

import (
	"net/http"
	"strings"

	appdefinitions "github.com/taQuangLing/agent-workflow/internal/app/definitions"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// handlePublishDefinitionVersion implements
// POST /definitions/{kind}/{id}/publish (operationId
// publishDefinitionVersion): the installation-scoped half of
// internal/app/definitions.PublishDefinitionVersion. This is a
// CREATE-shaped mutation (Idempotency-Key required, no If-Match) rather
// than an UPDATE-shaped one: PublishDefinitionVersionRequest carries no
// ExpectedVersion of its own — publishing genuinely new content always
// appends a new immutable Version, never overwrites an existing one, so
// there is no "current resource state" for an If-Match precondition to
// protect (mirrors CreateChildWorkItem's identical reasoning in
// internal/delivery/httpapi/workitem).
func handlePublishDefinitionVersion(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		publishDefinitionVersionCore(w, r, deps, definition.GlobalScope(), ports.InstallationScope())
	}
}

// handlePublishProjectDefinitionVersion implements
// POST /projects/{projectId}/definitions/{kind}/{id}/publish (operationId
// publishProjectDefinitionVersion): the project-scoped half.
func handlePublishProjectDefinitionVersion(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := r.PathValue("projectId")
		if strings.TrimSpace(projectID) == "" {
			writeValidationError(w, "projectId", "is required")
			return
		}
		publishDefinitionVersionCore(w, r, deps, definitionScopeFromProjectID(projectID), ports.ProjectScope(projectID))
	}
}

func publishDefinitionVersionCore(w http.ResponseWriter, r *http.Request, deps Dependencies, routeScope definition.Scope, cmdScope ports.CommandScope) {
	kind, ok := pathKind(w, r)
	if !ok {
		return
	}
	definitionID := r.PathValue("id")
	if strings.TrimSpace(definitionID) == "" {
		writeValidationError(w, "id", "is required")
		return
	}

	// Authoritative reload FIRST — before Idempotency-Key/body are even
	// read (mirrors internal/delivery/httpapi/workitem's own
	// handleCreateChildWorkItem doc comment): the Definition must already
	// exist and must actually belong to THIS route's own derived scope,
	// never trusted from the request body (V6-05's own "Không làm: ...
	// tin scope từ payload").
	fields, ok := loadDefinitionInScope(r.Context(), w, deps, kind, definitionID, routeScope)
	if !ok {
		return
	}

	var body authorDocumentBody
	cmd, ok := prepareCommand(w, r, deps, "PublishDefinitionVersion", cmdScope, &body)
	if !ok {
		return
	}

	if replayOrProceed(r.Context(), w, deps, cmd) {
		return
	}

	// VersionNumber: a discarded placeholder for the eight shared kinds
	// (internal/adapters/sqlite's own publishSharedDefinitionVersionTx
	// always reallocates the real next number from the database, ignoring
	// the candidate's own value entirely) — but Workflow's own
	// publishWorkflowVersionTx genuinely compares candidate.VersionNumber()
	// against the real next version and rejects a mismatch, so only for
	// Workflow this handler must ask the registry what that number
	// actually is first (mirrors cmd/aw/definition.go's own
	// runDefinitionPublish).
	versionNumber := uint64(1)
	if kind == definition.KindWorkflow {
		existing, err := appdefinitions.ListVersions(r.Context(), deps.UnitOfWork, definition.KindWorkflow, definitionID)
		if err != nil {
			writeCommandError(w, err)
			return
		}
		versionNumber = uint64(len(existing)) + 1
	}
	versionID := deps.IDs.NewID()

	compile, wfDefinition, wfRequest, ok := buildCandidate(w, kind, definitionID, fields, body, versionID, versionNumber, cmd.Actor, cmd.RequestedAt)
	if !ok {
		return
	}

	result, err := appdefinitions.PublishDefinitionVersion(r.Context(), deps.UnitOfWork, cmd, appdefinitions.PublishDefinitionVersionRequest{
		DefinitionID: definitionID, Kind: kind, Compile: compile, WorkflowDefinition: wfDefinition, WorkflowRequest: wfRequest,
	})
	if err != nil {
		writeCommandError(w, err)
		return
	}
	// The response itself already carries the exact source/compiled hash
	// and dependency pins this task's own "Thực hiện" line asks for
	// (versionFieldsView's own SourceHash/CompiledHash/Dependencies
	// fields) — no separate summary is needed.
	_ = httpapi.EncodeResult(w, http.StatusCreated, newVersionFieldsView(result), "")
}
