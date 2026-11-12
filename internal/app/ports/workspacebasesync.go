package ports

import "context"

// Outcomes of WorkspaceBaseSyncer.SyncWithBase (V12-20, ADR-055).
const (
	// BaseSyncUpToDate: the base branch has nothing the worktree lacks (it is HEAD or an ancestor of it).
	BaseSyncUpToDate = "UP_TO_DATE"
	// BaseSyncFastForwarded: the worktree branch was moved forward to the base branch's tip.
	BaseSyncFastForwarded = "FAST_FORWARDED"
	// BaseSyncNeedsMerge: the worktree branch and the base branch diverged; a person merges (aw does not).
	BaseSyncNeedsMerge = "NEEDS_MERGE"
	// BaseSyncDirty: the worktree has uncommitted changes; nothing was moved.
	BaseSyncDirty = "DIRTY"
)

// WorkspaceBaseSyncer brings a family worktree up to date with its repository's base branch, locally only: it may fetch from the
// repository's `origin` remote, never pushes, and only ever fast-forwards (V12-20). The gitworktree provider implements it.
type WorkspaceBaseSyncer interface {
	// BaseStatus reads how HEAD relates to the base branch, without fetching and without changing anything.
	BaseStatus(ctx context.Context, req BaseSyncRequest) (BaseSyncResult, error)
	// SyncWithBase fetches (when req.Fetch and the repository has an origin), then fast-forwards the worktree to the base branch
	// when HEAD is an ancestor of it. A dirty worktree or a diverged branch is reported, not changed.
	SyncWithBase(ctx context.Context, req BaseSyncRequest) (BaseSyncResult, error)
}

// BaseSyncRequest names the worktree and the base branch (the repository's default ref, e.g. "main").
type BaseSyncRequest struct {
	Handle  WorkspaceHandle
	BaseRef string
	Fetch   bool
}

// BaseSyncResult is how the worktree relates to the base branch.
type BaseSyncResult struct {
	BaseRef string `json:"baseRef"`
	// BaseRevision is the base branch's tip the decision used (origin's when it is newer than the local branch).
	BaseRevision string `json:"baseRevision"`
	// Head is the worktree's HEAD after the call.
	Head    string `json:"head"`
	Outcome string `json:"outcome,omitempty"`
	// Merged: HEAD is contained in the base branch (the family's work was merged into it).
	Merged bool `json:"merged"`
	// Ahead counts the commits of HEAD not in the base branch; Behind the commits of the base branch not in HEAD.
	Ahead  int `json:"ahead"`
	Behind int `json:"behind"`
	// Fetched says a fetch from origin ran; FetchProblem why it failed (the decision then used the refs already there).
	Fetched      bool   `json:"fetched"`
	FetchProblem string `json:"fetchProblem,omitempty"`
}
