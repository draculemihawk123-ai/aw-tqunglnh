package evidence

import (
	"context"
	"flag"
	"io"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	runtimeapp "github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"artifact", "list"}, Scope: cli.ScopeProject,
		AppOperation: "ListArtifactsForEvidence", HTTPOperationID: "listArtifacts",
	})
}

// artifactListResult wraps ListArtifactsForEvidence's own result in an object
// (rather than a bare top-level JSON array), mirroring
// internal/delivery/httpapi/evidence/dto.go's own listArtifactsResponse
// exactly. Items is runtimeapp.ArtifactSummary reused verbatim: bounded
// metadata (sensitivity, redaction, content hash, size), never a locator or
// filesystem path.
type artifactListResult struct {
	Items []runtimeapp.ArtifactSummary `json:"items"`
}

// RunArtifactList implements `aw artifact list <workItemId> <evidenceId>
// --project-id <id>` — the artifacts one Evidence row references, a pure
// read dispatch over runtime.ListArtifactsForEvidence mirroring
// internal/delivery/httpapi/evidence's own handleListArtifacts exactly. It is
// the discovery step for `aw artifact get`: the artifact ids it prints are
// exactly the ids that command accepts.
func RunArtifactList(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("artifact list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	positional := fs.Args()
	if len(positional) != 2 {
		return usageErrorf("usage: aw artifact list <workItemId> <evidenceId> --project-id <projectId>")
	}
	workItemID, evidenceID := positional[0], positional[1]
	if strings.TrimSpace(workItemID) == "" || strings.TrimSpace(evidenceID) == "" {
		return usageErrorf("<workItemId> and <evidenceId> arguments are required")
	}
	if err := requireProjectID(*projectID); err != nil {
		return err
	}

	items, err := runtimeapp.ListArtifactsForEvidence(ctx, deps.UnitOfWork, ports.ProjectScope(*projectID), workItemID, evidenceID)
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, artifactListResult{Items: items})
}
