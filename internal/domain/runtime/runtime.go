package runtime

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

type WorkflowRunID string
type NodeRunID string
type ExecutionAttemptID string
type ContextSnapshotID string
type CheckpointID string
type BlockVersionID string

type WorkflowRunState string

const (
	WorkflowRunCreated WorkflowRunState = "CREATED"
	WorkflowRunRunning WorkflowRunState = "RUNNING"
	WorkflowRunWaiting WorkflowRunState = "WAITING"
	WorkflowRunBlocked WorkflowRunState = "BLOCKED"
	// WorkflowRunVerifying is END's completion-candidate state (ADR-011,
	// ADR-021): added to the closed enum and CHECK constraint now, while the
	// table is still empty, so V4-12 (the first real caller — "END chuyển
	// run sang VERIFYING") never has to rebuild a non-empty workflow_runs
	// table the way V3-01 had to for repositories. No transition in this
	// codebase produces it yet.
	WorkflowRunVerifying WorkflowRunState = "VERIFYING"
	WorkflowRunSucceeded WorkflowRunState = "SUCCEEDED"
	WorkflowRunFailed    WorkflowRunState = "FAILED"
	// WorkflowRunCancelling is the mandatory quiesce phase between a cancel
	// intent and WorkflowRunCancelled (ADR-020's cancellation protocol);
	// every non-terminal state, CREATED included, has an edge into it. The
	// full protocol (durable intent, cancel_epoch fence, quiesce) is
	// V4-12B's own scope — this task only makes the state/enum/transition
	// representable and persistable.
	WorkflowRunCancelling WorkflowRunState = "CANCELLING"
	WorkflowRunCancelled  WorkflowRunState = "CANCELLED"
)

type WorkflowRun struct {
	ID                  WorkflowRunID
	ProjectID           project.ProjectID
	WorkItemID          work.WorkItemID
	WorkflowVersionID   workflow.WorkflowVersionID
	WorkflowVersionHash string
	FamilyID            work.TaskFamilyID
	ScopeVersion        uint64
	PinnedDependencies  workflow.DependencyManifest
	State               WorkflowRunState
	SharedState         json.RawMessage
	Version             uint64
	StartedAt           *time.Time
	FinishedAt          *time.Time
}

func NewWorkflowRun(
	id WorkflowRunID,
	projectID project.ProjectID,
	workItemID work.WorkItemID,
	version workflow.WorkflowVersion,
	familyID work.TaskFamilyID,
	scopeVersion uint64,
	sharedState json.RawMessage,
) (WorkflowRun, error) {
	if id == "" || projectID == "" || workItemID == "" || familyID == "" {
		return WorkflowRun{}, errors.New("workflow run identities are required")
	}
	if version.ID() == "" || version.ContentHash() == "" {
		return WorkflowRun{}, errors.New("published workflow version is required")
	}
	if scopeVersion == 0 {
		return WorkflowRun{}, errors.New("scope version must be greater than zero")
	}
	return WorkflowRun{
		ID:                  id,
		ProjectID:           projectID,
		WorkItemID:          workItemID,
		WorkflowVersionID:   version.ID(),
		WorkflowVersionHash: version.ContentHash(),
		FamilyID:            familyID,
		ScopeVersion:        scopeVersion,
		PinnedDependencies:  version.Dependencies(),
		State:               WorkflowRunCreated,
		SharedState:         append(json.RawMessage(nil), sharedState...),
		Version:             1,
	}, nil
}

type NodeRunState string

const (
	NodeRunPending   NodeRunState = "PENDING"
	NodeRunReady     NodeRunState = "READY"
	NodeRunQueued    NodeRunState = "QUEUED"
	NodeRunRunning   NodeRunState = "RUNNING"
	NodeRunWaiting   NodeRunState = "WAITING"
	NodeRunBlocked   NodeRunState = "BLOCKED"
	NodeRunSucceeded NodeRunState = "SUCCEEDED"
	NodeRunFailed    NodeRunState = "FAILED"
	NodeRunSkipped   NodeRunState = "SKIPPED"
	NodeRunCancelled NodeRunState = "CANCELLED"
)

type NodeRun struct {
	ID                   NodeRunID
	RunID                WorkflowRunID
	NodeKey              string
	ActivationSequence   uint64
	Iteration            uint32
	State                NodeRunState
	EffectiveScope       []work.RepositoryScope
	InputStateHash       string
	BlockVersionID       *BlockVersionID
	ExecutionProfileHash string
	SelectedOutcome      string
	BlockReason          string
	Version              uint64
}

