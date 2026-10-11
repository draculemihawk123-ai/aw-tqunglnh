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

// V11-12 (ADR-043): `aw repository update|disable|delete|check`, the CLI faces of the HTTP routes repositoriesUpdate, repositoriesDisable,
// repositoriesDelete and projectRepositoriesCheck.

const (
	commandTypeUpdateRepository  = "UpdateRepository"
	commandTypeDisableRepository = "DisableRepository"
	commandTypeDeleteRepository  = "DeleteRepository"
	appOpCheckRepository         = "CheckRepository"
)

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"repository", "update"}, Scope: cli.ScopeProject,
		AppOperation: commandTypeUpdateRepository, HTTPOperationID: "repositoriesUpdate",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"repository", "disable"}, Scope: cli.ScopeProject,
		AppOperation: commandTypeDisableRepository, HTTPOperationID: "repositoriesDisable",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"repository", "delete"}, Scope: cli.ScopeProject,
		AppOperation: commandTypeDeleteRepository, HTTPOperationID: "repositoriesDelete",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"repository", "check"}, Scope: cli.ScopeProject,
		AppOperation: appOpCheckRepository, HTTPOperationID: "projectRepositoriesCheck",
	})
}

// repositoryMutation is what update, disable and delete share: load the repository (its project is the scope), build the envelope, dispatch.
func repositoryMutation(ctx context.Context, deps Dependencies, stdout io.Writer, repositoryID, principalConfigPath, idempotencyKey, commandType string,
	expectedVersion uint64, payload []byte, run func(ctx context.Context, cmd ports.Command, projectID string) (any, error)) error {
	repo, err := appcatalog.GetRepository(ctx, deps.UoW, repositoryID)
	if err != nil {
		return err
	}
	principal, err := loadPrincipal(principalConfigPath)
	if err != nil {
		return err
	}
	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandType, Scope: ports.ProjectScope(string(repo.ProjectID)),
		NormalizedPayload: payload, ExpectedVersion: expectedVersion, IdempotencyKey: idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})
	dispatched, err := cli.Dispatch(ctx, deps.UoW, envelope.Command, func(ctx context.Context) (any, error) {
		return run(ctx, envelope.Command, string(repo.ProjectID))
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}

// RunRepositoryUpdate implements `aw repository update --expected-version <n> [--remote-locator …] [--default-ref …] [--name …] <id>` (flags first: the flag parser stops at the first argument): correct a BLOCKED
// repository's registration and probe it again. A flag left out keeps the current value.
func RunRepositoryUpdate(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("repository update", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	expectedVersion := cli.BindExpectedVersionFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	remoteLocator := fs.String("remote-locator", "", "the corrected local path of the repository")
	defaultRef := fs.String("default-ref", "", "the corrected default ref (branch)")
	name := fs.String("name", "", "a new name for the repository")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if len(fs.Args()) != 1 {
		return usageErrorf("usage: aw repository update --expected-version <version> [--remote-locator <path>] [--default-ref <ref>] [--name <name>] <repositoryId>")
	}
	if *expectedVersion == 0 {
		return usageErrorf("--expected-version is required (the Repository version you observed, the CLI equivalent of HTTP's If-Match)")
	}
	if *remoteLocator == "" && *defaultRef == "" && *name == "" {
		return usageErrorf("nothing to change: give --remote-locator, --default-ref or --name")
	}
	repositoryID := fs.Args()[0]
	repo, err := appcatalog.GetRepository(ctx, deps.UoW, repositoryID)
	if err != nil {
		return err
	}
	req := appcatalog.UpdateRepositoryRequest{RepositoryID: repositoryID, ProjectID: string(repo.ProjectID), Name: *name, RemoteLocator: repo.RemoteLocator, DefaultRef: repo.DefaultRef}
	if *remoteLocator != "" {
		req.RemoteLocator = *remoteLocator
	}
	if *defaultRef != "" {
		req.DefaultRef = *defaultRef
	}
	payload := []byte(fmt.Sprintf(`{"name":%q,"remoteLocator":%q,"defaultRef":%q}`, req.Name, req.RemoteLocator, req.DefaultRef))
	return repositoryMutation(ctx, deps, stdout, repositoryID, *principalConfigPath, *idempotencyKey, commandTypeUpdateRepository, *expectedVersion, payload,
		func(ctx context.Context, cmd ports.Command, _ string) (any, error) {
			return appcatalog.UpdateRepository(ctx, deps.UoW, deps.IDs, cmd, req)
		})
}

// RunRepositoryDisable implements `aw repository disable <id> --expected-version <n> [--reason …]`.
func RunRepositoryDisable(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("repository disable", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	expectedVersion := cli.BindExpectedVersionFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	reason := fs.String("reason", "", "why the repository is retired")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if len(fs.Args()) != 1 {
		return usageErrorf("usage: aw repository disable --expected-version <version> [--reason <text>] <repositoryId>")
	}
	if *expectedVersion == 0 {
		return usageErrorf("--expected-version is required (the Repository version you observed, the CLI equivalent of HTTP's If-Match)")
	}
	repositoryID := fs.Args()[0]
	text := strings.TrimSpace(*reason)
	return repositoryMutation(ctx, deps, stdout, repositoryID, *principalConfigPath, *idempotencyKey, commandTypeDisableRepository, *expectedVersion,
		[]byte(fmt.Sprintf(`{"reason":%q}`, text)),
		func(ctx context.Context, cmd ports.Command, projectID string) (any, error) {
			return appcatalog.DisableRepository(ctx, deps.UoW, cmd, appcatalog.DisableRepositoryRequest{RepositoryID: repositoryID, ProjectID: projectID, Reason: text})
		})
}

// RunRepositoryDelete implements `aw repository delete <id> --expected-version <n> --reason …`: permanent, only for a repository that never became ACTIVE.
func RunRepositoryDelete(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("repository delete", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	expectedVersion := cli.BindExpectedVersionFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	reason := fs.String("reason", "", "why the repository is removed (required; it is permanent and the id is never reused)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if len(fs.Args()) != 1 {
		return usageErrorf("usage: aw repository delete --expected-version <version> --reason <text> <repositoryId>")
	}
	if *expectedVersion == 0 {
		return usageErrorf("--expected-version is required (the Repository version you observed, the CLI equivalent of HTTP's If-Match)")
	}
	text := strings.TrimSpace(*reason)
	if text == "" {
		return usageErrorf("--reason is required: deleting a repository is permanent")
	}
	repositoryID := fs.Args()[0]
	return repositoryMutation(ctx, deps, stdout, repositoryID, *principalConfigPath, *idempotencyKey, commandTypeDeleteRepository, *expectedVersion,
		[]byte(fmt.Sprintf(`{"reason":%q}`, text)),
		func(ctx context.Context, cmd ports.Command, projectID string) (any, error) {
			return appcatalog.DeleteRepository(ctx, deps.UoW, cmd, appcatalog.DeleteRepositoryRequest{RepositoryID: repositoryID, ProjectID: projectID, Reason: text})
		})
}

// RunRepositoryCheck implements `aw repository check --project-id <id> --path <p> [--ref <r>]`: the probe a registration runs, writing nothing.
func RunRepositoryCheck(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("repository check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	path := fs.String("path", "", "the local path of the repository to check")
	ref := fs.String("ref", "", "the default ref to resolve (HEAD when omitted)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 || *projectID == "" || strings.TrimSpace(*path) == "" {
		return usageErrorf("usage: aw repository check --project-id <projectId> --path <path> [--ref <ref>]")
	}
	if _, err := appcatalog.GetProject(ctx, deps.UoW, ports.ProjectScope(*projectID), *projectID); err != nil {
		return err
	}
	if deps.Prober == nil {
		return fmt.Errorf("no repository prober is configured (is Git installed?)")
	}
	result, err := appcatalog.CheckRepository(ctx, deps.Prober, *path, *ref)
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, result)
}
