package message

import (
	"context"
	"net/http"

	appmessage "github.com/taQuangLing/agent-workflow/internal/app/message"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// maxBodyBytes bounds every request body this package's own
// httpapi.CanonicalizeJSON call decodes — a route-local limit on top of
// httpapi.MaxBytes' own server-wide ceiling (server.go's Config.MaxBodyBytes,
// cmd/aw/serve.go's own --max-body-bytes flag). Deliberately set to TWICE
// appmessage.MaxContentSize, not a flat 1 MiB the way
// internal/delivery/httpapi/workitem's own maxBodyBytes is: appendMessageBody
// carries Content as a plain JSON string field (append.go's own "Không làm:
// KHÔNG binary upload" line), and JSON string-escaping plus the request's
// other small fields can inflate the wire body somewhat past the raw
// content byte count — a request whose Content is legitimately exactly at
// appmessage.MaxContentSize must still reach that command's own
// authoritative size check (and its own ErrContentTooLarge error) rather
// than being rejected earlier, at this transport layer, by a limit tuned
// too tight to even decode it.
const maxBodyBytes = 2 * appmessage.MaxContentSize

// prepareCreateCommand is AppendMessage's own shared preamble — mirrors
// internal/delivery/httpapi/workitem's own prepareCreateCommand exactly
// (require Idempotency-Key, strictly decode+canonicalize the body into
// dst, build the resulting ports.Command; ExpectedVersion is always 0
// since AppendMessage has no existing resource to precondition against and
// this package's own routes never require If-Match — a Message is
// immutable/append-only, never updated). scope must already be the
// AUTHORITATIVE scope this package's own caller derived by reloading the
// route's real target (workapp.GetWorkItem) — never built from the path
// alone.
//
// On any failure this writes the appropriate error response itself and
// returns ok=false; the caller's own handler just returns immediately.
func prepareCreateCommand(w http.ResponseWriter, r *http.Request, deps Dependencies, commandType string, scope ports.CommandScope, dst any) (cmd ports.Command, ok bool) {
	idempotencyKey, err := httpapi.RequireIdempotencyKey(r)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
		return ports.Command{}, false
	}
	canonical, err := httpapi.CanonicalizeJSON(r, maxBodyBytes, dst)
	if err != nil {
		httpapi.WriteDecodeError(w, err)
		return ports.Command{}, false
	}
	hash := httpapi.SemanticHash(commandType, scope, canonical, "", 0)
	principal := httpapi.PrincipalFromContext(r.Context())
	cmd = ports.Command{
		ID: commandType + "-" + idempotencyKey, IdempotencyKey: idempotencyKey, Actor: principal.Actor,
		ActorRoles: principal.Roles, CorrelationID: httpapi.CorrelationIDFromContext(r.Context()),
		Scope: scope, RequestedAt: deps.Clock.Now(), Type: commandType, RequestHash: hash,
	}
	return cmd, true
}

// replayOrProceed looks up an existing receipt for cmd and, if found,
// reconciles it against cmd's own RequestHash — mirrors
// internal/delivery/httpapi/workitem's own replayOrProceed exactly (see
// that function's own doc comment for the full flow this repeats).
func replayOrProceed(ctx context.Context, w http.ResponseWriter, deps Dependencies, cmd ports.Command) (handled bool) {
	receipt, found, err := httpapi.LookupReceipt(ctx, deps.UnitOfWork, cmd.Actor, cmd.Scope, cmd.IdempotencyKey, cmd.Type)
	if err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
		return true
	}
	if !found {
		return false
	}
	if err := httpapi.ReconcileReceipt(receipt, cmd.RequestHash); err != nil {
		writeReceiptHashConflict(w)
		return true
	}
	httpapi.WriteReceiptReplay(w, receipt)
	return true
}
