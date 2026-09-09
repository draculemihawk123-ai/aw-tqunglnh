// RetryBlockedActivation is V5-08D's own scope (docs/design/07-v5-execution-evidence.md):
// an Attempt BLOCKED by V5-08's own admission checks (admission.go — the
// four reasons in admissionPriority) is otherwise a dead end — nothing else
// in this codebase ever re-drives a BLOCKED Attempt/NodeRun back to life.
// This file's own RetryBlockedActivationHandler.Retry gives it exactly one
// exit: re-run the SAME four admission checks against the SAME immutable
// pins (never a repin — see this file's own package doc below), and only
// on a genuine pass, atomically hand off to the EXISTING
// ScheduleExecutableNodeRun pipeline for a fresh NodeRun activation — the
// identical "one real resolver, every caller reuses it" shape
// reactivateBlockedNodeRunTx (scope_expansion.go, V4-12A) already
// established for the OTHER blocker group (SCOPE_EXPANSION_REQUIRED).
//
// Deliberately NOT the SAME command as ResolveWorkItemBlocker
// (resolve_work_item_blocker.go): that command's own precondition ("no
// non-terminal Run for this WorkItem") only ever fires once the WorkItem's
// owning Run has already reached a terminal state — it exists to formally
// close out a blocker whose Run is already gone. An admission-blocked
// NodeRun's own owning Run never leaves RUNNING/WAITING (no transition in
// this codebase ever produces runtime.WorkflowRunBlocked — the four
// admission reasons only ever CAS the NodeRun/Attempt, never the Run
// itself), so ResolveWorkItemBlocker's own precondition would reject every
// real retry attempt outright. RetryBlockedActivation is the dedicated
// command for a STILL-LIVE Run instead — the one workdomain.BlockerType's
// own doc comment already names ahead of time ("a future
// RetryBlockedActivation").
//
// "Không repin Run" (this task's own locked requirement, confirmed by
// reading the code rather than asking — loadExecutionProfile only ever
// reads the ORIGINAL, immutable "<nodeRunId>-execution-profile-v1"
// DecisionArtifact schedule.go recorded once at the NodeRun's own first
// scheduling): a retry re-probes the EXACT SAME pinned AdapterBuildID this
// NodeRun has always had. If that specific immutable build is still
// drifted, this command has no authority to substitute a newer one — the
// blocker stays OPEN, unresolved, and the only valid action left is
// CancelRun (workdomain.BlockerType.Waivable's own doc comment already
// names this exact case).
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

var (
	// ErrNodeRunNotBlocked is returned when the named NodeRun is not
	// BLOCKED at all, or is BLOCKED but no admission blocker backs it (a
	// genuine caller error — the wrong NodeRunID, or one blocked for some
	// other reason entirely). Unlike a scope-expansion reactivation or a
	// technical-cancellation classification, nothing in this codebase ever
	// transitions a BLOCKED NodeRun's own State away from BLOCKED again —
	// a retried NodeRun stays BLOCKED forever as a historical record,
	// exactly like the Attempt that blocked it (createRetriedNodeRunActivationTx's
	// own doc comment) — so "was this already retried" is answered by the
	// admission blocker's own State (RetryBlockedActivationResult's own
	// AlreadyRetried), never by re-checking the NodeRun's own State.
	ErrNodeRunNotBlocked = errors.New("runtime: node run is not blocked by an admission check")
	// ErrNotAnAdmissionBlockerReason is returned when the BLOCKED Attempt's
	// own TerminationReason is not one of admission.go's own four
	// admission-check reasons — most notably SCOPE_EXPANSION_REQUIRED,
	// which has its own dedicated resolution path
	// (reactivateBlockedNodeRunTx) and RUN_CANCELLED, which never blocks a
	// live Run in the first place (its own Run is already gone).
	ErrNotAnAdmissionBlockerReason = errors.New("runtime: node run's own blocked reason is not an admission-check reason")
	// ErrRunNotRetryable is returned when the owning WorkflowRun is not
	// RUNNING or WAITING — a Run that already left that pair (CANCELLING/
	// CANCELLED included — this task's own explicit "cancel fence", or any
	// terminal state) has nothing left for a fresh activation to route
	// into; reactivating now would create orphaned work.
	ErrRunNotRetryable = errors.New("runtime: workflow run is not in a retryable state (must be RUNNING or WAITING)")
)

