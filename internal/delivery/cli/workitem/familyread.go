package workitem

import (
	"context"
	"flag"
	"io"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// Two read leaves that close the family half of the parity ledger
// (docs/design/10-v8-alpha-hardening.md V8-12R-01): `aw work-item children`
// and `aw task-family show`, the CLI twins of the HTTP routes
// listChildWorkItems and getTaskFamily. Both are PROJECT-scoped plain reads.
func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"work-item", "children"}, Scope: cli.ScopeProject,
		AppOperation: "ListChildWorkItems", HTTPOperationID: "listChildWorkItems",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"task-family", "show"}, Scope: cli.ScopeProject,
		AppOperation: "GetTaskFamily", HTTPOperationID: "getTaskFamily",
	})
}

// RunWorkItemChildren implements `aw work-item children <workItemId>
// --project-id <projectId>` — workItemId's own DIRECT children (never
// grandchildren; see workapp.ListChildWorkItems), a read over that query
// mirroring internal/delivery/httpapi/workitem's own handleListChildWorkItems.
// It is the list `aw work-item show` does not embed.
func RunWorkItemChildren(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("work-item children", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw work-item children <workItemId> --project-id <projectId>")
	}
	workItemID := positional[0]
	if strings.TrimSpace(workItemID) == "" {
		return usageErrorf("<workItemId> argument is required")
	}
	if strings.TrimSpace(*projectID) == "" {
		return usageErrorf("--project-id is required")
	}

	children, err := workapp.ListChildWorkItems(ctx, deps.UoW, ports.ProjectScope(*projectID), workItemID)
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, workItemListView{Items: children})
}

// RunTaskFamilyShow implements `aw task-family show <familyId> --project-id
// <projectId>` — the authoritative TaskFamily detail (status and ScopeVersion,
// which only ever moves through an approved scope expansion), a read over
// workapp.GetTaskFamily mirroring handleGetTaskFamily.
func RunTaskFamilyShow(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("task-family show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw task-family show <familyId> --project-id <projectId>")
	}
	familyID := positional[0]
	if strings.TrimSpace(familyID) == "" {
		return usageErrorf("<familyId> argument is required")
	}
	if strings.TrimSpace(*projectID) == "" {
		return usageErrorf("--project-id is required")
	}

	detail, err := workapp.GetTaskFamily(ctx, deps.UoW, ports.ProjectScope(*projectID), familyID)
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, detail)
}
