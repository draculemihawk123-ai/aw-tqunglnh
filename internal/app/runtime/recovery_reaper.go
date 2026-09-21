// RecoveryReaperHandler is V4-13's own recovery coordinator
// (docs/design/06-v4-runtime-engine.md, ADR-020): a self-rescheduling
// JobClass=CONTROL job (RecoveryReaperJobKind, mirroring
// SCOPE_EXPANSION_RECONCILE's own PollGeneration self-rescheduling pattern,
// but fenced against the single, global recovery_reaper_state singleton
// row — migration 26 — rather than a per-origin row, since this
// coordinator has no per-Run/per-Attempt origin of its own) that sweeps
// three independent, each-idempotent concerns every time it runs:
//
//  1. Orphaned RUNNING ExecutionAttempts — an Attempt whose own driving
//     EXECUTE_NODE job is no longer actively, provably held by a live
//     worker (ListOrphanedRunningExecutionAttempts, ports.RuntimeRepository).
//     Classified LOST (read-only, never side-effecting) or INDETERMINATE
//     (mutating, workspace reconciled and quarantined if a mutation cannot
//     be ruled out) via worker.ReconcileInterruptedAttempt — the exact
//     SPK-04/SPK-09 primitive this task's own termination.go doc comment
//     names as its job to wire a real caller through, now emitting
//     ADR-020's own TerminationReasonLeaseLost/OwnershipLostMutating
//     (confirmed with the user before making that change — see
//     interruption.go's own doc comment). This primitive is spike-era,
//     built directly against a narrow InterruptionRecoveryStore/
//     WorkspaceReconciler surface (*sqlite.Store satisfies both
//     structurally) rather than the V4 ports.Tx abstraction — this handler
//     is deliberately constructed with BOTH: uow (ports.UnitOfWork, for
//     every V4-native durable-state step) and interruptions/workspaces
//     (for the one spike-era reconciliation step), rather than forcing a
//     premature unification neither this task nor any earlier one owns.
//     Once classified, this handler decides — and, for RETRY, actually
//     performs — exactly one of three next actions (confirmed with the
//     user before writing this file: FRESH_START's own real execution is
//     OUT of this task's scope, see below):
//     - RETRY: retry budget remains and the owning Run is not cancelling —
//     create a new ExecutionAttempt (AttemptNumber+1) and enqueue its own
//     EXECUTE_NODE job (RUN_WORK-classed, the exact shape
//     decideRetryOrExhaustion's own retry branch already uses, finalize.go)
//     immediately (no backoff — this is worker-crash recovery, not a
//     provider failure the pinned AttemptRules' own BackoffSeconds was
//     ever meant to pace).
//     - FRESH_START: the attempt was mutating and its own workspace
//     reconciliation observed a real mutation (quarantined), a usable
//     checkpoint exists to rebuild from, AND retry budget remains (V5-13's
//     own budget contract, 2026-09-11, confirmed with the user verbatim:
//     RETRY and FRESH_START share ONE AttemptNumber/MaxAttempts counter,
//     never two separate budgets — see decideRecoveryNextAction below) —
//     this handler records a durable recovery decision (DecisionArtifact,
//     Kind=RecoveryDecisionKind) naming the interrupted AttemptID, the
//     latest Checkpoint/ContextSnapshot a fresh execution would need,
//     this sweep's own recovery generation, the failure reason and the
//     pinned policy/budget basis — and stops there. It never itself
//     creates a new ExecutionAttempt/job for this case, and never calls
//     worker.StartFreshFromLatestCheckpoint (a real ports.AgentExecutor.Start
//     call — real process spawn — which a JobClass=CONTROL handler must
//     never perform: that would let a CONTROL job carry real workload with
//     no RunID/JobLease/cancel_epoch of its own, breaking the exact
//     cancellation/fencing contract V4-12B/V4-12C built). V5-13 is the task
//     that consumes this decision and performs the real three-phase
//     execution (Tx reserve Attempt+RUN_WORK job -> real call outside any
//     transaction -> Tx finalize with fencing).
//     - ESCALATE: retry/fresh-start budget is exhausted (reason
//     RecoveryReasonAttemptsExhausted — a NEW replacement Attempt would
//     exceed MaxAttempts, checked identically for both the mutating and
//     non-mutating branch), the Run is cancelling, or no checkpoint exists
//     to build a FRESH_START decision from — records the identical kind of
//     DecisionArtifact, NextAction=ESCALATE, never a blind retry.
//  2. Stranded RunCancellationIntent rows still REQUESTED whose own Run has
//     not yet reached CANCELLED — re-invokes CancelRunCoordinatorHandler's
//     own already-idempotent Handle for each (cancel_run_coordinator.go);
//     a coordinator that died mid-sweep is resumed by simply re-running the
//     identical sweep, never by inventing a second resume-specific code
//     path.
//  3. Stranded WorkItemCancellationIntent rows still REQUESTED — re-runs
//     reconcileWorkItemCancellationTx (cancel_work_item.go) for each, in its
//     own transaction: a no-op unless every one of that WorkItem's own Runs
//     has since reached terminal, in which case it finally closes the
//     WorkItem out.
//
// Every one of these three sweeps is independently idempotent and
// independently transacted — this handler does not wrap the whole sweep in
// one giant transaction, the same "several independently-idempotent
// operations, not one all-or-nothing unit" discipline CancelRunCoordinatorHandler's
// own sweep already established for its own, narrower scope.
package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/worker"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
	"github.com/taQuangLing/agent-workflow/internal/domain/contextsnapshot"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// RecoveryReaperJobKind is the durable, self-rescheduling CONTROL job this
// file's own handler serves (V4-13) — joined to durable_jobs.job_class's own
// CONTROL allow-list in migration 25, per ADR-020's own allow-list table.
const RecoveryReaperJobKind = "RECOVERY_REAPER"

const defaultRecoveryReaperJobMaxClaims = 10

// RecoveryReaperJobPayload is the exact JSON shape every RECOVERY_REAPER job
// carries — Generation names the exact recovery_reaper_state generation this
// job instance is entitled to advance from, the same fencing role
// ScopeExpansionReconcileJobPayload.PollGeneration already plays for that
// job's own self-rescheduling chain.
type RecoveryReaperJobPayload struct {
	Generation    uint64 `json:"generation"`
	CorrelationID string `json:"correlationId,omitempty"`
}

