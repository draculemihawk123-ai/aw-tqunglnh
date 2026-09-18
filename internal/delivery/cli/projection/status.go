package projection

import (
	"context"
	"flag"
	"io"

	appprojectionrebuild "github.com/taQuangLing/agent-workflow/internal/app/projectionrebuild"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// RunStatus implements
// `aw projection status --project-id <projectId> --projection-name <name>`
// — a plain read over appprojectionrebuild.GetProjectionStatus, mirroring
// GET /projects/{id}/projection?name=<projectionName>'s own identical shape
// (internal/delivery/httpapi/projectionrebuild/status.go). This never fails
// for "no generation built yet" — an unbuilt projection is a real, expected
// STALE freshness result (see GetProjectionStatus's own doc comment), never
// a command failure; only a genuinely unknown ProjectID or a missing
// required flag is.
func RunStatus(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("projection status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	projectionName := fs.String("projection-name", "", "projection name to query (required)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return usageErrorf("usage: aw projection status --project-id <projectId> --projection-name <name>")
	}
	if *projectID == "" {
		return usageErrorf("--project-id is required")
	}
	if *projectionName == "" {
		return usageErrorf("--projection-name is required")
	}

	result, err := appprojectionrebuild.GetProjectionStatus(ctx, deps.UoW, appprojectionrebuild.ProjectionStatusRequest{
		ProjectID: *projectID, ProjectionName: *projectionName,
	})
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, result)
}
