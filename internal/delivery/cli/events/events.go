package events

import (
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// Default tuning values applied whenever the corresponding Dependencies
// field is left at its zero value — mirrors
// internal/delivery/httpapi/eventstream.Dependencies's own identical
// defaulting shape (eventstream.go), adapted to this package's own smaller
// set of concerns (no heartbeat, no buffer size, no write timeout — see
// doc.go for why none of those apply to a CLI's own stdout writer).
const (
	defaultPollInterval       = time.Second
	defaultPollBatchLimit     = 200
	defaultRetentionScanLimit = 1000
)

// Dependencies is everything RunWatch needs — the CLI-side counterpart of
// internal/delivery/httpapi/eventstream.Dependencies, minus every field
// that only makes sense for a real HTTP/SSE connection (Shutdown,
// HeartbeatInterval, BufferSize, WriteTimeout — see doc.go's own "No
// bounded-channel" and "Heartbeats" sections for why). A future composition
// root (V6-15O) constructs one from its own already-built
// ports.UnitOfWork/redact.Matcher and passes it into RunWatch; this package
// never reaches for a global or constructs either itself.
type Dependencies struct {
	// UoW is the one real ports.UnitOfWork every poll tick reloads the
	// authoritative Project and scans the global journal through.
	UoW ports.UnitOfWork
	// Matcher is the SAME process-lifetime redact.Matcher every other
	// redacting leaf in this composition root reuses (see doc.go's own
	// "Wire shape and redaction" section) — never a second, differently
	// scoped one. The zero value is safe (redact.Matcher{} matches no known
	// secret), mirroring internal/delivery/cli/message.Dependencies.Matcher's
	// own identical doc comment.
	Matcher redact.Matcher
	// PollInterval is how often an already-running watch polls ScanJournal
	// for new events past its own current cursor, and re-checks
	// authorization (doc.go's own "Re-authorization" section). Zero uses
	// defaultPollInterval. A per-invocation --poll-interval flag, when set,
	// overrides this.
	PollInterval time.Duration
	// PollBatchLimit bounds how many journal rows one steady-state poll
	// tick scans (ports.EventsRepository.ScanJournal's own limit parameter)
	// — mirrors eventstream.Dependencies.PollBatchLimit's identical role.
	// Zero uses defaultPollBatchLimit.
	PollBatchLimit int
	// RetentionScanLimit bounds this package's own resync/retention policy
	// — see doc.go's own "Resync / retention policy" section. Zero uses
	// defaultRetentionScanLimit.
	RetentionScanLimit int
}

func (d Dependencies) pollInterval() time.Duration {
	if d.PollInterval > 0 {
		return d.PollInterval
	}
	return defaultPollInterval
}

func (d Dependencies) pollBatchLimit() int {
	if d.PollBatchLimit > 0 {
		return d.PollBatchLimit
	}
	return defaultPollBatchLimit
}

func (d Dependencies) retentionScanLimit() int {
	if d.RetentionScanLimit > 0 {
		return d.RetentionScanLimit
	}
	return defaultRetentionScanLimit
}

// appOpWatchProjectEvents is this package's own descriptor AppOperation —
// there is no real internal/app function this leaf calls (RunWatch is
// built directly on ports.EventsRepository.ScanJournal/
// ports.CatalogRepository.GetProject, per doc.go's own "independent
// reimplementation" reasoning), so this names the conceptual operation
// ADR-028 itself already calls out by this exact name ("SSE được biểu diễn
// bằng `aw events watch`"), matching HTTPOperationID's own real
// operationId (watchProjectEvents,
// internal/delivery/httpapi/eventstream/eventstream.go's own RegisterRoutes
// call) in spelling/casing convention.
const appOpWatchProjectEvents = "WatchProjectEvents"

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"events", "watch"}, Scope: cli.ScopeProject,
		AppOperation: appOpWatchProjectEvents, HTTPOperationID: "watchProjectEvents",
	})
}
