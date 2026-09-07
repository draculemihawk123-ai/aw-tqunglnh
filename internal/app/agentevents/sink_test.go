package agentevents_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/agentevents"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// fakeCheckpointStore is a minimal, in-memory agentevents.CheckpointStore —
// this package's own narrow interface, so its fake lives here rather than
// in the shared internal/app/ports/fake package.
type fakeCheckpointStore struct {
	stored []runtime.Checkpoint
}

func (f *fakeCheckpointStore) StoreCheckpoint(_ context.Context, checkpoint runtime.Checkpoint) (runtime.Checkpoint, error) {
	f.stored = append(f.stored, checkpoint)
	return checkpoint, nil
}

// fakeWorkspaceProvider is a minimal, in-memory ports.WorkspaceProvider for
// tests that only need Sink's own diff-capture call shape exercised, not a
// real git repository — see sink_sqlite_test.go for the real-git coverage
// of diff/scope behavior itself.
type fakeWorkspaceProvider struct {
	diff ports.WorkspaceDiff
	err  error
}

func (f *fakeWorkspaceProvider) Provision(context.Context, ports.ProvisionSpec) (ports.WorkspaceHandle, error) {
	return ports.NewWorkspaceHandle("fixture-handle")
}
func (f *fakeWorkspaceProvider) Inspect(context.Context, ports.WorkspaceHandle) (ports.WorkspaceInspection, error) {
	return ports.WorkspaceInspection{}, nil
}
func (f *fakeWorkspaceProvider) CaptureRevision(context.Context, ports.WorkspaceHandle) (workspace.Revision, error) {
	return workspace.Revision{}, nil
}
func (f *fakeWorkspaceProvider) Diff(context.Context, ports.WorkspaceHandle, workspace.Revision) (ports.WorkspaceDiff, error) {
	return f.diff, f.err
}
func (f *fakeWorkspaceProvider) Release(context.Context, ports.WorkspaceHandle) error { return nil }

var _ ports.WorkspaceProvider = (*fakeWorkspaceProvider)(nil)

func newTestSink(t *testing.T, checkpoints agentevents.CheckpointStore, matcher redact.Matcher) (*agentevents.Sink, *fake.UnitOfWork) {
	t.Helper()
	uow := fake.New()
	registry := eventschema.NewRegistry()
	agentevents.RegisterEventSchemas(registry)
	sink, err := agentevents.NewSink(context.Background(), agentevents.Config{
		RunID: "run-1", NodeRunID: "node-run-1", AttemptID: "attempt-1", ContextSnapshotID: "snapshot-1",
		Workspaces: &fakeWorkspaceProvider{}, Registry: registry, Matcher: matcher,
		UOW: uow, Checkpoints: checkpoints, IDs: idsource.NewSequential("evt"), Clock: clock.NewFixed(time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("NewSink: %v", err)
	}
	return sink, uow
}

func agentEventRows(t *testing.T, uow *fake.UnitOfWork, attemptID string) []ports.AgentEventRecord {
	t.Helper()
	var records []ports.AgentEventRecord
	err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		records, err = tx.AgentEvents().ListByAttempt(context.Background(), attemptID)
		return err
	})
	if err != nil {
		t.Fatalf("list agent events: %v", err)
	}
	return records
}