// RecoveryDecisionKind is the DecisionArtifact.Kind this file's own ESCALATE/
// FRESH_START recordings use (HE-03-M08's own durable decision ledger).
const RecoveryDecisionKind = "RecoveryDecision"

// RecoveryNextAction is the closed, four-valued "next diagnostic action"
// HE-01-M05 requires every failure record name (V4-13, confirmed with the
// user before writing this file) — directly the same four verbs V4-13's own
// Mục tiêu line names (retry/fresh-Start/reconcile/escalate). RECONCILE
// itself is not a separately-recorded NextAction value here: it describes
// what worker.ReconcileInterruptedAttempt already always does as part of
// classifying a mutating attempt (workspace reconciliation), not a fourth
// alternative branch alongside RETRY/FRESH_START/ESCALATE — every mutating
// attempt is reconciled, and RETRY/FRESH_START/ESCALATE is then decided from
// that reconciliation's own verdict.
type RecoveryNextAction string

const (
	RecoveryActionRetry      RecoveryNextAction = "RETRY"
	RecoveryActionFreshStart RecoveryNextAction = "FRESH_START"
	RecoveryActionEscalate   RecoveryNextAction = "ESCALATE"
)

const (
	RecoveryDecisionRecordedEventType     = "RECOVERY_DECISION_RECORDED"
	RecoveryDecisionRecordedSchemaVersion = 1
)

// recoveryDecisionRecordedEventPayload is RECOVERY_DECISION_RECORDED's own
// JSON shape (V4-13) — appended alongside every ESCALATE/FRESH_START
// DecisionArtifact this handler ever records (RETRY gets no such event,
// mirroring decideRetryOrExhaustion's own retry branch, which appends
// nothing beyond the routine EXECUTION_ATTEMPT_FINALIZED already covers).
type recoveryDecisionRecordedEventPayload struct {
	AttemptID  string `json:"attemptId"`
	NodeRunID  string `json:"nodeRunId"`
	RunID      string `json:"runId"`
	WorkItemID string `json:"workItemId"`
	NextAction string `json:"nextAction"`
	Reason     string `json:"reason"`
}

// recoveryDecisionInput/recoveryDecisionResult are the RecoveryDecisionKind
// DecisionArtifact's own Input/Result JSON shapes.
type recoveryDecisionInput struct {
	AttemptID          string `json:"attemptId"`
	NodeRunID          string `json:"nodeRunId"`
	RunID              string `json:"runId"`
	TerminationReason  string `json:"terminationReason"`
	Reconciliation     string `json:"reconciliation,omitempty"`
	RecoveryGeneration uint64 `json:"recoveryGeneration"`
}

type recoveryDecisionResult struct {
	NextAction        string `json:"nextAction"`
	Reason            string `json:"reason"`
	CheckpointID      string `json:"checkpointId,omitempty"`
	ContextSnapshotID string `json:"contextSnapshotId,omitempty"`
	PolicyVersionID   string `json:"policyVersionId,omitempty"`
	AttemptsUsed      uint32 `json:"attemptsUsed"`
	MaxAttempts       uint32 `json:"maxAttempts"`
}

// RecoveryReaperHandler is a ready-to-register workerpool.Handler for
// RecoveryReaperJobKind. See this file's own package doc comment for the
// full three-sweep design.
type RecoveryReaperHandler struct {
	uow           ports.UnitOfWork
	ids           idsource.Source
	clk           clock.Clock
	interruptions worker.InterruptionRecoveryStore
	workspaces    worker.WorkspaceReconciler
	recovery      worker.RecoveryStore
	// leaseGrace is added to "now" before comparing against a driving job's
	// own lease_until when listing orphaned attempts — a small, deliberate
	// safety margin so a worker whose lease is about to, but has not yet,
	// expire is never mistaken for orphaned out from under it. Defaults to
	// zero (no grace) via NewRecoveryReaperHandler.
	leaseGrace time.Duration
	// interval is how long the self-rescheduled successor job waits before a
	// worker may claim it. Zero (the default) keeps the historical behaviour -
	// the successor is claimable at once - which tests that drive the loop by
	// hand rely on; a real worker composition must set it (see
	// DefaultRecoveryReaperInterval), otherwise an idle worker re-runs the
	// reaper hundreds of times per second.
	interval time.Duration
}

// DefaultRecoveryReaperInterval is the pause a production worker leaves
// between two reaper passes: short enough that a crashed worker's orphaned
// attempts are recovered promptly (worker leases default to 30s), long enough
// that an idle installation does not spin.
const DefaultRecoveryReaperInterval = 5 * time.Second

// RecoveryReaperOption customizes NewRecoveryReaperHandler.
type RecoveryReaperOption func(*RecoveryReaperHandler)

// WithRecoveryReaperInterval sets how long the next reaper pass waits after
// this one.
func WithRecoveryReaperInterval(interval time.Duration) RecoveryReaperOption {
	return func(h *RecoveryReaperHandler) { h.interval = interval }
}

// NewRecoveryReaperHandler returns a ready-to-register RecoveryReaperHandler.
func NewRecoveryReaperHandler(
	uow ports.UnitOfWork, ids idsource.Source, clk clock.Clock,
	interruptions worker.InterruptionRecoveryStore, workspaces worker.WorkspaceReconciler, recovery worker.RecoveryStore,
	options ...RecoveryReaperOption,
) *RecoveryReaperHandler {
	h := &RecoveryReaperHandler{uow: uow, ids: ids, clk: clk, interruptions: interruptions, workspaces: workspaces, recovery: recovery}
	for _, option := range options {
		option(h)
	}
	return h
}

var _ workerpool.Handler = (*RecoveryReaperHandler)(nil)

