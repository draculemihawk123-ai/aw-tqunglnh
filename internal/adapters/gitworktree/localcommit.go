package gitworktree

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// Provider also satisfies ports.LocalCommitCreator (V5-10A,
// ports/localcommit.go's own doc comment) — Go's structural typing needs no
// change to Provider's own ports.WorkspaceProvider assertion above.
var _ ports.LocalCommitCreator = (*Provider)(nil)

// CreateLocalCommit implements ports.LocalCommitCreator: stages every
// change currently present in req.Handle's own workspace (`git add -A`,
// matching Diff's own --untracked-files=all view of what counts as "the
// workspace's current change") and commits it with an explicit, audited
// author identity passed via one-off `-c user.name=`/`-c user.email=`
// overrides — never written to the workspace's own persistent Git config —
// rather than relying on ambient config a fresh worktree may not have.
// Never a remote operation: this method's own git invocations are exactly
// {add, status, commit, rev-parse}, none of which ever contacts a remote
// (AK-ARCH-015C: "Alpha từ chối push/PR/merge/force-push trước khi adapter
// remote được gọi").
func (p *Provider) CreateLocalCommit(ctx context.Context, req ports.CreateLocalCommitRequest) (workspace.Revision, error) {
	inspection, err := p.Inspect(ctx, req.Handle)
	if err != nil {
		return workspace.Revision{}, err
	}
	if inspection.State == ports.WorkspaceReleased {
		return workspace.Revision{}, ErrWorkspaceReleased
	}

	message := strings.TrimSpace(req.Message)
	authorName := strings.TrimSpace(req.AuthorName)
	authorEmail := strings.TrimSpace(req.AuthorEmail)
	if !validCommitField(message) || !validCommitField(authorName) || !validCommitField(authorEmail) {
		return workspace.Revision{}, fmt.Errorf(
			"%w: commit message and author name/email are required and must not contain control characters", ErrInvalidSpec)
	}

	workspacePath, err := p.workspacePath(req.Handle)
	if err != nil {
		return workspace.Revision{}, err
	}
	if _, err := p.runGit(ctx, workspacePath, "add", "-A"); err != nil {
		return workspace.Revision{}, err
	}
	status, err := p.runGit(ctx, workspacePath, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return workspace.Revision{}, err
	}
	if len(status) == 0 {
		return workspace.Revision{}, ErrNothingToCommit
	}
	if _, err := p.runGit(ctx, workspacePath,
		"-c", "user.name="+authorName, "-c", "user.email="+authorEmail,
		"commit", "-m", message,
	); err != nil {
		return workspace.Revision{}, err
	}

	head, err := p.resolveCommit(ctx, workspacePath, "HEAD")
	if err != nil {
		return workspace.Revision{}, err
	}
	return workspace.Revision{
		RepositoryID: inspection.RepositoryID, VCSObjectID: head, WorkspaceGeneration: inspection.Generation,
	}, nil
}

func validCommitField(value string) bool {
	if value == "" || !utf8.ValidString(value) {
		return false
	}
	return !strings.ContainsAny(value, "\x00\r\n")
}
