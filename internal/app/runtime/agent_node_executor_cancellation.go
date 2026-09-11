// This file holds AgentNodeExecutor's own V5-08C cancellation-disposition
// logic — kept out of agent_node_executor.go itself for the same reason
// agent_node_executor_resources.go already is: keeping Execute/classify's
// own control flow readable. See classify's own AgentExecutionCancelled
// case (agent_node_executor.go) for where this is reached.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/worker"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// ErrAttemptAlreadyTerminated signals execute.go's own Handle that THIS
// call already moved the Attempt to a terminal state itself (V5-08C's own
// mutating-cancellation path, finalizeMutatingCancellation below) —
// unlike ErrIndeterminateExecution (which means "leave RUNNING, someone
// else resolves this later"), this means "already resolved, right now; do
// not call FinalizeExecutionAttempt at all" (the Attempt is no longer
// RUNNING by the time this returns, so that CAS would just fail).
var ErrAttemptAlreadyTerminated = errors.New("runtime: attempt was terminated directly by the cancellation path, not finalized")

// cancellationPollInterval is how often execute.go's own poller re-checks
// durable cancellation intent while an executor is running (execute.go's
// own Handle) — independent of this file, but documented here since
// classifyCancellation's own re-derivation below is this same poller's
// receiving end.
const cancellationPollInterval = 500 * time.Millisecond

// classifyCancellation is V5-08C's own locked decision: real cancellation
// reaches a running provider process entirely through ctx propagation
// already built into ports.ProcessSupervisor.Run (see this file's own
// package doc comment) — nothing new was needed there. What IS new is
// deciding what a confirmed-stopped process means for the Attempt: this
// bridge independently re-derives whether the ctx cancellation this
// Execute call just observed corresponds to a genuine, durable
// RunCancellationIntent (CancelRun, cancel_run.go) rather than some other
// reason ctx could have been cancelled (e.g. workerpool.Pool's own
// shutdown-grace escalation, cancelJobs) — trusting durable state, never a
// live signal from whatever caller happened to cancel this ctx.
//
// A free function (V5-09: CommandNodeExecutor, command_node_executor.go,
// needs this EXACT same cancellation disposition — the design doc's own
// locked requirement for COMMAND is "dùng đúng đường V5-08C, không có
// đường terminate riêng", i.e. reuse this, never a second implementation)
// rather than a method on *AgentNodeExecutor; e.classifyCancellation below
// is a thin, unchanged wrapper kept so agent_node_executor.go's own single
// call site needs no edit.
func classifyCancellation(
	ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, workspaces ports.WorkspaceProvider,
	writeLeases ports.WriteLeaseManager, req ports.NodeExecutionRequest, resolved resolvedExecutionResources,
) (ports.NodeExecutionResult, error) {
	// By the time this is ever called, ctx (execCtx) has ALREADY been
	// cancelled — that is precisely what "a cancellation was observed"
	// means here (pollForCancellation's own doit signal, execute.go). This
	// re-check read, like finalizeMutatingCancellation's own real I/O and
	// durable writes below, must use an uncancelable derivative
	// (context.WithoutCancel, the same real pattern pool.go's own
	// heartbeatLoop already uses) — running it against the already-done
	// ctx directly risks the read failing/hanging for the wrong reason
	// (ctx cancellation) rather than ever really answering the question,
	// silently routing a genuine cancellation into the generic
	// "leave RUNNING" branch (execute.go) instead of ever reaching
	// finalizeMutatingCancellation at all — confirmed empirically (not
	// guessed) while building this fix.
	cleanupCtx := context.WithoutCancel(ctx)
	var runCancelling bool
	if err := uow.WithReadOnly(cleanupCtx, func(tx ports.Tx) error {
		run, err := tx.Runtime().GetWorkflowRun(cleanupCtx, req.RunID)
		if err != nil {
			return err
		}
		runCancelling = run.State == runtimedomain.WorkflowRunCancelling || run.State == runtimedomain.WorkflowRunCancelled
		return nil
	}); err != nil {
		return ports.NodeExecutionResult{}, fmt.Errorf("runtime: re-check run cancellation state: %w", err)
	}
	if !runCancelling {
		// The process stopped in response to ctx being cancelled, but this
		// Run has no durable cancellation intent — an ambiguous cause this
		// bridge was never told the real reason for (pool shutdown is the
		// only one that reaches here today). Never guess CANCELLED for an
		// ambiguous cause: leave RUNNING for crash recovery, the same safe
		// default every other ambiguous case in this file already uses.
		return ports.NodeExecutionResult{}, fmt.Errorf("%w: process stopped but this run has no durable cancellation intent", ErrIndeterminateExecution)
	}
	if !resolved.hasWriteMount {
		// Read-only attempt: no side effect was ever possible, so a
		// confirmed-stopped process under a genuine cancellation intent is
		// unambiguously CANCELLED — routes through the normal
		// FinalizeExecutionAttempt -> decideCancelledOutcomeTx path
		// (finalize.go), exactly like admission-time cancellation already
		// does.
		return ports.NodeExecutionResult{
			State: runtimedomain.ExecutionAttemptCancelled, TerminationReason: runtimedomain.TerminationReasonRunCancelled,
		}, nil
	}
	return ports.NodeExecutionResult{}, finalizeMutatingCancellation(ctx, uow, ids, workspaces, writeLeases, req, resolved)
}

