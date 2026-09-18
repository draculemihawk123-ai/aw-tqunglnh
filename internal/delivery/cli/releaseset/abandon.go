package releaseset

import (
	"context"
	"io"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
)

// RunAbandon implements
// `aw release-set abandon <releaseSetId> --expected-version <n> [--idempotency-key <key>]`
// — a fenced CAS mutation over workapp.AbandonReleaseSet, closing a
// ReleaseSet's lifecycle as ABANDONED. See transition.go's own
// runReleaseSetTransition doc comment for the full shared contract (scope
// derivation, staleness handling, envelope shape) this and RunSeal both
// build on; mirrors
// internal/delivery/httpapi/releaseset/release_set_commands.go's own
// handleAbandonReleaseSet.
func RunAbandon(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	return runReleaseSetTransition(ctx, deps, "release-set abandon",
		"usage: aw release-set abandon <releaseSetId> --expected-version <n>",
		commandTypeAbandonReleaseSet, args, stdout, stderr,
		func(ctx context.Context, uow ports.UnitOfWork, cmd ports.Command, releaseSetID string) (any, error) {
			return workapp.AbandonReleaseSet(ctx, uow, cmd, workapp.AbandonReleaseSetRequest{ReleaseSetID: releaseSetID})
		})
}
