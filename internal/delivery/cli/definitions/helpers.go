package definitions

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/config"
	appdefinitions "github.com/taQuangLing/agent-workflow/internal/app/definitions"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// loadPrincipal resolves the acting principal exactly the way
// internal/delivery/cli/catalog's own loadPrincipal (and `aw serve`'s own
// --principal-config flag, cmd/aw/serve.go) already does: load the trusted
// JSON config file (or the local-operator/[operator] default when path is
// empty/missing/omits localPrincipal), then validate its shape. This is
// the ONLY place any Run* function in this package ever resolves a
// principal from — never a per-invocation --actor/--role flag, per
// ADR-028 and cli.BindPrincipalFlag's own doc comment.
func loadPrincipal(path string) (config.LocalPrincipal, error) {
	principal, err := config.LoadLocalPrincipalFile(path)
	if err != nil {
		return config.LocalPrincipal{}, err
	}
	if err := config.ValidateLocalPrincipal(principal); err != nil {
		return config.LocalPrincipal{}, err
	}
	return principal, nil
}

// usageErrorf builds a cli.UsageError (mapped to cli.ExitUsage by a future
// composition root's own cli.ExitCodeFor) from a formatted message —
// mirrors internal/delivery/cli/catalog's own usageErrorf exactly.
func usageErrorf(format string, args ...any) error {
	return cli.UsageError{Err: fmt.Errorf(format, args...)}
}

// parseFlags mirrors internal/delivery/cli/catalog's own parseFlags
// exactly: the one place every Run* function's own flag handling goes
// through, wrapping a parse failure as a cli.UsageError and passing
// flag.ErrHelp through unwrapped.
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return err
		}
		return cli.UsageError{Err: err}
	}
	return nil
}

// bindKindFlag registers --kind, required by every command in this package
// that routes between the shared definitions table and Workflow's own
// dedicated tables (create/show/list/versions/validate/publish — the same
// convention internal/app/definitions.CreateDefinition/GetDefinition/
// ListVersions/ListDefinitions themselves already use). `aw version show`/
// `aw version diff` never bind this: a VersionID alone is already globally
// unique and self-describing (internal/delivery/httpapi/definitions/
// routes.go's own doc comment gives the identical reason for those two
// routes never carrying a {kind} path segment either).
func bindKindFlag(fs *flag.FlagSet) *string {
	return fs.String("kind", "", "definition kind (WORKFLOW, BLOCK, SKILL, LAYER, ENGINEERING_PACK, AGENT_PROFILE, COMMAND, GATE, POLICY)")
}

// parseKind validates raw against definition.Kind's nine closed values,
// case-insensitively (so "block"/"BLOCK" both work) — mirrors
// internal/delivery/httpapi/definitions/dispatch.go's own pathKind.
func parseKind(raw string) (definition.Kind, error) {
	kind := definition.Kind(strings.ToUpper(strings.TrimSpace(raw)))
	if !kind.Valid() {
		return "", usageErrorf("--kind must be one of WORKFLOW, BLOCK, SKILL, LAYER, ENGINEERING_PACK, AGENT_PROFILE, COMMAND, GATE, POLICY (got %q)", raw)
	}
	return kind, nil
}

// bindProjectFlag is cli.BindProjectFlag under this package's own name —
// every command in this package is dual-scoped (global/project, ADR-028's
// own "definition global và project có thể cùng path CLI nhưng lần lượt là
// --scope global và --project-id <id>" example, named directly), so
// --project-id present selects the project-scoped half of that same
// command, absent selects the global half; there is no separate --scope
// flag since the two are mutually exclusive by construction.
func bindProjectFlag(fs *flag.FlagSet) *string {
	return cli.BindProjectFlag(fs)
}

// definitionScopeFromProjectID mirrors
// internal/delivery/httpapi/definitions's own definitionScopeFromProjectID:
// nil/empty means global, otherwise exactly the named project — this
// package's own single source of truth for turning --project-id into a
// definition.Scope, never trusted from a request body either way.
func definitionScopeFromProjectID(projectID string) definition.Scope {
	if strings.TrimSpace(projectID) == "" {
		return definition.GlobalScope()
	}
	return definition.ProjectScope(project.ProjectID(projectID))
}

// commandScopeFromProjectID is definitionScopeFromProjectID's own
// ports.CommandScope counterpart, for cli.BuildEnvelope's own Scope field —
// the two scope representations this package's mutating commands need side
// by side (definition.Scope for the domain-level create/reload calls,
// ports.CommandScope for the command-envelope/receipt-replay machinery).
func commandScopeFromProjectID(projectID string) ports.CommandScope {
	if strings.TrimSpace(projectID) == "" {
		return ports.InstallationScope()
	}
	return ports.ProjectScope(projectID)
}

