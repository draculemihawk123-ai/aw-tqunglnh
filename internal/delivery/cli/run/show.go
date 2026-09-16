package run

import (
	"context"
	"errors"
	"flag"
	"io"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"run", "show"}, Scope: cli.ScopeProject,
		AppOperation: "GetRunDetail", HTTPOperationID: "getRunDetail",
	})
}

// Show implements `aw run show <runId>`: a pure read dispatch over
// runtime.GetRunDetail (run_detail_queries.go) — mirrors
// internal/delivery/httpapi/rundetail's own handleGetRunDetail exactly (one
// query, no pagination, no scope check: GetRunDetail takes only a RunID,
// exactly like that HTTP handler's own route).
func Show(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("run show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	runID := fs.Arg(0)
	if strings.TrimSpace(runID) == "" {
		return errors.New("cli: run show: <runId> argument is required")
	}

	detail, err := runtime.GetRunDetail(ctx, deps.UOW, runID)
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, detail)
}
