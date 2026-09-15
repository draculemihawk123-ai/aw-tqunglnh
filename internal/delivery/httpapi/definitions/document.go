// This file is validate.go and publish.go's shared body: decoding the
// authored-document request payload and dispatching it, by this route's
// own path {kind}, to either the eight shared kinds' Compile closure
// (dispatch.go's compileClosure) or Workflow's own
// WorkflowDefinition/PublishRequest pair (dispatch.go's
// workflowRequestFrom) — the identical Kind-selected either/or shape
// internal/app/definitions.ValidateDraftRequest/
// PublishDefinitionVersionRequest themselves use.
package definitions

import (
	"net/http"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// authorDocumentBody is the wire shape of the authored document payload
// both validate and publish accept — V6-05's own "Phạm vi: bounded text/
// file payload": Content is the document's own raw JSON or YAML text
// (never a caller-supplied file path this server would have to open).
// Dependencies is accepted only for the eight shared (non-Workflow)
// kinds — an author-declared pin list none of those kinds' own Compile
// resolves against the real registry (V2-11's own established scope
// boundary; only Workflow's node-level pins are resolved, by
// workflowcompiler.CompileAndResolve). A WORKFLOW body that supplies one
// is simply ignored, exactly like SchemaVersion is (WORKFLOW's own schema
// version is a field of the document itself, never a request parameter).
type authorDocumentBody struct {
	Content       string              `json:"content"`
	Format        string              `json:"format,omitempty"`
	SchemaVersion int                 `json:"schemaVersion,omitempty"`
	Dependencies  []dependencyPinBody `json:"dependencies,omitempty"`
}

// parseDocumentFormat validates body.Format against the two
// authoring.Format values DecodeStrict supports — mirrors
// cmd/aw/definition.go's own parseDocumentFormat, writing this package's
// own 400 response directly on failure rather than returning an error the
// caller has to translate.
func parseDocumentFormat(w http.ResponseWriter, raw string) (authoring.Format, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "json":
		return authoring.FormatJSON, true
	case "yaml", "yml":
		return authoring.FormatYAML, true
	default:
		writeValidationError(w, "format", "must be \"json\" or \"yaml\"")
		return 0, false
	}
}

// buildCandidate decodes body's Content/Format against kind's own real
// document shape and returns either a non-workflow Compile closure
// (compile != nil, wfDefinition/wfRequest zero) or a Workflow
// WorkflowDefinition/PublishRequest pair (compile == nil) — exactly the
// either/or ValidateDraftRequest/PublishDefinitionVersionRequest
// themselves accept. On any failure it writes the response itself
// (a 400 for a request-shape problem, or writeCommandError's own richer
// mapping for a document validation failure — e.g. authoring.Diagnostics
// with real source locations) and returns ok=false.
func buildCandidate(w http.ResponseWriter, kind definition.Kind, definitionID string, fields definition.Fields, body authorDocumentBody, versionID string, versionNumber uint64, publishedBy string, publishedAt time.Time) (compile func() (definition.VersionFields, error), wfDefinition workflow.WorkflowDefinition, wfRequest workflow.PublishRequest, ok bool) {
	if strings.TrimSpace(body.Content) == "" {
		writeValidationError(w, "content", "is required")
		return nil, workflow.WorkflowDefinition{}, workflow.PublishRequest{}, false
	}
	format, formatOK := parseDocumentFormat(w, body.Format)
	if !formatOK {
		return nil, workflow.WorkflowDefinition{}, workflow.PublishRequest{}, false
	}

	if kind == definition.KindWorkflow {
		if format != authoring.FormatJSON {
			writeValidationError(w, "format", "WORKFLOW documents must be \"json\" — no YAML decode path exists for WORKFLOW")
			return nil, workflow.WorkflowDefinition{}, workflow.PublishRequest{}, false
		}
		def, req, err := workflowRequestFrom(definitionID, fields, []byte(body.Content), versionID, versionNumber, publishedBy, publishedAt)
		if err != nil {
			writeCommandError(w, err)
			return nil, workflow.WorkflowDefinition{}, workflow.PublishRequest{}, false
		}
		return nil, def, req, true
	}

	schemaVersion := body.SchemaVersion
	if schemaVersion == 0 {
		schemaVersion = 1
	}
	compileFn, err := compileClosure(kind, fields, compileInputs{
		DefinitionID: definitionID, RawDocument: []byte(body.Content), Format: format,
		VersionID: versionID, VersionNumber: versionNumber, SchemaVersion: schemaVersion,
		Dependencies: toDependencyManifest(body.Dependencies), PublishedBy: publishedBy, PublishedAt: publishedAt,
	})
	if err != nil {
		writeCommandError(w, err)
		return nil, workflow.WorkflowDefinition{}, workflow.PublishRequest{}, false
	}
	return compileFn, workflow.WorkflowDefinition{}, workflow.PublishRequest{}, true
}
