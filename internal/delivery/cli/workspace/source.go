package workspace

import (
	"bytes"
	"context"
	"flag"
	"io"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	appinspection "github.com/taQuangLing/agent-workflow/internal/app/workspaceinspection"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"repository-workspace", "source"}, Scope: cli.ScopeProject,
		AppOperation: "GetSource", HTTPOperationID: "getWorkspaceSource",
	})
}

// sourceResult is this leaf's own bounded metadata DTO — TotalBytes/
// LineCount/ByteLimit/LineLimit/Truncated/Binary/Revision straight off
// ports.SourceContent, minus its own Content field: the real bytes are
// ALWAYS routed to --output (stdout via "-" or a real file), never inlined
// into this JSON document, mirroring
// internal/delivery/cli/evidence/artifact.go's own ArtifactSummary
// "metadata only, content streamed separately" split exactly.
type sourceResult struct {
	Path       string         `json:"path"`
	Revision   revisionResult `json:"revision"`
	ByteLimit  int64          `json:"byteLimit"`
	LineLimit  int64          `json:"lineLimit"`
	TotalBytes int64          `json:"totalBytes"`
	LineCount  int64          `json:"lineCount"`
	Truncated  bool           `json:"truncated"`
	Binary     bool           `json:"binary"`
}

func toSourceResult(c ports.SourceContent) sourceResult {
	return sourceResult{
		Path: c.Path, Revision: toRevisionResult(c.Revision), ByteLimit: c.ByteLimit, LineLimit: c.LineLimit,
		TotalBytes: c.TotalBytes, LineCount: c.LineCount, Truncated: c.Truncated, Binary: c.Binary,
	}
}

// RunRepositoryWorkspaceSource implements `aw repository-workspace source
// <repositoryWorkspaceId> --project-id <id> --repository-id <id>
// --workspace-set-id <id> --revision <rev> --revision-generation <n>
// --path <path> --output <path|-> [--byte-limit <n>] [--line-limit <n>]` —
// this task's own "query with binary output" leaf: the real byte content
// streams to --output (raw bytes, never a JSON envelope), while bounded
// metadata (TotalBytes/LineCount/ByteLimit/LineLimit/Truncated/Binary) goes
// wherever --output did NOT: stderr (as one diagnostic line) when --output
// is "-" (content owns stdout), or stdout (as the one JSON document) when
// --output names a real file.
//
// Follows internal/delivery/cli/evidence/artifact.go's own
// RunArtifactGet template exactly for this output-routing split. Errors
// are passed through mapInspectionQueryError (helpers.go) — a path
// traversal attempt, an unauthorized/stale revision, or a directory/
// symlink/submodule rejection are all surfaced as the identical opaque
// message, never a more specific one (this task's own "Không làm: no
// arbitrary path" boundary).
func RunRepositoryWorkspaceSource(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("repository-workspace source", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	repositoryID := fs.String("repository-id", "", "repository id this repository workspace belongs to (required)")
	workspaceSetID := fs.String("workspace-set-id", "", "workspace set id this repository workspace belongs to (required)")
	revisionID := fs.String("revision", "", "the exact commit object id to read source at (required)")
	revisionGeneration := fs.Uint64("revision-generation", 0, "the workspace generation --revision belongs to (required)")
	treePath := fs.String("path", "", "the Git tree path (forward-slash separated, relative to the repository root) to read (required)")
	output := cli.BindOutputFlag(fs)
	byteLimit := fs.Int64("byte-limit", 0, "maximum content bytes to return (0 = server default)")
	lineLimit := fs.Int64("line-limit", 0, "maximum content lines to return (0 = server default)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw repository-workspace source <repositoryWorkspaceId> --project-id <id> --repository-id <id> --workspace-set-id <id> --revision <rev> --revision-generation <n> --path <path> --output <path|->")
	}
	repositoryWorkspaceID := positional[0]
	if err := requireScopeFlags(repositoryWorkspaceID, *projectID, *repositoryID, *workspaceSetID); err != nil {
		return err
	}
	if *revisionID == "" {
		return usageErrorf("--revision is required")
	}
	if *treePath == "" {
		return usageErrorf("--path is required")
	}
	if *output == "" {
		return usageErrorf("--output is required (a real file path, or - for stdout)")
	}

	queries := appinspection.New(deps.UOW, deps.Reader)
	result, err := queries.GetSource(ctx, appinspection.GetSourceRequest{
		Scope: appinspection.WorkspaceScope{
			ProjectID: *projectID, RepositoryID: *repositoryID,
			WorkspaceSetID: *workspaceSetID, RepositoryWorkspaceID: repositoryWorkspaceID,
		},
		Revision: workspace.Revision{
			RepositoryID: project.RepositoryID(*repositoryID), VCSObjectID: *revisionID, WorkspaceGeneration: *revisionGeneration,
		},
		Path: *treePath, ByteLimit: *byteLimit, LineLimit: *lineLimit,
	})
	if err != nil {
		return mapInspectionQueryError(err)
	}

	summary := toSourceResult(result)
	if *output == "-" {
		cli.Diagnosticf(stderr, "path=%s revision=%s generation=%d totalBytes=%d lineCount=%d truncated=%t binary=%t",
			summary.Path, summary.Revision.VCSObjectID, summary.Revision.WorkspaceGeneration,
			summary.TotalBytes, summary.LineCount, summary.Truncated, summary.Binary)
		if _, err := cli.WriteBinaryOutput(stdout, *output, bytes.NewReader(result.Content)); err != nil {
			return err
		}
		return nil
	}

	if _, err := cli.WriteBinaryOutput(stdout, *output, bytes.NewReader(result.Content)); err != nil {
		return err
	}
	return cli.EncodeQueryResult(stdout, summary)
}
