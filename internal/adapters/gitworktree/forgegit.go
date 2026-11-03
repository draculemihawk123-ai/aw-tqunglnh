package gitworktree

import (
	"context"
	"fmt"
	"strings"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

var _ ports.ForgeGit = (*Provider)(nil)

// validBranchName is a conservative branch name check: what `git check-ref-format --branch` accepts, minus anything that starts with
// "-" (it would read as an option).
func (p *Provider) validBranchName(ctx context.Context, directory, name string) error {
	if name == "" || strings.HasPrefix(name, "-") || !validRevisionInput(name) {
		return fmt.Errorf("%w: invalid branch name %q", ErrInvalidSpec, name)
	}
	if _, err := p.runGit(ctx, directory, "check-ref-format", "--branch", name); err != nil {
		return fmt.Errorf("%w: invalid branch name %q", ErrInvalidSpec, name)
	}
	return nil
}

// RemoteURL implements ports.ForgeGit.
func (p *Provider) RemoteURL(ctx context.Context, handle ports.WorkspaceHandle, remote string) (string, error) {
	_, workspacePath, err := p.inspectForRead(ctx, handle)
	if err != nil {
		return "", err
	}
	out, code, _ := p.runGitWithExitCode(ctx, workspacePath, "remote", "get-url", "--", remote)
	if code != 0 {
		return "", fmt.Errorf("%w: %s", ports.ErrNoRemote, remote)
	}
	return strings.TrimSpace(string(out)), nil
}

// RemoteBranchHead implements ports.ForgeGit (a network read: `git ls-remote`).
func (p *Provider) RemoteBranchHead(ctx context.Context, handle ports.WorkspaceHandle, remote, branch string) (string, bool, error) {
	_, workspacePath, err := p.inspectForRead(ctx, handle)
	if err != nil {
		return "", false, err
	}
	if err := p.validBranchName(ctx, workspacePath, branch); err != nil {
		return "", false, err
	}
	out, err := p.runGit(ctx, workspacePath, "ls-remote", "--heads", "--", remote, "refs/heads/"+branch)
	if err != nil {
		return "", false, wrapGitContextErr(ctx, err)
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 {
		return "", false, nil
	}
	return fields[0], true, nil
}

// PushHead implements ports.ForgeGit: `git push <remote> HEAD:refs/heads/<branch>`, never forced, so a remote branch that holds commits
// HEAD does not have is refused by git.
func (p *Provider) PushHead(ctx context.Context, handle ports.WorkspaceHandle, remote, branch string) (string, error) {
	inspection, workspacePath, err := p.inspectForRead(ctx, handle)
	if err != nil {
		return "", err
	}
	if err := p.validBranchName(ctx, workspacePath, branch); err != nil {
		return "", err
	}
	if _, err := p.runGit(ctx, workspacePath, "push", "--porcelain", "--", remote, "HEAD:refs/heads/"+branch); err != nil {
		return "", wrapGitContextErr(ctx, err)
	}
	return inspection.CurrentRevision.VCSObjectID, nil
}

// CommitsAhead implements ports.ForgeGit.
func (p *Provider) CommitsAhead(ctx context.Context, handle ports.WorkspaceHandle, base string) ([]string, error) {
	_, workspacePath, err := p.inspectForRead(ctx, handle)
	if err != nil {
		return nil, err
	}
	if !validRevisionInput(base) {
		return nil, fmt.Errorf("%w: invalid base %q", ErrInvalidSpec, base)
	}
	out, err := p.runGit(ctx, workspacePath, "rev-list", "--reverse", base+"..HEAD", "--")
	if err != nil {
		return nil, wrapGitContextErr(ctx, err)
	}
	return strings.Fields(string(out)), nil
}

// CurrentBranch implements ports.ForgeGit.
func (p *Provider) CurrentBranch(ctx context.Context, handle ports.WorkspaceHandle) (string, error) {
	_, workspacePath, err := p.inspectForRead(ctx, handle)
	if err != nil {
		return "", err
	}
	out, err := p.runGit(ctx, workspacePath, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		return "", fmt.Errorf("%w: the worktree is not on a branch", ErrInvalidSpec)
	}
	return strings.TrimSpace(string(out)), nil
}
