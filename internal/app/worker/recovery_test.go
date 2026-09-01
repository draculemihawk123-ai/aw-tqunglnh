package worker

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

func TestStartFreshFromCheckpointNeverUsesProviderResume(t *testing.T) {
	snapshot, checkpoint := recoveryFixture(t)
	executor := &recoveryExecutor{}
	result, err := StartFreshFromCheckpoint(context.Background(), executor, ports.AgentExecutionRequest{
		AttemptID:        "attempt-new",
		WorkingDirectory: t.TempDir(),
		Timeout:          time.Second,
	}, snapshot, checkpoint, ports.AgentEventSinkFunc(func(context.Context, ports.AgentEvent) error { return nil }))
	if err != nil {
		t.Fatalf("StartFreshFromCheckpoint() error = %v", err)
	}
	if result.AttemptID != "attempt-new" || executor.startCalls != 1 || executor.resumeCalls != 0 {
		t.Fatalf("unexpected fresh-start execution: result=%+v start=%d resume=%d", result, executor.startCalls, executor.resumeCalls)
	}
	if executor.request.ContextSnapshotID != snapshot.ID() ||
		!strings.Contains(executor.request.Prompt, string(snapshot.ID())) ||
		!strings.Contains(executor.request.Prompt, string(checkpoint.ID)) {
		t.Fatalf("fresh request does not carry canonical recovery context: %+v", executor.request)
	}
}

func TestBuildFreshRequestRejectsSameAttemptOrSnapshotMismatch(t *testing.T) {
	snapshot, checkpoint := recoveryFixture(t)
	if _, err := BuildFreshRequestFromCheckpoint(ports.AgentExecutionRequest{AttemptID: checkpoint.AttemptID}, snapshot, checkpoint); err == nil {
		t.Fatal("same-attempt recovery was accepted")
	}
	other := checkpoint
	other.ContextSnapshotID = "other-context"
	if _, err := BuildFreshRequestFromCheckpoint(ports.AgentExecutionRequest{AttemptID: "attempt-new"}, snapshot, other); err == nil {
		t.Fatal("mismatched context snapshot was accepted")
	}
}

func TestStartFreshFromLatestCheckpointLoadsDurableContextAndNeverResumes(t *testing.T) {
	snapshot, checkpoint := recoveryFixture(t)
	store := &recoveryStore{snapshot: snapshot, checkpoint: checkpoint}
	executor := &recoveryExecutor{}
	_, err := StartFreshFromLatestCheckpoint(
		context.Background(),
		store,
		executor,
		checkpoint.AttemptID,
		ports.AgentExecutionRequest{AttemptID: "attempt-replacement", WorkingDirectory: t.TempDir(), Timeout: time.Second},
		ports.AgentEventSinkFunc(func(context.Context, ports.AgentEvent) error { return nil }),
	)
	if err != nil {
		t.Fatalf("StartFreshFromLatestCheckpoint() error = %v", err)
	}
	if store.loadedAttemptID != checkpoint.AttemptID || store.loadedSnapshotID != snapshot.ID() {
		t.Fatalf("recovery did not load durable chain: attempt=%s snapshot=%s", store.loadedAttemptID, store.loadedSnapshotID)
	}
	if executor.startCalls != 1 || executor.resumeCalls != 0 || executor.request.ContextSnapshotID != snapshot.ID() {
		t.Fatalf("recovery execution = start:%d resume:%d request:%+v", executor.startCalls, executor.resumeCalls, executor.request)
	}
}

func recoveryFixture(t *testing.T) (runtime.ContextSnapshot, runtime.Checkpoint) {
	t.Helper()
	revisions, err := workspace.NewRevisionSet([]workspace.Revision{{
		RepositoryID: project.RepositoryID("repo-1"), VCSObjectID: "abc", WorkspaceGeneration: 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := runtime.NewContextSnapshot(runtime.ContextSnapshotInput{
		ID: "context-1", AttemptID: "attempt-old", Messages: []runtime.ContextMessage{{Role: runtime.ContextRoleUser, Content: "Continue task"}},
		Revisions: revisions, CreatedAt: time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := runtime.NewCheckpoint(
		"checkpoint-1", "run-1", "node-1", "attempt-old", 1, 4, snapshot.ID(), revisions,
		"sha256:state", nil, time.Date(2026, 8, 28, 0, 0, 1, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot, checkpoint
}

type recoveryExecutor struct {
	request     ports.AgentExecutionRequest
	startCalls  int
	resumeCalls int
}

type recoveryStore struct {
	snapshot         runtime.ContextSnapshot
	checkpoint       runtime.Checkpoint
	loadedAttemptID  runtime.ExecutionAttemptID
	loadedSnapshotID runtime.ContextSnapshotID
}

func (s *recoveryStore) LoadLatestCheckpoint(_ context.Context, attemptID runtime.ExecutionAttemptID) (runtime.Checkpoint, error) {
	s.loadedAttemptID = attemptID
	if attemptID != s.checkpoint.AttemptID {
		return runtime.Checkpoint{}, errors.New("checkpoint not found")
	}
	return s.checkpoint, nil
}

func (s *recoveryStore) LoadContextSnapshot(_ context.Context, id runtime.ContextSnapshotID) (runtime.ContextSnapshot, error) {
	s.loadedSnapshotID = id
	if id != s.snapshot.ID() {
		return runtime.ContextSnapshot{}, errors.New("context snapshot not found")
	}
	return s.snapshot, nil
}

func (e *recoveryExecutor) Capabilities(context.Context) (ports.AgentCapabilities, error) {
	return ports.AgentCapabilities{Provider: ports.ProviderCodex, AdapterVersion: "test", ProtocolVersion: "test", SupportsStart: true}, nil
}

func (e *recoveryExecutor) Start(_ context.Context, request ports.AgentExecutionRequest, _ ports.AgentEventSink) (ports.AgentExecutionResult, error) {
	e.startCalls++
	e.request = request
	return ports.AgentExecutionResult{AttemptID: request.AttemptID, Provider: ports.ProviderCodex}, nil
}

func (e *recoveryExecutor) Resume(context.Context, ports.AgentExecutionRequest, ports.ProviderSessionRef, ports.AgentEventSink) (ports.AgentExecutionResult, error) {
	e.resumeCalls++
	return ports.AgentExecutionResult{}, errors.New("Resume must never be called by fresh recovery")
}

func (*recoveryExecutor) Cancel(context.Context, ports.ExecutionAttemptID) error { return nil }
