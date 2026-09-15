// MarkWorkItemReady (V6-04A, docs/design/08-v6-api-projections.md V6-04A;
// ADR-028 §30) is the single, narrow public command that closes the
// BACKLOG -> READY gap ExplainWorkItemReadiness (queries.go) already
// explains read-only but explicitly never performs itself — that function's
// own doc comment names this exact command as "V6-04A's own separate job".
// ADR-028 §30 locks its contract in full: "server chạy lại readiness/
// contract validator và CAS đúng transition đó, append registered
// WORK_ITEM_MARKED_READY v1. Command không nhận target status." This file
// is that gap closed:
//
//   - reload the real, current WorkItem (never a projection, never a value
//     the caller supplies beyond its own ID);
//   - re-run the REAL workdomain.ValidateReadinessGate validator against it
//     — the exact same validator, same vocabulary, ExplainWorkItemReadiness
//     already runs read-only (queries.go), never a second, reinvented
//     check;
//   - only if that passes, atomically CAS the WorkItem's own Status
//     BACKLOG -> READY (reusing ports.WorkRepository.TransitionWorkItemStatus,
//     the same optimistic-concurrency CAS primitive V4-02's own
//     StartWorkflowRun READY->ACTIVE transition already established for
//     this exact column — this command is simply CanTransitionWorkItemStatus's
//     other, narrower caller, not a new transition primitive);
//   - append one registered WorkItemMarkedReady v1 domain event and the
//     command receipt every mutating command in this codebase already gets.
//
// This command deliberately never accepts a target status of any kind
// (MarkWorkItemReadyRequest carries only WorkItemID) — go-core-spec's own
// closed WorkItem-transition-authority table names MarkWorkItemReady as the
// ONE way to close BACKLOG->READY, and every other transition keeps its own
// existing, separate authority (StartWorkflowRun for READY->ACTIVE,
// blocker/scope handling, CompletionPolicy, cancellation) — this file never
// touches any of those.
//
// Eligibility vs. readiness are two DISTINCT rejection reasons, deliberately
// checked and reported differently:
//
//   - "not eligible" (ErrWorkItemNotEligibleForReady): the WorkItem's own
//     current Status is not BACKLOG at all (already READY — including the
//     literal "READY conflict" ADR-028/this task's own spec names for a
//     fresh Idempotency-Key arriving after an earlier call already
//     succeeded — or ACTIVE/BLOCKED/DONE/CANCELLED). There is nothing for
//     ValidateReadinessGate to even usefully evaluate here; this is a state
//     conflict, not a completeness problem, and the HTTP layer maps it to a
//     plain 409 CONFLICT with no problem list.
//   - "not ready" (workdomain.ReadinessError, returned verbatim, unwrapped):
//     the WorkItem IS BACKLOG but fails ValidateReadinessGate's own
//     completeness checks. This command returns that exact
//     *workdomain.ReadinessError value — never a generic wrapper, never a
//     re-derived message — so a caller (or its own HTTP layer) can report
//     the SAME problem list ExplainWorkItemReadiness would report for the
//     identical WorkItem, this task's own explicit "reuse that exact logic/
//     vocabulary, don't reinvent it" bar.
package work

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// ErrWorkItemNotEligibleForReady is returned by MarkWorkItemReady when the
// targeted WorkItem's own current Status is not BACKLOG — including the
// already-READY case (a fresh Idempotency-Key arriving after an earlier
// MarkWorkItemReady call already succeeded, this task's own explicit "key
// mới khi READY conflict" line) and every other non-BACKLOG status. See this
// file's own doc comment for why this is reported separately from a
// workdomain.ReadinessError.
var ErrWorkItemNotEligibleForReady = errors.New("work: work item is not eligible to transition to READY")

// MarkWorkItemReadyRequest is what a caller supplies to MarkWorkItemReady —
// deliberately just the target's own ID, nothing else: ADR-028's own
// "Command không nhận target status" bar is satisfied by construction, there
// is no field anywhere on this request a caller could even attempt to name a
// different transition with.
type MarkWorkItemReadyRequest struct {
	WorkItemID string
}

