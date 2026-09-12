// Package runtime is V4-02's application-command layer
// (docs/design/06-v4-runtime-engine.md), mirroring the domain package it
// orchestrates, internal/domain/runtime — the same "app package named after
// the domain package it orchestrates" convention internal/app/work already
// follows for internal/domain/work.
package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// AdvanceRunJobKind is the durable job StartWorkflowRun enqueues to trigger
// routing past the START node it just activated. It is deliberately not an
// EXECUTE_NODE-style worker job: START has no real work to run, so nothing
// needs a WriteLease or a process spawn for it — a scheduler consumes this
// job to decide what comes next (V4-03's own "Deterministic activation/
// router core"; V4-04 first gives an EXECUTABLE node's activation a real
// ExecutionAttempt + EXECUTE_NODE-style job). No consumer for this job kind
// exists yet in this codebase; it sits AVAILABLE until V4-03 builds one,
// the same "enqueue only, a separate later task is the only consumer"
// relationship internal/app/catalog.RegisterRepository's REPOSITORY_PROBE
// job and internal/app/work.CreateRootWorkItem's WORKSPACE_PROVISION jobs
// already established.
const AdvanceRunJobKind = "ADVANCE_RUN"

const defaultAdvanceRunJobMaxClaims = 3

var (
	// ErrWorkItemNotReady is returned when StartWorkflowRun's target
	// WorkItem is not currently READY. Only a READY WorkItem may start a
	// run: this also is how "một WorkItem policy chỉ có số active run cho
	// phép" (V4-02's own Hoàn thành khi) is enforced structurally — a
	// WorkItem already ACTIVE from a prior run in flight is never READY,
	// so a second StartWorkflowRun call for it is rejected here (or, in a
	// true concurrent race, by TransitionWorkItemStatus's own CAS instead).
	ErrWorkItemNotReady = errors.New("runtime: work item is not READY")
	// ErrWorkspaceNotReady is returned when the WorkItem's family
	// WorkspaceSet is not currently READY — a run cannot start against a
	// workspace that has no committed base revision to pin.
	ErrWorkspaceNotReady = errors.New("runtime: workspace set is not READY")
	// ErrWorkflowVersionMismatch is returned when the WorkItem already
	// pins a specific WorkflowVersionID and the caller requested a
	// different one — "version compatibility" (this task's own Thực hiện
	// line).
	ErrWorkflowVersionMismatch = errors.New("runtime: requested workflow version does not match the work item's pinned version")
	// ErrWorkItemCancellationPending is GC-INV-39's own fence: a WorkItem
	// with a still-open (REQUESTED) cancellation intent must never be
	// admitted into a new run, even before the (not-yet-built) coordinator
	// has terminalized it.
	ErrWorkItemCancellationPending = errors.New("runtime: work item has a pending cancellation intent")
	// ErrMissingStartNode is a defensive, should-be-unreachable error: the
	// workflow compiler already rejects any document without exactly one
	// START node before it can ever be published (validateNormalizedDocument).
	// This exists so a corrupted/foreign WorkflowVersion fails closed here
	// rather than panicking.
	ErrMissingStartNode = errors.New("runtime: workflow version has no START node")
)

// StartWorkflowRunRequest is what a caller supplies to StartWorkflowRun.
type StartWorkflowRunRequest struct {
	ProjectID         string
	WorkItemID        string
	WorkflowVersionID string
}

// StartWorkflowRunResult is StartWorkflowRun's own idempotent result — what
// a replayed command-receipt reconstructs.
type StartWorkflowRunResult struct {
	RunID      string `json:"runId"`
	ProjectID  string `json:"projectId"`
	WorkItemID string `json:"workItemId"`
	FamilyID   string `json:"familyId"`
	State      string `json:"state"`
	NodeRunID  string `json:"nodeRunId"`
	JobID      string `json:"jobId"`
}

