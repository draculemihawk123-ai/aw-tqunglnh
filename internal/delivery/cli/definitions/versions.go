package definitions

import (
	"context"
	"flag"
	"io"

	appdefinitions "github.com/taQuangLing/agent-workflow/internal/app/definitions"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// RunDefinitionVersions implements `aw definition versions <id> --kind
// <KIND> [--project-id <id>]` — a read-only query over
// appdefinitions.ListVersions. The Definition is authoritatively reloaded
// and scope-checked FIRST (loadDefinitionInScope), mirroring
// internal/delivery/httpapi/definitions/detail.go's own
// listDefinitionVersionsCore: a caller must not be able to enumerate a
// wrong-scope DefinitionID's published version count (or even whether it
// exists) via this command.
func RunDefinitionVersions(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("definition versions", flag.ContinueOnError)
	fs.SetOutput(stderr)
	kindRaw := bindKindFlag(fs)
	projectID := bindProjectFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw definition versions <id> --kind <KIND> [--project-id <projectId>]")
	}
	id := positional[0]
	kind, err := parseKind(*kindRaw)
	if err != nil {
		return err
	}

	routeScope := definitionScopeFromProjectID(*projectID)
	if _, err := loadDefinitionInScope(ctx, deps.UoW, kind, id, routeScope); err != nil {
		return err
	}
	versions, err := appdefinitions.ListVersions(ctx, deps.UoW, kind, id)
	if err != nil {
		return err
	}
	views := make([]versionFieldsView, 0, len(versions))
	for _, v := range versions {
		views = append(views, newVersionFieldsView(v))
	}
	return cli.EncodeQueryResult(stdout, versionListView{Items: views})
}
