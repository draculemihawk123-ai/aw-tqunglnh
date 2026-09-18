package workitem

import (
	"strings"

	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// scopeGrantBody is the wire shape of one repository scope grant — reused
// by `work-item create`'s own initialScope and `work-item create-child`'s
// own effectiveScope, mirroring
// internal/delivery/httpapi/workitem/dto.go's own scopeGrantBody exactly
// (that file's own doc comment explains why every route/leaf defines its
// own wire DTO rather than exposing workapp.ScopeGrantRequest directly on
// the wire — it carries no json tags of its own).
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
// internal/delivery/httpapi/workitem/dto.go's own validateScopeGrantBodies
// checks before ever dispatching — checked here first so a malformed grant
// gets a precise cli.UsageError instead of falling through to the real
// application command's own generic validation error. field is the flag/
// body field name this list came from ("initialScope" or "effectiveScope"),
// used to build an exact "field[i].subfield" pointer in the error message.
func validateScopeGrantBodies(field string, grants []scopeGrantBody) error {
	if len(grants) == 0 {
		return usageErrorf("%s: at least one entry is required", field)
	}
	seen := make(map[string]bool, len(grants))
	for i, g := range grants {
		if strings.TrimSpace(g.RepositoryID) == "" {
			return usageErrorf("%s[%d].repositoryId is required", field, i)
		}
		if seen[g.RepositoryID] {
			return usageErrorf("%s[%d].repositoryId: duplicate repository in the same request", field, i)
		}
		seen[g.RepositoryID] = true
		if g.Access != string(workdomain.RepositoryRead) && g.Access != string(workdomain.RepositoryWrite) {
			return usageErrorf("%s[%d].access must be READ or WRITE", field, i)
		}
		if strings.TrimSpace(g.Reason) == "" {
			return usageErrorf("%s[%d].reason is required", field, i)
		}
	}
	return nil
}
