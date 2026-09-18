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
		Path: []string{"evidence", "list"}, Scope: cli.ScopeProject,
		AppOperation: "ListEvidenceForWorkItem", HTTPOperationID: "listEvidence",
	})
}

// evidenceListResult wraps ListEvidenceForWorkItem's own result in an
// object (rather than a bare top-level JSON array), mirroring
// internal/delivery/httpapi/evidence/dto.go's own listEvidenceResponse
// exactly — same reasoning: a future field (a page cursor, a total count)
// can be added beside "items" without a breaking wire-shape change. Items
// is runtimeapp.EvidenceDetail reused VERBATIM, never a second, hand-mapped
// copy: that type already has no Locator field by construction, and every
// field on it already carries the exact json tag this leaf wants.
type evidenceListResult struct {
	Items []runtimeapp.EvidenceDetail `json:"items"`
}

// RunEvidenceList implements `aw evidence list <workItemId> --project-id
// <id> [--run-id <id>] [--kind <kind>]` — a pure read dispatch over
// runtime.ListEvidenceForWorkItem, mirroring
// internal/delivery/httpapi/evidence's own handleListEvidence exactly
// (project-scoped, WorkItem reloaded and scope-checked inside that query
// itself). --run-id/--kind are optional client-side narrowing, wired
// straight through to runtime.EvidenceFilter — the same "unfiltered query,
// caller classifies" contract that query's own doc comment describes.
func RunEvidenceList(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("evidence list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	runID := fs.String("run-id", "", "narrow to Evidence rows sourced from this Run only (optional)")
	kind := fs.String("kind", "", "narrow to Evidence rows of this Kind only (optional)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw evidence list <workItemId> --project-id <projectId> [--run-id <id>] [--kind <kind>]")
	}
	workItemID := positional[0]
	if strings.TrimSpace(workItemID) == "" {
		return usageErrorf("<workItemId> argument is required")
	}
	if err := requireProjectID(*projectID); err != nil {
		return err
	}

	scope := ports.ProjectScope(*projectID)
	items, err := runtimeapp.ListEvidenceForWorkItem(ctx, deps.UnitOfWork, scope, workItemID, runtimeapp.EvidenceFilter{
		RunID: *runID, Kind: *kind,
	})
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, evidenceListResult{Items: items})
}
