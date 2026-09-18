package adapterbuild

import (
	"context"
	"flag"
	"io"

	appadapterbuild "github.com/taQuangLing/agent-workflow/internal/app/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// RunShow implements `aw adapter show <id> [--json]`: a plain read over
// appadapterbuild.GetAdapterBuild, mirroring GET /adapter-builds/{id}'s own
// identical shape (internal/delivery/httpapi/adapterbuild/queries.go). An
// unknown id surfaces ports.ErrAdapterBuildNotFound directly — there is no
// cross-tenant leakage concern for an installation-scoped, single-operator
// CLI invocation the way there is for HTTP's own WriteResourceHidden
// funnel.
func RunShow(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("adapter show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOutput := cli.BindJSONFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw adapter show <id> [--json]")
	}
	id := positional[0]

	build, err := appadapterbuild.GetAdapterBuild(ctx, deps.UoW, id)
	if err != nil {
		return err
	}
	view := newBuildView(build)

	if *jsonOutput {
		return cli.EncodeQueryResult(stdout, view)
	}
	writeHumanBuild(stdout, view)
	return nil
}
