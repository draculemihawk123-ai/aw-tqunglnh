package projection

import (
	"context"
	"encoding/json"
	"flag"
	"io"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	appprojectionrebuild "github.com/taQuangLing/agent-workflow/internal/app/projectionrebuild"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// rebuildRequestBody is `projection rebuild`'s own canonicalized command
// payload — mirrors requestProjectionRebuildBody
// (internal/delivery/httpapi/projectionrebuild/commands.go) field for
// field, deliberately without ProjectID (always --project-id) or
// OperationID (a caller never chooses its own operation ID;
// RequestProjectionRebuild always mints it itself) — so a CLI invocation
// canonicalizes to the identical bytes an equivalent HTTP request body
// would, which cli.BuildEnvelope's own SemanticHash call requires for a
// receipt written by either delivery mechanism to ever agree.
type rebuildRequestBody struct {
	ProjectionName string `json:"projectionName"`
}

// ActiveRebuildConflictError is RunRebuild's own typed surfacing of
// appprojectionrebuild's active-rebuild-operation conflict
// (newActiveProjectionRebuildConflictError,
// internal/app/projectionrebuild/commands.go): a NEW idempotency key while
// (ProjectID, ProjectionName) already has a nonterminal rebuild operation
// in progress. ActiveOperationID is extracted via
// appprojectionrebuild.ActiveProjectionRebuildOperationID — the one
// function that package exports for exactly this purpose — and re-exposed
// here as a plain, typed, exported field so a caller (an operator's own
// script, or a future composition-root dispatcher) can read it
// programmatically instead of having to scrape the human-readable Error()
// text. Design choice, mirroring cli.CommandError's own identical
// "wrap the original error, expose a typed field, never re-derive
// apperror.Error.Details reaching-in from outside this package" shape
// (internal/delivery/cli/dispatch.go): Error()/Unwrap() both forward to the
// original error unchanged, so errors.Is/errors.As against any sentinel
// appprojectionrebuild itself might document later keeps working through
// this wrapper.
type ActiveRebuildConflictError struct {
	ActiveOperationID string
	Err               error
}

func (e *ActiveRebuildConflictError) Error() string { return e.Err.Error() }
func (e *ActiveRebuildConflictError) Unwrap() error { return e.Err }

// RunRebuild implements
// `aw projection rebuild --project-id <projectId> --projection-name <name>
// [--idempotency-key <key>]` — a mutation over
// appprojectionrebuild.RequestProjectionRebuild via the full
// cli.BuildEnvelope/cli.Dispatch flow, mirroring
// internal/delivery/cli/scopeexpansion/request.go's own RunRequest exactly
// (this task's own reference shape, per doc.go).
//
// Deliberately no pre-dispatch receipt-replay check of its own — mirrors
// internal/delivery/httpapi/projectionrebuild's own POST handler exactly
// (that package's own top-of-file doc comment: RequestProjectionRebuild
// already performs its own receipt lookup/replay internally, inside its own
// WithSerializedWrite transaction). cli.Dispatch's own pre-dispatch replay
// check is still safe to run in front of it regardless — it is a read-only
// latency optimization for the common replay case, never a correctness
// dependency (cli.Dispatch's own Execute doc comment) — so this still goes
// through the ordinary cli.Dispatch call every other mutating leaf in this
// framework uses, rather than skipping it.
func RunRebuild(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("projection rebuild", flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	projectID := cli.BindProjectFlag(fs)
	projectionName := fs.String("projection-name", "", "projection name to rebuild (required)")
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return usageErrorf("usage: aw projection rebuild --project-id <projectId> --projection-name <name> [--idempotency-key <key>]")
	}
	if *projectID == "" {
		return usageErrorf("--project-id is required")
	}
	if *projectionName == "" {
		return usageErrorf("--projection-name is required")
	}

	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}

	body := rebuildRequestBody{ProjectionName: *projectionName}
	normalized, err := json.Marshal(body)
	if err != nil {
		return err
	}

	scope := ports.ProjectScope(*projectID)
	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandTypeRequestProjectionRebuild, Scope: scope,
		NormalizedPayload: normalized, IdempotencyKey: *idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})

	dispatched, err := cli.Dispatch(ctx, deps.UoW, envelope.Command, func(ctx context.Context) (any, error) {
		return appprojectionrebuild.RequestProjectionRebuild(ctx, deps.UoW, deps.IDs, envelope.Command, appprojectionrebuild.RequestProjectionRebuildRequest{
			ProjectID: *projectID, ProjectionName: *projectionName,
		})
	})
	if err != nil {
		if activeID, ok := appprojectionrebuild.ActiveProjectionRebuildOperationID(err); ok {
			return &ActiveRebuildConflictError{ActiveOperationID: activeID, Err: err}
		}
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}
