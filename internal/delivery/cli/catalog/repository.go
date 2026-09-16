package catalog

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	appcatalog "github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// RunRepositoryList implements `aw repository list <projectId>` — a
// read-only query over appcatalog.ListProjectRepositories. The Project is
// reloaded first (mirroring
// internal/delivery/httpapi/catalog/repository.go's own
// listProjectRepositories) so a nonexistent projectId reports the real
// ports.ErrPersistenceNotFound rather than a silently empty list —
// ListProjectRepositories itself only ever filters by the stored
// project_id column and does not itself verify projectID names a real
// Project.
func RunRepositoryList(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("repository list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw repository list <projectId>")
	}
	projectID := positional[0]

	if _, err := appcatalog.GetProject(ctx, deps.UoW, ports.ProjectScope(projectID), projectID); err != nil {
		return err
	}
	repos, err := appcatalog.ListProjectRepositories(ctx, deps.UoW, projectID)
	if err != nil {
		return err
	}
	views := make([]repositoryView, 0, len(repos))
	for _, r := range repos {
		views = append(views, newRepositoryView(r))
	}
	return cli.EncodeQueryResult(stdout, repositoryListView{Repositories: views})
}

// RunRepositoryOnboarding implements `aw repository onboarding <id>` — a
// read-only query composing appcatalog.GetRepository +
// appcatalog.ListRepositoryProbeAttempts into one onboardingView, exactly
// like internal/delivery/httpapi/catalog/repository.go's own
// getRepositoryOnboarding. Like GetRepository itself, this reaches its
// target by RepositoryID alone (no project.go-style scope argument): the
// Repository's own ProjectID is learned FROM this query's own result, not
// supplied by the caller.
func RunRepositoryOnboarding(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("repository onboarding", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw repository onboarding <repositoryId>")
	}
	repositoryID := positional[0]

	repo, err := appcatalog.GetRepository(ctx, deps.UoW, repositoryID)
	if err != nil {
		return err
	}
	attempts, err := appcatalog.ListRepositoryProbeAttempts(ctx, deps.UoW, repositoryID)
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, newOnboardingView(repo, attempts))
}

// registerRepositoryRequestBody is `aw repository register`'s own request
// body shape (read via --file/stdin), mirroring
// internal/delivery/httpapi/catalog/repository.go's own
// registerRepositoryRequestBody: RepositoryID is caller-chosen (a
// Repository's identity is named by its caller, not minted by this
// application — appcatalog.RegisterRepositoryRequest's own doc comment).
// ProjectID is deliberately not a body field: it is supplied via
// --project-id instead (see RunRepositoryRegister below), the CLI
// equivalent of the HTTP route's own /projects/{id}/repositories path
// parameter — never trusted from the body either way.
type registerRepositoryRequestBody struct {
	RepositoryID  string `json:"repositoryId"`
	Name          string `json:"name"`
	RemoteLocator string `json:"remoteLocator"`
	DefaultRef    string `json:"defaultRef"`
}

// RunRepositoryRegister implements `aw repository register` — a mutation
// over appcatalog.RegisterRepository, scoped to --project-id
// (cli.BindProjectFlag), the flag this framework's own flags.go
// specifically names for "a project-scoped leaf ... to build its own
// ports.CommandScope(...)".
func RunRepositoryRegister(ctx context.Context, deps Dependencies, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("repository register", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	projectID := cli.BindProjectFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	filePath := cli.BindFileFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return usageErrorf("usage: aw repository register --project-id <projectId> [--file <path>] (or pipe the JSON body via stdin)")
	}
	if *projectID == "" {
		return usageErrorf("--project-id is required")
	}

	raw, err := cli.ReadBoundedInput(stdin, *filePath, cli.DefaultMaxInputBytes)
	if err != nil {
		return cli.UsageError{Err: err}
	}
	var body registerRepositoryRequestBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return usageErrorf("decode request body: %v", err)
	}
	normalized, err := json.Marshal(body)
	if err != nil {
		return err
	}

	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}

	scope := ports.ProjectScope(*projectID)
	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandTypeRegisterRepository, Scope: scope,
		NormalizedPayload: normalized, IdempotencyKey: *idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})

	dispatched, err := cli.Dispatch(ctx, deps.UoW, envelope.Command, func(ctx context.Context) (any, error) {
		return appcatalog.RegisterRepository(ctx, deps.UoW, deps.IDs, envelope.Command, appcatalog.RegisterRepositoryRequest{
			RepositoryID: body.RepositoryID, ProjectID: *projectID, Name: body.Name,
			RemoteLocator: body.RemoteLocator, DefaultRef: body.DefaultRef,
		})
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}

// RunRepositoryRetryProbe implements `aw repository retry-probe <id>` — a
// mutation over appcatalog.RetryRepositoryProbe, the CLI's own
// update-shaped command (cli.BindExpectedVersionFlag's own doc comment:
// "the CLI equivalent of HTTP's If-Match"). Order of operations mirrors
// internal/delivery/httpapi/catalog/repository.go's own retryRepositoryProbe
// exactly and for the identical reason: GetRepository (to learn ProjectID
// for scope) runs BEFORE cli.Dispatch's own receipt lookup, but the "is
// this Repository actually BLOCKED" precondition check runs INSIDE the
// execute closure — reached only on a genuinely fresh (non-replayed)
// dispatch — so a replay of an earlier successful retry always returns the
// original result regardless of what status the Repository has moved to
// since.
func RunRepositoryRetryProbe(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("repository retry-probe", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	expectedVersion := cli.BindExpectedVersionFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw repository retry-probe <repositoryId> --expected-version <version>")
	}
	repositoryID := positional[0]
	if *expectedVersion == 0 {
		return usageErrorf("--expected-version is required (the Repository version you observed, the CLI equivalent of HTTP's If-Match)")
	}

	repo, err := appcatalog.GetRepository(ctx, deps.UoW, repositoryID)
	if err != nil {
		return err
	}

	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}

	scope := ports.ProjectScope(string(repo.ProjectID))
	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandTypeRetryRepositoryProbe, Scope: scope,
		NormalizedPayload: []byte("{}"), ExpectedVersion: *expectedVersion,
		IdempotencyKey: *idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})

	dispatched, err := cli.Dispatch(ctx, deps.UoW, envelope.Command, func(ctx context.Context) (any, error) {
		// Route-level fast-reject only, not authoritative — the real CAS
		// inside appcatalog.RetryRepositoryProbe itself
		// (ExpectedStatus=BLOCKED) remains the sole authoritative
		// enforcement. Reached only when no receipt already exists for
		// this exact idempotency key, so a replay never re-checks (or is
		// ever rejected by) a status the Repository has since moved past.
		// Deliberately a plain error, not usageErrorf: this is a state/
		// precondition conflict (the CLI equivalent of HTTP's own 409
		// Conflict, internal/delivery/httpapi/catalog/repository.go's own
		// retryRepositoryProbe), never a badly-formed invocation — a
		// future composition root's own cli.ExitCodeFor must classify it
		// as cli.ExitFailure, not cli.ExitUsage.
		if repo.Status != project.RepositoryBlocked {
			return nil, fmt.Errorf("repository %s is %s, not BLOCKED — retry-probe is only valid for a blocked repository", repositoryID, repo.Status)
		}
		return appcatalog.RetryRepositoryProbe(ctx, deps.UoW, deps.IDs, envelope.Command, appcatalog.RetryRepositoryProbeRequest{
			RepositoryID: repositoryID, ProjectID: string(repo.ProjectID),
		})
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}
