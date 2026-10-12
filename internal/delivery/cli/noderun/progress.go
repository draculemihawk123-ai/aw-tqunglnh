package noderun

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// V11-11 (ADR-042): `aw node-run show <nodeRunId>` and `aw node-run events <attemptId> [--follow]`, the CLI faces of getNodeRunDetail and
// listAttemptEvents. Both only read.

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"node-run", "show"}, Scope: cli.ScopeProject,
		AppOperation: "GetNodeRunDetail", HTTPOperationID: "getNodeRunDetail",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"node-run", "events"}, Scope: cli.ScopeProject,
		AppOperation: "ListAttemptEvents", HTTPOperationID: "listAttemptEvents",
	})
}

// Show implements `aw node-run show <nodeRunId>`: what the node was asked to do, what it is doing, what came of it, and why it failed.
func Show(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("node-run show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	nodeRunID := fs.Arg(0)
	if strings.TrimSpace(nodeRunID) == "" {
		return errors.New("cli: node-run show: <nodeRunId> argument is required")
	}
	detail, err := runtime.GetNodeRunDetail(ctx, deps.UOW, deps.ArtifactStore, deps.Matcher, nodeRunID)
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, detail)
}

// followPoll is how often --follow asks for new events.
const followPoll = 2 * time.Second

// Events implements `aw node-run events <attemptId> [--cursor <seq>] [--limit <n>] [--follow]`. Without --follow it prints one page (the
// result carries nextSequence to pass as --cursor). With --follow it prints one JSON event per line, as they happen, and returns when the
// attempt is finished and everything has been printed.
func Events(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("node-run events", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cursor := fs.Uint64("cursor", 0, "print events after this sequence (the previous page's nextSequence)")
	limit := fs.Int("limit", 0, "events per page (default 100, max 500)")
	follow := fs.Bool("follow", false, "keep printing new events, one JSON object per line, until the attempt is finished")
	if err := fs.Parse(args); err != nil {
		return err
	}
	attemptID := fs.Arg(0)
	if strings.TrimSpace(attemptID) == "" {
		return errors.New("cli: node-run events: <attemptId> argument is required")
	}
	if !*follow {
		page, err := runtime.ListAttemptEvents(ctx, deps.UOW, deps.Matcher, attemptID, *cursor, *limit)
		if err != nil {
			return err
		}
		return cli.EncodeQueryResult(stdout, page)
	}
	sleep := deps.Sleep
	if sleep == nil {
		sleep = cli.DefaultSleeper
	}
	encoder := json.NewEncoder(stdout)
	after := *cursor
	for {
		page, err := runtime.ListAttemptEvents(ctx, deps.UOW, deps.Matcher, attemptID, after, *limit)
		if err != nil {
			return err
		}
		for _, event := range page.Events {
			if err := encoder.Encode(event); err != nil {
				return err
			}
		}
		after = page.NextSequence
		if page.HasMore {
			continue
		}
		if page.Finished {
			return nil
		}
		if err := sleep(ctx, followPoll); err != nil {
			return err
		}
	}
}
