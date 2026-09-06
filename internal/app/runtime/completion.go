// V4-12's own core (docs/design/06-v4-runtime-engine.md): END finally gets
// a real dispatch owner (advance.go's own isEndNode state assignment — no
// switch case is needed beyond that, since reconcileRunTerminalityTx below
// does all the real work generically), and — confirmed with the user
// before writing this file, correcting an initial narrower reading of the
// task text — a genuine Run-level failure-aggregation reducer that reacts
// to a NodeRun or JOIN failing terminally ANYWHERE in the graph, not just
// END's own dispatch. V4-06's own decideRetryOrExhaustion and V4-11's own
// evaluateJoinTx both already deliberately stop short of touching
// WorkflowRun.State on failure ("never an automatic WorkflowRun failure —
// V4-12's own scope"); this file is where that deferred aggregation
// actually happens.
//
// reconcileRunTerminalityTx is the single, canonical reducer every one of
// those call sites shares (advanceRunTx's own two exit points, and
// finalize.go's own decideRetryOrExhaustion) — always re-deriving the
// Run's own overall terminality fresh from durable NodeRun state rather
// than trusting a caller-supplied hint, so it is correct no matter which
// call site invoked it and safe to call even when nothing changed (the
// common case: a freshly created downstream NodeRun already makes it a
// no-op). It must always run AFTER any downstream activation/job this
// same transaction already created, never before — calling it mid-hop
// would observe a transient "no live NodeRun" state that is about to be
// falsified by the very activation this transaction is still in the
// middle of creating.
package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// RunNodeStateSummary classifies every NodeRun for one WorkflowRun into the
// three groups reconcileRunTerminalityTx's own priority order needs (V4-12,
// confirmed with the user before writing this file):
//   - Live/progress-capable: PENDING, READY, QUEUED, RUNNING, WAITING —
//     something can still advance this Run.
//   - Recoverable-stalled: BLOCKED — not live, but never a reason to fail
//     the Run either; RetryBlockedActivation/scope-amendment/cancel
//     protocol (V4-12A/V4-12B, neither built yet) owns it exclusively.
//   - Terminal: SUCCEEDED, FAILED, SKIPPED, CANCELLED — SKIPPED is
//     deliberately given no signal of its own here, since V4-07's own
//     cycle-exhaustion always creates a live escalation-target NodeRun in
//     the SAME transaction, so a SKIPPED row never coexists with
//     LiveCount==0 at the moment this summary is computed. CANCELLED
//     (V4-12B's own first real producer: the CANCEL_RUN_COORDINATOR job's
//     own sweep) is identically given no signal of its own — a Run in
//     RUNNING/WAITING never has a CANCELLED NodeRun to begin with (nothing
//     produces one outside the cancellation protocol, which only ever
//     runs once the Run itself has already left RUNNING/WAITING for
//     CANCELLING), so this only ever matters for reconcileCancellingRunTx's
//     own "zero live" check below, where a CANCELLED NodeRun correctly
//     counts toward neither LiveCount nor BlockedCount.
type RunNodeStateSummary struct {
	LiveCount    int
	BlockedCount int
	FailedCount  int
	// ReachedEndNodeRunID/ReachedEndNodeKey name the first SUCCEEDED
	// NodeRun found whose own node type is END — "" when no END has been
	// reached yet. At most one is ever expected to exist for a given Run
	// (reaching one already CASes the Run out of RUNNING/WAITING, so a
	// second could never itself be created — see advanceRunTx's own
	// idempotent-replay guard), but this only records the first found
	// rather than asserting that defensively.
	ReachedEndNodeRunID string
	ReachedEndNodeKey   string
}

// computeRunNodeStateSummary loads every NodeRun for runID and classifies
// it. document is needed only to recognize an END-type node among the
// SUCCEEDED ones — ListNodeRunsForRun itself is document-agnostic.
func computeRunNodeStateSummary(ctx context.Context, tx ports.Tx, runID string, document workflow.WorkflowDocument) (RunNodeStateSummary, error) {
	nodeRuns, err := tx.Runtime().ListNodeRunsForRun(ctx, runID)
	if err != nil {
		return RunNodeStateSummary{}, err
	}
	var summary RunNodeStateSummary
	for _, nodeRun := range nodeRuns {
		switch nodeRun.State {
		case runtimedomain.NodeRunPending, runtimedomain.NodeRunReady, runtimedomain.NodeRunQueued,
			runtimedomain.NodeRunRunning, runtimedomain.NodeRunWaiting:
			summary.LiveCount++
		case runtimedomain.NodeRunBlocked:
			summary.BlockedCount++
		case runtimedomain.NodeRunFailed:
			summary.FailedCount++
		case runtimedomain.NodeRunSucceeded:
			if summary.ReachedEndNodeRunID == "" {
				if node, ok := findNode(document, nodeRun.NodeKey); ok && node.Type == workflow.NodeEnd {
					summary.ReachedEndNodeRunID = string(nodeRun.ID)
					summary.ReachedEndNodeKey = nodeRun.NodeKey
				}
			}
		}
	}
	return summary, nil
}

