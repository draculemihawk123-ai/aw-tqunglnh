package workspace

import (
	"context"
	"flag"
	"io"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	appinspection "github.com/taQuangLing/agent-workflow/internal/app/workspaceinspection"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

func init() {
	cli.MustRegister(cli.Descriptor{
		Path: []string{"repository-workspace", "log"}, Scope: cli.ScopeProject,
		AppOperation: "GetRepositoryLog", HTTPOperationID: "getWorkspaceRepositoryLog",
	})
}

// repositoryLogEntryResult mirrors
// internal/delivery/httpapi/workspaceinspection/dto.go's own
// repositoryLogEntryResponse.
type repositoryLogEntryResult struct {
	CommitID         string    `json:"commitId"`
	ParentIDs        []string  `json:"parentIds"`
	AuthorName       string    `json:"authorName"`
	AuthorEmail      string    `json:"authorEmail"`
	AuthoredAt       time.Time `json:"authoredAt"`
	Subject          string    `json:"subject"`
	SubjectTruncated bool      `json:"subjectTruncated"`
}

// repositoryLogResult mirrors
// internal/delivery/httpapi/workspaceinspection/dto.go's own
// repositoryLogPageResponse — NextCursor is omitted (not merely
// empty-stringed) when empty, so a caller can check JSON field presence to
// learn "this ancestry is fully exhausted" without a separate boolean,
// identically to the HTTP response.
type repositoryLogResult struct {
	Anchor     revisionResult             `json:"anchor"`
	Entries    []repositoryLogEntryResult `json:"entries"`
	NextCursor string                     `json:"nextCursor,omitempty"`
	Limit      int                        `json:"limit"`
	ByteLimit  int64                      `json:"byteLimit"`
	Truncated  bool                       `json:"truncated"`
}

func toRepositoryLogResult(p ports.RepositoryLogPage) repositoryLogResult {
	entries := make([]repositoryLogEntryResult, 0, len(p.Entries))
	for _, e := range p.Entries {
		parents := append([]string(nil), e.ParentIDs...)
		entries = append(entries, repositoryLogEntryResult{
			CommitID: e.CommitID, ParentIDs: parents, AuthorName: e.AuthorName, AuthorEmail: e.AuthorEmail,
			AuthoredAt: e.AuthoredAt, Subject: e.Subject, SubjectTruncated: e.SubjectTruncated,
		})
	}
	return repositoryLogResult{
		Anchor: toRevisionResult(p.Anchor), Entries: entries, NextCursor: p.NextCursor,
		Limit: p.Limit, ByteLimit: p.ByteLimit, Truncated: p.Truncated,
	}
}

// RunRepositoryWorkspaceLog implements `aw repository-workspace log
// <repositoryWorkspaceId> --project-id <id> --repository-id <id>
// --workspace-set-id <id> --anchor <rev> --anchor-generation <n>
// [--cursor <cursor>] [--limit <n>] [--byte-limit <n>]` — a read-only,
// paginated query over appinspection.Queries.GetRepositoryLog.
//
// --cursor is passed straight through to
// appinspection.GetRepositoryLogRequest.Cursor completely unmodified —
// never decoded, re-signed, or otherwise reasoned about by this leaf, the
// identical "already a real, independently-adapter-revalidated domain
// value (a commit object id checked via `git merge-base --is-ancestor` one
// layer below), not a generic offset/sort-key token" reasoning
// internal/delivery/httpapi/workspaceinspection/repositorylog.go's own
// handleGetRepositoryLog doc comment gives — this is why, unlike
// internal/delivery/cli/run/cursor.go's own opaque, signed pagination
// codec (built for run graph/timeline's own SYNTHETIC cursor, which has no
// independent domain meaning of its own), this leaf needs no local cursor
// codec at all.
//
// --limit is parsed via httpapi.ResolveLimit, reused rather than
// reimplemented: "" defaults to httpapi.DefaultPageLimit, a non-positive or
// non-numeric value is a usage error (a genuine caller mistake worth
// surfacing at the CLI layer), and a too-large value silently clamps to
// httpapi.MaxPageLimit before ever reaching
// appinspection.GetRepositoryLogRequest.Limit's own separate clamp-not-error
// bound (internal/app/workspaceinspection's own defaultLogLimit/
// maxLogLimit) — the two bounds compose harmlessly (this leaf's own is
// simply the stricter of the two), never conflict.
func RunRepositoryWorkspaceLog(ctx context.Context, deps Dependencies, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("repository-workspace log", flag.ContinueOnError)
	fs.SetOutput(stderr)
	projectID := cli.BindProjectFlag(fs)
	repositoryID := fs.String("repository-id", "", "repository id this repository workspace belongs to (required)")
	workspaceSetID := fs.String("workspace-set-id", "", "workspace set id this repository workspace belongs to (required)")
	anchorRevisionID := fs.String("anchor", "", "the commit object id to start/anchor the log's own ancestry at (required)")
	anchorGeneration := fs.Uint64("anchor-generation", 0, "the workspace generation --anchor belongs to (required)")
	cursor := fs.String("cursor", "", "resume immediately after this commit object id, a real ancestor of --anchor (omit to start at --anchor)")
	limit := fs.String("limit", "", "maximum commits to return in this page (omit for the server default)")
	byteLimit := fs.Int64("byte-limit", 0, "maximum raw log bytes read for this page (0 = server default)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	positional := fs.Args()
	if len(positional) != 1 {
		return usageErrorf("usage: aw repository-workspace log <repositoryWorkspaceId> --project-id <id> --repository-id <id> --workspace-set-id <id> --anchor <rev> --anchor-generation <n>")
	}
	repositoryWorkspaceID := positional[0]
	if err := requireScopeFlags(repositoryWorkspaceID, *projectID, *repositoryID, *workspaceSetID); err != nil {
		return err
	}
	if *anchorRevisionID == "" {
		return usageErrorf("--anchor is required")
	}
	resolvedLimit, err := httpapi.ResolveLimit(*limit)
	if err != nil {
		return err
	}

	queries := appinspection.New(deps.UOW, deps.Reader)
	result, err := queries.GetRepositoryLog(ctx, appinspection.GetRepositoryLogRequest{
		Scope: appinspection.WorkspaceScope{
			ProjectID: *projectID, RepositoryID: *repositoryID,
			WorkspaceSetID: *workspaceSetID, RepositoryWorkspaceID: repositoryWorkspaceID,
		},
		Anchor: workspace.Revision{
			RepositoryID: project.RepositoryID(*repositoryID), VCSObjectID: *anchorRevisionID, WorkspaceGeneration: *anchorGeneration,
		},
		Cursor: *cursor, Limit: resolvedLimit, ByteLimit: *byteLimit,
	})
	if err != nil {
		return mapInspectionQueryError(err)
	}
	return cli.EncodeQueryResult(stdout, toRepositoryLogResult(result))
}
