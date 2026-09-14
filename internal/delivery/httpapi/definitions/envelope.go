package definitions

import (
	"context"
	"net/http"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// maxBodyBytes bounds every request body this package's own
// httpapi.CanonicalizeJSON call decodes — V6-05's own "Phạm vi: bounded
// text/file payload" line, the same 1 MiB bound every sibling endpoint
// package (workitem, catalog) already settled on for its own largest
// legitimate body.
const maxBodyBytes = 1 << 20 // 1 MiB

// prepareCommand is every CREATE-shaped mutation this package owns
// (CreateDefinition, PublishDefinitionVersion — see routes.go's own doc
// comment for why publish is create-shaped, never update-shaped: neither
// wrapped application command takes an ExpectedVersion) own shared
// preamble: require Idempotency-Key, strictly decode+canonicalize the
// body into dst, and build the resulting ports.Command. Mirrors
// internal/delivery/httpapi/workitem's own prepareCreateCommand exactly.
func prepareCommand(w http.ResponseWriter, r *http.Request, deps Dependencies, commandType string, scope ports.CommandScope, dst any) (cmd ports.Command, ok bool) {
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
// reconciles it against cmd's own RequestHash — V6-02's own documented
// flow step "receipt lookup → replay/conflict". Mirrors
// internal/delivery/httpapi/workitem's own replayOrProceed exactly.
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

// pathKind extracts and validates the {kind} path wildcard against
// definition.Kind's nine closed values, case-insensitively (so
// "block"/"BLOCK" both work — mirrors cmd/aw/definition.go's own
// parseDefinitionKind). Every create/validate/publish/detail/list-versions
// route in this package carries this segment; the two bare-VersionID
// routes (get one version, diff) deliberately do not — see routes.go's own
// doc comment.
func pathKind(w http.ResponseWriter, r *http.Request) (definition.Kind, bool) {
	kind := definition.Kind(strings.ToUpper(strings.TrimSpace(r.PathValue("kind"))))
	if !kind.Valid() {
		writeValidationError(w, "kind", "must be one of WORKFLOW, BLOCK, SKILL, LAYER, ENGINEERING_PACK, AGENT_PROFILE, COMMAND, GATE, POLICY")
		return "", false
	}
	return kind, true
}