// RunFailureReasonRunFailed/RunFailureReasonTerminalPathInvalid are
// RUN_FAILED's own two Reason values (V4-12, confirmed with the user
// before writing this file). RunFailureReasonRunFailed is the ordinary
// case: at least one NodeRun (an ordinary node, or a JOIN — evaluateJoinTx
// CASes a failed JOIN's own NodeRun to the exact same NodeRunFailed state,
// so no separate signal is needed to distinguish the two) failed
// terminally and nothing else can carry the Run forward.
// RunFailureReasonTerminalPathInvalid is the defensive, should-be-
// unreachable fail-closed branch: no live/blocked NodeRun, no END
// reached, and no FAILED NodeRun either — every path this codebase's own
// invariants intend to be exhaustive left nothing to explain a Run stuck
// with zero progress-capable activity, most plausibly forward-compat with
// a future producer of NodeRunCancelled (V4-12B) this task does not yet
// classify as an explicit failure signal.
const (
	RunFailureReasonRunFailed           = "RUN_FAILED"
	RunFailureReasonTerminalPathInvalid = "TERMINAL_PATH_INVALID"
)

// reconcileRunTerminalityTx implements V4-12's own locked priority order
// for a Run still RUNNING/WAITING, exactly:
//  1. Run is not currently RUNNING, WAITING, or CANCELLING (already
//     VERIFYING/FAILED/SUCCEEDED/CANCELLED/CREATED) — no-op; never fights
//     another authority (a future CompletionPolicy transition).
//     1b. Run is CANCELLING — delegates to reconcileCancellingRunTx below
//     (V4-12B's own extension), a DIFFERENT, simpler rule than 2-6: once
//     cancellation intent has committed, nothing about END/FAILED/
//     TERMINAL_PATH_INVALID matters anymore, only whether anything live
//     or BLOCKED remains.
//  2. Any live NodeRun exists — no-op; the Run can still make progress.
//  3. No live, but a BLOCKED NodeRun exists — no-op; recoverable-stalled,
//     owned by a future blocker/retry/cancel authority, never a Run
//     failure.
//  4. A valid END has been reached — CAS Run to VERIFYING (ADR-011's own
//     "completion candidate", never SUCCEEDED directly) and append
//     RUN_COMPLETION_REQUESTED.
//  5. No live/BLOCKED/END, but a FAILED NodeRun exists — CAS Run to
//     FAILED and append RUN_FAILED with Reason=RunFailureReasonRunFailed.
//  6. None of the above — CAS Run to FAILED and append RUN_FAILED with
//     Reason=RunFailureReasonTerminalPathInvalid (defensive fail-closed
//     branch).
//
// Never touches WorkItem.Status — that stays the exclusive authority of a
// future verification service (V5-11's own CompletionPolicy), exactly as
// this task's own text requires ("WorkItem chỉ giữ ACTIVE/BLOCKED cho tới
// verification service V5").
func reconcileRunTerminalityTx(
	ctx context.Context, tx ports.Tx, run runtimedomain.WorkflowRun, document workflow.WorkflowDocument, correlationID, jobID string,
) error {
	current, err := tx.Runtime().GetWorkflowRun(ctx, string(run.ID))
	if err != nil {
		return err
	}
	if current.State == runtimedomain.WorkflowRunCancelling {
		return reconcileCancellingRunTx(ctx, tx, current, document, correlationID, jobID)
	}
	if current.State != runtimedomain.WorkflowRunRunning && current.State != runtimedomain.WorkflowRunWaiting {
		return nil
	}

	summary, err := computeRunNodeStateSummary(ctx, tx, string(current.ID), document)
	if err != nil {
		return err
	}

	switch {
	case summary.LiveCount > 0:
		return nil
	case summary.BlockedCount > 0:
		return nil
	case summary.ReachedEndNodeRunID != "":
		return transitionRunToVerifyingTx(ctx, tx, current, summary, correlationID, jobID)
	case summary.FailedCount > 0:
		return transitionRunToFailedTx(ctx, tx, current, RunFailureReasonRunFailed, correlationID, jobID)
	default:
		return transitionRunToFailedTx(ctx, tx, current, RunFailureReasonTerminalPathInvalid, correlationID, jobID)
	}
}

