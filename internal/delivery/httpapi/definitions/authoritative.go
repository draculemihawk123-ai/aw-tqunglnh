// This file is the one place every mutating and read route in this
// package reloads its own authoritative target through, so "route derives
// scope; item/version reload phải authoritative Definition" and "không tin
// scope từ payload" (docs/design/08-v6-api-projections.md V6-05's own
// Thực hiện/Không làm lines) are each satisfied by construction exactly
// once, never re-derived slightly differently by seven separate handlers.
package definitions

import (
	"context"
	"net/http"

	appdefinitions "github.com/taQuangLing/agent-workflow/internal/app/definitions"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// loadDefinitionInScope reloads id's real, authoritative Definition row
// (internal/app/definitions.GetDefinition — never a client-echoed value)
// and confirms it actually belongs to routeScope — the scope THIS route
// itself derived from its own path (global prefix, or a real
// {projectId}), never trusted from the request body. Both "does not
// exist" and "exists but in a different scope" write the identical
// leakage-normalized WriteResourceHidden response, so a caller can never
// use a wrong-scope route to learn whether some DefinitionID exists at
// all (V6-05's own "global/project negative matrix" Verify bullet).
func loadDefinitionInScope(ctx context.Context, w http.ResponseWriter, deps Dependencies, kind definition.Kind, id string, routeScope definition.Scope) (definition.Fields, bool) {
	fields, err := appdefinitions.GetDefinition(ctx, deps.UnitOfWork, kind, id)
	if err != nil {
		writeQueryError(w, err)
		return definition.Fields{}, false
	}
	if !scopesMatch(fields.Scope, routeScope) {
		httpapi.WriteResourceHidden(w)
		return definition.Fields{}, false
	}
	return fields, true
}

// loadVersionInScope resolves versionID against either persistence layout
// (appdefinitions.LoadAnyVersion — this package's own routes deliberately
// carry no {kind} path segment for a bare version lookup, see routes.go's
// own doc comment for why), then re-derives Kind()/DefinitionID() from the
// loaded Version itself to reload ITS OWN owning Definition and confirm it
// belongs to routeScope — the same leakage-normalized check
// loadDefinitionInScope performs, applied here for a route addressed by
// VersionID instead of DefinitionID.
func loadVersionInScope(ctx context.Context, w http.ResponseWriter, deps Dependencies, versionID string, routeScope definition.Scope) (definition.VersionFields, bool) {
	version, err := appdefinitions.LoadAnyVersion(ctx, deps.UnitOfWork, versionID)
	if err != nil {
		writeQueryError(w, err)
		return definition.VersionFields{}, false
	}
	owner, err := appdefinitions.GetDefinition(ctx, deps.UnitOfWork, version.Kind(), version.DefinitionID())
	if err != nil {
		writeQueryError(w, err)
		return definition.VersionFields{}, false
	}
	if !scopesMatch(owner.Scope, routeScope) {
		httpapi.WriteResourceHidden(w)
		return definition.VersionFields{}, false
	}
	return version, true
}
