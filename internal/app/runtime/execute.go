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

	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
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
	// isolation and agents are V5-08's own admission dependencies —
	// isolation answers "is the pinned tier enforceable right now"
	// (ADR-023, V5-05's own checker); agents resolves the live,
	// real ports.AgentExecutor a pinned AdapterBuildVersion's own
	// ProviderKey names, so the adapter-build-drift check can probe it
	// fresh (V5-06/07). Neither is used for actual task execution — that
	// still goes through executor (ports.NodeExecutor) unchanged.
	isolation ports.IsolationEnforcementChecker
	agents    *agentregistry.Registry
}

// NewExecuteNodeHandler returns a ready-to-register ExecuteNodeHandler. clk
// is threaded through to every FinalizeExecutionAttempt call (V4-06) so a
// retryable failure's own AvailableAt backoff computation is deterministic
// under a test's clock.Fixed — pass clock.System{} in production. isolation
// and agents are V5-08's own admission dependencies (see their own struct
// field doc comments) — a caller with no real adapter builds ever pinned
// can safely pass agentregistry.New(ctx) with zero executors registered.
func NewExecuteNodeHandler(
	uow ports.UnitOfWork, ids idsource.Source, executor ports.NodeExecutor, clk clock.Clock,
	isolation ports.IsolationEnforcementChecker, agents *agentregistry.Registry,
) *ExecuteNodeHandler {
	return &ExecuteNodeHandler{uow: uow, ids: ids, executor: executor, clk: clk, isolation: isolation, agents: agents}
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
		Kind         string `json:"kind"`
		DefinitionID string `json:"definitionId"`
		VersionID    string `json:"versionId"`
		CompiledHash string `json:"compiledHash"`
	} `json:"executor"`
	// AdapterBuild is nil whenever the node declared no AdapterBuildID
	// (a legitimate, deliberately deferred Alpha state — see
	// runtime.ResolvedExecutionProfileV1.AdapterBuild's own doc comment)
	// — V5-08's own drift check has nothing to verify in that case.
	AdapterBuild *struct {
		BuildID string `json:"buildId"`
	} `json:"adapterBuild,omitempty"`
	TimeoutSeconds uint32 `json:"timeoutSeconds"`
	// IsolationTier and AllowedCapabilities are V5-08's own admission
	// inputs (ADR-023, HE-02-M02) — both already pinned by schedule.go's
	// own resolveExecutionProfile, just not previously decoded here since
	// nothing needed them before this task.
	IsolationTier       policy.IsolationTier `json:"isolationTier"`
	AllowedCapabilities []string             `json:"allowedCapabilities,omitempty"`
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

	profile, err := h.loadExecutionProfile(ctx, payload.NodeRunID)
	if err != nil {
		return err
	}
	if profile.TimeoutSeconds == 0 {
		return fmt.Errorf("%w: node run %s", ErrExecutionProfileMissingTimeout, payload.NodeRunID)
	}

	// V5-08's own admission Phase 1: real I/O (isolation check, live
	// adapter-build probe), entirely outside any database transaction —
	// see this package's own admission.go doc comment for why. Must run
	// even for the idempotent-redelivery case (admitOrClaimRunning below
	// still needs a probe result to pass into Phase 2), which is
	// harmless: Phase 2 re-checks the Attempt is still QUEUED before
	// acting on it either way.
	probe, err := h.runAdmissionProbePhase(ctx, payload, profile)
	if err != nil {
		return err
	}

	running, outcome, err := h.admitOrClaimRunning(ctx, payload, jobLease, profile, probe)
	if err != nil {
		return err
	}
	switch outcome {
	case admissionNoop:
		// Idempotent no-op — see this file's own package doc comment. This
		// is also V4-12B's own FIRST worker re-check checkpoint's own
		// no-op outcome: admitOrClaimRunning itself declines to advance
		// QUEUED->RUNNING when the owning Run is already CANCELLING/
		// CANCELLED, leaving the actual QUEUED->CANCELLED transition to
		// the CANCEL_RUN_COORDINATOR job's own sweep (never raced here).
		return nil
	case admissionBlocked:
		// V5-08's own admission blocker group: the Attempt/NodeRun/
		// WorkItemBlocker transition already happened, atomically, inside
		// admitOrClaimRunning's own transaction. Nothing more to do —
		// spawn count stays zero, no retry budget consumed.
		return nil
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
	// calling h.executor.Execute — executor spawn count is zero.
	//
	// Audit finding (2026-09-08): this used to just `return err` here,
	// leaving the Attempt RUNNING forever — no path in this codebase ever
	// re-drives a RUNNING Attempt whose own job keeps failing this exact
	// precondition on every redelivery/retry (a real livelock, not a
	// theoretical one). RUNNING→FAILED/EXECUTION_FAILED is already a valid
	// entry in the closed state-reason matrix (ADR-020,
	// docs/architecture/04-go-core-spec.md §4.5) — the SAME (state, reason)
	// pair the bare-executor-error branch below already uses for "this
	// execution could not be classified more precisely" — so finalizing
	// here needs no new TerminationReason/vocabulary, unlike the
	// BLOCKED-admission idea the old comment here used to reserve for
	// "V5-08 (not yet built)".
	if err := h.verifyContextSnapshot(ctx, payload.RunID, running); err != nil {
		// Mirrors every other terminal-finalize branch in this function:
		// once FinalizeExecutionAttempt itself succeeds, the Attempt is
		// terminal and this job's own work is done — return finalizeErr
		// (nil on success), not the original verify error, so the job is
		// never redelivered/retried for an Attempt that already reached a
		// terminal state.
		_, finalizeErr := FinalizeExecutionAttempt(ctx, h.uow, h.ids, h.clk, FinalizeExecutionAttemptRequest{
			RunID: payload.RunID, NodeRunID: payload.NodeRunID, AttemptID: payload.AttemptID, ExpectedVersion: running.Version,
			NextState: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonExecutionFailed,
			FailureCode: errorcode.CodeExecutionFailed, JobLease: jobLease, CorrelationID: payload.CorrelationID,
		})
		return finalizeErr
	}

	deadline := time.Now().Add(time.Duration(profile.TimeoutSeconds) * time.Second)
	attemptCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	// V5-08C: execCtx is deliberately a CHILD of attemptCtx, cancelled by
	// this handler's own poller below rather than by attemptCtx itself —
	// attemptCtx.Err() must stay nil when a cancellation intent (not our
	// own deadline) is what actually stopped the executor, so the
	// pre-existing "attemptCtx.Err() != nil -> leave RUNNING" branch below
	// (V4-12B's own safe default for an ambiguous outer-context cancel)
	// does not also fire for a cause this handler now classifies for real.
	execCtx, cancelExec := context.WithCancel(attemptCtx)
	pollDone := make(chan struct{})
	go h.pollForCancellation(ctx, payload.RunID, execCtx, cancelExec, pollDone)

	execResult, execErr := h.executor.Execute(execCtx, ports.NodeExecutionRequest{
		AttemptID: payload.AttemptID, NodeRunID: payload.NodeRunID, RunID: payload.RunID,
		ExecutorKind: profile.Executor.Kind, ExecutionProfileHash: running.ExecutionProfileHash,
		JobLease: jobLease,
	})
	// execCtxErr must be captured BEFORE cancelExec() below — that call
	// only exists to stop pollForCancellation's own goroutine (and is a
	// no-op if the poller itself already cancelled execCtx), but it would
	// otherwise make execCtx.Err() non-nil for every single call, including
	// ones where the executor returned its own unrelated error well before
	// any deadline or cancellation ever occurred.
	execCtxErr := execCtx.Err()
	cancelExec()
	<-pollDone

	if execErr != nil {
		if errors.Is(execErr, ErrAttemptAlreadyTerminated) {
			// AgentNodeExecutor's own handleMutatingCancellation
			// (agent_node_executor_cancellation.go) already moved the
			// Attempt to a terminal state itself, durably, before
			// returning — there is nothing left for this handler to
			// finalize (that CAS would just fail against a RUNNING
			// ExpectedVersion that no longer matches). Returning nil marks
			// this job done, exactly like every other successful finalize
			// below.
			return nil
		}
		if errors.Is(execCtxErr, context.DeadlineExceeded) {
			_, finalizeErr := FinalizeExecutionAttempt(ctx, h.uow, h.ids, h.clk, FinalizeExecutionAttemptRequest{
				RunID: payload.RunID, NodeRunID: payload.NodeRunID, AttemptID: payload.AttemptID, ExpectedVersion: running.Version,
				NextState: runtimedomain.ExecutionAttemptTimedOut, TerminationReason: runtimedomain.TerminationReasonDeadlineExceeded,
				FailureCode: errorcode.CodeTimeout, JobLease: jobLease, CorrelationID: payload.CorrelationID,
			})
			return finalizeErr
		}
		if execCtxErr != nil {
			// Cancelled for a reason this handler does not recognize as a
			// definitive resolution — either our own outer ctx (pool
			// shutdown, heartbeat loss), or V5-08C's own poller below
			// cancelled execCtx but the executor did not return a
			// recognized sentinel/State for it (a generic ports.NodeExecutor
			// that predates V5-08C's own cancellation classification, or
			// any executor that raced and returned its own unrelated error
			// right as the poller fired). Do NOT finalize; leave the
			// Attempt RUNNING. See this file's own package doc comment step
			// 4. AgentNodeExecutor (the real production bridge) never
			// reaches this branch for a genuine poller-triggered
			// cancellation — classifyCancellation always resolves it first,
			// either via ErrAttemptAlreadyTerminated (caught above) or a
			// definitive Cancelled-state result (execErr == nil, handled by
			// the switch below instead).
			return execCtxErr
		}
		if errors.Is(execErr, ErrIndeterminateExecution) {
			// V5-08B's own locked provider-loss mapping (baocaov5checklist.md's
			// own "Quyết định sau review source of truth — 2026-09-08",
			// mapping #4): a lost JobLease/WriteLease mid-execution, or an
			// unconfirmed process-tree quiescence on a mutating attempt, is
			// LOST/INDETERMINATE — neither is a state
			// FinalizeExecutionAttempt's own isFinalizableExecutionAttemptState
			// accepts (by design: no live lease could ever be fenced for
			// that transition). Leave the Attempt RUNNING exactly like the
			// outer-cancellation branch above; internal/app/worker's own
			// crash-recovery/interruption path (V4-13) is the sole durable
			// authority that ever resolves it.
			return execErr
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
	case runtimedomain.ExecutionAttemptCancelled:
		// V5-08C: classifyCancellation's own read-only-attempt branch
		// (agent_node_executor_cancellation.go) returns this State with no
		// execErr — a confirmed-stopped process under a genuine durable
		// cancellation intent, with no side effect ever possible to
		// reconcile. Without this case, nextState fell through to the
		// generic default below, which never overrides the hardcoded
		// ExecutionAttemptFailed default — silently finalizing a cancelled
		// attempt as FAILED.
		nextState = runtimedomain.ExecutionAttemptCancelled
		reason = execResult.TerminationReason
		failureCode = ""
	default:
		if execResult.TerminationReason != "" {
			reason = execResult.TerminationReason
		}
	}
	_, finalizeErr := FinalizeExecutionAttempt(ctx, h.uow, h.ids, h.clk, FinalizeExecutionAttemptRequest{
		RunID: payload.RunID, NodeRunID: payload.NodeRunID, AttemptID: payload.AttemptID, ExpectedVersion: running.Version,
		NextState: nextState, TerminationReason: reason, FailureCode: failureCode, SelectedOutcome: execResult.SelectedOutcome,
		RequestedScopeExpansion: scopeProposal, JobLease: jobLease, CorrelationID: payload.CorrelationID,
		Evidence: execResult.Evidence,
	})
	return finalizeErr
}

// admissionOutcome is admitOrClaimRunning's own three-way result.
type admissionOutcome int

const (
	// admissionNoop: the Attempt was not QUEUED (an earlier delivery of
	// this exact job already claimed it, or the owning Run already
	// committed a cancellation intent) — idempotent no-op, mirrors
	// AdvanceRun/ScheduleExecutableNodeRun's own discipline.
	admissionNoop admissionOutcome = iota
	// admissionBlocked: an admission check failed closed. The Attempt/
	// NodeRun/WorkItemBlocker transition already happened, atomically,
	// inside admitOrClaimRunning's own transaction.
	admissionBlocked
	// admissionRunning: every admission check passed; the Attempt/NodeRun
	// are now RUNNING and Handle's own caller may proceed to the
	// executor.
	admissionRunning
)

// admitOrClaimRunning is V5-08's own admission Phase 2 (see admission.go's
// own package doc comment for the full two-phase design and why): re-load
// the Attempt/Run/NodeRun fresh, re-verify Phase 1's own probe inputs
// haven't shifted, run the two pure-data checks, and atomically CAS to
// either BLOCKED (with the resolved reason) or RUNNING — a single
// transaction decides the outcome, closing the race a separate admission
// transaction followed by an unmodified claim step would otherwise open
// between "admission passed" and QUEUED->RUNNING (confirmed with the user
// before writing this file).
func (h *ExecuteNodeHandler) admitOrClaimRunning(
	ctx context.Context, payload ExecuteNodeJobPayload, jobLease ports.JobLease, profile resolvedExecutionProfileView, probe admissionProbe,
) (runtimedomain.ExecutionAttempt, admissionOutcome, error) {
	var running runtimedomain.ExecutionAttempt
	outcome := admissionNoop
	err := h.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		current, err := tx.Runtime().GetExecutionAttempt(ctx, payload.AttemptID)
		if err != nil {
			return err
		}
		if current.State != runtimedomain.ExecutionAttemptQueued {
			return nil
		}
		// V4-12B's own FIRST worker re-check checkpoint ("trước QUEUED ->
		// RUNNING"), run BEFORE admission (confirmed with the user):
		// decline to act at all if the owning Run already committed a
		// cancellation intent — never race the CANCEL_RUN_COORDINATOR
		// job's own sweep, admission included; that job is the SOLE
		// authority for the QUEUED->CANCELLED transition
		// (TerminationReasonRunCancelledBeforeStart).
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

		// TOCTOU-closing re-verification (ADR-022's own discipline): the
		// drift probe's own input pin must still be the exact row Phase 1
		// measured against. Build rows are immutable/content-addressed
		// and never updated or deleted, so this can only ever re-confirm
		// the same row still exists — it never re-runs the probe's own
		// I/O inside this transaction.
		if profile.AdapterBuild != nil {
			if probe.pinnedBuild == nil || probe.pinnedBuild.ID() != profile.AdapterBuild.BuildID {
				return fmt.Errorf("runtime: admission: node run %s's adapter build probe result does not match its own pin", payload.NodeRunID)
			}
			if _, err := tx.AdapterBuilds().Get(ctx, profile.AdapterBuild.BuildID); err != nil {
				return fmt.Errorf("runtime: admission: re-verify pinned adapter build %s: %w", profile.AdapterBuild.BuildID, err)
			}
		}

		decision, err := evaluateAdmission(ctx, tx, profile, nodeRun, probe)
		if err != nil {
			return err
		}
		if decision.reason != "" {
			if err := h.blockAdmission(ctx, tx, payload, jobLease, run, nodeRun, current, decision); err != nil {
				return err
			}
			outcome = admissionBlocked
			return nil
		}

		// V5-08's own "dựng envelope bất biến": persisted only once
		// admission has fully passed (an envelope for a BLOCKED attempt
		// would never have a reader) — the same "record a durable
		// DecisionArtifact keyed by NodeRunID" pattern schedule.go's own
		// execution-profile artifact already establishes, so a future
		// reader can look this up the identical way.
		if err := recordExecutionEnvelope(ctx, tx, payload.NodeRunID, run.ProjectID, nodeRun); err != nil {
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
		outcome = admissionRunning
		return nil
	})
	return running, outcome, err
}

