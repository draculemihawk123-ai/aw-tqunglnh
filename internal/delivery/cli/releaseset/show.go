package releaseset

import (
	"context"
	"flag"
	"io"

	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// RunShow implements `aw release-set show <releaseSetId>` — a read-only
// query over workapp.GetReleaseSet: the authoritative ReleaseSetDetail
// (State/ContentHash/Version and every per-repository entry), exactly
// mirroring internal/delivery/httpapi/releaseset's own handleGetReleaseSet.
// Deliberately no --project-id flag: workapp.GetReleaseSet takes no scope
// parameter of its own (release_set_queries.go's own doc comment — every
// existing caller derives/checks project scope itself, the way
// internal/delivery/httpapi/releaseset's own loadReleaseSetForUpdate does
// for the HTTP layer), and this task's own command surface names no
// --project-id flag for `show` either; a caller who wants the owning
// project reads it straight off this leaf's own result.ProjectID field.
func RunShow(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("release-set show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw release-set show <releaseSetId>")
	}
	releaseSetID := positional[0]

	detail, err := workapp.GetReleaseSet(ctx, deps.UoW, releaseSetID)
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, detail)
}
