package catalog

import (
	"net/http"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// beginMutation runs V6-02's own shared mutation preamble: require
// Idempotency-Key, canonicalize+decode the body into dst, build the
// ports.Command envelope, and check for an existing receipt — replaying
// (httpapi.WriteReceiptReplay) or conflicting (409) directly, and writing
// the response itself, if one is found. The caller only reaches its own
// real dispatch code when ok is true: no receipt existed yet for this
// exact (actor, scope, idempotency key, command type), nothing has been
// written to the response, dst holds the decoded+validated-shape request
// body, and cmd is ready to pass straight to the real application
// command.
//
// expectedVersion is 0 for every create-shaped command in this package
// (CreateProject, RegisterRepository, AssignComponentPack — none of them
// protects an existing row's current version) and the caller's own
// already-parsed If-Match version for the one update-shaped command,
// retryRepositoryProbe — parsing/requiring If-Match itself is the
// caller's own job (repository.go), not this shared helper's, since it is
// the one thing genuinely different between a create and an update-shaped
// mutation in this package and every other flow step is identical either
// way.
func (h *handler) beginMutation(w http.ResponseWriter, r *http.Request, scope ports.CommandScope, commandType string, expectedVersion uint64, dst any) (cmd ports.Command, ok bool) {
	idempotencyKey, err := httpapi.RequireIdempotencyKey(r)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
		return ports.Command{}, false
	}

	canonical, err := httpapi.CanonicalizeJSON(r, maxRequestBodyBytes, dst)
	if err != nil {
		httpapi.WriteDecodeError(w, err)
		return ports.Command{}, false
	}

	principal := httpapi.PrincipalFromContext(r.Context())
	hash := httpapi.SemanticHash(commandType, scope, canonical, "", expectedVersion)
	id := commandType + "-" + idempotencyKey
	cmd = ports.Command{
		ID: id, IdempotencyKey: idempotencyKey, Actor: principal.Actor, ActorRoles: principal.Roles,
		CorrelationID: id, Scope: scope, ExpectedVersion: expectedVersion,
		RequestedAt: time.Now().UTC(), Type: commandType, RequestHash: hash,
	}

	receipt, found, err := httpapi.LookupReceipt(r.Context(), h.uow, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
		return ports.Command{}, false
	}
	if found {
		if err := httpapi.ReconcileReceipt(receipt, cmd.RequestHash); err != nil {
			httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, "idempotency key reused with a different request", nil)
			return ports.Command{}, false
		}
		httpapi.WriteReceiptReplay(w, receipt)
		return ports.Command{}, false
	}
	return cmd, true
}
