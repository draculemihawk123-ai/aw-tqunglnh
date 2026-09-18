package securitymatrix

// SSE half of V6-13's own matrix: "SSE foreign-event absence" and
// "cancellation", against V6-11's real
// internal/delivery/httpapi/eventstream route
// (GET /projects/{id}/events/watch), composed and served exactly as
// production serves it.

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// projectEventSummary mirrors the wire shape eventstream puts in each SSE
// `data:` line — restated here rather than imported so this suite asserts
// against the BYTES on the wire, not against the producing package's own
// struct.
type projectEventSummary struct {
	JournalPosition uint64 `json:"journalPosition"`
	EventType       string `json:"eventType"`
}

// openStream issues a real GET against the SSE route and returns the
// response plus a cancel func. The caller is responsible for closing the
// body.
func (e *env) openStream(t *testing.T, ctx context.Context, projectID string, cursor uint64) *http.Response {
	t.Helper()
	url := e.base + "/projects/" + projectID + "/events/watch?cursor=" + itoa(cursor)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	httpReq.Host = e.host
	httpReq.Header.Set("Accept", "text/event-stream")
	// A streaming response must never be given the shared client's own
	// 30s timeout: it is unbounded by design, and the ctx above is what
	// actually ends it.
	client := &http.Client{}
	resp, err := client.Do(httpReq)
	if err != nil {
		t.Fatalf("open stream for %s: %v", projectID, err)
	}
	return resp
}

func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

// readSummaries reads SSE `data:` lines until the body ends (the stream is
// closed by the caller's own context cancellation) and returns every
// decoded event summary.
func readSummaries(t *testing.T, body io.Reader) []projectEventSummary {
	t.Helper()
	var out []projectEventSummary
	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var summary projectEventSummary
		if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &summary); err != nil {
			// The stream also carries non-summary frames (comments,
			// heartbeats); anything that is not a summary is simply not
			// part of what this test asserts on.
			continue
		}
		out = append(out, summary)
	}
	return out
}

// journalPositionsByProject reads the REAL global event journal
// (tx.Events().ScanJournal — the same read eventstream itself performs) and
// returns, per project, every JournalPosition that project owns. This is
// the ground truth every "no foreign event reached this stream" assertion
// below is checked against: not a guess about what SHOULD be there, but the
// actual contents of the database.
func (e *env) journalPositionsByProject(t *testing.T) map[string]map[uint64]bool {
	t.Helper()
	ctx := context.Background()
	byProject := map[string]map[uint64]bool{}
	if err := e.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		events, err := tx.Events().ScanJournal(ctx, 0, 100000)
		if err != nil {
			return err
		}
		for _, event := range events {
			if byProject[event.ProjectID] == nil {
				byProject[event.ProjectID] = map[uint64]bool{}
			}
			byProject[event.ProjectID][event.JournalPosition] = true
		}
		return nil
	}); err != nil {
		t.Fatalf("scan journal: %v", err)
	}
	return byProject
}

