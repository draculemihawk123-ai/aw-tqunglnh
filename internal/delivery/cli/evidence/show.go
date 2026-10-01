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
		Path: []string{"evidence", "show"}, Scope: cli.ScopeProject,
		AppOperation: "GetEvidence", HTTPOperationID: "getEvidence",
	})
}

// RunEvidenceShow implements `aw evidence show <workItemId> <evidenceId>
// --project-id <id>` — one Evidence row's own bounded detail/verify
// metadata, a pure read dispatch over runtime.GetEvidence mirroring
// internal/delivery/httpapi/evidence's own handleGetEvidence exactly
// (project-scoped; the WorkItem is reloaded and scope-checked inside that
// query itself). docs/design/11-v6-00-ux-artifact.md's own Screen 11 row 2
// reserves exactly this shape and says it is a DIFFERENT leaf from `aw
// evidence verify`: this one returns the stored metadata online and never
// re-hashes any artifact bytes; `verify` is the offline, CLI_LOCAL check.
// The result is runtime.EvidenceDetail reused verbatim — it carries no
// Locator field by construction (see this package's own doc comment).
func RunEvidenceShow(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("evidence show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	positional := fs.Args()
	if len(positional) != 2 {
		return usageErrorf("usage: aw evidence show <workItemId> <evidenceId> --project-id <projectId>")
	}
	workItemID, evidenceID := positional[0], positional[1]
	if strings.TrimSpace(workItemID) == "" || strings.TrimSpace(evidenceID) == "" {
		return usageErrorf("<workItemId> and <evidenceId> arguments are required")
	}
	if err := requireProjectID(*projectID); err != nil {
		return err
	}

	detail, err := runtimeapp.GetEvidence(ctx, deps.UnitOfWork, ports.ProjectScope(*projectID), workItemID, evidenceID)
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, detail)
}
