package eventstream

import (
	"context"
	"strconv"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// eventSink is the minimal write surface streamLoop needs — real production
// code passes httpEventSink (handler.go, backed by a real
// http.ResponseWriter/http.Flusher); a test passes a fake that can simulate
// a slow/blocking consumer deterministically, with no real socket involved
// (stream_internal_test.go). This seam is what makes the concurrency-heavy
// half of this task's own Verify bullets (slow client, bounded shutdown,
// heartbeat-never-advances-cursor) unit-testable in milliseconds instead of
// requiring real TCP backpressure.
type eventSink interface {
	WriteEvent(msg httpapi.SSEMessage) error
	WriteHeartbeat(comment string) error
}

// closeReason names why streamLoop stopped — returned for the caller's own
// logging/testing, and (for every reason except the two where nothing more
// can or should be sent — see below) included in the best-effort final
// stream.disconnected control event.
type closeReason string

const (
	reasonClientDisconnected closeReason = "client_disconnected"
	reasonServerShutdown     closeReason = "server_shutdown"
	reasonUnauthorized       closeReason = "unauthorized"
	reasonSlowClient         closeReason = "slow_client"
	reasonWriteFailed        closeReason = "write_failed"
)

// streamOutcome is streamLoop's own result — LastCursor is the exact
// JournalPosition of the last REAL event this connection actually,
// successfully wrote to the client (never advanced by a heartbeat, and
// never advanced past what genuinely reached the writer) — "so the client
// can reconnect and resume exactly from there" (this task's own
// Hoàn-thành-khi line).
type streamOutcome struct {
	LastCursor uint64
	Reason     closeReason
}

// outboundItem is one unit of work streamLoop's producer (the poll/
// heartbeat select loop) hands to its own writer goroutine over a bounded
// channel — the "bounded buffer" this task's own slow-client policy
// requires. Cursor is only meaningful when Heartbeat is false: a heartbeat
// item never carries or advances any cursor state (this task's own
// "Heartbeat has no event ID and never advances cursor" line, enforced here
// by construction — the writer goroutine below only ever updates its own
// lastWritten from a non-heartbeat item).
type outboundItem struct {
	heartbeat bool
	msg       httpapi.SSEMessage
	cursor    uint64
}

// streamLoop is this package's own from-scratch streaming connection
// lifecycle — no precedent exists anywhere else in
// internal/delivery/httpapi for a bounded-buffer/slow-client-disconnect/
// poll-and-heartbeat loop (confirmed by grep; sse.go only ever provides the
// low-level per-message write/heartbeat primitives this loop calls
// through sink).
//
// Two goroutines: THIS one (the caller's own goroutine — the real HTTP
// handler's own goroutine in production) runs the producer select loop
// (poll ScanJournal on pollTicker, re-authorize on every tick — see
// eventstream.go's own "Authorization" doc comment, send a heartbeat on
// heartbeatTicker, watch ctx/shutdown for a clean stop); a second goroutine
// this function starts is the sole writer — the only goroutine that ever
// calls sink.WriteEvent/WriteHeartbeat, so a real http.ResponseWriter (not
// safe for concurrent writes from multiple goroutines) is never touched
// from two places at once. The two communicate only through the bounded
// `ch` channel and the `stopWriter`/`writerDone` pair.
//
// initial is the already-scanned batch the caller's own retention probe
// (handler.go) performed BEFORE ever writing SSE headers — passed in
// rather than re-scanned here, so the exact same afterCursor window is
// never read from the journal twice and no event between "probe" and "poll
// loop starts" can be skipped or duplicated.
func streamLoop(ctx context.Context, deps Dependencies, projectID string, cursor uint64, initial []ports.JournalEvent, sink eventSink) streamOutcome {
	ch := make(chan outboundItem, deps.bufferSize())
	stopWriter := make(chan struct{})
	writerDone := make(chan struct{})

	var lastWritten = cursor
	go func() {
		defer close(writerDone)
		for {
			select {
			case item, ok := <-ch:
				if !ok {
					return
				}
				if item.heartbeat {
					if err := sink.WriteHeartbeat("keep-alive"); err != nil {
						return
					}
					continue
				}
				if err := sink.WriteEvent(item.msg); err != nil {
					return
				}
				lastWritten = item.cursor
			case <-stopWriter:
				return
			}
		}
	}()

	// enqueue is the ONE non-blocking send path every producer branch below
	// uses — a full channel means the writer goroutine has not kept up,
	// which is this package's own definition of "slow client" (see
	// eventstream.go's own BufferSize doc comment): the producer never
	// blocks waiting for room, it disconnects instead.
	enqueue := func(item outboundItem) bool {
		select {
		case ch <- item:
			return true
		default:
			return false
		}
	}

	localCursor := cursor

	// process applies one already-scanned batch: advance the local scan
	// cursor past EVERY event (including a foreign-project one — mirrors
	// internal/app/projection's own live consumer, which advances its scan
	// cursor past a foreign-project position "without row changes"), but
	// only ever enqueue a summary for one that belongs to projectID. It
	// returns false the instant a foreign-or-own event cannot be enqueued
	// (buffer full) — the caller must treat that as reasonSlowClient and
	// stop.
	process := func(events []ports.JournalEvent) bool {
		for _, event := range events {
			localCursor = event.JournalPosition
			if event.ProjectID != projectID {
				continue
			}
			summary := ProjectEventSummary{
				JournalPosition: event.JournalPosition,
				EventType:       deps.Matcher.String(event.EventType),
				SchemaVersion:   event.SchemaVersion,
			}
			item := outboundItem{
				msg: httpapi.SSEMessage{
					ID: strconv.FormatUint(event.JournalPosition, 10), Event: eventNameProjectInvalidated, Data: summary,
				},
				cursor: event.JournalPosition,
			}
			if !enqueue(item) {
				return false
			}
		}
		return true
	}

	stop := func(r closeReason) streamOutcome {
		// Best-effort final control event — never for reasonClientDisconnected
		// (the client is already gone, nothing to tell it) or
		// reasonWriteFailed (the writer goroutine's own last call already
		// failed; a working connection genuinely is not there to notify).
		// A non-blocking enqueue: when the channel happens to be full (e.g.
		// concurrently with a reasonSlowClient decision) this is correctly
		// undeliverable — the returned streamOutcome.LastCursor is still
		// accurate regardless of whether the wire notice itself got
		// through.
		if r != reasonClientDisconnected && r != reasonWriteFailed {
			enqueue(outboundItem{msg: httpapi.SSEMessage{
				Event: eventNameDisconnected,
				Data:  disconnectedPayload{Reason: string(r), LastCursor: localCursor},
			}})
		}
		close(stopWriter)
		<-writerDone
		return streamOutcome{LastCursor: lastWritten, Reason: r}
	}

	if !process(initial) {
		return stop(reasonSlowClient)
	}

	pollTicker := time.NewTicker(deps.pollInterval())
	defer pollTicker.Stop()
	heartbeatTicker := time.NewTicker(deps.heartbeatInterval())
	defer heartbeatTicker.Stop()
	shutdownDone := deps.shutdownDone()

	for {
		select {
		case <-ctx.Done():
			return stop(reasonClientDisconnected)
		case <-shutdownDone:
			return stop(reasonServerShutdown)
		case <-writerDone:
			return streamOutcome{LastCursor: lastWritten, Reason: reasonWriteFailed}
		case <-heartbeatTicker.C:
			if !enqueue(outboundItem{heartbeat: true}) {
				return stop(reasonSlowClient)
			}
		case <-pollTicker.C:
			events, err := probeProject(ctx, deps.UnitOfWork, projectID, localCursor, deps.pollBatchLimit())
			if err != nil {
				return stop(reasonUnauthorized)
			}
			if !process(events) {
				return stop(reasonSlowClient)
			}
		}
	}
}
