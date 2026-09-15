// This file is V6-04A's own additive HTTP route
// (docs/design/08-v6-api-projections.md V6-04A; ADR-028 §30):
// POST /work-items/{workItemId}/mark-ready, operationId markWorkItemReady —
// dispatches internal/app/work.MarkWorkItemReady, the single narrow command
// that closes BACKLOG->READY (see that function's own doc comment for the
// full application-layer contract).
//
// Path shape is deliberately DIFFERENT from every other route in this
// package: it has no {projectId} segment at all (routes.go's own doc
// comment explains why — the design doc's own route fragment is verbatim
// `POST /work-items/{id}/mark-ready`, and V6-06's already-merged run package
// established the concrete precedent for this exact shape first). This
// means the usual "reload via workapp.GetWorkItem(scope, id), which itself
// enforces scope match" preamble every other mutating handler in this
// package uses cannot apply here — there is no client-claimed scope to
// cross-check against, by construction. Instead, ProjectID is derived
// SOLELY by reloading the WorkItem's own real, stored row directly
// (loadWorkItemForMarkReady below), mirroring
// internal/delivery/httpapi/run/start.go's own loadWorkItemProjectID
// exactly — contract point 3's "không tin ID shape, payload hoặc
// projection" is satisfied the same way: the server derives Project/scope
// from the authoritative target, never from anything the client supplies.
//
// Body is emptyBody{} — the SAME zero-field type Approve/WithdrawScopeExpansion
// already use, reused verbatim rather than a fourth near-identical type.
// Because httpapi.DecodeJSON always calls json.Decoder.DisallowUnknownFields,
// posting ANY extra field at all — including a client's own attempted
// `{"targetStatus":"DONE"}` — is rejected outright as a 400 malformed-body
// error before ever reaching this handler's own logic: this task's own
// explicit "payload target-status" Verify-line requirement ("prove the
// endpoint rejects...any client-supplied status-like field") is satisfied
// structurally, by the same decode step every other mutating route in this
// package already goes through, not by a bespoke field-by-field check here.
//
// Like Approve/Reject/WithdrawScopeExpansion, this is an UPDATE-shaped
// mutation on an already-existing resource — Idempotency-Key AND a strong
// If-Match are both required (prepareUpdateCommand), and the caller's own
// claimed ExpectedVersion is checked against the freshly reloaded target
// BEFORE dispatch, after the replay decision (V6-02's own "nếu absent mới
// kiểm current version" flow step) — this task's own explicit "stale" Verify
// line.
package workitem

import (
	"context"
	"net/http"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// loadWorkItemForMarkReady reloads workItemID's own real, current row
// directly via tx.Work().GetWorkItem — never through workapp.GetWorkItem
// (queries.go), which requires an already-known project scope this route's
// own path does not carry (see this file's own doc comment). This is purely
// a read (uow.WithReadOnly), matching
// internal/archtest's own TestDeliveryHTTPAPINeverWritesOrRecordsAReceipt,
// which already walks this whole package recursively.
func loadWorkItemForMarkReady(ctx context.Context, uow ports.UnitOfWork, workItemID string) (workdomain.WorkItem, error) {
	var item workdomain.WorkItem
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		item, err = tx.Work().GetWorkItem(ctx, workItemID)
		return err
	})
	return item, err
}

// handleMarkWorkItemReady implements POST /work-items/{workItemId}/mark-ready
// (operationId markWorkItemReady): dispatches internal/app/work.
// MarkWorkItemReady. See this file's own doc comment for the full flow.
func handleMarkWorkItemReady(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workItemID := r.PathValue("workItemId")
		if strings.TrimSpace(workItemID) == "" {
			writeValidationError(w, "workItemId", "is required")
			return
		}

		item, err := loadWorkItemForMarkReady(r.Context(), deps.UnitOfWork, workItemID)
		if err != nil {
			writeQueryError(w, err)
			return
		}
		scope := ports.ProjectScope(string(item.ProjectID))

		var body emptyBody
		cmd, expectedVersion, ok := prepareUpdateCommand(w, r, deps, "MarkWorkItemReady", scope, &body)
		if !ok {
			return
		}

		if replayOrProceed(r.Context(), w, deps, cmd) {
			return
		}
		// "nếu absent mới kiểm current version" — checked here, AFTER the
		// replay decision, against the target already reloaded above (the
		// same ordering every other update handler in this package uses).
		if item.Version != expectedVersion {
			writePreconditionFailed(w, "If-Match does not match the current version; reload and retry")
			return
		}

		result, err := workapp.MarkWorkItemReady(r.Context(), deps.UnitOfWork, cmd, workapp.MarkWorkItemReadyRequest{
			WorkItemID: workItemID,
		})
		if err != nil {
			writeCommandError(w, err)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, result, httpapi.ETagFromVersion(result.Version))
	}
}
