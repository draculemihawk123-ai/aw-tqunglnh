package workitem

import (
	"context"
	"flag"
	"io"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// RunWorkItemReadiness implements
// `aw work-item readiness <workItemId> --project-id <projectId>` — a
// read-only dispatch straight to workapp.ExplainWorkItemReadiness, this
// task's own "recompute fresh, never trust a projection" primitive: that
// function reloads the WorkItem's own real, current row and re-runs the
// real workdomain.ValidateReadinessGate validator against it every single
// call — there is no projection table, cache or stored-status shortcut this
// subcommand could take instead, and it never invents one (this package's
// own doc comment).
func RunWorkItemReadiness(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("work-item readiness", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw work-item readiness <workItemId> --project-id <projectId>")
	}
	if *projectID == "" {
		return usageErrorf("--project-id is required")
	}
	workItemID := positional[0]

	readiness, err := workapp.ExplainWorkItemReadiness(ctx, deps.UoW, ports.ProjectScope(*projectID), workItemID)
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, readiness)
}