// StartupRecoveryScan enqueues the first RECOVERY_REAPER job for the
// current recovery_reaper_state generation, if one is not already pending —
// EnqueueJob's own established idempotent-insert-or-return-existing
// contract (IdempotencyKey "recovery-reaper:<generation>") is what actually
// guarantees two racing callers (e.g. two coordinators starting up at once)
// only ever produce one job for the same generation, exactly the "hai
// coordinator tranh recovery vẫn chỉ tạo một recovery job cho generation"
// requirement this task's own Verify line names.
func StartupRecoveryScan(ctx context.Context, uow ports.UnitOfWork, ids idsource.Source) error {
	return uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		state, err := tx.Runtime().GetRecoveryReaperState(ctx)
		if err != nil {
			return err
		}
		return enqueueRecoveryReaperJobTx(ctx, tx, ids, state.Generation, "", time.Time{})
	})
}

// enqueueRecoveryReaperJobTx enqueues the RECOVERY_REAPER job for generation.
// availableAt is the earliest time a worker may claim it; the zero time means
// "immediately" (the very first job, at worker startup, and hand-driven tests).
func enqueueRecoveryReaperJobTx(ctx context.Context, tx ports.Tx, ids idsource.Source, generation uint64, correlationID string, availableAt time.Time) error {
	payload, err := json.Marshal(RecoveryReaperJobPayload{Generation: generation, CorrelationID: correlationID})
	if err != nil {
		return fmt.Errorf("marshal %s job payload: %w", RecoveryReaperJobKind, err)
	}
	_, err = tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: ports.JobID(ids.NewID()), Kind: RecoveryReaperJobKind,
		AggregateType: "RecoveryReaper", AggregateID: "singleton", Payload: payload,
		AvailableAt: availableAt,
		MaxClaims:   defaultRecoveryReaperJobMaxClaims, IdempotencyKey: fmt.Sprintf("recovery-reaper:%d", generation),
	})
	if errors.Is(err, ports.ErrPersistenceAlreadyExists) {
		// Another coordinator already enqueued this exact generation's own
		// job — idempotent no-op, never a second job for the same
		// generation.
		return nil
	}
	return err
}

// Handle implements workerpool.Handler for RecoveryReaperJobKind. See this
// file's own package doc comment for the full three-sweep design.
func (h *RecoveryReaperHandler) Handle(ctx context.Context, job ports.DurableJob) error {
	var payload RecoveryReaperJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("runtime: unmarshal %s job %s payload: %w", RecoveryReaperJobKind, job.ID, err)
	}

	if err := h.sweepOrphanedAttempts(ctx, payload); err != nil {
		return err
	}
	if err := h.sweepStrandedRunIntents(ctx, payload.CorrelationID); err != nil {
		return err
	}
	if err := h.sweepStrandedWorkItemIntents(ctx, payload.CorrelationID); err != nil {
		return err
	}

	return h.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		state, err := tx.Runtime().GetRecoveryReaperState(ctx)
		if err != nil {
			return err
		}
		if state.Generation != payload.Generation {
			// A later generation already advanced past this one (this exact
			// job was redelivered after a successor already ran) — no-op,
			// never regress or re-enqueue a stale successor.
			return nil
		}
		advanced, err := tx.Runtime().AdvanceRecoveryReaperGeneration(ctx, ports.AdvanceRecoveryReaperGenerationRequest{
			ExpectedGeneration: state.Generation, ExpectedVersion: state.Version,
		})
		if err != nil {
			return err
		}
		var availableAt time.Time
		if h.interval > 0 {
			availableAt = h.clk.Now().Add(h.interval)
		}
		return enqueueRecoveryReaperJobTx(ctx, tx, h.ids, advanced.Generation, payload.CorrelationID, availableAt)
	})
}

// sweepStrandedRunIntents resumes every RunCancellationIntent still
// REQUESTED whose own Run has not yet reached CANCELLED, by re-invoking
// CancelRunCoordinatorHandler's own already-idempotent Handle.
func (h *RecoveryReaperHandler) sweepStrandedRunIntents(ctx context.Context, correlationID string) error {
	var intents []runtimedomain.RunCancellationIntent
	if err := h.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		intents, err = tx.Runtime().ListRunCancellationIntentsByState(ctx, runtimedomain.CancellationIntentRequested)
		return err
	}); err != nil {
		return err
	}

	coordinator := NewCancelRunCoordinatorHandler(h.uow, h.ids)
	for _, intent := range intents {
		var run runtimedomain.WorkflowRun
		if err := h.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
			var err error
			run, err = tx.Runtime().GetWorkflowRun(ctx, string(intent.RunID))
			return err
		}); err != nil {
			return err
		}
		if run.State == runtimedomain.WorkflowRunCancelled {
			continue
		}
		jobPayload, err := json.Marshal(CancelRunCoordinatorJobPayload{RunID: string(intent.RunID), CorrelationID: correlationID})
		if err != nil {
			return fmt.Errorf("marshal %s job payload: %w", CancelRunCoordinatorJobKind, err)
		}
		if err := coordinator.Handle(ctx, ports.DurableJob{
			ID: ports.JobID(h.ids.NewID()), AggregateType: "WorkflowRun", AggregateID: string(intent.RunID), Payload: jobPayload,
		}); err != nil {
			return fmt.Errorf("resume stranded run cancellation intent for run %s: %w", intent.RunID, err)
		}
	}
	return nil
}

// sweepStrandedWorkItemIntents resumes every WorkItemCancellationIntent
// still REQUESTED by re-running reconcileWorkItemCancellationTx — a no-op
// unless every one of that WorkItem's own Runs has since reached terminal.
func (h *RecoveryReaperHandler) sweepStrandedWorkItemIntents(ctx context.Context, correlationID string) error {
	var intents []runtimedomain.WorkItemCancellationIntent
	if err := h.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		intents, err = tx.Runtime().ListWorkItemCancellationIntentsByState(ctx, runtimedomain.CancellationIntentRequested)
		return err
	}); err != nil {
		return err
	}
	for _, intent := range intents {
		workItemID := string(intent.WorkItemID)
		if err := h.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
			return reconcileWorkItemCancellationTx(ctx, tx, workItemID, correlationID, "")
		}); err != nil {
			return fmt.Errorf("resume stranded work item cancellation intent for work item %s: %w", workItemID, err)
		}
	}
	return nil
}

