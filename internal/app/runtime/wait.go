// WAIT persistence and signal semantics (V4-08,
// docs/design/06-v4-runtime-engine.md, GC-INV-31). advance.go's own
// advanceRunTx creates a WaitRegistration inline the moment a WAIT node is
// activated (see its own "case isWaitNode" branch) — this file owns
// everything that happens AFTER that: the durable TIMER job that wakes a
// registration up at its own due time (WaitTimeoutHandler), and the public
// command an external signal arrives through (SignalWait).
//
// Consume-once is enforced by exactly the two mechanisms GC-INV-31 names,
// composed together, never durable_jobs itself: (1) WaitSignal's own
// UNIQUE(wait_registration_id, signal_key) — the same real external event
// reported twice (even via different command invocations/actors) is
// recognized as one signal, never inserted twice; (2) a fenced CAS on
// WaitRegistration.State/Version — exactly one of SignalWait and the timer
// job may ever transition a given registration away from ACTIVE. A
// redelivered/replayed timer job that finds the registration no longer
// ACTIVE is a pure no-op (confirmed with the user before writing this
// task's code): it never creates a second WaitSignal, never re-routes,
// never re-CASes.
//
// SignalWait/the timer job never choose which Outcome to route on: both
// simply race the CAS, and the winner routes using whichever of
// CompletionOutcome/TimeoutOutcome the WaitRegistration itself already
// pinned at registration time (the exact values advanceRunTx resolved from
// the compiled WaitNodeConfig — see internal/domain/runtime/wait.go's own
// doc comment).
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// WaitTimerJobKind is the durable job advanceRunTx enqueues (with
// AvailableAt = the registration's own DueAt) whenever a WAIT node's
// registration has a due time at all — see advanceRunTx's own "case
// isWaitNode" branch.
const WaitTimerJobKind = "WAIT_TIMER"

const defaultWaitTimerJobMaxClaims = 3

// WaitTimerJobPayload is the exact JSON shape advanceRunTx marshals for a
// WaitTimerJobKind job and WaitTimeoutHandler unmarshals.
type WaitTimerJobPayload struct {
	RunID              string `json:"runId"`
	NodeRunID          string `json:"nodeRunId"`
	WaitRegistrationID string `json:"waitRegistrationId"`
	CorrelationID      string `json:"correlationId,omitempty"`
}

// WaitTimeoutHandler is a ready-to-register workerpool.Handler for
// WaitTimerJobKind.
type WaitTimeoutHandler struct {
	uow ports.UnitOfWork
	ids idsource.Source
}

// NewWaitTimeoutHandler returns a ready-to-register WaitTimeoutHandler.
func NewWaitTimeoutHandler(uow ports.UnitOfWork, ids idsource.Source) *WaitTimeoutHandler {
	return &WaitTimeoutHandler{uow: uow, ids: ids}
}

var _ workerpool.Handler = (*WaitTimeoutHandler)(nil)

// Handle implements workerpool.Handler for WaitTimerJobKind. It is
// GC-INV-31's own "durable job chỉ đánh thức timer và không phải authority
// của signal" made concrete: firing this job never itself decides whether
// the wait is over — it only attempts the SAME fenced CAS a winning
// SignalWait would, and does nothing at all if it loses (the registration
// is no longer ACTIVE, e.g. already CONSUMED by a signal that arrived just
// before this job fired).
func (h *WaitTimeoutHandler) Handle(ctx context.Context, job ports.DurableJob) error {
	var payload WaitTimerJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("runtime: unmarshal %s job %s payload: %w", WaitTimerJobKind, job.ID, err)
	}
	if payload.RunID == "" || payload.NodeRunID == "" || payload.WaitRegistrationID == "" {
		return fmt.Errorf("runtime: %s job %s payload missing runId/nodeRunId/waitRegistrationId", WaitTimerJobKind, job.ID)
	}

	return h.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		registration, err := tx.Wait().GetWaitRegistration(ctx, payload.WaitRegistrationID)
		if err != nil {
			return err
		}
		if registration.State != runtimedomain.WaitRegistrationActive {
			// Idempotent no-op — see this file's own package doc comment.
			return nil
		}

		// SignalName is empty exactly for DURATION-mode registrations (the
		// same discriminator workflow.WaitNodeConfig's own validation
		// enforces) — its own due time reaching now is a normal, designed
		// completion (ELAPSED, routed via CompletionOutcome), never a
		// TIMED_OUT failure. A SIGNAL-mode registration only ever gets a
		// timer job at all when it declared a TimeoutSeconds ceiling, so
		// reaching that due time here is genuinely a timeout.
		nextState := runtimedomain.WaitRegistrationTimedOut
		outcome := registration.TimeoutOutcome
		if registration.SignalName == "" {
			nextState = runtimedomain.WaitRegistrationElapsed
			outcome = registration.CompletionOutcome
		}

		if _, err := tx.Wait().TransitionWaitRegistration(ctx, ports.TransitionWaitRegistrationRequest{
			WaitRegistrationID: payload.WaitRegistrationID, ExpectedState: runtimedomain.WaitRegistrationActive, ExpectedVersion: registration.Version,
			NextState: nextState,
		}); err != nil {
			if errors.Is(err, ports.ErrOptimisticConflict) {
				// Lost the race to a signal that won in between our own
				// read and this CAS attempt — idempotent no-op, same as
				// above.
				return nil
			}
			return err
		}

		_, err = advanceRunTx(ctx, tx, h.ids, AdvanceRunRequest{
			RunID: payload.RunID, NodeRunID: payload.NodeRunID, Outcome: outcome, CorrelationID: payload.CorrelationID, JobID: string(job.ID),
		})
		return err
	})
}

