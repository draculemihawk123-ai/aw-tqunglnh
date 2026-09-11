package gitworktree

import "errors"

var (
	ErrInvalidConfig     = errors.New("invalid git workspace provider configuration")
	ErrInvalidSpec       = errors.New("invalid workspace provision specification")
	ErrInvalidHandle     = errors.New("invalid workspace handle")
	ErrWorkspaceNotFound = errors.New("workspace not found")
	ErrWorkspaceReleased = errors.New("workspace has been released")
	ErrWorkspaceDirty    = errors.New("workspace has uncommitted changes")
	ErrUnsafePath        = errors.New("workspace path is unsafe")
	ErrProvisionConflict = errors.New("workspace provision conflicts with existing metadata")
	ErrGit               = errors.New("git command failed")
	// ErrNothingToCommit is returned by CreateLocalCommit (V5-10A) when a
	// workspace has no staged, unstaged, or untracked change at all —
	// distinct from ErrWorkspaceDirty above (Release's own "refuses to
	// discard a real change" case): here, an empty `git status` means
	// there is nothing for a commit to record.
	ErrNothingToCommit = errors.New("workspace has no change to commit")
)
