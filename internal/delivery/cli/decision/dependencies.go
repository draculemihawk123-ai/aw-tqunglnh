package decision

import (
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// Dependencies is what every command function in this package needs — the
// CLI-side counterpart of internal/delivery/httpapi/decision.Dependencies,
// mirroring internal/delivery/cli/scopeexpansion.Dependencies's own shape
// exactly. A future composition root (V6-15O) constructs one from its own
// already-built ports.UnitOfWork/idsource.Source and passes it into
// whichever command it dispatches to; this package never reaches for a
// global or constructs either itself.
type Dependencies struct {
	UOW ports.UnitOfWork
	IDs idsource.Source
	// Now defaults to time.Now().UTC when nil, exactly like
	// cli.EnvelopeRequest.Now's own doc comment — a caller only needs to
	// supply this for deterministic tests.
	Now func() time.Time
}
