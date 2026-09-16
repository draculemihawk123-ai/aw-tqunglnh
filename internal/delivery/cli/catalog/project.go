package catalog

import (
	"context"
	"encoding/json"
	"flag"
	"io"

	appcatalog "github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// RunProjectList implements `aw project list` — a read-only query over
// appcatalog.ListProjects, always installation-scoped per ADR-025 (that
// function itself rejects anything else with ports.ErrScopeMismatch).
// Takes no positional arguments.
func RunProjectList(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("project list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return usageErrorf("usage: aw project list")
	}

	projects, err := appcatalog.ListProjects(ctx, deps.UoW, ports.InstallationScope())
	if err != nil {
		return err
	}
	views := make([]projectView, 0, len(projects))
	for _, p := range projects {
		views = append(views, newProjectView(p))
	}
	return cli.EncodeQueryResult(stdout, projectListView{Projects: views})
}

// RunProjectShow implements `aw project show <projectId>` — a read-only
// query over appcatalog.GetProject. The one positional argument names both
// the Project being requested AND the ports.ProjectScope it is read under
// (GetProject's own doc comment: "the route itself already names the exact
// Project being requested").
func RunProjectShow(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("project show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw project show <projectId>")
	}
	projectID := positional[0]

	p, err := appcatalog.GetProject(ctx, deps.UoW, ports.ProjectScope(projectID), projectID)
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, newProjectView(p))
}

// RunProjectCreate implements `aw project create` — a mutation over
// appcatalog.CreateProject, always installation-scoped per ADR-025. The
// request body (`{"name": "..."}`) is read from --file or stdin
// (cli.ReadBoundedInput) and decoded directly into
// appcatalog.CreateProjectRequest, mirroring
// internal/delivery/httpapi/catalog/project.go's own createProject: that
// struct's one field, Name, has no JSON tag, so encoding/json's own
// case-insensitive fallback matching binds a `{"name": "..."}` body to it
// exactly the same way.
func RunProjectCreate(ctx context.Context, deps Dependencies, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("project create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	filePath := cli.BindFileFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return usageErrorf("usage: aw project create [--file <path>] (or pipe the JSON body via stdin)")
	}

	raw, err := cli.ReadBoundedInput(stdin, *filePath, cli.DefaultMaxInputBytes)
	if err != nil {
		return cli.UsageError{Err: err}
	}
	var req appcatalog.CreateProjectRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return usageErrorf("decode request body: %v", err)
	}
	normalized, err := json.Marshal(req)
	if err != nil {
		return err
	}

	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}

	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandTypeCreateProject, Scope: ports.InstallationScope(),
		NormalizedPayload: normalized, IdempotencyKey: *idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})

	dispatched, err := cli.Dispatch(ctx, deps.UoW, envelope.Command, func(ctx context.Context) (any, error) {
		return appcatalog.CreateProject(ctx, deps.UoW, deps.IDs, envelope.Command, req)
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}
