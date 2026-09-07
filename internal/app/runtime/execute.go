// ExecuteNodeHandler is V4-05's own workerpool.Handler for ExecuteNodeKind
// (schedule.go, V4-04) — the consumer that schedule.go's own doc comment
// already named as "V4-05's fake executor tương lai". It is the "job claim,
// attempt RUNNING, optional write leases, fake typed events/result, fenced
// finalize" sequence docs/design/06-v4-runtime-engine.md's own V4-05
// section names, composed as one Handle call:
//
//  1. Unmarshal the job payload; transition the Attempt QUEUED->RUNNING
//     (plain CAS, unfenced — the job's own already-valid LEASED state at
//     the moment workerpool.Pool invoked this Handle is fencing enough for
//     this one transition; only the LATER terminal transition needs the
//     full fenced finalize, since only that one accepts a result an
//     external executor produced). Idempotent: anything other than QUEUED
//     means an earlier delivery of this exact job already claimed this
//     attempt (or a later recovery task, V4-13, is now the sole authority
//     over it) — no-op, mirroring AdvanceRun/ScheduleExecutableNodeRun's
//     own idempotent-early-return discipline.
//  2. Resolve the pinned ResolvedExecutionProfileV1's own TimeoutSeconds —
//     the ONLY durable source of it (NodeRun/Attempt only carry the opaque
//     ExecutionProfileHash) — from the DecisionArtifact V4-04's own
//     ScheduleExecutableNodeRun records under the deterministic ID
//     "<nodeRunId>-execution-profile-v1".
//  3. Run the injected ports.NodeExecutor under a context deliberately
//     DERIVED from this deadline (context.WithDeadline), not the bare job
//     context — this is what lets step 4 tell "our own AttemptPolicy
//     deadline fired" (TIMED_OUT) apart from "something else cancelled the
//     outer context" (pool shutdown, heartbeat loss) without ever mapping
//     every context.DeadlineExceeded/Canceled to a terminal Attempt state
//     (confirmed with the user before writing this file — a lecture this
//     package itself needed, since the two are easy to conflate): only
//     THIS derived context's own DeadlineExceeded means our deadline
//     fired; if the OUTER context is what actually got cancelled, the
//     derived one reports Canceled instead (context's own propagation
//     rule), which this handler treats as "do not finalize, leave RUNNING"
//     rather than a business outcome.
//  4. Accept or refuse the executor's proposed result via
//     FinalizeExecutionAttempt — the sole fenced acceptance path
//     (GC-INV-17/18). "cancel" (outer context cancelled, not our own
//     deadline) and "lease-loss" (FinalizeExecutionAttempt's own fencing
//     rejects a stale JobLease) BOTH leave the Attempt RUNNING,
//     un-terminalized, un-eventful — V4-12B (cancellation coordinator) and
//     V4-13 (recovery coordinator) are the durable authorities over those
//     two cases respectively, not this task.
//
// V4-05's own bounded scope on TerminationReason (confirmed with the
// user): this handler only ever produces COMPLETED (SUCCEEDED),
// EXECUTION_FAILED (FAILED) or DEADLINE_EXCEEDED (TIMED_OUT) — the exact
// three reasons termination.go's own doc comment names as "V4-05
// COMPLETED/EXECUTION_FAILED/DEADLINE_EXCEEDED". It never writes
// RUN_CANCELLED (V4-12B) or LEASE_LOST/OWNERSHIP_LOST_MUTATING (V4-13).
//
// Write-lease acquisition is deliberately NOT implemented here yet, even
// though FinalizeExecutionAttempt's own fenced finalize already accepts
// and validates WriteLeaseGrant proofs end to end (schedule.go's own
// "optional write leases" line): this task's own fake executor never
// writes to a real repository, so there is nothing yet that needs one
// acquired for real, and ports.WriteLeaseManager itself has no fake
// counterpart anywhere in this codebase (internal/app/ports/fake/work.go's
// own HasActiveWriteLease doc comment: "checkable for real today, not
// faked"). V5's own real executor is the first caller with a genuine
// reason to resolve NodeRun.EffectiveScope's WRITE grants into
// WorkspaceLeaseTargets and call AcquireWriteLeases before Execute.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// ErrExecutionProfileMissingTimeout is returned when the pinned
// ResolvedExecutionProfileV1 decision artifact for a NodeRun has no
// positive TimeoutSeconds — should be unreachable in practice
// (NewResolvedExecutionProfileV1 already rejects TimeoutSeconds == 0
// before V4-04 ever records the artifact), so this exists to fail closed
// rather than run an executor under an unbounded deadline if that
// invariant is ever somehow violated.
var ErrExecutionProfileMissingTimeout = errors.New("runtime: execution profile decision artifact is missing a positive timeoutSeconds")

