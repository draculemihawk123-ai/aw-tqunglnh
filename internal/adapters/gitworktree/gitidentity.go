package gitworktree

import (
	"context"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

var _ ports.GitIdentityReader = (*Provider)(nil)

// ReadGitIdentity implements ports.GitIdentityReader: `git config --show-scope --get user.name`
// and `user.email`, run inside the worktree so a repository-local setting wins over the global one.
// A missing setting is not an error (git exits 1): the part is reported empty and the caller decides.
// Nothing is written to any git configuration.
func (p *Provider) ReadGitIdentity(ctx context.Context, handle ports.WorkspaceHandle) (ports.GitIdentity, error) {
	workspacePath, err := p.activeWorkspacePath(ctx, handle)
	if err != nil {
		return ports.GitIdentity{}, err
	}
	var identity ports.GitIdentity
	identity.Name, identity.NameSource, err = p.readGitConfig(ctx, workspacePath, "user.name")
	if err != nil {
		return ports.GitIdentity{}, err
	}
	identity.Email, identity.EmailSource, err = p.readGitConfig(ctx, workspacePath, "user.email")
	if err != nil {
		return ports.GitIdentity{}, err
	}
	return identity, nil
}

// readGitConfig returns the effective value of key and the scope git reports for it, or ("", "")
// when it is not set. A value with a control character or a NUL is treated as not set: it could not
// be passed to `git -c user.name=...` as one safe argument.
func (p *Provider) readGitConfig(ctx context.Context, workspacePath, key string) (value, scope string, err error) {
	output, exitCode, runErr := p.runGitWithEnv(ctx, workspacePath, nil, "config", "--show-scope", "--get", key)
	if runErr != nil && exitCode < 0 {
		return "", "", runErr
	}
	if exitCode != 0 {
		return "", "", nil
	}
	line := strings.TrimRight(string(output), "\r\n")
	scope, value, found := strings.Cut(line, "\t")
	if !found {
		return "", "", nil
	}
	value = strings.TrimSpace(value)
	if !validCommitField(value) {
		return "", "", nil
	}
	return value, strings.TrimSpace(scope), nil
}
