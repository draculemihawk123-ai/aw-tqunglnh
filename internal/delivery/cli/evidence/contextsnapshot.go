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
		Path: []string{"context-snapshot", "show"}, Scope: cli.ScopeProject,
		AppOperation: "GetContextSnapshot", HTTPOperationID: "getContextSnapshot",
	})
}

// RunContextSnapshotShow implements `aw context-snapshot show <workItemId>
// <snapshotId> --project-id <id>` — a pure read dispatch over
// runtime.GetContextSnapshot, mirroring
// internal/delivery/httpapi/evidence's own handleGetContextSnapshot
// exactly. The result is runtimeapp.ContextSnapshotDetail reused VERBATIM
// (never re-shaped), the same standalone, message-independent detail that
// query's own doc comment describes — MessageRefs/ResourceRefs/
// EvidenceRefs are themselves only ever bare IDs/content hashes, never
// inlined content.
func RunContextSnapshotShow(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("context-snapshot show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	positional := fs.Args()
	if len(positional) != 2 {
		return usageErrorf("usage: aw context-snapshot show <workItemId> <snapshotId> --project-id <projectId>")
	}
	workItemID, snapshotID := positional[0], positional[1]
	if strings.TrimSpace(workItemID) == "" || strings.TrimSpace(snapshotID) == "" {
		return usageErrorf("<workItemId> and <snapshotId> arguments are required")
	}
	if err := requireProjectID(*projectID); err != nil {
		return err
	}

	scope := ports.ProjectScope(*projectID)
	detail, err := runtimeapp.GetContextSnapshot(ctx, deps.UnitOfWork, scope, workItemID, snapshotID)
	if err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, detail)
}
