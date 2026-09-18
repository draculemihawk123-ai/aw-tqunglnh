package evidence

import (
	"flag"
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// usageErrorf builds a cli.UsageError (mapped to cli.ExitUsage by a future
// composition root's own cli.ExitCodeFor) from a formatted message — every
// bad-invocation condition in this package (wrong argument count, a
// required flag left empty) returns one of these rather than a bare error,
// mirroring internal/delivery/cli/catalog's own identical helper.
func usageErrorf(format string, args ...any) error {
	return cli.UsageError{Err: fmt.Errorf(format, args...)}
}

// parseFlags parses args into fs and wraps a parse failure as a
// cli.UsageError — the one place every Run* function in this package
// routes its own flag handling through, mirroring
// internal/delivery/cli/catalog's own identical helper. flag.ErrHelp
// (from -h/--help) is returned unwrapped so a future composition root can
// keep treating it as cli.ExitSuccess.
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return err
		}
		return cli.UsageError{Err: err}
	}
	return nil
}

// requireProjectID validates a --project-id flag value non-empty, returning
// a cli.UsageError naming the flag when it is blank — every subcommand in
// this package is project-scoped (there is no installation-scoped leaf
// here), so this precondition is shared by all four.
func requireProjectID(projectID string) error {
	if strings.TrimSpace(projectID) == "" {
		return usageErrorf("--project-id is required")
	}
	return nil
}
