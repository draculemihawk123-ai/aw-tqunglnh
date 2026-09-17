// Package eventstream is V6-11's own HTTP surface for the redacted,
// project-scoped SSE invalidation/runtime-summary stream
// (docs/design/08-v6-api-projections.md V6-11: "WatchProjectEvents, replay
// cursor, heartbeat, retention/resync và slow-client policy"). It owns its
// own subpackage under internal/delivery/httpapi, mirroring
// internal/delivery/httpapi/evidence (V6-07B) and
// internal/delivery/httpapi/rundetail (V6-06B)'s own "mỗi endpoint task sở
// hữu subpackage, descriptor/schema fragment và test riêng" discipline
// (contract point 8).
//
// Route: GET /projects/{id}/events/watch?cursor=<uint64>   watchProjectEvents
//
// # What this stream carries
//
// Every real event on the wire is a ProjectEventSummary — {journalPosition,
// eventType, schemaVersion} only. This is deliberately an INVALIDATION
// signal, never a content feed: the task's own "Không làm" line ("no
// artifact bodies/log bodies/secrets... no browser-side event-cache
// authority") means a client reacts to a summary by re-fetching whatever
// authoritative/projected resource that eventType actually concerns, never
// by trying to reconstruct state from the stream itself. Every summary's
// EventType is still routed through the shared redact.Matcher before it
// ever reaches httpapi.WriteSSE (Dependencies.Matcher — the SAME
// process-lifetime instance internal/delivery/httpapi/rundetail and every
// other redacting route in this composition root already reuses, never a
// second, differently-scoped one) — defense in depth even though the wire
// shape carries no free-text payload field a secret could hide inside.
//
// # Cursor design (documented per this task's own instruction)
//
// The cursor is a plain, unsigned, base-10 decimal JournalPosition passed
// as a `cursor` query parameter — NOT httpapi.CursorCodec's opaque,
// tamper-evident token. CursorCodec exists to stop a client hand-editing an
// opaque PAGING walk's own ProjectID/Generation/LastKey to see another
// project's rows or replay a stale generation (cursor.go's own doc
// comment) — a different shape of problem than this endpoint's own "resume
// a live event tail from an exact global journal position." A JournalPosition
// is a same-process, same-database observation (V6-08's own "Cursor is
// greatest scanned global JournalPosition" — the exact value every
// projection-backed read response already exposes verbatim as
// Freshness.AsOfJournalPosition, httpapi/freshness.go), not a credential:
// nothing is gained by signing it, and a client that hand-edits it to a
// larger value only ever skips its OWN missed events (never sees another
// project's data, since every event is still re-filtered by ProjectID
// server-side on every tick) — the one thing CursorCodec's signature
// actually defends against for a query cursor does not apply here. `cursor`
// is REQUIRED: this stream's own "Fetch response exposes an observed
// cursor" line (the design doc's own flow) means a client is always
// expected to arrive here holding an AsOfJournalPosition from a prior
// query response (e.g. a Kanban board fetch) — accepting an implicit
// "start from nothing"/"start from now" default would silently reintroduce
// the exact query→subscribe race this task's own Verify bullet exists to
// close.
//
// # Retention policy (documented per this task's own instruction)
//
// domain_events is never pruned anywhere in this codebase (grep confirms
// no DELETE FROM domain_events exists) — so "too old" is never a hard
// data-availability limit here, unlike a real log-retention window. It is
// instead a deliberate RESOURCE-BOUND policy: on every open/reopen this
// package probes ScanJournal(afterPosition=cursor,
// limit=RetentionScanLimit+1) — a bounded read, never an unbounded catch-up
// scan — and if more than RetentionScanLimit events are already pending
// (across every project; ScanJournal is global), it returns a typed
// RESYNC_REQUIRED response instead of ever upgrading the connection to SSE.
// The intended client reaction is "re-fetch full authoritative state and
// retry with a fresh AsOfJournalPosition," never "keep retrying the same
// stale cursor" — the same policy this package's own doc comment above
// already commits this stream to (no browser-side event-cache authority: a
// client that has been offline long enough to accumulate a huge backlog
// should trust a fresh snapshot over a huge live replay burst).
//
// # Authorization (documented per this task's own instruction)
//
// Every open/reopen — and every single poll tick of an already-open stream
// — reloads the Project fresh from CatalogRepository.GetProject (never a
// cached copy) and requires project.ProjectActive; anything else (not
// found, or a Status this package does not recognize as authorized) writes
// the identical httpapi.WriteResourceHidden 404 the rest of this
// composition root already uses for "not found or not visible in caller's
// scope" (V6-02A's own leakage-normalization policy) — a probing client can
// never distinguish "this project does not exist" from "this project
// exists but you may not watch it," and an already-open connection that
// loses that authorization on a LATER poll tick stops delivering
// immediately, not merely refuses the NEXT connection attempt. No
// production command in this codebase can currently move a Project out of
// ACTIVE (there is no ArchiveProject/TransitionProject mutator anywhere —
// confirmed by grep), so this package's own re-authorization mechanism has
// no real trigger to exercise end-to-end today; it exists so that whichever
// future task adds one needs no change here at all, and this package's own
// test suite exercises the mechanism directly (a wrapped
// ports.CatalogRepository whose GetProject answer changes mid-test stands
// in for that future capability) rather than skipping coverage for a
// capability this codebase does not yet expose a real command for.
package eventstream

