package workitem

import (
	"context"
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// maxBodyBytes bounds every request body this package's own
// httpapi.CanonicalizeJSON call decodes — a route-local limit on top of
// httpapi.MaxBytes' own server-wide ceiling (server.go's Config.MaxBodyBytes),
// generous enough for the largest legitimate body this package ever
// accepts (CreateRootWorkItemRequest.InitialScope, a bounded list of scope
// grants) while still refusing an unbounded read.
const maxBodyBytes = 1 << 20 // 1 MiB

// prepareCreateCommand is every CREATE-shaped mutation's own shared
// preamble (CreateRootWorkItem, CreateChildWorkItem, RequestScopeExpansion):
// require Idempotency-Key, strictly decode+canonicalize the body into dst,
// and build the resulting ports.Command. ExpectedVersion is always 0 — a
// create has no existing resource to precondition against, and none of
// these three routes requires If-Match (V6-02's own rule is scoped to
// "update", never "create"). scope must already be the AUTHORITATIVE scope
// this package's own caller derived by reloading the route's real target
// (see each handler's own doc comment) — never built from the path alone.
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

// prepareUpdateCommand is every UPDATE-shaped mutation's own shared
// preamble (ApproveScopeExpansion, RejectScopeExpansion,
// WithdrawScopeExpansion): require both Idempotency-Key and a strong
// If-Match (V6-02's own "update bắt buộc strong If-Match"), strictly
// decode+canonicalize the body into dst, and build the resulting
// ports.Command with ExpectedVersion set from the parsed If-Match. Mirrors
// prepareCreateCommand exactly except for that one extra required header —
// see this package's own scope_expansion_commands.go for why the returned
// expectedVersion is then checked against an ALREADY-reloaded target rather
// than triggering a second load.
func prepareUpdateCommand(w http.ResponseWriter, r *http.Request, deps Dependencies, commandType string, scope ports.CommandScope, dst any) (cmd ports.Command, expectedVersion uint64, ok bool) {
	idempotencyKey, err := httpapi.RequireIdempotencyKey(r)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
		return ports.Command{}, 0, false
	}
	ifMatch, err := httpapi.RequireIfMatch(r)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
		return ports.Command{}, 0, false
	}
	expectedVersion, err = httpapi.VersionFromETag(ifMatch)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "If-Match is not a valid ETag", nil)
		return ports.Command{}, 0, false
	}
	canonical, err := httpapi.CanonicalizeJSON(r, maxBodyBytes, dst)
	if err != nil {
		httpapi.WriteDecodeError(w, err)
		return ports.Command{}, 0, false
	}
	hash := httpapi.SemanticHash(commandType, scope, canonical, "", expectedVersion)
	principal := httpapi.PrincipalFromContext(r.Context())
	cmd = ports.Command{
		ID: commandType + "-" + idempotencyKey, IdempotencyKey: idempotencyKey, Actor: principal.Actor,
		ActorRoles: principal.Roles, CorrelationID: httpapi.CorrelationIDFromContext(r.Context()),
		Scope: scope, ExpectedVersion: expectedVersion, RequestedAt: deps.Clock.Now(), Type: commandType, RequestHash: hash,
	}
	return cmd, expectedVersion, true
}

// replayOrProceed looks up an existing receipt for cmd and, if found,
// reconciles it against cmd's own RequestHash — V6-02's own documented flow
// step "receipt lookup → replay/conflict". A true replay (same hash) writes
// the stored result verbatim and reports handled=true; a hash conflict
// (different hash, same key) writes 409 and also reports handled=true —
// either way there is nothing left for the caller's own handler to do. "not
// found" (a genuinely fresh key) reports handled=false so the caller
// proceeds to its own next step (V6-02's own "nếu absent mới kiểm current
// version/external prework/dispatch").
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
