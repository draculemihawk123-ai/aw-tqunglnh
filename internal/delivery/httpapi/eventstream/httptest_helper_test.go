package eventstream_test

// newTestServer composes a REAL net/http server around this package's own
// RegisterRoutes — mirrors internal/delivery/httpapi/rundetail's own
// identical helper (httptest_helper_test.go) exactly: every test in this
// file drives the actual registered route pattern (including the {id}
// wildcard extraction) through a real httptest.Server and a real
// *http.Client, never a bare handler func call.
//
// readSSEFrame/sseEvent are this package's own small SSE wire parser — no
// precedent for one exists elsewhere in this codebase (sse.go only ever
// provides the WRITE side) — reading raw "id:"/"event:"/"data:" lines up to
// the blank line that terminates one frame (or a comment-only "^: "
// heartbeat line), matching httpapi.WriteSSE/WriteSSEComment's own wire
// format exactly.

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/eventstream"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

const testActor = "operator-1"

func testPrincipal() httpapi.LocalPrincipalSnapshot {
	return httpapi.LocalPrincipalSnapshot{Actor: testActor, Roles: []string{"operator"}}
}

func newTestServer(t *testing.T, deps eventstream.Dependencies) *httptest.Server {
	t.Helper()
	registry := httpapi.NewRouteRegistry()
	eventstream.RegisterRoutes(registry, deps)

	mux := http.NewServeMux()
	for _, d := range registry.Descriptors() {
		mux.HandleFunc(d.Method+" "+d.Path, d.Handler)
	}
	handler := httpapi.Chain(mux, httpapi.BindPrincipal(testPrincipal()))

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

// newTestProject seeds a fresh in-memory fake.UnitOfWork with one ACTIVE
// Project named id.
func newTestProject(t *testing.T, id string) *fake.UnitOfWork {
	t.Helper()
	uow := fake.New()
	if err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(context.Background(), ports.CreateProjectRequest{ID: id, Name: id})
		return err
	}); err != nil {
		t.Fatalf("create test project %s: %v", id, err)
	}
	return uow
}

// appendEvent appends one domain event for projectID with a distinct
// AggregateID (n) — n must be unique per call for the SAME projectID to
// avoid the fake's own duplicate-(aggregate_type,aggregate_id,sequence)
// rejection.
func appendEvent(t *testing.T, uow ports.UnitOfWork, projectID, eventType string, n int) {
	t.Helper()
	if err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		return tx.Events().Append(context.Background(), ports.DomainEvent{
			ID: fmt.Sprintf("evt-%s-%d", projectID, n), ProjectID: projectID,
			AggregateType: "TestAggregate", AggregateID: fmt.Sprintf("agg-%s-%d", projectID, n), Sequence: 1,
			EventType: eventType, SchemaVersion: 1, PayloadJSON: "{}", CreatedAt: time.Now().UTC(),
		})
	}); err != nil {
		t.Fatalf("append event for %s: %v", projectID, err)
	}
}

// archivableCatalog/archivableTx/archivableUOW let a test flip one
// Project's own authorized-for-streaming status at connect time — this
// external-package copy exists because stream_internal_test.go's own
// identical types live in package eventstream (not eventstream_test) and
// so are not visible here; see that file's own doc comment for the full
// "no real ArchiveProject mutator exists in this codebase yet" reasoning.
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

func testDeps(uow ports.UnitOfWork) eventstream.Dependencies {
	return eventstream.Dependencies{
		UnitOfWork: uow, Matcher: redact.NewMatcher(),
		PollInterval: 10 * time.Millisecond, HeartbeatInterval: time.Minute,
	}
}

type sseEvent struct {
	ID    string
	Event string
	Data  string
}

const sseCommentEvent = "__comment__"

// readSSEFrame reads one full SSE frame (up to and including its
// terminating blank line) from r.
func readSSEFrame(r *bufio.Reader) (sseEvent, error) {
	var ev sseEvent
	sawAny := false
	for {
		line, err := r.ReadString('\n')
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed != "" {
			sawAny = true
			switch {
			case strings.HasPrefix(trimmed, "id: "):
				ev.ID = strings.TrimPrefix(trimmed, "id: ")
			case strings.HasPrefix(trimmed, "event: "):
				ev.Event = strings.TrimPrefix(trimmed, "event: ")
			case strings.HasPrefix(trimmed, "data: "):
				ev.Data = strings.TrimPrefix(trimmed, "data: ")
			case strings.HasPrefix(trimmed, ": "):
				ev.Event = sseCommentEvent
				ev.Data = strings.TrimPrefix(trimmed, ": ")
			}
		}
		if err != nil {
			if sawAny {
				return ev, nil
			}
			return ev, err
		}
		if trimmed == "" && sawAny {
			return ev, nil
		}
	}
}

// sseReader streams parsed frames from resp.Body on a background goroutine
// so a test can read with a bounded timeout instead of risking an
// indefinite block on a connection that (by design, for several of this
// package's own tests) never sends anything more.
type sseReader struct {
	ch   chan sseEvent
	resp *http.Response
}

func startSSEReader(resp *http.Response) *sseReader {
	sr := &sseReader{ch: make(chan sseEvent), resp: resp}
	go func() {
		defer close(sr.ch)
		r := bufio.NewReader(resp.Body)
		for {
			ev, err := readSSEFrame(r)
			if err != nil {
				return
			}
			sr.ch <- ev
		}
	}()
	return sr
}

func (sr *sseReader) next(t *testing.T, timeout time.Duration) (sseEvent, bool) {
	t.Helper()
	select {
	case ev, ok := <-sr.ch:
		return ev, ok
	case <-time.After(timeout):
		t.Fatalf("timed out after %s waiting for the next SSE frame", timeout)
		return sseEvent{}, false
	}
}

func (sr *sseReader) close() {
	_ = sr.resp.Body.Close()
}
