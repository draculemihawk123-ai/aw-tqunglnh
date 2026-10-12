package workspaceinspection

import (
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// revisionResponse is the wire shape of one workspace.Revision — reused by
// every one of this package's three JSON-carrying DTOs below (getWorkspaceSource
// itself streams raw content rather than JSON; see source.go).
type revisionResponse struct {
	RepositoryID        string `json:"repositoryId"`
	VCSObjectID         string `json:"vcsObjectId"`
	WorkspaceGeneration uint64 `json:"workspaceGeneration"`
}

func toRevisionResponse(r workspace.Revision) revisionResponse {
	return revisionResponse{
		RepositoryID: string(r.RepositoryID), VCSObjectID: r.VCSObjectID, WorkspaceGeneration: r.WorkspaceGeneration,
	}
}

// diffFileChangeResponse is the wire shape of one ports.DiffFileChange.
//
// The last six fields are only set by a working-tree diff (V11-06,
// result=WORKING_TREE) and are omitted otherwise, so a committed diff's response
// is exactly what it always was.
type diffFileChangeResponse struct {
	Path        string `json:"path"`
	Additions   int64  `json:"additions"`
	Deletions   int64  `json:"deletions"`
	Binary      bool   `json:"binary"`
	Status      string `json:"status,omitempty"`
	Untracked   bool   `json:"untracked,omitempty"`
	Symlink     bool   `json:"symlink,omitempty"`
	SizeBytes   int64  `json:"sizeBytes,omitempty"`
	ContentHash string `json:"contentHash,omitempty"`
}

// diffContentResponse is getWorkspaceDiff's own response body — a direct,
// field-for-field wire mapping of ports.DiffContent (no field invented, none
// dropped): Patch is declared []byte on purpose so encoding/json's own
// standard behavior base64-encodes it automatically — ReadDiff's own real
// patch bytes can legitimately include non-UTF8 byte sequences (a binary
// file diff's own "--binary" literal index lines, or a path containing
// unusual bytes), so this never risks a lossy/rejected string encoding the
// way a plain `json:"patch"` string field would.
type diffContentResponse struct {
	BaseRevision   revisionResponse         `json:"baseRevision"`
	ResultRevision revisionResponse         `json:"resultRevision"`
	Files          []diffFileChangeResponse `json:"files"`
	Patch          []byte                   `json:"patch"`
	ByteLimit      int64                    `json:"byteLimit"`
	FileLimit      int                      `json:"fileLimit"`
	FilesTruncated bool                     `json:"filesTruncated"`
	PatchTruncated bool                     `json:"patchTruncated"`
	// Uncommitted, TotalFiles and ChangeSetDigest are only set by a working-tree
	// diff (V11-06, result=WORKING_TREE): the diff is the worktree's uncommitted
	// state on top of BaseRevision (ResultRevision is then empty, there is no
	// result commit), TotalFiles counts the whole change set even when Files is cut
	// at FileLimit, and ChangeSetDigest changes iff the set of changes does.
	Uncommitted     bool   `json:"uncommitted,omitempty"`
	TotalFiles      int    `json:"totalFiles,omitempty"`
	ChangeSetDigest string `json:"changeSetDigest,omitempty"`
}

func toDiffContentResponse(d ports.DiffContent) diffContentResponse {
	files := make([]diffFileChangeResponse, 0, len(d.Files))
	for _, f := range d.Files {
		files = append(files, diffFileChangeResponse{Path: f.Path, Additions: f.Additions, Deletions: f.Deletions, Binary: f.Binary})
	}
	return diffContentResponse{
		BaseRevision: toRevisionResponse(d.BaseRevision), ResultRevision: toRevisionResponse(d.ResultRevision),
		Files: files, Patch: d.Patch, ByteLimit: d.ByteLimit, FileLimit: d.FileLimit,
		FilesTruncated: d.FilesTruncated, PatchTruncated: d.PatchTruncated,
	}
}

// toWorkingTreeDiffResponse maps ports.WorkingTreeDiff onto the same response
// type a committed diff uses, marked Uncommitted.
func toWorkingTreeDiffResponse(d ports.WorkingTreeDiff) diffContentResponse {
	files := make([]diffFileChangeResponse, 0, len(d.Files))
	for _, f := range d.Files {
		files = append(files, diffFileChangeResponse{
			Path: f.Path, Additions: f.Additions, Deletions: f.Deletions, Binary: f.Binary,
			Status: string(f.Status), Untracked: f.Untracked, Symlink: f.Symlink, SizeBytes: f.SizeBytes, ContentHash: f.ContentHash,
		})
	}
	return diffContentResponse{
		BaseRevision: toRevisionResponse(d.BaseRevision), Files: files, Patch: d.Patch, ByteLimit: d.ByteLimit, FileLimit: d.FileLimit,
		FilesTruncated: d.FilesTruncated, PatchTruncated: d.PatchTruncated,
		Uncommitted: true, TotalFiles: d.TotalFiles, ChangeSetDigest: d.ChangeSetDigest,
	}
}

// repositoryLogEntryResponse is the wire shape of one ports.RepositoryLogEntry.
type repositoryLogEntryResponse struct {
	CommitID         string    `json:"commitId"`
	ParentIDs        []string  `json:"parentIds"`
	AuthorName       string    `json:"authorName"`
	AuthorEmail      string    `json:"authorEmail"`
	AuthoredAt       time.Time `json:"authoredAt"`
	Subject          string    `json:"subject"`
	SubjectTruncated bool      `json:"subjectTruncated"`
}

// repositoryLogPageResponse is getWorkspaceRepositoryLog's own response body
// — a direct field-for-field wire mapping of ports.RepositoryLogPage.
// NextCursor is omitted (not merely empty-stringed) when empty, so a client
// can check its own JSON field presence to learn "this ancestry is fully
// exhausted" without a separate boolean.
type repositoryLogPageResponse struct {
	Anchor     revisionResponse             `json:"anchor"`
	Entries    []repositoryLogEntryResponse `json:"entries"`
	NextCursor string                       `json:"nextCursor,omitempty"`
	Limit      int                          `json:"limit"`
	ByteLimit  int64                        `json:"byteLimit"`
	Truncated  bool                         `json:"truncated"`
}

func toRepositoryLogPageResponse(p ports.RepositoryLogPage) repositoryLogPageResponse {
	entries := make([]repositoryLogEntryResponse, 0, len(p.Entries))
	for _, e := range p.Entries {
		parents := append([]string(nil), e.ParentIDs...)
		entries = append(entries, repositoryLogEntryResponse{
			CommitID: e.CommitID, ParentIDs: parents, AuthorName: e.AuthorName, AuthorEmail: e.AuthorEmail,
			AuthoredAt: e.AuthoredAt, Subject: e.Subject, SubjectTruncated: e.SubjectTruncated,
		})
	}
	return repositoryLogPageResponse{
		Anchor: toRevisionResponse(p.Anchor), Entries: entries, NextCursor: p.NextCursor,
		Limit: p.Limit, ByteLimit: p.ByteLimit, Truncated: p.Truncated,
	}
}
