// V12-19 (docs/design/15-v12-ui-operations.md, UX-30, ADR-054): the family task queue. An operator queues several WorkItems of a
// family; the worker (AdvanceFamilyQueues) starts the next one when it may. A family shares one worktree, so the queue runs one task
// at a time and strictly in order:
//
//   - only the QUEUED entry with the lowest position (the head) may start;
//   - no run of the family may be active (a run that is not SUCCEEDED, FAILED or CANCELLED);
//   - the task the queue started before it must be DONE — a task that failed or was cancelled pauses the queue (fail-closed): the
//     operator decides (run it again, or take it out of the queue);
//   - the task the head waits for, if any, must be DONE.
//
// The head starts through StartWorkflowRun with the WorkItem's pinned workflow version, as an operator would. When it cannot start
// (not READY, a baseline not admitted, …) the entry stays QUEUED with the reason in Problem, and the next sweep tries again.
package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// queueSystemActor is the actor of the runs the queue starts.
const queueSystemActor = "aw-queue"

// Errors of the queue commands.
var (
	ErrQueueWrongFamily       = errors.New("runtime: the task to wait for is not in the same family")
	ErrQueueNoWorkflowVersion = errors.New("runtime: the work item has no pinned workflow version; it cannot be queued")
	ErrQueueWorkItemClosed    = errors.New("runtime: a DONE or CANCELLED work item cannot be queued")
	ErrQueueWaitsForItself    = errors.New("runtime: a work item cannot wait for itself")
)

// SetQueueRequest puts a WorkItem in its family's queue (Queued) or takes it out.
type SetQueueRequest struct {
	ProjectID  string
	WorkItemID string
	Queued     bool
	// Position orders the queue (lower first); nil = after every entry of the family.
	Position *int
	// WaitsForWorkItemID: the head starts only once this WorkItem (same family) is DONE.
	WaitsForWorkItemID string
}