// sweepOrphanedAttempts finds and classifies every orphaned RUNNING
// ExecutionAttempt, then decides — and, for RETRY, performs — its own next
// action. See this file's own package doc comment for the full decision
// matrix.
func (h *RecoveryReaperHandler) sweepOrphanedAttempts(ctx context.Context, payload RecoveryReaperJobPayload) error {
	var orphaned []runtimedomain.ExecutionAttempt
	if err := h.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		orphaned, err = tx.Runtime().ListOrphanedRunningExecutionAttempts(ctx, h.clk.Now().Add(-h.leaseGrace))
		return err
	}); err != nil {
		return err
	}

	for _, attempt := range orphaned {
		if err := h.recoverOneAttempt(ctx, attempt, payload); err != nil {
			return fmt.Errorf("recover orphaned execution attempt %s: %w", attempt.ID, err)
		}
	}
	return nil
}

func (h *RecoveryReaperHandler) recoverOneAttempt(ctx context.Context, attempt runtimedomain.ExecutionAttempt, payload RecoveryReaperJobPayload) error {
	// Resolve the one RepositoryWorkspace this attempt's own write lease (if
	// any) names, and the exact revision it was pinned to (the workspace's
	// own BaseRevision as of its current generation) plus the workspace's
	// own current Version (needed only for the quarantine CAS, and only
	// ever consulted if classification below turns out mutating) — before
	// classifying, since ReconcileInterruptedAttempt needs both supplied
	// upfront and this handler cannot know the classification outcome in
	// advance without duplicating the check it already performs internally.
	var repositoryWorkspaceID, pinnedRevision string
	var workspaceVersion uint64
	if err := h.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		id, found, err := tx.Runtime().GetWriteLeaseRepositoryWorkspaceForAttempt(ctx, string(attempt.ID))
		if err != nil || !found {
			return err
		}
		record, err := tx.Work().GetRepositoryWorkspaceByID(ctx, id)
		if err != nil {
			return err
		}
		repositoryWorkspaceID = id
		pinnedRevision = record.Workspace.BaseRevision
		workspaceVersion = record.Workspace.Version
		return nil
	}); err != nil {
		return err
	}

	// V5-15D (2026-09-12, the user's own binding fix contract, verbatim):
	// "Recovery reaper cũng phải gọi cùng cancellation-finalization path
	// khi orphaned Attempt thuộc một Run đang CANCELLING" — a genuinely
	// mutating orphaned Attempt (repositoryWorkspaceID != "" above) whose
	// own owning Run is already CANCELLING/CANCELLED must go through the
	// SAME real, atomic cancellation-finalization boundary the live V5-08C
	// poller path uses (agent_node_executor_cancellation.go's own
	// cancelMutatingAttemptTx), never the RETRY/FRESH_START/ESCALATE
	// decision below — that path only ever records a DecisionArtifact and
	// never itself lets the owning NodeRun (let alone WorkflowRun) reach a
	// terminal state, so a Run whose own live cancellation poller happened
	// to crash before finalizing would otherwise stay stuck in CANCELLING
	// forever even after the crash-recovery reaper picks it up. A
	// non-mutating (read-only) orphaned Attempt is unaffected by this
	// check at all — LOST already routes through decideRecoveryNextAction's
	// own runCancelling-aware ESCALATE branch below correctly, since a
	// read-only Attempt was never a live NodeRun blocker to begin with.
	if repositoryWorkspaceID != "" {
		var run runtimedomain.WorkflowRun
		if err := h.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
			nodeRun, err := tx.Runtime().GetNodeRun(ctx, string(attempt.NodeRunID))
			if err != nil {
				return err
			}
			run, err = tx.Runtime().GetWorkflowRun(ctx, string(nodeRun.RunID))
			return err
		}); err != nil {
			return err
		}
		if run.State == runtimedomain.WorkflowRunCancelling || run.State == runtimedomain.WorkflowRunCancelled {
			return h.finalizeCancelledMutatingOrphan(ctx, attempt, string(run.ID), repositoryWorkspaceID, pinnedRevision, workspaceVersion)
		}
	}

	eventID := string(attempt.ID) + "-recovery-terminated-gen-" + fmt.Sprint(payload.Generation)
	quarantineEventID := string(attempt.ID) + "-recovery-quarantine-gen-" + fmt.Sprint(payload.Generation)
	recovery, err := worker.ReconcileInterruptedAttempt(ctx, h.interruptions, h.workspaces, worker.InterruptedAttemptRecoveryRequest{
		AttemptID: attempt.ID, ExpectedAttemptVersion: attempt.Version,
		TerminationEventID: eventID, CorrelationID: payload.CorrelationID, OccurredAt: time.Now().UTC(),
		RepositoryWorkspaceID: repositoryWorkspaceID, PinnedRevision: pinnedRevision,
		ExpectedWorkspaceVersion: workspaceVersion, QuarantineEventID: quarantineEventID,
	})
	if err != nil {
		if errors.Is(err, ports.ErrOptimisticConflict) {
			// Already terminated by an earlier winner (a duplicate reaper
			// delivery, or the Attempt's own real finalize arrived first) —
			// idempotent no-op.
			return nil
		}
		return err
	}

	var nodeRun runtimedomain.NodeRun
	var run runtimedomain.WorkflowRun
	var attemptRules policyAttemptRules
	var policyVersionID string
	if err := h.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		nodeRun, err = tx.Runtime().GetNodeRun(ctx, string(attempt.NodeRunID))
		if err != nil {
			return err
		}
		run, err = tx.Runtime().GetWorkflowRun(ctx, string(nodeRun.RunID))
		if err != nil {
			return err
		}
		rules, versionID, err := resolvePinnedAttemptRules(ctx, tx, string(attempt.NodeRunID))
		if err != nil {
			return err
		}
		attemptRules = policyAttemptRules{MaxAttempts: rules.MaxAttempts}
		policyVersionID = versionID
		return nil
	}); err != nil {
		return err
	}

	runCancelling := run.State == runtimedomain.WorkflowRunCancelling || run.State == runtimedomain.WorkflowRunCancelled
	budgetRemains := uint32(attempt.AttemptNumber) < attemptRules.MaxAttempts
	mutationObserved := recovery.NextState == runtimedomain.ExecutionAttemptIndeterminate && recovery.Reconciliation == worker.ReconciliationMutationObserved
	// hasUsableCheckpoint does real I/O — only ever consulted when a
	// mutation was actually observed (Go's && short-circuits), the one
	// case that can lead to FRESH_START at all.
	hasCheckpoint := mutationObserved && h.hasUsableCheckpoint(ctx, attempt.ID)

	action, reason := decideRecoveryNextAction(mutationObserved, hasCheckpoint, runCancelling, budgetRemains, string(recovery.Reason))
	switch action {
	case RecoveryActionRetry:
		return h.retryAttempt(ctx, attempt, nodeRun, run)
	case RecoveryActionFreshStart:
		return h.consumeFreshStart(ctx, attempt, nodeRun, run, payload, reason, policyVersionID, attemptRules)
	default:
		return h.recordDecision(ctx, attempt, nodeRun, run, payload, action, reason, policyVersionID, attempt, attemptRules)
	}
}

