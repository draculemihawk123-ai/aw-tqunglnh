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
	// ErrUnsupportedEntry is returned by ReadSource (V6-10C) when the
	// requested path resolves to a tree entry ReadSource refuses to serve
	// as file content: a directory (mode 040000), a symlink (mode 120000 —
	// its own blob content is a target path string, never real file
	// content), or a submodule gitlink (mode 160000). Never returned for a
	// path that simply does not exist at the requested revision — that is
	// ErrPathNotFound.
	ErrUnsupportedEntry = errors.New("workspace inspection path is not a readable file")
	// ErrPathNotFound is returned by ReadSource when req.Path names no tree
	// entry at all at req.Revision.
	ErrPathNotFound = errors.New("workspace inspection path does not exist at revision")
)
