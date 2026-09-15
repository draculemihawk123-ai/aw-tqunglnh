package recovery

import (
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// CancelWorkItemRequest is the JSON body POST /work-items/{workItemId}/cancel
// accepts. WorkItemID comes only from the URL path; Actor comes only from
// the bound LocalPrincipalSnapshot (ADR-028) — the identical discipline
// run.CancelRunRequest (internal/delivery/httpapi/run/cancel.go) already
// establishes for the Run-level command this one mirrors at the WorkItem
// level.
type CancelWorkItemRequest struct {
	Reason string `json:"reason"`
}

// CancelWorkItemResponse is a camelCase-JSON wire projection of
// runtime.CancelWorkItemResult.
type CancelWorkItemResponse struct {
	WorkItemID string `json:"workItemId"`
	// AlreadyRequested: a second CancelWorkItem call for the same
	// WorkItem — idempotent by WorkItemID via its own durable
	// WorkItemCancellationIntent, never a second intent, never a second
	// round of Run-quiescing.
	AlreadyRequested bool `json:"alreadyRequested"`
	// Status is the WorkItem's own real current Status this call actually
	// observed — CANCELLED when every one of its Runs was already
	// terminal (or it had none), still whatever it was before otherwise
	// (this task's own "cancellation của active Run trả quiesce state,
	// không giả terminal ngay" line, at the WorkItem level: a WorkItem with
	// at least one still-quiescing Run is NOT forced into any intermediate
	// status by this call — see cancel_work_item.go's own package doc
	// comment).
	Status string `json:"status"`
}

// CancelWorkItemHTTPHandler returns the POST /work-items/{workItemId}/cancel
// handler.
//
// 202 Accepted uniformly — CancelWorkItem drives every one of the
// WorkItem's own non-terminal Runs through the exact same quiesce protocol
// CancelRun itself uses (cancel_work_item.go's own package doc comment:
// "cancelRunTx... the exact same protocol CancelRun itself uses"), so this
// handler's own status-code reasoning is identical to
// run.CancelRunHandler's own: a request accepted into an asynchronous
// quiesce, never a synchronous guarantee that the WorkItem already reached
// CANCELLED by the time this response is written (it may well have, when
// there was nothing left to quiesce — Status reports whatever
// runtime.CancelWorkItem itself actually observed, never a status this
// handler invents).
//
// No pre-dispatch reload of the WorkItem: runtime.CancelWorkItem's own
// first transactional step (tx.Work().GetWorkItem) already performs the
// identical existence check, returning ports.ErrPersistenceNotFound
// unwrapped for an unknown WorkItemID — mapped by writeCancelWorkItemError
// (errors.go), the same "let the real command's own preflight do the one
// existence check, don't duplicate it as business logic in this thin
// dispatcher" choice retry.go's own doc comment makes for
// RetryBlockedActivation.
func CancelWorkItemHTTPHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		workItemID := r.PathValue("workItemId")

		var body CancelWorkItemRequest
		if err := httpapi.DecodeJSON(r, maxRecoveryCommandBodyBytes, &body); err != nil {
			httpapi.WriteDecodeError(w, err)
			return
		}
		if body.Reason == "" {
			httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "reason is required", nil)
			return
		}

		principal := httpapi.PrincipalFromContext(ctx)
		result, err := runtime.CancelWorkItem(ctx, deps.UOW, deps.IDs, runtime.CancelWorkItemRequest{
			WorkItemID: workItemID, Actor: principal.Actor, Reason: body.Reason,
			CorrelationID: httpapi.CorrelationIDFromContext(ctx),
		})
		if err != nil {
			writeCancelWorkItemError(w, err)
			return
		}

		response := CancelWorkItemResponse{WorkItemID: result.WorkItemID, AlreadyRequested: result.AlreadyRequested, Status: result.Status}
		_ = httpapi.EncodeResult(w, http.StatusAccepted, response, "")
	}
}
