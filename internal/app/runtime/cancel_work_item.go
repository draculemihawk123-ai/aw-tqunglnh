// CancelWorkItem (V4-12C, docs/design/06-v4-runtime-engine.md, ADR-020) is
// the WorkItem-level counterpart of CancelRun: "một task nhiều run nên
// intent theo Run là không đủ". Inside one
// ports.UnitOfWork.WithSerializedWrite transaction it (1) records the one
// durable WorkItemCancellationIntent a WorkItem ever has (idempotent by
// WorkItemID — a duplicate call is a harmless no-op), (2) drives EVERY
// currently-non-terminal Run this WorkItem has ever had through the exact
// same quiesce protocol CancelRun itself uses (cancelRunTx, cancel_run.go —
// extracted from that file specifically so this command can compose it
// inside its OWN transaction rather than nesting a second one), and (3)
// immediately checks whether the WorkItem can already close out right now
// (reconcileWorkItemCancellationTx below — the common case for a WorkItem
// with zero active Runs, or every Run already historically terminal). A
// WorkItem with at least one genuinely still-quiescing Run stays as-is
// (Status unchanged — never itself forced to BLOCKED or any other
// intermediate state) until each of those Runs eventually reaches its own
// terminal state and this SAME reconciliation runs again from
// transitionRunToCancelledTx/transitionRunToFailedTx's own tail call
// (completion.go).
package runtime

import (
	"context"
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

// CancelWorkItemRequest is what a caller supplies to CancelWorkItem.
type CancelWorkItemRequest struct {
	WorkItemID    string
	Actor         string
	Reason        string
	CorrelationID string
}

// CancelWorkItemResult is what CancelWorkItem returns.
type CancelWorkItemResult struct {
	WorkItemID       string
	AlreadyRequested bool
	Status           string
}

// ErrWorkItemAlreadyTerminal is returned when CancelWorkItem targets a
// WorkItem that has already reached a genuinely terminal state (DONE or
// CANCELLED) — the WorkItem-level mirror of CancelRun's own
// ErrRunAlreadyTerminal, and the exact race ADR-020's own cancel-vs-PASS
// table describes at the WorkItem level: "PASS trước → no-op idempotent".
var ErrWorkItemAlreadyTerminal = errors.New("runtime: work item is already terminal, cancel is a no-op")

const (
	WorkItemCancellationRequestedEventType     = "WORK_ITEM_CANCELLATION_REQUESTED"
	WorkItemCancellationRequestedSchemaVersion = 1

	WorkItemCancelledEventType     = "WORK_ITEM_CANCELLED"
	WorkItemCancelledSchemaVersion = 1
)

// workItemCancellationRequestedEventPayload is WORK_ITEM_CANCELLATION_REQUESTED's
// own JSON shape (V4-12C): the durable audit record of who asked for this
// WorkItem to cancel and why, distinct from WORK_ITEM_CANCELLED (appended
// only once every Run has actually finished quiescing).
type workItemCancellationRequestedEventPayload struct {
	WorkItemID string `json:"workItemId"`
	Actor      string `json:"actor"`
	Reason     string `json:"reason"`
}

// workItemCancelledEventPayload is WORK_ITEM_CANCELLED's own JSON shape
// (V4-12C).
type workItemCancelledEventPayload struct {
	WorkItemID string `json:"workItemId"`
	JobID      string `json:"jobId,omitempty"`
}

// CancelWorkItem implements ADR-020's own WorkItem-level cancellation entry
// point. See this file's own package doc comment for the full transaction
// shape.
func CancelWorkItem(ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, req CancelWorkItemRequest) (CancelWorkItemResult, error) {
	workItemID := strings.TrimSpace(req.WorkItemID)
	actor := strings.TrimSpace(req.Actor)
	reason := strings.TrimSpace(req.Reason)
	if workItemID == "" {
		return CancelWorkItemResult{}, errors.New("runtime: WorkItemID is required")
	}
	if actor == "" {
		return CancelWorkItemResult{}, errors.New("runtime: Actor is required")
	}
	if reason == "" {
		return CancelWorkItemResult{}, errors.New("runtime: Reason is required")
	}

	var result CancelWorkItemResult
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		item, err := tx.Work().GetWorkItem(ctx, workItemID)
		if err != nil {
			return err
		}

		if _, err := tx.Runtime().GetWorkItemCancellationIntent(ctx, workItemID); err == nil {
			// A second CancelWorkItem call for the same work item — the
			// identical "idempotent theo run" rule CancelRun's own
			// cancelRunTx already applies, at the WorkItem level: return the
			// already-committed state, never re-quiesce, never re-append the
			// requested event a second time.
			result = CancelWorkItemResult{WorkItemID: workItemID, AlreadyRequested: true, Status: string(item.Status)}
			return nil
		} else if !errors.Is(err, ports.ErrPersistenceNotFound) {
			return err
		}

		if item.Status == workdomain.WorkItemDone || item.Status == workdomain.WorkItemCancelled {
			return fmt.Errorf("%w: work item %s is %s", ErrWorkItemAlreadyTerminal, workItemID, item.Status)
		}

		intentID := ids.NewID()
		intent, err := runtimedomain.NewWorkItemCancellationIntent(
			runtimedomain.WorkItemCancellationIntentID(intentID), item.ProjectID, item.ID, actor, reason, time.Now().UTC(),
		)
		if err != nil {
			return err
		}
		if _, err := tx.Runtime().RecordWorkItemCancellationIntent(ctx, intent); err != nil {
			return err
		}

		eventPayload, err := json.Marshal(workItemCancellationRequestedEventPayload{WorkItemID: workItemID, Actor: actor, Reason: reason})
		if err != nil {
			return fmt.Errorf("marshal %s event payload: %w", WorkItemCancellationRequestedEventType, err)
		}
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID: intentID + "-requested", ProjectID: string(item.ProjectID),
			AggregateType: "WorkItemCancellationIntent", AggregateID: intentID, Sequence: 1,
			EventType: WorkItemCancellationRequestedEventType, SchemaVersion: WorkItemCancellationRequestedSchemaVersion,
			PayloadJSON: string(eventPayload), CorrelationID: req.CorrelationID, CreatedAt: time.Now().UTC(),
		}); err != nil {
			return err
		}

		// Quiesce every currently-non-terminal Run this WorkItem has ever
		// had — cancelRunTx (cancel_run.go) is the exact same protocol
		// CancelRun itself uses, each call idempotent by RunID (a Run a
		// standalone CancelRun already targeted is a harmless no-op here).
		runs, err := tx.Runtime().ListWorkflowRunsForWorkItem(ctx, workItemID)
		if err != nil {
			return err
		}
		for _, run := range runs {
			if isWorkflowRunTerminal(run.State) {
				continue
			}
			if _, err := cancelRunTx(ctx, tx, ids, string(run.ID), actor, reason, req.CorrelationID); err != nil {
				return err
			}
		}

		// The common case for a WorkItem with zero Runs at all, or every Run
		// already historically terminal before this call: close out right
		// now rather than waiting for a Run-closing transaction that will
		// never come.
		if err := reconcileWorkItemCancellationTx(ctx, tx, workItemID, req.CorrelationID, ""); err != nil {
			return err
		}

		final, err := tx.Work().GetWorkItem(ctx, workItemID)
		if err != nil {
			return err
		}
		result = CancelWorkItemResult{WorkItemID: workItemID, Status: string(final.Status)}
		return nil
	})
	return result, err
}

