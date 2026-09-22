package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/worker"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

func TestCheckpointPersistsLatestRecoveryPointAcrossRestart(t *testing.T) {
	ctx := context.Background()
	databasePath := migratedDatabasePath(t, "agentkit-checkpoint.db")
	store, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	seedSchedulingFixture(t, store)
	snapshot := testContextSnapshot(t, "context-checkpoint")
	if _, err := store.StoreContextSnapshot(ctx, "project-1", snapshot); err != nil {
		t.Fatal(err)
	}
	first := testCheckpoint(t, "checkpoint-1", 1, 10, snapshot)
	second := testCheckpoint(t, "checkpoint-2", 2, 20, snapshot)
	if _, err := store.StoreCheckpoint(ctx, first); err != nil {
		t.Fatalf("StoreCheckpoint(first) error = %v", err)
	}
	if _, err := store.StoreCheckpoint(ctx, second); err != nil {
		t.Fatalf("StoreCheckpoint(second) error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	latest, err := restarted.LoadLatestCheckpoint(ctx, "attempt-1")
	if err != nil {
		t.Fatalf("LoadLatestCheckpoint() error = %v", err)
	}
	if latest.ID != second.ID || latest.Sequence != 2 || latest.CanonicalEventSequence != 20 ||
		latest.ContextSnapshotID != snapshot.ID() {
		t.Fatalf("latest checkpoint = %#v", latest)
	}
	if _, err := restarted.StoreCheckpoint(ctx, second); err != nil {
		t.Fatalf("idempotent StoreCheckpoint() error = %v", err)
	}
}

func TestRestartLoadsCheckpointedContextAndStartsFreshAgent(t *testing.T) {
	ctx := context.Background()
	databasePath := migratedDatabasePath(t, "agentkit-fresh-recovery.db")
	store, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	seedSchedulingFixture(t, store)
	snapshot := testContextSnapshot(t, "context-fresh-recovery")
	if _, err := store.StoreContextSnapshot(ctx, "project-1", snapshot); err != nil {
		t.Fatal(err)
	}
	checkpoint := testCheckpoint(t, "checkpoint-fresh-recovery", 1, 10, snapshot)
	if _, err := store.StoreCheckpoint(ctx, checkpoint); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	restarted, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	executor := &freshRecoveryExecutor{}
	_, err = worker.StartFreshFromLatestCheckpoint(
		ctx,
		restarted,
		executor,
		checkpoint.AttemptID,
		ports.AgentExecutionRequest{AttemptID: "attempt-replacement", WorkingDirectory: t.TempDir(), Timeout: time.Second},
		ports.AgentEventSinkFunc(func(context.Context, ports.AgentEvent) error { return nil }),
	)
	if err != nil {
		t.Fatalf("StartFreshFromLatestCheckpoint() error = %v", err)
	}
	if executor.startCalls != 1 || executor.resumeCalls != 0 || executor.request.ContextSnapshotID != snapshot.ID() {
		t.Fatalf("fresh recovery = start:%d resume:%d request:%+v", executor.startCalls, executor.resumeCalls, executor.request)
	}
}

func testCheckpoint(
	t *testing.T,
	id runtime.CheckpointID,
	sequence uint64,
	eventSequence uint64,
	snapshot runtime.ContextSnapshot,
) runtime.Checkpoint {
	t.Helper()
	checkpoint, err := runtime.NewCheckpoint(
		id,
		"run-1",
		"node-run-1",
		"attempt-1",
		sequence,
		eventSequence,
		snapshot.ID(),
		snapshot.Revisions(),
		"sha256:shared-state",
		[]string{"artifact:log", "artifact:diff"},
		time.Date(2026, 8, 28, 0, int(sequence), 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	return checkpoint
}

type freshRecoveryExecutor struct {
	request     ports.AgentExecutionRequest
	startCalls  int
	resumeCalls int
}

func (*freshRecoveryExecutor) Capabilities(context.Context) (ports.AgentCapabilities, error) {
	return ports.AgentCapabilities{Provider: ports.ProviderCodex, SupportsStart: true}, nil
}

func (e *freshRecoveryExecutor) Start(_ context.Context, request ports.AgentExecutionRequest, _ ports.AgentEventSink) (ports.AgentExecutionResult, error) {
	e.startCalls++
	e.request = request
	return ports.AgentExecutionResult{AttemptID: request.AttemptID, Provider: ports.ProviderCodex, Status: ports.AgentExecutionSucceeded}, nil
}

func (e *freshRecoveryExecutor) Resume(context.Context, ports.AgentExecutionRequest, ports.ProviderSessionRef, ports.AgentEventSink) (ports.AgentExecutionResult, error) {
	e.resumeCalls++
	return ports.AgentExecutionResult{}, errors.New("fresh recovery must not call resume")
}

// TestCheckpointsRepository_InsertCheckpoint_CommitsWithinCallerTransaction
// is V5-08B's own Tx-composable twin of TestCheckpointPersistsLatestRecoveryPointAcrossRestart
// above: unlike Store.StoreCheckpoint (autocommit, its own *sql.DB), this
// runs inside the caller's existing transaction — proving both that a
// commit really persists (round-tripped via LoadLatestCheckpoint after
// commit) and that a rollback really discards it (nothing reaches the
// database when the caller's own transaction function returns an error).
func TestCheckpointsRepository_InsertCheckpoint_CommitsWithinCallerTransaction(t *testing.T) {
	ctx := context.Background()
	store := openReceiptsStore(t, "agentkit-checkpoints-repository.db")
	seedSchedulingFixture(t, store)
	snapshot := testContextSnapshot(t, "context-checkpoints-repository")
	if _, err := store.StoreContextSnapshot(ctx, "project-1", snapshot); err != nil {
		t.Fatal(err)
	}
	checkpoint := testCheckpoint(t, "checkpoint-tx-1", 1, 10, snapshot)

	boom := errors.New("boom: rollback this transaction")
	rolledBack := testCheckpoint(t, "checkpoint-tx-rolled-back", 2, 20, snapshot)
	if err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		if err := (checkpointsRepository{tx: tx}).InsertCheckpoint(ctx, rolledBack); err != nil {
			return err
		}
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("RunSerializedWrite (rollback) error = %v, want %v", err, boom)
	}
	if _, err := store.LoadLatestCheckpoint(ctx, "attempt-1"); !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("LoadLatestCheckpoint after rollback = %v, want ErrPersistenceNotFound (nothing should have committed)", err)
	}

	if err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		return (checkpointsRepository{tx: tx}).InsertCheckpoint(ctx, checkpoint)
	}); err != nil {
		t.Fatalf("RunSerializedWrite (commit): %v", err)
	}
	loaded, err := store.LoadLatestCheckpoint(ctx, "attempt-1")
	if err != nil {
		t.Fatalf("LoadLatestCheckpoint: %v", err)
	}
	if loaded.ID != checkpoint.ID || loaded.Sequence != 1 || loaded.CanonicalEventSequence != 10 {
		t.Fatalf("loaded checkpoint = %#v, want %#v", loaded, checkpoint)
	}

	// A genuine UNIQUE(attempt_id, sequence) conflict is a real bug, not a
	// legitimate race to dedup (this repository's own doc comment) —
	// InsertCheckpoint surfaces it as an ordinary error, never the
	// idempotent-equal-content early return StoreCheckpoint's own
	// autocommit path performs.
	if err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		return (checkpointsRepository{tx: tx}).InsertCheckpoint(ctx, checkpoint)
	}); err == nil {
		t.Fatal("InsertCheckpoint duplicate (attempt_id, sequence) succeeded, want a UNIQUE constraint error")
	}
}

func (*freshRecoveryExecutor) Cancel(context.Context, ports.ExecutionAttemptID) error { return nil }