// finalizeCancelledMutatingOrphan is V5-15D's own real production fix
// (2026-09-12, the user's own binding fix contract): recoverOneAttempt's
// own early routing above calls this INSTEAD of the RETRY/FRESH_START/
// ESCALATE decision whenever the orphaned Attempt is genuinely mutating and
// its own owning Run is already CANCELLING/CANCELLED. It computes this real
// mount's own reconciliation verdict via h.workspaces
// (worker.WorkspaceReconciler's own narrower, ID-based
// LoadRepositoryWorkspaceRevision — this handler's own established real
// capability, never ports.WorkspaceProvider, which it was never
// constructed with and does not need to be: see
// agent_node_executor_cancellation.go's own cancelledMutatingMountVerdict
// doc comment for why the shared atomic core only ever needs an
// already-computed verdict, not a specific revision-capture mechanism), then
// hands off to the SAME real cancelMutatingAttemptTx the live V5-08C poller
// path uses — CAS Attempt->INDETERMINATE, quarantine if a mutation was
// observed, CAS NodeRun->CANCELLED, terminalize its own BranchToken,
// reconcile Run terminality (the real path to WorkflowRun->CANCELLED once
// LiveCount==0), and defensively complete the cancellation intent.
func (h *RecoveryReaperHandler) finalizeCancelledMutatingOrphan(
	ctx context.Context, attempt runtimedomain.ExecutionAttempt, runID, repositoryWorkspaceID, pinnedRevision string, workspaceVersion uint64,
) error {
	// context.WithoutCancel mirrors finalizeMutatingCancellation's own
	// identical discipline (agent_node_executor_cancellation.go) — this
	// job's own ctx is not expected to be cancelled here (RECOVERY_REAPER
	// is its own independent CONTROL job, not sharing execCtx with
	// whatever live Attempt originally ran), but cancelMutatingAttemptTx's
	// own contract documents that it expects an already-uncancelable ctx,
	// so this satisfies that unconditionally rather than relying on the
	// caller's own ctx happening to never be cancelled.
	cleanupCtx := context.WithoutCancel(ctx)
	currentRevision, err := h.workspaces.LoadRepositoryWorkspaceRevision(cleanupCtx, repositoryWorkspaceID)
	if err != nil {
		return fmt.Errorf("runtime: load current revision for repository workspace %s during recovery cancellation finalization: %w", repositoryWorkspaceID, err)
	}
	verdict, err := worker.ReconcileMutatingAttempt(pinnedRevision, currentRevision)
	if err != nil {
		return fmt.Errorf("runtime: reconcile orphaned cancelling mutating attempt %s: %w", attempt.ID, err)
	}
	return cancelMutatingAttemptTx(cleanupCtx, h.uow, h.ids, string(attempt.ID), string(attempt.NodeRunID), runID, []cancelledMutatingMountVerdict{{
		repositoryWorkspaceID: workspace.RepositoryWorkspaceID(repositoryWorkspaceID), workspaceVersion: workspaceVersion,
		mutationObserved: verdict == worker.ReconciliationMutationObserved,
	}})
}

// RecoveryReasonAttemptsExhausted is a typed override for
// recoveryDecisionResult.Reason (2026-09-11, V5-13's own budget contract,
// confirmed with the user verbatim before implementing it): whenever
// creating a replacement Attempt would exceed the pinned
// AttemptRules.MaxAttempts, this handler ESCALATEs instead — for either
// the mutating (would-be FRESH_START) or non-mutating (would-be RETRY)
// branch alike — and names this specific reason rather than whatever
// ReconcileInterruptedAttempt's own classification Reason says (that
// describes why the ORIGINAL attempt died, e.g. LEASE_LOST, not why
// recovery cannot proceed with a new one).
const RecoveryReasonAttemptsExhausted = "RECOVERY_ATTEMPTS_EXHAUSTED"

// decideRecoveryNextAction is recoverOneAttempt's own pure decision core —
// extracted as a free, side-effect-free function (no ctx/uow) so every
// branch of this decision matrix is directly unit-testable without a real
// database or a real observed mutation. budgetRemains gates BOTH the
// mutating (FRESH_START-eligible) and non-mutating (RETRY-eligible)
// branches IDENTICALLY — RETRY and FRESH_START share one AttemptNumber/
// MaxAttempts counter, never two separate budgets a caller could use to
// dodge the limit (2026-09-11, V5-13's own budget contract, confirmed
// with the user verbatim: "RETRY và FRESH_START dùng chung một
// MaxAttempts; không có hai ngân sách để lách giới hạn").
func decideRecoveryNextAction(mutationObserved, hasUsableCheckpoint, runCancelling, budgetRemains bool, classificationReason string) (action RecoveryNextAction, reason string) {
	if mutationObserved {
		// A confirmed mutation was observed — never retry in place against a
		// now-QUARANTINED workspace.
		if !budgetRemains {
			return RecoveryActionEscalate, RecoveryReasonAttemptsExhausted
		}
		if hasUsableCheckpoint {
			return RecoveryActionFreshStart, classificationReason
		}
		// No usable basis for FRESH_START either.
		return RecoveryActionEscalate, classificationReason
	}
	if !runCancelling && budgetRemains {
		return RecoveryActionRetry, classificationReason
	}
	if !budgetRemains {
		return RecoveryActionEscalate, RecoveryReasonAttemptsExhausted
	}
	return RecoveryActionEscalate, classificationReason
}

