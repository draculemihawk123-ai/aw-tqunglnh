package releaseset

import (
	"fmt"
	"net/http"
	"strings"

	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
)

// repositoryReleaseBody is one repository's own wire shape inside
// createReleaseSetBody's own Repositories list — mirrors
// workapp.RepositoryReleaseRequest exactly (that application-layer type
// carries no json tags of its own, and this package never marshals it
// directly — see workitem's own scopeGrantBody for the identical
// precedent/reasoning: every route defines its own wire DTO rather than
// exposing an application/domain type on the wire).
type repositoryReleaseBody struct {
	RepositoryID      string `json:"repositoryId"`
	BaseVCSObjectID   string `json:"baseVcsObjectId"`
	ResultVCSObjectID string `json:"resultVcsObjectId"`
	Verdict           string `json:"verdict"`
}

func (r repositoryReleaseBody) toRequest() workapp.RepositoryReleaseRequest {
	return workapp.RepositoryReleaseRequest{
		RepositoryID: r.RepositoryID, BaseVCSObjectID: r.BaseVCSObjectID,
		ResultVCSObjectID: r.ResultVCSObjectID, Verdict: r.Verdict,
	}
}

func toRepositoryReleaseRequests(repositories []repositoryReleaseBody) []workapp.RepositoryReleaseRequest {
	out := make([]workapp.RepositoryReleaseRequest, 0, len(repositories))
	for _, r := range repositories {
		out = append(out, r.toRequest())
	}
	return out
}

// validateRepositoryReleaseBodies enforces, at the HTTP layer, the same
// non-blank/known-verdict/no-duplicate shape workdomain.NewReleaseSet's own
// validation (internal/domain/work/release_set.go) would otherwise catch
// only with a bare, un-typed `errors.New(...)` this package's own
// writeCommandError has no sentinel to route precisely — checked here
// first so a malformed entry gets a precise per-entry ErrorDetail instead
// of falling through to a generic 500 (mirrors workitem's own
// validateScopeGrantBodies exactly, adapted to ReleaseSet's own field set).
func validateRepositoryReleaseBodies(w http.ResponseWriter, repositories []repositoryReleaseBody) bool {
	if len(repositories) == 0 {
		writeValidationError(w, "repositories", "at least one entry is required")
		return false
	}
	seen := make(map[string]bool, len(repositories))
	for i, r := range repositories {
		if strings.TrimSpace(r.RepositoryID) == "" {
			writeValidationError(w, fmt.Sprintf("repositories[%d].repositoryId", i), "is required")
			return false
		}
		if seen[r.RepositoryID] {
			writeValidationError(w, fmt.Sprintf("repositories[%d].repositoryId", i), "duplicate repository in the same request")
			return false
		}
		seen[r.RepositoryID] = true
		if strings.TrimSpace(r.BaseVCSObjectID) == "" {
			writeValidationError(w, fmt.Sprintf("repositories[%d].baseVcsObjectId", i), "is required")
			return false
		}
		if strings.TrimSpace(r.ResultVCSObjectID) == "" {
			writeValidationError(w, fmt.Sprintf("repositories[%d].resultVcsObjectId", i), "is required")
			return false
		}
		if !gate.Verdict(r.Verdict).IsValid() {
			writeValidationError(w, fmt.Sprintf("repositories[%d].verdict", i), "must be one of PASS, FAIL, ERROR, NOT_RUN, NOT_APPLICABLE")
			return false
		}
	}
	return true
}

// emptyBody is the wire shape of a mutation with no payload beyond the
// path/headers themselves (sealReleaseSet, abandonReleaseSet) — the caller
// still sends a literal `{}` JSON object body, kept uniform with every
// other mutating route's own httpapi.CanonicalizeJSON decode step rather
// than a special-cased bodyless branch. Mirrors workitem's own identical
// emptyBody.
type emptyBody struct{}
