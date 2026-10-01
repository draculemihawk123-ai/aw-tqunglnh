package workitem

import (
	"context"
	"flag"
	"io"
	"strings"

	kanbanapp "github.com/taQuangLing/agent-workflow/internal/app/kanban"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// The two PROJECTED work-item reads — the CLI twins of the HTTP routes
// listWorkItemKanban and getWorkItemProjectedDetail, closing the last two rows
// of the parity ledger (docs/design/10-v8-alpha-hardening.md V8-12R-01).
// `work-item list` / `work-item show` stay the AUTHORITATIVE reads (straight
// off the WorkItem rows); these two read the board projection (cards with
// blocker counts, active run, repository badges) and say how fresh it is.
func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"work-item", "kanban"}, Scope: cli.ScopeProject,
		AppOperation: "ListWorkItemKanban", HTTPOperationID: "listWorkItemKanban",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"work-item", "detail"}, Scope: cli.ScopeProject,
		AppOperation: "GetWorkItemProjectedDetail", HTTPOperationID: "getWorkItemProjectedDetail",
	})
}

// RunWorkItemKanban implements `aw work-item kanban --project-id <projectId>
// [--status <STATUS>]... [--family-id <familyId>]` — the project's board, a
// read over kanbanapp.ListWorkItemKanban, the same operation GET
// /projects/{projectId}/work-items/kanban serves. The result is
// {"items": [...cards], "freshness": {generation, asOfJournalPosition,
// status}}: card fields are PROJECTED (possibly stale) and `freshness` is how
// stale — a STALE board is a real answer, not an error.
//
// --status may repeat (a card matches any of them); values are trimmed and
// case-insensitive. Unlike the HTTP route this leaf never paginates and has no
// cursor: the board is one bounded project view, `--status`/`--family-id`
// narrow it, and a signed cursor would be meaningless across CLI processes.
// The whole board is read in ONE transaction, so it is a consistent snapshot.
func RunWorkItemKanban(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("work-item kanban", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	var statuses []string
	fs.Func("status", "only cards in this status (repeatable)", func(v string) error {
		statuses = append(statuses, v)
		return nil
	})
	familyID := fs.String("family-id", "", "only cards of this task family")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return usageErrorf("usage: aw work-item kanban --project-id <projectId> [--status <STATUS>]... [--family-id <familyId>]")
	}
	if strings.TrimSpace(*projectID) == "" {
		return usageErrorf("--project-id is required")
	}

	page, err := kanbanapp.ListWorkItemKanban(ctx, deps.UoW, kanbanapp.ListRequest{
		ProjectID: *projectID, Filter: kanbanapp.NormalizeFilter(statuses, *familyID),
	})
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, page)
}

// RunWorkItemDetail implements `aw work-item detail <workItemId> --project-id
// <projectId>` — one WorkItem's projected card next to its FRESH authoritative
// readiness, a read over kanbanapp.GetWorkItemProjectedDetail, the same
// operation GET /work-items/{workItemId}/detail serves. The result is
// {"card": ..., "readiness": ..., "freshness": ...}: `card` is projected
// (possibly stale — see `freshness`); `readiness` is recomputed from the real
// WorkItem on every call and is the only part to act on (its version is what
// `work-item mark-ready --expected-version` takes). --project-id is checked
// against the WorkItem's own stored project: another project's id reports the
// same hidden-resource error as an unknown one, never a leaked row.
//
// The HTTP route's advisory validActions list is not part of this result: it
// exists so a browser can populate its next request's If-Match, and the CLI
// takes --expected-version from readiness directly.
func RunWorkItemDetail(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("work-item detail", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw work-item detail <workItemId> --project-id <projectId>")
	}
	workItemID := positional[0]
	if strings.TrimSpace(workItemID) == "" {
		return usageErrorf("<workItemId> argument is required")
	}
	if strings.TrimSpace(*projectID) == "" {
		return usageErrorf("--project-id is required")
	}

	detail, err := kanbanapp.GetWorkItemProjectedDetail(ctx, deps.UoW, kanbanapp.DetailRequest{
		WorkItemID: workItemID, ProjectID: *projectID,
	})
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, detail)
}