// ErrContextSnapshotUnverified is returned by verifyContextSnapshot
// (V5-04) when the Attempt's own bound context manifest cannot be
// confirmed durable and intact immediately before dispatch — see Handle's
// own doc comment at the call site for what happens next (the Attempt is
// left RUNNING, never finalized here).
var ErrContextSnapshotUnverified = errors.New("runtime: execution attempt's context snapshot could not be verified before dispatch")

// ExecuteNodeHandler is a ready-to-register workerpool.Handler for
// ExecuteNodeJobKind.
type ExecuteNodeHandler struct {
	uow      ports.UnitOfWork
	ids      idsource.Source
	executor ports.NodeExecutor
	clk      clock.Clock
}

// NewExecuteNodeHandler returns a ready-to-register ExecuteNodeHandler. clk
// is threaded through to every FinalizeExecutionAttempt call (V4-06) so a
// retryable failure's own AvailableAt backoff computation is deterministic
// under a test's clock.Fixed — pass clock.System{} in production.
func NewExecuteNodeHandler(uow ports.UnitOfWork, ids idsource.Source, executor ports.NodeExecutor, clk clock.Clock) *ExecuteNodeHandler {
	return &ExecuteNodeHandler{uow: uow, ids: ids, executor: executor, clk: clk}
}

var _ workerpool.Handler = (*ExecuteNodeHandler)(nil)

// resolvedExecutionProfileView decodes only the fields this handler needs
// from the canonical ResolvedExecutionProfileV1 JSON — it is deliberately
// not the full internal/domain/runtime.ResolvedExecutionProfileV1 type
// (that type lives in the domain package this app-layer file does not
// need the rest of; a partial view is the same "decode just what this
// resolver needs" discipline schedule.go's own decodeCompiledAgentProfile/
// decodeCompiledPolicy already established for a different snapshot shape).
type resolvedExecutionProfileView struct {
	Executor struct {
		Kind string `json:"kind"`
	} `json:"executor"`
	TimeoutSeconds uint32 `json:"timeoutSeconds"`
}