// NewNodeRun validates and builds a new NodeRun activation, PENDING at
// version 1. ExecutionProfileHash may be blank: a structural node with no
// real execution (e.g. START, V4-02) has no profile to pin — only a node
// that actually dispatches an ExecutionAttempt (V4-04) needs one, and that
// task is responsible for supplying it.
func NewNodeRun(
	id NodeRunID,
	runID WorkflowRunID,
	nodeKey string,
	activationSequence uint64,
	iteration uint32,
	effectiveScope []work.RepositoryScope,
	inputStateHash string,
	executionProfileHash string,
) (NodeRun, error) {
	nodeKey = strings.TrimSpace(nodeKey)
	inputStateHash = strings.TrimSpace(inputStateHash)
	executionProfileHash = strings.TrimSpace(executionProfileHash)
	if id == "" || runID == "" || nodeKey == "" {
		return NodeRun{}, errors.New("node run id, workflow run id and node key are required")
	}
	if activationSequence == 0 {
		return NodeRun{}, errors.New("node activation sequence must be greater than zero")
	}
	if inputStateHash == "" {
		return NodeRun{}, errors.New("node input state hash is required")
	}
	return NodeRun{
		ID:                   id,
		RunID:                runID,
		NodeKey:              nodeKey,
		ActivationSequence:   activationSequence,
		Iteration:            iteration,
		State:                NodeRunPending,
		EffectiveScope:       append([]work.RepositoryScope(nil), effectiveScope...),
		InputStateHash:       inputStateHash,
		ExecutionProfileHash: executionProfileHash,
		Version:              1,
	}, nil
}

type ExecutionAttemptState string

const (
	ExecutionAttemptQueued        ExecutionAttemptState = "QUEUED"
	ExecutionAttemptRunning       ExecutionAttemptState = "RUNNING"
	ExecutionAttemptSucceeded     ExecutionAttemptState = "SUCCEEDED"
	ExecutionAttemptFailed        ExecutionAttemptState = "FAILED"
	ExecutionAttemptTimedOut      ExecutionAttemptState = "TIMED_OUT"
	ExecutionAttemptCancelled     ExecutionAttemptState = "CANCELLED"
	ExecutionAttemptLost          ExecutionAttemptState = "LOST"
	ExecutionAttemptIndeterminate ExecutionAttemptState = "INDETERMINATE"
	// ExecutionAttemptBlocked is ADR-020's terminal business/admission
	// blocker state, deliberately separate from ExecutionAttemptFailed: it
	// never consumes AttemptPolicy retry budget and a BLOCKED attempt is
	// never revived (RetryBlockedActivation instead creates a new
	// activation/Attempt after revalidating the exact pin — V4-12A/V4-13's
	// own scope, not this one's).
	ExecutionAttemptBlocked ExecutionAttemptState = "BLOCKED"
)

type ExecutionAttempt struct {
	ID                   ExecutionAttemptID
	NodeRunID            NodeRunID
	AttemptNumber        uint32
	State                ExecutionAttemptState
	ProviderKey          string
	ExecutionProfileHash string
	ContextSnapshotID    *ContextSnapshotID
	InputRevisionSet     *workspace.RevisionSet
	LastCheckpointID     *CheckpointID
	StartedAt            *time.Time
	FinishedAt           *time.Time
	TerminationReason    TerminationReason
	Version              uint64
}

func NewExecutionAttempt(
	id ExecutionAttemptID,
	nodeRunID NodeRunID,
	attemptNumber uint32,
	executionProfileHash string,
	providerKey string,
	inputRevisionSet *workspace.RevisionSet,
) (ExecutionAttempt, error) {
	executionProfileHash = strings.TrimSpace(executionProfileHash)
	if id == "" || nodeRunID == "" {
		return ExecutionAttempt{}, errors.New("execution attempt id and node run id are required")
	}
	if attemptNumber == 0 {
		return ExecutionAttempt{}, errors.New("attempt number must be greater than zero")
	}
	if executionProfileHash == "" {
		return ExecutionAttempt{}, errors.New("execution profile hash is required")
	}
	var pinnedRevisionSet *workspace.RevisionSet
	if inputRevisionSet != nil {
		copyOfSet, err := workspace.NewRevisionSet(inputRevisionSet.Entries())
		if err != nil {
			return ExecutionAttempt{}, err
		}
		pinnedRevisionSet = &copyOfSet
	}
	return ExecutionAttempt{
		ID:                   id,
		NodeRunID:            nodeRunID,
		AttemptNumber:        attemptNumber,
		State:                ExecutionAttemptQueued,
		ProviderKey:          strings.TrimSpace(providerKey),
		ExecutionProfileHash: executionProfileHash,
		InputRevisionSet:     pinnedRevisionSet,
		Version:              1,
	}, nil
}
