package catalog

import (
	"context"
	"flag"
	"io"

	appcatalog "github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// RunComponentList implements `aw component list <projectId>` — "catalog
// component đã được repository onboarding/probe discover"
// (docs/design/01-system-design.md §6's own API sketch), the "component
// discovery" Verify bullet this task's own brief names: every Component
// this query can ever return was inserted by V3-02's own onboarding-probe
// worker (internal/app/repositoryprobe's finishActive), never by anything
// reachable from this package — this leaf exposes no create/mutate
// command for a Component at all (V6-15D's own "Không làm: no generic
// probe/component creation" line). The Project is reloaded first, exactly
// like RunRepositoryList, so a nonexistent projectId reports a real
// ports.ErrPersistenceNotFound rather than a silently empty list.
func RunComponentList(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("component list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw component list <projectId>")
	}
	projectID := positional[0]

	if _, err := appcatalog.GetProject(ctx, deps.UoW, ports.ProjectScope(projectID), projectID); err != nil {
		return err
	}
	components, err := appcatalog.ListComponents(ctx, deps.UoW, projectID)
	if err != nil {
		return err
	}
	views := make([]componentView, 0, len(components))
	for _, c := range components {
		views = append(views, newComponentView(c))
	}
	return cli.EncodeQueryResult(stdout, componentListView{Components: views})
}
