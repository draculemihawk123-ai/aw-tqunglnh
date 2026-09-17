package eventstream

// White-box coverage for streamLoop's own concurrency-sensitive behavior
// (stream.go) — deliberately package eventstream (not eventstream_test),
// unlike every sibling httpapi endpoint package's pure black-box
// httptest.Server convention: the slow-client/shutdown/re-authorization
// Verify bullets need a deterministic, millisecond-fast seam
// (streamLoop's own eventSink interface, and a wrapped fake
// ports.CatalogRepository) that a real socket cannot give without either
// genuine TCP backpressure (slow, flaky) or an infinitely-blocked write
// (which would make streamLoop itself hang — see fakeSink's own doc
// comment below for why it sleeps instead of blocking forever). Real
// HTTP-wire coverage (route registration, headers, JSON error envelopes,
// query→subscribe race, cross-project leak, resync) lives in
// eventstream_test.go instead, mirroring every sibling package.

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// fakeSink is eventSink's own test double. WriteEvent sleeps writeDelay
// before recording — deliberately a bounded SLEEP, never an unbounded
// block on a channel/gate that the test alone controls: streamLoop's own
// stop() unconditionally waits for the writer goroutine to actually return
// (<-writerDone), so a fake that could block a WriteEvent call forever
// would make a "slow client" test hang instead of proving disconnection —
// exactly the real-world case httpEventSink's own WriteTimeout
// (handler.go) exists to bound for a genuine frozen socket. A short fixed
// sleep instead lets a small BufferSize fill (and streamLoop detect it)
// long before this call returns, while still guaranteeing the writer
// goroutine always eventually drains/exits.
type fakeSink struct {
	mu         sync.Mutex
	writeDelay time.Duration
	events     []httpapi.SSEMessage
	heartbeats int
}

func (s *fakeSink) WriteEvent(msg httpapi.SSEMessage) error {
	if s.writeDelay > 0 {
		time.Sleep(s.writeDelay)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, msg)
	return nil
}

func (s *fakeSink) WriteHeartbeat(string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.heartbeats++
	return nil
}

func (s *fakeSink) snapshot() (events []httpapi.SSEMessage, heartbeats int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]httpapi.SSEMessage(nil), s.events...), s.heartbeats
}

var _ eventSink = (*fakeSink)(nil)

// archivableCatalog/archivableTx/archivableUOW let a test flip one
// Project's own authorized-for-streaming status (project.ProjectActive vs
// anything else) at an arbitrary moment DURING an already-running
// streamLoop, standing in for the "role/permission revoked mid-stream"
// capability this codebase has no real production command for yet (grep
// confirms no ArchiveProject/TransitionProject mutator exists anywhere —
// see eventstream.go's own "Authorization" doc comment). Plain Go
// interface embedding over the SAME underlying fake.UnitOfWork/ports.Tx —
// no change to the shared internal/app/ports/fake package itself.
type archivableCatalog struct {
	ports.CatalogRepository
	projectID string
	archived  *atomic.Bool
}

func (c archivableCatalog) GetProject(ctx context.Context, id string) (project.Project, error) {
	p, err := c.CatalogRepository.GetProject(ctx, id)
	if err != nil {
		return p, err
	}
	if id == c.projectID && c.archived.Load() {
		p.Status = project.ProjectArchived
	}
	return p, nil
}

type archivableTx struct {
	ports.Tx
	projectID string
	archived  *atomic.Bool
}

func (t archivableTx) Catalog() ports.CatalogRepository {
	return archivableCatalog{t.Tx.Catalog(), t.projectID, t.archived}
}

type archivableUOW struct {
	inner     ports.UnitOfWork
	projectID string
	archived  *atomic.Bool
}

func (u archivableUOW) WithReadOnly(ctx context.Context, fn func(ports.Tx) error) error {
	return u.inner.WithReadOnly(ctx, func(tx ports.Tx) error { return fn(archivableTx{tx, u.projectID, u.archived}) })
}

func (u archivableUOW) WithSerializedWrite(ctx context.Context, fn func(ports.Tx) error) error {
	return u.inner.WithSerializedWrite(ctx, func(tx ports.Tx) error { return fn(archivableTx{tx, u.projectID, u.archived}) })
}

var _ ports.UnitOfWork = archivableUOW{}

func newFakeProject(t *testing.T, id string) *fake.UnitOfWork {
	t.Helper()
	uow := fake.New()
	if err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(context.Background(), ports.CreateProjectRequest{ID: id, Name: id})
		return err
	}); err != nil {
		t.Fatalf("create test project: %v", err)
	}
	return uow
}

