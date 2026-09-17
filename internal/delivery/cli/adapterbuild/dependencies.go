package adapterbuild

import (
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// Dependencies is everything every Run* function in this package needs —
// mirrors internal/delivery/cli/definitions.Dependencies's own shape
// exactly (this package's own closest sibling: both wrap an installation/
// project-agnostic-or-installation-only application layer through the
// identical cli.BuildEnvelope/cli.Dispatch flow). A future composition root
// (V6-15O) constructs one from its own already-built ports.UnitOfWork/
// idsource.Source and passes it into whichever Run* function it dispatches
// to; this package never reaches for a global or constructs either itself.
type Dependencies struct {
	// UoW is the one real ports.UnitOfWork every Run* function in this
	// package dispatches through.
	UoW ports.UnitOfWork
	// IDs generates a fresh idempotency key when an operator omits
	// --idempotency-key — cli.EnvelopeRequest.IDSource's own doc comment.
	IDs idsource.Source
	// Now defaults to time.Now().UTC when nil, exactly like
	// cli.EnvelopeRequest.Now's own doc comment — a caller only needs to
	// supply this for deterministic tests.
	Now func() time.Time
}