// Handle implements workerpool.Handler for ExecuteNodeJobKind. See this
// file's own package doc comment for the full four-step design.
func (h *ExecuteNodeHandler) Handle(ctx context.Context, job ports.DurableJob) error {
	var payload ExecuteNodeJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("runtime: unmarshal %s job %s payload: %w", ExecuteNodeJobKind, job.ID, err)
	}
	if payload.RunID == "" || payload.NodeRunID == "" || payload.AttemptID == "" {
		return fmt.Errorf("runtime: %s job %s payload missing runId/nodeRunId/attemptId", ExecuteNodeJobKind, job.ID)
	}
	if job.LeaseUntil == nil {
		return fmt.Errorf("runtime: %s job %s has no active lease", ExecuteNodeJobKind, job.ID)
	}
	jobLease := ports.JobLease{JobID: job.ID, Owner: job.LeaseOwner, Token: job.LeaseToken, LeaseUntil: *job.LeaseUntil}

	running, advanced, err := h.claimRunning(ctx, payload)
	if err != nil {
		return err
	}
	if !advanced {
		// Idempotent no-op — see this file's own package doc comment. This
		// is also V4-12B's own FIRST worker re-check checkpoint's own
		// no-op outcome: claimRunning itself declines to advance
		// QUEUED->RUNNING when the owning Run is already CANCELLING/
		// CANCELLED, leaving the actual QUEUED->CANCELLED transition to
		// the CANCEL_RUN_COORDINATOR job's own sweep (never raced here).
		return nil
	}

	profile, err := h.loadExecutionProfile(ctx, payload.NodeRunID)
	if err != nil {
		return err
	}
	if profile.TimeoutSeconds == 0 {
		return fmt.Errorf("%w: node run %s", ErrExecutionProfileMissingTimeout, payload.NodeRunID)
	}

	// V4-12B's own SECOND worker re-check checkpoint ("ngay trước
	// ProcessSupervisor.Start" — this codebase's own closest analogue
	// today, since no real ProcessSupervisor exists yet; V5-08C owns
	// real process/provider cancellation): claimRunning's own CAS to
	// RUNNING and this call are two separate transactions, so a cancel
	// intent can still commit in between. No real work has started yet
	// at this exact point, so it is both safe and correct to finalize
	// straight to CANCELLED here (TerminationReasonRunCancelled, the
	// RUNNING-sourced reason — distinct from the coordinator's own
	// QUEUED-sourced TerminationReasonRunCancelledBeforeStart) rather
	// than ever invoking the executor. Once the executor call below
	// actually starts, Alpha has no way to interrupt it — that Attempt
	// runs to its own natural conclusion and is accepted as a historical
	// fact by FinalizeExecutionAttempt, exactly like every other
	// authoritative outcome; only the Run's own routing is suppressed
	// (advanceRunTx/decideRetryOrExhaustion's own cancelling guards).
	if cancelling, err := h.runIsCancelling(ctx, payload.RunID); err != nil {
		return err
	} else if cancelling {
		_, finalizeErr := FinalizeExecutionAttempt(ctx, h.uow, h.ids, h.clk, FinalizeExecutionAttemptRequest{
			RunID: payload.RunID, NodeRunID: payload.NodeRunID, AttemptID: payload.AttemptID, ExpectedVersion: running.Version,
			NextState: runtimedomain.ExecutionAttemptCancelled, TerminationReason: runtimedomain.TerminationReasonRunCancelled,
			JobLease: jobLease, CorrelationID: payload.CorrelationID,
		})
		return finalizeErr
	}

	// V5-04's own dispatch precondition ("provider không start nếu
	// snapshot chưa durable"): re-verify the Attempt's own bound context
	// snapshot in a fresh read-only transaction, closed BEFORE the
	// executor is ever invoked (never hold a DB transaction open across
	// an external process call). A failure here returns without ever
	// calling h.executor.Execute — executor spawn count is zero — and,
	// like the cancel/lease-loss cases this file's own doc comment
	// already documents, does NOT finalize the Attempt: it is left
	// RUNNING, unterminalized. V5-08 (not yet built) is the future
	// authority that maps this typed error to a real admission outcome
	// (BLOCKED + an appropriate TerminationReason); no such reason exists
	// in today's state-reason matrix (docs/architecture/04-go-core-spec.md
	// §4.5), and inventing one here would be exactly the kind of
	// unauthorized state-machine decision this task's own scope avoids.
	if err := h.verifyContextSnapshot(ctx, payload.RunID, running); err != nil {
		return err
	}

	deadline := time.Now().Add(time.Duration(profile.TimeoutSeconds) * time.Second)
	attemptCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	execResult, execErr := h.executor.Execute(attemptCtx, ports.NodeExecutionRequest{
		AttemptID: payload.AttemptID, NodeRunID: payload.NodeRunID, RunID: payload.RunID,
		ExecutorKind: profile.Executor.Kind, ExecutionProfileHash: running.ExecutionProfileHash,
	})

	if execErr != nil {
		if errors.Is(attemptCtx.Err(), context.DeadlineExceeded) {
			_, finalizeErr := FinalizeExecutionAttempt(ctx, h.uow, h.ids, h.clk, FinalizeExecutionAttemptRequest{
				RunID: payload.RunID, NodeRunID: payload.NodeRunID, AttemptID: payload.AttemptID, ExpectedVersion: running.Version,
				NextState: runtimedomain.ExecutionAttemptTimedOut, TerminationReason: runtimedomain.TerminationReasonDeadlineExceeded,
				FailureCode: errorcode.CodeTimeout, JobLease: jobLease, CorrelationID: payload.CorrelationID,
			})
			return finalizeErr
		}
		if attemptCtx.Err() != nil {
			// Cancelled for a reason OTHER than our own deadline — do NOT
			// finalize; leave the Attempt RUNNING. See this file's own
			// package doc comment step 4.
			return attemptCtx.Err()
		}
		// execErr is a bare Go error, not a structured NodeExecutionResult —
		// the executor returned before it could classify its own failure
		// (e.g. it panicked/errored outside its own result-construction
		// path), so this handler falls back to the coarsest classification
		// rather than leaving FailureCode empty (ErrFailureCodeRequired
		// would otherwise reject this finalize outright).
		_, finalizeErr := FinalizeExecutionAttempt(ctx, h.uow, h.ids, h.clk, FinalizeExecutionAttemptRequest{
			RunID: payload.RunID, NodeRunID: payload.NodeRunID, AttemptID: payload.AttemptID, ExpectedVersion: running.Version,
			NextState: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonExecutionFailed,
			FailureCode: errorcode.CodeExecutionFailed, JobLease: jobLease, CorrelationID: payload.CorrelationID,
		})
		return finalizeErr
	}

	nextState := runtimedomain.ExecutionAttemptFailed
	reason := runtimedomain.TerminationReasonExecutionFailed
	failureCode := execResult.ErrorCode
	if failureCode == "" {
		failureCode = errorcode.CodeExecutionFailed
	}
	var scopeProposal *runtimedomain.ScopeExpansionProposal
	switch execResult.State {
	case runtimedomain.ExecutionAttemptSucceeded:
		nextState = runtimedomain.ExecutionAttemptSucceeded
		reason = runtimedomain.TerminationReasonCompleted
		failureCode = ""
	case runtimedomain.ExecutionAttemptBlocked:
		// V4-12A (confirmed with the user before writing this file): a
		// malformed proposal is treated as a real, non-retryable FAILURE
		// — OUTCOME_REJECTED — never a legitimate BLOCKED transition
		// ("Shape sai → OUTCOME_REJECTED, không tạo scope request"). Only
		// a well-formed proposal ever reaches FinalizeExecutionAttempt as
		// BLOCKED.
		if err := execResult.RequestedScopeExpansion.Validate(); err != nil {
			nextState = runtimedomain.ExecutionAttemptFailed
			reason = runtimedomain.TerminationReasonOutcomeRejected
			failureCode = errorcode.CodeValidationFailed
		} else {
			nextState = runtimedomain.ExecutionAttemptBlocked
			reason = runtimedomain.TerminationReasonScopeExpansionRequired
			failureCode = ""
			scopeProposal = execResult.RequestedScopeExpansion
		}
	default:
		if execResult.TerminationReason != "" {
			reason = execResult.TerminationReason
		}
	}
	_, finalizeErr := FinalizeExecutionAttempt(ctx, h.uow, h.ids, h.clk, FinalizeExecutionAttemptRequest{
		RunID: payload.RunID, NodeRunID: payload.NodeRunID, AttemptID: payload.AttemptID, ExpectedVersion: running.Version,
		NextState: nextState, TerminationReason: reason, FailureCode: failureCode, SelectedOutcome: execResult.SelectedOutcome,
		RequestedScopeExpansion: scopeProposal, JobLease: jobLease, CorrelationID: payload.CorrelationID,
	})
	return finalizeErr
}

