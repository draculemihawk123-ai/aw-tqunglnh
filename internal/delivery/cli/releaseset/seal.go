package releaseset

import (
	"context"
	"io"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
)

// RunSeal implements
// `aw release-set seal <releaseSetId> --expected-version <n> [--idempotency-key <key>]`
// — a fenced CAS mutation over workapp.SealReleaseSet, closing a
// ReleaseSet's lifecycle as SEALED. See transition.go's own
// runReleaseSetTransition doc comment for the full shared contract (scope
// derivation, staleness handling, envelope shape) this and RunAbandon both
// build on; mirrors
// internal/delivery/httpapi/releaseset/release_set_commands.go's own
// handleSealReleaseSet.
func RunSeal(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	return runReleaseSetTransition(ctx, deps, "release-set seal",
		"usage: aw release-set seal <releaseSetId> --expected-version <n>",
		commandTypeSealReleaseSet, args, stdout, stderr,
		func(ctx context.Context, uow ports.UnitOfWork, cmd ports.Command, releaseSetID string) (any, error) {
			return workapp.SealReleaseSet(ctx, uow, cmd, workapp.SealReleaseSetRequest{ReleaseSetID: releaseSetID})
		})
}
