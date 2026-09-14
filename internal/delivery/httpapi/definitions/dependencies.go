package definitions

import (
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// Dependencies is everything RegisterRoutes' own handlers need — mirrors
// internal/delivery/httpapi/workitem.Dependencies exactly (a UnitOfWork, an
// ID source and a Clock; no HTTP server/listener concern belongs here).
type Dependencies struct {
	// UnitOfWork is the one real ports.UnitOfWork every handler in this
	// package dispatches through.
	UnitOfWork ports.UnitOfWork
	// IDs mints every fresh candidate VersionID a validate/publish route
	// needs (internal/app/definitions.ValidateDraft/PublishDefinitionVersion
	// never mint one themselves — every existing caller, CLI included,
	// always supplies an already-minted VersionID; see
	// cmd/aw/definition.go's own candidateVersionID). A composition root
	// supplies idsource.Random{} in production; a test supplies
	// idsource.Sequential for deterministic assertions.
	IDs idsource.Source
	// Clock supplies cmd.RequestedAt/PublishedAt for every command envelope
	// and compiled candidate this package builds.
	Clock clock.Clock
}
