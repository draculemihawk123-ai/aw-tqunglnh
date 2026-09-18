// Package events is V6-15N's own CLI leaf, Part 2
// (docs/design/08-v6-api-projections.md:783-791; ADR-028's own "SSE được
// biểu diễn bằng `aw events watch`", docs/architecture/02-architecture-
// decisions.md:725-727): `aw events watch` — a terminal-native, NDJSON
// observer of the same redacted, project-scoped domain-event journal
// internal/delivery/httpapi/eventstream (V6-11) exposes over SSE.
//
// # Why this is an independent reimplementation, not a wrapper
//
// internal/delivery/httpapi/eventstream's own probeProject/streamLoop/
// eventSink are all unexported and tightly coupled to SSE wire shapes
// (httpapi.SSEMessage) — nothing in that package is callable from here, and
// ADR-028's own line above confirms this is intentional: a terminal process
// has no reason to make an HTTP call to itself, and parsing SSE inside a
// CLI is a worse fit than direct ports.EventsRepository.ScanJournal
// polling. This package therefore builds its own ~15-line probe (watch.go's
// own probeProjectEvents, a deliberate close mirror of eventstream/errors.go's
// probeProject — same shape, independently written, read there first) and
// its own poll loop directly on the one primitive both packages share:
// ports.EventsRepository.ScanJournal (internal/app/ports/unitofwork.go)
// GLOBAL across every project, filtered client-side exactly like
// eventstream/stream.go's own process closure does — advance the local scan
// cursor past every row (including a foreign-project one) but only ever
// emit a line for the one matching --project-id.
//
// # Wire shape and redaction
//
// Every emitted line is this package's own ProjectEventSummary
// ({journalPosition, eventType, schemaVersion} — never PayloadJSON, the
// same "invalidation signal, never a content feed" scope line
// eventstream.go's own doc comment states) — a deliberate, independent
// duplicate of eventstream.ProjectEventSummary rather than an import of
// that package (see above: importing it at all is architecturally the
// wrong direction, even though that one struct happens to be exported).
// EventType is routed through the shared redact.Matcher
// (Dependencies.Matcher — the SAME process-lifetime instance every other
// redacting leaf/handler in this composition root reuses, e.g.
// internal/delivery/cli/run.Dependencies.Matcher and
// internal/delivery/httpapi/eventstream.Dependencies.Matcher) before it is
// ever written — defense in depth, identical reasoning to eventstream's own.
//
// # No bounded-channel/slow-client-disconnect architecture
//
// internal/delivery/httpapi/eventstream/stream.go's own two-goroutine/
// bounded-buffer/active-disconnect design exists because an HTTP SERVER
// must proactively protect itself from a slow, external, untrusted BROWSER
// client it cannot otherwise control. This package has no such concern: it
// is a single process writing NDJSON to its OWN stdout — if whatever
// consumes that stdout (e.g. piped into `jq`) is slow, the OS pipe's own
// backpressure naturally blocks the next Write call, which is normal,
// correct, and simpler than inventing a disconnect policy this CLI has no
// reason to need. RunWatch is therefore a single loop in the CALLER's own
// goroutine — no writer goroutine, no channel, nothing to leak: a
// context-cancellation is observed directly in the same select loop that
// drives polling, so "no goroutine leak" holds by construction (there is no
// second goroutine to leak in the first place), not by an explicit
// leak-detector dependency.
//
// # Heartbeats
//
// Deliberately NONE. eventstream's own HeartbeatInterval exists to defeat
// an idle-connection timeout somewhere on the SSE wire path (a browser, a
// reverse proxy) — a concern this package's own stdout pipe never has. A
// heartbeat NDJSON line would also break this task's own single most
// important guarantee ("every line is a ProjectEventSummary a downstream
// `jq` consumer can rely on") for zero benefit. Re-authorization instead
// piggybacks on the SAME poll tick that scans for new events (see below) —
// there is no separate cadence to reason about.
//
// # Re-authorization
//
// Every probe — the first one (at whatever --from-cursor the operator
// started from) and every subsequent poll tick of an already-running watch
// — reloads the Project fresh via tx.Catalog().GetProject and requires
// project.ProjectActive, mirroring eventstream.go's own "Authorization" doc
// comment exactly, including its own admission that no production command
// in this codebase can currently move a Project out of ACTIVE: this
// invariant ("only ever show events for a project currently authorized")
// should hold regardless of why authorization might change mid-session, so
// this package pays the identical re-check cost eventstream already does,
// for the identical forward-looking reason.
//
// # Resync / retention policy for --from-cursor
//
// Mirrors eventstream.go's own "Retention policy" doc comment, adapted to a
// CLI's own vocabulary (a typed Go error, ErrResyncRequired, rather than an
// HTTP 409): on start, this package probes
// ScanJournal(afterPosition=fromCursor, limit=RetentionScanLimit+1); if
// more than RetentionScanLimit events are already pending (GLOBAL across
// every project — ScanJournal's own contract), RunWatch returns
// ErrResyncRequired instead of ever starting to poll. The intended operator
// reaction is identical to the HTTP stream's own: re-fetch a fresh
// authoritative/projected cursor (e.g. from `aw projection status`'s own
// Cursor field) and reconnect with a fresh --from-cursor, never keep
// retrying the same stale one.
//
// --from-cursor itself defaults to 0 when omitted — DELIBERATELY DIFFERENT
// from eventstream's own HTTP route, which REQUIRES an explicit `cursor`
// query parameter (eventstream.go's own "Cursor design" doc comment: a
// browser client always arrives there already holding an
// AsOfJournalPosition from a prior fetch response, so an implicit default
// would silently reintroduce a query→subscribe race). An `aw events watch`
// invocation has no such prior "fetch" step of its own — there is no
// UI-fetch-then-subscribe race for a bare terminal command to reintroduce —
// so defaulting a first, cursor-less invocation to "replay everything
// currently in the journal, then follow live" (afterPosition=0, which can
// never collide with a real JournalPosition — those start at 1) is the
// least-surprising CLI default, not a silently-swallowed correctness gap.
// An operator who DOES hold a prior observed cursor (e.g. from `aw
// projection status`) passes it via --from-cursor exactly like a browser
// client would pass one via `cursor`.
//
// # Duplicate / reconnect correctness
//
// This package keeps no server-side cursor state of its own between
// process invocations — restarting `aw events watch --from-cursor=<n>`
// after a prior run is the CLI's own equivalent of an SSE client
// reconnecting with its own last-observed cursor. ScanJournal's own
// contract (JournalPosition > afterPosition, strictly) is what makes a
// restart with the prior run's own last-emitted JournalPosition neither
// re-emit that event nor skip the next one — proved directly in
// watch_test.go by capturing one run's own last emitted cursor and
// asserting a second run started from it picks up exactly where the first
// left off.
//
// This package never wires itself into cmd/aw (V6-15O's own job): it only
// registers its own cli.Descriptor via its own init(), and exposes RunWatch
// for a future composition root to call once that wiring exists.
package events
