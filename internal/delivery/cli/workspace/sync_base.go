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
		Path: []string{"repository-workspace", "sync-base"}, Scope: cli.ScopeProject,
		AppOperation: commandTypeSyncWorkspaceWithBase, HTTPOperationID: "syncWorkspaceWithBase",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"repository-workspace", "base-status"}, Scope: cli.ScopeProject,
		AppOperation: "GetWorkspaceBaseStatus", HTTPOperationID: "getWorkspaceBaseStatus",
	})
}

// commandTypeSyncWorkspaceWithBase matches internal/delivery/httpapi/workspaceadopt.go's syncWorkspaceWithBaseHandler.
const commandTypeSyncWorkspaceWithBase = "SyncWorkspaceWithBase"

// RunRepositoryWorkspaceSyncBase implements `aw repository-workspace sync-base <repositoryWorkspaceId> --project-id <id>
// --expected-version <n>` (V12-20, ADR-055): fetch the repository's base branch from origin, fast-forward the worktree to it, and
// record the new HEAD. A diverged branch is reported as NEEDS_MERGE and nothing moves. Never pushes.
func RunRepositoryWorkspaceSyncBase(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("repository-workspace sync-base", flag.ContinueOnError)
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
		return usageErrorf("usage: aw repository-workspace sync-base <repositoryWorkspaceId> --project-id <projectId> --expected-version <n>")
	}
	if err := requireProjectID(*projectID); err != nil {
		return err
	}
	if *expectedVersion == 0 {
		return usageErrorf("--expected-version is required (the RepositoryWorkspace version you observed)")
	}
	syncer, _ := deps.HeadAuditor.(ports.WorkspaceBaseSyncer)
	if deps.HeadAuditor == nil || syncer == nil {
		return errors.New("workspace: this process has no Git workspace provider, so it cannot sync a worktree")
	}
	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}
	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandTypeSyncWorkspaceWithBase, Scope: ports.ProjectScope(*projectID),
		NormalizedPayload: []byte("{}"), ExpectedVersion: *expectedVersion,
		IdempotencyKey: *idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})
	dispatched, err := cli.Dispatch(ctx, deps.UOW, envelope.Command, func(ctx context.Context) (any, error) {
		return workspacereconcile.SyncWorkspaceWithBase(ctx, workspacereconcile.SyncWorkspaceWithBaseDeps{
			Adopt:  workspacereconcile.AdoptWorkspaceHeadDeps{UnitOfWork: deps.UOW, Auditor: deps.HeadAuditor, Matcher: redact.NewMatcher()},
			Syncer: syncer,
		}, envelope.Command, workspacereconcile.AdoptWorkspaceHeadRequest{RepositoryWorkspaceID: positional[0], ProjectID: *projectID})
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}

// RunRepositoryWorkspaceBaseStatus implements `aw repository-workspace base-status <repositoryWorkspaceId> --project-id <id>`:
// ahead/behind/merged against the base branch, without fetching.
func RunRepositoryWorkspaceBaseStatus(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("repository-workspace base-status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw repository-workspace base-status <repositoryWorkspaceId> --project-id <projectId>")
	}
	if err := requireProjectID(*projectID); err != nil {
		return err
	}
	syncer, _ := deps.HeadAuditor.(ports.WorkspaceBaseSyncer)
	if syncer == nil {
		return errors.New("workspace: this process has no Git workspace provider")
	}
	status, err := workspacereconcile.GetWorkspaceBaseStatus(ctx, deps.UOW, syncer, *projectID, positional[0])
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, status)
}