// blockAdmission is admitOrClaimRunning's own BLOCKED branch: CAS the
// NodeRun QUEUED->BLOCKED alongside the Attempt QUEUED->BLOCKED in this
// same transaction (an admission blocker must never leave one advanced
// and the other stuck QUEUED), open the WorkItem-authority half of the
// block via openWorkItemBlockerTx (the same helper
// requestScopeExpansionTx already uses for the OTHER blocker group), and
// reconcile the Run's own terminality — harmless, almost always a no-op,
// the same "could have removed the Run's last live activation" reasoning
// requestScopeExpansionTx's own final step already documents.
func (h *ExecuteNodeHandler) blockAdmission(
	ctx context.Context, tx ports.Tx, payload ExecuteNodeJobPayload, jobLease ports.JobLease,
	run runtimedomain.WorkflowRun, nodeRun runtimedomain.NodeRun, attempt runtimedomain.ExecutionAttempt, decision admissionDecision,
) error {
	if _, err := tx.Runtime().TransitionNodeRun(ctx, ports.TransitionNodeRunRequest{
		NodeRunID: payload.NodeRunID, ExpectedState: runtimedomain.NodeRunQueued, ExpectedVersion: nodeRun.Version,
		NextState: runtimedomain.NodeRunBlocked,
	}); err != nil {
		return err
	}
	if _, err := tx.Runtime().TransitionExecutionAttempt(ctx, ports.TransitionExecutionAttemptRequest{
		AttemptID: payload.AttemptID, ExpectedState: runtimedomain.ExecutionAttemptQueued, ExpectedVersion: attempt.Version,
		NextState: runtimedomain.ExecutionAttemptBlocked, TerminationReason: decision.reason,
	}); err != nil {
		return err
	}
	blockerID := payload.AttemptID + "-admission-blocker"
	if _, err := openWorkItemBlockerTx(
		ctx, tx, run.ProjectID, string(run.WorkItemID), blockerID, decision.blockerType,
		payload.RunID, payload.NodeRunID, payload.AttemptID, decision.detail, payload.CorrelationID, string(jobLease.JobID),
	); err != nil {
		return err
	}
	version, err := tx.Definitions().GetWorkflowVersion(ctx, string(run.WorkflowVersionID))
	if err != nil {
		return err
	}
	return reconcileRunTerminalityTx(ctx, tx, run, version.Document(), payload.CorrelationID, string(jobLease.JobID))
}

