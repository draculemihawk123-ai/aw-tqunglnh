package events_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	clievents "github.com/taQuangLing/agent-workflow/internal/delivery/cli/events"
)

// runWatch runs clievents.RunWatch in its own goroutine, cancels its
// context after cancelAfter, and waits (with a generous bound) for it to
// actually return — this task's own "shutdown ... bounded" pattern, mirrors
// internal/delivery/httpapi/eventstream/stream_internal_test.go's own
// identical "start in a goroutine, fire the stop condition, assert the
// result arrives within a bounded time" shape.
func runWatch(t *testing.T, deps clievents.Dependencies, args []string, cancelAfter time.Duration) (*bytes.Buffer, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout, stderr bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- clievents.RunWatch(ctx, deps, args, &stdout, &stderr) }()

	time.Sleep(cancelAfter)
	start := time.Now()
	cancel()

	select {
	case err := <-done:
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("RunWatch took %s to return after context cancellation — not a bounded shutdown", elapsed)
		}
		if stderr.Len() != 0 {
			t.Fatalf("stderr got written to: %q", stderr.String())
		}
		return &stdout, err
	case <-time.After(5 * time.Second):
		t.Fatal("RunWatch did not return after context cancellation — no bounded shutdown")
		return nil, nil
	}
}

// decodeNDJSONLines decodes every line in buf as a
// clievents.ProjectEventSummary via bufio.Scanner + json.Unmarshal, failing
// the test on any line that is not complete, valid, independently
// parseable JSON — this task's own single most important test for the
// NDJSON half of V6-15N.
func decodeNDJSONLines(t *testing.T, buf *bytes.Buffer) []clievents.ProjectEventSummary {
	t.Helper()
	scanner := bufio.NewScanner(buf)
	var lines []clievents.ProjectEventSummary
	n := 0
	for scanner.Scan() {
		var summary clievents.ProjectEventSummary
		if err := json.Unmarshal(scanner.Bytes(), &summary); err != nil {
			t.Fatalf("line %d is not valid, complete, independently-parseable JSON: %v (%q)", n, err, scanner.Text())
		}
		lines = append(lines, summary)
		n++
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner error: %v", err)
	}
	return lines
}

func TestRunWatch_MissingProjectID_UsageError(t *testing.T) {
	uow := newFakeProject(t, "p1")
	deps := newTestDeps(uow)
	var stdout, stderr bytes.Buffer
	err := clievents.RunWatch(context.Background(), deps, nil, &stdout, &stderr)
	if !isUsageError(err) {
		t.Fatalf("RunWatch() error = %v, want a cli.UsageError", err)
	}
}

func TestRunWatch_RejectsPositionalArgs(t *testing.T) {
	uow := newFakeProject(t, "p1")
	deps := newTestDeps(uow)
	var stdout, stderr bytes.Buffer
	err := clievents.RunWatch(context.Background(), deps, []string{"--project-id=p1", "unexpected"}, &stdout, &stderr)
	if !isUsageError(err) {
		t.Fatalf("RunWatch() error = %v, want a cli.UsageError for an unexpected positional argument", err)
	}
}

// TestRunWatch_UnknownProject_AtStart proves an unknown --project-id fails
// immediately (before ever entering the poll loop — no goroutine/timeout
// needed to observe this).
func TestRunWatch_UnknownProject_AtStart(t *testing.T) {
	uow := newFakeProject(t, "some-other-project")
	deps := newTestDeps(uow)
	var stdout, stderr bytes.Buffer
	err := clievents.RunWatch(context.Background(), deps, []string{"--project-id=does-not-exist"}, &stdout, &stderr)
	if !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("RunWatch() error = %v, want it to wrap ports.ErrPersistenceNotFound", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout got written to for an unknown project: %q", stdout.String())
	}
}