// policyAttemptRules is the narrow slice of policy.AttemptRules this file
// actually needs.
type policyAttemptRules struct {
	MaxAttempts uint32
}

// hasUsableCheckpoint reports whether attemptID has a real, loadable latest
// Checkpoint — the minimum basis a FRESH_START decision needs to name a real
// Checkpoint/ContextSnapshot pair. Any error (none exists, the store is
// unreachable) is treated as "no", falling back to ESCALATE — never a
// FRESH_START decision recorded with nothing real behind it.
//
// Confirmed bug fix (2026-09-11, V5-13 PR1): this used to verify the
// checkpoint's own ContextSnapshotID via h.recovery.LoadContextSnapshot —
// worker.RecoveryStore's own LEGACY lookup, which queries the pre-V5-04
// context_snapshots table (internal/domain/runtime.ContextSnapshot).
// Nothing in this codebase's real execution path has EVER written a row
// there (grepped internal/adapters/sqlite: only context_store_test.go and
// crashworker_fixtures.go's own spike fixtures do) — every REAL Checkpoint
// a live Attempt's own sink writes instead stores the real V5
// contextsnapshot.Snapshot's own ID (agent_node_executor.go:
// `ContextSnapshotID: string(request.ContextSnapshot.ID)`), just re-typed
// through the legacy Go type. The stored value was always correct; only
// the lookup table was wrong — meaning this check ALWAYS failed for any
// real orphaned mutating Attempt, so FRESH_START was structurally
// unreachable in production (every real case silently fell through to
// ESCALATE instead). Fixed by verifying against the REAL
// contextsnapshot repository instead of the legacy RecoveryStore
// interface.
func (h *RecoveryReaperHandler) hasUsableCheckpoint(ctx context.Context, attemptID runtimedomain.ExecutionAttemptID) bool {
	if h.recovery == nil {
		return false
	}
	checkpoint, err := h.recovery.LoadLatestCheckpoint(ctx, attemptID)
	if err != nil || checkpoint.ContextSnapshotID == "" {
		return false
	}
	var found bool
	if err := h.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		_, snapshotErr := tx.ContextSnapshots().GetSnapshot(ctx, string(checkpoint.ContextSnapshotID))
		found = snapshotErr == nil
		return nil
	}); err != nil {
		return false
	}
	return found
}

// retryAttempt creates a new ExecutionAttempt (AttemptNumber+1) and enqueues
// its own EXECUTE_NODE job — the exact shape decideRetryOrExhaustion's own
// retry branch already uses (finalize.go), run immediately (no backoff:
// this is worker-crash recovery, not a provider failure the pinned
// AttemptRules' own BackoffSeconds was ever meant to pace).
func (h *RecoveryReaperHandler) retryAttempt(ctx context.Context, attempt runtimedomain.ExecutionAttempt, nodeRun runtimedomain.NodeRun, run runtimedomain.WorkflowRun) error {
	return h.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		// Re-fetch fresh: another winner (a duplicate reaper delivery, or
		// the Attempt's own real finalize) may already have advanced this
		// NodeRun since the read-only classification pass above.
		current, err := tx.Runtime().GetNodeRun(ctx, string(nodeRun.ID))
		if err != nil {
			return err
		}
		if current.State != runtimedomain.NodeRunRunning {
			// Already moved on — idempotent no-op.
			return nil
		}
		nextAttemptID := h.ids.NewID()
		nextAttempt, err := runtimedomain.NewExecutionAttempt(
			runtimedomain.ExecutionAttemptID(nextAttemptID), attempt.NodeRunID, attempt.AttemptNumber+1,
			attempt.ExecutionProfileHash, attempt.ProviderKey, attempt.InputRevisionSet,
		)
		if err != nil {
			return err
		}

		// V5-04: mirrors decideRetryOrExhaustion's own clone-on-retry
		// (finalize.go) — a recovery-driven retry attempt needs its own
		// bound snapshot exactly as much as a technical-retry one does;
		// without this, ExecuteNodeHandler's own dispatch-time
		// verification would reject every recovery retry forever (no
		// bound snapshot to verify), livelocking the exact orphaned
		// attempt this reaper exists to unstick. Gracefully skipped when
		// the attempt being retried itself has no bound snapshot (a
		// pre-V5-04-shaped attempt).
		var clonedSnapshot contextsnapshot.Snapshot
		haveClone := false
		previousSnapshot, err := tx.ContextSnapshots().GetSnapshotByAttemptID(ctx, string(attempt.ID))
		if err != nil && !errors.Is(err, ports.ErrPersistenceNotFound) {
			return err
		}
		if err == nil {
			nextSnapshotID := contextsnapshot.ID(h.ids.NewID())
			clonedSnapshot, err = contextsnapshot.NewSnapshot(
				nextSnapshotID, previousSnapshot.ProjectID, previousSnapshot.WorkItemID, contextsnapshot.AttemptID(nextAttemptID),
				previousSnapshot.MessageRefs, previousSnapshot.ResourceRefs, previousSnapshot.EvidenceRefs, previousSnapshot.Revisions, h.clk.Now(),
			)
			if err != nil {
				return err
			}
			nextAttempt.ContextSnapshotID = &nextSnapshotID
			haveClone = true
		}

		if _, err := tx.Runtime().CreateExecutionAttempt(ctx, nextAttempt); err != nil {
			return err
		}
		if haveClone {
			if _, err := tx.ContextSnapshots().CreateSnapshot(ctx, clonedSnapshot); err != nil {
				return err
			}
		}
		jobPayload, err := json.Marshal(ExecuteNodeJobPayload{RunID: string(run.ID), NodeRunID: string(nodeRun.ID), AttemptID: nextAttemptID})
		if err != nil {
			return fmt.Errorf("marshal %s retry job payload: %w", ExecuteNodeJobKind, err)
		}
		_, err = tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
			ID: ports.JobID(h.ids.NewID()), ProjectID: run.ProjectID, Kind: ExecuteNodeJobKind, RunID: string(run.ID),
			AggregateType: "ExecutionAttempt", AggregateID: nextAttemptID, Payload: jobPayload,
			MaxClaims: defaultExecuteNodeJobMaxClaims, IdempotencyKey: "execute-" + nextAttemptID,
		})
		return err
	})
}

