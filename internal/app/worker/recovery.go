package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// BuildFreshRequestFromCheckpoint intentionally has no ProviderSessionRef
// input. Product policy is to resume by starting a new provider execution from
// the platform-owned ContextSnapshot, never by continuing a provider session.
func BuildFreshRequestFromCheckpoint(
	base ports.AgentExecutionRequest,
	snapshot runtime.ContextSnapshot,
	checkpoint runtime.Checkpoint,
) (ports.AgentExecutionRequest, error) {
	if base.AttemptID == "" {
		return ports.AgentExecutionRequest{}, errors.New("new execution attempt id is required")
	}
	if snapshot.ID() == "" || checkpoint.ContextSnapshotID != snapshot.ID() {
		return ports.AgentExecutionRequest{}, errors.New("checkpoint does not reference the supplied context snapshot")
	}
	if base.AttemptID == checkpoint.AttemptID {
		return ports.AgentExecutionRequest{}, errors.New("recovery must use a new execution attempt id")
	}
	if base.ContextSnapshotID != "" && base.ContextSnapshotID != snapshot.ID() {
		return ports.AgentExecutionRequest{}, errors.New("execution request references another context snapshot")
	}
	base.ContextSnapshotID = snapshot.ID()
	base.Prompt = renderCanonicalSnapshot(snapshot, checkpoint)
	return base, nil
}

func StartFreshFromCheckpoint(
	ctx context.Context,
	executor ports.AgentExecutor,
	base ports.AgentExecutionRequest,
	snapshot runtime.ContextSnapshot,
	checkpoint runtime.Checkpoint,
	sink ports.AgentEventSink,
) (ports.AgentExecutionResult, error) {
	if executor == nil {
		return ports.AgentExecutionResult{}, errors.New("agent executor is required")
	}
	request, err := BuildFreshRequestFromCheckpoint(base, snapshot, checkpoint)
	if err != nil {
		return ports.AgentExecutionResult{}, err
	}
	return executor.Start(ctx, request, sink)
}

// RecoveryStore is the narrow read boundary used when a replacement worker
// resumes work after a crash. It deliberately contains no provider-session
// lookup: a session is diagnostic data only and cannot be a dependency for
// recovery.
type RecoveryStore interface {
	LoadLatestCheckpoint(context.Context, runtime.ExecutionAttemptID) (runtime.Checkpoint, error)
	LoadContextSnapshot(context.Context, runtime.ContextSnapshotID) (runtime.ContextSnapshot, error)
}

// StartFreshFromLatestCheckpoint loads durable state using only the interrupted
// attempt ID, then starts a distinct execution attempt from the checkpoint's
// canonical ContextSnapshot. This is the application-level recovery path;
// callers cannot accidentally route it through AgentExecutor.Resume.
func StartFreshFromLatestCheckpoint(
	ctx context.Context,
	store RecoveryStore,
	executor ports.AgentExecutor,
	interruptedAttemptID runtime.ExecutionAttemptID,
	base ports.AgentExecutionRequest,
	sink ports.AgentEventSink,
) (ports.AgentExecutionResult, error) {
	if store == nil {
		return ports.AgentExecutionResult{}, errors.New("recovery store is required")
	}
	if interruptedAttemptID == "" {
		return ports.AgentExecutionResult{}, errors.New("interrupted attempt id is required")
	}
	checkpoint, err := store.LoadLatestCheckpoint(ctx, interruptedAttemptID)
	if err != nil {
		return ports.AgentExecutionResult{}, fmt.Errorf("load latest checkpoint: %w", err)
	}
	snapshot, err := store.LoadContextSnapshot(ctx, checkpoint.ContextSnapshotID)
	if err != nil {
		return ports.AgentExecutionResult{}, fmt.Errorf("load checkpoint context snapshot: %w", err)
	}
	return StartFreshFromCheckpoint(ctx, executor, base, snapshot, checkpoint, sink)
}

func renderCanonicalSnapshot(snapshot runtime.ContextSnapshot, checkpoint runtime.Checkpoint) string {
	var prompt strings.Builder
	prompt.WriteString("You are a new execution resuming a durable software task. ")
	prompt.WriteString("Do not assume access to any prior provider session. ")
	prompt.WriteString("Use this canonical platform context and checkpoint.\n\n")
	fmt.Fprintf(&prompt, "context_snapshot_id: %s\n", snapshot.ID())
	fmt.Fprintf(&prompt, "context_snapshot_hash: %s\n", snapshot.ContentHash())
	fmt.Fprintf(&prompt, "checkpoint_id: %s\n", checkpoint.ID)
	fmt.Fprintf(&prompt, "checkpoint_sequence: %d\n", checkpoint.Sequence)
	fmt.Fprintf(&prompt, "checkpoint_event_sequence: %d\n", checkpoint.CanonicalEventSequence)
	prompt.WriteString("canonical_context_json:\n")
	prompt.Write(snapshot.CanonicalContent())
	prompt.WriteByte('\n')
	return prompt.String()
}