// MarkWorkItemReadyResult is what MarkWorkItemReady returns (and what a
// replayed command receipt reconstructs).
type MarkWorkItemReadyResult struct {
	WorkItemID string `json:"workItemId"`
	ProjectID  string `json:"projectId"`
	FamilyID   string `json:"familyId"`
	Status     string `json:"status"`
	Version    uint64 `json:"version"`
}

// MarkWorkItemReady is the single public command that closes BACKLOG->READY
// — see this file's own doc comment for the full contract.
func MarkWorkItemReady(ctx context.Context, uow ports.UnitOfWork, cmd ports.Command, req MarkWorkItemReadyRequest) (MarkWorkItemReadyResult, error) {
	if strings.TrimSpace(req.WorkItemID) == "" {
		return MarkWorkItemReadyResult{}, errors.New("work: WorkItemID is required")
	}

	var result MarkWorkItemReadyResult
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

		// Reload the real, current WorkItem fresh, inside this same
		// transaction — never a value the caller supplies beyond its own ID,
		// never a projection.
		item, err := tx.Work().GetWorkItem(ctx, req.WorkItemID)
		if err != nil {
			return err
		}

		if item.Status != workdomain.WorkItemBacklog {
			return fmt.Errorf("%w: work item %s is %s", ErrWorkItemNotEligibleForReady, req.WorkItemID, item.Status)
		}

		// The REAL canonical validator, the exact same one
		// ExplainWorkItemReadiness already runs read-only — returned
		// verbatim (a *workdomain.ReadinessError) rather than wrapped, so a
		// caller sees the identical problem list/vocabulary.
		if err := workdomain.ValidateReadinessGate(item); err != nil {
			return err
		}

		updated, err := tx.Work().TransitionWorkItemStatus(ctx, ports.TransitionWorkItemStatusRequest{
			WorkItemID: req.WorkItemID, ExpectedStatus: workdomain.WorkItemBacklog, ExpectedVersion: item.Version,
			NextStatus: workdomain.WorkItemReady,
		})
		if err != nil {
			return err
		}

		eventPayload, err := json.Marshal(workItemMarkedReadyEventPayload{
			WorkItemID: req.WorkItemID, ProjectID: string(item.ProjectID), FamilyID: string(item.FamilyID),
			MarkedBy: cmd.Actor,
		})
		if err != nil {
			return fmt.Errorf("marshal WorkItemMarkedReady payload: %w", err)
		}
		// AggregateType/AggregateID deliberately mint a fresh, decision-scoped
		// aggregate identity keyed by cmd.ID rather than reusing
		// "WorkItem"/req.WorkItemID: this WorkItem's own aggregate identity
		// already carries a Sequence=1 event from its own creation
		// (RootWorkItemCreated/ChildWorkItemCreated, commands.go) —
		// domain_events' own UNIQUE(aggregate_type, aggregate_id, sequence)
		// constraint would reject a second Sequence=1 row there. This mirrors
		// ApproveScopeExpansion/RejectScopeExpansion/WithdrawScopeExpansion's
		// own identical "second decision on an already-existing aggregate"
		// pattern (scope_expansion.go) exactly.
		if err := tx.Events().Append(ctx, ports.DomainEvent{
			ID: cmd.ID + "-marked-ready", ProjectID: string(item.ProjectID),
			AggregateType: "WorkItemReadyMark", AggregateID: cmd.ID, Sequence: 1,
			EventType: WorkItemMarkedReadyEventType, SchemaVersion: WorkItemMarkedReadySchemaVersion, PayloadJSON: string(eventPayload),
			CorrelationID: cmd.CorrelationID, CreatedAt: cmd.RequestedAt,
		}); err != nil {
			return err
		}

		result = MarkWorkItemReadyResult{
			WorkItemID: req.WorkItemID, ProjectID: string(item.ProjectID), FamilyID: string(item.FamilyID),
			Status: string(updated.Status), Version: updated.Version,
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
