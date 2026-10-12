package ports

import (
	"context"
	"errors"

	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// ErrNothingToCommit is what a LocalCommitCreator returns (wrapped or bare;
// callers use errors.Is) when the workspace it was asked to commit has no
// staged, unstaged or untracked change at all (V9-09): a determinate,
// permanent answer about the workspace's current state, never a transient
// fault. internal/app/releasesetcommit.ExecuteReleaseSetLocalCommit maps it
// to a terminal FAILED/NO_CHANGES outcome instead of leaving the job to
// retry — retrying cannot make a clean worktree dirty. It lives here (not in
// the adapter) because the application layer must be able to name it without
// importing internal/adapters/gitworktree, which aliases it as its own
// ErrNothingToCommit.
var ErrNothingToCommit = errors.New("workspace has no change to commit")

// LocalCommitCreator creates a typed, audited local Git commit inside a
// workspace this WorkspaceProvider issued the handle for (V5-10A,
// docs/design/07-v5-execution-evidence.md; AK-ARCH-015C: "Alpha từ chối
// push/PR/merge/force-push trước khi adapter remote được gọi"). That
// rejection is structural, not a runtime check this interface performs:
// CreateLocalCommit is the only Git-mutating method this port — or any
// port in this codebase — declares, so no caller can reach a remote
// operation through it even in principle; there is no remote adapter for
// one to reach.
//
// Declared here as its own narrow port rather than a widening of
// WorkspaceProvider (workspace.go), mirroring WorkspaceDirectoryResolver's
// own reasoning (readiness.go): internal/adapters/gitworktree.Provider
// structurally satisfies this via Go's structural typing with zero change
// to its own ports.WorkspaceProvider assertion, and only this task's own
// application-layer callers need to depend on this one capability — every
// existing WorkspaceProvider caller is untouched.
type LocalCommitCreator interface {
	CreateLocalCommit(ctx context.Context, req CreateLocalCommitRequest) (workspace.Revision, error)
}

// CreateLocalCommitRequest is what a caller supplies to
// LocalCommitCreator.CreateLocalCommit. AuthorName/AuthorEmail make the
// commit's own audit trail explicit (a real `git commit` in a fresh
// worktree cannot rely on ambient user.name/user.email being configured)
// rather than implicit ambient Git config — "typed/audited" (this task's
// own Thực hiện line) means the caller states who is committing and why,
// not that the adapter infers it.
type CreateLocalCommitRequest struct {
	Handle      WorkspaceHandle
	Message     string
	AuthorName  string
	AuthorEmail string
	// Selection (V11-07), when set, commits only those files and nothing else; nil keeps
	// the behavior of `git add -A` over the whole worktree.
	Selection *LocalCommitSelection
}

// UnselectedHandling says what happens to the worktree's changes that a selective local
// commit (V11-07) did not select.
type UnselectedHandling string

const (
	// UnselectedKeep leaves them in the worktree, uncommitted (today's behavior and the
	// default). A strictly read-only node that runs next sees them as a SCOPE_VIOLATION.
	UnselectedKeep UnselectedHandling = "KEEP"
	// UnselectedPark moves them into a hidden ref (refs/agentkit/parked/...) and cleans
	// them from the worktree; RestoreParkedChanges puts them back.
	UnselectedPark UnselectedHandling = "PARK"
)

// IsValid reports whether h is one of the two closed values.
func (h UnselectedHandling) IsValid() bool { return h == UnselectedKeep || h == UnselectedPark }

// LocalCommitFile names one file of a selective commit. ContentHash (and Status) are what
// the approver saw: when set, the commit is refused with ErrChangeSetDrift if the worktree
// no longer has exactly that content for the path. Both empty means "no drift check" (a
// manual commit whose caller did not see hashes).
type LocalCommitFile struct {
	Path        string
	Status      WorkingTreeFileStatus
	ContentHash string
}

// LocalCommitSelection restricts a local commit to some of the worktree's changes
// (V11-07). Files must be a non-empty subset of the change set (what differs from HEAD).
type LocalCommitSelection struct {
	Files []LocalCommitFile
}

var (
	// ErrChangeSetDrift is returned by CreateLocalCommit when a selected file no longer
	// has the content (or status) the approver saw, or is no longer changed at all.
	// Determinate: nothing was staged or committed.
	ErrChangeSetDrift = errors.New("the worktree no longer matches the change set that was approved")
	// ErrPathNotInChangeSet is returned when a selected path is not changed in the worktree
	// (and no hash says the approver saw it), or names something that is never committed
	// (.git, a path outside the worktree).
	ErrPathNotInChangeSet = errors.New("selected path is not part of the worktree's change set")
	// ErrParkedRestoreConflict is returned by RestoreParkedChanges when restoring would
	// overwrite something the worktree changed since the files were parked.
	ErrParkedRestoreConflict = errors.New("restoring the parked changes would overwrite newer changes in the worktree")
	// ErrNothingParked is returned by RestoreParkedChanges when the ref does not exist (never
	// parked, or already restored).
	ErrNothingParked = errors.New("nothing is parked under that ref")
)

// ParkedChanges describes what ParkRemainingChanges moved out of the worktree.
type ParkedChanges struct {
	Ref   string
	Paths []string
}

// UnselectedChangeParker moves a worktree's remaining changes out of it, and back.
// Declared apart from LocalCommitCreator for the same reason that port is apart from
// WorkspaceProvider: only the release-set commit worker needs it.
type UnselectedChangeParker interface {
	// ParkRemainingChanges stores everything that still differs from HEAD (tracked
	// changes, deletions and untracked files) as a commit under ref, then cleans those
	// paths from the worktree. Idempotent: with nothing left it does nothing and returns
	// no paths, so the commit worker may call it again after a crash.
	ParkRemainingChanges(ctx context.Context, handle WorkspaceHandle, ref string) (ParkedChanges, error)
	// RestoreParkedChanges writes the parked files back into the worktree and deletes ref.
	// It refuses with ErrParkedRestoreConflict if a path was changed meanwhile.
	RestoreParkedChanges(ctx context.Context, handle WorkspaceHandle, ref string) ([]string, error)
}

// SelectedChangeParker is UnselectedChangeParker's sibling that parks only the named paths and leaves every other change in place
// (V11-10: a redo parks the files a previous run left OUTSIDE the WorkItem's scope but keeps the in-scope work the new run continues
// from). Restoring is the same RestoreParkedChanges of UnselectedChangeParker, which works for any ref this produces.
type SelectedChangeParker interface {
	// ParkChangedPaths stores the named paths' current changes as a commit under ref and cleans exactly those paths from the
	// worktree. A path that is not changed is ignored. Idempotent like ParkRemainingChanges: when none of the paths is changed any
	// more and ref exists, it reports what ref already holds.
	ParkChangedPaths(ctx context.Context, handle WorkspaceHandle, ref string, paths []string) (ParkedChanges, error)
}