func (e *AgentNodeExecutor) classifyCancellation(
	ctx context.Context, req ports.NodeExecutionRequest, resolved resolvedExecutionResources,
) (ports.NodeExecutionResult, error) {
	return classifyCancellation(ctx, e.uow, e.ids, e.workspaces, e.writeLeases, req, resolved)
}

// executionAttemptTerminatedEventPayload is EXECUTION_ATTEMPT_TERMINATED's
// own JSON shape — mirrors internal/adapters/sqlite/attempt_store.go's own
// TerminateInterruptedAttempt (the SPK-04-era primitive this function
// replaces for the CANCELLING-Run case, V5-15D): finalizeMutatingCancellation
// below CASes the Attempt via the already-tx-scoped
// ports.RuntimeRepository.TransitionExecutionAttempt instead (so it can
// share ONE atomic transaction with the NodeRun/Run-terminality steps),
// which appends no event of its own — this is that same audit event,
// appended manually here to keep the identical real trace either path
// produces.
type executionAttemptTerminatedEventPayload struct {
	AttemptID string `json:"attemptId"`
	NextState string `json:"nextState"`
	Reason    string `json:"reason"`
}

const (
	executionAttemptTerminatedEventType     = "EXECUTION_ATTEMPT_TERMINATED"
	executionAttemptTerminatedSchemaVersion = 1
)

