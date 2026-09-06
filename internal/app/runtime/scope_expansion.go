// V4-12A's own app layer (docs/design/06-v4-runtime-engine.md,
// AK-ARCH-015A: "apply scope expansion đã duyệt mà không sửa quyền lịch sử
// hoặc mở quyền cho sibling"). Two durable jobs, confirmed with the user
// before writing this file:
//
//   - REQUEST_SCOPE_EXPANSION (enqueued by finalize.go's own
//     requestScopeExpansionTx, the moment a NodeExecutor reports
//     ExecutionAttemptBlocked): RequestScopeExpansionHandler calls the real
//     work.RequestScopeExpansion command, in ITS OWN separate transaction,
//     passing the ScopeExpansionRequestID that transaction already
//     RESERVED — never minting a new one — so a crash/redelivery always
//     replays onto the exact same request rather than losing the link.
//   - SCOPE_EXPANSION_RECONCILE (enqueued by internal/app/work's own
//     ApproveScopeExpansion, exactly once per request that has a runtime
//     origin): ScopeExpansionReconcileHandler is a self-rescheduling poll —
//     PENDING (still waiting on provisioning) re-enqueues itself with
//     backoff; REJECTED/WITHDRAWN stops, leaving the Attempt/NodeRun/Run
//     BLOCKED (Alpha has no automatic resume — only CancelRun); a
//     WorkspaceSet that reaches a terminal non-READY state (BLOCKED/
//     RELEASING/RELEASED) stops too, marking the origin NEEDS_RECOVERY
//     rather than polling forever; APPROVED + WorkspaceSet READY (with its
//     own BaseRevisionSet actually covering every requested repository —
//     never trusting a possibly-stale READY alone) is the only path that
//     reaches reactivateBlockedNodeRunTx.
//
// Neither job ever mutates a SIBLING NodeRun's own EffectiveScope/Attempt,
// and the reactivated NodeRun always PINS its own new EffectiveScope/
// ManifestRevision through the EXISTING, unmodified ScheduleExecutableNodeRun
// pipeline (via a plain ScheduleNodeRunJobKind job) rather than duplicating
// that resolution here — the same "one real resolver, every caller reuses
// it" discipline this package already established.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// scopeExpansionSystemActor is the Actor RequestScopeExpansionHandler
// stamps on the internal-runtime-path work.RequestScopeExpansion call it
// makes — confirmed with the user before writing this file: never the
// executor's own identity (the executor only proposes; it never
// authenticates as anything), and never a real human/operator actor
// either, since no human decided to raise this request — the BLOCKED
// Attempt itself did.
const scopeExpansionSystemActor = "system:runtime"

// RequestScopeExpansionJobKind is the durable job requestScopeExpansionTx
// (finalize.go) enqueues. internal/app/work never imports this package (it
// only ever CONSUMES work.RequestScopeExpansion, never enqueues this job
// kind itself) — this stays the mirror image of ScopeExpansionReconcileJobKind
// (defined in internal/app/work, since THAT package is the one that
// enqueues it).
const RequestScopeExpansionJobKind = "REQUEST_SCOPE_EXPANSION"

const defaultRequestScopeExpansionJobMaxClaims = 3

// RequestScopeExpansionJobPayload is the exact JSON shape
// requestScopeExpansionTx marshals for a RequestScopeExpansionJobKind job.
type RequestScopeExpansionJobPayload struct {
	RunID         string `json:"runId"`
	NodeRunID     string `json:"nodeRunId"`
	AttemptID     string `json:"attemptId"`
	CorrelationID string `json:"correlationId,omitempty"`
}

// RequestScopeExpansionHandler is a ready-to-register workerpool.Handler
// for RequestScopeExpansionJobKind.
type RequestScopeExpansionHandler struct {
	uow ports.UnitOfWork
	ids idsource.Source
}

// NewRequestScopeExpansionHandler returns a ready-to-register
// RequestScopeExpansionHandler.
func NewRequestScopeExpansionHandler(uow ports.UnitOfWork, ids idsource.Source) *RequestScopeExpansionHandler {
	return &RequestScopeExpansionHandler{uow: uow, ids: ids}
}

var _ workerpool.Handler = (*RequestScopeExpansionHandler)(nil)