// QueueEntryView is one entry of a family queue as the API shows it.
type QueueEntryView struct {
	WorkItemID         string    `json:"workItemId"`
	WorkItemTitle      string    `json:"workItemTitle"`
	WorkItemStatus     string    `json:"workItemStatus"`
	Position           int       `json:"position"`
	WaitsForWorkItemID string    `json:"waitsForWorkItemId,omitempty"`
	State              string    `json:"state"`
	RunID              string    `json:"runId,omitempty"`
	Problem            string    `json:"problem,omitempty"`
	Version            uint64    `json:"version"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

// FamilyQueue is a family's queue, in order, and whether (and why) it waits.
type FamilyQueue struct {
	ProjectID string           `json:"projectId"`
	FamilyID  string           `json:"familyId"`
	Entries   []QueueEntryView `json:"entries"`
	// Waiting says what the head waits for (empty when the queue is empty or the head is about to start).
	Waiting string `json:"waiting,omitempty"`
}

// SetWorkItemQueue queues a WorkItem (or takes it out of the queue), idempotently by cmd's receipt.
func SetWorkItemQueue(ctx context.Context, uow ports.UnitOfWork, cmd ports.Command, req SetQueueRequest) (QueueEntryView, error) {
	var result QueueEntryView
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		receipt, found, err := tx.Receipts().Load(ctx, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
		if err != nil {
			return err
		}
		if found {
			if receipt.RequestHash != cmd.RequestHash {
				return ports.ErrReceiptConflict
			}
			return json.Unmarshal([]byte(receipt.ResultJSON), &result)
		}
		item, err := tx.Work().GetWorkItem(ctx, req.WorkItemID)
		if err != nil {
			return err
		}
		if string(item.ProjectID) != req.ProjectID {
			return fmt.Errorf("%w: work item %s", ports.ErrPersistenceNotFound, req.WorkItemID)
		}
		existing, err := tx.Queue().GetQueueEntry(ctx, req.WorkItemID)
		var expected uint64
		switch {
		case err == nil:
			expected = existing.Version
		case !errors.Is(err, ports.ErrPersistenceNotFound):
			return err
		}
		entry := existing
		entry.WorkItemID, entry.ProjectID, entry.FamilyID, entry.UpdatedAt = req.WorkItemID, req.ProjectID, string(item.FamilyID), cmd.RequestedAt
		if expected == 0 {
			entry.CreatedAt = cmd.RequestedAt
		}
		if req.Queued {
			if item.Status == workdomain.WorkItemDone || item.Status == workdomain.WorkItemCancelled {
				return ErrQueueWorkItemClosed
			}
			if item.WorkflowVersionID == nil {
				return ErrQueueNoWorkflowVersion
			}
			if req.WaitsForWorkItemID != "" {
				if req.WaitsForWorkItemID == req.WorkItemID {
					return ErrQueueWaitsForItself
				}
				other, err := tx.Work().GetWorkItem(ctx, req.WaitsForWorkItemID)
				if err != nil {
					return err
				}
				if other.FamilyID != item.FamilyID {
					return ErrQueueWrongFamily
				}
			}
			entries, err := tx.Queue().ListQueueEntriesForFamily(ctx, string(item.FamilyID))
			if err != nil {
				return err
			}
			position := 1
			for _, e := range entries {
				if e.WorkItemID != req.WorkItemID && e.Position >= position {
					position = e.Position + 1
				}
			}
			if req.Position != nil {
				position = *req.Position
			}
			entry.Position, entry.WaitsForWorkItemID, entry.State, entry.RunID, entry.Problem = position, req.WaitsForWorkItemID, ports.QueueQueued, "", ""
		} else {
			if expected == 0 {
				return fmt.Errorf("%w: work item %s is not in a queue", ports.ErrPersistenceNotFound, req.WorkItemID)
			}
			if existing.State != ports.QueueQueued {
				return fmt.Errorf("%w: queue entry of %s is %s, only a QUEUED entry can be taken out", ports.ErrOptimisticConflict, req.WorkItemID, existing.State)
			}
			entry.State, entry.Problem = ports.QueueRemoved, ""
		}
		stored, err := tx.Queue().PutQueueEntry(ctx, entry, expected)
		if err != nil {
			return err
		}
		result = queueView(stored, item)
		resultJSON, err := json.Marshal(result)
		if err != nil {
			return err
		}
		return tx.Receipts().Record(ctx, ports.Receipt{
			Actor: cmd.Actor, Scope: cmd.Scope, IdempotencyKey: cmd.IdempotencyKey, CommandType: cmd.Type,
			RequestHash: cmd.RequestHash, ResultJSON: string(resultJSON), CreatedAt: cmd.RequestedAt,
		})
	})
	return result, err
}

func queueView(e ports.QueueEntry, item workdomain.WorkItem) QueueEntryView {
	return QueueEntryView{
		WorkItemID: e.WorkItemID, WorkItemTitle: item.Title, WorkItemStatus: string(item.Status), Position: e.Position,
		WaitsForWorkItemID: e.WaitsForWorkItemID, State: string(e.State), RunID: e.RunID, Problem: e.Problem, Version: e.Version, UpdatedAt: e.UpdatedAt,
	}
}

// GetFamilyQueue reads a family's queue: its QUEUED and STARTED entries in order, and what the head waits for.
func GetFamilyQueue(ctx context.Context, uow ports.UnitOfWork, projectID, familyID string) (FamilyQueue, error) {
	out := FamilyQueue{ProjectID: projectID, FamilyID: familyID, Entries: []QueueEntryView{}}
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		family, err := tx.Work().GetTaskFamily(ctx, familyID)
		if err != nil {
			return err
		}
		if string(family.ProjectID) != projectID {
			return fmt.Errorf("%w: family %s", ports.ErrPersistenceNotFound, familyID)
		}
		entries, err := tx.Queue().ListQueueEntriesForFamily(ctx, familyID)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.State == ports.QueueRemoved {
				continue
			}
			item, err := tx.Work().GetWorkItem(ctx, e.WorkItemID)
			if err != nil {
				return err
			}
			out.Entries = append(out.Entries, queueView(e, item))
		}
		if _, why, err := queueHeadTx(ctx, tx, familyID); err != nil {
			return err
		} else {
			out.Waiting = why
		}
		return nil
	})
	return out, err
}

// queueHeadTx returns the entry that may start now, or why none may (a plain sentence for the operator; empty with no head
// when the queue is empty).
func queueHeadTx(ctx context.Context, tx ports.Tx, familyID string) (*ports.QueueEntry, string, error) {
	entries, err := tx.Queue().ListQueueEntriesForFamily(ctx, familyID)
	if err != nil {
		return nil, "", err
	}
	var head *ports.QueueEntry
	var lastStarted *ports.QueueEntry
	for i := range entries {
		e := entries[i]
		switch e.State {
		case ports.QueueQueued:
			if head == nil {
				head = &e
			}
		case ports.QueueStarted:
			if lastStarted == nil || e.UpdatedAt.After(lastStarted.UpdatedAt) {
				lastStarted = &e
			}
		}
	}
	if head == nil {
		return nil, "", nil
	}
	if lastStarted != nil {
		prev, err := tx.Work().GetWorkItem(ctx, lastStarted.WorkItemID)
		if err != nil {
			return nil, "", err
		}
		if prev.Status != workdomain.WorkItemDone {
			if prev.Status == workdomain.WorkItemCancelled || prev.Status == workdomain.WorkItemBlocked || latestRunFailed(ctx, tx, lastStarted.WorkItemID) {
				return nil, fmt.Sprintf("paused: %q did not finish (%s); run it again or take the next task out of the queue", prev.Title, prev.Status), nil
			}
			return nil, fmt.Sprintf("waiting for %q to finish", prev.Title), nil
		}
	}
	items, err := tx.Work().ListWorkItemsByProject(ctx, head.ProjectID)
	if err != nil {
		return nil, "", err
	}
	for _, item := range items {
		if string(item.FamilyID) != familyID {
			continue
		}
		runs, err := tx.Runtime().ListWorkflowRunsForWorkItem(ctx, string(item.ID))
		if err != nil {
			return nil, "", err
		}
		for _, run := range runs {
			if !runEnded(run.State) {
				return nil, fmt.Sprintf("waiting: %q has a %s run", item.Title, run.State), nil
			}
		}
	}
	if head.WaitsForWorkItemID != "" {
		other, err := tx.Work().GetWorkItem(ctx, head.WaitsForWorkItemID)
		if err != nil {
			return nil, "", err
		}
		if other.Status != workdomain.WorkItemDone {
			return nil, fmt.Sprintf("waiting for %q (%s)", other.Title, other.Status), nil
		}
	}
	return head, "", nil
}

func runEnded(state runtimedomain.WorkflowRunState) bool {
	return state == runtimedomain.WorkflowRunSucceeded || state == runtimedomain.WorkflowRunFailed || state == runtimedomain.WorkflowRunCancelled
}

// latestRunFailed reports whether the newest run of workItemID ended FAILED or CANCELLED (a run started again after it — Run Again —
// takes its place, so the queue resumes when that one succeeds).
func latestRunFailed(ctx context.Context, tx ports.Tx, workItemID string) bool {
	runs, err := tx.Runtime().ListWorkflowRunsForWorkItem(ctx, workItemID)
	if err != nil || len(runs) == 0 {
		return false
	}
	newest := runs[0]
	for _, r := range runs {
		if !runEnded(r.State) {
			return false // a run is going again
		}
		if r.StartedAt != nil && (newest.StartedAt == nil || r.StartedAt.After(*newest.StartedAt)) {
			newest = r
		}
	}
	return newest.State == runtimedomain.WorkflowRunFailed || newest.State == runtimedomain.WorkflowRunCancelled
}

// QueueAdvanceReport is what one AdvanceFamilyQueues sweep did.
type QueueAdvanceReport struct {
	Started []string
	Waiting map[string]string
}

// AdvanceFamilyQueues starts the head of every family queue that may start (see the package comment of this file). It is
// idempotent: the start command's idempotency key names the entry's version, so two workers sweeping at once start one run.
func AdvanceFamilyQueues(ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, now time.Time) (QueueAdvanceReport, error) {
	report := QueueAdvanceReport{Waiting: map[string]string{}}
	var families []string
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		families, err = tx.Queue().ListFamiliesWithQueuedEntries(ctx)
		return err
	}); err != nil {
		return report, err
	}
	var failures []error
	for _, familyID := range families {
		if ctx.Err() != nil {
			break
		}
		var head *ports.QueueEntry
		var workflowVersionID string
		if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
			h, why, err := queueHeadTx(ctx, tx, familyID)
			if err != nil || h == nil {
				report.Waiting[familyID] = why
				return err
			}
			item, err := tx.Work().GetWorkItem(ctx, h.WorkItemID)
			if err != nil {
				return err
			}
			if item.WorkflowVersionID != nil {
				workflowVersionID = string(*item.WorkflowVersionID)
			}
			head = h
			return nil
		}); err != nil {
			failures = append(failures, fmt.Errorf("queue of family %s: %w", familyID, err))
			continue
		}
		if head == nil {
			continue
		}
		key := fmt.Sprintf("queue-start:%s:v%d", head.WorkItemID, head.Version)
		digest := sha256.Sum256([]byte(key + "|" + workflowVersionID))
		started, startErr := StartWorkflowRun(ctx, uow, ids, ports.Command{
			ID: ids.NewID(), IdempotencyKey: key, Actor: queueSystemActor, CorrelationID: ids.NewID(),
			Scope: ports.ProjectScope(head.ProjectID), RequestedAt: now, Type: "StartWorkflowRun", RequestHash: hex.EncodeToString(digest[:]),
		}, StartWorkflowRunRequest{ProjectID: head.ProjectID, WorkItemID: head.WorkItemID, WorkflowVersionID: workflowVersionID})
		if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
			current, err := tx.Queue().GetQueueEntry(ctx, head.WorkItemID)
			if err != nil || current.Version != head.Version || current.State != ports.QueueQueued {
				return err // the operator changed the entry meanwhile; the next sweep decides again
			}
			next := current
			next.UpdatedAt = now
			if startErr != nil {
				problem := boundProblem(startErr.Error())
				if current.Problem == problem {
					return nil
				}
				next.Problem = problem
			} else {
				next.State, next.RunID, next.Problem = ports.QueueStarted, started.RunID, ""
			}
			_, err = tx.Queue().PutQueueEntry(ctx, next, current.Version)
			return err
		}); err != nil {
			failures = append(failures, fmt.Errorf("record queue start of %s: %w", head.WorkItemID, err))
			continue
		}
		if startErr == nil {
			report.Started = append(report.Started, head.WorkItemID)
		} else {
			report.Waiting[familyID] = boundProblem(startErr.Error())
		}
	}
	return report, errors.Join(failures...)
}

func boundProblem(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 500 {
		s = s[:500] + "…"
	}
	return s
}
