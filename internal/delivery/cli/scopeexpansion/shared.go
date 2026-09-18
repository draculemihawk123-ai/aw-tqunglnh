package scopeexpansion

import (
	"flag"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// loadPrincipal resolves --principal-config into a validated
// config.LocalPrincipal — the SAME two-step (LoadLocalPrincipalFile then
// ValidateLocalPrincipal) `aw serve` itself performs (cmd/aw/serve.go),
// applied here per-invocation since a one-shot `aw` process has no
// long-lived startup phase separate from the command itself. Deliberately
// duplicated (not exported from a shared location) — the same "sibling
// packages duplicate a small helper" discipline
// internal/delivery/httpapi/rundetail/fixture_test.go's own doc comment
// already establishes, applied here to another sibling leaf-command
// package.
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
// mirrors internal/delivery/cli/definitions's own usageErrorf exactly.
func usageErrorf(format string, args ...any) error {
	return cli.UsageError{Err: fmt.Errorf(format, args...)}
}

// parseFlags mirrors internal/delivery/cli/definitions's own parseFlags
// exactly: the one place every command's own flag handling goes through,
// wrapping a parse failure as a cli.UsageError and passing flag.ErrHelp
// through unwrapped.
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return err
		}
		return cli.UsageError{Err: err}
	}
	return nil
}
