package workitem

import (
	"fmt"
	"net/http"
	"strings"

	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// scopeGrantBody is the wire shape of one repository scope grant — reused
// by CreateRootWorkItem's own InitialScope, CreateChildWorkItem's own
// EffectiveScope and RequestScopeExpansion's own RequestedGrants, mirroring
// workapp.ScopeGrantRequest exactly (that application-layer type carries no
// json tags of its own, and this package never marshals it directly — see
// this package's own top-of-file doc comment in routes.go for why every
// route defines its own wire DTO rather than exposing an application/domain
// type on the wire).
type scopeGrantBody struct {
	RepositoryID string   `json:"repositoryId"`
	Access       string   `json:"access"`
	PathScopes   []string `json:"pathScopes,omitempty"`
	Reason       string   `json:"reason"`
}

func (g scopeGrantBody) toRequest() workapp.ScopeGrantRequest {
	return workapp.ScopeGrantRequest{RepositoryID: g.RepositoryID, Access: g.Access, PathScopes: g.PathScopes, Reason: g.Reason}
}

func toScopeGrantRequests(grants []scopeGrantBody) []workapp.ScopeGrantRequest {
	out := make([]workapp.ScopeGrantRequest, 0, len(grants))
	for _, g := range grants {
		out = append(out, g.toRequest())
	}
	return out
}

// validateScopeGrantBodies enforces the same non-blank/known-access shape
// every one of CreateRootWorkItem/CreateChildWorkItem/RequestScopeExpansion's
// own top-of-function guards already requires (work.NewRepositoryScope/
// work.NewScopeExpansionRequest's own validation) — checked here first so a
// malformed grant gets a precise per-entry ErrorDetail instead of falling
// through to this package's own generic 500 default (writeCommandError's
// own doc comment explains why that default should be unreachable for a
// well-formed request). field is the JSON field name this list came from
// ("initialScope", "effectiveScope" or "requestedGrants"), used to build an
// exact "field[i].subfield" pointer in the response.
func validateScopeGrantBodies(w http.ResponseWriter, field string, grants []scopeGrantBody) bool {
	if len(grants) == 0 {
		writeValidationError(w, field, "at least one entry is required")
		return false
	}
	seen := make(map[string]bool, len(grants))
	for i, g := range grants {
		if strings.TrimSpace(g.RepositoryID) == "" {
			writeValidationError(w, fmt.Sprintf("%s[%d].repositoryId", field, i), "is required")
			return false
		}
		if seen[g.RepositoryID] {
			writeValidationError(w, fmt.Sprintf("%s[%d].repositoryId", field, i), "duplicate repository in the same request")
			return false
		}
		seen[g.RepositoryID] = true
		if g.Access != string(workdomain.RepositoryRead) && g.Access != string(workdomain.RepositoryWrite) {
			writeValidationError(w, fmt.Sprintf("%s[%d].access", field, i), "must be READ or WRITE")
			return false
		}
		if strings.TrimSpace(g.Reason) == "" {
			writeValidationError(w, fmt.Sprintf("%s[%d].reason", field, i), "is required")
			return false
		}
	}
	return true
}
