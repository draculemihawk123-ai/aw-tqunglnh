package adapterbuild

import (
	"errors"
	"net/http"

	appadapterbuild "github.com/taQuangLing/agent-workflow/internal/app/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	domainadapterbuild "github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
)

// writeValidationError writes a single field-level 400 — mirrors
// internal/delivery/httpapi/workitem's own identical helper.
func writeValidationError(w http.ResponseWriter, field, message string) {
	httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, "request validation failed",
		[]httpapi.ErrorDetail{{Field: field, Message: message}})
}

// writeQueryError maps a query-side error from
// appadapterbuild.ListAdapterBuilds/GetAdapterBuild to the shared httpapi
// envelope. GetAdapterBuild's own ports.ErrAdapterBuildNotFound is the only
// named sentinel either query can return (internal/app/adapterbuild/commands.go);
// everything else is an unexpected internal failure.
func writeQueryError(w http.ResponseWriter, err error) {
	if errors.Is(err, ports.ErrAdapterBuildNotFound) {
		httpapi.WriteResourceHidden(w)
		return
	}
	httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
}

// writeProbeError maps every named sentinel appadapterbuild.ProbeAdapterBuild
// can actually return — enumerated by reading that function's own source
// (internal/app/adapterbuild/commands.go):
//
//   - ports.ErrScopeMismatch: requireInstallationScope's own rejection of a
//     non-installation command scope. This package's own prepareCommand
//     always builds ports.InstallationScope() itself, so a well-formed
//     request reaching this handler can never actually trigger this —
//     defense-in-depth only, falls through to the same 500 default as any
//     other unmatched error.
//   - domainadapterbuild.ErrInvalidCapabilityManifest: ProbeRequest.validate()'s
//     own manifest check. validateProbeBody (commands.go) already calls the
//     same real domainadapterbuild.ValidateCapabilityManifest before ever
//     dispatching, so in practice this branch is defense-in-depth too — kept
//     explicit rather than folded into the 500 default since it IS a
//     genuine caller-request problem, never an internal fault, if it is ever
//     reached.
//   - ports.ErrReceiptConflict: a genuine concurrent race past this
//     package's own deliberate absence of a pre-dispatch receipt check (see
//     adapterbuild.go's own top-of-file doc comment for why there is none)
//     — two callers raced the identical Idempotency-Key with two different
//     request bodies, caught by ProbeAdapterBuild's own internal receipt
//     recheck.
func writeProbeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domainadapterbuild.ErrInvalidCapabilityManifest):
		writeValidationError(w, "capabilityManifest", "must have supportsStart=true and no empty/duplicate canonicalEventKinds")
	case errors.Is(err, ports.ErrReceiptConflict):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, "idempotency key reused with a different request", nil)
	default:
		httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
	}
}

// writeRegisterError maps every named sentinel
// appadapterbuild.RegisterAdapterBuild can actually return — enumerated by
// reading that function's own source (internal/app/adapterbuild/commands.go)
// and the two real dependencies it calls into
// (internal/domain/adapterbuild/token.go's VerifyToken,
// internal/app/ports's AdapterBuildRepository):
//
//   - domainadapterbuild.ErrInvalidSignature: the token's signature does not
//     match what the current signing key would have produced — a forged,
//     tampered, or cross-installation token. A caller mistake, not a race:
//     400.
//   - domainadapterbuild.ErrTokenExpired: an otherwise genuine token whose
//     tokenTTL window has passed — the caller must probe again for a fresh
//     candidate. A real, visible state condition: 409, not 400 (nothing
//     about the request itself was malformed).
//   - ports.ErrNoSigningKey: RegisterAdapterBuild called before any Probe has
//     ever run on this installation (LoadSigningKey, never
//     LoadOrCreateSigningKey — RegisterAdapterBuild must never itself
//     bootstrap the key). A precondition-not-met business conflict: 409.
//   - appadapterbuild.ErrExecutableDrift / ErrCapabilityManifestDrift: the
//     TOCTOU-closing re-measurement (ADR-022) found the executable or
//     capability manifest no longer matches what the token pinned. Routed
//     through the shared httpapi.StatusForAppErrorCode(errorcode.CodeAdapterBuildDrift)
//     table rather than a hardcoded status/code pair, so this stays
//     byte-for-byte consistent with that one shared mapping if it is ever
//     revisited (409, ErrorCodeConflict).
//   - ports.ErrReceiptConflict: the identical concurrent-race case
//     writeProbeError documents, on RegisterAdapterBuild's own receipt axis.
func writeRegisterError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domainadapterbuild.ErrInvalidSignature):
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest,
			"candidate token signature is invalid; probe again to obtain a fresh token", nil)
	case errors.Is(err, domainadapterbuild.ErrTokenExpired):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict,
			"candidate token has expired; probe again to obtain a fresh token", nil)
	case errors.Is(err, ports.ErrNoSigningKey):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict,
			"no adapter-build candidate has ever been probed on this installation", nil)
	case errors.Is(err, appadapterbuild.ErrExecutableDrift):
		status, code := httpapi.StatusForAppErrorCode(errorcode.CodeAdapterBuildDrift)
		httpapi.WriteError(w, status, code, "the executable no longer matches what the candidate token pinned; probe again", nil)
	case errors.Is(err, appadapterbuild.ErrCapabilityManifestDrift):
		status, code := httpapi.StatusForAppErrorCode(errorcode.CodeAdapterBuildDrift)
		httpapi.WriteError(w, status, code, "the capability manifest no longer matches what the candidate token pinned; probe again", nil)
	case errors.Is(err, ports.ErrReceiptConflict):
		httpapi.WriteError(w, http.StatusConflict, httpapi.ErrorCodeConflict, "idempotency key reused with a different request", nil)
	default:
		httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
	}
}
