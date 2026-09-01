package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/scopeguard"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

func TestFinalizerRejectsOutOfScopeDiffBeforePersistence(t *testing.T) {
	persistence := &recordingPersistence{}
	finalizer, err := NewFinalizer(persistence)
	if err != nil {
		t.Fatal(err)
	}
	_, err = finalizer.Finalize(context.Background(), FinalizationInput{
		Finalization: finalizationFixture(),
		EffectiveScopes: []work.RepositoryScope{
			mustWriteScope(t, "repo-user", []string{"src"}),
		},
		Diffs: []ports.WorkspaceDiff{{
			RepositoryID: "repo-user",
			Files:        []ports.FileStatus{{Path: "infra/deploy.yaml"}},
		}},
	})
	if !errors.Is(err, scopeguard.ErrScopeViolation) {
		t.Fatalf("Finalize() error = %v, want ErrScopeViolation", err)
	}
	if persistence.finalizationCalls != 0 {
		t.Fatalf("persistence finalization calls = %d, want 0", persistence.finalizationCalls)
	}
}

func TestFinalizerForwardsCoveredDiffToFencedPersistence(t *testing.T) {
	persistence := &recordingPersistence{}
	finalizer, err := NewFinalizer(persistence)
	if err != nil {
		t.Fatal(err)
	}
	_, err = finalizer.Finalize(context.Background(), FinalizationInput{
		Finalization: finalizationFixture(),
		EffectiveScopes: []work.RepositoryScope{
			mustWriteScope(t, "repo-user", []string{"src"}),
		},
		Diffs: []ports.WorkspaceDiff{{
			RepositoryID: "repo-user",
			Files:        []ports.FileStatus{{Path: "src/main.go"}},
		}},
	})
	if err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}
	if persistence.finalizationCalls != 1 {
		t.Fatalf("persistence finalization calls = %d, want 1", persistence.finalizationCalls)
	}
}

func finalizationFixture() ports.WorkerWorkflowRunFinalization {
	return ports.WorkerWorkflowRunFinalization{
		Transition: ports.WorkflowRunTransition{
			RunID:           "run-1",
			ExpectedState:   runtime.WorkflowRunRunning,
			ExpectedVersion: 2,
			NextState:       runtime.WorkflowRunSucceeded,
			SharedState:     json.RawMessage(`{}`),
			OccurredAt:      time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC),
		},
		JobLease:      ports.JobLease{JobID: "job-1", Owner: "worker-1", Token: 1},
		EventID:       "event-1",
		CorrelationID: "correlation-1",
	}
}

func mustWriteScope(t *testing.T, repository string, paths []string) work.RepositoryScope {
	t.Helper()
	scope, err := work.NewRepositoryScope(
		"family-1", 1, project.RepositoryID(repository), work.RepositoryWrite,
		paths, "test", "tester", time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

type recordingPersistence struct {
	finalizationCalls int
}

func (*recordingPersistence) PublishWorkflowVersion(context.Context, workflow.WorkflowDefinition, workflow.WorkflowVersion) (workflow.WorkflowVersion, error) {
	return workflow.WorkflowVersion{}, errors.New("not used")
}

func (*recordingPersistence) LoadWorkflowVersion(context.Context, workflow.WorkflowVersionID) (workflow.WorkflowVersion, error) {
	return workflow.WorkflowVersion{}, errors.New("not used")
}

func (*recordingPersistence) StartWorkflowRun(context.Context, runtime.WorkflowRun) error {
	return errors.New("not used")
}

func (*recordingPersistence) LoadWorkflowRun(context.Context, runtime.WorkflowRunID) (runtime.WorkflowRun, error) {
	return runtime.WorkflowRun{}, errors.New("not used")
}

func (*recordingPersistence) CompareAndSwapWorkflowRun(context.Context, ports.WorkflowRunTransition) (runtime.WorkflowRun, error) {
	return runtime.WorkflowRun{}, errors.New("not used")
}

func (*recordingPersistence) StoreContextSnapshot(context.Context, project.ProjectID, runtime.ContextSnapshot) (runtime.ContextSnapshot, error) {
	return runtime.ContextSnapshot{}, errors.New("not used")
}

func (*recordingPersistence) LoadContextSnapshot(context.Context, runtime.ContextSnapshotID) (runtime.ContextSnapshot, error) {
	return runtime.ContextSnapshot{}, errors.New("not used")
}

func (*recordingPersistence) StoreCheckpoint(context.Context, runtime.Checkpoint) (runtime.Checkpoint, error) {
	return runtime.Checkpoint{}, errors.New("not used")
}

func (*recordingPersistence) LoadLatestCheckpoint(context.Context, runtime.ExecutionAttemptID) (runtime.Checkpoint, error) {
	return runtime.Checkpoint{}, errors.New("not used")
}

func (p *recordingPersistence) FinalizeWorkflowRun(_ context.Context, _ ports.WorkerWorkflowRunFinalization) (runtime.WorkflowRun, error) {
	p.finalizationCalls++
	return runtime.WorkflowRun{ID: "run-1", State: runtime.WorkflowRunSucceeded}, nil
}
