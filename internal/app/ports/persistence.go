package ports

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

var (
	ErrPersistenceNotFound      = errors.New("persistent record was not found")
	ErrPersistenceAlreadyExists = errors.New("persistent record already exists")
	ErrImmutableVersionConflict = errors.New("immutable workflow version conflicts with persisted content")
	ErrOptimisticConflict       = errors.New("aggregate changed since it was loaded")
	ErrPinnedVersionMismatch    = errors.New("workflow run does not match its pinned workflow version")
	ErrWorkerFinalizationNeeded = errors.New("terminal worker transition requires fenced finalization")
)

// WorkflowRunTransition is an optimistic compare-and-swap request. Callers
// must provide both the state and aggregate version they observed; a stale
// caller is rejected instead of overwriting a transition committed by another
// worker.
type WorkflowRunTransition struct {
	RunID           runtime.WorkflowRunID
	ExpectedState   runtime.WorkflowRunState
	ExpectedVersion uint64
	NextState       runtime.WorkflowRunState
	SharedState     json.RawMessage
	OccurredAt      time.Time
}

// WorkerWorkflowRunFinalization is the only worker-authorized path from a
// RUNNING workflow run to a terminal state. The persistence adapter must
// validate the job and optional workspace fencing proofs, transition the run,
// append the correlated domain event and complete the durable job in one
// transaction. A failure in any operation rolls the whole finalization back.
type WorkerWorkflowRunFinalization struct {
	Transition    WorkflowRunTransition
	JobLease      JobLease
	WriteLeases   []WriteLeaseGrant
	EventID       string
	CorrelationID string
}

// WorkflowPersistence is deliberately narrow for the spike. WorkflowVersion
// has no update operation: publishing either inserts a new immutable snapshot
// or returns the already-published snapshot with identical canonical content.
type WorkflowPersistence interface {
	PublishWorkflowVersion(
		context.Context,
		workflow.WorkflowDefinition,
		workflow.WorkflowVersion,
	) (workflow.WorkflowVersion, error)
	LoadWorkflowVersion(context.Context, workflow.WorkflowVersionID) (workflow.WorkflowVersion, error)

	StartWorkflowRun(context.Context, runtime.WorkflowRun) error
	LoadWorkflowRun(context.Context, runtime.WorkflowRunID) (runtime.WorkflowRun, error)
	CompareAndSwapWorkflowRun(context.Context, WorkflowRunTransition) (runtime.WorkflowRun, error)
	FinalizeWorkflowRun(context.Context, WorkerWorkflowRunFinalization) (runtime.WorkflowRun, error)
	StoreContextSnapshot(context.Context, project.ProjectID, runtime.ContextSnapshot) (runtime.ContextSnapshot, error)
	LoadContextSnapshot(context.Context, runtime.ContextSnapshotID) (runtime.ContextSnapshot, error)
	StoreCheckpoint(context.Context, runtime.Checkpoint) (runtime.Checkpoint, error)
	LoadLatestCheckpoint(context.Context, runtime.ExecutionAttemptID) (runtime.Checkpoint, error)
}
