package run

import (
	"context"
	"errors"
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// maxCancelRunBodyBytes bounds the POST /runs/{runId}/cancel body —
// CancelRunRequest is a single short string field, same reasoning as
// maxStartRunBodyBytes (start.go).
const maxCancelRunBodyBytes = 1 << 16

// CancelRunRequest is the JSON body POST /runs/{runId}/cancel accepts. RunID
// comes only from the URL path (same "no redundant/untrusted body ID"
// discipline as StartRunRequest); Actor comes only from the bound
// LocalPrincipalSnapshot — ADR-028: "Actor và ActorRoles là authentication
// context... HTTP body/header... MUST NOT được phép override actor/roles" —
// so there is deliberately no Actor field here at all, never mind one this
// handler would trust.
type CancelRunRequest struct {
	Reason string `json:"reason"`
}

// CancelRunResponse is a camelCase-JSON wire projection of
// runtime.CancelRunResult, which carries no json tags of its own (its Go
// field names read fine internally, but this package owns the public wire
// contract and maps explicitly rather than exposing the app layer's exact
// Go casing verbatim). ValidActions is always empty: once a Run is
// CANCELLING (or already CANCELLED by the time a racing/duplicate call
// observes it) there is no further mutation in this task's own closed set
// to advertise — calling cancel again is already a safe, idempotent no-op a
// caller may issue without being told it is "valid."
type CancelRunResponse struct {
	RunID            string                `json:"runId"`
	State            string                `json:"state"`
	AlreadyRequested bool                  `json:"alreadyRequested"`
	CoordinatorJobID string                `json:"coordinatorJobId,omitempty"`
	ValidActions     []httpapi.ValidAction `json:"validActions"`
}

// CancelRunHandler returns the POST /runs/{runId}/cancel handler.
//
// Deliberately NOT following V6-02's CommandEnvelope/receipt flow — no
// Idempotency-Key, no If-Match required. This was decided only after
// reading runtime.StartWorkflowRun's and runtime.CancelRun's own signatures
// side by side, per this task's own explicit review instruction, not
// assumed by default:
//
//   - runtime.CancelRun (internal/app/runtime/cancel_run.go) takes a plain
//     CancelRunRequest{RunID, Actor, Reason, CorrelationID}. There is no
//     ports.Command parameter, no IdempotencyKey field, no ExpectedVersion
//     field anywhere on it — unlike runtime.StartWorkflowRun, which takes a
//     ports.Command exactly like every other CommandEnvelope-shaped command
//     in this codebase (internal/app/catalog.CreateProject and siblings).
//
//   - This is not an oversight this handler should paper over. ADR-020 §22
//     ("Cancellation protocol", docs/architecture/02-architecture-decisions.md
//     line 354-355) states outright: "CancelRun atomically ghi durable
//     cancel intent, chuyển Run sang CANCELLING, append event và enqueue
//     job; command là idempotent theo run" — idempotent BY RUN, structurally.
//     cancel_run.go's own package doc comment makes the mechanism explicit:
//     a Run has exactly one RunCancellationIntent ever (unique per RunID);
//     a second CancelRun call finds it already recorded and returns
//     AlreadyRequested — never a second intent, never a second
//     CANCEL_RUN_COORDINATOR job (cancelRunTx's own early-return branch).
//
//   - A second, or a genuinely concurrent racing, call to THIS exact
//     handler is therefore already safe no matter what Idempotency-Key
//     header a client did or didn't send. Requiring one here would validate
//     a header this package could never actually use: runtime.CancelRun
//     writes no command receipt at all, so httpapi.LookupReceipt would find
//     literally nothing for it, forever — requiring-but-never-consulting a
//     header would misrepresent the real contract to a caller (and to
//     V6-12's future OpenAPI generation) rather than document it honestly.
//
//   - Likewise no If-Match/ExpectedVersion: CancelRun reloads the Run's own
//     current state and CASes internally (cancelRunTx's own
//     TransitionWorkflowRunState call uses the version it JUST loaded, not
//     one a caller supplied) — a client does not need to already know the
//     Run's current version to ask it to cancel.
//
// See run.go's own package doc comment for the same reasoning stated
// alongside start.go's opposite choice.
func CancelRunHandler(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		runID := r.PathValue("runId")

		if err := requireRunExists(ctx, deps.UOW, runID); err != nil {
			if errors.Is(err, ports.ErrPersistenceNotFound) {
				httpapi.WriteResourceHidden(w)
				return
			}
			httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
			return
		}

		var body CancelRunRequest
		if err := httpapi.DecodeJSON(r, maxCancelRunBodyBytes, &body); err != nil {
			httpapi.WriteDecodeError(w, err)
			return
		}
		if body.Reason == "" {
			httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "reason is required", nil)
			return
		}

		principal := httpapi.PrincipalFromContext(ctx)
		result, err := runtime.CancelRun(ctx, deps.UOW, deps.IDs, runtime.CancelRunRequest{
			RunID: runID, Actor: principal.Actor, Reason: body.Reason,
			CorrelationID: httpapi.CorrelationIDFromContext(ctx),
		})
		if err != nil {
			writeCancelRunError(w, err)
			return
		}

		// 202 Accepted, always — even when AlreadyRequested. Cancellation is
		// a quiesce PROTOCOL, not a synchronous CAS to CANCELLED (this
		// task's own "Thực hiện" line: "cancel trả CANCELLING, không giả
		// CANCELLED"); result.State reports whatever runtime.CancelRun
		// itself observed (freshly CANCELLING, or — for a duplicate call —
		// the Run's real current state, which may already have advanced to
		// CANCELLED by the time this call landed), never a status this
		// handler invents.
		response := CancelRunResponse{
			RunID: result.RunID, State: result.State, AlreadyRequested: result.AlreadyRequested,
			CoordinatorJobID: result.CoordinatorJobID, ValidActions: []httpapi.ValidAction{},
		}
		_ = httpapi.EncodeResult(w, http.StatusAccepted, response, "")
	}
}

// requireRunExists is this handler's own read-only pre-authorization reload
// (contract point 3, identical reasoning to start.go's own
// loadWorkItemProjectID): it proves the Run genuinely exists (and therefore
// belongs to some real, currently-scoped Project) BEFORE this handler ever
// decodes a body or dispatches a mutation, so an unknown/foreign RunID fails
// closed with a consistent, leakage-normalized 404 rather than whatever raw
// error shape happens to bubble up from deep inside runtime.CancelRun's own
// transaction. uow.WithReadOnly only, never a write. Unlike start.go's
// loadWorkItemProjectID, the ProjectID itself is not otherwise needed here:
// CancelRunRequest carries no ProjectID/scope field for this handler to
// populate, and this task defines no additional per-project role gate
// beyond the single trusted LocalPrincipalSnapshot every request already
// carries (ADR-028) — so this function's only job is the existence/ownership
// proof itself.
func requireRunExists(ctx context.Context, uow ports.UnitOfWork, runID string) error {
	return uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		_, err := tx.Runtime().GetWorkflowRun(ctx, runID)
		return err
	})
}
