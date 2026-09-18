package workitem

import (
	"context"
	"flag"
	"io"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// workItemListView wraps a workapp.WorkItemDetail collection in an object
// (rather than a bare top-level JSON array), mirroring
// internal/delivery/httpapi/workitem/workitem_queries.go's own
// workItemListResponse exactly.
type workItemListView struct {
	Items []workapp.WorkItemDetail `json:"items"`
}

// RunWorkItemList implements `aw work-item list --project-id <projectId>` —
// a read-only query over workapp.ListWorkItems: every WorkItem (every Kind,
// every Status) in --project-id's own project, never anything from another
// project (workapp.ListWorkItems' own tx.Work().ListWorkItemsByProject is
// filtered by the stored project_id column, so this can never leak a
// cross-project row into the result — the "subset" half of this task's own
// Verify line).
func RunWorkItemList(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("work-item list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return usageErrorf("usage: aw work-item list --project-id <projectId>")
	}
	if *projectID == "" {
		return usageErrorf("--project-id is required")
	}

	items, err := workapp.ListWorkItems(ctx, deps.UoW, ports.ProjectScope(*projectID))
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, workItemListView{Items: items})
}
