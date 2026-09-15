package evidence

import (
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// Dependencies is everything this package's own handlers need — a real
// ports.UnitOfWork (every query dispatches through
// internal/app/runtime/queries.go, never a second, competing persistence
// path) and a real ports.ArtifactStore for the one route that streams raw
// content (getArtifactContent). No idsource.Source/clock.Clock is needed:
// every route here is read-only and mints no new ID/timestamp of its own.
type Dependencies struct {
	// UnitOfWork is the one real ports.UnitOfWork every handler in this
	// package dispatches through.
	UnitOfWork ports.UnitOfWork
	// ArtifactStore is the real, composition-root-owned content-addressed
	// store getArtifactContent opens/verifies against — the SAME store
	// cmd/aw/serve.go already constructs for internal/delivery/httpapi/message
	// (V6-07), never a second one rooted elsewhere.
	ArtifactStore ports.ArtifactStore
}
