package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"

	"github.com/taQuangLing/agent-workflow/internal/app/forgerelease"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// V12-28 (ADR-060): `aw repository-workspace remote|push|pull-request|merge`, the CLI faces of getWorkspaceRemote, pushWorkspaceBranch,
// createWorkspacePullRequest and mergeWorkspacePullRequest, with the machine's git credential helper and GitHub CLI.

func init() {
	for _, d := range []cli.Descriptor{
		{Path: []string{"repository-workspace", "remote"}, Scope: cli.ScopeProject, AppOperation: "GetWorkspaceRemote", HTTPOperationID: "getWorkspaceRemote"},
		{Path: []string{"repository-workspace", "push"}, Scope: cli.ScopeProject, AppOperation: "PushFamilyBranch", HTTPOperationID: "pushWorkspaceBranch"},
		{Path: []string{"repository-workspace", "pull-request"}, Scope: cli.ScopeProject, AppOperation: "CreateWorkspacePullRequest", HTTPOperationID: "createWorkspacePullRequest"},
		{Path: []string{"repository-workspace", "merge"}, Scope: cli.ScopeProject, AppOperation: "MergeWorkspacePullRequest", HTTPOperationID: "mergeWorkspacePullRequest"},
	} {
		cli.MustRegister(d)
	}
}

func forgeDeps(deps Dependencies) (forgerelease.Deps, error) {
	git, _ := deps.HeadAuditor.(ports.ForgeGit)
	if deps.HeadAuditor == nil || git == nil || deps.Forge == nil {
		return forgerelease.Deps{}, errors.New("workspace: this process has no Git workspace provider or no GitHub CLI client")
	}
	return forgerelease.Deps{UnitOfWork: deps.UOW, Auditor: deps.HeadAuditor, Git: git, Forge: deps.Forge}, nil
}

// forgeCommandTypes are the command types of the HTTP routes (internal/delivery/httpapi/workspaceforge.go), so both replay alike.
var forgeCommandTypes = map[string]string{"push": "PushFamilyBranch", "pull-request": "CreateWorkspacePullRequest", "merge": "MergeWorkspacePullRequest"}

func forgeLeaf(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer, name, usage string,
	bind func(fs *flag.FlagSet) func() any, run func(ctx context.Context, d forgerelease.Deps, cmd ports.Command, projectID, rwID string) (any, error)) error {
	fs := flag.NewFlagSet("repository-workspace "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	projectID := cli.BindProjectFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	payload := bind(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if len(fs.Args()) != 1 {
		return usageErrorf("usage: aw repository-workspace %s", usage)
	}
	if err := requireProjectID(*projectID); err != nil {
		return err
	}
	d, err := forgeDeps(deps)
	if err != nil {
		return err
	}
	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}
	body, err := json.Marshal(payload())
	if err != nil {
		return err
	}
	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: forgeCommandTypes[name], Scope: ports.ProjectScope(*projectID), NormalizedPayload: body,
		IdempotencyKey: *idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})
	dispatched, err := cli.Dispatch(ctx, deps.UOW, envelope.Command, func(ctx context.Context) (any, error) {
		return run(ctx, d, envelope.Command, *projectID, fs.Args()[0])
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result})
}

// RunRepositoryWorkspaceRemote implements `aw repository-workspace remote --project-id <p> <repositoryWorkspaceId>`.
func RunRepositoryWorkspaceRemote(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("repository-workspace remote", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if len(fs.Args()) != 1 {
		return usageErrorf("usage: aw repository-workspace remote --project-id <projectId> <repositoryWorkspaceId>")
	}
	if err := requireProjectID(*projectID); err != nil {
		return err
	}
	d, err := forgeDeps(deps)
	if err != nil {
		return err
	}
	view, err := forgerelease.GetRemote(ctx, d, *projectID, fs.Args()[0])
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, view)
}

// RunRepositoryWorkspacePush implements `aw repository-workspace push --project-id <p> [--remote-branch <name>] <repositoryWorkspaceId>`.
func RunRepositoryWorkspacePush(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	var branch *string
	return forgeLeaf(ctx, deps, args, stdout, stderr, "push", "push --project-id <projectId> [--remote-branch <name>] <repositoryWorkspaceId>",
		func(fs *flag.FlagSet) func() any {
			branch = fs.String("remote-branch", "", "name on origin, only at the first push (default: the family branch)")
			return func() any { return map[string]string{"remoteBranch": *branch} }
		},
		func(ctx context.Context, d forgerelease.Deps, cmd ports.Command, projectID, rwID string) (any, error) {
			return forgerelease.Push(ctx, d, cmd, forgerelease.PushRequest{ProjectID: projectID, RepositoryWorkspaceID: rwID, RemoteBranch: *branch})
		})
}

// RunRepositoryWorkspacePullRequest implements `aw repository-workspace pull-request --project-id <p> --title <t> [--body <b>] [--draft] <id>`.
func RunRepositoryWorkspacePullRequest(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	var title, body *string
	var draft *bool
	return forgeLeaf(ctx, deps, args, stdout, stderr, "pull-request", "pull-request --project-id <projectId> --title <title> [--body <text>] [--draft] <repositoryWorkspaceId>",
		func(fs *flag.FlagSet) func() any {
			title = fs.String("title", "", "pull request title (required)")
			body = fs.String("body", "", "pull request description")
			draft = fs.Bool("draft", false, "open it as a draft (default: ready for review)")
			return func() any { return map[string]any{"title": *title, "body": *body, "draft": *draft} }
		},
		func(ctx context.Context, d forgerelease.Deps, cmd ports.Command, projectID, rwID string) (any, error) {
			return forgerelease.CreatePullRequest(ctx, d, cmd, forgerelease.CreatePullRequestRequest{
				ProjectID: projectID, RepositoryWorkspaceID: rwID, Title: *title, Body: *body, Draft: *draft,
			})
		})
}

// RunRepositoryWorkspaceMerge implements `aw repository-workspace merge --project-id <p> [--method merge|squash|rebase] [--no-ci] <id>`.
func RunRepositoryWorkspaceMerge(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	var method *string
	var noCI *bool
	return forgeLeaf(ctx, deps, args, stdout, stderr, "merge", "merge --project-id <projectId> [--method merge|squash|rebase] [--no-ci] <repositoryWorkspaceId>",
		func(fs *flag.FlagSet) func() any {
			method = fs.String("method", "merge", "merge (a merge commit, default), squash or rebase")
			noCI = fs.Bool("no-ci", false, "confirm that the pull request has no CI (required when it has no check)")
			return func() any { return map[string]any{"method": *method, "acknowledgeNoChecks": *noCI} }
		},
		func(ctx context.Context, d forgerelease.Deps, cmd ports.Command, projectID, rwID string) (any, error) {
			return forgerelease.Merge(ctx, d, cmd, forgerelease.MergeRequest{
				ProjectID: projectID, RepositoryWorkspaceID: rwID, Method: *method, AcknowledgeNoChecks: *noCI,
			})
		})
}
