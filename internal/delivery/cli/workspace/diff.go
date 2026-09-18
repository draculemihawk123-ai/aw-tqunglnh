package workspace

import (
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
		Path: []string{"repository-workspace", "diff"}, Scope: cli.ScopeProject,
		AppOperation: "GetDiff", HTTPOperationID: "getWorkspaceDiff",
	})
}

// diffFileChangeResult mirrors
// internal/delivery/httpapi/workspaceinspection/dto.go's own
// diffFileChangeResponse — see revisionResult's own doc comment (helpers.go)
// for why this package duplicates rather than imports it.
type diffFileChangeResult struct {
	Path      string `json:"path"`
	Additions int64  `json:"additions"`
	Deletions int64  `json:"deletions"`
	Binary    bool   `json:"binary"`
}

// diffResult mirrors
// internal/delivery/httpapi/workspaceinspection/dto.go's own
// diffContentResponse — Patch stays []byte so encoding/json base64-encodes
// it automatically, the identical reasoning that file's own doc comment
// gives (a real patch can legitimately contain non-UTF8 byte sequences).
type diffResult struct {
	BaseRevision   revisionResult         `json:"baseRevision"`
	ResultRevision revisionResult         `json:"resultRevision"`
	Files          []diffFileChangeResult `json:"files"`
	Patch          []byte                 `json:"patch"`
	ByteLimit      int64                  `json:"byteLimit"`
	FileLimit      int                    `json:"fileLimit"`
	FilesTruncated bool                   `json:"filesTruncated"`
	PatchTruncated bool                   `json:"patchTruncated"`
}

func toDiffResult(d ports.DiffContent) diffResult {
	files := make([]diffFileChangeResult, 0, len(d.Files))
	for _, f := range d.Files {
		files = append(files, diffFileChangeResult{Path: f.Path, Additions: f.Additions, Deletions: f.Deletions, Binary: f.Binary})
	}
	return diffResult{
		BaseRevision: toRevisionResult(d.BaseRevision), ResultRevision: toRevisionResult(d.ResultRevision),
		Files: files, Patch: d.Patch, ByteLimit: d.ByteLimit, FileLimit: d.FileLimit,
		FilesTruncated: d.FilesTruncated, PatchTruncated: d.PatchTruncated,
	}
}

// RunRepositoryWorkspaceDiff implements `aw repository-workspace diff
// <repositoryWorkspaceId> --project-id <id> --repository-id <id>
// --workspace-set-id <id> --base-revision <rev>
// --base-revision-generation <n> --result-revision <rev>
// --result-revision-generation <n> [--byte-limit <n>] [--file-limit <n>]`
// — a read-only query over appinspection.Queries.GetDiff, JSON output via
// cli.EncodeQueryResult. Errors are passed through mapInspectionQueryError
// (helpers.go) — see that function's own doc comment for the deliberate
// opacity this leaf must never work around.
func RunRepositoryWorkspaceDiff(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("repository-workspace diff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	repositoryID := fs.String("repository-id", "", "repository id this repository workspace belongs to (required)")
	workspaceSetID := fs.String("workspace-set-id", "", "workspace set id this repository workspace belongs to (required)")
	baseRevisionID := fs.String("base-revision", "", "the base commit object id to diff from (required)")
	baseRevisionGeneration := fs.Uint64("base-revision-generation", 0, "the workspace generation --base-revision belongs to (required)")
	resultRevisionID := fs.String("result-revision", "", "the result commit object id to diff to (required)")
	resultRevisionGeneration := fs.Uint64("result-revision-generation", 0, "the workspace generation --result-revision belongs to (required)")
	byteLimit := fs.Int64("byte-limit", 0, "maximum patch bytes to return (0 = server default)")
	fileLimit := fs.Int("file-limit", 0, "maximum changed-file entries to return (0 = server default)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw repository-workspace diff <repositoryWorkspaceId> --project-id <id> --repository-id <id> --workspace-set-id <id> --base-revision <rev> --base-revision-generation <n> --result-revision <rev> --result-revision-generation <n>")
	}
	repositoryWorkspaceID := positional[0]
	if err := requireScopeFlags(repositoryWorkspaceID, *projectID, *repositoryID, *workspaceSetID); err != nil {
		return err
	}
	if *baseRevisionID == "" {
		return usageErrorf("--base-revision is required")
	}
	if *resultRevisionID == "" {
		return usageErrorf("--result-revision is required")
	}

	queries := appinspection.New(deps.UOW, deps.Reader)
	result, err := queries.GetDiff(ctx, appinspection.GetDiffRequest{
		Scope: appinspection.WorkspaceScope{
			ProjectID: *projectID, RepositoryID: *repositoryID,
			WorkspaceSetID: *workspaceSetID, RepositoryWorkspaceID: repositoryWorkspaceID,
		},
		BaseRevision: workspace.Revision{
			RepositoryID: project.RepositoryID(*repositoryID), VCSObjectID: *baseRevisionID, WorkspaceGeneration: *baseRevisionGeneration,
		},
		ResultRevision: workspace.Revision{
			RepositoryID: project.RepositoryID(*repositoryID), VCSObjectID: *resultRevisionID, WorkspaceGeneration: *resultRevisionGeneration,
		},
		ByteLimit: *byteLimit, FileLimit: *fileLimit,
	})
	if err != nil {
		return mapInspectionQueryError(err)
	}
	return cli.EncodeQueryResult(stdout, toDiffResult(result))
}