// Handle implements workerpool.Handler for RequestScopeExpansionJobKind: it
// reads the already-persisted ScopeExpansionOrigin (read-only — the origin
// row itself was already durably created inside requestScopeExpansionTx's
// own fenced transaction; this job's own job is purely to raise the real
// request from it) and calls work.RequestScopeExpansion, in that
// command's own separate transaction, with the RESERVED RequestID. A
// redelivered/duplicate job replays idempotently through Receipts (the
// SAME (Actor, Scope, IdempotencyKey) every time), exactly like any other
// command in this codebase — never a new request, never a lost link.
func (h *RequestScopeExpansionHandler) Handle(ctx context.Context, job ports.DurableJob) error {
	var payload RequestScopeExpansionJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("runtime: unmarshal %s job %s payload: %w", RequestScopeExpansionJobKind, job.ID, err)
	}
	if payload.RunID == "" || payload.NodeRunID == "" || payload.AttemptID == "" {
		return fmt.Errorf("runtime: %s job %s payload missing runId/nodeRunId/attemptId", RequestScopeExpansionJobKind, job.ID)
	}

	var origin runtimedomain.ScopeExpansionOrigin
	var run runtimedomain.WorkflowRun
	if err := h.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		origin, err = tx.Runtime().GetScopeExpansionOriginByAttemptID(ctx, payload.AttemptID)
		if err != nil {
			return err
		}
		run, err = tx.Runtime().GetWorkflowRun(ctx, payload.RunID)
		return err
	}); err != nil {
		return err
	}

	grants := make([]work.ScopeGrantRequest, len(origin.Proposal.RequestedGrants))
	for i, g := range origin.Proposal.RequestedGrants {
		grants[i] = work.ScopeGrantRequest{RepositoryID: g.RepositoryID, Access: g.Access, PathScopes: g.PathScopes, Reason: g.Reason}
	}

	cmd := ports.Command{
		ID: h.ids.NewID(), IdempotencyKey: "scope-expansion-request:" + payload.AttemptID, Actor: scopeExpansionSystemActor,
		CorrelationID: payload.CorrelationID, Scope: ports.ProjectScope(string(run.ProjectID)), RequestedAt: time.Now().UTC(),
		Type: "RequestScopeExpansion", RequestHash: origin.ProposalHash,
	}
	_, err := work.RequestScopeExpansion(ctx, h.uow, h.ids, cmd, work.RequestScopeExpansionRequest{
		FamilyID: origin.FamilyID, RequestedGrants: grants, Reason: origin.Proposal.Reason,
		ReferencedWorkItemID: origin.WorkItemID, RequestID: origin.RequestID,
	})
	return err
}

// --- SCOPE_EXPANSION_RECONCILE ---

const (
	scopeExpansionReconcileBaseBackoffSeconds  = 5
	scopeExpansionReconcileMaxBackoffSeconds   = 300
	defaultScopeExpansionReconcileJobMaxClaims = 20
)

// ScopeExpansionReconcileHandler is a ready-to-register workerpool.Handler
// for work.ScopeExpansionReconcileJobKind (defined in internal/app/work,
// the package that first enqueues it — this handler is that job kind's
// sole consumer).
type ScopeExpansionReconcileHandler struct {
	uow ports.UnitOfWork
	ids idsource.Source
}

// NewScopeExpansionReconcileHandler returns a ready-to-register
// ScopeExpansionReconcileHandler.
func NewScopeExpansionReconcileHandler(uow ports.UnitOfWork, ids idsource.Source) *ScopeExpansionReconcileHandler {
	return &ScopeExpansionReconcileHandler{uow: uow, ids: ids}
}

var _ workerpool.Handler = (*ScopeExpansionReconcileHandler)(nil)