// RetryBlockedActivationRequest is what a caller supplies to
// RetryBlockedActivationHandler.Retry.
type RetryBlockedActivationRequest struct {
	NodeRunID     string
	Actor         string
	Reason        string
	CorrelationID string
}

// RetryBlockedActivationResult is what Retry returns — a structured
// business outcome, never an error, for every case this command has real
// authority to decide (a revalidation failure included: "thất bại giữ
// nguyên blocker hiện tại" is an expected, first-class outcome here, not a
// technical failure).
type RetryBlockedActivationResult struct {
	NodeRunID string
	// AlreadyRetried reports the idempotent no-op case: the NodeRun was no
	// longer BLOCKED by the time this call actually looked (an earlier
	// winner already retried it, or it was never blocked at all).
	AlreadyRetried bool
	// Retried reports genuine success: exactly one new NodeRun activation
	// was created and handed to ScheduleExecutableNodeRun.
	Retried bool
	// ReactivatedNodeRunID is populated only when Retried is true.
	ReactivatedNodeRunID string
	// FailureReason/FailureDetail are populated only when revalidation
	// itself still fails — the SAME vocabulary evaluateAdmission's own
	// admissionDecision already uses, so a caller sees exactly why, never
	// a generic rejection. The existing blocker is left OPEN, unresolved,
	// untouched, exactly as it was (this task's own locked requirement).
	FailureReason runtimedomain.TerminationReason
	FailureDetail string
}

// RetryBlockedActivationHandler is the application-layer command V5-08D
// adds — needs the same isolation/agents dependencies admission.go's own
// ExecuteNodeHandler already carries, since revalidation re-runs the exact
// same two real-I/O checks (isolation enforceability, adapter-build
// drift) admission originally ran.
type RetryBlockedActivationHandler struct {
	uow       ports.UnitOfWork
	ids       idsource.Source
	isolation ports.IsolationEnforcementChecker
	agents    *agentregistry.Registry
}

// NewRetryBlockedActivationHandler returns a ready-to-use
// RetryBlockedActivationHandler.
func NewRetryBlockedActivationHandler(
	uow ports.UnitOfWork, ids idsource.Source, isolation ports.IsolationEnforcementChecker, agents *agentregistry.Registry,
) *RetryBlockedActivationHandler {
	return &RetryBlockedActivationHandler{uow: uow, ids: ids, isolation: isolation, agents: agents}
}

