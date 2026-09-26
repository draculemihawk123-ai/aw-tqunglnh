package definitions

import "github.com/taQuangLing/agent-workflow/internal/delivery/cli"

// AppOperation/command-type constants for this package's eight commands.
// CreateDefinition/PublishDefinitionVersion are byte-for-byte identical to
// the commandType string literal internal/delivery/httpapi/definitions'
// own handlers already pass to prepareCommand — a hard requirement (see
// internal/delivery/cli/catalog/helpers.go's own commandType* doc comment
// for why): cli.Dispatch/cli.BuildEnvelope reuse the exact same
// httpapi.SemanticHash/LookupReceipt/ReconcileReceipt replay authority
// internal/delivery/httpapi/definitions itself uses, keyed in part on this
// command_type string, so an HTTP call and a CLI call for "the same
// command" must hash and replay identically.
const (
	appOpListDefinitions         = "ListDefinitions"
	commandTypeCreateDefinition  = "CreateDefinition"
	appOpGetDefinition           = "GetDefinition"
	appOpListVersions            = "ListVersions"
	appOpValidateDraft           = "ValidateDraft"
	commandTypePublishDefinition = "PublishDefinitionVersion"
	appOpLoadAnyVersion          = "LoadAnyVersion"
	appOpDiffVersions            = "DiffVersions"
)

// This block registers V6-15E's own sixteen Descriptors into cli.Default
// from this package's own init() — the "own package, own init()"
// discipline descriptor.go's own doc comment describes, mirroring
// internal/delivery/cli/catalog's own init() shape exactly. Every command
// this package exposes is dual-scoped (ADR-028's own "definition global
// và project có thể cùng path CLI nhưng lần lượt là --scope global và
// --project-id <id>, map tới hai operationId khác nhau" example, named
// directly) — one CLI Path registered twice, once per Scope, each with the
// HTTPOperationID its own scoped half of internal/delivery/httpapi/
// definitions/routes.go actually uses. `definition list` used to be the one
// exception (HTTPOperationID cli.CLILocalOperation at both scopes, tracked
// as accepted debt in internal/delivery/parity/ledger.go's own "definition
// list: a CLI_LOCAL leaf with no route" entries) until V7-07A added
// GET /definitions/{kind} / GET /projects/{projectId}/definitions/{kind} —
// it now mirrors listDefinitions/listProjectDefinitions exactly like every
// other command in this file.
func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"definition", "list"}, Scope: cli.ScopeInstallation,
		AppOperation: appOpListDefinitions, HTTPOperationID: "listDefinitions",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"definition", "list"}, Scope: cli.ScopeProject,
		AppOperation: appOpListDefinitions, HTTPOperationID: "listProjectDefinitions",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"definition", "create"}, Scope: cli.ScopeInstallation,
		AppOperation: commandTypeCreateDefinition, HTTPOperationID: "createDefinition",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"definition", "create"}, Scope: cli.ScopeProject,
		AppOperation: commandTypeCreateDefinition, HTTPOperationID: "createProjectDefinition",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"definition", "show"}, Scope: cli.ScopeInstallation,
		AppOperation: appOpGetDefinition, HTTPOperationID: "getDefinition",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"definition", "show"}, Scope: cli.ScopeProject,
		AppOperation: appOpGetDefinition, HTTPOperationID: "getProjectDefinition",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"definition", "versions"}, Scope: cli.ScopeInstallation,
		AppOperation: appOpListVersions, HTTPOperationID: "listDefinitionVersions",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"definition", "versions"}, Scope: cli.ScopeProject,
		AppOperation: appOpListVersions, HTTPOperationID: "listProjectDefinitionVersions",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"definition", "validate"}, Scope: cli.ScopeInstallation,
		AppOperation: appOpValidateDraft, HTTPOperationID: "validateDefinitionDraft",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"definition", "validate"}, Scope: cli.ScopeProject,
		AppOperation: appOpValidateDraft, HTTPOperationID: "validateProjectDefinitionDraft",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"definition", "publish"}, Scope: cli.ScopeInstallation,
		AppOperation: commandTypePublishDefinition, HTTPOperationID: "publishDefinitionVersion",
		HighImpact: true, // UX Screen 4: "dialog confirm publish" (V6-15O confirmation parity)
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"definition", "publish"}, Scope: cli.ScopeProject,
		AppOperation: commandTypePublishDefinition, HTTPOperationID: "publishProjectDefinitionVersion",
		HighImpact: true,
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"version", "show"}, Scope: cli.ScopeInstallation,
		AppOperation: appOpLoadAnyVersion, HTTPOperationID: "getDefinitionVersion",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"version", "show"}, Scope: cli.ScopeProject,
		AppOperation: appOpLoadAnyVersion, HTTPOperationID: "getProjectDefinitionVersion",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"version", "diff"}, Scope: cli.ScopeInstallation,
		AppOperation: appOpDiffVersions, HTTPOperationID: "diffDefinitionVersions",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"version", "diff"}, Scope: cli.ScopeProject,
		AppOperation: appOpDiffVersions, HTTPOperationID: "diffProjectDefinitionVersions",
	})
}
