package workspace

import (
	"context"
	"flag"
	"io"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/workspacestate"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"repository-workspace", "show"}, Scope: cli.ScopeProject,
		AppOperation: "GetRepositoryWorkspaceState", HTTPOperationID: "getRepositoryWorkspaceState",
	})
}

// RunRepositoryWorkspaceShow implements `aw repository-workspace show
// <repositoryWorkspaceId> --project-id <projectId>` — one RepositoryWorkspace's
// own state, lease, fence and quarantine, a read-only query over
// workspacestate.GetRepositoryWorkspaceState mirroring
// internal/delivery/httpapi's own getRepositoryWorkspaceStateHandler. It is
// the single-workspace twin of `aw workspace-set show` (which lists every
// RepositoryWorkspace of a family): an operator who follows a reconcile or a
// quarantine to ONE workspace id reads that workspace directly instead of
// fishing it out of the set.
//
// The result is this package's own repositoryWorkspaceStateResult — the same
// delivery-owned DTO `workspace-set show` nests per workspace — so both
// commands describe a workspace identically. Errors
// (ports.ErrPersistenceNotFound, workspacestate.ErrScopeMismatch) are
// returned verbatim, like `workspace-set show`: this is a plain state read
// that never touches Git or the filesystem, so nothing adapter-internal needs
// hiding.
func RunRepositoryWorkspaceShow(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("repository-workspace show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw repository-workspace show <repositoryWorkspaceId> --project-id <projectId>")
	}
	if err := requireProjectID(*projectID); err != nil {
		return err
	}
	repositoryWorkspaceID := positional[0]
	if strings.TrimSpace(repositoryWorkspaceID) == "" {
		return usageErrorf("<repositoryWorkspaceId> argument is required")
	}

	state, err := workspacestate.GetRepositoryWorkspaceState(ctx, deps.UOW, workspacestate.GetRepositoryWorkspaceStateRequest{
		ProjectID: *projectID, RepositoryWorkspaceID: repositoryWorkspaceID,
	})
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, toRepositoryWorkspaceStateResult(state))
}