// Handle implements workerpool.Handler for work.ScopeExpansionReconcileJobKind.
// See this file's own package doc comment for the full state machine.
func (h *ScopeExpansionReconcileHandler) Handle(ctx context.Context, job ports.DurableJob) error {
	var payload work.ScopeExpansionReconcileJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("runtime: unmarshal %s job %s payload: %w", work.ScopeExpansionReconcileJobKind, job.ID, err)
	}
	if payload.AttemptID == "" {
		return fmt.Errorf("runtime: %s job %s payload missing attemptId", work.ScopeExpansionReconcileJobKind, job.ID)
	}

	return h.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		origin, err := tx.Runtime().GetScopeExpansionOriginByAttemptID(ctx, payload.AttemptID)
		if err != nil {
			return err
		}
		if origin.ReconcileStatus != runtimedomain.ScopeExpansionReconcilePending {
			// Already terminal (REACTIVATED/REJECTED/NEEDS_RECOVERY) — a
			// late/duplicate delivery, no-op.
			return nil
		}
		if origin.PollGeneration != payload.PollGeneration {
			// Stale generation: a newer successor already exists (or
			// already resolved this origin) — this delivery is superseded,
			// no-op. Never re-enqueue from a stale generation, or duplicate
			// delivery could mint two successors.
			return nil
		}

		run, err := tx.Runtime().GetWorkflowRun(ctx, string(origin.RunID))
		if err != nil {
			return err
		}

		request, err := tx.Work().GetScopeExpansionRequest(ctx, origin.RequestID)
		if err != nil {
			return err
		}

		switch request.Status {
		case workdomain.ScopeExpansionPending:
			// Defensive: ApproveScopeExpansion is the only real enqueuer of
			// this job's own first generation, and it only ever fires
			// after a real decision — should be unreachable in practice.
			// No-op rather than guessing.
			return nil
		case workdomain.ScopeExpansionRejected, workdomain.ScopeExpansionWithdrawn:
			_, err := tx.Runtime().TransitionScopeExpansionOrigin(ctx, ports.TransitionScopeExpansionOriginRequest{
				AttemptID: payload.AttemptID, ExpectedVersion: origin.Version,
				NextReconcileStatus: runtimedomain.ScopeExpansionReconcileRejected,
			})
			return err
		case workdomain.ScopeExpansionApproved:
			// Falls through below — the only status that can ever reach
			// reactivation.
		default:
			return fmt.Errorf("runtime: scope expansion request %s has unexpected status %q", origin.RequestID, request.Status)
		}

		set, err := tx.Work().GetWorkspaceSetByFamilyID(ctx, origin.FamilyID)
		if err != nil {
			return err
		}

		switch set.State {
		case workspace.WorkspaceSetRequested, workspace.WorkspaceSetProvisioning:
			return enqueueScopeExpansionReconcileSuccessorTx(ctx, tx, h.ids, run, origin)
		case workspace.WorkspaceSetReady:
			if !workspaceSetCoversEveryGrant(set, request.RequestedGrants) {
				// A READY snapshot that predates this exact approval (or a
				// stale read) — not actually covering the newly-requested
				// repository yet. Poll again rather than trusting State
				// alone.
				return enqueueScopeExpansionReconcileSuccessorTx(ctx, tx, h.ids, run, origin)
			}
			return reactivateBlockedNodeRunTx(ctx, tx, h.ids, run, origin, request)
		default:
			// BLOCKED/RELEASING/RELEASED/FAILED: never poll forever against
			// a WorkspaceSet that will not become READY on its own.
			_, err := tx.Runtime().TransitionScopeExpansionOrigin(ctx, ports.TransitionScopeExpansionOriginRequest{
				AttemptID: payload.AttemptID, ExpectedVersion: origin.Version,
				NextReconcileStatus: runtimedomain.ScopeExpansionReconcileNeedsRecovery,
			})
			return err
		}
	})
}

// workspaceSetCoversEveryGrant reports whether set's own BaseRevisionSet
// (only ever non-nil once set reached READY) actually carries a Revision
// for every repository grants names — the defensive check confirmed with
// the user before writing this file: State alone can be a stale read of a
// READY snapshot computed BEFORE this exact approval's own newly-required
// repository was added to the family's required set.
func workspaceSetCoversEveryGrant(set workspace.WorkspaceSet, grants []workdomain.RequestedGrant) bool {
	if set.BaseRevisionSet == nil {
		return false
	}
	for _, grant := range grants {
		if _, found := set.BaseRevisionSet.RevisionFor(grant.RepositoryID); !found {
			return false
		}
	}
	return true
}

// enqueueScopeExpansionReconcileSuccessorTx bumps PollGeneration (fenced,
// so a duplicate delivery of THIS SAME attempt can never mint two
// successors) and enqueues the next SCOPE_EXPANSION_RECONCILE job at an
// increasing, capped backoff.
func enqueueScopeExpansionReconcileSuccessorTx(
	ctx context.Context, tx ports.Tx, ids idsource.Source, run runtimedomain.WorkflowRun, origin runtimedomain.ScopeExpansionOrigin,
) error {
	nextGeneration := origin.PollGeneration + 1
	if _, err := tx.Runtime().TransitionScopeExpansionOrigin(ctx, ports.TransitionScopeExpansionOriginRequest{
		AttemptID: string(origin.AttemptID), ExpectedVersion: origin.Version,
		NextReconcileStatus: runtimedomain.ScopeExpansionReconcilePending, NextPollGeneration: &nextGeneration,
	}); err != nil {
		return err
	}

	backoffSeconds := scopeExpansionReconcileBaseBackoffSeconds
	for i := uint64(0); i < nextGeneration && backoffSeconds < scopeExpansionReconcileMaxBackoffSeconds; i++ {
		backoffSeconds *= 2
	}
	if backoffSeconds > scopeExpansionReconcileMaxBackoffSeconds {
		backoffSeconds = scopeExpansionReconcileMaxBackoffSeconds
	}

	payload, err := json.Marshal(work.ScopeExpansionReconcileJobPayload{AttemptID: string(origin.AttemptID), PollGeneration: nextGeneration})
	if err != nil {
		return fmt.Errorf("marshal %s job payload: %w", work.ScopeExpansionReconcileJobKind, err)
	}
	_, err = tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: ports.JobID(ids.NewID()), ProjectID: run.ProjectID, Kind: work.ScopeExpansionReconcileJobKind,
		AggregateType: "ExecutionAttempt", AggregateID: string(origin.AttemptID), Payload: payload,
		AvailableAt:    time.Now().UTC().Add(time.Duration(backoffSeconds) * time.Second),
		MaxClaims:      defaultScopeExpansionReconcileJobMaxClaims,
		IdempotencyKey: fmt.Sprintf("scope-expansion-reconcile:%s:%d", origin.AttemptID, nextGeneration),
	})
	return err
}