// finalizeMutatingCancellation is V5-15D's own real production fix
// (2026-09-12, confirmed with the user before writing this — a genuine,
// previously-unexercised architectural gap found only by actually running
// the system, not by reading the code alone): the ORIGINAL V5-08C
// handleMutatingCancellation only ever CASed the Attempt to INDETERMINATE
// and quarantined the workspace (both via separate, non-atomic *sqlite.Store
// calls), then returned ErrAttemptAlreadyTerminated — which execute.go's own
// Handle treats as "already resolved, skip FinalizeExecutionAttempt
// entirely." That skip meant the owning NodeRun was NEVER transitioned away
// from RUNNING, so reconcileCancellingRunTx's own LiveCount==0 gate
// (completion.go) could never pass, and the owning WorkflowRun was stuck in
// CANCELLING forever — confirmed empirically (internal/integration/v5accept's
// own cancel-during-mutating-attempt scenario) before this fix.
//
// The user's own binding fix contract (verbatim state matrix, 2026-09-12):
// Attempt -> INDETERMINATE/OWNERSHIP_LOST_MUTATING; Workspace -> QUARANTINED
// if a mutation is observed; NodeRun -> CANCELLED; BranchToken -> CANCELLED
// if present; WorkflowRun -> CANCELLED once LiveCount==0; WorkItem ->
// BLOCKED with a RUN_CANCELLED blocker; cancellation intent -> COMPLETED.
// All of this (except the Attempt CAS/event and the workspace quarantine
// itself) already exists and is reused verbatim here:
// terminalizeBranchTokenForCancelledNodeRunTx and reconcileRunTerminalityTx
// (finalize.go/completion.go) are the SAME functions decideCancelledOutcomeTx
// already uses for a plain (non-mutating) cancelled Attempt — and
// reconcileRunTerminalityTx's own reconcileCancellingRunTx branch already
// opens the WorkItem-level RUN_CANCELLED blocker via transitionRunToCancelledTx
// once it observes LiveCount==0 (openRunCancelledBlockerTx, completion.go) —
// nothing new needed for that step, only for actually letting the NodeRun
// reach a terminal state in the first place so LiveCount can ever reach zero.
//
// Real I/O (CaptureRevision per write mount) still runs BEFORE the atomic
// transaction, exactly like the original code — a real git revision read is
// not something a database transaction should ever wrap. Everything durable
// this decision produces (Attempt CAS + its own audit event, workspace
// quarantine, NodeRun CAS, branch-token terminalization, Run-terminality
// reconciliation, and — defensively — the cancellation intent's own
// COMPLETED transition) commits together in ONE uow.WithSerializedWrite: a
// crash before that commit leaves the Attempt still genuinely RUNNING (the
// recovery reaper can still pick it up for real); a crash after it leaves
// every one of those rows mutually consistent. WriteLeases are released
// only AFTER this transaction commits (V5-08C's own already-locked "chỉ
// release WriteLease SAU KHI xác nhận process đã dừng" — now also after the
// full finalization is durable, not merely after the Attempt alone is).
func finalizeMutatingCancellation(
	ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, workspaces ports.WorkspaceProvider,
	writeLeases ports.WriteLeaseManager, req ports.NodeExecutionRequest, resolved resolvedExecutionResources,
) error {
	// cleanupCtx (context.WithoutCancel, the SAME real pattern pool.go's own
	// heartbeatLoop already uses for its lease-renewal write) is what every
	// real I/O and durable write below actually uses, never ctx directly:
	// by the time this function is ever called, ctx (execCtx,
	// pollForCancellation's own doit signal) has ALREADY been cancelled —
	// that is precisely what "a cancellation was observed" means here. Real
	// I/O against an already-cancelled ctx does not merely fail fast; a
	// real SQLite write transaction (uow.WithSerializedWrite, BEGIN
	// IMMEDIATE) that needs to wait even briefly for another writer (the
	// SAME Attempt's own still-active heartbeat loop, for one) can hang
	// INDEFINITELY on an already-done ctx rather than erroring — confirmed
	// empirically (not guessed) while building this: the real cancellation
	// poller fired and killed the real process correctly, but this whole
	// function then never returned, leaving the Run stuck exactly like
	// before this fix. This finalize step is only ever reached BECAUSE
	// cancellation was already observed — it must never itself be starved
	// by that same cancellation.
	cleanupCtx := context.WithoutCancel(ctx)

	verdicts := make([]cancelledMutatingMountVerdict, 0, len(resolved.writeMounts))
	for _, mount := range resolved.writeMounts {
		currentRevision, err := workspaces.CaptureRevision(cleanupCtx, mount.handle)
		if err != nil {
			return fmt.Errorf("runtime: capture current revision for repository workspace %s during cancellation finalization: %w", mount.repositoryWorkspaceID, err)
		}
		verdict, err := worker.ReconcileMutatingAttempt(mount.pinnedRevision, currentRevision.VCSObjectID)
		if err != nil {
			return fmt.Errorf("runtime: reconcile cancelled mutating attempt against repository workspace %s: %w", mount.repositoryWorkspaceID, err)
		}
		verdicts = append(verdicts, cancelledMutatingMountVerdict{
			repositoryWorkspaceID: mount.repositoryWorkspaceID, workspaceVersion: mount.workspaceVersion,
			mutationObserved: verdict == worker.ReconciliationMutationObserved,
		})
	}

	if err := cancelMutatingAttemptTx(cleanupCtx, uow, ids, req.AttemptID, req.NodeRunID, req.RunID, verdicts); err != nil {
		return err
	}

	if len(resolved.writeLeaseGrants) > 0 {
		if err := writeLeases.ReleaseWriteLeases(cleanupCtx, resolved.writeLeaseGrants); err != nil {
			return fmt.Errorf("runtime: release write leases after cancelled mutating attempt %s: %w", req.AttemptID, err)
		}
	}
	return ErrAttemptAlreadyTerminated
}

