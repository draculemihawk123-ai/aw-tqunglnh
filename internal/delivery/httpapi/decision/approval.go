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
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// maxResolveApprovalBodyBytes bounds
// POST /runs/{runId}/approval-requests/{approvalRequestId}/resolve's own
// body — ResolveApprovalBody is two short string fields, same reasoning as
// run.go's own maxCancelRunBodyBytes.
const maxResolveApprovalBodyBytes = 1 << 16

// ResolveApprovalBody is
// POST /runs/{runId}/approval-requests/{approvalRequestId}/resolve's own
// wire request shape. Deliberately NOT RunID/ApprovalRequestID (the URL
// path is their only source — "không tin ID shape, payload hoặc
// projection") and deliberately NOT an actor/roles field of any kind
// (ADR-028; this package's own doc comment) — cmd.ActorRoles always comes
// from httpapi.PrincipalFromContext, never decoded out of this body, and
// httpapi.DecodeJSON's own strict "unknown fields rejected" behavior means
// a caller who tries to smuggle one in (e.g. `{"outcome":"approved",
// "actorRoles":["reviewer"]}`) gets a 400 INVALID_REQUEST, not a silently
// ignored field — this task's own "spoof" Verify bullet.
type ResolveApprovalBody struct {
	// Outcome is the exact declared Outcome this operator's decision maps
	// to — an open vocabulary the node's own compiled ApprovalNodeConfig
	// declares (docs/design/11-v6-00-ux-artifact.md row 11: "vocabulary do
	// node khai báo — không phải generic status"), never validated against
	// a closed approved/rejected enum here; GC-INV-11's own allow-list
	// check inside advanceRunTx (reached via runtime.ResolveApproval) is
	// what actually validates it.
	Outcome string `json:"outcome"`
	Reason  string `json:"reason,omitempty"`
}

// resolveApprovalHashPayload is what this handler actually canonicalizes
// and hashes — ResolveApprovalBody's own fields PLUS the path-derived
// ApprovalRequestID, folded in for the identical reason run/start.go's own
// startRunHashPayload folds in WorkItemID: without it, two callers
// resolving two DIFFERENT ApprovalRequests in the same project, who happen
// to reuse the same literal Idempotency-Key AND request the same Outcome/
// Reason, would compute an identical semantic hash and the second caller
// would wrongly replay the first caller's stored result instead of
// genuinely resolving its own target.
type resolveApprovalHashPayload struct {
	ApprovalRequestID string `json:"approvalRequestId"`
	Outcome           string `json:"outcome"`
	Reason            string `json:"reason,omitempty"`
}

// ResolveApprovalResponse is a successful (fresh or replayed) decision's
// own wire response: the exact runtime.ResolveApprovalResult (whose own
// json tags already carry the audit reference this task's own
// "DecisionArtifact references" Phạm vi line calls for — ApprovalRequestID/
// State, see this package's own doc comment for why no NEW DecisionArtifact
// row is written here) plus this task's own "mutation valid actions"
// advisory, always empty: once an ApprovalRequest is DECIDED/ESCALATED/
// CANCELLED there is no further action this closed set defines, and a
// PENDING request that merely lost a race (Won=false) is not this caller's
// own action to retry — it already has its own real outcome.
type ResolveApprovalResponse struct {
	runtime.ResolveApprovalResult
	ValidActions []httpapi.ValidAction `json:"validActions"`
}

