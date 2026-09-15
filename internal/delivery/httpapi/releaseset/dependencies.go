package releaseset

import (
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// Dependencies is everything RegisterRoutes' own handlers need — a
// composition root builds exactly one of these and passes it to
// RegisterRoutes; nothing in this package ever reaches for a package-level
// global. Mirrors internal/delivery/httpapi/workitem.Dependencies' own
// identical shape (a UnitOfWork, an ID source and a Clock — no HTTP
// server/listener concern belongs here, that stays composition-root/V6-12
// territory).
type Dependencies struct {
	// UnitOfWork is the one real ports.UnitOfWork every handler in this
	// package dispatches through — this package never opens a second,
	// competing persistence path (Hard rule: real ports.UnitOfWork, real
	// sqlite adapters, never a direct row fabrication).
	UnitOfWork ports.UnitOfWork
	// IDs mints every new aggregate ID a create-shaped command needs
	// (CreateReleaseSet/RequestReleaseSetLocalCommit both take an
	// idsource.Source themselves). A composition root supplies
	// idsource.Random{} in production; a test supplies idsource.Sequential
	// for deterministic replay assertions.
	IDs idsource.Source
	// Clock supplies cmd.RequestedAt for every command envelope this package
	// builds — internal/app/clock's own "no caller reaches for time.Now()
	// directly" discipline (a composition root supplies clock.System{}, a
	// test supplies clock.NewFixed).
	Clock clock.Clock
}
