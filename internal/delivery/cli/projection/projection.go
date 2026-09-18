package projection

import (
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// Dependencies is everything every Run* function in this package needs —
// mirrors internal/delivery/cli/workitem.Dependencies's own shape exactly
// (this package's own closest sibling: both wrap a project-scoped
// application layer through the identical cli.BuildEnvelope/cli.Dispatch
// flow for their mutating command, plus a couple of plain reads). A future
// composition root (V6-15O) constructs one from its own already-built
// ports.UnitOfWork/idsource.Source and passes it into whichever Run*
// function it dispatches to; this package never reaches for a global or
// constructs either itself.
type Dependencies struct {
	// UoW is the one real ports.UnitOfWork every Run* function in this
	// package dispatches through.
	UoW ports.UnitOfWork
	// IDs mints RequestProjectionRebuild's own new OperationID/JobID, and a
	// fresh idempotency key when an operator omits --idempotency-key on
	// `projection rebuild` (cli.EnvelopeRequest.IDSource's own doc comment).
	IDs idsource.Source
	// Now defaults to time.Now().UTC when nil, exactly like
	// cli.EnvelopeRequest.Now's own doc comment — a caller only needs to
	// supply this for deterministic tests.
	Now func() time.Time
}

// This block registers this package's own three Descriptors into
// cli.Default from this package's own init() — the "own package, own
// init()" discipline descriptor.go's own doc comment (internal/delivery/cli)
// describes. HTTPOperationID mirrors
// internal/delivery/httpapi/projectionrebuild's own RegisterRoutes
// OperationID values exactly (getProjectionStatus/requestProjectionRebuild/
// getProjectionRebuildOperationStatus) — the same operation this leaf's own
// dispatch calls into by way of the shared internal/app/projectionrebuild
// functions AppOperation names below.
func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"projection", "status"}, Scope: cli.ScopeProject,
		AppOperation: appOpGetProjectionStatus, HTTPOperationID: "getProjectionStatus",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"projection", "rebuild"}, Scope: cli.ScopeProject,
		AppOperation: commandTypeRequestProjectionRebuild, HTTPOperationID: "requestProjectionRebuild",
	})
	cli.MustRegister(cli.Descriptor{
		Path: []string{"projection", "rebuild-status"}, Scope: cli.ScopeProject,
		AppOperation: appOpGetProjectionRebuildStatus, HTTPOperationID: "getProjectionRebuildOperationStatus",
	})
}
