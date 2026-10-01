package releaseset

import (
	"context"
	"flag"
	"io"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"release-set", "local-commit", "status"}, Scope: cli.ScopeProject,
		AppOperation: "GetReleaseSetLocalCommitStatus", HTTPOperationID: "getReleaseSetLocalCommitStatus",
	})
}

// RunLocalCommitStatus implements `aw release-set local-commit status
// --project-id <id> <releaseSetId> <localCommitId>` — the state of one local
// commit operation, a pure read over workapp.GetReleaseSetLocalCommitStatus
// mirroring internal/delivery/httpapi/releaseset's own
// handleGetReleaseSetLocalCommitStatus exactly: the status is loaded by its
// own id, then required to belong to BOTH the named project and the named
// ReleaseSet, and a mismatch is reported as plain not-found — never a
// distinct "exists elsewhere" answer, so a wrong-project invocation learns
// nothing about whether the id exists.
//
// docs/design/11-v6-00-ux-artifact.md's own Screen 10 row 7 reserves exactly
// this shape. `release-set local-commit --wait` already polls this same query
// internally; this leaf is the standalone observation a timed-out or
// interrupted --wait leaves the operator without.
func RunLocalCommitStatus(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("release-set local-commit status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 2 {
		return usageErrorf("usage: aw release-set local-commit status --project-id <projectId> <releaseSetId> <localCommitId>")
	}
	releaseSetID, localCommitID := positional[0], positional[1]
	if strings.TrimSpace(*projectID) == "" {
		return usageErrorf("--project-id is required")
	}
	if strings.TrimSpace(releaseSetID) == "" || strings.TrimSpace(localCommitID) == "" {
		return usageErrorf("<releaseSetId> and <localCommitId> arguments are required")
	}

	status, err := workapp.GetReleaseSetLocalCommitStatus(ctx, deps.UoW, localCommitID)
	if err != nil {
		return err
	}
	if status.ProjectID != *projectID || status.ReleaseSetID != releaseSetID {
		return apperror.New(apperror.CodeNotFound, "release set local commit not found", false)
	}
	return cli.EncodeQueryResult(stdout, status)
}
