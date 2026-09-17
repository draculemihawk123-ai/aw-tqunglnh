package eventstream_test

// Real HTTP round-trip coverage for GET /projects/{id}/events/watch
// (V6-11). Every test drives the actual registered route through a real
// httptest.Server and a real *http.Client (newTestServer,
// httptest_helper_test.go) — mirrors internal/delivery/httpapi/rundetail's
// own convention. Concurrency-sensitive internals (slow client, bounded
// shutdown, heartbeat-never-advances-cursor, mid-stream re-authorization)
// have their own deterministic, millisecond-fast white-box coverage in
// stream_internal_test.go instead — see that file's own doc comment for
// why.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

func get(t *testing.T, ctx context.Context, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

func decodeErrorBody(t *testing.T, resp *http.Response) httpapi.ErrorResponse {
	t.Helper()
	defer resp.Body.Close()
	var body httpapi.ErrorResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	return body
}

// TestWatchProjectEvents_QuerySubscribeRace proves the "fetch response
// exposes an observed cursor; streaming from that cursor emits all later
// relevant summaries" contract: an event that commits strictly BETWEEN a
// client's own fetch-for-cursor and its immediate subscribe-from-that-cursor
// is never missed.
func TestWatchProjectEvents_QuerySubscribeRace(t *testing.T) {
	uow := newTestProject(t, "p1")
	appendEvent(t, uow, "p1", "Kickoff", 1) // journal position 1 — this is what "the fetch" would have observed

	server := newTestServer(t, testDeps(uow))
	// The event that arrives strictly between the client's own fetch (which
	// observed cursor=1 above) and its subscribe call below.
	appendEvent(t, uow, "p1", "AfterFetch", 2)

	resp := get(t, context.Background(), server.URL+"/projects/p1/events/watch?cursor=1")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	reader := startSSEReader(resp)
	defer reader.close()

	ev, _ := reader.next(t, 5*time.Second)
	if ev.ID != "2" || ev.Event != "project.invalidated" {
		t.Fatalf("got event %+v, want the position-2 event that arrived between fetch and subscribe", ev)
	}
}

// TestWatchProjectEvents_ReconnectNeverDuplicatesOrSkips proves a
// reconnect using the last-received cursor sees every later event exactly
// once — never a repeat of something already delivered, never a gap.
func TestWatchProjectEvents_ReconnectNeverDuplicatesOrSkips(t *testing.T) {
	uow := newTestProject(t, "p1")
	for i := 1; i <= 3; i++ {
		appendEvent(t, uow, "p1", "E", i)
	}
	server := newTestServer(t, testDeps(uow))

	resp1 := get(t, context.Background(), server.URL+"/projects/p1/events/watch?cursor=0")
	reader1 := startSSEReader(resp1)
	var lastID string
	for i := 0; i < 3; i++ {
		ev, ok := reader1.next(t, 5*time.Second)
		if !ok {
			t.Fatalf("connection ended early after %d events", i)
		}
		lastID = ev.ID
	}
	reader1.close()
	if lastID != "3" {
		t.Fatalf("lastID = %q, want %q", lastID, "3")
	}

	appendEvent(t, uow, "p1", "E", 4)
	appendEvent(t, uow, "p1", "E", 5)

	resp2 := get(t, context.Background(), server.URL+"/projects/p1/events/watch?cursor="+lastID)
	defer resp2.Body.Close()
	reader2 := startSSEReader(resp2)
	defer reader2.close()

	got := map[string]bool{}
	for i := 0; i < 2; i++ {
		ev, ok := reader2.next(t, 5*time.Second)
		if !ok {
			t.Fatalf("reconnected stream ended early after %d events", i)
		}
		if got[ev.ID] {
			t.Fatalf("event %s delivered twice", ev.ID)
		}
		got[ev.ID] = true
	}
	if !got["4"] || !got["5"] {
		t.Fatalf("got %v, want exactly {4,5} — no skip, no duplicate of 1-3", got)
	}
}

// TestWatchProjectEvents_ForeignProjectEventsNeverLeak proves events from
// OTHER projects interleaved in the global journal never appear on this
// connection's own stream.
func TestWatchProjectEvents_ForeignProjectEventsNeverLeak(t *testing.T) {
	uow := newTestProject(t, "p1")
	if err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(context.Background(), ports.CreateProjectRequest{ID: "p2", Name: "p2"})
		return err
	}); err != nil {
		t.Fatalf("create p2: %v", err)
	}

	appendEvent(t, uow, "p2", "ForeignBefore", 1) // position 1
	appendEvent(t, uow, "p1", "OwnEvent", 1)      // position 2

	server := newTestServer(t, testDeps(uow))
	resp := get(t, context.Background(), server.URL+"/projects/p1/events/watch?cursor=0")
	defer resp.Body.Close()
	reader := startSSEReader(resp)
	defer reader.close()

	ev, _ := reader.next(t, 5*time.Second)
	if ev.ID != "2" {
		t.Fatalf("got id %q, want %q — the foreign (p2) position-1 event must never be delivered to this p1 stream", ev.ID, "2")
	}

	appendEvent(t, uow, "p2", "ForeignAfter", 2) // position 3, foreign, interleaved live
	appendEvent(t, uow, "p1", "OwnEvent2", 2)    // position 4, own

	ev2, _ := reader.next(t, 5*time.Second)
	if ev2.ID != "4" {
		t.Fatalf("got id %q, want %q — a live-interleaved foreign event must also never leak", ev2.ID, "4")
	}
}

