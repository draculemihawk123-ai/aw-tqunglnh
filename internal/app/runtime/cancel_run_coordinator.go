// CancelRunCoordinatorHandler is CancelRun's own CONTROL job handler
// (V4-12B, docs/design/06-v4-runtime-engine.md, ADR-020): the asynchronous
// half of the cancellation protocol, doing everything CancelRun's own
// transaction (cancel_run.go) deliberately leaves for later — CASing
// every still-ACTIVE WAIT registration, still-PENDING APPROVAL request,
// still-QUEUED ExecutionAttempt, and every other not-yet-started NodeRun
// of one Run to CANCELLED, in a single sweep, all in one transaction.
//
// BLOCKED is deliberately NEVER touched here (confirmed with the user
// before writing this file, by the same "BLOCKED không được tự fail Run"
// philosophy V4-12 already locked, extended here to "không được tự
// cancel"): a NodeRun/ExecutionAttempt sitting BLOCKED for scope expansion
// is left exactly as-is. This is what makes reconcileCancellingRunTx's own
// BlockedCount>0 no-op guard (completion.go) a real, load-bearing check
// rather than dead code — a Run with a lingering BLOCKED NodeRun stays at
// CANCELLING until an operator or a future CancelWorkItem/
// ResolveWorkItemBlocker-style mechanism (V4-12C) closes it out.
//
// A RUNNING NodeRun is swept too, but ONLY when it has no real
// ExecutionAttempt of its own: a STRUCTURAL auto-advance node (START/
// ROUTER/FORK/END) is assigned RUNNING the instant it is created and
// never gets an Attempt at all — its only path forward is an ADVANCE_RUN
// job CancelRun's own FenceAndCancelRunJobs already fenced, so leaving it
// alone (the same way a real, executing Attempt is left alone) would
// strand it RUNNING forever with nothing left to ever route it. An
// executable node's own RUNNING NodeRun (a real ExecutionAttempt in
// flight) is left untouched, exactly like BLOCKED — Alpha has no
// process-kill; it runs to its own natural conclusion.
package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// CancelRunCoordinatorHandler is a ready-to-register workerpool.Handler
// for CancelRunCoordinatorJobKind.
type CancelRunCoordinatorHandler struct {
	uow ports.UnitOfWork
	ids idsource.Source
}

// NewCancelRunCoordinatorHandler returns a ready-to-register
// CancelRunCoordinatorHandler.
func NewCancelRunCoordinatorHandler(uow ports.UnitOfWork, ids idsource.Source) *CancelRunCoordinatorHandler {
	return &CancelRunCoordinatorHandler{uow: uow, ids: ids}
}

var _ workerpool.Handler = (*CancelRunCoordinatorHandler)(nil)

