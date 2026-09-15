package recovery

import (
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// ResolveWorkItemBlockerRequest is the JSON body POST
// /work-item-blockers/{blockerId}/resolve accepts. BlockerID comes only from
// the URL path; Actor comes only from the bound LocalPrincipalSnapshot
// (ADR-028). Mode MUST be supplied explicitly — there is no default, the
// identical "no default" discipline resolve_work_item_blocker.go's own
// package doc comment locks in at the application layer; this handler
// enforces nothing extra about it beyond decoding it verbatim and letting
// runtime.ResolveWorkItemBlocker's own ErrResolutionModeRequired reject an
// empty/invalid value (writeResolveWorkItemBlockerError, errors.go).
type ResolveWorkItemBlockerRequest struct {
	Mode   string `json:"mode"`
	Reason string `json:"reason"`
	// PolicyGrantRef is required exactly when Mode is "WAIVED" — see
	// resolve_work_item_blocker.go's own package doc comment. Left to
	// runtime.ResolveWorkItemBlocker's own ErrWaiveRequiresPolicyGrant to
	// enforce, not duplicated here.
	PolicyGrantRef string `json:"policyGrantRef,omitempty"`
}

// ResolveWorkItemBlockerResponse is a camelCase-JSON wire projection of
// runtime.ResolveWorkItemBlockerResult.
type ResolveWorkItemBlockerResponse struct {
	BlockerID string `json:"blockerId"`
	// AlreadyResolved: the blocker was no longer OPEN by the time this call
	// actually looked (already RESOLVED or WAIVED by an earlier call) — a
	// safe, idempotent no-op replay; this task's own "resolved/waived
	// replay no-op theo core" line, trusted verbatim from
	// runtime.ResolveWorkItemBlockerResult, never re-derived by this
	// handler.
	AlreadyResolved bool `json:"alreadyResolved"`
	// State is the blocker's own real current State ("RESOLVED" or
	// "WAIVED") this call actually observed.
	State string `json:"state"`
	// WorkItemUnblocked reports whether closing this blocker left the
	// WorkItem with zero remaining OPEN blockers (closeWorkItemBlockerTx's
	// own "unblocked" outcome) — false on the AlreadyResolved replay path
	// (runtime.ResolveWorkItemBlockerResult's own zero value there).
	WorkItemUnblocked bool `json:"workItemUnblocked"`
	// WorkItemStatus is the WorkItem's own real current Status.
	WorkItemStatus string `json:"workItemStatus"`
}

// ResolveWorkItemBlockerHTTPHandler returns the POST
// /work-item-blockers/{blockerId}/resolve handler.
//
// 200 OK — unlike RetryBlockedActivation/CancelWorkItem, ResolveWorkItemBlocker
// never enqueues a follow-up async job of its own (closeWorkItemBlockerTx
// CASes the blocker and, when it was the WorkItem's own last OPEN blocker,
// the WorkItem's own Status, entirely within this one transaction); the
// response already reports the fully final outcome, the same reasoning
// internal/delivery/httpapi/workitem's own ApproveScopeExpansion/
// RejectScopeExpansion handlers (scope_expansion_commands.go) already apply
// for an identically-shaped "one CAS, no async follow-up" command.
//
// No pre-dispatch reload of the blocker: runtime.ResolveWorkItemBlocker's
// own first transactional step (tx.Work().GetWorkItemBlocker) already
// performs the identical existence check — the same "let the real command's
// own preflight do it" choice this package's other two handlers make.
func ResolveWorkItemBlockerHTTPHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		blockerID := r.PathValue("blockerId")

		var body ResolveWorkItemBlockerRequest
		if err := httpapi.DecodeJSON(r, maxRecoveryCommandBodyBytes, &body); err != nil {
			httpapi.WriteDecodeError(w, err)
			return
		}
		if body.Reason == "" {
			httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "reason is required", nil)
			return
		}

		principal := httpapi.PrincipalFromContext(ctx)
		result, err := runtime.ResolveWorkItemBlocker(ctx, deps.UOW, runtime.ResolveWorkItemBlockerRequest{
			BlockerID: blockerID, Mode: runtime.ResolutionMode(body.Mode), Actor: principal.Actor, Reason: body.Reason,
			PolicyGrantRef: body.PolicyGrantRef, CorrelationID: httpapi.CorrelationIDFromContext(ctx),
		})
		if err != nil {
			writeResolveWorkItemBlockerError(w, err)
			return
		}

		response := ResolveWorkItemBlockerResponse{
			BlockerID: result.BlockerID, AlreadyResolved: result.AlreadyResolved, State: result.State,
			WorkItemUnblocked: result.WorkItemUnblocked, WorkItemStatus: result.WorkItemStatus,
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, response, "")
	}
}
