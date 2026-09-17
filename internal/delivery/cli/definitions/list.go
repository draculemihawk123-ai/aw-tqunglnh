package definitions

import (
	"context"
	"flag"
	"io"

	appdefinitions "github.com/taQuangLing/agent-workflow/internal/app/definitions"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// RunDefinitionList implements `aw definition list --kind <KIND>
// [--project-id <id>]` — this package's own genuine addition below the CLI
// layer (see doc.go's own top comment and
// internal/app/definitions/queries.go's own ListDefinitions doc comment):
// a read-only query over appdefinitions.ListDefinitions, scoped to global
// (no --project-id) or the named project. --kind is required, exactly
// like every other command in this package that routes between the shared
// definitions table and Workflow's own dedicated tables.
func RunDefinitionList(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("definition list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	kindRaw := bindKindFlag(fs)
	projectID := bindProjectFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return usageErrorf("usage: aw definition list --kind <KIND> [--project-id <projectId>]")
	}
	kind, err := parseKind(*kindRaw)
	if err != nil {
		return err
	}

	scope := definitionScopeFromProjectID(*projectID)
	summaries, err := appdefinitions.ListDefinitions(ctx, deps.UoW, kind, scope)
	if err != nil {
		return err
	}
	views := make([]definitionView, 0, len(summaries))
	for _, s := range summaries {
		views = append(views, newDefinitionView(s.ID, s.Fields))
	}
	return cli.EncodeQueryResult(stdout, definitionListView{Definitions: views})
}
