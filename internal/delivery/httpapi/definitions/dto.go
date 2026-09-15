package definitions

import (
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// scopeView is the wire shape of a definition.Scope: {"global":true} for
// the installation-wide scope, {"global":false,"projectId":"..."} for a
// project scope — never a bare nullable projectId field, so a client can
// never confuse "global" with "the JSON happened to omit projectId".
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

// definitionView is GET .../definitions/{kind}/{id}'s own response shape —
// the authoritative (non-projected) Definition row, reloaded via
// internal/app/definitions.GetDefinition, never a client-echoed value.
type definitionView struct {
	ID      string          `json:"id"`
	Kind    definition.Kind `json:"kind"`
	Scope   scopeView       `json:"scope"`
	Name    string          `json:"name"`
	Status  definition.Status `json:"status"`
	Version uint64          `json:"version"`
}

func newDefinitionView(id string, fields definition.Fields) definitionView {
	return definitionView{ID: id, Kind: fields.Kind, Scope: newScopeView(fields.Scope), Name: fields.Name, Status: fields.Status, Version: fields.Version}
}

// dependencyPinBody is the wire shape of one definition.DependencyPin — an
// author-declared pin on create/validate/publish's own request body for
// one of the eight shared (non-Workflow) kinds. Workflow never accepts
// this field: its own dependency manifest is always resolved automatically
// against the real registry (workflowcompiler.CompileAndResolve), never
// author-declared (see workflowDocumentBody's own doc comment).
type dependencyPinBody struct {
	Kind         string `json:"kind"`
	DefinitionID string `json:"definitionId"`
	VersionID    string `json:"versionId"`
}

func (p dependencyPinBody) toPin() definition.DependencyPin {
	return definition.DependencyPin{Kind: definition.Kind(p.Kind), DefinitionID: p.DefinitionID, VersionID: p.VersionID}
}

func toDependencyManifest(pins []dependencyPinBody) definition.DependencyManifest {
	if len(pins) == 0 {
		return definition.DependencyManifest{}
	}
	out := make([]definition.DependencyPin, 0, len(pins))
	for _, p := range pins {
		out = append(out, p.toPin())
	}
	return definition.DependencyManifest{Pins: out}
}

// versionFieldsView is this package's own stable, exported-field JSON view
// of a definition.VersionFields (whose own fields are all unexported —
// V2-01's immutability discipline) — mirrors
// internal/app/definitions.versionFieldsDTO and cmd/aw/definition.go's own
// versionFieldsView exactly, a THIRD independent copy of the identical
// shape for the identical reason those two already give: this package must
// never import cmd/aw (a `package main`, and backwards regardless), and
// internal/app/definitions' own versionFieldsDTO is unexported to its
// package.
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

// versionListResponse wraps a Version collection in an object rather than
// a bare top-level JSON array — mirrors workitem's own workItemListResponse
// doc comment exactly (room for a future cursor/count field with no
// breaking wire-shape change); ListVersions itself is unbounded/unpaged,
// like every other plain list already in this codebase.
type versionListResponse struct {
	Items []versionFieldsView `json:"items"`
}

// emptyBody is decoded for a route whose request carries no meaningful
// JSON fields of its own (mirrors workitem's identical emptyBody) —
// CanonicalizeJSON still strictly rejects an unexpected field or a
// malformed body even though nothing here is ever read back out of it.
type emptyBody struct{}

// definitionScopeFromProjectID mirrors cmd/aw/definition.go's own
// resolveDefinitionScopes, applied to just the definition.Scope half: nil/
// empty means global, otherwise exactly the named project — never trusted
// from a request body, always derived from the route itself (global prefix
// vs a real path {projectId}).
func definitionScopeFromProjectID(projectID string) definition.Scope {
	if projectID == "" {
		return definition.GlobalScope()
	}
	return definition.ProjectScope(project.ProjectID(projectID))
}

// scopesMatch reports whether a and b name the exact same definition.Scope
// — both global, or both the same project ID. Mirrors
// internal/app/ports/fake/unitofwork.go's own sameDefinitionScope exactly
// (a fourth independent copy of the identical two-line comparison, for the
// same "each package stays free of a dependency on the others' internals"
// reasoning already established there).
func scopesMatch(a, b definition.Scope) bool {
	if a.IsGlobal() != b.IsGlobal() {
		return false
	}
	if a.IsGlobal() {
		return true
	}
	return *a.ProjectID == *b.ProjectID
}