// ResolveApprovalHandler returns the
// POST /runs/{runId}/approval-requests/{approvalRequestId}/resolve handler
// (operationId resolveApproval): reload the ApprovalRequest for its
// authoritative ProjectID/scope and to catch a stale/cross-run target,
// require Idempotency-Key and a strong If-Match, canonicalize/hash the
// body (folding in ApprovalRequestID), replay-or-dispatch the real
// runtime.ResolveApproval command, and encode the result. This handler's
// own body contains no business logic beyond that dispatch and wire
// mapping (V6-06's own "handler chỉ dispatch" line, reused here) — the
// ActorRoles/AuthorizedRoles intersection, the fenced CAS/Won race, and
// GC-INV-11's own outcome allow-list check are all decided entirely inside
// runtime.ResolveApproval itself, never re-derived here.
func ResolveApprovalHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		runID := r.PathValue("runId")
		approvalRequestID := r.PathValue("approvalRequestId")

		request, ok := loadApprovalRequestForUpdate(w, ctx, deps.UOW, runID, approvalRequestID)
		if !ok {
			return
		}

		idempotencyKey, err := httpapi.RequireIdempotencyKey(r)
		if err != nil {
			httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
			return
		}
		ifMatch, err := httpapi.RequireIfMatch(r)
		if err != nil {
			httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
			return
		}
		expectedVersion, err := httpapi.VersionFromETag(ifMatch)
		if err != nil {
			httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "If-Match is not a valid ETag", nil)
			return
		}

		var body ResolveApprovalBody
		if err := httpapi.DecodeJSON(r, maxResolveApprovalBodyBytes, &body); err != nil {
			httpapi.WriteDecodeError(w, err)
			return
		}
		if strings.TrimSpace(body.Outcome) == "" {
			httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "outcome is required", nil)
			return
		}

		scope := ports.ProjectScope(string(request.ProjectID))
		hashPayload, err := json.Marshal(resolveApprovalHashPayload{
			ApprovalRequestID: approvalRequestID, Outcome: body.Outcome, Reason: body.Reason,
		})
		if err != nil {
			httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
			return
		}
		requestHash := httpapi.SemanticHash("ResolveApproval", scope, hashPayload, "", expectedVersion)

		principal := httpapi.PrincipalFromContext(ctx)

		// Role authorization BEFORE the receipt fast path (V6-13). The
		// design doc's own §1 contract point 3 requires authorization to
		// run again on a replay, "nên role bị thu hồi không thể dùng replay
		// để đọc/mutate" — but this handler answers a replay entirely by
		// itself (WriteReceiptReplay below), without ever calling
		// runtime.ResolveApproval, so the command's own role check could
		// not possibly protect this path. Without this check, an actor
		// whose authorizing role had since been revoked could still read
		// back their own earlier decision's stored result by resending the
		// same Idempotency-Key. `request` is the ApprovalRequest
		// loadApprovalRequestForUpdate already reloaded at the top of this
		// handler, so this costs no extra I/O, and it uses the SAME
		// matching rule the command itself applies
		// (runtime.MatchAuthorizedRole) rather than a second copy that
		// could drift. Proven by internal/delivery/httpapi/securitymatrix's
		// own TestReceiptReplay_RevokedRoleCannotReplayItsOwnEarlierDecision.
		if runtime.MatchAuthorizedRole(principal.Roles, request.AuthorizedRoles) == "" {
			status, code := httpapi.StatusForAppErrorCode(errorcode.CodePolicyDenied)
			httpapi.WriteError(w, status, code, "the current actor is not authorized to resolve this approval request", nil)
			return
		}

		receipt, found, lookupErr := httpapi.LookupReceipt(ctx, deps.UOW, principal.Actor, scope, idempotencyKey, "ResolveApproval")
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
			// A real replay wins over ETag/state drift (receiptreplay.go's
			// own WriteReceiptReplay doc comment) — never re-checked
			// against the If-Match precondition below.
			httpapi.WriteReceiptReplay(w, receipt)
			return
		}

		// "nếu absent mới kiểm current version" (V6-02's own flow) —
		// checked here, AFTER the replay decision, against the
		// ApprovalRequest already reloaded above by
		// loadApprovalRequestForUpdate. ResolveApproval's own internal
		// fenced CAS (TransitionApprovalRequestRequest) is what actually
		// decides a genuine race past this point — see this package's own
		// doc comment for why this check can only ever catch an
		// already-stale caller, never replace that CAS.
		if request.Version != expectedVersion {
			status, code := httpapi.StatusForAppErrorCode(errorcode.CodePreconditionFailed)
			httpapi.WriteError(w, status, code, "If-Match does not match the current version; reload and retry", nil)
			return
		}

		cmd := ports.Command{
			ID: "ResolveApproval-" + idempotencyKey, IdempotencyKey: idempotencyKey,
			Actor: principal.Actor, ActorRoles: principal.Roles,
			CorrelationID: httpapi.CorrelationIDFromContext(ctx), Scope: scope, ExpectedVersion: expectedVersion,
			RequestedAt: time.Now().UTC(), Type: "ResolveApproval", RequestHash: requestHash,
		}
		result, err := runtime.ResolveApproval(ctx, deps.UOW, deps.IDs, cmd, runtime.ResolveApprovalRequest{
			RunID: runID, ApprovalRequestID: approvalRequestID, Outcome: body.Outcome, Reason: body.Reason,
		})
		if err != nil {
			writeResolveApprovalError(w, err)
			return
		}

		response := ResolveApprovalResponse{ResolveApprovalResult: result, ValidActions: []httpapi.ValidAction{}}
		_ = httpapi.EncodeResult(w, http.StatusOK, response, "")
	}
}

// loadApprovalRequestForUpdate reloads approvalRequestID via
// tx.Approvals().GetApprovalRequest — this handler's own read-only
// pre-authorization reload (contract point 3, identical reasoning to
// run/start.go's own loadWorkItemProjectID and
// workitem/scope_expansion_commands.go's own
// loadScopeExpansionRequestForUpdate): it proves the target genuinely
// exists AND belongs to the RunID the caller's own URL names, BEFORE this
// handler ever reads Idempotency-Key/If-Match/body, so an unknown or
// cross-run ApprovalRequestID fails closed with the same
// leakage-normalized 404 (V6-02A's own policy: a guessed foreign ID and a
// genuinely nonexistent one must be indistinguishable) rather than
// whatever raw error shape happens to bubble up from deep inside
// runtime.ResolveApproval's own transaction. uow.WithReadOnly only, never a
// write; the returned ApprovalRequest is reused for BOTH its own ProjectID
// (deriving cmd.Scope) and its own Version (the If-Match precondition check
// further down) — never a second reload for either.
func loadApprovalRequestForUpdate(w http.ResponseWriter, ctx context.Context, uow ports.UnitOfWork, runID, approvalRequestID string) (runtimedomain.ApprovalRequest, bool) {
	var request runtimedomain.ApprovalRequest
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var loadErr error
		request, loadErr = tx.Approvals().GetApprovalRequest(ctx, approvalRequestID)
		return loadErr
	})
	if err != nil {
		if errors.Is(err, ports.ErrPersistenceNotFound) {
			httpapi.WriteResourceHidden(w)
			return runtimedomain.ApprovalRequest{}, false
		}
		httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
		return runtimedomain.ApprovalRequest{}, false
	}
	if string(request.RunID) != runID {
		// Leakage-normalized identically to a genuine not-found (V6-02A's
		// own policy) — this task's own "stale/cross-project target" Verify
		// bullet: an ApprovalRequestID that is real but belongs to a
		// DIFFERENT run than the URL names must never be distinguishable
		// from one that does not exist at all.
		httpapi.WriteResourceHidden(w)
		return runtimedomain.ApprovalRequest{}, false
	}
	return request, true
}
