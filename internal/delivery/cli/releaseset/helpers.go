package releaseset

import (
	"flag"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// Command-type constants for this package's own four
// cli.BuildEnvelope/cli.Dispatch-shaped mutations. Each value is
// byte-for-byte identical to the commandType string literal
// internal/delivery/httpapi/releaseset's own handlers already pass to
// prepareCreateCommand/prepareUpdateCommand (release_set_commands.go,
// local_commit_commands.go) — the same hard requirement
// internal/delivery/cli/workitem/helpers.go's own identical constant block
// documents: an HTTP call and a CLI call for what should be "the same
// command" must hash and replay identically via the shared
// httpapi.SemanticHash/LookupReceipt/ReconcileReceipt authority, which only
// holds if both delivery mechanisms use the identical literal.
const (
	commandTypeCreateReleaseSet             = "CreateReleaseSet"
	commandTypeSealReleaseSet               = "SealReleaseSet"
	commandTypeAbandonReleaseSet            = "AbandonReleaseSet"
	commandTypeRequestReleaseSetLocalCommit = "RequestReleaseSetLocalCommit"
)

// AppOperation constants for this package's own two query leaves —
// descriptor.go's own "fourth column of ADR-028's eventual inventory". Each
// name mirrors the internal/app/work function it calls directly
// (release_set_queries.go's ListReleaseSetsForFamily/GetReleaseSet).
const (
	appOpListReleaseSetsForFamily = "ListReleaseSetsForFamily"
	appOpGetReleaseSet            = "GetReleaseSet"
)

// loadPrincipal resolves --principal-config into a validated
// config.LocalPrincipal — the same two-step (LoadLocalPrincipalFile then
// ValidateLocalPrincipal) `aw serve` itself performs (cmd/aw/serve.go).
// Deliberately duplicated rather than exported from a shared location —
// internal/delivery/cli/run/shared.go's own doc comment on loadPrincipal
// explains why sibling leaf-command packages duplicate this small helper
// rather than share one.
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

// usageErrorf builds a cli.UsageError from a formatted message — every
// bad-invocation condition in this package (wrong argument count, a
// required flag left empty, a malformed --file/stdin body) returns one of
// these rather than a bare error, mirroring
// internal/delivery/cli/workitem/helpers.go's own identical helper.
func usageErrorf(format string, args ...any) error {
	return cli.UsageError{Err: fmt.Errorf(format, args...)}
}

// parseFlags parses args into fs and wraps a parse failure as a
// cli.UsageError — mirroring
// internal/delivery/cli/workitem/helpers.go's own identical helper.
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return err
		}
		return cli.UsageError{Err: err}
	}
	return nil
}
