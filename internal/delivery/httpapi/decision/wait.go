package decision

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// maxSubmitWaitSignalBodyBytes bounds
// POST /runs/{runId}/wait-registrations/{waitRegistrationId}/signal's own
// body — SubmitWaitSignalBody is one short string plus a caller-shaped
// payload, same small ceiling reasoning as approval.go's own
// maxResolveApprovalBodyBytes.
const maxSubmitWaitSignalBodyBytes = 1 << 16

// SubmitWaitSignalBody is
// POST /runs/{runId}/wait-registrations/{waitRegistrationId}/signal's own
// wire request shape. Deliberately NOT RunID/WaitRegistrationID (the URL
// path is their only source) and deliberately NOT an actor field (ADR-028
// — see this package's own doc comment).
type SubmitWaitSignalBody struct {
	// SignalKey is deliberately distinct from the Idempotency-Key header —
	// see runtime.SignalWait's own doc comment and this package's own doc
	// comment: Idempotency-Key protects THIS one HTTP call/receipt,
	// SignalKey is the caller-supplied, stable identity of the real-world
	// external event being reported, so the exact same event reported
	// through two different command invocations (two different
	// Idempotency-Keys) is still recognized and consumed only once —
	// this task's own "duplicate signal" Verify bullet.
	SignalKey string `json:"signalKey"`
	// Payload is the caller-shaped external event body, passed through to
	// runtime.SignalWaitRequest.Payload verbatim — never interpreted by
	// this handler.
	Payload json.RawMessage `json:"payload,omitempty"`
}

// submitWaitSignalHashPayload is what this handler actually canonicalizes
// and hashes — SubmitWaitSignalBody's own fields PLUS the path-derived
// WaitRegistrationID, folded in for the identical reason
// approval.go's own resolveApprovalHashPayload folds in ApprovalRequestID
// (itself mirroring run/start.go's own startRunHashPayload): without it,
// two callers signaling two DIFFERENT WaitRegistrations in the same
// project, who happen to reuse the same literal Idempotency-Key AND the
// same SignalKey/payload, would compute an identical semantic hash and the
// second caller would wrongly replay the first caller's stored result.
type submitWaitSignalHashPayload struct {
	WaitRegistrationID string          `json:"waitRegistrationId"`
	SignalKey          string          `json:"signalKey"`
	Payload            json.RawMessage `json:"payload,omitempty"`
}

// SubmitWaitSignalResponse is a successful (fresh or replayed) signal's own
// wire response: the exact runtime.SignalWaitResult (whose own json tags
// already carry the audit reference this task's own "DecisionArtifact
// references" Phạm vi line calls for — see this package's own doc comment)
// plus this task's own "mutation valid actions" advisory, always empty:
// once a WaitRegistration is no longer ACTIVE there is no further action
// this closed set defines.
type SubmitWaitSignalResponse struct {
	runtime.SignalWaitResult
	ValidActions []httpapi.ValidAction `json:"validActions"`
}

