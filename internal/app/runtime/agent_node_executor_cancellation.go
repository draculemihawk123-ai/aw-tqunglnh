// This file holds AgentNodeExecutor's own V5-08C cancellation-disposition
// logic — kept out of agent_node_executor.go itself for the same reason
// agent_node_executor_resources.go already is: keeping Execute/classify's
// own control flow readable. See classify's own AgentExecutionCancelled
// case (agent_node_executor.go) for where this is reached.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/worker"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// ErrAttemptAlreadyTerminated signals execute.go's own Handle that THIS
// call already moved the Attempt to a terminal state itself (V5-08C's own
// mutating-cancellation path, handleMutatingCancellation below) — unlike
// ErrIndeterminateExecution (which means "leave RUNNING, someone else
// resolves this later"), this means "already resolved, right now; do not
// call FinalizeExecutionAttempt at all" (the Attempt is no longer RUNNING
// by the time this returns, so that CAS would just fail).
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
func (e *AgentNodeExecutor) classifyCancellation(
	ctx context.Context, req ports.NodeExecutionRequest, resolved resolvedExecutionResources,
) (ports.NodeExecutionResult, error) {
	var runCancelling bool
	if err := e.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		run, err := tx.Runtime().GetWorkflowRun(ctx, req.RunID)
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
	return ports.NodeExecutionResult{}, e.handleMutatingCancellation(ctx, req, resolved)
}

// handleMutatingCancellation is V5-08C's own locked requirement: "mutating
// attempt không chứng minh được kết quả thành INDETERMINATE với workspace
// QUARANTINED" — a mutating attempt that was genuinely cancelled mid-flight
// can never be assumed clean just because the process is confirmed dead
// (classify's own TreeQuiesced check, already run before this is ever
// reached); it must be reconciled right now, not left for a later
// crash-recovery sweep to eventually discover. This reuses V4-13's own
// already-proven primitives exactly as RecoveryReaperHandler does
// (recovery_reaper.go) — internal/app/worker/interruption.go's own
// TerminateInterruptedAttempt/ReconcileMutatingAttempt/
// QuarantineRepositoryWorkspace — rather than inventing a second way to
// reach the identical durable outcome. Unlike that crash-recovery caller,
// this bridge already knows definitively that the attempt IS mutating
// (resolved.hasWriteMount) and already has every repository workspace's
// own identity/pinned revision in hand (resolved.writeMounts) — no
// ClassifyInterruptedAttempt/AttemptHeldAnyWriteLease round trip needed.
//
// WriteLeases are released only after this whole reconciliation completes
// (V5-08C's own locked "chỉ release WriteLease SAU KHI xác nhận process đã
// dừng") — releasing before quarantine would open a real window for
// another attempt to acquire a write lease against a workspace whose own
// integrity is not yet decided.
func (e *AgentNodeExecutor) handleMutatingCancellation(
	ctx context.Context, req ports.NodeExecutionRequest, resolved resolvedExecutionResources,
) error {
	occurredAt := time.Now().UTC()
	attemptVersion, err := e.loadAttemptVersion(ctx, req.AttemptID)
	if err != nil {
		return fmt.Errorf("runtime: load attempt version before terminating a cancelled mutating attempt: %w", err)
	}
	if err := e.interruptions.TerminateInterruptedAttempt(ctx, ports.AttemptTerminationUpdate{
		AttemptID: ports.ExecutionAttemptID(req.AttemptID), ExpectedVersion: attemptVersion,
		NextState: runtimedomain.ExecutionAttemptIndeterminate, Reason: runtimedomain.TerminationReasonOwnershipLostMutating,
		EventID: req.AttemptID + "-cancelled-indeterminate", CorrelationID: "", OccurredAt: occurredAt,
	}); err != nil {
		return fmt.Errorf("runtime: terminate cancelled mutating attempt %s indeterminate: %w", req.AttemptID, err)
	}

	for _, mount := range resolved.writeMounts {
		currentRevision, err := e.workspaces.CaptureRevision(ctx, mount.handle)
		if err != nil {
			return fmt.Errorf("runtime: capture current revision for repository workspace %s during cancellation reconciliation: %w", mount.repositoryWorkspaceID, err)
		}
		verdict, err := worker.ReconcileMutatingAttempt(mount.pinnedRevision, currentRevision.VCSObjectID)
		if err != nil {
			return fmt.Errorf("runtime: reconcile cancelled mutating attempt against repository workspace %s: %w", mount.repositoryWorkspaceID, err)
		}
		if verdict != worker.ReconciliationMutationObserved {
			continue
		}
		if err := e.reconciler.QuarantineRepositoryWorkspace(ctx, ports.QuarantineRepositoryWorkspaceUpdate{
			RepositoryWorkspaceID: mount.repositoryWorkspaceID, ExpectedVersion: mount.workspaceVersion,
			Reason: string(verdict), EventID: req.AttemptID + "-cancelled-quarantine-" + string(mount.repositoryWorkspaceID),
			CorrelationID: "", OccurredAt: occurredAt,
		}); err != nil {
			return fmt.Errorf("runtime: quarantine repository workspace %s after cancelled mutating attempt: %w", mount.repositoryWorkspaceID, err)
		}
	}

	if len(resolved.writeLeaseGrants) > 0 {
		if err := e.writeLeases.ReleaseWriteLeases(ctx, resolved.writeLeaseGrants); err != nil {
			return fmt.Errorf("runtime: release write leases after cancelled mutating attempt %s: %w", req.AttemptID, err)
		}
	}
	return ErrAttemptAlreadyTerminated
}

// loadAttemptVersion re-reads req.AttemptID's own current Version — needed
// fresh (not the Version this Attempt had when Execute started) since
// nothing else has fenced-CAS'd it since; TerminateInterruptedAttempt's own
// CAS needs the real current value.
func (e *AgentNodeExecutor) loadAttemptVersion(ctx context.Context, attemptID string) (uint64, error) {
	var version uint64
	if err := e.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		attempt, err := tx.Runtime().GetExecutionAttempt(ctx, attemptID)
		if err != nil {
			return err
		}
		version = attempt.Version
		return nil
	}); err != nil {
		return 0, err
	}
	return version, nil
}
