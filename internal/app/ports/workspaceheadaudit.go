package ports

import (
	"context"
	"time"
)

// WorkspaceHeadAuditor is the narrow, read-only adapter port behind V11-01
// (docs/design/14-v11-bugfix.md): "a commit made outside aw moved the
// worktree's HEAD away from the revision aw recorded". It answers one
// question about an already-provisioned worktree — how does the live HEAD
// relate to a revision aw pinned? — and is declared as its own port, narrower
// than WorkspaceProvider (workspace.go), for the identical reason
// WorkspaceInspectionReader (workspaceinspection.go) and LocalCommitCreator
// (localcommit.go) are: internal/adapters/gitworktree.Provider satisfies it
// by structural typing with no change to its ports.WorkspaceProvider
// assertion, and the many WorkspaceProvider test doubles are untouched.
// Callers that hold only a ports.WorkspaceProvider type-assert for it and fall
// back to a bare "HEAD differs" answer when the provider cannot audit.
//
// Nothing here mutates anything: the real adapter runs only
// {rev-parse, merge-base, rev-list, log, status}, all read-only, and never
// accepts a ref expression — PinnedRevision must be an exact commit object id.
type WorkspaceHeadAuditor interface {
	AuditHead(ctx context.Context, req AuditHeadRequest) (HeadAudit, error)
}

// AuditHeadRequest is what a caller supplies to WorkspaceHeadAuditor.AuditHead.
type AuditHeadRequest struct {
	Handle WorkspaceHandle
	// PinnedRevision is the exact commit object id aw recorded for the
	// worktree (a RepositoryWorkspace's CurrentRevision, or the revision a
	// Run pinned into an attempt's snapshot).
	PinnedRevision string
	// CommitLimit bounds how many commits of PinnedRevision..HEAD are named;
	// TotalCommits still counts all of them. A non-positive value means
	// DefaultAuditCommitLimit.
	CommitLimit int
}

// DefaultAuditCommitLimit is the commit list bound AuditHead applies when the
// caller names none.
const DefaultAuditCommitLimit = 20

// HeadAudit is AuditHead's result.
type HeadAudit struct {
	// Head is the worktree's live HEAD commit object id.
	Head string
	// Pinned echoes AuditHeadRequest.PinnedRevision.
	Pinned string
	// FastForward reports that Pinned is an ancestor of (or equal to) Head: the
	// only shape in which adopting Head loses no history. False after a reset
	// or a rebase that dropped Pinned from the branch.
	FastForward bool
	// Dirty reports uncommitted changes in the worktree (tracked or untracked).
	Dirty bool
	// TotalCommits counts the commits reachable from Head but not from Pinned.
	TotalCommits int
	// Commits names the newest of them, oldest first, at most CommitLimit.
	Commits []HeadAuditCommit
}

// HeadAuditCommit is one commit that sits between a pinned revision and HEAD.
// Subject is bounded by the adapter and carries control characters replaced.
type HeadAuditCommit struct {
	CommitID    string
	AuthorName  string
	AuthorEmail string
	AuthoredAt  time.Time
	Subject     string
}
