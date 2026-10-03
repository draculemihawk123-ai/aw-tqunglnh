package definitions

import (
	"time"

	appdefinitions "github.com/taQuangLing/agent-workflow/internal/app/definitions"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// Dependencies is what every Run* function in this package needs — the
// CLI-side counterpart of internal/delivery/httpapi/definitions.Dependencies,
// mirroring internal/delivery/cli/catalog.Dependencies's own shape exactly.
// A future composition root (V6-15O) constructs one from its own
// already-built ports.UnitOfWork/idsource.Source and passes it into
// whichever Run* function it dispatches to; this package never reaches for
// a global or constructs either itself.
type Dependencies struct {
	UoW ports.UnitOfWork
	IDs idsource.Source
	// Now defaults to time.Now().UTC when nil, exactly like
	// cli.EnvelopeRequest.Now's own doc comment — a caller only needs to
	// supply this for deterministic tests.
	Now func() time.Time
	// Hygiene (V9-10) configures the knowledge-hygiene warnings `definition
	// publish` reports; the zero value takes the defaults, and the publish
	// leaf's own --warn-* flags override it per invocation.
	Hygiene appdefinitions.WarnPolicy
}
