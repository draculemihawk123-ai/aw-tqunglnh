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

// acceptanceCriterionBody is the wire shape of one acceptance criterion inside
// workItemContractBody. An empty verificationRef is a legitimate,
// descriptive-only criterion (it just does not count as executable for
// readiness).
type acceptanceCriterionBody struct {
	Description     string `json:"description"`
	VerificationRef string `json:"verificationRef,omitempty"`
}

// workItemContractBody is the wire shape of the optional "contract" object
// POST /projects/{projectId}/work-items and
// POST /projects/{projectId}/work-items/{workItemId}/children both accept
// (V6-04B), mirroring workapp.WorkItemContractRequest exactly. Every field is
// optional, and every one is omitempty so that "given but zero" and "omitted"
// canonicalize to the same bytes (and therefore hash identically for
// idempotency) — a zero value means "not given" throughout. Deliberately no
// status/state/family/workspace-style field of any kind: a contract can only
// ever describe what a WorkItem promises, never move it anywhere, and the
// strict decode (DisallowUnknownFields, applied recursively) rejects any key
// not listed here with a 400.
type workItemContractBody struct {
	SchemaVersion      int                       `json:"schemaVersion,omitempty"`
	Behavior           string                    `json:"behavior,omitempty"`
	AcceptanceCriteria []acceptanceCriterionBody `json:"acceptanceCriteria,omitempty"`
	VerificationSpec   string                    `json:"verificationSpec,omitempty"`
	RiskLevel          string                    `json:"riskLevel,omitempty"`
	Exclusions         []string                  `json:"exclusions,omitempty"`
	WorkflowVersionID  string                    `json:"workflowVersionId,omitempty"`
}

// toRequest converts b into the application-layer request; a nil b (no
// "contract" key in the body) stays nil, which is exactly "create the WorkItem
// with an empty contract, as before".
func (b *workItemContractBody) toRequest() *workapp.WorkItemContractRequest {
	if b == nil {
		return nil
	}
	req := &workapp.WorkItemContractRequest{
		SchemaVersion: b.SchemaVersion, Behavior: b.Behavior, VerificationSpec: b.VerificationSpec,
		RiskLevel: b.RiskLevel, Exclusions: b.Exclusions, WorkflowVersionID: b.WorkflowVersionID,
	}
	if len(b.AcceptanceCriteria) > 0 {
		req.AcceptanceCriteria = make([]workapp.AcceptanceCriterionRequest, 0, len(b.AcceptanceCriteria))
		for _, criterion := range b.AcceptanceCriteria {
			req.AcceptanceCriteria = append(req.AcceptanceCriteria, workapp.AcceptanceCriterionRequest{
				Description: criterion.Description, VerificationRef: criterion.VerificationRef,
			})
		}
	}
	return req
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
