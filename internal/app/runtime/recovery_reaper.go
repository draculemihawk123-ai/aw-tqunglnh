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
//     reconciliation observed a real mutation (quarantined) — this handler
//     records a durable recovery decision (DecisionArtifact,
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
//     - ESCALATE: retry budget is exhausted, the Run is cancelling, or no
//     checkpoint exists to build a FRESH_START decision from — records the
//     identical kind of DecisionArtifact, NextAction=ESCALATE, never a
//     blind retry.
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
}

// NewRecoveryReaperHandler returns a ready-to-register RecoveryReaperHandler.
func NewRecoveryReaperHandler(
	uow ports.UnitOfWork, ids idsource.Source, clk clock.Clock,
	interruptions worker.InterruptionRecoveryStore, workspaces worker.WorkspaceReconciler, recovery worker.RecoveryStore,
) *RecoveryReaperHandler {
	return &RecoveryReaperHandler{uow: uow, ids: ids, clk: clk, interruptions: interruptions, workspaces: workspaces, recovery: recovery}
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
		return enqueueRecoveryReaperJobTx(ctx, tx, ids, state.Generation, "")
	})
}

func enqueueRecoveryReaperJobTx(ctx context.Context, tx ports.Tx, ids idsource.Source, generation uint64, correlationID string) error {
	payload, err := json.Marshal(RecoveryReaperJobPayload{Generation: generation, CorrelationID: correlationID})
	if err != nil {
		return fmt.Errorf("marshal %s job payload: %w", RecoveryReaperJobKind, err)
	}
	_, err = tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: ports.JobID(ids.NewID()), Kind: RecoveryReaperJobKind,
		AggregateType: "RecoveryReaper", AggregateID: "singleton", Payload: payload,
		MaxClaims: defaultRecoveryReaperJobMaxClaims, IdempotencyKey: fmt.Sprintf("recovery-reaper:%d", generation),
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
		return enqueueRecoveryReaperJobTx(ctx, tx, h.ids, advanced.Generation, payload.CorrelationID)
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

	if recovery.NextState == runtimedomain.ExecutionAttemptIndeterminate && recovery.Reconciliation == worker.ReconciliationMutationObserved {
		// A confirmed mutation was observed — never retry in place against a
		// now-QUARANTINED workspace. If there is a checkpoint to build a
		// FRESH_START decision from, record that; otherwise ESCALATE (no
		// usable basis for FRESH_START either).
		if h.hasUsableCheckpoint(ctx, attempt.ID) {
			return h.recordDecision(ctx, attempt, nodeRun, run, payload, RecoveryActionFreshStart, string(recovery.Reason), policyVersionID, attempt, attemptRules)
		}
		return h.recordDecision(ctx, attempt, nodeRun, run, payload, RecoveryActionEscalate, string(recovery.Reason), policyVersionID, attempt, attemptRules)
	}

	if !runCancelling && budgetRemains {
		return h.retryAttempt(ctx, attempt, nodeRun, run)
	}
	return h.recordDecision(ctx, attempt, nodeRun, run, payload, RecoveryActionEscalate, string(recovery.Reason), policyVersionID, attempt, attemptRules)
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
func (h *RecoveryReaperHandler) hasUsableCheckpoint(ctx context.Context, attemptID runtimedomain.ExecutionAttemptID) bool {
	if h.recovery == nil {
		return false
	}
	checkpoint, err := h.recovery.LoadLatestCheckpoint(ctx, attemptID)
	if err != nil || checkpoint.ContextSnapshotID == "" {
		return false
	}
	if _, err := h.recovery.LoadContextSnapshot(ctx, checkpoint.ContextSnapshotID); err != nil {
		return false
	}
	return true
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
// own RECOVERY_DECISION_RECORDED event — ESCALATE/FRESH_START's own shared
// closing step.
func (h *RecoveryReaperHandler) recordDecision(
	ctx context.Context, attempt runtimedomain.ExecutionAttempt, nodeRun runtimedomain.NodeRun, run runtimedomain.WorkflowRun,
	payload RecoveryReaperJobPayload, action RecoveryNextAction, reason, policyVersionID string,
	sourceAttempt runtimedomain.ExecutionAttempt, attemptRules policyAttemptRules,
) error {
	return h.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		result := recoveryDecisionResult{
			NextAction: string(action), Reason: reason, PolicyVersionID: policyVersionID,
			AttemptsUsed: uint32(sourceAttempt.AttemptNumber), MaxAttempts: attemptRules.MaxAttempts,
		}
		if action == RecoveryActionFreshStart && h.recovery != nil {
			if checkpoint, err := h.recovery.LoadLatestCheckpoint(ctx, attempt.ID); err == nil {
				result.CheckpointID = string(checkpoint.ID)
				result.ContextSnapshotID = string(checkpoint.ContextSnapshotID)
			}
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
	})
}