// reconcileCancellingRunTx is V4-12B's own extension to the shared
// reducer (docs/design/06-v4-runtime-engine.md, ADR-020): once a Run has
// committed its own cancellation intent (CANCELLING), the only question
// left is whether anything still live or BLOCKED remains — no END/FAILED/
// TERMINAL_PATH_INVALID distinction matters anymore once cancellation has
// already been decided. Requires BOTH LiveCount==0 AND BlockedCount==0: a
// lingering BLOCKED NodeRun (most plausibly one that went BLOCKED for
// scope expansion in the brief window between the cancel intent
// committing and that Attempt's own outcome later arriving) is never
// silently auto-resolved here — it stays exactly as CANCELLING until an
// operator or a future CancelWorkItem/ResolveWorkItemBlocker-style
// mechanism (V4-12C) closes it out, the same "BLOCKED không được tự fail
// Run" philosophy V4-12 already locked, applied here to "BLOCKED không
// được tự cancel Run" too. This is also the ONLY place
// WorkflowRunCancelled is ever produced — called both from the
// CANCEL_RUN_COORDINATOR job's own tail (right after its own sweep) and
// from every ordinary reconcileRunTerminalityTx call site whenever it
// observes the Run already CANCELLING (most importantly a RUNNING
// Attempt's own eventual FinalizeExecutionAttempt, arriving after cancel
// intent already committed). internal/adapters/sqlite's own Store opens
// every write transaction with _txlock=immediate (txrunner.go's own doc
// comment), so two WithSerializedWrite calls fully serialize against each
// other — whichever of these two call sites' own transaction commits
// first is unconditionally the only one that ever observes "CANCELLING
// and now quiesced" at all; every later transaction's own fresh
// GetWorkflowRun read (this function's own caller, reconcileRunTerminalityTx)
// already sees CANCELLED and takes the plain no-op branch instead. There
// is no genuine CAS-conflict race to swallow here the way, say, two
// concurrent ApproveScopeExpansion calls might need.
func reconcileCancellingRunTx(
	ctx context.Context, tx ports.Tx, run runtimedomain.WorkflowRun, document workflow.WorkflowDocument, correlationID, jobID string,
) error {
	summary, err := computeRunNodeStateSummary(ctx, tx, string(run.ID), document)
	if err != nil {
		return err
	}
	if summary.LiveCount > 0 || summary.BlockedCount > 0 {
		return nil
	}
	return transitionRunToCancelledTx(ctx, tx, run, correlationID, jobID)
}

// RunCompletionRequestedEventType/RunCompletionRequestedSchemaVersion
// identify RUN_COMPLETION_REQUESTED's own registered (EventType,
// SchemaVersion) pair (V4-12, event_schema.go) — registered from the same
// changeset that produces this event, not deferred.
const (
	RunCompletionRequestedEventType     = "RUN_COMPLETION_REQUESTED"
	RunCompletionRequestedSchemaVersion = 1
)

// runCompletionRequestedEventPayload is RUN_COMPLETION_REQUESTED's own
// JSON shape (V4-12): ADR-011's own completion-candidate signal, naming
// exactly which END NodeRun triggered it.
type runCompletionRequestedEventPayload struct {
	RunID        string `json:"runId"`
	WorkItemID   string `json:"workItemId"`
	EndNodeRunID string `json:"endNodeRunId"`
	EndNodeKey   string `json:"endNodeKey"`
	// JobID is the durable job that drove this hop (blank if the caller
	// did not supply one via AdvanceRunRequest.JobID).
	JobID string `json:"jobId,omitempty"`
}

func transitionRunToVerifyingTx(
	ctx context.Context, tx ports.Tx, run runtimedomain.WorkflowRun, summary RunNodeStateSummary, correlationID, jobID string,
) error {
	updated, err := tx.Runtime().TransitionWorkflowRunState(ctx, ports.TransitionWorkflowRunStateRequest{
		RunID: string(run.ID), ExpectedState: run.State, ExpectedVersion: run.Version,
		NextState: runtimedomain.WorkflowRunVerifying,
	})
	if err != nil {
		return err
	}
	payload, err := json.Marshal(runCompletionRequestedEventPayload{
		RunID: string(run.ID), WorkItemID: string(run.WorkItemID),
		EndNodeRunID: summary.ReachedEndNodeRunID, EndNodeKey: summary.ReachedEndNodeKey, JobID: jobID,
	})
	if err != nil {
		return fmt.Errorf("marshal %s event payload: %w", RunCompletionRequestedEventType, err)
	}
	return tx.Events().Append(ctx, ports.DomainEvent{
		ID: string(run.ID) + "-completion-requested", ProjectID: string(run.ProjectID),
		AggregateType: "WorkflowRun", AggregateID: string(run.ID), Sequence: int64(updated.Version),
		EventType: RunCompletionRequestedEventType, SchemaVersion: RunCompletionRequestedSchemaVersion, PayloadJSON: string(payload),
		CorrelationID: correlationID, CreatedAt: time.Now().UTC(),
	})
}

