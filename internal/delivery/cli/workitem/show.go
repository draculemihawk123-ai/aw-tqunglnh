package workitem

import (
	"context"
	"flag"
	"io"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// RunWorkItemShow implements
// `aw work-item show <workItemId> --project-id <projectId>` — a read-only
// query over workapp.GetWorkItem: the authoritative (non-projected)
// WorkItem detail, scope-checked against --project-id (a workItemId
// belonging to a different real project reports the same
// ports.ErrScopeMismatch/not-found-shaped error GetWorkItem's own doc
// comment describes, never a leaked cross-project row).
func RunWorkItemShow(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("work-item show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw work-item show <workItemId> --project-id <projectId>")
	}
	if *projectID == "" {
		return usageErrorf("--project-id is required")
	}
	workItemID := positional[0]

	detail, err := workapp.GetWorkItem(ctx, deps.UoW, ports.ProjectScope(*projectID), workItemID)
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, detail)
}
