package workspace

import (
	"context"
	"errors"
	"flag"
	"io"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/workspacereconcile"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"repository-workspace", "adopt-head"}, Scope: cli.ScopeProject,
		AppOperation: "AdoptWorkspaceHead", HTTPOperationID: "adoptWorkspaceHead",
	})
}

// commandTypeAdoptWorkspaceHead is byte-for-byte identical to the commandType
// string literal internal/delivery/httpapi/workspaceadopt.go's own
// adoptWorkspaceHeadHandler passes to newWorkspaceCommand — see
// commandTypeRequestWorkspaceSetRelease's own doc comment (release.go) for why
// this exact match matters.
const commandTypeAdoptWorkspaceHead = "AdoptWorkspaceHead"

// RunRepositoryWorkspaceAdoptHead implements `aw repository-workspace
// adopt-head <repositoryWorkspaceId> --project-id <projectId>
// --expected-version <n> [--idempotency-key <key>]` (V11-01): record commits
// made in the worktree outside aw (an IDE, a terminal) as the workspace's
// current revision, so runs started afterwards pin them. The full
// cli.BuildEnvelope/cli.Dispatch CommandEnvelope flow over
// workspacereconcile.AdoptWorkspaceHead, mirroring
// internal/delivery/httpapi/workspaceadopt.go exactly. It reads the worktree's
// HEAD through deps.HeadAuditor, the same Git workspace provider deps.Reader is.
func RunRepositoryWorkspaceAdoptHead(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("repository-workspace adopt-head", flag.ContinueOnError)
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
		return usageErrorf("usage: aw repository-workspace adopt-head <repositoryWorkspaceId> --project-id <projectId> --expected-version <n>")
	}
	if err := requireProjectID(*projectID); err != nil {
		return err
	}
	repositoryWorkspaceID := positional[0]
	if *expectedVersion == 0 {
		return usageErrorf("--expected-version is required (the RepositoryWorkspace version you observed, the CLI equivalent of HTTP's If-Match)")
	}
	if deps.HeadAuditor == nil {
		return errors.New("workspace: this process has no Git workspace provider, so it cannot adopt a worktree HEAD")
	}

	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}

	scope := ports.ProjectScope(*projectID)
	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandTypeAdoptWorkspaceHead, Scope: scope,
		NormalizedPayload: []byte("{}"), ExpectedVersion: *expectedVersion,
		IdempotencyKey: *idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})

	dispatched, err := cli.Dispatch(ctx, deps.UOW, envelope.Command, func(ctx context.Context) (any, error) {
		return workspacereconcile.AdoptWorkspaceHead(ctx,
			workspacereconcile.AdoptWorkspaceHeadDeps{UnitOfWork: deps.UOW, Auditor: deps.HeadAuditor, Matcher: redact.NewMatcher()},
			envelope.Command, workspacereconcile.AdoptWorkspaceHeadRequest{RepositoryWorkspaceID: repositoryWorkspaceID, ProjectID: *projectID})
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}
