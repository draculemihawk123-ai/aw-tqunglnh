package runtime

import (
	"errors"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// CancellationIntentState tracks whether a durable cancellation intent's own
// quiesce has finished. V4-01 only persists the intent; the coordinator that
// actually quiesces WAIT/APPROVAL/jobs/attempts and drives this to COMPLETED
// is V4-12B (run-level) / V4-12C (work-item-level) — this state exists now
// so V4-13's recovery reaper has a durable signal for "an intent committed
// but the coordinator died before quiesce finished" without needing a
// second migration later.
type CancellationIntentState string

const (
	CancellationIntentRequested CancellationIntentState = "REQUESTED"
	CancellationIntentCompleted CancellationIntentState = "COMPLETED"
)

// RunCancellationIntentID identifies one durable run-level cancel intent.
type RunCancellationIntentID string

// RunCancellationIntent is the durable intent ADR-020's cancellation
// protocol requires before a WorkflowRun may ever move to CANCELLING: "một
// intent theo run, idempotent" — at most one row per RunID ever exists,
// enforced by a unique constraint, so a second CancelRun call for the same
// run always observes and returns this same row rather than creating a
// second one.
type RunCancellationIntent struct {
	ID          RunCancellationIntentID
	ProjectID   project.ProjectID
	RunID       WorkflowRunID
	Actor       string
	Reason      string
	State       CancellationIntentState
	RequestedAt time.Time
}

// NewRunCancellationIntent validates and builds a new, REQUESTED run
// cancellation intent.
func NewRunCancellationIntent(
	id RunCancellationIntentID,
	projectID project.ProjectID,
	runID WorkflowRunID,
	actor string,
	reason string,
	requestedAt time.Time,
) (RunCancellationIntent, error) {
	actor = strings.TrimSpace(actor)
	reason = strings.TrimSpace(reason)
	if id == "" || projectID == "" || runID == "" {
		return RunCancellationIntent{}, errors.New("run cancellation intent identities are required")
	}
	if actor == "" || reason == "" {
		return RunCancellationIntent{}, errors.New("run cancellation intent actor and reason are required")
	}
	if requestedAt.IsZero() {
		return RunCancellationIntent{}, errors.New("run cancellation intent requested timestamp is required")
	}
	return RunCancellationIntent{
		ID:          id,
		ProjectID:   projectID,
		RunID:       runID,
		Actor:       actor,
		Reason:      reason,
		State:       CancellationIntentRequested,
		RequestedAt: requestedAt.UTC(),
	}, nil
}

// WorkItemCancellationIntentID identifies one durable work-item-level
// cancel intent.
type WorkItemCancellationIntentID string

// WorkItemCancellationIntent is CancelWorkItem's own durable intent
// (ADR-020: "một task nhiều run nên intent theo Run là không đủ") — at most
// one row per WorkItemID ever exists. It fences both ResolveWorkItemBlocker
// (must not reopen a WorkItem an intent already targets) and
// StartWorkflowRun (must not admit a new Run for a WorkItem being
// cancelled); those two CAS checks are V4-02/V4-12C's own scope, not this
// table's.
type WorkItemCancellationIntent struct {
	ID          WorkItemCancellationIntentID
	ProjectID   project.ProjectID
	WorkItemID  work.WorkItemID
	Actor       string
	Reason      string
	State       CancellationIntentState
	RequestedAt time.Time
}

// NewWorkItemCancellationIntent validates and builds a new, REQUESTED
// work-item cancellation intent.
func NewWorkItemCancellationIntent(
	id WorkItemCancellationIntentID,
	projectID project.ProjectID,
	workItemID work.WorkItemID,
	actor string,
	reason string,
	requestedAt time.Time,
) (WorkItemCancellationIntent, error) {
	actor = strings.TrimSpace(actor)
	reason = strings.TrimSpace(reason)
	if id == "" || projectID == "" || workItemID == "" {
		return WorkItemCancellationIntent{}, errors.New("work item cancellation intent identities are required")
	}
	if actor == "" || reason == "" {
		return WorkItemCancellationIntent{}, errors.New("work item cancellation intent actor and reason are required")
	}
	if requestedAt.IsZero() {
		return WorkItemCancellationIntent{}, errors.New("work item cancellation intent requested timestamp is required")
	}
	return WorkItemCancellationIntent{
		ID:          id,
		ProjectID:   projectID,
		WorkItemID:  workItemID,
		Actor:       actor,
		Reason:      reason,
		State:       CancellationIntentRequested,
		RequestedAt: requestedAt.UTC(),
	}, nil
}
