package catalog

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	appcatalog "github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// V11-13 (ADR-044): `aw project rename|archive|restore`, the CLI faces of projectsRename, projectsArchive and projectsRestore. Flags go before the id.

const (
	commandTypeRenameProject  = "RenameProject"
	commandTypeArchiveProject = "ArchiveProject"
	commandTypeRestoreProject = "RestoreProject"
)

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"project", "rename"}, Scope: cli.ScopeProject,
		AppOperation: commandTypeRenameProject, HTTPOperationID: "projectsRename",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"project", "archive"}, Scope: cli.ScopeProject,
		AppOperation: commandTypeArchiveProject, HTTPOperationID: "projectsArchive",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"project", "restore"}, Scope: cli.ScopeProject,
		AppOperation: commandTypeRestoreProject, HTTPOperationID: "projectsRestore",
	})
}

// projectMutation builds the envelope in the project's own scope and dispatches run.
func projectMutation(ctx context.Context, deps Dependencies, stdout io.Writer, projectID, principalConfigPath, idempotencyKey, commandType string,
	expectedVersion uint64, payload []byte, run func(ctx context.Context, cmd ports.Command) (any, error)) error {
	principal, err := loadPrincipal(principalConfigPath)
	if err != nil {
		return err
	}
	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandType, Scope: ports.ProjectScope(projectID),
		NormalizedPayload: payload, ExpectedVersion: expectedVersion, IdempotencyKey: idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})
	dispatched, err := cli.Dispatch(ctx, deps.UoW, envelope.Command, func(ctx context.Context) (any, error) { return run(ctx, envelope.Command) })
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}

// RunProjectRename implements `aw project rename --expected-version <n> --name <name> <projectId>`.
func RunProjectRename(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("project rename", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	expectedVersion := cli.BindExpectedVersionFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	name := fs.String("name", "", "the new project name (at most 200 characters)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if len(fs.Args()) != 1 || strings.TrimSpace(*name) == "" {
		return usageErrorf("usage: aw project rename --expected-version <version> --name <name> <projectId>")
	}
	if *expectedVersion == 0 {
		return usageErrorf("--expected-version is required (the Project version you observed, the CLI equivalent of HTTP's If-Match)")
	}
	projectID := fs.Args()[0]
	return projectMutation(ctx, deps, stdout, projectID, *principalConfigPath, *idempotencyKey, commandTypeRenameProject, *expectedVersion,
		[]byte(fmt.Sprintf(`{"name":%q}`, strings.TrimSpace(*name))),
		func(ctx context.Context, cmd ports.Command) (any, error) {
			return appcatalog.RenameProject(ctx, deps.UoW, cmd, appcatalog.RenameProjectRequest{ProjectID: projectID, Name: *name})
		})
}

// RunProjectArchive implements `aw project archive --expected-version <n> [--reason <text>] <projectId>`.
func RunProjectArchive(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("project archive", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	expectedVersion := cli.BindExpectedVersionFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	reason := fs.String("reason", "", "why the project is archived")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if len(fs.Args()) != 1 {
		return usageErrorf("usage: aw project archive --expected-version <version> [--reason <text>] <projectId>")
	}
	if *expectedVersion == 0 {
		return usageErrorf("--expected-version is required (the Project version you observed, the CLI equivalent of HTTP's If-Match)")
	}
	projectID := fs.Args()[0]
	text := strings.TrimSpace(*reason)
	return projectMutation(ctx, deps, stdout, projectID, *principalConfigPath, *idempotencyKey, commandTypeArchiveProject, *expectedVersion,
		[]byte(fmt.Sprintf(`{"reason":%q}`, text)),
		func(ctx context.Context, cmd ports.Command) (any, error) {
			return appcatalog.ArchiveProject(ctx, deps.UoW, cmd, appcatalog.ArchiveProjectRequest{ProjectID: projectID, Reason: text})
		})
}

// RunProjectRestore implements `aw project restore --expected-version <n> <projectId>`.
func RunProjectRestore(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("project restore", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	expectedVersion := cli.BindExpectedVersionFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if len(fs.Args()) != 1 {
		return usageErrorf("usage: aw project restore --expected-version <version> <projectId>")
	}
	if *expectedVersion == 0 {
		return usageErrorf("--expected-version is required (the Project version you observed, the CLI equivalent of HTTP's If-Match)")
	}
	projectID := fs.Args()[0]
	return projectMutation(ctx, deps, stdout, projectID, *principalConfigPath, *idempotencyKey, commandTypeRestoreProject, *expectedVersion,
		[]byte("{}"),
		func(ctx context.Context, cmd ports.Command) (any, error) {
			return appcatalog.RestoreProject(ctx, deps.UoW, cmd, appcatalog.RestoreProjectRequest{ProjectID: projectID})
		})
}
