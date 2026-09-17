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
		Path: []string{"scope-expansion", "withdraw"}, Scope: cli.ScopeProject,
		AppOperation: "WithdrawScopeExpansion", HTTPOperationID: "withdrawScopeExpansion",
	})
}

const commandTypeWithdrawScopeExpansion = "WithdrawScopeExpansion"

type withdrawFlags struct {
	principal, project, idempotencyKey *string
	expectedVersion                    *uint64
}

// bindWithdrawFlags is Withdraw's own flag-binding step — see request.go's
// own bindRequestFlags doc comment for why this is factored out (the
// "spoof" Verify bullet).
func bindWithdrawFlags(fs *flag.FlagSet) withdrawFlags {
	return withdrawFlags{
		principal:       cli.BindPrincipalFlag(fs),
		project:         cli.BindProjectFlag(fs),
		expectedVersion: cli.BindExpectedVersionFlag(fs),
		idempotencyKey:  cli.BindIdempotencyKeyFlag(fs),
	}
}

// Withdraw implements `aw scope-expansion withdraw <requestId>
// --project-id <projectId> --expected-version <n> [--idempotency-key
// <key>]` — an UPDATE-shaped mutation over workapp.WithdrawScopeExpansion.
// See approve.go's own doc comment for the shared reload-first/
// Version-precondition-inside-the-execute-closure discipline.
//
// workapp.WithdrawScopeExpansion is itself idempotent at the BUSINESS
// level (a request already WITHDRAWN, reached via ANY prior successful
// withdrawal regardless of actor/idempotency-key, is a harmless replay to
// the SAME result — work/scope_expansion.go's own WithdrawScopeExpansion
// doc comment). This command's own Version precondition below still
// applies uniformly on the fresh-dispatch path even so: a caller supplying
// a --expected-version that no longer matches the reloaded request
// genuinely does not yet know the request's current state (WITHDRAWN or
// otherwise), and workitem/scope_expansion_commands.go's own
// handleWithdrawScopeExpansion adds no special-casing here either.
func Withdraw(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("scope-expansion withdraw", flag.ContinueOnError)
	fs.SetOutput(stderr)
	f := bindWithdrawFlags(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw scope-expansion withdraw <requestId> --project-id <projectId> --expected-version <n>")
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
		Principal: principal, CommandType: commandTypeWithdrawScopeExpansion, Scope: scope,
		NormalizedPayload: []byte("{}"), ExpectedVersion: *f.expectedVersion,
		IdempotencyKey: *f.idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})

	dispatched, err := cli.Dispatch(ctx, deps.UOW, envelope.Command, func(ctx context.Context) (any, error) {
		// Deliberately excludes the WITHDRAWN case (unlike approve.go's own
		// identical-looking check): workapp.WithdrawScopeExpansion's own
		// business-level idempotency already tolerates a stale
		// --expected-version once the target is WITHDRAWN (this file's own
		// doc comment) — but this outer, advisory check has no way to know
		// the target is ALREADY WITHDRAWN without re-reading it, so it is
		// deliberately left exactly as strict as approve/reject's own
		// checks; a caller retrying a genuine withdraw after observing a
		// version bump from some OTHER decision still gets a clean,
		// actionable error here rather than a silently accepted no-op for
		// the wrong reason.
		if detail.Version != *f.expectedVersion {
			return nil, fmt.Errorf("scope expansion request %s is at version %d, not %d — reload and retry", requestID, detail.Version, *f.expectedVersion)
		}
		return workapp.WithdrawScopeExpansion(ctx, deps.UOW, envelope.Command, workapp.WithdrawScopeExpansionRequest{
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
