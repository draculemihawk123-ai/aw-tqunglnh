package adapterbuild

import (
	"context"
	"flag"
	"io"

	appadapterbuild "github.com/taQuangLing/agent-workflow/internal/app/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// RunList implements `aw adapter list [--json]`: a plain, unbounded,
// installation-scoped query over appadapterbuild.ListAdapterBuilds — no
// filter/sort/cursor, mirrors GET /adapter-builds' own identical shape
// (internal/delivery/httpapi/adapterbuild/queries.go). No idempotency key
// or CommandEnvelope at all: a plain read has neither (output.go's own
// EncodeQueryResult doc comment draws the identical distinction).
func RunList(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("adapter list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOutput := cli.BindJSONFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return usageErrorf("usage: aw adapter list [--json]")
	}

	builds, err := appadapterbuild.ListAdapterBuilds(ctx, deps.UoW)
	if err != nil {
		return err
	}
	views := make([]buildView, len(builds))
	for i, b := range builds {
		views[i] = newBuildView(b)
	}
	list := buildListView{Builds: views}

	if *jsonOutput {
		return cli.EncodeQueryResult(stdout, list)
	}
	writeHumanBuildList(stdout, list)
	return nil
}