import (
	"context"
	"net/http"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// Default tuning values applied whenever the corresponding Dependencies
// field is left at its zero value — every one of them is a resource/latency
// trade-off this package owns, never a caller-configurable wire contract
// (contrast httpapi.MaxBytes/Config.MaxBodyBytes, which the composition
// root does configure per-process).
const (
	defaultPollInterval       = 500 * time.Millisecond
	defaultHeartbeatInterval  = 15 * time.Second
	defaultPollBatchLimit     = 200
	defaultRetentionScanLimit = 1000
	defaultBufferSize         = 64
	defaultWriteTimeout       = 10 * time.Second
)

// Dependencies is everything this package's own handler needs.
type Dependencies struct {
	// UnitOfWork is the one real ports.UnitOfWork every poll tick reloads
	// the authoritative Project and scans the global journal through.
	UnitOfWork ports.UnitOfWork
	// Matcher is the SAME process-lifetime redact.Matcher every other
	// redacting route in this composition root reuses (see
	// internal/delivery/httpapi/rundetail.Dependencies' own Matcher doc
	// comment) — never a second, differently-scoped one.
	Matcher redact.Matcher
	// Shutdown, when non-nil, is a context this package watches on every
	// open stream's own select loop alongside the request's own
	// r.Context() — cancel it to signal every currently-open stream to close
	// itself promptly. A composition root passes the SAME context serve()
	// already watches for SIGINT/SIGTERM (cmd/aw/serve.go), cancelled
	// BEFORE (or concurrently with) calling httpapi.Server.Shutdown: plain
	// net/http.Server.Shutdown does not itself cancel an in-flight
	// handler's r.Context() (it only stops accepting new connections and
	// waits for active ones to finish on their own — see net/http's own
	// Shutdown doc comment), so a long-lived streaming handler that only
	// ever watched r.Context() would make Shutdown hang until its own
	// ctx/timeout gives up, leaving the handler goroutine running in the
	// background regardless. Left nil (e.g. a caller that does not care
	// about graceful shutdown), every open stream simply never observes a
	// shutdown signal — r.Context() (real client disconnect) is still
	// always watched regardless.
	Shutdown context.Context

	// PollInterval is how often an open stream polls ScanJournal for new
	// events past its own current cursor. Zero uses defaultPollInterval.
	PollInterval time.Duration
	// HeartbeatInterval is how often an open stream sends a comment-only
	// keep-alive (httpapi.WriteSSEComment) independent of whether real
	// events are flowing, so an idle-but-alive connection is not killed by
	// a client/proxy idle timeout. Zero uses defaultHeartbeatInterval.
	HeartbeatInterval time.Duration
	// PollBatchLimit bounds how many journal rows one steady-state poll
	// tick scans (ports.EventsRepository.ScanJournal's own limit
	// parameter) — mirrors internal/app/projection.ApplyBatchRequest.BatchSize's
	// identical role for the live projection consumer, V6-08A's own first
	// production reader of this same primitive. Zero uses
	// defaultPollBatchLimit.
	PollBatchLimit int
	// RetentionScanLimit bounds this package's own resource-bound
	// retention policy — see this package's own doc comment ("Retention
	// policy") above for the full reasoning. Zero uses
	// defaultRetentionScanLimit.
	RetentionScanLimit int
	// BufferSize is the bounded per-connection outbound channel capacity —
	// the "bounded buffer" this task's own "slow-client policy" line
	// requires: once it is full, a producer that cannot enqueue one more
	// message treats the connection as a slow client and disconnects it
	// (stream.go's own streamLoop). Zero uses defaultBufferSize.
	BufferSize int
	// WriteTimeout bounds a single real socket write
	// (http.ResponseController.SetWriteDeadline, best-effort — silently
	// ignored for a ResponseWriter that does not support it, e.g. a test
	// httptest.ResponseRecorder) — defense in depth alongside BufferSize's
	// own channel-level backpressure detection, for the case where a
	// single write call itself blocks on a frozen socket rather than many
	// small writes merely outpacing a slow-but-not-frozen reader. Zero
	// uses defaultWriteTimeout.
	WriteTimeout time.Duration
}

func (d Dependencies) pollInterval() time.Duration {
	if d.PollInterval > 0 {
		return d.PollInterval
	}
	return defaultPollInterval
}

func (d Dependencies) heartbeatInterval() time.Duration {
	if d.HeartbeatInterval > 0 {
		return d.HeartbeatInterval
	}
	return defaultHeartbeatInterval
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

func (d Dependencies) bufferSize() int {
	if d.BufferSize > 0 {
		return d.BufferSize
	}
	return defaultBufferSize
}

func (d Dependencies) writeTimeout() time.Duration {
	if d.WriteTimeout > 0 {
		return d.WriteTimeout
	}
	return defaultWriteTimeout
}

// shutdownDone returns d.Shutdown's own Done() channel, or nil (which
// blocks forever in a select, i.e. "never signals shutdown") when Shutdown
// was left unset.
func (d Dependencies) shutdownDone() <-chan struct{} {
	if d.Shutdown == nil {
		return nil
	}
	return d.Shutdown.Done()
}

// ProjectEventSummary is the one `data` shape every real event on this
// stream carries — see this package's own doc comment ("What this stream
// carries") for why it is deliberately this minimal.
type ProjectEventSummary struct {
	JournalPosition uint64 `json:"journalPosition"`
	EventType       string `json:"eventType"`
	SchemaVersion   int    `json:"schemaVersion"`
}

// eventNameProjectInvalidated is every real event's own SSE `event:` field.
const eventNameProjectInvalidated = "project.invalidated"

// disconnectedPayload is the `data` shape of the one best-effort control
// message a closing stream sends before it stops (stream.go's own
// streamLoop) — event name eventNameDisconnected, no `id:` field (it names
// no journal position of its own; LastCursor inside the payload is what a
// client uses to reconnect losslessly).
type disconnectedPayload struct {
	Reason     string `json:"reason"`
	LastCursor uint64 `json:"lastCursor"`
}

const eventNameDisconnected = "stream.disconnected"

// RegisterRoutes registers this package's own one route fragment onto reg
// — a composition root (cmd/aw/serve.go) calls this once, alongside every
// sibling endpoint task's own RegisterRoutes (contract point 8: "Parallel
// work không sửa registry chung").
func RegisterRoutes(reg *httpapi.RouteRegistry, deps Dependencies) {
	reg.Register(httpapi.RouteDescriptor{
		Method: http.MethodGet, Path: "/projects/{id}/events/watch", OperationID: "watchProjectEvents",
		ScopeKind: httpapi.ScopeProject, RequestSchema: struct{}{}, ResponseSchema: ProjectEventSummary{},
		Handler: handleWatchProjectEvents(deps),
	})
}