// StartWorkflowRun is the single public command that atomically creates a
// WorkflowRun, its immutable ExecutionManifest, the START node's NodeRun
// activation and one AdvanceRunJobKind durable job, and transitions the
// owning WorkItem READY -> ACTIVE — all inside one
// ports.UnitOfWork.WithSerializedWrite call (V4-02's own "tạo run,
// manifest, START activation, checkpoint/event và advance job atomically";
// go-core-spec §7's "StartWorkflowRun tạo run, START NodeRun và checkpoint
// khởi đầu cùng transaction").
//
// "checkpoint" from that Mục tiêu line is deliberately not a literal
// checkpoints table row here: internal/domain/runtime.Checkpoint is
// attempt-scoped (it requires a real ExecutionAttemptID and
// ContextSnapshotID), and START has neither — it is a structural node with
// no execution. The initial state this task's "checkpoint" language is
// really asking to durably capture is the ExecutionManifest's own
// BaseRevisionSet (V4-01) plus the RunStarted domain event this command
// appends; extending Checkpoint to support an attempt-less/context-less
// initial record is a separate design change, out of this task's own
// citations (GC-INV-39 does not mention checkpoints at all).
//
// It follows V1-06's idempotent-command shape exactly like
// internal/app/work.CreateRootWorkItem: a retry with the same
// IdempotencyKey and RequestHash replays the first call's result without
// creating any duplicate row or job; the same key with a different
// RequestHash is rejected as ports.ErrReceiptConflict.
func StartWorkflowRun(ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, cmd ports.Command, req StartWorkflowRunRequest) (StartWorkflowRunResult, error) {
	if req.ProjectID == "" || req.WorkItemID == "" || req.WorkflowVersionID == "" {
		return StartWorkflowRunResult{}, errors.New("runtime: ProjectID, WorkItemID and WorkflowVersionID are required")
	}

	var result StartWorkflowRunResult
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		existingReceipt, found, err := tx.Receipts().Load(ctx, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
		if err != nil {
			return err
		}
		if found {
			if existingReceipt.RequestHash != cmd.RequestHash {
				return ports.ErrReceiptConflict
			}
			return json.Unmarshal([]byte(existingReceipt.ResultJSON), &result)
		}

		item, err := tx.Work().GetWorkItem(ctx, req.WorkItemID)
		if err != nil {
			return err
		}
		if item.Status != workdomain.WorkItemReady {
			return fmt.Errorf("%w: work item %s is %s", ErrWorkItemNotReady, req.WorkItemID, item.Status)
		}
		if item.WorkflowVersionID != nil && string(*item.WorkflowVersionID) != req.WorkflowVersionID {
			return fmt.Errorf(
				"%w: work item %s is pinned to %s, requested %s",
				ErrWorkflowVersionMismatch, req.WorkItemID, *item.WorkflowVersionID, req.WorkflowVersionID,
			)
		}

		// GC-INV-39: a still-open work-item cancellation intent fences
		// admission into a new run, even before any coordinator has
		// terminalized the WorkItem.
		intent, err := tx.Runtime().GetWorkItemCancellationIntent(ctx, req.WorkItemID)
		switch {
		case err == nil:
			if intent.State == runtimedomain.CancellationIntentRequested {
				return fmt.Errorf("%w: work item %s", ErrWorkItemCancellationPending, req.WorkItemID)
			}
		case errors.Is(err, ports.ErrPersistenceNotFound):
			// No intent at all — the common case.
		default:
			return err
		}

		family, err := tx.Work().GetTaskFamily(ctx, string(item.FamilyID))
		if err != nil {
			return err
		}

		workspaceSet, err := tx.Work().GetWorkspaceSetByFamilyID(ctx, string(item.FamilyID))
		if err != nil {
			return err
		}
		if workspaceSet.State != workspace.WorkspaceSetReady || workspaceSet.BaseRevisionSet == nil {
			return fmt.Errorf("%w: workspace set %s is %s", ErrWorkspaceNotReady, workspaceSet.ID, workspaceSet.State)
		}

		version, err := tx.Definitions().GetWorkflowVersion(ctx, req.WorkflowVersionID)
		if err != nil {
			return err
		}
		startNodeKey, err := resolveStartNodeKey(version.Document())
		if err != nil {
			return err
		}

		activated, err := tx.Work().TransitionWorkItemStatus(ctx, ports.TransitionWorkItemStatusRequest{
			WorkItemID: req.WorkItemID, ExpectedStatus: workdomain.WorkItemReady, ExpectedVersion: item.Version,
			NextStatus: workdomain.WorkItemActive,
		})
		if err != nil {
			return err
		}

		runID := ids.NewID()
		sharedState := json.RawMessage(`{}`)
		run, err := runtimedomain.NewWorkflowRun(
			runtimedomain.WorkflowRunID(runID), project.ProjectID(req.ProjectID), activated.ID,
			version, activated.FamilyID, family.ScopeVersion, sharedState,
		)
		if err != nil {
			return err
		}
		startedAt := cmd.RequestedAt
		run.State = runtimedomain.WorkflowRunRunning
		run.StartedAt = &startedAt
		if _, err := tx.Runtime().CreateWorkflowRun(ctx, run); err != nil {
			return err
		}

		manifestID := ids.NewID()
		manifest, err := runtimedomain.NewExecutionManifest(
			runtimedomain.ExecutionManifestID(manifestID), run.ID, version.ID(), version.ContentHash(),
			version.Dependencies(), *workspaceSet.BaseRevisionSet, "", "", cmd.RequestedAt,
		)
		if err != nil {
			return err
		}
		if _, err := tx.Runtime().CreateExecutionManifest(ctx, manifest); err != nil {
			return err
		}

		nodeRunID := ids.NewID()
		nodeRun, err := runtimedomain.NewNodeRun(
			runtimedomain.NodeRunID(nodeRunID), run.ID, startNodeKey, 1, 0, nil,
			canonicalStateHash(sharedState), "",
		)
		if err != nil {
			return err
		}
		nodeRun.State = runtimedomain.NodeRunRunning
		if _, err := tx.Runtime().CreateNodeRun(ctx, nodeRun); err != nil {
			return err
		}

		eventPayload, err := json.Marshal(workflowRunStartedEventPayload{
			RunID: string(run.ID), WorkItemID: req.WorkItemID, NodeRunID: nodeRunID, NodeKey: startNodeKey,
		})
		if err != nil {
			return fmt.Errorf("marshal WorkflowRunStarted payload: %w", err)
		}
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID: cmd.ID + "-started", ProjectID: req.ProjectID,
			AggregateType: "WorkflowRun", AggregateID: string(run.ID), Sequence: 1,
			EventType: WorkflowRunStartedEventType, SchemaVersion: WorkflowRunStartedSchemaVersion, PayloadJSON: string(eventPayload),
			CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
		}); err != nil {
			return err
		}

		// CorrelationID seeds AdvanceRunJobPayload's own chain (correction
		// found during V4-03 review): every downstream AdvanceRun hop
		// forwards it unchanged into its own follow-up job, so the whole
		// run's own sequence of NODE_ROUTED events shares this command's
		// CorrelationID (go-core-spec §20).
		jobPayload, err := json.Marshal(AdvanceRunJobPayload{RunID: string(run.ID), NodeRunID: nodeRunID, CorrelationID: cmd.CorrelationID})
		if err != nil {
			return fmt.Errorf("marshal %s job payload: %w", AdvanceRunJobKind, err)
		}
		jobID := ids.NewID()
		job, err := tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
			ID: ports.JobID(jobID), ProjectID: project.ProjectID(req.ProjectID), Kind: AdvanceRunJobKind,
			AggregateType: "WorkflowRun", AggregateID: string(run.ID), Payload: jobPayload,
			AvailableAt: cmd.RequestedAt, MaxClaims: defaultAdvanceRunJobMaxClaims,
			IdempotencyKey: fmt.Sprintf("%s-advance-%s", cmd.IdempotencyKey, run.ID),
		})
		if err != nil {
			return err
		}

		result = StartWorkflowRunResult{
			RunID: string(run.ID), ProjectID: req.ProjectID, WorkItemID: req.WorkItemID,
			FamilyID: string(activated.FamilyID), State: string(run.State), NodeRunID: nodeRunID, JobID: string(job.ID),
		}
		resultJSON, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("marshal receipt result: %w", err)
		}
		return tx.Receipts().Record(ctx, ports.Receipt{
			Actor: cmd.Actor, Scope: cmd.Scope, IdempotencyKey: cmd.IdempotencyKey,
			CommandType: cmd.Type, RequestHash: cmd.RequestHash, ResultJSON: string(resultJSON),
			CreatedAt: cmd.RequestedAt,
		})
	})
	return result, err
}

// resolveStartNodeKey finds the document's one START node. The workflow
// compiler already refuses to publish a document without exactly one
// (validateNormalizedDocument, docs/design/02-v0-spike-verdict.md-era
// invariant) — this is a defensive fail-closed check, not the primary
// enforcement of that rule.
func resolveStartNodeKey(document workflow.WorkflowDocument) (string, error) {
	for _, node := range document.Nodes {
		if node.Type == workflow.NodeStart {
			return node.Key, nil
		}
	}
	return "", ErrMissingStartNode
}

// canonicalStateHash hashes a canonical JSON payload the same
// "sha256:<hex>" way ContextSnapshot/ExecutionManifest content hashes
// already do elsewhere in this codebase.
func canonicalStateHash(payload json.RawMessage) string {
	digest := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(digest[:])
}
