package adapterbuild

import "github.com/taQuangLing/agent-workflow/internal/delivery/cli"

// AppOperation/command-type constants for this package's four commands.
// commandTypeProbe/commandTypeRegister are byte-for-byte identical to the
// commandType string literal internal/delivery/httpapi/adapterbuild/
// adapterbuild.go's own commandTypeProbe/commandTypeRegister constants —
// a hard requirement (see internal/delivery/cli/definitions/descriptor.go's
// own identical doc comment for why): cli.Dispatch/cli.BuildEnvelope reuse
// the exact same httpapi.SemanticHash/LookupReceipt/ReconcileReceipt replay
// authority internal/delivery/httpapi/adapterbuild itself dispatches
// through, keyed in part on this command_type string, so an HTTP call and a
// CLI call for "the same command" must hash and replay identically.
const (
	appOpListAdapterBuilds = "ListAdapterBuilds"
	appOpGetAdapterBuild   = "GetAdapterBuild"
	commandTypeProbe       = "ProbeAdapterBuild"
	commandTypeRegister    = "RegisterAdapterBuild"
)

// This block registers V6-15F's own four Descriptors into cli.Default from
// this package's own init() — the "own package, own init()" discipline
// descriptor.go's own doc comment (internal/delivery/cli) describes,
// mirroring internal/delivery/cli/settings and internal/delivery/cli/doctor's
// own init() shape exactly. Every command here is installation-scoped only
// (ADR-022/ADR-025's own closed list names ProbeAdapterBuild/
// RegisterAdapterBuild explicitly, and AdapterBuildVersion has no
// project-scoped mirror at all — this task's own "Không làm: no ProjectID"
// line) — unlike internal/delivery/cli/definitions, there is no dual
// registration at cli.ScopeProject here.
func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"adapter", "list"}, Scope: cli.ScopeInstallation,
		AppOperation: appOpListAdapterBuilds, HTTPOperationID: "listAdapterBuilds",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"adapter", "show"}, Scope: cli.ScopeInstallation,
		AppOperation: appOpGetAdapterBuild, HTTPOperationID: "getAdapterBuild",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"adapter", "probe"}, Scope: cli.ScopeInstallation,
		AppOperation: commandTypeProbe, HTTPOperationID: "probeAdapterBuild",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"adapter", "register"}, Scope: cli.ScopeInstallation,
		AppOperation: commandTypeRegister, HTTPOperationID: "registerAdapterBuild",
	})
}