// recordDecision persists a RecoveryDecisionKind DecisionArtifact plus its
// own RECOVERY_DECISION_RECORDED event — ESCALATE's own closing step.
// FRESH_START no longer goes through this function (see consumeFreshStart
// below, 2026-09-11 V5-13 PR2): a FRESH_START decision must be recorded
// ATOMICALLY with reserving its own replacement Attempt/Snapshot/job, not
// as a separate, later transaction — writeRecoveryDecisionArtifactTx is
// the shared body both this function and consumeFreshStart call.
func (h *RecoveryReaperHandler) recordDecision(
	ctx context.Context, attempt runtimedomain.ExecutionAttempt, nodeRun runtimedomain.NodeRun, run runtimedomain.WorkflowRun,
	payload RecoveryReaperJobPayload, action RecoveryNextAction, reason, policyVersionID string,
	sourceAttempt runtimedomain.ExecutionAttempt, attemptRules policyAttemptRules,
) error {
	return h.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		return writeRecoveryDecisionArtifactTx(ctx, tx, attempt, nodeRun, run, payload, action, reason, policyVersionID, sourceAttempt, attemptRules, nil)
	})
}

// recoveryCheckpointRef is the already-resolved Checkpoint/ContextSnapshot
// pair a FRESH_START decision names — resolved once by consumeFreshStart
// (which also needs the real Checkpoint to clone a Snapshot from) and
// threaded straight into writeRecoveryDecisionArtifactTx, rather than that
// shared function re-loading it a second time in the same transaction.
type recoveryCheckpointRef struct {
	checkpointID      string
	contextSnapshotID string
}

// writeRecoveryDecisionArtifactTx is recordDecision/consumeFreshStart's own
// shared closing step — persists one RecoveryDecisionKind DecisionArtifact
// plus its own RECOVERY_DECISION_RECORDED event. checkpointRef is nil for
// ESCALATE (nothing to name) and populated for FRESH_START.
func writeRecoveryDecisionArtifactTx(
	ctx context.Context, tx ports.Tx, attempt runtimedomain.ExecutionAttempt, nodeRun runtimedomain.NodeRun, run runtimedomain.WorkflowRun,
	payload RecoveryReaperJobPayload, action RecoveryNextAction, reason, policyVersionID string,
	sourceAttempt runtimedomain.ExecutionAttempt, attemptRules policyAttemptRules, checkpointRef *recoveryCheckpointRef,
) error {
	result := recoveryDecisionResult{
		NextAction: string(action), Reason: reason, PolicyVersionID: policyVersionID,
		AttemptsUsed: uint32(sourceAttempt.AttemptNumber), MaxAttempts: attemptRules.MaxAttempts,
	}
	if checkpointRef != nil {
		result.CheckpointID = checkpointRef.checkpointID
		result.ContextSnapshotID = checkpointRef.contextSnapshotID
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal recovery decision result: %w", err)
	}
	inputJSON, err := json.Marshal(recoveryDecisionInput{
		AttemptID: string(attempt.ID), NodeRunID: string(nodeRun.ID), RunID: string(run.ID),
		TerminationReason: reason, RecoveryGeneration: payload.Generation,
	})
	if err != nil {
		return fmt.Errorf("marshal recovery decision input: %w", err)
	}
	artifactPolicyVersion := policyVersionID
	if artifactPolicyVersion == "" {
		artifactPolicyVersion = "none"
	}
	artifact, err := runtimedomain.NewDecisionArtifact(
		runtimedomain.DecisionArtifactID(string(attempt.ID)+"-recovery-decision-gen-"+fmt.Sprint(payload.Generation)),
		run.ProjectID, RecoveryDecisionKind, artifactPolicyVersion, inputJSON, resultJSON, time.Now().UTC(),
	)
	if err != nil {
		return err
	}
	if _, err := tx.Runtime().RecordDecisionArtifact(ctx, artifact); err != nil {
		return err
	}

	eventPayload, err := json.Marshal(recoveryDecisionRecordedEventPayload{
		AttemptID: string(attempt.ID), NodeRunID: string(nodeRun.ID), RunID: string(run.ID),
		WorkItemID: string(run.WorkItemID), NextAction: string(action), Reason: reason,
	})
	if err != nil {
		return fmt.Errorf("marshal %s event payload: %w", RecoveryDecisionRecordedEventType, err)
	}
	return tx.Events().Append(ctx, ports.DomainEvent{
		ID: string(artifact.ID) + "-event", ProjectID: string(run.ProjectID),
		AggregateType: "RecoveryDecision", AggregateID: string(artifact.ID), Sequence: 1,
		EventType: RecoveryDecisionRecordedEventType, SchemaVersion: RecoveryDecisionRecordedSchemaVersion,
		PayloadJSON: string(eventPayload), CorrelationID: payload.CorrelationID, CreatedAt: time.Now().UTC(),
	})
}

// deterministicRecoveryAttemptID/deterministicRecoverySnapshotID are
// V5-13's own contract, confirmed with the user verbatim (2026-09-11):
// "recovery activation ID phải dẫn xuất deterministic từ Attempt bị crash
// và recovery generation" — a REPLAY of the same (interrupted attempt,
// generation) pair must derive the SAME replacement Attempt/Snapshot ID,
// so consumeFreshStart's own idempotent-no-op guard (a second delivery
// finds the row already there) is what makes replay safe, never a second
// replacement. Mirrors the identical sha256/hex/truncated-16-byte
// convention this codebase already established for every other
// deterministic ID (deterministicJoinNodeRunID, advance.go;
// deterministicCompletionDecisionID, completion_policy.go).
func deterministicRecoveryAttemptID(interruptedAttemptID runtimedomain.ExecutionAttemptID, generation uint64) string {
	sum := sha256.Sum256([]byte("recovery-fresh-start-attempt" + "\x00" + string(interruptedAttemptID) + "\x00" + fmt.Sprint(generation)))
	return "recovery-attempt-" + hex.EncodeToString(sum[:16])
}