// Retry implements the two-phase design admission.go's own package doc
// comment already established (confirmed by reading that discipline, not
// re-asked): Phase 1 is a read-only preflight plus real I/O entirely
// outside any transaction (the two admission checks that need it); Phase 2
// is a single serialized-write transaction that re-verifies every
// precondition fresh (closing the identical TOCTOU race admitOrClaimRunning
// itself closes between preflight and commit) before choosing, atomically,
// between "still failing, blocker stays OPEN" and "passed, new activation
// created".
func (h *RetryBlockedActivationHandler) Retry(ctx context.Context, req RetryBlockedActivationRequest) (RetryBlockedActivationResult, error) {
	nodeRunID := strings.TrimSpace(req.NodeRunID)
	actor := strings.TrimSpace(req.Actor)
	reason := strings.TrimSpace(req.Reason)
	if nodeRunID == "" {
		return RetryBlockedActivationResult{}, errors.New("runtime: NodeRunID is required")
	}
	if actor == "" {
		return RetryBlockedActivationResult{}, errors.New("runtime: Actor is required")
	}
	if reason == "" {
		return RetryBlockedActivationResult{}, errors.New("runtime: Reason is required")
	}

	// Preflight: read-only, decides whether this call is even worth a real
	// I/O probe — never mutates anything. The admission blocker's own
	// State, not the NodeRun's own State, is the real idempotency gate
	// (see ErrNodeRunNotBlocked's own doc comment for why).
	_, run, blockedAttempt, blocker, err := h.loadForRetry(ctx, nodeRunID)
	if err != nil {
		return RetryBlockedActivationResult{}, err
	}
	if !isAdmissionBlockerReason(blockedAttempt.TerminationReason) {
		return RetryBlockedActivationResult{}, fmt.Errorf("%w: reason %s", ErrNotAnAdmissionBlockerReason, blockedAttempt.TerminationReason)
	}
	if blocker.State != workdomain.BlockerOpen {
		return RetryBlockedActivationResult{NodeRunID: nodeRunID, AlreadyRetried: true}, nil
	}
	if run.State != runtimedomain.WorkflowRunRunning && run.State != runtimedomain.WorkflowRunWaiting {
		return RetryBlockedActivationResult{}, fmt.Errorf("%w: run %s is %s", ErrRunNotRetryable, run.ID, run.State)
	}

	profile, err := loadExecutionProfile(ctx, h.uow, nodeRunID)
	if err != nil {
		return RetryBlockedActivationResult{}, err
	}

	// Phase 1: real I/O, entirely outside any transaction — the identical
	// probe admitOrClaimRunning itself runs, re-run against the SAME
	// immutable pin (never a different, newer AdapterBuildVersion — see
	// this file's own package doc comment).
	probe, err := runAdmissionProbePhase(ctx, h.uow, h.isolation, h.agents, nodeRunID, profile)
	if err != nil {
		return RetryBlockedActivationResult{}, err
	}

	var result RetryBlockedActivationResult
	err = h.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		nodeRun, run, blockedAttempt, blocker, err := loadForRetryTx(ctx, tx, nodeRunID)
		if err != nil {
			return err
		}
		if !isAdmissionBlockerReason(blockedAttempt.TerminationReason) {
			return fmt.Errorf("%w: reason %s", ErrNotAnAdmissionBlockerReason, blockedAttempt.TerminationReason)
		}
		if blocker.State != workdomain.BlockerOpen {
			// Redelivery, or a concurrent winner already retried this
			// exact NodeRun — idempotent no-op.
			result = RetryBlockedActivationResult{NodeRunID: nodeRunID, AlreadyRetried: true}
			return nil
		}
		if run.State != runtimedomain.WorkflowRunRunning && run.State != runtimedomain.WorkflowRunWaiting {
			return fmt.Errorf("%w: run %s is %s", ErrRunNotRetryable, run.ID, run.State)
		}

		// TOCTOU-closing re-verification — the identical check
		// admitOrClaimRunning itself runs between its own Phase 1 probe
		// and Phase 2 commit (admission.go): the pinned build row this
		// probe ran against must still be the exact one profile names.
		if profile.AdapterBuild != nil {
			if probe.pinnedBuild == nil || probe.pinnedBuild.ID() != profile.AdapterBuild.BuildID {
				return fmt.Errorf("runtime: retry: node run %s's adapter build probe result does not match its own pin", nodeRunID)
			}
			if _, err := tx.AdapterBuilds().Get(ctx, profile.AdapterBuild.BuildID); err != nil {
				return fmt.Errorf("runtime: retry: re-verify pinned adapter build %s: %w", profile.AdapterBuild.BuildID, err)
			}
		}

		decision, err := evaluateAdmission(ctx, tx, profile, nodeRun, probe)
		if err != nil {
			return err
		}
		if decision.reason != "" {
			// This task's own locked requirement ("thất bại giữ nguyên
			// blocker hiện tại và không tạo thêm blocked activation"): no
			// write at all on this branch — report the still-failing
			// reason/detail back as a legitimate business outcome.
			result = RetryBlockedActivationResult{NodeRunID: nodeRunID, FailureReason: decision.reason, FailureDetail: decision.detail}
			return nil
		}

		reactivatedID, err := createRetriedNodeRunActivationTx(ctx, tx, h.ids, run, nodeRun)
		if err != nil {
			return err
		}

		// The ONLY event with authority to resolve THIS admission blocker
		// — never ResolveWorkItemBlocker (see this file's own package doc
		// comment for why that command's own precondition can never fire
		// here). unlockedStatus is ACTIVE, not READY: the SAME Run
		// resumes, it never stopped (mirrors reactivateBlockedNodeRunTx's
		// own identical choice for SCOPE_EXPANSION_REQUIRED). blocker was
		// already confirmed OPEN above, in this SAME transaction.
		if _, err := closeWorkItemBlockerTx(
			ctx, tx, blocker, workdomain.BlockerResolved, actor, reason, "", workdomain.WorkItemActive, req.CorrelationID, "",
		); err != nil {
			return err
		}

		schedulePayload, err := json.Marshal(ScheduleNodeRunJobPayload{RunID: string(run.ID), NodeRunID: reactivatedID, CorrelationID: req.CorrelationID})
		if err != nil {
			return fmt.Errorf("marshal %s job payload: %w", ScheduleNodeRunJobKind, err)
		}
		if _, err := tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
			ID: ports.JobID(h.ids.NewID()), ProjectID: run.ProjectID, Kind: ScheduleNodeRunJobKind,
			AggregateType: "NodeRun", AggregateID: reactivatedID, Payload: schedulePayload,
			MaxClaims: defaultScheduleNodeRunJobMaxClaims, IdempotencyKey: "schedule-" + reactivatedID,
		}); err != nil {
			return err
		}

		result = RetryBlockedActivationResult{NodeRunID: nodeRunID, Retried: true, ReactivatedNodeRunID: reactivatedID}
		return nil
	})
	if err != nil {
		return RetryBlockedActivationResult{}, err
	}
	return result, nil
}

