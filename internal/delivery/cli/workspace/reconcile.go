package workspace

import (
	"context"
	"flag"
	"io"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workspacereconcile"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"repository-workspace", "reconcile"}, Scope: cli.ScopeProject,
		AppOperation: "RequestWorkspaceReconciliation", HTTPOperationID: "requestWorkspaceReconciliation",
	})
}

// commandTypeRequestWorkspaceReconciliation is byte-for-byte identical to
// the commandType string literal
// internal/delivery/httpapi/workspacereconcile.go's own
// requestWorkspaceReconciliationHandler already passes to
// newWorkspaceCommand — see commandTypeRequestWorkspaceSetRelease's own doc
// comment (release.go) for why this exact match matters.
const commandTypeRequestWorkspaceReconciliation = "RequestWorkspaceReconciliation"

// RunRepositoryWorkspaceReconcile implements `aw repository-workspace
// reconcile <repositoryWorkspaceId> --project-id <projectId>
// --expected-version <n> [--idempotency-key <key>]` — the full
// cli.BuildEnvelope/cli.Dispatch CommandEnvelope flow over
// workspacereconcile.RequestWorkspaceReconciliation, mirroring
// internal/delivery/httpapi/workspacereconcile.go's own
// requestWorkspaceReconciliationHandler exactly. Unlike release, no
// ports.ReleaseEligibilityAuthority is needed:
// workspacereconcile.RequestWorkspaceReconciliation depends on nothing
// beyond ports.UnitOfWork/idsource.Source.
func RunRepositoryWorkspaceReconcile(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("repository-workspace reconcile", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	projectID := cli.BindProjectFlag(fs)
	expectedVersion := cli.BindExpectedVersionFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw repository-workspace reconcile <repositoryWorkspaceId> --project-id <projectId> --expected-version <n>")
	}
	if err := requireProjectID(*projectID); err != nil {
		return err
	}
	repositoryWorkspaceID := positional[0]
	if *expectedVersion == 0 {
		return usageErrorf("--expected-version is required (the RepositoryWorkspace version you observed, the CLI equivalent of HTTP's If-Match)")
	}

	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}

	scope := ports.ProjectScope(*projectID)
	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandTypeRequestWorkspaceReconciliation, Scope: scope,
		NormalizedPayload: []byte("{}"), ExpectedVersion: *expectedVersion,
		IdempotencyKey: *idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})

	dispatched, err := cli.Dispatch(ctx, deps.UOW, envelope.Command, func(ctx context.Context) (any, error) {
		return workspacereconcile.RequestWorkspaceReconciliation(ctx, deps.UOW, deps.IDs, envelope.Command, workspacereconcile.RequestWorkspaceReconciliationRequest{
			RepositoryWorkspaceID: repositoryWorkspaceID, ProjectID: *projectID,
		})
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}
