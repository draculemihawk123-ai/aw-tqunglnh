package catalog

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runapp"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// V12-30 (ADR-062): `aw repository run-app show|set` (a repository's Run app profile) and `aw repository-workspace run-app
// status|start|stop` (its application in one worktree, run by `aw worker`). The command types are the HTTP routes', so both replay
// alike.

func init() {
	for _, d := range []cli.Descriptor{
		{Path: []string{"repository", "run-app", "show"}, Scope: cli.ScopeProject, AppOperation: "GetRunAppProfile", HTTPOperationID: "getRunAppProfile"},
		{Path: []string{"repository", "run-app", "set"}, Scope: cli.ScopeProject, AppOperation: "SetRunAppProfile", HTTPOperationID: "setRunAppProfile"},
		{Path: []string{"repository-workspace", "run-app", "status"}, Scope: cli.ScopeProject, AppOperation: "GetRunAppStatus", HTTPOperationID: "getRunAppStatus"},
		{Path: []string{"repository-workspace", "run-app", "start"}, Scope: cli.ScopeProject, AppOperation: "StartRunApp", HTTPOperationID: "startRunApp"},
		{Path: []string{"repository-workspace", "run-app", "stop"}, Scope: cli.ScopeProject, AppOperation: "StopRunApp", HTTPOperationID: "stopRunApp"},
	} {
		cli.MustRegister(d)
	}
}

// RunAppDependencies is what the run-app leaves need.
type RunAppDependencies struct {
	Dependencies
	// Host reads the log tails for status (nil: none shown).
	Host ports.RunAppHost
}

func (d RunAppDependencies) app() runapp.Deps { return runapp.Deps{UnitOfWork: d.UoW, Host: d.Host} }

func runAppQuery(fs *flag.FlagSet, args []string, usage string) (projectID, id string, err error) {
	project := cli.BindProjectFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return "", "", err
	}
	if len(fs.Args()) != 1 || strings.TrimSpace(*project) == "" {
		return "", "", usageErrorf("usage: aw %s", usage)
	}
	return *project, fs.Args()[0], nil
}

// RunRepositoryRunAppShow implements `aw repository run-app show --project-id <p> <repositoryId>`.
func RunRepositoryRunAppShow(ctx context.Context, deps RunAppDependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("repository run-app show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID, repositoryID, err := runAppQuery(fs, args, "repository run-app show --project-id <projectId> <repositoryId>")
	if err != nil {
		return err
	}
	view, err := runapp.GetProfile(ctx, deps.app(), projectID, repositoryID)
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, view)
}

// RunRepositoryRunAppSet implements `aw repository run-app set --project-id <p> --expected-version <n> [--file profile.json]
// <repositoryId>`; the profile JSON is read from --file or stdin.
func RunRepositoryRunAppSet(ctx context.Context, deps RunAppDependencies, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("repository run-app set", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	expected := fs.Uint64("expected-version", 0, "the profile version you read (0 for the first profile)")
	file := cli.BindFileFlag(fs)
	projectID, repositoryID, err := runAppQuery(fs, args, "repository run-app set --project-id <projectId> [--expected-version <n>] [--file <profile.json>] <repositoryId>")
	if err != nil {
		return err
	}
	raw, err := cli.ReadBoundedInput(stdin, *file, 64<<10)
	if err != nil {
		return cli.UsageError{Err: err}
	}
	var profile runapp.Profile
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&profile); err != nil {
		return usageErrorf("the profile is not valid JSON: %v", err)
	}
	if profile, err = runapp.Normalize(profile); err != nil {
		return cli.UsageError{Err: err}
	}
	payload, err := json.Marshal(struct {
		RepositoryID    string         `json:"repositoryId"`
		Profile         runapp.Profile `json:"profile"`
		ExpectedVersion uint64         `json:"expectedVersion"`
	}{repositoryID, profile, *expected})
	if err != nil {
		return err
	}
	return projectMutation(ctx, deps.Dependencies, stdout, projectID, *principalConfigPath, *idempotencyKey, "SetRunAppProfile", 0, payload,
		func(ctx context.Context, cmd ports.Command) (any, error) {
			return runapp.SetProfile(ctx, deps.app(), cmd, projectID, repositoryID, profile, *expected)
		})
}

// RunRepositoryWorkspaceRunAppStatus implements `aw repository-workspace run-app status --project-id <p> <repositoryWorkspaceId>`.
func RunRepositoryWorkspaceRunAppStatus(ctx context.Context, deps RunAppDependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("repository-workspace run-app status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID, rwID, err := runAppQuery(fs, args, "repository-workspace run-app status --project-id <projectId> <repositoryWorkspaceId>")
	if err != nil {
		return err
	}
	st, err := runapp.GetStatus(ctx, deps.app(), projectID, rwID)
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, st)
}

func runAppSessionLeaf(ctx context.Context, deps RunAppDependencies, args []string, stdout, stderr io.Writer, action, commandType string,
	run func(ctx context.Context, d runapp.Deps, cmd ports.Command, projectID, rwID string) (runapp.SessionView, error)) error {
	fs := flag.NewFlagSet("repository-workspace run-app "+action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	projectID, rwID, err := runAppQuery(fs, args, fmt.Sprintf("repository-workspace run-app %s --project-id <projectId> <repositoryWorkspaceId>", action))
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]string{"repositoryWorkspaceId": rwID})
	return projectMutation(ctx, deps.Dependencies, stdout, projectID, *principalConfigPath, *idempotencyKey, commandType, 0, payload,
		func(ctx context.Context, cmd ports.Command) (any, error) {
			return run(ctx, deps.app(), cmd, projectID, rwID)
		})
}

// RunRepositoryWorkspaceRunAppStart implements `aw repository-workspace run-app start --project-id <p> <repositoryWorkspaceId>`: a
// running `aw worker` takes it within seconds.
func RunRepositoryWorkspaceRunAppStart(ctx context.Context, deps RunAppDependencies, args []string, stdout, stderr io.Writer) error {
	return runAppSessionLeaf(ctx, deps, args, stdout, stderr, "start", "StartRunApp", runapp.Start)
}

// RunRepositoryWorkspaceRunAppStop implements `aw repository-workspace run-app stop --project-id <p> <repositoryWorkspaceId>`.
func RunRepositoryWorkspaceRunAppStop(ctx context.Context, deps RunAppDependencies, args []string, stdout, stderr io.Writer) error {
	return runAppSessionLeaf(ctx, deps, args, stdout, stderr, "stop", "StopRunApp", runapp.Stop)
}