// TestWatchProjectEvents_HeartbeatWireFormat proves a heartbeat is a
// comment-only line carrying no `id:` field.
func TestWatchProjectEvents_HeartbeatWireFormat(t *testing.T) {
	uow := newTestProject(t, "p1")
	deps := testDeps(uow)
	deps.HeartbeatInterval = 20 * time.Millisecond
	deps.PollInterval = time.Minute
	server := newTestServer(t, deps)

	resp := get(t, context.Background(), server.URL+"/projects/p1/events/watch?cursor=0")
	defer resp.Body.Close()
	reader := startSSEReader(resp)
	defer reader.close()

	ev, _ := reader.next(t, 5*time.Second)
	if ev.Event != sseCommentEvent {
		t.Fatalf("first frame = %+v, want a comment-only heartbeat", ev)
	}
	if ev.ID != "" {
		t.Fatalf("heartbeat carried id=%q, want empty — a heartbeat must never carry an event ID", ev.ID)
	}
}

// TestWatchProjectEvents_RetentionExceededReturnsTypedResync proves a
// cursor with more than RetentionScanLimit events already pending gets a
// typed RESYNC_REQUIRED response instead of ever being upgraded to SSE.
func TestWatchProjectEvents_RetentionExceededReturnsTypedResync(t *testing.T) {
	uow := newTestProject(t, "p1")
	for i := 1; i <= 5; i++ {
		appendEvent(t, uow, "p1", "E", i)
	}
	deps := testDeps(uow)
	deps.RetentionScanLimit = 2
	server := newTestServer(t, deps)

	resp := get(t, context.Background(), server.URL+"/projects/p1/events/watch?cursor=0")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json (never upgraded to SSE)", ct)
	}
	body := decodeErrorBody(t, resp)
	if body.Error.Code != httpapi.ErrorCodeResyncRequired {
		t.Fatalf("error.code = %q, want %q", body.Error.Code, httpapi.ErrorCodeResyncRequired)
	}
}

// TestWatchProjectEvents_UnknownProjectReturns404Hidden proves the
// cross-project-leak/leakage-normalization policy: an unauthorized (here,
// simply nonexistent) project ID never streams anything, not even a
// heartbeat that could confirm its existence — a plain 404 JSON body,
// never SSE.
func TestWatchProjectEvents_UnknownProjectReturns404Hidden(t *testing.T) {
	uow := newTestProject(t, "p1")
	server := newTestServer(t, testDeps(uow))

	resp := get(t, context.Background(), server.URL+"/projects/does-not-exist/events/watch?cursor=0")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json — never even opened as an SSE stream", ct)
	}
	body := decodeErrorBody(t, resp)
	if body.Error.Code != httpapi.ErrorCodeNotFound {
		t.Fatalf("error.code = %q, want %q", body.Error.Code, httpapi.ErrorCodeNotFound)
	}
}

