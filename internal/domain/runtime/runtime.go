package runtime

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/contextsnapshot"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
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
	// CancelEpoch is populated now (V4-12B): nil until a RunCancellationIntent
	// commits, then set to 1 in that SAME transaction as the CAS to
	// CANCELLING — never reset, never bumped again (a Run is ever cancelled
	// at most once; run_cancellation_intents.run_id is already UNIQUE).
	// Every non-terminal RUN_WORK durable_jobs row of this Run is fenced by
	// the identical value in the same transaction (ports.DurableJob.CancelEpoch).
	CancelEpoch *uint64
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
	// ManifestRevision is the RunManifestAmendment.Revision (0 meaning
	// "the initial ExecutionManifest, no amendment yet") this NodeRun's
	// own EffectiveScope/ExecutionProfileHash were resolved against
	// (V4-04, GC-INV-08's own "exact manifest revision"). Zero/unset until
	// scheduling pins it — a structural node (START, ROUTER) that never
	// dispatches an Attempt never needs one.
	ManifestRevision uint64
	// BranchTokenID is populated now (V4-10, HE-14-M09): nil for every
	// NodeRun outside a FORK's own branch (the common case — including the
	// FORK's own NodeRun itself, and anything reached before the first
	// FORK or after its own JOIN). A non-nil value names the exact
	// BranchToken this NodeRun activation belongs to — set by the caller
	// after construction (NewNodeRun itself never takes it, mirroring how
	// State is already set post-construction by callers that need
	// something other than the default PENDING), and propagated unchanged
	// by advanceRunTx across every ordinary hop within a branch, in lock-
	// step with a CAS on that same token's own CurrentNodeKey.
	BranchTokenID *BranchTokenID
	// ReactivationReason is populated now (V4-12A, AK-ARCH-015A): "" for
	// every ordinary activation this codebase creates (advanceRunTx's own
	// normal routing, V4-07's own cycle escalation, V4-10's own fork
	// fan-out — none of those ever set it), "SCOPE_EXPANDED" only for the
	// one NodeRun this task's own reactivation logic creates once an
	// approved scope-expansion request's own repositories are durably
	// READY. Set post-construction by that one caller, mirroring how
	// State/BranchTokenID are already set post-construction elsewhere in
	// this codebase rather than threaded through NewNodeRun's own
	// constructor.
	ReactivationReason string
	Version            uint64
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
	// ContextSnapshotID references V5-04's own
	// internal/domain/contextsnapshot.Snapshot — deliberately NOT this
	// package's own (legacy, crash-recovery-only) ContextSnapshotID type;
	// see contextsnapshot's own package doc comment for why the two are
	// kept apart and why this package imports contextsnapshot rather than
	// the reverse. nil until NewExecutionAttempt's caller explicitly
	// binds it (schedule.go/finalize.go, V5-04) — every production
	// scheduling path added from V5-04 onward MUST set this to a real,
	// already-durable Snapshot ID before the Attempt becomes visible to
	// dispatch; existing callers that never set it (pre-V5-04 tests) are
	// unaffected, since NewExecutionAttempt's own signature is unchanged.
	ContextSnapshotID *contextsnapshot.ID
	InputRevisionSet  *workspace.RevisionSet
	LastCheckpointID  *CheckpointID
	StartedAt         *time.Time
	FinishedAt        *time.Time
	TerminationReason TerminationReason
	// FailureCode is populated now (V4-06) for a terminal FAILED or
	// TIMED_OUT attempt: the exact errorcode.Code (go-core-spec §18) this
	// attempt's own failure classifies as, durably pinned so a retry
	// decision can be re-evaluated across a process restart from this
	// field alone, never from TerminationReason (a different, coarser
	// vocabulary — RUNNING->terminal transition kind, not a specific
	// AppError code) and never by re-parsing an error message. Blank for
	// every other terminal state (SUCCEEDED, CANCELLED, LOST,
	// INDETERMINATE, BLOCKED) — those either have no failure to classify
	// or are already fail-closed by construction.
	FailureCode errorcode.Code
	Version     uint64
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
