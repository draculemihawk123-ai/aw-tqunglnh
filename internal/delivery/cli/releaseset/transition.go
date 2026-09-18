package releaseset

import (
	"context"
	"flag"
	"io"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// releaseSetTransitionExecute is Seal/Abandon's own shared dispatch shape:
// both wrap a fenced CAS transition over an already-CREATED ReleaseSet,
// differing only in which internal/app/work function they call.
type releaseSetTransitionExecute func(ctx context.Context, uow ports.UnitOfWork, cmd ports.Command, releaseSetID string) (any, error)

// runReleaseSetTransition is seal.go's and abandon.go's own shared
// implementation — see each caller's own doc comment for the
// command-specific contract this shares between them. Both `seal` and
// `abandon` bind the identical flag set (<releaseSetId> positional,
// --expected-version, --idempotency-key, --principal-config) and build the
// identical "{}" empty-body envelope (mirroring
// internal/delivery/httpapi/releaseset's own emptyBody for these same two
// routes) — commandType and execute are the only two things that differ
// between them.
//
// Deliberately NO --project-id flag (unlike
// internal/delivery/cli/scopeexpansion's own approve/reject/withdraw,
// whose identically-shaped leaves DO bind one): workapp.GetReleaseSet
// (release_set_queries.go) takes no scope parameter of its own, unlike
// workapp.GetScopeExpansionRequest — this function instead reloads the
// ReleaseSet by ID FIRST and derives Scope from its own authoritative,
// already-persisted ProjectID, the same "server derives scope from the
// authoritative target, never trusts a caller-supplied value" discipline
// internal/delivery/cli/workitem's own loadWorkItemProjectID already
// establishes for `work-item mark-ready`/`work-item cancel`
// (workitem/helpers.go).
//
// Staleness (the "stale" Verify bullet: "a stale --expected-version on
// seal/abandon ... is rejected with a typed conflict, never silently
// overwritten") is never manually pre-checked here the way
// scopeexpansion.Approve's own doc comment describes for THAT leaf: unlike
// ApproveScopeExpansion (whose own internal CAS is keyed on
// ExpectedStatus=PENDING alone, never on the caller's version — so
// scopeexpansion.Approve must pre-check the version itself, inside its own
// cli.Dispatch execute closure, to enforce --expected-version at all),
// SealReleaseSet/AbandonReleaseSet's own internal transitionReleaseSet
// (internal/app/work/release_set.go) already fences directly on
// cmd.ExpectedVersion via TransitionReleaseSetState's own CAS — a stale
// --expected-version is rejected with a real ports.ErrOptimisticConflict
// from that one authoritative check, never a redundant second one here.
func runReleaseSetTransition(
	ctx context.Context, deps Dependencies, commandName, usage, commandType string, args []string, stdout, stderr io.Writer,
	execute releaseSetTransitionExecute,
) error {
	fs := flag.NewFlagSet(commandName, flag.ContinueOnError)
	fs.SetOutput(stderr)
	principalConfigPath := cli.BindPrincipalFlag(fs)
	expectedVersion := cli.BindExpectedVersionFlag(fs)
	idempotencyKey := cli.BindIdempotencyKeyFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("%s", usage)
	}
	releaseSetID := positional[0]
	if *expectedVersion == 0 {
		return usageErrorf("--expected-version is required (the ReleaseSet version you observed, the CLI equivalent of HTTP's If-Match)")
	}

	detail, err := workapp.GetReleaseSet(ctx, deps.UoW, releaseSetID)
	if err != nil {
		return err
	}
	scope := ports.ProjectScope(detail.ProjectID)

	principal, err := loadPrincipal(*principalConfigPath)
	if err != nil {
		return err
	}

	envelope := cli.BuildEnvelope(cli.EnvelopeRequest{
		Principal: principal, CommandType: commandType, Scope: scope,
		NormalizedPayload: []byte("{}"), ExpectedVersion: *expectedVersion,
		IdempotencyKey: *idempotencyKey, IDSource: deps.IDs, Now: deps.Now,
	})

	dispatched, err := cli.Dispatch(ctx, deps.UoW, envelope.Command, func(ctx context.Context) (any, error) {
		return execute(ctx, deps.UoW, envelope.Command, releaseSetID)
	})
	if err != nil {
		return err
	}
	return cli.EncodeCommandResult(stdout, cli.ResultEnvelope{
		IdempotencyKey: envelope.Command.IdempotencyKey, Replayed: dispatched.Replayed, Result: dispatched.Result,
	})
}
