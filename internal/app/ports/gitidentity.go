package ports

import "context"

// GitIdentity is the author a commit made by aw on behalf of an approver would carry: the
// git configuration of the machine that runs aw, as the repository's own worktree sees it
// (V11-07, ADR-040). A repository-local value overrides the global one, so each repository
// has its own identity. Name/Email are empty when git has no such setting; NameSource and
// EmailSource then stay empty too, otherwise they are the scope git reports (local, global,
// system, worktree, command).
type GitIdentity struct {
	Name        string
	Email       string
	NameSource  string
	EmailSource string
}

// Complete reports whether both parts of the identity are set.
func (i GitIdentity) Complete() bool { return i.Name != "" && i.Email != "" }

// GitIdentityReader reads the git identity configured for a workspace. It is read-only and
// never writes git configuration. It runs in the aw process, so that process needs HOME
// (USERPROFILE on Windows) in its environment to see the global configuration; without it the
// global values are simply reported missing rather than guessed.
type GitIdentityReader interface {
	ReadGitIdentity(ctx context.Context, handle WorkspaceHandle) (GitIdentity, error)
}
