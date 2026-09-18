package scopeexpansion

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"scope-expansion", "approve"}, Scope: cli.ScopeProject,
		AppOperation: "ApproveScopeExpansion", HTTPOperationID: "approveScopeExpansion",
	})
}

const commandTypeApproveScopeExpansion = "ApproveScopeExpansion"

type approveFlags struct {
	principal, project, idempotencyKey *string
	expectedVersion                    *uint64
}

// bindApproveFlags is Approve's own flag-binding step — see request.go's
// own bindRequestFlags doc comment for why this is factored out (the
// "spoof" Verify bullet).
func bindApproveFlags(fs *flag.FlagSet) approveFlags {
	return approveFlags{
		principal:       cli.BindPrincipalFlag(fs),
		project:         cli.BindProjectFlag(fs),
		expectedVersion: cli.BindExpectedVersionFlag(fs),
		idempotencyKey:  cli.BindIdempotencyKeyFlag(fs),
	}
}

// Approve implements `aw scope-expansion approve <requestId> --project-id
// <projectId> --expected-version <n> [--idempotency-key <key>]` — an
// UPDATE-shaped mutation over workapp.ApproveScopeExpansion, mirroring
// internal/delivery/httpapi/workitem/scope_expansion_commands.go's own
// handleApproveScopeExpansion: the named ScopeExpansionRequest is reloaded
// via workapp.GetScopeExpansionRequest FIRST (deriving scope, and its
// current Version for the --expected-version precondition below) before
// Idempotency-Key is even read.
//
// The Version precondition check is deliberately performed INSIDE the
// cli.Dispatch execute closure — reached only on a genuinely fresh
// (non-replayed) dispatch — never before calling cli.Dispatch: a real
// receipt replay must always win over an apparently-stale
// --expected-version, exactly mirroring
// internal/delivery/httpapi/decision/approval.go's own documented ordering
// ("A real replay wins over ETag/state drift... never re-checked against
// the If-Match precondition") and internal/delivery/cli/catalog's own
// RunRepositoryRetryProbe (repository.go). This check can only ever catch
// an ALREADY-stale caller — workapp.ApproveScopeExpansion's own internal
// fenced CAS (keyed on ExpectedStatus=PENDING, not on this caller-supplied
// version) remains the one true race-decider; see
// scope_expansion.go's own ApproveScopeExpansion doc comment.
func Approve(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("scope-expansion approve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	f := bindApproveFlags(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw scope-expansion approve <requestId> --project-id <projectId> --expected-version <n>")
	}
	requestID := positional[0]
	if strings.TrimSpace(*f.project) == "" {
		return usageErrorf("--project-id is required")
	}
	if *f.expectedVersion == 0 {
		return usageErrorf("--expected-version is required (the ScopeExpansionRequest version you observed, the CLI equivalent of HTTP's If-Match)")
	}

	scope := ports.ProjectScope(*f.project)
	detail, err := workapp.GetScopeExpansionRequest(ctx, deps.UOW, scope, requestID)
	if err != nil {
		return err
	}

	principal, err := loadPrincipal(*f.principal)
	if err != nil {
		return err
	}

	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandTypeApproveScopeExpansion, Scope: scope,
		NormalizedPayload: []byte("{}"), ExpectedVersion: *f.expectedVersion,
		IdempotencyKey: *f.idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})

	dispatched, err := cli.Dispatch(ctx, deps.UOW, envelope.Command, func(ctx context.Context) (any, error) {
		if detail.Version != *f.expectedVersion {
			return nil, fmt.Errorf("scope expansion request %s is at version %d, not %d — reload and retry", requestID, detail.Version, *f.expectedVersion)
		}
		return workapp.ApproveScopeExpansion(ctx, deps.UOW, deps.IDs, envelope.Command, workapp.ApproveScopeExpansionRequest{
			RequestID: requestID,
		})
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}