// reactivateBlockedNodeRunTx is V4-12A's own closing step, confirmed with
// the user before writing this file: append the RunManifestAmendment,
// create EXACTLY ONE new NodeRun activation (NodeKey/Iteration/
// BranchTokenID copied unchanged from the original BLOCKED activation —
// Iteration is copied, never incremented, so a reactivation never
// consumes a V4-07 cycle budget; BranchTokenID is copied so a FORK/JOIN
// this NodeRun belongs to keeps tracking the right branch), pin its own
// new EffectiveScope by extending the WorkItem's own snapshot
// (AddEffectiveScope, the same mechanism CreateChildWorkItem already
// uses) with exactly the grants this request approved, and hand off to
// the EXISTING ScheduleExecutableNodeRun pipeline (via a plain
// ScheduleNodeRunJobKind job) rather than re-deriving
// EffectiveScope/ManifestRevision/ExecutionProfileHash a second way here.
func reactivateBlockedNodeRunTx(
	ctx context.Context, tx ports.Tx, ids idsource.Source,
	run runtimedomain.WorkflowRun, origin runtimedomain.ScopeExpansionOrigin, request workdomain.ScopeExpansionRequest,
) error {
	if origin.ReactivatedNodeRunID != nil {
		// Already reactivated by an earlier winner — idempotent no-op.
		return nil
	}
	if run.State != runtimedomain.WorkflowRunRunning && run.State != runtimedomain.WorkflowRunWaiting {
		// The Run left RUNNING/WAITING for an unrelated reason (a future
		// V4-12B cancellation, or some other terminal transition) since
		// this Attempt went BLOCKED — reactivating now would create
		// orphaned work with nothing left to route it. Needs an operator
		// to notice and decide, not an infinite retry loop against an
		// already-decided Run.
		_, err := tx.Runtime().TransitionScopeExpansionOrigin(ctx, ports.TransitionScopeExpansionOriginRequest{
			AttemptID: string(origin.AttemptID), ExpectedVersion: origin.Version,
			NextReconcileStatus: runtimedomain.ScopeExpansionReconcileNeedsRecovery,
		})
		return err
	}

	blockedNodeRun, err := tx.Runtime().GetNodeRun(ctx, string(origin.NodeRunID))
	if err != nil {
		return err
	}
	if blockedNodeRun.State != runtimedomain.NodeRunBlocked {
		// Already reactivated (or moved on for some other reason) —
		// idempotent no-op.
		return nil
	}
	if request.ApprovedScopeVersion == nil {
		return fmt.Errorf("runtime: scope expansion request %s is APPROVED but has no ApprovedScopeVersion", origin.RequestID)
	}

	now := time.Now().UTC()
	for _, grant := range request.RequestedGrants {
		scope, err := workdomain.NewRepositoryScope(
			workdomain.TaskFamilyID(origin.FamilyID), *request.ApprovedScopeVersion, grant.RepositoryID, grant.Access,
			grant.PathScopes, grant.Reason, scopeExpansionSystemActor, now,
		)
		if err != nil {
			return err
		}
		if _, err := tx.Work().AddEffectiveScope(ctx, origin.WorkItemID, scope); err != nil {
			return err
		}
	}

	amendments, err := tx.Runtime().ListRunManifestAmendments(ctx, string(origin.RunID))
	if err != nil {
		return err
	}
	var previousRevision uint64
	if len(amendments) > 0 {
		previousRevision = amendments[len(amendments)-1].Revision
	}
	amendmentID := ids.NewID()
	amendment, err := runtimedomain.NewRunManifestAmendment(
		runtimedomain.RunManifestAmendmentID(amendmentID), origin.RunID, previousRevision+1, previousRevision,
		*request.ApprovedScopeVersion, origin.Proposal.Reason, scopeExpansionSystemActor, now, origin.ProposalHash,
	)
	if err != nil {
		return err
	}
	if _, err := tx.Runtime().AppendRunManifestAmendment(ctx, amendment); err != nil {
		return err
	}

	nodeRuns, err := tx.Runtime().ListNodeRunsForRun(ctx, string(origin.RunID))
	if err != nil {
		return err
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
		return err
	}
	reactivated.BranchTokenID = blockedNodeRun.BranchTokenID
	reactivated.ReactivationReason = "SCOPE_EXPANDED"
	if _, err := tx.Runtime().CreateNodeRun(ctx, reactivated); err != nil {
		return err
	}
	// Deliberately no DecisionArtifact copy here: the SCHEDULE_NODE_RUN job
	// enqueued below runs the real ScheduleExecutableNodeRun, which records
	// its own EXECUTION_PROFILE_V1 DecisionArtifact under this exact
	// deterministic ID (NodeRunID+"-execution-profile-v1", schedule.go) —
	// V4-14's own real end-to-end run through a real workerpool.Pool caught
	// an earlier version of this function pre-writing that SAME artifact
	// ID here "for ExecuteNodeHandler's own loadExecutionProfile", which
	// collided with ScheduleExecutableNodeRun's own real write
	// (ErrPersistenceAlreadyExists) every single time, permanently
	// stranding the reactivated NodeRun at PENDING. The reactivated
	// NodeRun always goes through a REAL SCHEDULE_NODE_RUN dispatch (this
	// is not a shortcut path straight to EXECUTE_NODE), so nothing else
	// needs the profile decision to exist before that job runs.
	nextReactivated := reactivatedID
	if _, err := tx.Runtime().TransitionScopeExpansionOrigin(ctx, ports.TransitionScopeExpansionOriginRequest{
		AttemptID: string(origin.AttemptID), ExpectedVersion: origin.Version,
		NextReconcileStatus: runtimedomain.ScopeExpansionReconcileReactivated, NextReactivatedNodeRunID: &nextReactivated,
	}); err != nil {
		return err
	}

	// V4-12C: this reactivation is the ONLY event with authority to resolve
	// the SCOPE_EXPANSION_REQUIRED blocker requestScopeExpansionTx's own
	// BLOCKED branch opened (finalize.go) — never ResolveWorkItemBlocker,
	// the public command (workdomain.BlockerType.ResolvableViaCommand's own
	// doc comment). Unblocks the WorkItem back to ACTIVE (never READY — the
	// SAME Run resumes, it never stopped), but only when this was its own
	// last remaining OPEN blocker and no WorkItem-level cancellation is
	// pending (closeWorkItemBlockerTx's own two-separate-conditions rule).
	// ErrPersistenceNotFound is tolerated as a no-op: a ScopeExpansionOrigin
	// seeded directly (bypassing requestScopeExpansionTx, e.g. an
	// origin/fixture predating this wiring) has no corresponding blocker to
	// resolve at all — nothing about the reactivation itself depends on one
	// existing.
	blockerID := string(origin.AttemptID) + "-scope-expansion-blocker"
	blocker, err := tx.Work().GetWorkItemBlocker(ctx, blockerID)
	switch {
	case err == nil && blocker.State == workdomain.BlockerOpen:
		if _, err := closeWorkItemBlockerTx(
			ctx, tx, blocker, workdomain.BlockerResolved, scopeExpansionSystemActor,
			"scope expansion approved and reactivated", "", workdomain.WorkItemActive, "", "",
		); err != nil {
			return err
		}
	case err != nil && !errors.Is(err, ports.ErrPersistenceNotFound):
		return err
	}

	schedulePayload, err := json.Marshal(ScheduleNodeRunJobPayload{RunID: string(run.ID), NodeRunID: reactivatedID})
	if err != nil {
		return fmt.Errorf("marshal %s job payload: %w", ScheduleNodeRunJobKind, err)
	}
	_, err = tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: ports.JobID(ids.NewID()), ProjectID: run.ProjectID, Kind: ScheduleNodeRunJobKind,
		AggregateType: "NodeRun", AggregateID: reactivatedID, Payload: schedulePayload,
		MaxClaims: defaultScheduleNodeRunJobMaxClaims, IdempotencyKey: "schedule-" + reactivatedID,
	})
	return err
}
