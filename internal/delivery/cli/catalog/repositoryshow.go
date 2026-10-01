package catalog

import (
	"context"
	"flag"
	"io"
	"strings"

	appcatalog "github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

const appOpGetRepository = "GetRepository"

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"repository", "show"}, Scope: cli.ScopeProject,
		AppOperation: appOpGetRepository, HTTPOperationID: "repositoriesGet",
	})
}

// RunRepositoryShow implements `aw repository show <repositoryId>` — one
// Repository's own status and Version (the value `repository retry-probe
// --expected-version` takes), a read-only query over appcatalog.GetRepository
// mirroring internal/delivery/httpapi/catalog's own getRepository. Like
// `repository onboarding`, it reaches its target by RepositoryID alone: the
// Repository's own ProjectID is learned FROM the result, never supplied by the
// caller. The view is the very repositoryView `repository list` emits per
// element, so the two commands describe a repository identically.
func RunRepositoryShow(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("repository show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw repository show <repositoryId>")
	}
	repositoryID := positional[0]
	if strings.TrimSpace(repositoryID) == "" {
		return usageErrorf("<repositoryId> argument is required")
	}

	repo, err := appcatalog.GetRepository(ctx, deps.UoW, repositoryID)
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, newRepositoryView(repo))
}
