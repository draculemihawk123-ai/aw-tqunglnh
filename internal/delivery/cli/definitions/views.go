package definitions

import (
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// This file defines this package's own JSON view shapes for every
// read-only Run* function's own cli.EncodeQueryResult output — mirroring
// internal/delivery/cli/catalog/views.go's own reasoning exactly:
// definition.Fields/VersionFields carry no JSON tags of their own (Fields
// has none at all; VersionFields' own fields are all unexported — V2-01's
// own immutability discipline), so every view type here is a small,
// explicit, camelCase-tagged mapping this package owns end to end,
// mirroring internal/delivery/httpapi/definitions/dto.go's own
// scopeView/definitionView/versionFieldsView field for field.

// scopeView is the wire shape of a definition.Scope — mirrors
// internal/delivery/httpapi/definitions/dto.go's own scopeView exactly:
// {"global":true} for the installation-wide scope, {"global":false,
// "projectId":"..."} for a project scope, never a bare nullable projectId
// field.
type scopeView struct {
	Global    bool   `json:"global"`
	ProjectID string `json:"projectId,omitempty"`
}

func newScopeView(s definition.Scope) scopeView {
	if s.IsGlobal() {
		return scopeView{Global: true}
	}
	return scopeView{Global: false, ProjectID: string(*s.ProjectID)}
}

// definitionView is `aw definition show`'s own wire shape for a
// definition.Fields — mirrors httpapi's own definitionView.
type definitionView struct {
	ID      string            `json:"id"`
	Kind    definition.Kind   `json:"kind"`
	Scope   scopeView         `json:"scope"`
	Name    string            `json:"name"`
	Status  definition.Status `json:"status"`
	Version uint64            `json:"version"`
}

func newDefinitionView(id string, fields definition.Fields) definitionView {
	return definitionView{ID: id, Kind: fields.Kind, Scope: newScopeView(fields.Scope), Name: fields.Name, Status: fields.Status, Version: fields.Version}
}

// definitionListView is `aw definition list`'s own response shape.
type definitionListView struct {
	Definitions []definitionView `json:"definitions"`
}

// versionFieldsView is `aw definition validate`/`aw definition publish`/
// `aw version show`'s own wire shape for a definition.VersionFields —
// mirrors internal/app/definitions' own unexported versionFieldsDTO and
// httpapi's own versionFieldsView field for field (this package's own
// fourth independent copy of the identical shape — see
// internal/app/definitions/commands.go's own versionFieldsDTO doc comment
// for why a JSON-tagged mirror of an intentionally-unexported domain type
// is never shared code, only ever independently reproduced per caller).
type versionFieldsView struct {
	ID               string                        `json:"id"`
	DefinitionID     string                        `json:"definitionId"`
	Kind             definition.Kind               `json:"kind"`
	VersionNumber    uint64                        `json:"versionNumber"`
	SchemaVersion    int                           `json:"schemaVersion"`
	CanonicalSource  string                        `json:"canonicalSource"`
	SourceHash       string                        `json:"sourceHash"`
	CompiledSnapshot string                        `json:"compiledSnapshot"`
	CompiledHash     string                        `json:"compiledHash"`
	Dependencies     definition.DependencyManifest `json:"dependencies"`
	PublishedBy      string                        `json:"publishedBy"`
	PublishedAt      time.Time                     `json:"publishedAt"`
}

func newVersionFieldsView(v definition.VersionFields) versionFieldsView {
	return versionFieldsView{
		ID: v.ID(), DefinitionID: v.DefinitionID(), Kind: v.Kind(),
		VersionNumber: v.VersionNumber(), SchemaVersion: v.SchemaVersion(),
		CanonicalSource: v.CanonicalSource(), SourceHash: v.SourceHash(),
		CompiledSnapshot: v.CompiledSnapshot(), CompiledHash: v.CompiledHash(),
		Dependencies: v.Dependencies(), PublishedBy: v.PublishedBy(), PublishedAt: v.PublishedAt(),
	}
}

// versionListView is `aw definition versions <id>`'s own response shape.
type versionListView struct {
	Items []versionFieldsView `json:"items"`
}

// diagnosticView is one field-level explanation `aw definition validate`
// attaches when the authored document fails to compile — mirrors
// internal/delivery/httpapi.ErrorDetail's own {field, message} shape
// (this package defines its own copy rather than importing the shared
// httpapi package's type here, so this leaf's own wire contract stays
// entirely self-contained, the same "each package owns its own view
// types" discipline views.go's own top doc comment already establishes).
type diagnosticView struct {
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

// validateResultView is `aw definition validate`'s own response shape —
// V6-15E's own "Diagnostics" Verify bullet made concrete: Valid is always
// present, Version is populated only when Valid, Diagnostics only when
// not. A caller (human or script) never has to infer success/failure from
// process exit status alone; the one JSON document on stdout already
// carries the full, structured answer either way — see validate.go's own
// doc comment for exactly which errors populate Diagnostics versus
// propagating as a plain Go error instead (a request-shape problem, e.g.
// unknown --kind or missing --content, is never a "diagnostic": it never
// even reaches ValidateDraft).
type validateResultView struct {
	Valid       bool               `json:"valid"`
	Version     *versionFieldsView `json:"version,omitempty"`
	Diagnostics []diagnosticView   `json:"diagnostics,omitempty"`
}
