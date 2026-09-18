package workitem

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// createRootWorkItemBody is `work-item create`'s own request body shape
// (read via --file/stdin) — deliberately without a projectId field: the
// project is always --project-id, mirroring
// internal/delivery/httpapi/workitem/workitem_commands.go's own
// createRootWorkItemBody exactly ("the project is always the path's own
// {projectId}... never re-declared by the caller's own body").
type createRootWorkItemBody struct {
	Title        string           `json:"title"`
	InitialScope []scopeGrantBody `json:"initialScope"`
}

// RunWorkItemCreate implements
// `aw work-item create --project-id <projectId>` — a mutation over
// workapp.CreateRootWorkItem, following the full
// cli.BuildEnvelope/cli.Dispatch CommandEnvelope flow, mirroring
// internal/delivery/cli/catalog/repository.go's own RunRepositoryRegister
// exactly (that function's own doc comment is this leaf's own template, per
// this task's own brief).
func RunWorkItemCreate(ctx context.Context, deps Dependencies, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("work-item create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	projectID := cli.BindProjectFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	filePath := cli.BindFileFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return usageErrorf("usage: aw work-item create --project-id <projectId> [--file <path>] (or pipe the JSON body via stdin)")
	}
	if *projectID == "" {
		return usageErrorf("--project-id is required")
	}

	raw, err := cli.ReadBoundedInput(stdin, *filePath, cli.DefaultMaxInputBytes)
	if err != nil {
		return cli.UsageError{Err: err}
	}
	var body createRootWorkItemBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return usageErrorf("decode request body: %v", err)
	}
	if strings.TrimSpace(body.Title) == "" {
		return usageErrorf("title is required")
	}
	if err := validateScopeGrantBodies("initialScope", body.InitialScope); err != nil {
		return err
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
		Principal: principal, CommandType: commandTypeCreateRootWorkItem, Scope: scope,
		NormalizedPayload: normalized, IdempotencyKey: *idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})

	dispatched, err := cli.Dispatch(ctx, deps.UoW, envelope.Command, func(ctx context.Context) (any, error) {
		return workapp.CreateRootWorkItem(ctx, deps.UoW, deps.IDs, envelope.Command, workapp.CreateRootWorkItemRequest{
			ProjectID: *projectID, Title: body.Title, InitialScope: toScopeGrantRequests(body.InitialScope),
		})
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}