// pollForCancellation is V5-08C's own poller: the piece that actually
// notices a durable RunCancellationIntent (CancelRun, cancel_run.go) WHILE
// h.executor.Execute is running and turns it into real process termination
// by cancelling execCtx — ctx propagation into a real running provider
// process already works end to end (ports.ProcessSupervisor.Run, verified
// during this task's own research; no new plumbing needed there), so this
// goroutine's only job is deciding WHEN to pull that trigger.
//
// pollCtx is deliberately the OUTER job ctx, not execCtx — a poll read
// must survive execCtx being cancelled (that's this goroutine's own doit
// signal, not a reason to stop reading), and must still be bounded by
// something, so the outer ctx (cancelled only by pool shutdown or this
// job's own natural end) is the correct scope.
//
// Runs until execCtx.Done() fires for ANY reason (poller-triggered
// cancel, attemptCtx's own deadline, or Execute simply returning and this
// Handle calling cancelExec() itself) — callers must call cancelExec()
// after Execute returns and receive from pollDone before proceeding, so
// no goroutine is ever leaked past a single Handle call.
func (h *ExecuteNodeHandler) pollForCancellation(
	pollCtx context.Context, runID string, execCtx context.Context, cancelExec context.CancelFunc, done chan<- struct{},
) {
	defer close(done)
	ticker := time.NewTicker(cancellationPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-execCtx.Done():
			return
		case <-ticker.C:
			cancelling, err := h.runIsCancelling(pollCtx, runID)
			if err != nil {
				// Transient read failure (e.g. pool shutdown tearing down
				// the store mid-poll) — never guess; just try again next
				// tick, exactly like every other ambiguous-cause default
				// in this handler.
				continue
			}
			if cancelling {
				cancelExec()
				return
			}
		}
	}
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
// pin.
//
// Audit finding (2026-09-08): this used to stop at snapshot/binding/
// RevisionSet — it never dereferenced a single MessageRef or ResourceRef,
// so a snapshot that itself looked fine but pinned a Message/Resource that
// had since been deleted, tampered with, or never really matched its own
// pinned identity would sail through undetected. Every MessageRef is now
// resolved to a real Message belonging to the SAME WorkItem/Project, whose
// own ContentArtifactID names an ATTACHED Artifact row; every ResourceRef
// is re-verified via loadResourceCandidate (schedule.go, V5-08B0) exactly
// the same way real request assembly does — this function and
// AssembleAgentExecutionRequest deliberately share that one verification
// path rather than maintaining two.
//
// Runs inside its own read-only transaction, closed before Handle's own
// caller ever invokes the executor.
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
		for _, ref := range snapshot.MessageRefs {
			msg, err := tx.Messages().GetMessage(ctx, ref.MessageID)
			if err != nil {
				return fmt.Errorf("%w: snapshot %s message %s: %v", ErrContextSnapshotUnverified, snapshot.ID, ref.MessageID, err)
			}
			if msg.WorkItemID != run.WorkItemID || msg.ProjectID != run.ProjectID {
				return fmt.Errorf("%w: snapshot %s message %s belongs to work item %s / project %s, not %s / %s",
					ErrContextSnapshotUnverified, snapshot.ID, ref.MessageID, msg.WorkItemID, msg.ProjectID, run.WorkItemID, run.ProjectID)
			}
			art, err := tx.Artifacts().GetArtifact(ctx, string(msg.ContentArtifactID))
			if err != nil {
				return fmt.Errorf("%w: snapshot %s message %s content artifact: %v", ErrContextSnapshotUnverified, snapshot.ID, ref.MessageID, err)
			}
			if art.AttachState != artifact.Attached {
				return fmt.Errorf("%w: snapshot %s message %s content artifact is not ATTACHED (state %s)", ErrContextSnapshotUnverified, snapshot.ID, ref.MessageID, art.AttachState)
			}
		}
		for _, ref := range snapshot.ResourceRefs {
			// A pre-V5-08B0 snapshot's own ResourceRef has no OwnerVersionID
			// (contextsnapshot.ResourceRef's own doc comment) — such a
			// snapshot must never dispatch silently.
			if ref.OwnerVersionID == "" {
				return fmt.Errorf("%w: snapshot %s resource %s has no OwnerVersionID (pre-V5-08B0 snapshot)", ErrContextSnapshotUnverified, snapshot.ID, ref.ResourceKey)
			}
			if _, err := loadResourceCandidate(ctx, tx, policy.ResourceRef{OwnerVersionID: ref.OwnerVersionID, ResourceKey: ref.ResourceKey, ContentHash: ref.ContentHash}); err != nil {
				return fmt.Errorf("%w: snapshot %s resource %s: %v", ErrContextSnapshotUnverified, snapshot.ID, ref.ResourceKey, err)
			}
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
