// APPROVAL semantics (V4-09, docs/design/06-v4-runtime-engine.md,
// HE-14-S03/GC-INV-11/HE-08-M08). advance.go's own advanceRunTx creates an
// ApprovalRequest inline the moment an APPROVAL node is activated (see its
// own "case isApprovalNode" branch) — this file owns everything that
// happens AFTER that: the durable TIMER job that wakes a request up at its
// own due time (ApprovalTimeoutHandler), and the public command a typed
// operator decision arrives through (ResolveApproval).
//
// GC-INV-11's own "outcome từ agent/command phải thuộc allow-list của node
// trước khi routing" already has real enforcement (outcomeDeclared/
// findEdge, advanceRunTx) — unlike V4-08's WAIT (where SignalWait's own
// signal carries no outcome information at all), an operator's decision
// directly supplies which of the node's own declared Outcomes it maps to,
// so ResolveApproval needs no CompletionOutcome/TimeoutOutcome-style
// pre-pinning the way WaitNodeConfig needed for its own two, ambiguous
// completion paths. The one outcome that IS pre-pinned, EscalationOutcome,
// is what the timer uses on timeout — never chosen by the timer itself.
//
// Authorization (confirmed with the user before writing this task's code):
// ports.Command gains ActorRoles — authentication context the transport/
// API layer populates from its own local session, never decoded out of a
// request body and never part of RequestHash. ResolveApproval checks an
// exact, case-sensitive set-intersection between cmd.ActorRoles and the
// ApprovalRequest's own pinned AuthorizedRoles; an empty intersection is
// rejected as errorcode.CodePolicyDenied before this call ever touches the
// request's own state — no approval is written, no NodeRun is routed, and
// the rejection happens identically whether the request is still PENDING
// or already resolved by someone else.
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// ApprovalTimerJobKind is the durable job advanceRunTx enqueues (with
// AvailableAt = the request's own DueAt) whenever an APPROVAL node is
// activated — see advanceRunTx's own "case isApprovalNode" branch.
// Unlike V4-08's WaitTimerJobKind, this one is never conditional:
// ApprovalNodeConfig.TimeoutSeconds is always required and positive.
const ApprovalTimerJobKind = "APPROVAL_TIMER"

const defaultApprovalTimerJobMaxClaims = 3

// ApprovalTimerJobPayload is the exact JSON shape advanceRunTx marshals
// for an ApprovalTimerJobKind job and ApprovalTimeoutHandler unmarshals.
type ApprovalTimerJobPayload struct {
	RunID             string `json:"runId"`
	NodeRunID         string `json:"nodeRunId"`
	ApprovalRequestID string `json:"approvalRequestId"`
	CorrelationID     string `json:"correlationId,omitempty"`
}

// ApprovalTimeoutHandler is a ready-to-register workerpool.Handler for
// ApprovalTimerJobKind.
type ApprovalTimeoutHandler struct {
	uow ports.UnitOfWork
	ids idsource.Source
}

// NewApprovalTimeoutHandler returns a ready-to-register ApprovalTimeoutHandler.
func NewApprovalTimeoutHandler(uow ports.UnitOfWork, ids idsource.Source) *ApprovalTimeoutHandler {
	return &ApprovalTimeoutHandler{uow: uow, ids: ids}
}

var _ workerpool.Handler = (*ApprovalTimeoutHandler)(nil)

// Handle implements workerpool.Handler for ApprovalTimerJobKind. Firing
// this job never itself decides the approval — it only attempts the SAME
// fenced CAS a winning ResolveApproval would, and does nothing at all if
// it loses (the request is no longer PENDING, e.g. already DECIDED by an
// operator who responded just before this job fired) — GC-INV-31's own
// "durable job chỉ đánh thức timer và không phải authority" discipline,
// applied here to APPROVAL exactly as V4-08's WaitTimeoutHandler already
// applies it to WAIT.
func (h *ApprovalTimeoutHandler) Handle(ctx context.Context, job ports.DurableJob) error {
	var payload ApprovalTimerJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("runtime: unmarshal %s job %s payload: %w", ApprovalTimerJobKind, job.ID, err)
	}
	if payload.RunID == "" || payload.NodeRunID == "" || payload.ApprovalRequestID == "" {
		return fmt.Errorf("runtime: %s job %s payload missing runId/nodeRunId/approvalRequestId", ApprovalTimerJobKind, job.ID)
	}

	return h.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		request, err := tx.Approvals().GetApprovalRequest(ctx, payload.ApprovalRequestID)
		if err != nil {
			return err
		}
		if request.State != runtimedomain.ApprovalRequestPending {
			// Idempotent no-op — see this file's own package doc comment.
			return nil
		}

		if _, err := tx.Approvals().TransitionApprovalRequest(ctx, ports.TransitionApprovalRequestRequest{
			ApprovalRequestID: payload.ApprovalRequestID, ExpectedState: runtimedomain.ApprovalRequestPending, ExpectedVersion: request.Version,
			NextState: runtimedomain.ApprovalRequestEscalated,
		}); err != nil {
			if errors.Is(err, ports.ErrOptimisticConflict) {
				// Lost the race to a decision that won in between our own
				// read and this CAS attempt — idempotent no-op, same as
				// above.
				return nil
			}
			return err
		}

		_, err = advanceRunTx(ctx, tx, h.ids, AdvanceRunRequest{
			RunID: payload.RunID, NodeRunID: payload.NodeRunID, Outcome: request.EscalationOutcome, CorrelationID: payload.CorrelationID, JobID: string(job.ID),
		})
		return err
	})
}