// claimRunning transitions the Attempt QUEUED->RUNNING, and — in the same
// transaction — the NodeRun itself QUEUED->RUNNING alongside it: nothing
// upstream of this handler ever advances the NodeRun past the QUEUED state
// V4-04's own ScheduleExecutableNodeRun leaves it in, but advanceRunTx's
// own routing step (this file's own Handle, on the SUCCEEDED path) only
// ever completes a NodeRun it observes as RUNNING — exactly the state this
// step is responsible for establishing before any executor runs. advanced
// is false for the idempotent no-op case (Attempt was not QUEUED).
func (h *ExecuteNodeHandler) claimRunning(ctx context.Context, payload ExecuteNodeJobPayload) (runtimedomain.ExecutionAttempt, bool, error) {
	var running runtimedomain.ExecutionAttempt
	advanced := false
	err := h.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		current, err := tx.Runtime().GetExecutionAttempt(ctx, payload.AttemptID)
		if err != nil {
			return err
		}
		if current.State != runtimedomain.ExecutionAttemptQueued {
			return nil
		}
		// V4-12B's own FIRST worker re-check checkpoint ("trước QUEUED ->
		// RUNNING"): decline to advance if the owning Run already
		// committed a cancellation intent — never race the
		// CANCEL_RUN_COORDINATOR job's own sweep by trying to CAS this
		// same Attempt/NodeRun to CANCELLED here too; that job is the
		// SOLE authority for the QUEUED->CANCELLED transition
		// (TerminationReasonRunCancelledBeforeStart), and will claim this
		// exact Attempt on its own next pass regardless of this no-op.
		run, err := tx.Runtime().GetWorkflowRun(ctx, payload.RunID)
		if err != nil {
			return err
		}
		if run.State == runtimedomain.WorkflowRunCancelling || run.State == runtimedomain.WorkflowRunCancelled {
			return nil
		}
		nodeRun, err := tx.Runtime().GetNodeRun(ctx, payload.NodeRunID)
		if err != nil {
			return err
		}
		if nodeRun.State == runtimedomain.NodeRunQueued {
			if _, err := tx.Runtime().TransitionNodeRun(ctx, ports.TransitionNodeRunRequest{
				NodeRunID: payload.NodeRunID, ExpectedState: runtimedomain.NodeRunQueued, ExpectedVersion: nodeRun.Version,
				NextState: runtimedomain.NodeRunRunning,
			}); err != nil {
				return err
			}
		}
		running, err = tx.Runtime().TransitionExecutionAttempt(ctx, ports.TransitionExecutionAttemptRequest{
			AttemptID: payload.AttemptID, ExpectedState: runtimedomain.ExecutionAttemptQueued, ExpectedVersion: current.Version,
			NextState: runtimedomain.ExecutionAttemptRunning,
		})
		if err != nil {
			return err
		}
		advanced = true
		return nil
	})
	return running, advanced, err
}