// scopesMatch reports whether a and b name the exact same definition.Scope
// — both global, or both the same project ID. Mirrors
// internal/delivery/httpapi/definitions/dto.go's own scopesMatch (itself
// already documented there as "a fourth independent copy" of the identical
// two-line comparison) — this package's own fifth, for the same "each
// package stays free of a dependency on the others' internals" reasoning.
func scopesMatch(a, b definition.Scope) bool {
	if a.IsGlobal() != b.IsGlobal() {
		return false
	}
	if a.IsGlobal() {
		return true
	}
	return *a.ProjectID == *b.ProjectID
}

// ErrDefinitionNotFound is this package's own leakage-normalized not-found
// sentinel — returned by loadDefinitionInScope/loadVersionInScope for BOTH
// "no such Definition/Version exists" and "it exists, but not in the scope
// this invocation named", so a caller (an operator scripting against `aw
// definition show`) can never use a wrong-scope invocation to learn
// whether some ID exists at all. Mirrors
// internal/delivery/httpapi/definitions/authoritative.go's own
// WriteResourceHidden discipline, translated to a plain Go sentinel error
// since a CLI response has no HTTP status code to normalize.
var ErrDefinitionNotFound = errors.New("cli/definitions: definition not found")

// ErrDefinitionVersionNotFound is ErrDefinitionNotFound's own counterpart
// for `aw version show`/`aw version diff`.
var ErrDefinitionVersionNotFound = errors.New("cli/definitions: definition version not found")

// loadDefinitionInScope reloads id's real, authoritative Definition row
// (internal/app/definitions.GetDefinition) and confirms it actually
// belongs to routeScope — the scope THIS invocation itself derived from
// --project-id, never trusted from any request body. Mirrors
// internal/delivery/httpapi/definitions/authoritative.go's own
// loadDefinitionInScope exactly, translated from an http.ResponseWriter
// short-circuit to a plain (fields, error) return.
func loadDefinitionInScope(ctx context.Context, uow ports.UnitOfWork, kind definition.Kind, id string, routeScope definition.Scope) (definition.Fields, error) {
	fields, err := appdefinitions.GetDefinition(ctx, uow, kind, id)
	if err != nil {
		if errors.Is(err, ports.ErrPersistenceNotFound) {
			return definition.Fields{}, ErrDefinitionNotFound
		}
		return definition.Fields{}, err
	}
	if !scopesMatch(fields.Scope, routeScope) {
		return definition.Fields{}, ErrDefinitionNotFound
	}
	return fields, nil
}

// loadVersionInScope resolves versionID against either persistence layout
// (appdefinitions.LoadAnyVersion — `aw version show`/`aw version diff`
// deliberately carry no --kind flag, mirroring routes.go's own reasoning
// for why their HTTP equivalents carry no {kind} path segment), then
// re-derives Kind()/DefinitionID() from the loaded Version itself to
// reload ITS OWN owning Definition and confirm it belongs to routeScope —
// the same leakage-normalized check loadDefinitionInScope performs,
// applied here for an invocation addressed by VersionID instead of
// DefinitionID. Mirrors authoritative.go's own loadVersionInScope exactly.
func loadVersionInScope(ctx context.Context, uow ports.UnitOfWork, versionID string, routeScope definition.Scope) (definition.VersionFields, error) {
	version, err := appdefinitions.LoadAnyVersion(ctx, uow, versionID)
	if err != nil {
		if errors.Is(err, ports.ErrDefinitionVersionNotFound) || errors.Is(err, ports.ErrPersistenceNotFound) {
			return definition.VersionFields{}, ErrDefinitionVersionNotFound
		}
		return definition.VersionFields{}, err
	}
	owner, err := appdefinitions.GetDefinition(ctx, uow, version.Kind(), version.DefinitionID())
	if err != nil {
		if errors.Is(err, ports.ErrPersistenceNotFound) {
			return definition.VersionFields{}, ErrDefinitionVersionNotFound
		}
		return definition.VersionFields{}, err
	}
	if !scopesMatch(owner.Scope, routeScope) {
		return definition.VersionFields{}, ErrDefinitionVersionNotFound
	}
	return version, nil
}

// parseDocumentFormat validates raw against the two authoring.Format
// values DecodeStrict supports — mirrors
// internal/delivery/httpapi/definitions/document.go's own
// parseDocumentFormat, returning a cli.UsageError directly rather than
// writing an HTTP response.
func parseDocumentFormat(raw string) (authoring.Format, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "json":
		return authoring.FormatJSON, nil
	case "yaml", "yml":
		return authoring.FormatYAML, nil
	default:
		return 0, usageErrorf(`--format must be "json" or "yaml" (got %q)`, raw)
	}
}
