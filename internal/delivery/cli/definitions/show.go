package definitions

import (
	"context"
	"flag"
	"io"

	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// RunDefinitionShow implements `aw definition show <id> --kind <KIND>
// [--project-id <id>]` — a read-only query over
// appdefinitions.GetDefinition, authoritatively pinned to routeScope (the
// scope THIS invocation itself derived from --project-id): both "no such
// Definition" and "it exists, but in a different scope than the one
// named" report the identical ErrDefinitionNotFound (loadDefinitionInScope,
// helpers.go) — V6-15E's own "scope negative matrix" Verify bullet made
// concrete, mirroring internal/delivery/httpapi/definitions's own
// leakage-normalized 404 for the identical two cases.
func RunDefinitionShow(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("definition show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	kindRaw := bindKindFlag(fs)
	projectID := bindProjectFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw definition show <id> --kind <KIND> [--project-id <projectId>]")
	}
	id := positional[0]
	kind, err := parseKind(*kindRaw)
	if err != nil {
		return err
	}

	routeScope := definitionScopeFromProjectID(*projectID)
	fields, err := loadDefinitionInScope(ctx, deps.UoW, kind, id, routeScope)
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, newDefinitionView(id, fields))
}