// loadForRetry is the read-only-transaction wrapper around loadForRetryTx —
// Retry's own preflight (before any real I/O) and final-transaction reload
// both need the exact same four rows, so this is the one place that reads
// them, called twice (once wrapped in WithReadOnly, once directly inside
// the final WithSerializedWrite).
func (h *RetryBlockedActivationHandler) loadForRetry(ctx context.Context, nodeRunID string) (
	nodeRun runtimedomain.NodeRun, run runtimedomain.WorkflowRun, blockedAttempt runtimedomain.ExecutionAttempt,
	blocker workdomain.WorkItemBlocker, err error,
) {
	err = h.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var loadErr error
		nodeRun, run, blockedAttempt, blocker, loadErr = loadForRetryTx(ctx, tx, nodeRunID)
		return loadErr
	})
	return nodeRun, run, blockedAttempt, blocker, err
}

// loadForRetryTx locates the NodeRun, its owning WorkflowRun, the single
// ExecutionAttempt that put nodeRunID BLOCKED, and that attempt's own
// admission blocker — blockAdmission (execute.go) always CASes the NodeRun
// and its owning Attempt together, in lockstep, and opens the blocker in
// that SAME transaction (openWorkItemBlockerTx), so exactly one BLOCKED
// Attempt with a real, deterministically-IDed blocker must exist whenever
// the NodeRun itself is BLOCKED for an admission reason. No accessor keyed
// directly by NodeRunID exists (ListExecutionAttemptsForRun is the only
// listing this codebase's own ports.Tx exposes), so this filters that by
// NodeRunID/State instead of adding a new one for a single caller.
func loadForRetryTx(ctx context.Context, tx ports.Tx, nodeRunID string) (
	nodeRun runtimedomain.NodeRun, run runtimedomain.WorkflowRun, blockedAttempt runtimedomain.ExecutionAttempt,
	blocker workdomain.WorkItemBlocker, err error,
) {
	nodeRun, err = tx.Runtime().GetNodeRun(ctx, nodeRunID)
	if err != nil {
		return nodeRun, run, blockedAttempt, blocker, err
	}
	run, err = tx.Runtime().GetWorkflowRun(ctx, string(nodeRun.RunID))
	if err != nil {
		return nodeRun, run, blockedAttempt, blocker, err
	}
	if nodeRun.State != runtimedomain.NodeRunBlocked {
		return nodeRun, run, blockedAttempt, blocker, fmt.Errorf("%w: node run %s is %s, not BLOCKED", ErrNodeRunNotBlocked, nodeRunID, nodeRun.State)
	}
	attempts, err := tx.Runtime().ListExecutionAttemptsForRun(ctx, string(run.ID))
	if err != nil {
		return nodeRun, run, blockedAttempt, blocker, err
	}
	found := false
	for _, attempt := range attempts {
		if string(attempt.NodeRunID) == nodeRunID && attempt.State == runtimedomain.ExecutionAttemptBlocked {
			blockedAttempt = attempt
			found = true
			break
		}
	}
	if !found {
		return nodeRun, run, blockedAttempt, blocker, fmt.Errorf("%w: node run %s has no blocked attempt", ErrNodeRunNotBlocked, nodeRunID)
	}
	// Tolerate ErrPersistenceNotFound here: a BLOCKED Attempt whose own
	// TerminationReason is not an admission reason (SCOPE_EXPANSION_REQUIRED,
	// whose own blocker uses a different ID suffix entirely) has no row
	// under THIS ID pattern at all — Retry's own isAdmissionBlockerReason
	// check (run before this blocker is ever inspected) is what rejects
	// that case with a clear, specific error, not a raw persistence
	// not-found bubbling up from here.
	blocker, err = tx.Work().GetWorkItemBlocker(ctx, string(blockedAttempt.ID)+"-admission-blocker")
	if errors.Is(err, ports.ErrPersistenceNotFound) {
		return nodeRun, run, blockedAttempt, workdomain.WorkItemBlocker{}, nil
	}
	return nodeRun, run, blockedAttempt, blocker, err
}