// cancelledMutatingMountVerdict is one write mount's own already-computed
// reconciliation verdict — cancelMutatingAttemptTx's own input. Deliberately
// carries no live capability (no WorkspaceHandle, no ports.WorkspaceProvider):
// computing the verdict needs real I/O (a live revision read) a caller must
// do BEFORE ever opening the atomic transaction below, using WHATEVER real
// mechanism it has access to — finalizeMutatingCancellation above uses
// ports.WorkspaceProvider.CaptureRevision (a real WorkspaceHandle), while
// RecoveryReaperHandler's own orphan-sweep caller (recovery_reaper.go) uses
// its own narrower worker.WorkspaceReconciler.LoadRepositoryWorkspaceRevision
// (a bare RepositoryWorkspaceID) instead — two different real capabilities,
// the same real verdict shape once each has one.
type cancelledMutatingMountVerdict struct {
	repositoryWorkspaceID workspace.RepositoryWorkspaceID
	workspaceVersion      uint64
	mutationObserved      bool
}

// cancelMutatingAttemptTx is finalizeMutatingCancellation's own atomic core
// (V5-15D, 2026-09-12) — extracted so RecoveryReaperHandler's own orphan
// sweep (recovery_reaper.go) can reuse the IDENTICAL real transaction for a
// genuinely mutating orphaned Attempt whose own owning Run is CANCELLING,
// per the user's own binding fix contract: "Recovery reaper cũng phải gọi
// cùng cancellation-finalization path khi orphaned Attempt thuộc một Run
// đang CANCELLING." Never called directly by anything outside this file and
// recovery_reaper.go — both call sites already independently computed
// verdicts (real revision reads) before ever reaching here, so this
// function's own only job is the durable, all-or-nothing part: CAS Attempt
// RUNNING->INDETERMINATE (+ its own audit event), quarantine every mount
// with an observed mutation, CAS NodeRun RUNNING->CANCELLED, terminalize its
// own BranchToken if any, reconcile Run terminality (which — once
// LiveCount==0 — is also where WorkflowRun actually reaches CANCELLED and
// the WorkItem-level RUN_CANCELLED blocker opens, completion.go, nothing
// new needed there), and defensively complete the cancellation intent.
//
// ctx is expected to already be an uncancelable derivative (both real
// callers pass one) — this function does not itself derive one, since it
// has no ctx of its own to derive FROM otherwise.
func cancelMutatingAttemptTx(
	ctx context.Context, uow ports.UnitOfWork, ids idsource.Source,
	attemptID, nodeRunID, runID string, verdicts []cancelledMutatingMountVerdict,
) error {
	occurredAt := time.Now().UTC()
	return uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		run, err := tx.Runtime().GetWorkflowRun(ctx, runID)
		if err != nil {
			return err
		}
		if run.State != runtimedomain.WorkflowRunCancelling && run.State != runtimedomain.WorkflowRunCancelled {
			return fmt.Errorf("runtime: cancellation finalization: run %s is no longer cancelling (state=%s)", runID, run.State)
		}

		attempt, err := tx.Runtime().GetExecutionAttempt(ctx, attemptID)
		if err != nil {
			return err
		}
		if string(attempt.NodeRunID) != nodeRunID {
			return fmt.Errorf("runtime: cancellation finalization: attempt %s does not belong to node run %s", attemptID, nodeRunID)
		}
		if attempt.State != runtimedomain.ExecutionAttemptRunning {
			// Already terminated by an earlier winner (a duplicate reaper
			// delivery, or the live cancellation poller's own path already
			// won the race) — idempotent no-op, never a second CAS attempt
			// or a second audit event for it.
			return nil
		}

		updatedAttempt, err := tx.Runtime().TransitionExecutionAttempt(ctx, ports.TransitionExecutionAttemptRequest{
			AttemptID: attemptID, ExpectedState: runtimedomain.ExecutionAttemptRunning, ExpectedVersion: attempt.Version,
			NextState: runtimedomain.ExecutionAttemptIndeterminate, TerminationReason: runtimedomain.TerminationReasonOwnershipLostMutating,
		})
		if err != nil {
			return err
		}
		terminatedPayload, err := json.Marshal(executionAttemptTerminatedEventPayload{
			AttemptID: attemptID, NextState: string(runtimedomain.ExecutionAttemptIndeterminate),
			Reason: string(runtimedomain.TerminationReasonOwnershipLostMutating),
		})
		if err != nil {
			return fmt.Errorf("marshal %s event payload: %w", executionAttemptTerminatedEventType, err)
		}
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID: attemptID + "-cancelled-indeterminate", ProjectID: string(run.ProjectID),
			AggregateType: "ExecutionAttempt", AggregateID: attemptID, Sequence: int64(updatedAttempt.Version),
			EventType: executionAttemptTerminatedEventType, SchemaVersion: executionAttemptTerminatedSchemaVersion,
			PayloadJSON: string(terminatedPayload), CreatedAt: occurredAt,
		}); err != nil {
			return fmt.Errorf("append %s event: %w", executionAttemptTerminatedEventType, err)
		}

		for _, v := range verdicts {
			if !v.mutationObserved {
				continue
			}
			if err := tx.Work().QuarantineRepositoryWorkspace(ctx, ports.QuarantineRepositoryWorkspaceUpdate{
				RepositoryWorkspaceID: v.repositoryWorkspaceID, ExpectedVersion: v.workspaceVersion,
				Reason: string(worker.ReconciliationMutationObserved), EventID: attemptID + "-cancelled-quarantine-" + string(v.repositoryWorkspaceID),
				OccurredAt: occurredAt,
			}); err != nil {
				return fmt.Errorf("quarantine repository workspace %s after cancelled mutating attempt: %w", v.repositoryWorkspaceID, err)
			}
		}

		nodeRun, err := tx.Runtime().GetNodeRun(ctx, nodeRunID)
		if err != nil {
			return err
		}
		if nodeRun.State == runtimedomain.NodeRunRunning {
			if _, err := tx.Runtime().TransitionNodeRun(ctx, ports.TransitionNodeRunRequest{
				NodeRunID: nodeRunID, ExpectedState: runtimedomain.NodeRunRunning, ExpectedVersion: nodeRun.Version,
				NextState: runtimedomain.NodeRunCancelled,
			}); err != nil {
				return err
			}
		}

		version, err := tx.Definitions().GetWorkflowVersion(ctx, string(run.WorkflowVersionID))
		if err != nil {
			return err
		}
		document := version.Document()

		if err := terminalizeBranchTokenForCancelledNodeRunTx(ctx, tx, ids, run, document, nodeRun, "", attemptID+"-cancel-finalize"); err != nil {
			return err
		}

		if err := reconcileRunTerminalityTx(ctx, tx, run, document, "", attemptID+"-cancel-finalize"); err != nil {
			return err
		}

		// Defensive (the user's own contract, item 7): the coordinator's own
		// earlier one-shot sweep (CancelRunCoordinatorHandler.Handle,
		// cancel_run_coordinator.go) already marks the RunCancellationIntent
		// COMPLETED regardless of whether the Run itself closed out yet, so
		// this is normally already a no-op by the time this transaction
		// runs — kept only as a safety net, never trusted as the primary
		// mechanism, and never treated as an error if it no-ops.
		if _, err := tx.Runtime().TransitionRunCancellationIntentState(ctx, ports.TransitionRunCancellationIntentStateRequest{
			RunID: runID, ExpectedState: runtimedomain.CancellationIntentRequested, NextState: runtimedomain.CancellationIntentCompleted,
		}); err != nil && !errors.Is(err, ports.ErrOptimisticConflict) && !errors.Is(err, ports.ErrPersistenceNotFound) {
			return fmt.Errorf("complete run cancellation intent for run %s: %w", runID, err)
		}

		return nil
	})
}