// isWorkflowRunTerminal reports whether state is one of a WorkflowRun's own
// three genuinely terminal states — SUCCEEDED, FAILED, CANCELLED. VERIFYING
// is deliberately excluded (a completion candidate, not yet decided — no
// CompletionPolicy service exists in this codebase to ever resolve it,
// V5-11's own future scope) and so is CANCELLING (still quiescing).
func isWorkflowRunTerminal(state runtimedomain.WorkflowRunState) bool {
	switch state {
	case runtimedomain.WorkflowRunSucceeded, runtimedomain.WorkflowRunFailed, runtimedomain.WorkflowRunCancelled:
		return true
	default:
		return false
	}
}

// reconcileWorkItemCancellationTx is CancelWorkItem's own closing step
// (V4-12C): called from CancelWorkItem's own tail (the zero/already-terminal-
// Run case) and from every place a Run reaches a genuine terminal state
// (transitionRunToCancelledTx, transitionRunToFailedTx — completion.go) —
// the Run-level mirror of reconcileRunTerminalityTx's own "always re-derive
// fresh, safe to call as a no-op" discipline. When the WorkItem has a
// still-REQUESTED WorkItemCancellationIntent AND every one of its own Runs
// (ListWorkflowRunsForWorkItem, unfiltered across the WorkItem's full
// history) has now reached a terminal state, this CASes the WorkItem to
// CANCELLED, marks the intent COMPLETED, and appends WORK_ITEM_CANCELLED —
// a no-op in every other case (no intent at all — by far the common case for
// every Run-closing transaction that runs today; intent exists but at least
// one Run is still non-terminal; WorkItem already CANCELLED — an earlier
// winner already closed it out).
func reconcileWorkItemCancellationTx(ctx context.Context, tx ports.Tx, workItemID, correlationID, jobID string) error {
	intent, err := tx.Runtime().GetWorkItemCancellationIntent(ctx, workItemID)
	if errors.Is(err, ports.ErrPersistenceNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if intent.State != runtimedomain.CancellationIntentRequested {
		return nil
	}

	runs, err := tx.Runtime().ListWorkflowRunsForWorkItem(ctx, workItemID)
	if err != nil {
		return err
	}
	for _, run := range runs {
		if !isWorkflowRunTerminal(run.State) {
			return nil
		}
	}

	item, err := tx.Work().GetWorkItem(ctx, workItemID)
	if err != nil {
		return err
	}
	if item.Status == workdomain.WorkItemCancelled {
		return nil
	}

	updated, err := tx.Work().TransitionWorkItemStatus(ctx, ports.TransitionWorkItemStatusRequest{
		WorkItemID: workItemID, ExpectedStatus: item.Status, ExpectedVersion: item.Version,
		NextStatus: workdomain.WorkItemCancelled,
	})
	if err != nil {
		return err
	}
	if _, err := tx.Runtime().TransitionWorkItemCancellationIntentState(ctx, ports.TransitionWorkItemCancellationIntentStateRequest{
		WorkItemID: workItemID, ExpectedState: runtimedomain.CancellationIntentRequested,
		NextState: runtimedomain.CancellationIntentCompleted,
	}); err != nil {
		return err
	}

	payload, err := json.Marshal(workItemCancelledEventPayload{WorkItemID: workItemID, JobID: jobID})
	if err != nil {
		return fmt.Errorf("marshal %s event payload: %w", WorkItemCancelledEventType, err)
	}
	return tx.Events().Append(ctx, ports.DomainEvent{
		ID: workItemID + "-cancelled", ProjectID: string(updated.ProjectID),
		AggregateType: "WorkItem", AggregateID: workItemID, Sequence: int64(updated.Version),
		EventType: WorkItemCancelledEventType, SchemaVersion: WorkItemCancelledSchemaVersion, PayloadJSON: string(payload),
		CorrelationID: correlationID, CreatedAt: time.Now().UTC(),
	})
}
