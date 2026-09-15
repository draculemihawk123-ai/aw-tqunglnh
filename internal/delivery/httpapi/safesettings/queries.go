package safesettings

import (
	"net/http"

	safesettingsapp "github.com/taQuangLing/agent-workflow/internal/app/safesettings"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// handleGetSafeSettings implements GET /settings/safe (operationId
// getSafeSettings): a plain read, no Idempotency-Key/If-Match — dispatches
// internal/app/safesettings.GetSafeSettings, then combines it with this
// process' own fixed, boot-time Effective snapshot (deps.Effective) into
// the shared responseDTO. The ETag response header carries the current
// version, ready for a subsequent PUT's own If-Match.
func handleGetSafeSettings(deps Dependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := safesettingsapp.GetSafeSettings(r.Context(), deps.UnitOfWork)
		if err != nil {
			// Not a leakage-sensitive lookup (this is a singleton,
			// installation-wide resource with no per-caller scope to hide) —
			// a plain 500 is the correct response for either "migration
			// never ran" (should never happen) or ports.ErrSafeSettingsCorrupt
			// (V6-10G's own readiness/Doctor typed path is the one that names
			// that condition explicitly; this endpoint's own Verify scope
			// does not ask for a second, HTTP-specific typed response).
			httpapi.WriteError(w, http.StatusInternalServerError, httpapi.ErrorCodeInternal, "internal error", nil)
			return
		}
		response := buildResponse(result, deps.Effective, deps.Matcher)
		_ = httpapi.EncodeResult(w, http.StatusOK, response, httpapi.ETagFromVersion(result.Version))
	}
}
