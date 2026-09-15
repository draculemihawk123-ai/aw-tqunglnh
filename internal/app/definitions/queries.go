// This file adds two read-only queries V6-05
// (docs/design/08-v6-api-projections.md) needs that V2-09/V2-10 never
// built: GetDefinition (reload one Definition's own current Kind/Scope/
// Name/Status/generation) and LoadAnyVersion (resolve a published Version
// by ID without the caller needing to already know its Kind). Neither
// takes a ports.Command — both are pure reads, the same "no command
// envelope for a query" discipline ListVersions/LoadVersion above already
// follow.
package definitions

import (
	"context"
	"errors"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// GetDefinition returns id's own current Definition Fields (Kind/Scope/
// Name/Status/generation), or ports.ErrPersistenceNotFound — a read-only
// query, never a Command. The caller supplies kind (the same convention
// CreateDefinition/PublishDefinitionVersion/ListVersions already use to
// route between the shared definitions table and Workflow's own
// workflow_definitions table) — this is what lets an HTTP route (or any
// other caller) confirm its own authoritative scope for id BEFORE
// dispatching a mutation against it, rather than trusting a client-supplied
// scope (V6-05's own "Không làm: ... tin scope từ payload").
func GetDefinition(ctx context.Context, uow ports.UnitOfWork, kind definition.Kind, id string) (definition.Fields, error) {
	var result definition.Fields
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		fields, err := tx.Definitions().GetDefinition(ctx, kind, id)
		result = fields
		return err
	})
	return result, err
}

// LoadAnyVersion resolves versionID against either persistence layout a
// published Version might live in: the eight shared kinds' table (via
// LoadVersion above) first, falling back to Workflow's own dedicated
// tables (via DefinitionsRepository.GetWorkflowVersion) only when the
// shared-table lookup specifically reports
// ports.ErrDefinitionVersionNotFound. This mirrors cmd/aw/definition.go's
// own loadAnyVersion (V2-11) exactly, moved to the application layer so a
// caller that does not already know a Version's Kind (V6-05's own "get
// one version"/"diff" routes, whose path deliberately carries no {kind}
// segment — see internal/delivery/httpapi/definitions' own routes.go doc
// comment for why) has one shared, tested resolution path instead of
// reimplementing the same fallback a third time (cmd/aw/definition.go is
// left untouched; this is a new, additive sibling, not a refactor of that
// CLI-local copy).
func LoadAnyVersion(ctx context.Context, uow ports.UnitOfWork, versionID string) (definition.VersionFields, error) {
	fields, err := LoadVersion(ctx, uow, versionID)
	if err == nil {
		return fields, nil
	}
	if !errors.Is(err, ports.ErrDefinitionVersionNotFound) {
		return definition.VersionFields{}, err
	}
	var wfFields definition.VersionFields
	wfErr := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		v, loadErr := tx.Definitions().GetWorkflowVersion(ctx, versionID)
		if loadErr != nil {
			return loadErr
		}
		converted, convErr := workflowVersionToVersionFields(v)
		wfFields = converted
		return convErr
	})
	if wfErr != nil {
		if errors.Is(wfErr, ports.ErrPersistenceNotFound) {
			return definition.VersionFields{}, err // the original, shared-table not-found error
		}
		return definition.VersionFields{}, wfErr
	}
	return wfFields, nil
}
