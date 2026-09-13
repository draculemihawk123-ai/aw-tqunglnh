package run

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// maxStartRunBodyBytes bounds the POST /work-items/{workItemId}/runs body —
// StartRunRequest is a single short string field, so this is a small,
// generous ceiling, well under Config.MaxBodyBytes' own server-wide default
// (cmd/aw/serve.go's --max-body-bytes, 1<<20) rather than a reuse of it.
const maxStartRunBodyBytes = 1 << 16

// StartRunRequest is the JSON body POST /work-items/{workItemId}/runs
// accepts. WorkItemID is deliberately NOT a field here: the URL path is its
// only source (contract point 3, "Không tin ID shape, payload hoặc
// projection... route reload authoritative target"), so a client can never
// send a body WorkItemID that silently disagrees with the URL it actually
// posted to.
type StartRunRequest struct {
	WorkflowVersionID string `json:"workflowVersionId"`
}

// startRunHashPayload is what this handler actually canonicalizes and feeds
// to httpapi.SemanticHash — StartRunRequest's own body PLUS the path-derived
// WorkItemID. Folding WorkItemID into the hash is deliberate and load-
// bearing, not decorative: without it, two callers starting runs for two
// DIFFERENT WorkItems in the same project, who happen to reuse the same
// literal Idempotency-Key AND request the same WorkflowVersionID, would
// compute an IDENTICAL semantic hash (SemanticHash's own inputs are command
// type + scope + normalized payload + expected version — scope alone is the
// whole PROJECT, not one WorkItem) — the second caller's request would then
// wrongly replay the first caller's stored result instead of genuinely
// starting its own run. See start_test.go's own
// TestStartWorkflowRun_HTTP_SameIdempotencyKeyDifferentWorkItem_ConflictsRatherThanCrossReplays.
type startRunHashPayload struct {
	WorkItemID        string `json:"workItemId"`
	WorkflowVersionID string `json:"workflowVersionId"`
}

// StartRunResponse is what a successful (fresh or replayed) start returns:
// the exact runtime.StartWorkflowRunResult plus this task's own "mutation
// valid actions" advisory (V6-06's own Phạm vi line). A freshly started Run
// is always RUNNING (StartWorkflowRun's own doc comment: it transitions the
// WorkItem straight to ACTIVE and the Run straight to RUNNING inside one
// transaction), so the one action this task's own closed set now makes
// valid is cancelRun. TargetVersion is deliberately 0: unlike a typical
// CAS-guarded mutation, runtime.CancelRun takes no ExpectedVersion/If-Match
// precondition at all (see cancel.go's own doc comment) — there is no
// meaningful version for a client to echo back, so httpapi.ValidAction's
// own zero value is the honest, correct advisory here, not a placeholder
// bug.
type StartRunResponse struct {
	runtime.StartWorkflowRunResult
	ValidActions []httpapi.ValidAction `json:"validActions"`
}

// StartWorkflowRunHandler returns the POST /work-items/{workItemId}/runs
// handler: reload the WorkItem for its authoritative ProjectID/scope,
// require Idempotency-Key, canonicalize/hash the body, replay-or-dispatch
// the real runtime.StartWorkflowRun command, and encode the result. This
// handler's own body contains no business logic beyond that dispatch and
// wire mapping (V6-06's own "handler chỉ dispatch" line) — every precondition
// (WorkItem READY, WorkspaceSet READY, pinned-version conflict, GC-INV-39
// cancellation fence) is decided entirely inside runtime.StartWorkflowRun
// itself, never re-derived here.
func StartWorkflowRunHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		workItemID := r.PathValue("workItemId")

		projectID, err := loadWorkItemProjectID(ctx, deps.UOW, workItemID)
		if err != nil {
			if errors.Is(err, ports.ErrPersistenceNotFound) {
				httpapi.WriteResourceHidden(w)
				return
			}
			httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
			return
		}
		scope := ports.ProjectScope(projectID)

		idempotencyKey, err := httpapi.RequireIdempotencyKey(r)
		if err != nil {
			httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
			return
		}

		var body StartRunRequest
		if err := httpapi.DecodeJSON(r, maxStartRunBodyBytes, &body); err != nil {
			httpapi.WriteDecodeError(w, err)
			return
		}
		if body.WorkflowVersionID == "" {
			httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "workflowVersionId is required", nil)
			return
		}

		hashPayload, err := json.Marshal(startRunHashPayload{WorkItemID: workItemID, WorkflowVersionID: body.WorkflowVersionID})
		if err != nil {
			httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
			return
		}
		requestHash := httpapi.SemanticHash("StartWorkflowRun", scope, hashPayload, "", 0)

		principal := httpapi.PrincipalFromContext(ctx)

		receipt, found, lookupErr := httpapi.LookupReceipt(ctx, deps.UOW, principal.Actor, scope, idempotencyKey, "StartWorkflowRun")
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
			ID: "StartWorkflowRun-" + idempotencyKey, IdempotencyKey: idempotencyKey,
			Actor: principal.Actor, ActorRoles: principal.Roles,
			CorrelationID: httpapi.CorrelationIDFromContext(ctx), Scope: scope,
			RequestedAt: time.Now().UTC(), Type: "StartWorkflowRun", RequestHash: requestHash,
		}
		result, err := runtime.StartWorkflowRun(ctx, deps.UOW, deps.IDs, cmd, runtime.StartWorkflowRunRequest{
			ProjectID: projectID, WorkItemID: workItemID, WorkflowVersionID: body.WorkflowVersionID,
		})
		if err != nil {
			// NOTE: a genuinely concurrent duplicate call can reach here
			// (both racers pass the LookupReceipt fast path above before
			// either has committed) and still return successfully rather
			// than an error — runtime.StartWorkflowRun's own transaction
			// re-checks the receipt authoritatively and replays the first
			// committed result, exactly like ErrReceiptConflict below is
			// the one outcome where that inner recheck itself fails closed.
			writeStartWorkflowRunError(w, err)
			return
		}

		response := StartRunResponse{
			StartWorkflowRunResult: result,
			ValidActions: []httpapi.ValidAction{
				{OperationID: "cancelRun", ScopeKind: httpapi.ScopeProject, TargetVersion: 0},
			},
		}
		_ = httpapi.EncodeResult(w, http.StatusCreated, response, "")
	}
}

// loadWorkItemProjectID is this handler's own read-only pre-authorization
// reload (contract point 3: "Mọi item route reload authoritative target để
// suy Project/scope và authorize; không tin ID shape, payload hoặc
// projection") — uow.WithReadOnly only, never a write, matching
// internal/archtest's own TestDeliveryHTTPAPINeverWritesOrRecordsAReceipt.
// The returned ProjectID is what this handler passes into
// runtime.StartWorkflowRunRequest.ProjectID and into the command's own
// ports.CommandScope — NEVER a client-supplied ProjectID, since
// StartRunRequest has no such field at all and runtime.StartWorkflowRun
// itself trusts its caller's ProjectID at face value (commands.go stamps
// req.ProjectID directly onto the new Run/event with no WorkItem cross-check
// of its own — this handler is what keeps that trust well-placed). This is
// purely a scope-determination optimization, same spirit as
// httpapi.LookupReceipt: the real, authoritative WorkItem reload still
// happens again inside StartWorkflowRun's own transaction regardless.
func loadWorkItemProjectID(ctx context.Context, uow ports.UnitOfWork, workItemID string) (string, error) {
	var projectID string
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		item, err := tx.Work().GetWorkItem(ctx, workItemID)
		if err != nil {
			return err
		}
		projectID = string(item.ProjectID)
		return nil
	})
	return projectID, err
}