func appendTestEvents(t *testing.T, uow ports.UnitOfWork, projectID string, n int) {
	t.Helper()
	if err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		for i := 0; i < n; i++ {
			if err := tx.Events().Append(context.Background(), ports.DomainEvent{
				ID: fmt.Sprintf("evt-%s-%d", projectID, i), ProjectID: projectID,
				AggregateType: "TestAggregate", AggregateID: fmt.Sprintf("agg-%s-%d", projectID, i), Sequence: 1,
				EventType: "Test.Event", SchemaVersion: 1, PayloadJSON: "{}", CreatedAt: time.Now().UTC(),
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("append test events: %v", err)
	}
}

func TestStreamLoop_SlowClientDisconnectsWithLastSafeCursor(t *testing.T) {
	uow := newFakeProject(t, "p1")
	appendTestEvents(t, uow, "p1", 200)

	sink := &fakeSink{writeDelay: 20 * time.Millisecond}
	deps := Dependencies{UnitOfWork: uow, Matcher: redact.NewMatcher(), BufferSize: 2, PollInterval: time.Minute, HeartbeatInterval: time.Minute}

	initial, err := probeProject(context.Background(), uow, "p1", 0, 1000)
	if err != nil {
		t.Fatalf("probeProject: %v", err)
	}
	if len(initial) != 200 {
		t.Fatalf("initial = %d events, want 200", len(initial))
	}

	done := make(chan streamOutcome, 1)
	go func() { done <- streamLoop(context.Background(), deps, "p1", 0, initial, sink) }()

	var outcome streamOutcome
	select {
	case outcome = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("streamLoop did not return — a slow client must be disconnected, not hung forever")
	}

	if outcome.Reason != reasonSlowClient {
		t.Fatalf("Reason = %q, want %q", outcome.Reason, reasonSlowClient)
	}
	written, _ := sink.snapshot()
	if len(written) >= 200 {
		t.Fatalf("sink recorded all %d events — the bounded buffer never actually disconnected a slow client", len(written))
	}
	if outcome.LastCursor == 0 && len(written) > 0 {
		t.Fatal("LastCursor is 0 but at least one event was actually written — LastCursor must reflect the last successful write")
	}
	if len(written) > 0 {
		lastWrittenID := written[len(written)-1].ID
		if lastWrittenID != fmt.Sprint(outcome.LastCursor) {
			t.Fatalf("outcome.LastCursor = %d, want the last actually-written event's own ID %q", outcome.LastCursor, lastWrittenID)
		}
	}
}

func TestStreamLoop_AuthorizationRevokedMidStreamStopsDelivery(t *testing.T) {
	inner := newFakeProject(t, "p1")
	archived := &atomic.Bool{}
	uow := archivableUOW{inner: inner, projectID: "p1", archived: archived}

	sink := &fakeSink{}
	deps := Dependencies{UnitOfWork: uow, Matcher: redact.NewMatcher(), PollInterval: 5 * time.Millisecond, HeartbeatInterval: time.Minute, BufferSize: 16}

	done := make(chan streamOutcome, 1)
	go func() { done <- streamLoop(context.Background(), deps, "p1", 0, nil, sink) }()

	time.Sleep(30 * time.Millisecond) // several poll ticks against the still-ACTIVE project
	archived.Store(true)              // simulate authorization being revoked mid-stream

	var outcome streamOutcome
	select {
	case outcome = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("streamLoop did not stop after authorization was revoked mid-stream")
	}

	if outcome.Reason != reasonUnauthorized {
		t.Fatalf("Reason = %q, want %q — a stream must actually stop delivering once its project is no longer authorized, not merely refuse a NEW connection attempt", outcome.Reason, reasonUnauthorized)
	}
}

func TestStreamLoop_ShutdownSignalStopsPromptly(t *testing.T) {
	uow := newFakeProject(t, "p1")
	sink := &fakeSink{}
	shutdownCtx, cancelShutdown := context.WithCancel(context.Background())
	// Poll/heartbeat intervals are deliberately long (a minute): the
	// assertion below only holds if returning promptly is caused by the
	// shutdown signal itself, never by coincidentally waiting for the next
	// tick anyway.
	deps := Dependencies{UnitOfWork: uow, Matcher: redact.NewMatcher(), PollInterval: time.Minute, HeartbeatInterval: time.Minute, Shutdown: shutdownCtx}

	done := make(chan streamOutcome, 1)
	go func() { done <- streamLoop(context.Background(), deps, "p1", 0, nil, sink) }()

	time.Sleep(10 * time.Millisecond)
	start := time.Now()
	cancelShutdown()

	var outcome streamOutcome
	select {
	case outcome = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("streamLoop did not return promptly after the shutdown signal fired — a server shutdown must close every active stream, not hang")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("streamLoop took %s to return after shutdown fired — too slow for a bounded shutdown", elapsed)
	}
	if outcome.Reason != reasonServerShutdown {
		t.Fatalf("Reason = %q, want %q", outcome.Reason, reasonServerShutdown)
	}
}

func TestStreamLoop_HeartbeatNeverAdvancesCursor(t *testing.T) {
	uow := newFakeProject(t, "p1")
	sink := &fakeSink{}
	ctx, cancel := context.WithCancel(context.Background())
	deps := Dependencies{UnitOfWork: uow, Matcher: redact.NewMatcher(), PollInterval: time.Minute, HeartbeatInterval: 5 * time.Millisecond, BufferSize: 16}

	const startCursor = uint64(42) // an arbitrary already-observed cursor; no event ever reaches this position in this test
	done := make(chan streamOutcome, 1)
	go func() { done <- streamLoop(ctx, deps, "p1", startCursor, nil, sink) }()

	time.Sleep(40 * time.Millisecond) // several heartbeat intervals, no real events ever appended
	cancel()

	var outcome streamOutcome
	select {
	case outcome = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("streamLoop did not stop after ctx was cancelled")
	}

	_, heartbeats := sink.snapshot()
	if heartbeats == 0 {
		t.Fatal("no heartbeat was ever sent — the test's own HeartbeatInterval should have fired several times")
	}
	if outcome.LastCursor != startCursor {
		t.Fatalf("LastCursor = %d, want unchanged %d — a heartbeat must never advance cursor state", outcome.LastCursor, startCursor)
	}
}