// ResolveApprovalRequest is what a caller supplies to ResolveApproval.
type ResolveApprovalRequest struct {
	RunID             string
	ApprovalRequestID string
	// Outcome is the exact declared Outcome this operator's decision maps
	// to (e.g. an authoring vocabulary of "approved"/"rejected", or
	// whatever richer set the node itself declares) — GC-INV-11's own
	// allow-list check (advanceRunTx) is what actually validates it, not
	// this command.
	Outcome string
	Reason  string
}

// ResolveApprovalResult is ResolveApproval's own idempotent result.
type ResolveApprovalResult struct {
	ApprovalRequestID string `json:"approvalRequestId"`
	State             string `json:"state"`
	// Won reports whether THIS call's own decision is the one that
	// actually resolved the request (false when the request was already
	// non-PENDING — resolved by an earlier decision or the timer — by the
	// time this call's own fenced CAS ran).
	Won           bool   `json:"won"`
	MatchedRole   string `json:"matchedRole,omitempty"`
	Advanced      bool   `json:"advanced"`
	NextNodeRunID string `json:"nextNodeRunId,omitempty"`
	NextNodeKey   string `json:"nextNodeKey,omitempty"`
}

// ResolveApproval is V4-09's own public command: the sole entry point a
// typed operator decision goes through (HE-14-S03's own "chat không tự
// resolve" — nothing else in this codebase may transition an
// ApprovalRequest to DECIDED). It follows V1-06's idempotent-command shape
// exactly like StartWorkflowRun/SignalWait — a retry with the same
// cmd.IdempotencyKey and cmd.RequestHash replays the first call's result;
// the same key with a different RequestHash is rejected as
// ports.ErrReceiptConflict. Authorization is checked BEFORE anything else
// (including before the request's own current state is even considered) —
// see this file's own package doc comment for the full contract.
func ResolveApproval(ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, cmd ports.Command, req ResolveApprovalRequest) (ResolveApprovalResult, error) {
	if req.RunID == "" || req.ApprovalRequestID == "" || req.Outcome == "" {
		return ResolveApprovalResult{}, errors.New("runtime: RunID, ApprovalRequestID and Outcome are required")
	}

	var result ResolveApprovalResult
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

		request, err := tx.Approvals().GetApprovalRequest(ctx, req.ApprovalRequestID)
		if err != nil {
			return err
		}
		if string(request.RunID) != req.RunID {
			return fmt.Errorf("%w: approval request %s belongs to run %s, not %s", ErrNodeRunMismatch, req.ApprovalRequestID, request.RunID, req.RunID)
		}

		matchedRole := matchAuthorizedRole(cmd.ActorRoles, request.AuthorizedRoles)
		if matchedRole == "" {
			return apperror.New(
				errorcode.CodePolicyDenied,
				fmt.Sprintf("actor %q is not authorized to resolve approval request %s", cmd.Actor, req.ApprovalRequestID),
				false,
			)
		}

		if request.State == runtimedomain.ApprovalRequestPending {
			updated, casErr := tx.Approvals().TransitionApprovalRequest(ctx, ports.TransitionApprovalRequestRequest{
				ApprovalRequestID: req.ApprovalRequestID, ExpectedState: runtimedomain.ApprovalRequestPending, ExpectedVersion: request.Version,
				NextState: runtimedomain.ApprovalRequestDecided, DecidedBy: cmd.Actor, DecidedRole: matchedRole,
				DecidedOutcome: req.Outcome, Reason: req.Reason, DecidedAt: cmd.RequestedAt,
			})
			switch {
			case casErr == nil:
				advanceResult, err := advanceRunTx(ctx, tx, ids, AdvanceRunRequest{
					RunID: req.RunID, NodeRunID: string(request.NodeRunID), Outcome: req.Outcome,
					CorrelationID: cmd.CorrelationID, JobID: cmd.ID,
				})
				if err != nil {
					return err
				}
				result = ResolveApprovalResult{
					ApprovalRequestID: req.ApprovalRequestID, State: string(updated.State), Won: true, MatchedRole: matchedRole,
					Advanced: advanceResult.Advanced, NextNodeRunID: advanceResult.NextNodeRunID, NextNodeKey: advanceResult.NextNodeKey,
				}
			case errors.Is(casErr, ports.ErrOptimisticConflict):
				// Lost the race to something else (another decision, or
				// the timer) that resolved this request in between our
				// own read and this CAS attempt.
				current, reErr := tx.Approvals().GetApprovalRequest(ctx, req.ApprovalRequestID)
				if reErr != nil {
					return reErr
				}
				result = ResolveApprovalResult{ApprovalRequestID: req.ApprovalRequestID, State: string(current.State), Won: false}
			default:
				return casErr
			}
		} else {
			result = ResolveApprovalResult{ApprovalRequestID: req.ApprovalRequestID, State: string(request.State), Won: false}
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

// matchAuthorizedRole returns the first role in actorRoles that also
// appears in authorizedRoles (exact, case-sensitive match, confirmed with
// the user before writing this task's code), or "" if none does.
func matchAuthorizedRole(actorRoles, authorizedRoles []string) string {
	authorized := make(map[string]bool, len(authorizedRoles))
	for _, role := range authorizedRoles {
		authorized[role] = true
	}
	for _, role := range actorRoles {
		if authorized[role] {
			return role
		}
	}
	return ""
}