// createRetriedNodeRunActivationTx creates exactly one new NodeRun
// activation for a retried admission-blocked NodeRun — NodeKey/Iteration/
// BranchTokenID copied unchanged from the blocked activation (Iteration
// copied, never incremented, so a retry never consumes a V4-07 cycle
// budget; BranchTokenID copied so a FORK/JOIN this NodeRun belongs to keeps
// tracking the right branch), the identical "mint a new NodeRunID, bump
// ActivationSequence, hand off to the EXISTING ScheduleExecutableNodeRun
// pipeline via a plain ScheduleNodeRunJobKind job" shape
// reactivateBlockedNodeRunTx (scope_expansion.go) already established.
// Unlike that caller, this one never touches EffectiveScope or appends a
// RunManifestAmendment: an admission failure is never a scope problem, so
// nothing about the WorkItem's own granted scope needs to change for a
// retry to make sense — EffectiveScope is left nil, exactly like
// reactivateBlockedNodeRunTx's own reactivated NodeRun, since
// ScheduleExecutableNodeRun re-derives it fresh from the WorkItem's own
// current snapshot regardless of whatever NewNodeRun was constructed with.
func createRetriedNodeRunActivationTx(
	ctx context.Context, tx ports.Tx, ids idsource.Source, run runtimedomain.WorkflowRun, blockedNodeRun runtimedomain.NodeRun,
) (string, error) {
	nodeRuns, err := tx.Runtime().ListNodeRunsForRun(ctx, string(run.ID))
	if err != nil {
		return "", err
	}
	var maxSequence uint64
	for _, nr := range nodeRuns {
		if nr.ActivationSequence > maxSequence {
			maxSequence = nr.ActivationSequence
		}
	}
	reactivatedID := ids.NewID()
	reactivated, err := runtimedomain.NewNodeRun(
		runtimedomain.NodeRunID(reactivatedID), run.ID, blockedNodeRun.NodeKey, maxSequence+1, blockedNodeRun.Iteration, nil,
		canonicalStateHash(run.SharedState), blockedNodeRun.ExecutionProfileHash,
	)
	if err != nil {
		return "", err
	}
	reactivated.BranchTokenID = blockedNodeRun.BranchTokenID
	reactivated.ReactivationReason = "ADMISSION_RETRIED"
	if _, err := tx.Runtime().CreateNodeRun(ctx, reactivated); err != nil {
		return "", err
	}
	return reactivatedID, nil
}
