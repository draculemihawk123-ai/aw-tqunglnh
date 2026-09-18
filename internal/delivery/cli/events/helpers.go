package events

import (
	"flag"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

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
