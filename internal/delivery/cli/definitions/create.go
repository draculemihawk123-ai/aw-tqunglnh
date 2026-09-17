package definitions

import (
	"context"
	"encoding/json"
	"flag"
	"io"

	appdefinitions "github.com/taQuangLing/agent-workflow/internal/app/definitions"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// createDefinitionBody is `aw definition create`'s own wire request shape
// — mirrors internal/delivery/httpapi/definitions/create.go's own
// createDefinitionBody exactly: deliberately without a scope/projectId
// field of its own, since scope is always this invocation's own
// --project-id flag, never re-declared in the body (V6-05's own "Không
// làm: ... tin scope từ payload", equally binding on this CLI mirror).
type createDefinitionBody struct {
	DefinitionID string `json:"definitionId"`
	Name         string `json:"name"`
}

// RunDefinitionCreate implements `aw definition create --kind <KIND>
// [--project-id <id>] [--file <path>]` (or pipe the JSON body via stdin) —
// a mutation over appdefinitions.CreateDefinition, dual-scoped by
// --project-id exactly like internal/delivery/cli/catalog's own
// RunProjectCreate/RunRepositoryRegister are.
func RunDefinitionCreate(ctx context.Context, deps Dependencies, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("definition create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	kindRaw := bindKindFlag(fs)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	projectID := bindProjectFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	filePath := cli.BindFileFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return usageErrorf("usage: aw definition create --kind <KIND> [--project-id <projectId>] [--file <path>] (or pipe the JSON body via stdin)")
	}
	kind, err := parseKind(*kindRaw)
	if err != nil {
		return err
	}

	raw, err := cli.ReadBoundedInput(stdin, *filePath, cli.DefaultMaxInputBytes)
	if err != nil {
		return cli.UsageError{Err: err}
	}
	var body createDefinitionBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return usageErrorf("decode request body: %v", err)
	}
	if body.DefinitionID == "" {
		return usageErrorf(`request body "definitionId" is required`)
	}
	if body.Name == "" {
		return usageErrorf(`request body "name" is required`)
	}
	normalized, err := json.Marshal(body)
	if err != nil {
		return err
	}

	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}

	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandTypeCreateDefinition, Scope: commandScopeFromProjectID(*projectID),
		NormalizedPayload: normalized, IdempotencyKey: *idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})

	dispatched, err := cli.Dispatch(ctx, deps.UoW, envelope.Command, func(ctx context.Context) (any, error) {
		return appdefinitions.CreateDefinition(ctx, deps.UoW, envelope.Command, appdefinitions.CreateDefinitionRequest{
			DefinitionID: body.DefinitionID, Kind: kind, Scope: definitionScopeFromProjectID(*projectID), Name: body.Name,
		})
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}
