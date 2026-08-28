package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/worker"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

func TestCheckpointPersistsLatestRecoveryPointAcrossRestart(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "agentkit-checkpoint.db")
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
	databasePath := filepath.Join(t.TempDir(), "agentkit-fresh-recovery.db")
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

func (*freshRecoveryExecutor) Cancel(context.Context, ports.ExecutionAttemptID) error { return nil }