// SignalWaitRequest is what a caller supplies to SignalWait.
type SignalWaitRequest struct {
	RunID              string
	WaitRegistrationID string
	// SignalKey is the caller-supplied, opaque, stable identity of the
	// real-world external event this call represents (confirmed with the
	// user before writing this task's code) — see
	// runtime.WaitSignal's own doc comment for the full consume-once
	// contract this enables, deliberately separate from cmd.IdempotencyKey.
	SignalKey string
	Payload   json.RawMessage
}

// SignalWaitResult is SignalWait's own idempotent result.
type SignalWaitResult struct {
	WaitRegistrationID string `json:"waitRegistrationId"`
	State              string `json:"state"`
	// Won reports whether THIS call's own signal is the one that actually
	// resolved the registration (false when the registration was already
	// non-ACTIVE — resolved by an earlier signal or the timer — by the
	// time this call's own fenced CAS ran; the signal itself is still
	// durably recorded either way, never lost, never double-consumed).
	Won           bool   `json:"won"`
	Advanced      bool   `json:"advanced"`
	NextNodeRunID string `json:"nextNodeRunId,omitempty"`
	NextNodeKey   string `json:"nextNodeKey,omitempty"`
}

// SignalWait is V4-08's own public command: the sole entry point an
// external signal delivery goes through. It follows V1-06's idempotent-
// command shape exactly like StartWorkflowRun — a retry with the same
// cmd.IdempotencyKey and cmd.RequestHash replays the first call's result
// without recording a second WaitSignal or attempting a second CAS; the
// same key with a different RequestHash is rejected as
// ports.ErrReceiptConflict (this is the ports.Command envelope's own
// protection for ONE command call/receipt — SignalKey, checked
// separately below via WaitRepository.RecordWaitSignal, is what protects
// the same REAL external event arriving through two different command
// invocations; see this file's own package doc comment and
// runtime.WaitSignal's own doc comment for why these are deliberately two
// different mechanisms).
func SignalWait(ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, cmd ports.Command, req SignalWaitRequest) (SignalWaitResult, error) {
	if req.RunID == "" || req.WaitRegistrationID == "" || req.SignalKey == "" {
		return SignalWaitResult{}, errors.New("runtime: RunID, WaitRegistrationID and SignalKey are required")
	}

	var result SignalWaitResult
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

		registration, err := tx.Wait().GetWaitRegistration(ctx, req.WaitRegistrationID)
		if err != nil {
			return err
		}
		if string(registration.RunID) != req.RunID {
			return fmt.Errorf("%w: wait registration %s belongs to run %s, not %s", ErrNodeRunMismatch, req.WaitRegistrationID, registration.RunID, req.RunID)
		}

		// Durably record this exact external event FIRST, before racing
		// the CAS — never lost even when this call goes on to lose the
		// race (RecordWaitSignal's own idempotent-replay/conflict
		// contract, WaitRepository's own doc comment).
		signalID := ids.NewID()
		signal, err := runtimedomain.NewWaitSignal(
			runtimedomain.WaitSignalID(signalID), runtimedomain.WaitRegistrationID(req.WaitRegistrationID), req.SignalKey,
			req.Payload, canonicalStateHash(req.Payload), cmd.Actor, cmd.RequestedAt,
		)
		if err != nil {
			return err
		}
		recordedSignal, _, err := tx.Wait().RecordWaitSignal(ctx, signal)
		if err != nil {
			return err
		}

		if registration.State == runtimedomain.WaitRegistrationActive {
			updated, casErr := tx.Wait().TransitionWaitRegistration(ctx, ports.TransitionWaitRegistrationRequest{
				WaitRegistrationID: req.WaitRegistrationID, ExpectedState: runtimedomain.WaitRegistrationActive, ExpectedVersion: registration.Version,
				NextState: runtimedomain.WaitRegistrationConsumed, ConsumedSignalID: string(recordedSignal.ID),
			})
			switch {
			case casErr == nil:
				advanceResult, err := advanceRunTx(ctx, tx, ids, AdvanceRunRequest{
					RunID: req.RunID, NodeRunID: string(registration.NodeRunID), Outcome: registration.CompletionOutcome,
					CorrelationID: cmd.CorrelationID, JobID: cmd.ID,
				})
				if err != nil {
					return err
				}
				result = SignalWaitResult{
					WaitRegistrationID: req.WaitRegistrationID, State: string(updated.State), Won: true,
					Advanced: advanceResult.Advanced, NextNodeRunID: advanceResult.NextNodeRunID, NextNodeKey: advanceResult.NextNodeKey,
				}
			case errors.Is(casErr, ports.ErrOptimisticConflict):
				// Lost the race to something else (another signal, or the
				// timer) that resolved this registration in between our
				// own read and this CAS attempt.
				current, reErr := tx.Wait().GetWaitRegistration(ctx, req.WaitRegistrationID)
				if reErr != nil {
					return reErr
				}
				result = SignalWaitResult{WaitRegistrationID: req.WaitRegistrationID, State: string(current.State), Won: false}
			default:
				return casErr
			}
		} else {
			result = SignalWaitResult{WaitRegistrationID: req.WaitRegistrationID, State: string(registration.State), Won: false}
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
