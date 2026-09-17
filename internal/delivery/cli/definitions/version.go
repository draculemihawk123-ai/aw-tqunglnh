package definitions

import (
	"context"
	"flag"
	"io"

	appdefinitions "github.com/taQuangLing/agent-workflow/internal/app/definitions"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// RunVersionShow implements `aw version show <versionId>
// [--project-id <id>]` — a read-only query over
// appdefinitions.LoadAnyVersion, authoritatively pinned to routeScope via
// loadVersionInScope (helpers.go). Deliberately no --kind flag: a
// VersionID is already globally unique and self-describing (mirrors
// internal/delivery/httpapi/definitions/routes.go's own doc comment for
// why its own getDefinitionVersion/diffDefinitionVersions routes carry no
// {kind} path segment either). This is `aw definition show`'s own "pins to
// an EXACT version, never latest by implication" counterpart — the
// versionFieldsView it returns names the queried VersionID's own immutable
// content (CanonicalSource/CompiledHash/...), never a "current" or
// "latest" alias that could silently resolve to a different Version on a
// later call.
func RunVersionShow(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("version show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := bindProjectFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw version show <versionId> [--project-id <projectId>]")
	}
	versionID := positional[0]

	routeScope := definitionScopeFromProjectID(*projectID)
	version, err := loadVersionInScope(ctx, deps.UoW, versionID, routeScope)
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, newVersionFieldsView(version))
}

// RunVersionDiff implements `aw version diff <versionIdA> <versionIdB>
// [--project-id <id>]` — a read-only query composing loadVersionInScope
// (twice, once per operand — V6-05's own "diff operands phải cùng scope"
// rule, made concrete: checking each independently against THIS
// invocation's own single fixed routeScope is strictly stronger than a
// pairwise A==B comparison, since it also refuses either operand
// belonging to some OTHER scope neither this invocation's own
// --project-id nor the other operand names) with
// appdefinitions.DiffVersions — the identical comparison
// internal/delivery/httpapi/definitions's own diff route now calls too
// (promoted from that package by this same task; see
// internal/app/definitions/diff.go's own doc comment), so `aw version
// diff`'s own output is byte-for-byte the same shape as
// GET .../definitions/versions/diff's own response body.
func RunVersionDiff(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("version diff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := bindProjectFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 2 {
		return usageErrorf("usage: aw version diff <versionIdA> <versionIdB> [--project-id <projectId>]")
	}
	versionIDA, versionIDB := positional[0], positional[1]

	routeScope := definitionScopeFromProjectID(*projectID)
	a, err := loadVersionInScope(ctx, deps.UoW, versionIDA, routeScope)
	if err != nil {
		return err
	}
	b, err := loadVersionInScope(ctx, deps.UoW, versionIDB, routeScope)
	if err != nil {
		return err
	}
	if a.Kind() != b.Kind() {
		return usageErrorf("version %s (kind %s) and version %s (kind %s) are not the same Kind — diff requires two versions of the same Kind", versionIDA, a.Kind(), versionIDB, b.Kind())
	}

	return cli.EncodeQueryResult(stdout, appdefinitions.DiffVersions(a, b))
}
