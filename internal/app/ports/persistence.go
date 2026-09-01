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

// AttemptTerminationUpdate is the fenced, recovery-time request that moves a
// crashed ExecutionAttempt from RUNNING to a terminal/quarantine state
// (LOST or INDETERMINATE). It mirrors WorkflowRunTransition's CAS shape at
// the attempt level: a stale caller racing on the same attempt is rejected
// with ErrOptimisticConflict instead of writing a second terminal record.
type AttemptTerminationUpdate struct {
	AttemptID       runtime.ExecutionAttemptID
	ExpectedVersion uint64
	NextState       runtime.ExecutionAttemptState
	Reason          runtime.TerminationReason
	EventID         string
	CorrelationID   string
	OccurredAt      time.Time
}

// NodeCompletionDispatch is the fenced worker path from a RUNNING NodeRun to
// SUCCEEDED plus the downstream job that dispatches the next node. The
// persistence adapter must complete the node, acknowledge the driving job
// and enqueue the downstream job in one transaction: a caller replaying the
// exact same request after a crash (same NextJob.IdempotencyKey) must get
// back the job that transaction already created, never a duplicate.
type NodeCompletionDispatch struct {
	NodeRunID       runtime.NodeRunID
	ExpectedVersion uint64
	SelectedOutcome string
	JobLease        JobLease
	NextJob         EnqueueJobRequest
	EventID         string
	CorrelationID   string
	OccurredAt      time.Time
}

// NodeIntentDispatch is the fenced application transaction that creates a
// WorkflowRun's first durable intent to execute: transitioning the run to
// RUNNING, creating its first NodeRun and enqueuing the job that dispatches
// it, all in one SQLite transaction. It closes crash boundaries 1-2 of
// SPK-04's six-boundary matrix: killed before this commits, nothing exists;
// killed after, everything exists and is claimable exactly once. A caller
// replaying the same request (same Job.IdempotencyKey) after a crash gets
// back the job that transaction already created, never a duplicate.
type NodeIntentDispatch struct {
	RunID              runtime.WorkflowRunID
	ExpectedRunVersion uint64
	NodeRunID          runtime.NodeRunID
	NodeKey            string
	ActivationSequence uint64
	InputStateHash     string
	Job                EnqueueJobRequest
	EventID            string
	CorrelationID      string
	OccurredAt         time.Time
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