// Handle implements workerpool.Handler for CancelRunCoordinatorJobKind.
// See this file's own package doc comment for the full sweep.
func (h *CancelRunCoordinatorHandler) Handle(ctx context.Context, job ports.DurableJob) error {
	var payload CancelRunCoordinatorJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("runtime: unmarshal %s job %s payload: %w", CancelRunCoordinatorJobKind, job.ID, err)
	}
	if payload.RunID == "" {
		return fmt.Errorf("runtime: %s job %s payload missing runId", CancelRunCoordinatorJobKind, job.ID)
	}

	return h.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		run, err := tx.Runtime().GetWorkflowRun(ctx, payload.RunID)
		if err != nil {
			return err
		}
		if run.State != runtimedomain.WorkflowRunCancelling {
			// Already resolved (a RUNNING Attempt's own later finalize
			// closed it out first, or a duplicate delivery of this same
			// job arrived after an earlier claim already finished the
			// sweep) — idempotent no-op. A redelivered
			// CANCEL_RUN_COORDINATOR job (worker crash, at-least-once
			// delivery, MaxClaims>1) must never re-sweep or re-fire
			// events.
			return nil
		}

		registrations, err := tx.Wait().ListWaitRegistrationsForRun(ctx, payload.RunID)
		if err != nil {
			return err
		}
		for _, registration := range registrations {
			if registration.State != runtimedomain.WaitRegistrationActive {
				continue
			}
			if _, err := tx.Wait().TransitionWaitRegistration(ctx, ports.TransitionWaitRegistrationRequest{
				WaitRegistrationID: string(registration.ID), ExpectedState: runtimedomain.WaitRegistrationActive,
				ExpectedVersion: registration.Version, NextState: runtimedomain.WaitRegistrationCancelled,
			}); err != nil {
				return err
			}
		}

		approvals, err := tx.Approvals().ListApprovalRequestsForRun(ctx, payload.RunID)
		if err != nil {
			return err
		}
		for _, approval := range approvals {
			if approval.State != runtimedomain.ApprovalRequestPending {
				continue
			}
			if _, err := tx.Approvals().TransitionApprovalRequest(ctx, ports.TransitionApprovalRequestRequest{
				ApprovalRequestID: string(approval.ID), ExpectedState: runtimedomain.ApprovalRequestPending,
				ExpectedVersion: approval.Version, NextState: runtimedomain.ApprovalRequestCancelled,
			}); err != nil {
				return err
			}
		}

		// ADR-020's own "Attempt đã tạo cùng job nhưng chưa khởi động
		// chuyển QUEUED -> CANCELLED": every QUEUED (created, never
		// started) ExecutionAttempt of this Run — never RUNNING (Alpha
		// has no real process-kill; a RUNNING Attempt runs to its own
		// natural conclusion) and never BLOCKED (see this file's own
		// package doc comment).
		attempts, err := tx.Runtime().ListExecutionAttemptsForRun(ctx, payload.RunID)
		if err != nil {
			return err
		}
		// nodeRunHasAttempt distinguishes the two ways a NodeRun can be
		// RUNNING: an executable (e.g. AGENT) node's own NodeRun is only
		// ever RUNNING in lockstep with a real ExecutionAttempt
		// (ExecuteNodeHandler's own claimRunning moves both together) —
		// genuine work Alpha cannot force-stop. A STRUCTURAL auto-advance
		// node (START/ROUTER/FORK/END) is assigned RUNNING the instant it
		// is created and NEVER gets an ExecutionAttempt of its own at
		// all — its only path forward is an ADVANCE_RUN job that
		// CancelRun's own FenceAndCancelRunJobs already fenced, so
		// without this distinction it would sit RUNNING forever with
		// nothing left to ever route it. The NodeRun sweep below treats
		// a RUNNING NodeRun with no Attempt exactly like PENDING/READY/
		// QUEUED/WAITING.
		nodeRunHasAttempt := make(map[string]bool, len(attempts))
		for _, attempt := range attempts {
			nodeRunHasAttempt[string(attempt.NodeRunID)] = true
			if attempt.State != runtimedomain.ExecutionAttemptQueued {
				continue
			}
			if _, err := tx.Runtime().TransitionExecutionAttempt(ctx, ports.TransitionExecutionAttemptRequest{
				AttemptID: string(attempt.ID), ExpectedState: runtimedomain.ExecutionAttemptQueued, ExpectedVersion: attempt.Version,
				NextState: runtimedomain.ExecutionAttemptCancelled, TerminationReason: runtimedomain.TerminationReasonRunCancelledBeforeStart,
			}); err != nil {
				return err
			}
		}

		version, err := tx.Definitions().GetWorkflowVersion(ctx, string(run.WorkflowVersionID))
		if err != nil {
			return err
		}
		document := version.Document()

		// Every not-yet-started NodeRun of this Run: PENDING/READY/QUEUED
		// (never dispatched, or an Attempt created but not started — the
		// exact rows the two loops above already updated the DOMAIN
		// object for; this NodeRun-level sweep is what ADR-020 itself
		// calls "NodeRun chưa chạy chuyển CANCELLED") and WAITING (a WAIT/
		// APPROVAL node's own NodeRun — cancelled here, never inside the
		// two loops above, since neither WaitRegistration nor
		// ApprovalRequest owns its own NodeRun's state; also a JOIN's own
		// NodeRun sitting WAITING on sibling branches, which has no
		// WaitRegistration/ApprovalRequest of its own at all). BLOCKED is
		// deliberately excluded — see this file's own package doc
		// comment. Each cancelled NodeRun's own BranchToken (if it
		// belonged to a FORK branch) is terminalized and the JOIN's own
		// policy re-evaluated in the same pass — the identical
		// terminalizeBranchTokenForCancelledNodeRunTx helper
		// decideCancelledOutcomeTx (finalize.go) already uses for the
		// RUNNING-Attempt-cancelled case.
		nodeRunIDs, err := tx.Runtime().ListNodeRunsForRun(ctx, payload.RunID)
		if err != nil {
			return err
		}
		for _, listed := range nodeRunIDs {
			// Re-fetch fresh rather than trusting this listing's own
			// snapshot: terminalizeBranchTokenForCancelledNodeRunTx's own
			// evaluateJoinTx call, triggered by an EARLIER iteration of
			// THIS SAME loop cancelling a sibling branch, can already have
			// changed a LATER entry's own state/version within this same
			// transaction (e.g. an ALL-mode JOIN's own NodeRun going
			// WAITING->FAILED the moment the first branch in iteration
			// order is cancelled, well before this loop's own turn to
			// process that JOIN's own listed entry) — a stale ExpectedVersion
			// here would surface as a spurious ErrOptimisticConflict that
			// aborts the entire sweep.
			nodeRun, err := tx.Runtime().GetNodeRun(ctx, string(listed.ID))
			if err != nil {
				return err
			}
			cancelEligible := false
			switch nodeRun.State {
			case runtimedomain.NodeRunPending, runtimedomain.NodeRunReady,
				runtimedomain.NodeRunQueued, runtimedomain.NodeRunWaiting:
				cancelEligible = true
			case runtimedomain.NodeRunRunning:
				// See nodeRunHasAttempt's own doc comment above: only a
				// structural node with no real ExecutionAttempt of its
				// own is eligible here.
				cancelEligible = !nodeRunHasAttempt[string(nodeRun.ID)]
			}
			if cancelEligible {
				if _, err := tx.Runtime().TransitionNodeRun(ctx, ports.TransitionNodeRunRequest{
					NodeRunID: string(nodeRun.ID), ExpectedState: nodeRun.State, ExpectedVersion: nodeRun.Version,
					NextState: runtimedomain.NodeRunCancelled,
				}); err != nil {
					return err
				}
				if err := terminalizeBranchTokenForCancelledNodeRunTx(ctx, tx, h.ids, run, document, nodeRun, payload.CorrelationID, string(job.ID)); err != nil {
					return err
				}
			}
		}

		// Quiesce sweep is done — mark the intent COMPLETED regardless of
		// whether the Run itself reaches CANCELLED right now (a RUNNING
		// Attempt may still be outstanding; reconcileCancellingRunTx's own
		// tail call below — or a later one, from that Attempt's own
		// eventual finalize — is what actually closes the Run out).
		if _, err := tx.Runtime().TransitionRunCancellationIntentState(ctx, ports.TransitionRunCancellationIntentStateRequest{
			RunID: payload.RunID, ExpectedState: runtimedomain.CancellationIntentRequested,
			NextState: runtimedomain.CancellationIntentCompleted,
		}); err != nil {
			return err
		}

		return reconcileRunTerminalityTx(ctx, tx, run, document, payload.CorrelationID, string(job.ID))
	})
}