// RunFailedEventType/RunFailedSchemaVersion identify RUN_FAILED's own
// registered (EventType, SchemaVersion) pair (V4-12, event_schema.go) —
// registered from the same changeset that produces this event, not
// deferred.
const (
	RunFailedEventType     = "RUN_FAILED"
	RunFailedSchemaVersion = 1
)

// runFailedEventPayload is RUN_FAILED's own JSON shape (V4-12).
type runFailedEventPayload struct {
	RunID      string `json:"runId"`
	WorkItemID string `json:"workItemId"`
	Reason     string `json:"reason"`
	// JobID is the durable job that drove this hop (blank if the caller
	// did not supply one via AdvanceRunRequest.JobID).
	JobID string `json:"jobId,omitempty"`
}

func transitionRunToFailedTx(
	ctx context.Context, tx ports.Tx, run runtimedomain.WorkflowRun, reason, correlationID, jobID string,
) error {
	updated, err := tx.Runtime().TransitionWorkflowRunState(ctx, ports.TransitionWorkflowRunStateRequest{
		RunID: string(run.ID), ExpectedState: run.State, ExpectedVersion: run.Version,
		NextState: runtimedomain.WorkflowRunFailed,
	})
	if err != nil {
		return err
	}
	payload, err := json.Marshal(runFailedEventPayload{
		RunID: string(run.ID), WorkItemID: string(run.WorkItemID), Reason: reason, JobID: jobID,
	})
	if err != nil {
		return fmt.Errorf("marshal %s event payload: %w", RunFailedEventType, err)
	}
	return tx.Events().Append(ctx, ports.DomainEvent{
		ID: string(run.ID) + "-failed", ProjectID: string(run.ProjectID),
		AggregateType: "WorkflowRun", AggregateID: string(run.ID), Sequence: int64(updated.Version),
		EventType: RunFailedEventType, SchemaVersion: RunFailedSchemaVersion, PayloadJSON: string(payload),
		CorrelationID: correlationID, CreatedAt: time.Now().UTC(),
	})
}

// RunCancelledEventType/RunCancelledSchemaVersion identify
// RUN_CANCELLED's own registered (EventType, SchemaVersion) pair (V4-12B,
// event_schema.go) — registered from the same changeset that produces
// this event, not deferred.
const (
	RunCancelledEventType     = "RUN_CANCELLED"
	RunCancelledSchemaVersion = 1
)

// runCancelledEventPayload is RUN_CANCELLED's own JSON shape (V4-12B).
type runCancelledEventPayload struct {
	RunID      string `json:"runId"`
	WorkItemID string `json:"workItemId"`
	// JobID is the durable job that drove this hop (blank if the caller
	// did not supply one).
	JobID string `json:"jobId,omitempty"`
}

// transitionRunToCancelledTx is reconcileCancellingRunTx's own closing
// step (V4-12B): CAS CANCELLING->CANCELLED and append RUN_CANCELLED — the
// only place either ever happens in this codebase.
func transitionRunToCancelledTx(
	ctx context.Context, tx ports.Tx, run runtimedomain.WorkflowRun, correlationID, jobID string,
) error {
	updated, err := tx.Runtime().TransitionWorkflowRunState(ctx, ports.TransitionWorkflowRunStateRequest{
		RunID: string(run.ID), ExpectedState: run.State, ExpectedVersion: run.Version,
		NextState: runtimedomain.WorkflowRunCancelled,
	})
	if err != nil {
		return err
	}
	payload, err := json.Marshal(runCancelledEventPayload{
		RunID: string(run.ID), WorkItemID: string(run.WorkItemID), JobID: jobID,
	})
	if err != nil {
		return fmt.Errorf("marshal %s event payload: %w", RunCancelledEventType, err)
	}
	return tx.Events().Append(ctx, ports.DomainEvent{
		ID: string(run.ID) + "-cancelled", ProjectID: string(run.ProjectID),
		AggregateType: "WorkflowRun", AggregateID: string(run.ID), Sequence: int64(updated.Version),
		EventType: RunCancelledEventType, SchemaVersion: RunCancelledSchemaVersion, PayloadJSON: string(payload),
		CorrelationID: correlationID, CreatedAt: time.Now().UTC(),
	})
}
