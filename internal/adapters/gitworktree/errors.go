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
)