// TestSSE_ForeignProjectEventsNeverReachAnotherProjectsStream is V6-13's own
// "SSE foreign-event absence" bullet. Both fixture projects have already
// emitted real domain events (every seeding command in this suite goes
// through the real application commands, which emit into the real journal),
// so the beta events this test asserts must NOT appear are genuinely
// present in the same journal the stream scans.
func TestSSE_ForeignProjectEventsNeverReachAnotherProjectsStream(t *testing.T) {
	e := newEnv(t)
	positions := e.journalPositionsByProject(t)
	if len(positions[e.alpha.ProjectID]) == 0 || len(positions[e.beta.ProjectID]) == 0 {
		t.Fatalf("fixture did not emit real events for both projects (alpha=%d, beta=%d) — this test would prove nothing",
			len(positions[e.alpha.ProjectID]), len(positions[e.beta.ProjectID]))
	}

	ctx, cancel := context.WithCancel(context.Background())
	resp := e.openStream(t, ctx, e.alpha.ProjectID, 0)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("open alpha stream: status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	// Let the stream's own poll loop deliver its whole backlog, then end it.
	go func() {
		time.Sleep(2 * time.Second)
		cancel()
	}()
	summaries := readSummaries(t, resp.Body)

	if len(summaries) == 0 {
		t.Fatal("alpha's stream delivered no events at all — a foreign-event-absence assertion over an empty stream proves nothing")
	}
	for _, summary := range summaries {
		if positions[e.beta.ProjectID][summary.JournalPosition] {
			t.Errorf("alpha's stream delivered journal position %d, which belongs to project %s (eventType %q)",
				summary.JournalPosition, e.beta.ProjectID, summary.EventType)
		}
		if !positions[e.alpha.ProjectID][summary.JournalPosition] {
			t.Errorf("alpha's stream delivered journal position %d, which belongs to no event of project %s",
				summary.JournalPosition, e.alpha.ProjectID)
		}
	}
	t.Logf("alpha's stream delivered %d summaries; %d of the journal's positions belong to beta and none of them appeared",
		len(summaries), len(positions[e.beta.ProjectID]))
}

// TestSSE_UnknownProjectIsRefusedBeforeAnyStreamIsOpened proves the stream
// route reloads and authorizes its target BEFORE upgrading the connection
// (eventstream's own probeProject, called from handler.go before
// SSEHeaders/streamLoop) — and that the refusal is the same
// leakage-normalized 404 every other route uses, never a distinguishable
// 403 and never an opened-then-immediately-closed stream.
func TestSSE_UnknownProjectIsRefusedBeforeAnyStreamIsOpened(t *testing.T) {
	e := newEnv(t)
	got := e.do(t, req{Method: http.MethodGet, Path: "/projects/" + fabricatedID + "/events/watch?cursor=0"})
	if got.Status != http.StatusNotFound || strings.TrimSpace(got.Body) != hiddenResourceBody {
		t.Errorf("watch on a never-issued project: status = %d body = %s, want the leakage-normalized 404",
			got.Status, truncate(got.Body))
	}
	if ct := got.Header.Get("Content-Type"); strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("a refused watch still upgraded to SSE (Content-Type %q) — authorization must precede the upgrade", ct)
	}
}

// TestSSE_ClientCancellationClosesTheStreamPromptly is V6-13's own
// "cancellation" bullet for the streaming route: a client that goes away
// must not leave this server writing into a dead socket. Cancelling the
// request context has to end the response body read quickly — well inside
// eventstream's own 15s default heartbeat interval, which is what a naive
// implementation that only noticed the client on its next write would be
// bounded by.
func TestSSE_ClientCancellationClosesTheStreamPromptly(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	resp := e.openStream(t, ctx, e.alpha.ProjectID, 0)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("open stream: status = %d, want 200", resp.StatusCode)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(io.Discard, resp.Body)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream was still open 5s after the client cancelled — streamLoop must observe r.Context() rather than " +
			"only noticing on its next heartbeat write")
	}
}

// TestSSE_ServerShutdownClosesOpenStreams is the other half of
// cancellation: graceful shutdown must not hang waiting on an unbounded
// streaming response. eventstream's Dependencies.Shutdown is wired by
// httpcompose to the composition root's own signal context, and this env
// passes the same context every server in this suite runs under — so this
// asserts the real production wiring, not a test-only shortcut.
func TestSSE_ServerShutdownClosesOpenStreams(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp := e.openStream(t, ctx, e.alpha.ProjectID, 0)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("open stream: status = %d, want 200", resp.StatusCode)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(io.Discard, resp.Body)
	}()

	// The real production sequence: cancel the signal context every open
	// stream watches, THEN gracefully shut the server down.
	e.shutdownCancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := e.server.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown with an open stream: %v", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("an open stream outlived graceful shutdown by more than 5s")
	}
}