// SubmitWaitSignalHandler returns the
// POST /runs/{runId}/wait-registrations/{waitRegistrationId}/signal handler
// (operationId submitWaitSignal): reload the WaitRegistration for its
// authoritative ProjectID/scope and to catch a stale/cross-run target,
// require Idempotency-Key, canonicalize/hash the body (folding in
// WaitRegistrationID), replay-or-dispatch the real runtime.SignalWait
// command, and encode the result.
//
// Deliberately NO If-Match/ExpectedVersion precondition — see this
// package's own decision.go doc comment for the full reasoning (mirroring
// run/cancel.go's own choice, not workitem's ApproveScopeExpansion): a WAIT
// signal's caller is typically an external system identified by SignalKey,
// not a human who has just reloaded a UI page, and SignalWait's own
// idempotent-duplicate-delivery contract must keep working even when a
// retried delivery has no way of knowing the registration's current
// version.
//
// Unlike resolveApproval, this handler also enforces no role/
// AuthorizedRoles gate at all — runtime.WaitRegistration carries no such
// field (confirmed by reading internal/domain/runtime/wait.go's own doc
// comment before writing this handler): WAIT exists for typed external
// signals (e.g. a CI webhook), not necessarily an authenticated human
// decision. cmd.ActorRoles is still populated from the bound principal
// here (never the body, exactly like every other command this codebase
// dispatches) — it is just that runtime.SignalWait itself never reads it.
func SubmitWaitSignalHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		runID := r.PathValue("runId")
		waitRegistrationID := r.PathValue("waitRegistrationId")

		registration, ok := loadWaitRegistrationForRun(w, ctx, deps.UOW, runID, waitRegistrationID)
		if !ok {
			return
		}

		idempotencyKey, err := httpapi.RequireIdempotencyKey(r)
		if err != nil {
			httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
			return
		}

		var body SubmitWaitSignalBody
		if err := httpapi.DecodeJSON(r, maxSubmitWaitSignalBodyBytes, &body); err != nil {
			httpapi.WriteDecodeError(w, err)
			return
		}
		if strings.TrimSpace(body.SignalKey) == "" {
			httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "signalKey is required", nil)
			return
		}

		scope := ports.ProjectScope(string(registration.ProjectID))
		hashPayload, err := json.Marshal(submitWaitSignalHashPayload{
			WaitRegistrationID: waitRegistrationID, SignalKey: body.SignalKey, Payload: body.Payload,
		})
		if err != nil {
			httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
			return
		}
		requestHash := httpapi.SemanticHash("SignalWait", scope, hashPayload, "", 0)

		principal := httpapi.PrincipalFromContext(ctx)

		receipt, found, lookupErr := httpapi.LookupReceipt(ctx, deps.UOW, principal.Actor, scope, idempotencyKey, "SignalWait")
		if lookupErr != nil {
			httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
			return
		}
		if found {
			if reconcileErr := httpapi.ReconcileReceipt(receipt, requestHash); reconcileErr != nil {
				httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict,
					"idempotency key already used with a different request", nil)
				return
			}
			httpapi.WriteReceiptReplay(w, receipt)
			return
		}

		cmd := ports.Command{
			ID: "SignalWait-" + idempotencyKey, IdempotencyKey: idempotencyKey,
			Actor: principal.Actor, ActorRoles: principal.Roles,
			CorrelationID: httpapi.CorrelationIDFromContext(ctx), Scope: scope,
			RequestedAt: time.Now().UTC(), Type: "SignalWait", RequestHash: requestHash,
		}
		result, err := runtime.SignalWait(ctx, deps.UOW, deps.IDs, cmd, runtime.SignalWaitRequest{
			RunID: runID, WaitRegistrationID: waitRegistrationID, SignalKey: body.SignalKey, Payload: body.Payload,
		})
		if err != nil {
			writeSignalWaitError(w, err)
			return
		}

		response := SubmitWaitSignalResponse{SignalWaitResult: result, ValidActions: []httpapi.ValidAction{}}
		_ = httpapi.EncodeResult(w, http.StatusOK, response, "")
	}
}

// loadWaitRegistrationForRun reloads waitRegistrationID via
// tx.Wait().GetWaitRegistration — this handler's own read-only
// pre-authorization reload, identical reasoning to approval.go's own
// loadApprovalRequestForUpdate: proves the target genuinely exists AND
// belongs to the RunID the caller's own URL names before Idempotency-Key/
// body are ever read, folding both "not found" and "wrong run" into the
// same leakage-normalized 404. The returned WaitRegistration is used only
// to derive its own ProjectID (cmd.Scope) — unlike
// loadApprovalRequestForUpdate, its Version is never compared against
// anything (see this handler's own doc comment for why).
func loadWaitRegistrationForRun(w http.ResponseWriter, ctx context.Context, uow ports.UnitOfWork, runID, waitRegistrationID string) (runtimedomain.WaitRegistration, bool) {
	var registration runtimedomain.WaitRegistration
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var loadErr error
		registration, loadErr = tx.Wait().GetWaitRegistration(ctx, waitRegistrationID)
		return loadErr
	})
	if err != nil {
		if errors.Is(err, ports.ErrPersistenceNotFound) {
			httpapi.WriteResourceHidden(w)
			return runtimedomain.WaitRegistration{}, false
		}
		httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
		return runtimedomain.WaitRegistration{}, false
	}
	if string(registration.RunID) != runID {
		// Leakage-normalized identically to a genuine not-found — this
		// task's own "stale/cross-project target" Verify bullet.
		httpapi.WriteResourceHidden(w)
		return runtimedomain.WaitRegistration{}, false
	}
	return registration, true
}
