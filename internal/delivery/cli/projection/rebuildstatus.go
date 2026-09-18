package projection

import (
	"context"
	"flag"
	"io"

	appprojectionrebuild "github.com/taQuangLing/agent-workflow/internal/app/projectionrebuild"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// RunRebuildStatus implements `aw projection rebuild-status <operationId>`
// — a plain, EXACT, by-ID lookup over
// appprojectionrebuild.GetProjectionRebuildStatus (this task's own
// "Không làm: ... no latest-operation inference" line; that function's own
// doc comment: "it never infers 'the latest' operation"). No --project-id
// flag: see this package's own doc comment (doc.go) for why — the loaded
// operation's own ProjectID field is intrinsic to the row itself, never a
// caller-supplied filter this command could apply on top of it.
func RunRebuildStatus(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("projection rebuild-status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw projection rebuild-status <operationId>")
	}
	operationID := positional[0]

	status, err := appprojectionrebuild.GetProjectionRebuildStatus(ctx, deps.UoW, operationID)
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, status)
}
