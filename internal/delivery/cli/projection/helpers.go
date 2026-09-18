package projection

import (
	"flag"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// appOpGetProjectionStatus/appOpGetProjectionRebuildStatus and
// commandTypeRequestProjectionRebuild are this package's own descriptor
// AppOperation/command-type constants. commandTypeRequestProjectionRebuild
// is byte-for-byte identical to internal/delivery/httpapi/projectionrebuild's
// own commandTypeRequestProjectionRebuild constant — a hard requirement
// (mirrors internal/delivery/cli/adapterbuild/descriptor.go's own identical
// doc comment for why): cli.Dispatch/cli.BuildEnvelope reuse the exact same
// httpapi.SemanticHash/LookupReceipt/ReconcileReceipt replay authority
// internal/delivery/httpapi/projectionrebuild itself dispatches through,
// keyed in part on this command_type string, so an HTTP call and a CLI call
// for "the same command" must hash and replay identically.
const (
	appOpGetProjectionStatus            = "GetProjectionStatus"
	commandTypeRequestProjectionRebuild = "RequestProjectionRebuild"
	appOpGetProjectionRebuildStatus     = "GetProjectionRebuildStatus"
)

// loadPrincipal resolves --principal-config into a validated
// config.LocalPrincipal — the same two-step (LoadLocalPrincipalFile then
// ValidateLocalPrincipal) `aw serve` itself performs (cmd/aw/serve.go).
// Mirrors internal/delivery/cli/adapterbuild and internal/delivery/cli/
// workitem's own identically named/implemented loadPrincipal exactly (this
// framework's own "own package, own duplicated small helper rather than a
// shared one" convention).
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

// usageErrorf builds a cli.UsageError from a formatted message — mirrors
// every sibling leaf package's own identical helper.
func usageErrorf(format string, args ...any) error {
	return cli.UsageError{Err: fmt.Errorf(format, args...)}
}

// parseFlags parses args into fs and wraps a parse failure as a
// cli.UsageError — mirrors every sibling leaf package's own identical
// helper.
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return err
		}
		return cli.UsageError{Err: err}
	}
	return nil
}