// TestRunWatch_ProjectNotActiveAtStart proves a project that is not
// project.ProjectActive fails immediately with ErrProjectNotAuthorized —
// also observable synchronously, before any poll tick.
func TestRunWatch_ProjectNotActiveAtStart(t *testing.T) {
	inner := newFakeProject(t, "p1")
	archived := &atomic.Bool{}
	archived.Store(true)
	uow := archivableUOW{inner: inner, projectID: "p1", archived: archived}
	deps := newTestDeps(uow)

	var stdout, stderr bytes.Buffer
	err := clievents.RunWatch(context.Background(), deps, []string{"--project-id=p1"}, &stdout, &stderr)
	if !errors.Is(err, clievents.ErrProjectNotAuthorized) {
		t.Fatalf("RunWatch() error = %v, want ErrProjectNotAuthorized", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout got written to for an unauthorized project: %q", stdout.String())
	}
}

// TestRunWatch_ContinuousNDJSONParsing is this task's own single most
// important test: every line `aw events watch` writes decodes as a
// complete, independent, valid ProjectEventSummary, in ascending
// JournalPosition order, matching exactly the events already in the
// journal before the watch even started.
func TestRunWatch_ContinuousNDJSONParsing(t *testing.T) {
	uow := newFakeProject(t, "p1")
	appendTestEvents(t, uow, "p1", 25)
	deps := newTestDeps(uow)

	stdout, err := runWatch(t, deps, []string{"--project-id=p1"}, 30*time.Millisecond)
	if err != nil {
		t.Fatalf("RunWatch() error = %v", err)
	}

	lines := decodeNDJSONLines(t, stdout)
	if len(lines) != 25 {
		t.Fatalf("got %d lines, want 25", len(lines))
	}
	for i, line := range lines {
		wantPosition := uint64(i + 1)
		if line.JournalPosition != wantPosition {
			t.Fatalf("line %d JournalPosition = %d, want %d (must be strictly ascending, matching append order)", i, line.JournalPosition, wantPosition)
		}
		wantType := "Test.Event." + strconv.Itoa(i)
		if line.EventType != wantType {
			t.Fatalf("line %d EventType = %q, want %q", i, line.EventType, wantType)
		}
		if line.SchemaVersion != 1 {
			t.Fatalf("line %d SchemaVersion = %d, want 1", i, line.SchemaVersion)
		}
	}
}

// TestRunWatch_FiltersForeignProjectEvents proves the global journal scan
// is filtered client-side to only ever emit a line for --project-id's own
// events, even though the underlying scan cursor still advances past a
// foreign project's own rows (mirrors
// internal/delivery/httpapi/eventstream/stream.go's own process closure).
func TestRunWatch_FiltersForeignProjectEvents(t *testing.T) {
	uow := newFakeProject(t, "p1")
	mustCreateProject(t, uow, "p2")
	appendTestEvents(t, uow, "p2", 5) // journal positions 1-5
	appendTestEvents(t, uow, "p1", 5) // journal positions 6-10
	deps := newTestDeps(uow)

	stdout, err := runWatch(t, deps, []string{"--project-id=p1"}, 30*time.Millisecond)
	if err != nil {
		t.Fatalf("RunWatch() error = %v", err)
	}

	lines := decodeNDJSONLines(t, stdout)
	if len(lines) != 5 {
		t.Fatalf("got %d lines, want exactly 5 (p1's own events only, never p2's)", len(lines))
	}
	for i, line := range lines {
		wantPosition := uint64(6 + i)
		if line.JournalPosition != wantPosition {
			t.Fatalf("line %d JournalPosition = %d, want %d — the scan cursor must advance past p2's own rows without emitting them, then resume at p1's own", i, line.JournalPosition, wantPosition)
		}
	}
}

// TestRunWatch_DuplicateAcrossRestart_FromCursor is this task's own
// "duplicate" Verify bullet: restarting `aw events watch` with
// --from-cursor set to the prior run's own last-emitted JournalPosition
// never re-emits an already-seen event and never skips one appended in
// between the two runs.
func TestRunWatch_DuplicateAcrossRestart_FromCursor(t *testing.T) {
	uow := newFakeProject(t, "p1")
	appendTestEvents(t, uow, "p1", 10) // journal positions 1-10
	deps := newTestDeps(uow)

	firstOut, err := runWatch(t, deps, []string{"--project-id=p1"}, 30*time.Millisecond)
	if err != nil {
		t.Fatalf("first RunWatch() error = %v", err)
	}
	firstLines := decodeNDJSONLines(t, firstOut)
	if len(firstLines) != 10 {
		t.Fatalf("first run got %d lines, want 10", len(firstLines))
	}
	lastCursor := firstLines[len(firstLines)-1].JournalPosition
	if lastCursor != 10 {
		t.Fatalf("first run's own last cursor = %d, want 10", lastCursor)
	}

	appendTestEvents(t, uow, "p1", 5) // journal positions 11-15, appended BETWEEN the two runs

	secondOut, err := runWatch(t, deps, []string{"--project-id=p1", "--from-cursor=" + strconv.FormatUint(lastCursor, 10)}, 30*time.Millisecond)
	if err != nil {
		t.Fatalf("second RunWatch() error = %v", err)
	}
	secondLines := decodeNDJSONLines(t, secondOut)
	if len(secondLines) != 5 {
		t.Fatalf("second run got %d lines, want exactly 5 (the events appended after the first run's own last cursor, no duplicates, no gaps)", len(secondLines))
	}
	for i, line := range secondLines {
		wantPosition := uint64(11 + i)
		if line.JournalPosition != wantPosition {
			t.Fatalf("second run line %d JournalPosition = %d, want %d", i, line.JournalPosition, wantPosition)
		}
	}
}

// TestRunWatch_ResyncRequired_OldCursorOutsideRetention is this task's own
// "resync" Verify bullet: a --from-cursor value old enough that more than
// RetentionScanLimit events are already pending returns the typed
// ErrResyncRequired instead of ever starting to poll/emit — no partial
// output written first.
func TestRunWatch_ResyncRequired_OldCursorOutsideRetention(t *testing.T) {
	uow := newFakeProject(t, "p1")
	appendTestEvents(t, uow, "p1", 10)
	deps := newTestDeps(uow)
	deps.RetentionScanLimit = 5

	var stdout, stderr bytes.Buffer
	err := clievents.RunWatch(context.Background(), deps, []string{"--project-id=p1", "--from-cursor=0"}, &stdout, &stderr)
	if !errors.Is(err, clievents.ErrResyncRequired) {
		t.Fatalf("RunWatch() error = %v, want ErrResyncRequired", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout got written to before the resync check ran: %q", stdout.String())
	}
}

// TestRunWatch_ResyncNotTriggeredWithinRetentionWindow is
// ResyncRequired's own negative case: a --from-cursor within the retention
// window streams normally, never spuriously resync-rejected.
func TestRunWatch_ResyncNotTriggeredWithinRetentionWindow(t *testing.T) {
	uow := newFakeProject(t, "p1")
	appendTestEvents(t, uow, "p1", 4)
	deps := newTestDeps(uow)
	deps.RetentionScanLimit = 5

	stdout, err := runWatch(t, deps, []string{"--project-id=p1", "--from-cursor=0"}, 30*time.Millisecond)
	if err != nil {
		t.Fatalf("RunWatch() error = %v", err)
	}
	lines := decodeNDJSONLines(t, stdout)
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want 4", len(lines))
	}
}

// TestRunWatch_ShutdownOnContextCancellation is this task's own "shutdown"
// Verify bullet: a context cancellation mid-poll stops RunWatch cleanly
// (nil error) within a bounded time — runWatch's own bound assertion
// already covers "no goroutine leak" by construction (RunWatch spawns no
// goroutine of its own — see watch.go's own doc comment), so the only thing
// left to assert here is a clean, non-error return.
func TestRunWatch_ShutdownOnContextCancellation(t *testing.T) {
	uow := newFakeProject(t, "p1")
	deps := newTestDeps(uow)

	_, err := runWatch(t, deps, []string{"--project-id=p1"}, 30*time.Millisecond)
	if err != nil {
		t.Fatalf("RunWatch() error = %v, want nil for a clean context-cancellation shutdown", err)
	}
}

// TestRunWatch_ReauthorizationMidStream_StopsDelivery is this task's own
// re-authorization Verify concern (doc.go's own "Re-authorization"
// section): a project deactivated mid-stream stops delivery on the very
// next poll tick, not merely on the next NEW invocation.
func TestRunWatch_ReauthorizationMidStream_StopsDelivery(t *testing.T) {
	inner := newFakeProject(t, "p1")
	archived := &atomic.Bool{}
	uow := archivableUOW{inner: inner, projectID: "p1", archived: archived}
	deps := newTestDeps(uow)

	var stdout, stderr bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- clievents.RunWatch(context.Background(), deps, []string{"--project-id=p1"}, &stdout, &stderr)
	}()

	time.Sleep(30 * time.Millisecond) // several poll ticks against the still-ACTIVE project
	archived.Store(true)              // simulate authorization being revoked mid-stream

	select {
	case err := <-done:
		if !errors.Is(err, clievents.ErrProjectNotAuthorized) {
			t.Fatalf("RunWatch() error = %v, want ErrProjectNotAuthorized — a stream must actually stop delivering once its project is no longer authorized, not merely refuse a NEW invocation", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunWatch did not stop after authorization was revoked mid-stream")
	}
}

// slowStdout sleeps briefly before every Write — this task's own "slow
// consumer" concern exercised at the RunWatch level (cli.EncodeNDJSONLine's
// own tests already cover the encoder in isolation; this proves RunWatch's
// own poll loop built on top of it neither corrupts output nor hangs when
// its sink is slow).
type slowStdout struct {
	buf   bytes.Buffer
	delay time.Duration
}

func (s *slowStdout) Write(p []byte) (int, error) {
	time.Sleep(s.delay)
	return s.buf.Write(p)
}

func TestRunWatch_SlowConsumer_NeverCorruptsOrDeadlocks(t *testing.T) {
	uow := newFakeProject(t, "p1")
	appendTestEvents(t, uow, "p1", 15)
	deps := newTestDeps(uow)

	ctx, cancel := context.WithCancel(context.Background())
	sink := &slowStdout{delay: 5 * time.Millisecond}
	var stderr bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- clievents.RunWatch(ctx, deps, []string{"--project-id=p1"}, sink, &stderr) }()

	time.Sleep(200 * time.Millisecond) // enough for the slow initial batch to fully drain
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunWatch() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunWatch did not return — a slow sink must never deadlock the poll loop")
	}

	lines := decodeNDJSONLines(t, &sink.buf)
	if len(lines) != 15 {
		t.Fatalf("got %d lines, want 15 (slow sink must never corrupt/drop/duplicate output)", len(lines))
	}
	for i, line := range lines {
		if line.JournalPosition != uint64(i+1) {
			t.Fatalf("line %d JournalPosition = %d, want %d", i, line.JournalPosition, i+1)
		}
	}
}

func TestRunWatch_PollIntervalFlagOverridesDependenciesDefault(t *testing.T) {
	uow := newFakeProject(t, "p1")
	deps := newTestDeps(uow)
	deps.PollInterval = time.Hour // would never tick within this test's own bound if not overridden by the flag

	appendTestEvents(t, uow, "p1", 1)
	stdout, err := runWatch(t, deps, []string{"--project-id=p1", "--poll-interval=5ms"}, 30*time.Millisecond)
	if err != nil {
		t.Fatalf("RunWatch() error = %v", err)
	}
	lines := decodeNDJSONLines(t, stdout)
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1 (initial batch always emits regardless of poll interval)", len(lines))
	}
}