func TestSink_Accept_NormalizesBuffersAndFlushesOnCheckpoint(t *testing.T) {
	sink, uow := newTestSink(t, &fakeCheckpointStore{}, redact.NewMatcher())
	ctx := context.Background()

	if err := sink.Accept(ctx, ports.AgentEvent{Sequence: 1, Kind: ports.AgentEventExecutionStarted, ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("accept event 1: %v", err)
	}
	if err := sink.Accept(ctx, ports.AgentEvent{Sequence: 2, Kind: ports.AgentEventAssistantMessage, Message: "hello", ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("accept event 2: %v", err)
	}
	// Not flushed yet — still buffered, below MaxBatchEvents and no
	// checkpoint proposed.
	if rows := agentEventRows(t, uow, "attempt-1"); len(rows) != 0 {
		t.Fatalf("rows persisted before flush = %d, want 0", len(rows))
	}

	if err := sink.Accept(ctx, ports.AgentEvent{Sequence: 3, Kind: ports.AgentEventCheckpointProposed, ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("accept checkpoint-proposed event: %v", err)
	}

	rows := agentEventRows(t, uow, "attempt-1")
	if len(rows) != 3 {
		t.Fatalf("rows persisted after checkpoint = %d, want 3", len(rows))
	}
	for i, row := range rows {
		if row.Sequence != uint64(i+1) {
			t.Fatalf("row %d sequence = %d, want %d", i, row.Sequence, i+1)
		}
		if row.SchemaVersion != 1 {
			t.Fatalf("row %d schema version = %d, want 1", i, row.SchemaVersion)
		}
	}
}

func TestSink_Accept_RejectsSequenceGap(t *testing.T) {
	sink, _ := newTestSink(t, &fakeCheckpointStore{}, redact.NewMatcher())
	ctx := context.Background()
	if err := sink.Accept(ctx, ports.AgentEvent{Sequence: 1, Kind: ports.AgentEventExecutionStarted}); err != nil {
		t.Fatalf("accept event 1: %v", err)
	}
	err := sink.Accept(ctx, ports.AgentEvent{Sequence: 3, Kind: ports.AgentEventAssistantMessage})
	if !errors.Is(err, agentevents.ErrSequenceGap) {
		t.Fatalf("accept sequence 3 after 1 error = %v, want ErrSequenceGap", err)
	}
}

func TestSink_Accept_RejectsDuplicateSequence(t *testing.T) {
	sink, _ := newTestSink(t, &fakeCheckpointStore{}, redact.NewMatcher())
	ctx := context.Background()
	event := ports.AgentEvent{Sequence: 1, Kind: ports.AgentEventExecutionStarted}
	if err := sink.Accept(ctx, event); err != nil {
		t.Fatalf("accept event 1: %v", err)
	}
	if err := sink.Accept(ctx, event); !errors.Is(err, agentevents.ErrDuplicateEvent) {
		t.Fatalf("accept duplicate sequence 1 error = %v, want ErrDuplicateEvent", err)
	}
}

func TestSink_Accept_RejectsUnregisteredEventKind(t *testing.T) {
	sink, _ := newTestSink(t, &fakeCheckpointStore{}, redact.NewMatcher())
	err := sink.Accept(context.Background(), ports.AgentEvent{Sequence: 1, Kind: ports.AgentEventKind("NOT_A_REAL_KIND")})
	if !errors.Is(err, eventschema.ErrNotRegistered) {
		t.Fatalf("accept unregistered kind error = %v, want eventschema.ErrNotRegistered", err)
	}
}

func TestSink_Accept_RejectsOversizedPayload(t *testing.T) {
	sink, _ := newTestSink(t, &fakeCheckpointStore{}, redact.NewMatcher())
	oversized := strings.Repeat("x", agentevents.MaxEventPayloadBytes+1)
	err := sink.Accept(context.Background(), ports.AgentEvent{Sequence: 1, Kind: ports.AgentEventAssistantMessage, Message: oversized})
	if !errors.Is(err, agentevents.ErrPayloadTooLarge) {
		t.Fatalf("accept oversized payload error = %v, want ErrPayloadTooLarge", err)
	}
}

func TestSink_Accept_BatchesUpToBoundThenFlushesWithoutCheckpoint(t *testing.T) {
	sink, uow := newTestSink(t, &fakeCheckpointStore{}, redact.NewMatcher())
	ctx := context.Background()
	for i := uint64(1); i <= agentevents.MaxBatchEvents; i++ {
		if err := sink.Accept(ctx, ports.AgentEvent{Sequence: i, Kind: ports.AgentEventAssistantMessage, Message: "m"}); err != nil {
			t.Fatalf("accept event %d: %v", i, err)
		}
	}
	rows := agentEventRows(t, uow, "attempt-1")
	if len(rows) != agentevents.MaxBatchEvents {
		t.Fatalf("rows persisted at bound = %d, want %d", len(rows), agentevents.MaxBatchEvents)
	}
}

func TestSink_Flush_PersistsTrailingBufferedEvents(t *testing.T) {
	sink, uow := newTestSink(t, &fakeCheckpointStore{}, redact.NewMatcher())
	ctx := context.Background()
	if err := sink.Accept(ctx, ports.AgentEvent{Sequence: 1, Kind: ports.AgentEventExecutionStarted}); err != nil {
		t.Fatalf("accept event 1: %v", err)
	}
	if rows := agentEventRows(t, uow, "attempt-1"); len(rows) != 0 {
		t.Fatalf("rows persisted before Flush = %d, want 0", len(rows))
	}
	if err := sink.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if rows := agentEventRows(t, uow, "attempt-1"); len(rows) != 1 {
		t.Fatalf("rows persisted after Flush = %d, want 1", len(rows))
	}
	// Flush with an empty buffer is a safe no-op.
	if err := sink.Flush(ctx); err != nil {
		t.Fatalf("Flush (empty buffer): %v", err)
	}
}

func TestSink_Accept_RedactsKnownSecretBeforePersist(t *testing.T) {
	// redact.Matcher matches by exact equality only, never substring/regex
	// (its own package doc comment) — the fixture value must therefore BE
	// the known secret, not a larger string that merely contains it,
	// mirroring internal/app/message's own
	// TestAppendMessage_MatcherNeverScansSubstringWithinFreeText precedent.
	const secret = "sk-live-abc123"
	sink, uow := newTestSink(t, &fakeCheckpointStore{}, redact.NewMatcher(secret))
	ctx := context.Background()
	event := ports.AgentEvent{
		Sequence: 1, Kind: ports.AgentEventToolCallFinished,
		Tool: &ports.AgentToolEvent{CallID: "call-1", Name: "shell", Output: secret},
	}
	if err := sink.Accept(ctx, event); err != nil {
		t.Fatalf("accept event with secret: %v", err)
	}
	if err := sink.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}
	rows := agentEventRows(t, uow, "attempt-1")
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if strings.Contains(rows[0].PayloadJSON, secret) {
		t.Fatalf("persisted payload contains the raw secret: %s", rows[0].PayloadJSON)
	}
}

func TestSink_CheckpointProposed_StoresImmutableCheckpoint(t *testing.T) {
	checkpoints := &fakeCheckpointStore{}
	sink, _ := newTestSink(t, checkpoints, redact.NewMatcher())
	ctx := context.Background()
	if err := sink.Accept(ctx, ports.AgentEvent{Sequence: 1, Kind: ports.AgentEventExecutionStarted}); err != nil {
		t.Fatalf("accept event 1: %v", err)
	}
	if err := sink.Accept(ctx, ports.AgentEvent{Sequence: 2, Kind: ports.AgentEventCheckpointProposed}); err != nil {
		t.Fatalf("accept checkpoint-proposed: %v", err)
	}
	if len(checkpoints.stored) != 1 {
		t.Fatalf("checkpoints stored = %d, want 1", len(checkpoints.stored))
	}
	checkpoint := checkpoints.stored[0]
	if checkpoint.Sequence != 1 {
		t.Fatalf("checkpoint sequence = %d, want 1", checkpoint.Sequence)
	}
	if checkpoint.CanonicalEventSequence != 2 {
		t.Fatalf("checkpoint canonical event sequence = %d, want 2 (the triggering event's own sequence)", checkpoint.CanonicalEventSequence)
	}
	if checkpoint.SharedStateHash == "" {
		t.Fatal("checkpoint shared state hash is empty")
	}

	// A second checkpoint-proposed event advances Sequence — proving
	// checkpoint sequence is a real, monotonic counter, not re-derived from
	// the triggering event's own Sequence.
	if err := sink.Accept(ctx, ports.AgentEvent{Sequence: 3, Kind: ports.AgentEventCheckpointProposed}); err != nil {
		t.Fatalf("accept second checkpoint-proposed: %v", err)
	}
	if len(checkpoints.stored) != 2 || checkpoints.stored[1].Sequence != 2 {
		t.Fatalf("second checkpoint = %+v, want Sequence=2", checkpoints.stored)
	}
}

func TestSink_CheckpointProposed_PropagatesWorkspaceDiffError(t *testing.T) {
	checkpoints := &fakeCheckpointStore{}
	uow := fake.New()
	registry := eventschema.NewRegistry()
	agentevents.RegisterEventSchemas(registry)
	boom := errors.New("boom: diff unavailable")
	sink, err := agentevents.NewSink(context.Background(), agentevents.Config{
		RunID: "run-1", NodeRunID: "node-run-1", AttemptID: "attempt-1", ContextSnapshotID: "snapshot-1",
		Mounts:     []agentevents.Mount{{RepositoryID: "repo-1"}},
		Workspaces: &fakeWorkspaceProvider{err: boom}, Registry: registry, Matcher: redact.NewMatcher(),
		UOW: uow, Checkpoints: checkpoints, IDs: idsource.NewSequential("evt"), Clock: clock.NewFixed(time.Now()),
	})
	if err != nil {
		t.Fatalf("NewSink: %v", err)
	}
	ctx := context.Background()
	if err := sink.Accept(ctx, ports.AgentEvent{Sequence: 1, Kind: ports.AgentEventCheckpointProposed}); !errors.Is(err, boom) {
		t.Fatalf("accept checkpoint-proposed with failing diff error = %v, want %v", err, boom)
	}
	if len(checkpoints.stored) != 0 {
		t.Fatalf("checkpoints stored despite diff failure = %d, want 0", len(checkpoints.stored))
	}
}
