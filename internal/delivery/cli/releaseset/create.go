package releaseset

import (
	"context"
	"encoding/json"
	"flag"
	"io"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// createReleaseSetBody is `release-set create`'s own request body shape
// (read via --file/stdin — see this package's own doc comment for why)
// — deliberately without ProjectID/FamilyID: both are always --project-id/
// --family-id flags, mirroring
// internal/delivery/httpapi/releaseset/release_set_commands.go's own
// createReleaseSetBody, which excludes the identical two path segments
// ({projectId}/{familyId}) for the identical reason
// internal/delivery/cli/workitem's own createRootWorkItemBody already gives
// ("the project is always --project-id... never re-declared by the
// caller's own body"). This is also why NormalizedPayload below hashes only
// {repositories:[...]}, never FamilyID — the exact same canonical bytes
// httpapi.CanonicalizeJSON produces for this same body shape on the HTTP
// side, so an HTTP call and an equivalent CLI call for "the same"
// CreateReleaseSet hash and replay identically (cli.BuildEnvelope's own
// doc comment).
type createReleaseSetBody struct {
	Repositories []repositoryReleaseBody `json:"repositories"`
}

// RunCreate implements
// `aw release-set create --project-id <projectId> --family-id <familyId> [--file <path>]`
// (or pipe the JSON body via stdin) — a mutation over workapp.CreateReleaseSet,
// following the full cli.BuildEnvelope/cli.Dispatch CommandEnvelope flow,
// mirroring internal/delivery/cli/workitem/create.go's own RunWorkItemCreate
// exactly (that function's own doc comment names
// internal/delivery/cli/catalog/repository.go's own RunRepositoryRegister as
// ITS template — the same lineage this leaf continues).
func RunCreate(ctx context.Context, deps Dependencies, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("release-set create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	projectID := cli.BindProjectFlag(fs)
	familyID := fs.String("family-id", "", "task family this release set belongs to (required)")
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	filePath := cli.BindFileFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return usageErrorf("usage: aw release-set create --project-id <projectId> --family-id <familyId> [--file <path>] (or pipe the JSON body via stdin)")
	}
	if *projectID == "" {
		return usageErrorf("--project-id is required")
	}
	if *familyID == "" {
		return usageErrorf("--family-id is required")
	}

	raw, err := cli.ReadBoundedInput(stdin, *filePath, cli.DefaultMaxInputBytes)
	if err != nil {
		return cli.UsageError{Err: err}
	}
	var body createReleaseSetBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return usageErrorf("decode request body: %v", err)
	}
	if err := validateRepositoryReleaseBodies("repositories", body.Repositories); err != nil {
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
		Principal: principal, CommandType: commandTypeCreateReleaseSet, Scope: scope,
		NormalizedPayload: normalized, IdempotencyKey: *idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})

	dispatched, err := cli.Dispatch(ctx, deps.UoW, envelope.Command, func(ctx context.Context) (any, error) {
		return workapp.CreateReleaseSet(ctx, deps.UoW, deps.IDs, envelope.Command, workapp.CreateReleaseSetRequest{
			ProjectID: *projectID, FamilyID: *familyID, Repositories: toRepositoryReleaseRequests(body.Repositories),
		})
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}