func deterministicRecoverySnapshotID(interruptedAttemptID runtimedomain.ExecutionAttemptID, generation uint64) string {
	sum := sha256.Sum256([]byte("recovery-fresh-start-snapshot" + "\x00" + string(interruptedAttemptID) + "\x00" + fmt.Sprint(generation)))
	return "recovery-snapshot-" + hex.EncodeToString(sum[:16])
}

// consumeFreshStart is V5-13's own real Phase 1 (2026-09-11, PR2):
// "Tx reserve Attempt mới, tạo V5 Snapshot mới bind replacement Attempt và
// enqueue EXECUTE_NODE ... Tx claim decision đúng một lần" — reserves a
// replacement ExecutionAttempt (AttemptNumber+1, deterministic ID),
// clones the checkpoint's own referenced ContextSnapshot for it (the
// IDENTICAL "clone Snapshot for a new Attempt" pattern retryAttempt above
// already uses for RETRY — a recovery-driven fresh start needs its own
// bound Snapshot exactly as much as a technical retry does), pins
// LastCheckpointID on the new Attempt (so AssembleAgentExecutionRequest's
// own future consumer, V5-13 PR3, knows to set RecoveryCheckpoint for
// AGENT), enqueues its own EXECUTE_NODE job, and records the
// RecoveryDecisionKind DecisionArtifact — all atomically in ONE
// transaction, so a redelivered reaper job for the identical (interrupted
// attempt, generation) pair either finds everything already there (its
// own deterministic IDs collide) and no-ops, or nothing at all exists yet
// and the whole reservation commits together.
//
// Never calls a real ports.AgentExecutor.Start itself (this whole handler
// is JobClass=CONTROL — see this file's own package doc comment for why
// that would be a structural violation): dispatch is the EXISTING
// ExecuteNodeHandler's own job, unchanged, the same as for any other
// EXECUTE_NODE job this codebase already enqueues.
func (h *RecoveryReaperHandler) consumeFreshStart(
	ctx context.Context, attempt runtimedomain.ExecutionAttempt, nodeRun runtimedomain.NodeRun, run runtimedomain.WorkflowRun,
	payload RecoveryReaperJobPayload, reason, policyVersionID string, attemptRules policyAttemptRules,
) error {
	return h.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		current, err := tx.Runtime().GetNodeRun(ctx, string(nodeRun.ID))
		if err != nil {
			return err
		}
		if current.State != runtimedomain.NodeRunRunning {
			// Already moved on since the read-only classification pass —
			// idempotent no-op, mirroring retryAttempt's own guard.
			return nil
		}

		nextAttemptIDStr := deterministicRecoveryAttemptID(attempt.ID, payload.Generation)
		if _, err := tx.Runtime().GetExecutionAttempt(ctx, nextAttemptIDStr); err == nil {
			// A previous delivery already reserved this exact replacement —
			// idempotent no-op, never a second Attempt/Snapshot/job for the
			// same (interrupted attempt, generation) pair.
			return nil
		} else if !errors.Is(err, ports.ErrPersistenceNotFound) {
			return err
		}

		if h.recovery == nil {
			return errors.New("runtime: recovery reaper has no RecoveryStore, cannot consume a FRESH_START decision")
		}
		checkpoint, err := h.recovery.LoadLatestCheckpoint(ctx, attempt.ID)
		if err != nil {
			return fmt.Errorf("load latest checkpoint for fresh start: %w", err)
		}
		previousSnapshot, err := tx.ContextSnapshots().GetSnapshot(ctx, string(checkpoint.ContextSnapshotID))
		if err != nil {
			return fmt.Errorf("load checkpoint's own context snapshot: %w", err)
		}

		nextAttempt, err := runtimedomain.NewExecutionAttempt(
			runtimedomain.ExecutionAttemptID(nextAttemptIDStr), attempt.NodeRunID, attempt.AttemptNumber+1,
			attempt.ExecutionProfileHash, attempt.ProviderKey, attempt.InputRevisionSet,
		)
		if err != nil {
			return err
		}
		checkpointID := checkpoint.ID
		nextAttempt.LastCheckpointID = &checkpointID

		nextSnapshotID := contextsnapshot.ID(deterministicRecoverySnapshotID(attempt.ID, payload.Generation))
		clonedSnapshot, err := contextsnapshot.NewSnapshot(
			nextSnapshotID, previousSnapshot.ProjectID, previousSnapshot.WorkItemID, contextsnapshot.AttemptID(nextAttemptIDStr),
			previousSnapshot.MessageRefs, previousSnapshot.ResourceRefs, previousSnapshot.EvidenceRefs, previousSnapshot.Revisions, h.clk.Now(),
		)
		if err != nil {
			return err
		}
		nextAttempt.ContextSnapshotID = &nextSnapshotID

		if _, err := tx.Runtime().CreateExecutionAttempt(ctx, nextAttempt); err != nil {
			return err
		}
		if _, err := tx.ContextSnapshots().CreateSnapshot(ctx, clonedSnapshot); err != nil {
			return err
		}

		jobPayload, err := json.Marshal(ExecuteNodeJobPayload{RunID: string(run.ID), NodeRunID: string(nodeRun.ID), AttemptID: nextAttemptIDStr})
		if err != nil {
			return fmt.Errorf("marshal %s fresh-start job payload: %w", ExecuteNodeJobKind, err)
		}
		if _, err := tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
			ID: ports.JobID(h.ids.NewID()), ProjectID: run.ProjectID, Kind: ExecuteNodeJobKind, RunID: string(run.ID),
			AggregateType: "ExecutionAttempt", AggregateID: nextAttemptIDStr, Payload: jobPayload,
			MaxClaims: defaultExecuteNodeJobMaxClaims, IdempotencyKey: "execute-" + nextAttemptIDStr,
		}); err != nil {
			return err
		}

		checkpointRef := &recoveryCheckpointRef{checkpointID: string(checkpoint.ID), contextSnapshotID: string(checkpoint.ContextSnapshotID)}
		return writeRecoveryDecisionArtifactTx(ctx, tx, attempt, nodeRun, run, payload, RecoveryActionFreshStart, reason, policyVersionID, attempt, attemptRules, checkpointRef)
	})
}