// runIsCancelling is V4-12B's own read-only check for the second worker
// re-check checkpoint (this file's own Handle, right before the executor
// is ever invoked).
func (h *ExecuteNodeHandler) runIsCancelling(ctx context.Context, runID string) (bool, error) {
	var cancelling bool
	err := h.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		run, err := tx.Runtime().GetWorkflowRun(ctx, runID)
		if err != nil {
			return err
		}
		cancelling = run.State == runtimedomain.WorkflowRunCancelling || run.State == runtimedomain.WorkflowRunCancelled
		return nil
	})
	return cancelling, err
}

// verifyContextSnapshot is V5-04's own dispatch-time re-check: attempt's
// own bound ContextSnapshotID must resolve to a real, durable Snapshot row
// (whose own load path, contextSnapshotRepository.GetSnapshot, already
// recomputes ManifestHash and rejects a stored/recomputed mismatch as
// ports.ErrImmutableVersionConflict — tamper/corruption detection lives
// there, not duplicated here) genuinely bound to THIS attempt, whose own
// Project/WorkItem/RevisionSet match what the owning Run/Attempt actually
// pin. Runs inside its own read-only transaction, closed before Handle's
// own caller ever invokes the executor.
func (h *ExecuteNodeHandler) verifyContextSnapshot(ctx context.Context, runID string, attempt runtimedomain.ExecutionAttempt) error {
	return h.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		if attempt.ContextSnapshotID == nil {
			return fmt.Errorf("%w: attempt %s has no bound context snapshot", ErrContextSnapshotUnverified, attempt.ID)
		}
		snapshot, err := tx.ContextSnapshots().GetSnapshot(ctx, string(*attempt.ContextSnapshotID))
		if err != nil {
			return fmt.Errorf("%w: load snapshot %s: %v", ErrContextSnapshotUnverified, *attempt.ContextSnapshotID, err)
		}
		if string(snapshot.AttemptID) != string(attempt.ID) {
			return fmt.Errorf("%w: snapshot %s is bound to attempt %s, not %s", ErrContextSnapshotUnverified, snapshot.ID, snapshot.AttemptID, attempt.ID)
		}
		run, err := tx.Runtime().GetWorkflowRun(ctx, runID)
		if err != nil {
			return err
		}
		if snapshot.ProjectID != run.ProjectID || snapshot.WorkItemID != run.WorkItemID {
			return fmt.Errorf("%w: snapshot %s project/work item does not match run %s", ErrContextSnapshotUnverified, snapshot.ID, runID)
		}
		if attempt.InputRevisionSet != nil && attempt.InputRevisionSet.ContentHash() != snapshot.Revisions.ContentHash() {
			return fmt.Errorf("%w: snapshot %s revision set does not match attempt %s", ErrContextSnapshotUnverified, snapshot.ID, attempt.ID)
		}
		return nil
	})
}

func (h *ExecuteNodeHandler) loadExecutionProfile(ctx context.Context, nodeRunID string) (resolvedExecutionProfileView, error) {
	var decision runtimedomain.DecisionArtifact
	err := h.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		decision, err = tx.Runtime().GetDecisionArtifact(ctx, nodeRunID+"-execution-profile-v1")
		return err
	})
	if err != nil {
		return resolvedExecutionProfileView{}, fmt.Errorf("runtime: load execution profile decision for node run %s: %w", nodeRunID, err)
	}
	var profile resolvedExecutionProfileView
	if err := json.Unmarshal(decision.Result, &profile); err != nil {
		return resolvedExecutionProfileView{}, fmt.Errorf("runtime: decode execution profile decision for node run %s: %w", nodeRunID, err)
	}
	return profile, nil
}
