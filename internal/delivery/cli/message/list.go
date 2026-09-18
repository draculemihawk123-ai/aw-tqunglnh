package message

import (
	"context"
	"flag"
	"io"

	appmessage "github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// RunMessageList implements `aw message list <workItemId> --project-id
// <id>`: a plain read over appmessage.ListMessages — no idempotency key or
// CommandEnvelope at all, mirroring GET .../messages' own handleListMessages
// (internal/delivery/httpapi/message/list.go). workItemID's own owning
// WorkItem is reloaded via reloadWorkItem FIRST, unconditionally — the
// identical "reload the route's own authoritative target and confirm its
// real project scope" discipline every handler in that HTTP package already
// establishes.
//
// Unlike that HTTP route, this leaf never paginates: appmessage.ListMessages
// itself already returns the full, already-Sequence-ordered slice for one
// WorkItem (that function's own doc comment: task chat is "bounded, typed
// content"), and this task's own "Command surface to build" line asks for a
// plain `aw message list <workItemId>`, with no --cursor/--limit flag named
// — a genuine, considered scope narrowing versus the HTTP route's own
// cursor-paginated response, not an oversight.
func RunMessageList(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("message list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw message list <workItemId> --project-id <projectId>")
	}
	workItemID := positional[0]

	if _, err := reloadWorkItem(ctx, deps.UnitOfWork, *projectID, workItemID); err != nil {
		return err
	}

	messages, err := appmessage.ListMessages(ctx, deps.UnitOfWork, workItemID)
	if err != nil {
		return err
	}
	views := make([]messageView, 0, len(messages))
	for _, m := range messages {
		views = append(views, newMessageView(m))
	}
	return cli.EncodeQueryResult(stdout, messageListView{Items: views})
}
