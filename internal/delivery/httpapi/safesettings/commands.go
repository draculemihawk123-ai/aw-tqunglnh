package safesettings

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	safesettingsapp "github.com/taQuangLing/agent-workflow/internal/app/safesettings"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/safesettings"
)

// maxBodyBytes bounds PUT /settings/safe's own request body — generous for
// the largest legitimate body this route ever accepts (7 scalar fields, no
// list/array anywhere in the allowlist) while still refusing an unbounded
// read, the same discipline internal/delivery/httpapi/workitem's own
// maxBodyBytes doc comment establishes for its own, larger bodies.
const maxBodyBytes = 1 << 16 // 64 KiB

// handleUpdateSafeSettings implements PUT /settings/safe (operationId
// updateSafeSettings): dispatches internal/app/safesettings.
// UpdateSafeSettings. Follows the identical flow every other V6-02-wrapped
// mutation in this codebase already follows (see
// internal/delivery/httpapi/workitem's own package doc comment): require
// Idempotency-Key + strong If-Match → strictly decode+canonicalize the body
// → pre-dispatch semantic validation → receipt lookup → replay/conflict →
// "nếu absent mới kiểm current version" → dispatch → encode result.
//
// Two deliberate departures from that generic shape, both load-bearing for
// this task's own Verify checklist:
//
//  1. The request body is decoded DIRECTLY into safesettings.SafeSettings
//     (domain type) rather than a package-local wire struct: that type's
//     own UnmarshalJSON already IS the exact strict decoder this task's own
//     "strict unknown-field rejection" line asks for (DisallowUnknownFields,
//     the identical convention DecodeJSON/CanonicalizeJSON already use for
//     every other endpoint) — reusing it here means an unknown/forbidden
//     field (a typo, or an explicitly forbidden one like "databasePath")
//     is rejected at the SAME layer, with the SAME httpapi.WriteDecodeError
//     mapping (400 INVALID_REQUEST), every other endpoint in this codebase
//     already uses, rather than a second, hand-rolled wire DTO that could
//     silently drift from the domain type's own allowlist over time.
//  2. A true idempotent replay (see replayOrProceed below) does NOT call
//     httpapi.WriteReceiptReplay directly the way every other endpoint's
//     own replayOrProceed helper does: that function writes the STORED
//     ResultJSON verbatim, which is safesettingsapp.SafeSettingsResult's
//     own plain JSON encoding — containing ProviderCredentialRef in
//     CLEARTEXT (the receipt was recorded by UpdateSafeSettings' own
//     transaction, which has no reason to know about this HTTP layer's own
//     masking convention) and missing the "effective" object entirely (that
//     Effective value never existed inside the stored application
//     result at all — it is purely an HTTP-layer addition, see
//     dependencies.go's own Effective doc comment). A replay here instead
//     decodes the stored ResultJSON back into a SafeSettingsResult and
//     re-renders it through the SAME buildResponse this handler's own fresh
//     path uses — a replayed PUT and a fresh PUT are therefore always
//     byte-for-byte identically SHAPED (desired/effective/version/
//     restartRequired, credential ref always masked), which is both this
//     task's own "same result" contract for a true replay and the one way
//     to satisfy "never echoed back in cleartext" for every response this
//     endpoint can ever produce, replay included.
func handleUpdateSafeSettings(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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

		var desired safesettings.SafeSettings
		canonical, err := httpapi.CanonicalizeJSON(r, maxBodyBytes, &desired)
		if err != nil {
			httpapi.WriteDecodeError(w, err)
			return
		}
		// Pre-dispatch semantic validation — the SAME safesettings.Validate
		// UpdateSafeSettings' own transaction re-checks, called here first
		// so a caller gets a clean 400 with the real validation message
		// before this handler ever computes a semantic hash or touches
		// storage (mirrors internal/delivery/httpapi/workitem's own
		// "field-presence guards ... always re-validated by this package's
		// own handler before it ever dispatches" discipline).
		if err := safesettings.Validate(desired); err != nil {
			writeValidationError(w, "desired", err.Error())
			return
		}

		scope := ports.InstallationScope()
		hash := httpapi.SemanticHash("UpdateSafeSettings", scope, canonical, "", expectedVersion)
		principal := httpapi.PrincipalFromContext(r.Context())
		cmd := ports.Command{
			ID: "UpdateSafeSettings-" + idempotencyKey, IdempotencyKey: idempotencyKey, Actor: principal.Actor,
			ActorRoles: principal.Roles, CorrelationID: httpapi.CorrelationIDFromContext(r.Context()),
			Scope: scope, ExpectedVersion: expectedVersion, RequestedAt: deps.Clock.Now(),
			Type: "UpdateSafeSettings", RequestHash: hash,
		}

		if replayOrProceed(r.Context(), w, deps, cmd) {
			return
		}

		// "nếu absent mới kiểm current version" (V6-02's own flow): reloaded
		// AFTER the replay decision — WriteReceiptReplay's own doc comment
		// is explicit that a true replay "never re-validates If-Match/
		// current version" — so this check only ever applies to a
		// genuinely fresh request.
		current, err := safesettingsapp.GetSafeSettings(r.Context(), deps.UnitOfWork)
		if err != nil {
			httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
			return
		}
		if current.Version != expectedVersion {
			writePreconditionFailed(w, "If-Match does not match the current version; reload and retry")
			return
		}

		result, err := safesettingsapp.UpdateSafeSettings(r.Context(), deps.UnitOfWork, deps.IDs, cmd,
			safesettingsapp.UpdateSafeSettingsRequest{DesiredJSON: json.RawMessage(canonical)})
		if err != nil {
			writeCommandError(w, err)
			return
		}
		response := buildResponse(result, deps.Effective, deps.Matcher)
		_ = httpapi.EncodeResult(w, http.StatusOK, response, httpapi.ETagFromVersion(result.Version))
	}
}

// replayOrProceed looks up an existing receipt for cmd and, if found,
// reconciles it against cmd's own RequestHash — V6-02's own documented flow
// step "receipt lookup → replay/conflict". A true replay (same hash)
// re-renders the stored result through buildResponse (see this file's own
// top-of-file doc comment for why, unlike every other endpoint's own
// replayOrProceed, this never calls httpapi.WriteReceiptReplay for a
// SUCCESSFUL stored result) and reports handled=true. A hash conflict
// (different hash, same key) writes 409 and also reports handled=true. "not
// found" (a genuinely fresh key) reports handled=false so the caller
// proceeds to its own next step.
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
	if receipt.ErrorCode != "" {
		// A stored failure carries no secret data (UpdateSafeSettings only
		// ever records a receipt on its own success path — see that
		// function's own source — so this branch is defense-in-depth, kept
		// for the same uniform "replay a stored failure the same way every
		// other endpoint does" reason workitem's own replayOrProceed does).
		httpapi.WriteReceiptReplay(w, receipt)
		return true
	}
	var result safesettingsapp.SafeSettingsResult
	if err := json.Unmarshal([]byte(receipt.ResultJSON), &result); err != nil {
		httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
		return true
	}
	response := buildResponse(result, deps.Effective, deps.Matcher)
	_ = httpapi.EncodeResult(w, http.StatusOK, response, httpapi.ETagFromVersion(result.Version))
	return true
}
