package releaseset

import (
	"context"
	"flag"
	"io"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// releaseSetListView wraps a workapp.ReleaseSetDetail collection in an
// object (rather than a bare top-level JSON array), mirroring
// internal/delivery/httpapi/releaseset/release_set_queries.go's own
// releaseSetListResponse exactly.
type releaseSetListView struct {
	Items []workapp.ReleaseSetDetail `json:"items"`
}

// RunList implements
// `aw release-set list <familyId> --project-id <projectId>` — a read-only
// query over workapp.ListReleaseSetsForFamily: every ReleaseSet ever
// created for <familyId>, ordered by (CreatedAt, ID). The family named by
// <familyId> is reloaded via workapp.GetTaskFamily FIRST — the same
// "reload the route's real target, scoped" discipline
// internal/delivery/httpapi/releaseset's own handleListReleaseSetsForFamily
// already establishes for this identical path shape, so a familyId
// belonging to a different real project reports the same not-found-shaped
// error GetTaskFamily's own doc comment describes, never a leaked
// cross-project row.
func RunList(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("release-set list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw release-set list <familyId> --project-id <projectId>")
	}
	familyID := positional[0]
	if *projectID == "" {
		return usageErrorf("--project-id is required")
	}

	scope := ports.ProjectScope(*projectID)
	if _, err := workapp.GetTaskFamily(ctx, deps.UoW, scope, familyID); err != nil {
		return err
	}

	items, err := workapp.ListReleaseSetsForFamily(ctx, deps.UoW, familyID)
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, releaseSetListView{Items: items})
}
