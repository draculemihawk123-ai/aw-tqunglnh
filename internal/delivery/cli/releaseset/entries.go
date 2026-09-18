package releaseset

import (
	"strings"

	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
)

// repositoryReleaseBody is one repository's own wire shape inside
// createReleaseSetBody's own Repositories list — mirrors
// workapp.RepositoryReleaseRequest exactly (that application-layer type
// carries no json tags of its own) and
// internal/delivery/httpapi/releaseset/dto.go's own repositoryReleaseBody
// byte-for-byte, the same "every route/leaf defines its own wire DTO rather
// than exposing an application/domain type on the wire" convention
// internal/delivery/cli/workitem's own scopeGrantBody already establishes.
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

// validateRepositoryReleaseBodies enforces, client-side, the same non-blank/
// known-verdict/no-duplicate shape workdomain.NewReleaseSet's own
// validation (internal/domain/work/release_set.go) would otherwise catch
// only with a bare, un-typed error this leaf has no field-level pointer
// for — checked here first so a malformed entry gets a precise, per-entry
// cli.UsageError instead of falling through to the real application
// command's own generic validation error (the "partial" Verify bullet: "an
// entry list with a duplicate repositoryId or invalid verdict is rejected
// before any partial ReleaseSet is created"). Mirrors
// internal/delivery/httpapi/releaseset/dto.go's own
// validateRepositoryReleaseBodies exactly, adapted to return a cli.UsageError
// instead of writing an HTTP response.
func validateRepositoryReleaseBodies(field string, repositories []repositoryReleaseBody) error {
	if len(repositories) == 0 {
		return usageErrorf("%s: at least one entry is required", field)
	}
	seen := make(map[string]bool, len(repositories))
	for i, r := range repositories {
		if strings.TrimSpace(r.RepositoryID) == "" {
			return usageErrorf("%s[%d].repositoryId is required", field, i)
		}
		if seen[r.RepositoryID] {
			return usageErrorf("%s[%d].repositoryId: duplicate repository in the same request", field, i)
		}
		seen[r.RepositoryID] = true
		if strings.TrimSpace(r.BaseVCSObjectID) == "" {
			return usageErrorf("%s[%d].baseVcsObjectId is required", field, i)
		}
		if strings.TrimSpace(r.ResultVCSObjectID) == "" {
			return usageErrorf("%s[%d].resultVcsObjectId is required", field, i)
		}
		if !gate.Verdict(r.Verdict).IsValid() {
			return usageErrorf("%s[%d].verdict must be one of PASS, FAIL, ERROR, NOT_RUN, NOT_APPLICABLE", field, i)
		}
	}
	return nil
}