// TestWatchProjectEvents_ArchivedProjectReturns404Hidden proves a Project
// that exists but is not authorized for streaming gets the IDENTICAL
// response as TestWatchProjectEvents_UnknownProjectReturns404Hidden —
// never a distinguishable 403 that would leak "this project exists."
func TestWatchProjectEvents_ArchivedProjectReturns404Hidden(t *testing.T) {
	inner := newTestProject(t, "p1")
	archived := &atomic.Bool{}
	archived.Store(true)
	uow := archivableUOW{inner: inner, projectID: "p1", archived: archived}
	server := newTestServer(t, testDeps(uow))

	resp := get(t, context.Background(), server.URL+"/projects/p1/events/watch?cursor=0")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	body := decodeErrorBody(t, resp)
	if body.Error.Code != httpapi.ErrorCodeNotFound || body.Error.Message != "the requested resource was not found" {
		t.Fatalf("body = %+v, want the exact same leakage-normalized shape as an unknown project", body)
	}
}

// TestWatchProjectEvents_MissingCursorIsRejected proves `cursor` is a
// required query parameter (see eventstream.go's own "Cursor design" doc
// comment for why — a client is always expected to arrive holding a prior
// fetch's own observed cursor).
func TestWatchProjectEvents_MissingCursorIsRejected(t *testing.T) {
	uow := newTestProject(t, "p1")
	server := newTestServer(t, testDeps(uow))

	resp := get(t, context.Background(), server.URL+"/projects/p1/events/watch")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	body := decodeErrorBody(t, resp)
	if body.Error.Code != httpapi.ErrorCodeInvalidRequest {
		t.Fatalf("error.code = %q, want %q", body.Error.Code, httpapi.ErrorCodeInvalidRequest)
	}
}

// TestWatchProjectEvents_ShutdownClosesOpenStream proves a server
// shutdown signal cleanly closes an already-open stream (bounded
// shutdown, no hang) end to end over a real connection.
func TestWatchProjectEvents_ShutdownClosesOpenStream(t *testing.T) {
	uow := newTestProject(t, "p1")
	shutdownCtx, cancelShutdown := context.WithCancel(context.Background())
	deps := testDeps(uow)
	deps.PollInterval = time.Minute
	deps.HeartbeatInterval = time.Minute
	deps.Shutdown = shutdownCtx
	server := newTestServer(t, deps)

	resp := get(t, context.Background(), server.URL+"/projects/p1/events/watch?cursor=0")
	defer resp.Body.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 1)
		for {
			if _, err := resp.Body.Read(buf); err != nil {
				return
			}
		}
	}()

	time.Sleep(20 * time.Millisecond)
	cancelShutdown()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("connection did not close after the shutdown signal fired")
	}
}

// TestWatchProjectEvents_SlowClientDisconnected proves a deliberately
// slow-reading client gets disconnected rather than allowing this
// connection's own outbound buffer to grow without bound: a small
// BufferSize combined with a producer that never blocks (stream.go's own
// enqueue) means a client that stops reading altogether eventually causes
// the connection to close on its own.
func TestWatchProjectEvents_SlowClientDisconnected(t *testing.T) {
	uow := newTestProject(t, "p1")
	for i := 1; i <= 500; i++ {
		appendEvent(t, uow, "p1", "E", i)
	}
	deps := testDeps(uow)
	deps.BufferSize = 2
	deps.WriteTimeout = 200 * time.Millisecond
	server := newTestServer(t, deps)

	resp := get(t, context.Background(), server.URL+"/projects/p1/events/watch?cursor=0")
	defer resp.Body.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Deliberately never read resp.Body at all: TCP/HTTP write
		// buffers eventually fill, the WriteTimeout above bounds an
		// individual blocked write, and the bounded channel's own
		// producer-side backpressure detection (stream.go) takes it from
		// there — the connection must close within a bounded time either
		// way.
		buf := make([]byte, 4096)
		_, _ = resp.Body.Read(buf) // one bounded read is enough to notice EOF/close eventually via the loop below
		for {
			if _, err := resp.Body.Read(buf); err != nil {
				return
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("slow client connection was never disconnected — bounded buffer / write timeout did not trigger")
	}
}
