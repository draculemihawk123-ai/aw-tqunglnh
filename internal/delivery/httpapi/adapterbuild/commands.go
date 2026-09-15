package adapterbuild

import (
	"net/http"
	"strings"

	appadapterbuild "github.com/taQuangLing/agent-workflow/internal/app/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	domainadapterbuild "github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
)

// maxBodyBytes bounds every request body this package's own
// httpapi.CanonicalizeJSON call decodes — both bodies this package ever
// decodes (a probe request, a register request) are a handful of short
// string fields plus a bounded CanonicalEventKinds list, the same
// reasoning internal/delivery/httpapi/workitem's own identical constant
// documents.
const maxBodyBytes = 1 << 20 // 1 MiB

// prepareCommand is this package's own shared preamble for both mutating
// routes (ProbeAdapterBuild, RegisterAdapterBuild): require Idempotency-Key,
// strictly decode+canonicalize the body into dst, and build the resulting
// installation-scoped ports.Command — mirroring
// internal/delivery/httpapi/workitem's own prepareCreateCommand exactly
// (both of this package's own commands are "create-shaped": ExpectedVersion
// is always 0, no If-Match is ever required, since neither command
// preconditions against an existing resource's own version — Probe creates
// no registry row at all, and Register's own target does not exist yet).
//
// Unlike prepareCreateCommand, this function's own caller never follows it
// with a receipt-replay fast path (httpapi.LookupReceipt/ReconcileReceipt/
// WriteReceiptReplay) — see this package's own top-of-file doc comment
// (adapterbuild.go) for why: V6-10J's own "Không làm: ... transport
// receipt" line means this package must never invent a second,
// HTTP-layer-only replay check on top of what ProbeAdapterBuild/
// RegisterAdapterBuild already do internally. Every command built here is
// dispatched straight through, unconditionally.
func prepareCommand(w http.ResponseWriter, r *http.Request, deps Dependencies, commandType string, dst any) (cmd ports.Command, ok bool) {
	idempotencyKey, err := httpapi.RequireIdempotencyKey(r)
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, httpapi.ErrorCodeInvalidRequest, err.Error(), nil)
		return ports.Command{}, false
	}
	scope := ports.InstallationScope()
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

// handleProbeAdapterBuild implements POST /adapter-builds/probe
// (operationId probeAdapterBuild): dispatches
// appadapterbuild.ProbeAdapterBuild. 200 OK, never 201 — a probe never
// creates a registry row (this task's own spec: "although registry state
// does not change"), it only returns a candidate the caller may later
// confirm via POST /adapter-builds.
func handleProbeAdapterBuild(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body probeAdapterBuildBody
		cmd, ok := prepareCommand(w, r, deps, commandTypeProbe, &body)
		if !ok {
			return
		}
		if !validateProbeBody(w, body) {
			return
		}

		token, err := appadapterbuild.ProbeAdapterBuild(r.Context(), deps.UnitOfWork, cmd, appadapterbuild.ProbeRequest{
			ProviderKey: body.ProviderKey, ExecutablePath: body.ExecutablePath, ProtocolVersion: body.ProtocolVersion,
			CapabilityManifest: body.CapabilityManifest.toManifest(), OS: body.OS, Toolchain: body.Toolchain,
			ConfigIdentity: body.ConfigIdentity,
		})
		if err != nil {
			writeProbeError(w, err)
			return
		}
		_ = httpapi.EncodeResult(w, http.StatusOK, token, "")
	}
}

// validateProbeBody checks probeAdapterBuildBody's own required fields
// before ever building a command envelope — the same "malformed request
// never even computes a semantic hash" discipline
// internal/delivery/httpapi/workitem's own writeValidationError doc comment
// documents. The plain string fields have no sentinel error to catch on the
// way back out of appadapterbuild.ProbeAdapterBuild (ProbeRequest.validate()
// returns a bare errors.New for each), so they are checked here explicitly;
// CapabilityManifest reuses the real, exported
// domainadapterbuild.ValidateCapabilityManifest instead of duplicating its
// rules.
func validateProbeBody(w http.ResponseWriter, body probeAdapterBuildBody) bool {
	switch {
	case strings.TrimSpace(body.ProviderKey) == "":
		writeValidationError(w, "providerKey", "is required")
		return false
	case strings.TrimSpace(body.ExecutablePath) == "":
		writeValidationError(w, "executablePath", "is required")
		return false
	case strings.TrimSpace(body.ProtocolVersion) == "":
		writeValidationError(w, "protocolVersion", "is required")
		return false
	case strings.TrimSpace(body.OS) == "":
		writeValidationError(w, "os", "is required")
		return false
	case strings.TrimSpace(body.Toolchain) == "":
		writeValidationError(w, "toolchain", "is required")
		return false
	case strings.TrimSpace(body.ConfigIdentity) == "":
		writeValidationError(w, "configIdentity", "is required")
		return false
	}
	if err := domainadapterbuild.ValidateCapabilityManifest(body.CapabilityManifest.toManifest()); err != nil {
		writeValidationError(w, "capabilityManifest", "must have supportsStart=true and no empty/duplicate canonicalEventKinds")
		return false
	}
	return true
}

// handleRegisterAdapterBuild implements POST /adapter-builds (operationId
// registerAdapterBuild): dispatches appadapterbuild.RegisterAdapterBuild.
// 201 Created for a genuinely new registry row, 200 OK when the exact same
// measured tuple was already registered (RegisterResult.AlreadyExisted) —
// the content-hash fingerprint dedup axis V6-10I's own doc comment
// documents as independent from command-receipt replay; either way the
// response body is the same registerAdapterBuildResponse shape.
func handleRegisterAdapterBuild(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body registerAdapterBuildBody
		cmd, ok := prepareCommand(w, r, deps, commandTypeRegister, &body)
		if !ok {
			return
		}

		result, err := appadapterbuild.RegisterAdapterBuild(r.Context(), deps.UnitOfWork, cmd, appadapterbuild.RegisterRequest{
			Token: body.Token, CapabilityManifest: body.CapabilityManifest.toManifest(),
		})
		if err != nil {
			writeRegisterError(w, err)
			return
		}
		status := http.StatusCreated
		if result.AlreadyExisted {
			status = http.StatusOK
		}
		_ = httpapi.EncodeResult(w, status, registerAdapterBuildResponse{
			Build: newAdapterBuildView(result.Build), AlreadyExisted: result.AlreadyExisted,
		}, "")
	}
}
