package recovery

import (
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// RetryBlockedActivationRequest is the JSON body POST
// /node-runs/{nodeRunId}/retry-blocked-activation accepts. NodeRunID comes
// only from the URL path; Actor comes only from the bound
// LocalPrincipalSnapshot (ADR-028) — neither is a field here, the identical
// "no redundant/untrusted body ID, no body Actor override" discipline
// run.CancelRunRequest already establishes.
type RetryBlockedActivationRequest struct {
	Reason string `json:"reason"`
}

// RetryBlockedActivationResponse is a camelCase-JSON wire projection of
// runtime.RetryBlockedActivationResult (which carries no json tags of its
// own — this package owns the public wire contract, the same mapping
// discipline run.CancelRunResponse's own doc comment already explains).
// Exactly one of AlreadyRetried, Retried or FailureReason is populated —
// runtime.RetryBlockedActivationResult's own three-way outcome, verbatim,
// never collapsed or re-interpreted here.
type RetryBlockedActivationResponse struct {
	NodeRunID string `json:"nodeRunId"`
	// AlreadyRetried: the admission blocker was no longer OPEN by the time
	// this call actually looked (an earlier winner already retried it, or a
	// redelivered/duplicate call) — a safe, idempotent no-op.
	AlreadyRetried bool `json:"alreadyRetried"`
	// Retried: revalidation passed for real — exactly one new NodeRun
	// activation was created and handed to the ordinary scheduling
	// pipeline (a fresh async ScheduleNodeRunJobKind job, the same
	// pipeline that scheduled the original blocked activation).
	Retried bool `json:"retried"`
	// ReactivatedNodeRunID is populated only when Retried is true.
	ReactivatedNodeRunID string `json:"reactivatedNodeRunId,omitempty"`
	// FailureReason/FailureDetail are populated only when revalidation
	// itself still fails — this task's own "Không làm" line ("resolve
	// open blocker hợp lệ... revalidation thất bại giữ nguyên blocker hiện
	// tại"): the SAME vocabulary evaluateAdmission's own admissionDecision
	// uses (internal/app/runtime/admission.go), so a caller sees exactly
	// why, never a generic rejection — and never an HTTP error status: this
	// is a normal, structured business outcome (retry_blocked_activation.go's
	// own doc comment on RetryBlockedActivationResult), the existing
	// blocker is left OPEN, unresolved, untouched.
	FailureReason string `json:"failureReason,omitempty"`
	FailureDetail string `json:"failureDetail,omitempty"`
}

// RetryBlockedActivationHTTPHandler returns the POST
// /node-runs/{nodeRunId}/retry-blocked-activation handler.
//
// 202 Accepted uniformly, for all three outcomes — the same reasoning
// run.CancelRunHandler's own doc comment already applies to CancelRun's own
// single always-202 status, adapted to this command's own three-way result:
// a genuine Retried=true outcome enqueues a fresh async
// ScheduleNodeRunJobKind job whose own eventual completion this response
// makes no promise about (identical to CancelRun's own coordinator-job
// framing); AlreadyRetried and a still-failing FailureReason both report the
// current, authoritative, already-observed outcome of that same real
// admission re-check — never a client mistake, so never a 4xx/5xx, and
// never a status this handler invents beyond what
// runtime.RetryBlockedActivationHandler.Retry itself already decided.
//
// Deliberately no pre-dispatch reload of the NodeRun the way run.go's own
// requireRunExists/loadWorkItemProjectID do for their own routes: Retry's
// own preflight (loadForRetryTx, read-only, run before any real I/O or
// write) already performs the identical existence/state check this handler
// would otherwise duplicate, and returns ports.ErrPersistenceNotFound
// unwrapped for a genuinely unknown NodeRunID — mapped by
// writeRetryBlockedActivationError (errors.go) exactly like every other
// route's own pre-check would. Duplicating that reload here would be
// business logic this task's own "Không làm" line reserves for the real
// command, not this thin dispatcher.
func RetryBlockedActivationHTTPHandler(deps Dependencies) http.HandlerFunc {
	handler := runtime.NewRetryBlockedActivationHandler(deps.UOW, deps.IDs, deps.Isolation, deps.Agents)
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		nodeRunID := r.PathValue("nodeRunId")

		var body RetryBlockedActivationRequest
		if err := httpapi.DecodeJSON(r, maxRecoveryCommandBodyBytes, &body); err != nil {
			httpapi.WriteDecodeError(w, err)
			return
		}
		if body.Reason == "" {
			httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "reason is required", nil)
			return
		}

		principal := httpapi.PrincipalFromContext(ctx)
		result, err := handler.Retry(ctx, runtime.RetryBlockedActivationRequest{
			NodeRunID: nodeRunID, Actor: principal.Actor, Reason: body.Reason,
			CorrelationID: httpapi.CorrelationIDFromContext(ctx),
		})
		if err != nil {
			writeRetryBlockedActivationError(w, err)
			return
		}

		response := RetryBlockedActivationResponse{
			NodeRunID: result.NodeRunID, AlreadyRetried: result.AlreadyRetried, Retried: result.Retried,
			ReactivatedNodeRunID: result.ReactivatedNodeRunID, FailureReason: string(result.FailureReason), FailureDetail: result.FailureDetail,
		}
		_ = httpapi.EncodeResult(w, http.StatusAccepted, response, "")
	}
}
