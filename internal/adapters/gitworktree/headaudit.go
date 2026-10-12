package gitworktree

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// Provider also satisfies ports.WorkspaceHeadAuditor (V11-01) — see
// ports/workspaceheadaudit.go.
var _ ports.WorkspaceHeadAuditor = (*Provider)(nil)

// headAuditLogByteLimit bounds the raw `git log` output AuditHead reads: at
// most DefaultAuditCommitLimit-sized pages of bounded subjects.
const headAuditLogByteLimit int64 = 256 << 10

// AuditHead implements ports.WorkspaceHeadAuditor. It reports how the
// worktree's live HEAD relates to req.PinnedRevision and names the commits in
// between. Read-only: it never moves HEAD, touches the index or writes a ref.
func (p *Provider) AuditHead(ctx context.Context, req ports.AuditHeadRequest) (ports.HeadAudit, error) {
	if err := ctx.Err(); err != nil {
		return ports.HeadAudit{}, err
	}
	if !validObjectID(req.PinnedRevision) {
		return ports.HeadAudit{}, fmt.Errorf("%w: pinned revision must be an exact commit object id", ErrInvalidSpec)
	}
	inspection, workspacePath, err := p.inspectForRead(ctx, req.Handle)
	if err != nil {
		return ports.HeadAudit{}, err
	}
	head := inspection.CurrentRevision.VCSObjectID
	if !validObjectID(head) {
		return ports.HeadAudit{}, fmt.Errorf("%w: worktree HEAD is not an exact commit object id", ErrInvalidSpec)
	}
	if _, err := p.runGit(ctx, workspacePath, "cat-file", "-e", req.PinnedRevision+"^{commit}"); err != nil {
		return ports.HeadAudit{}, wrapGitContextErr(ctx, fmt.Errorf("%w: pinned revision is not a commit in this worktree: %v", ErrInvalidSpec, err))
	}

	audit := ports.HeadAudit{Head: head, Pinned: req.PinnedRevision, Dirty: inspection.Dirty}

	_, exitCode, ancestryErr := p.runGitWithExitCode(ctx, workspacePath, "merge-base", "--is-ancestor", req.PinnedRevision, head)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ports.HeadAudit{}, ctxErr
	}
	switch exitCode {
	case 0:
		audit.FastForward = true
	case 1:
		audit.FastForward = false
	default:
		if ancestryErr != nil {
			return ports.HeadAudit{}, fmt.Errorf("%w: verify HEAD ancestry: %v", ErrGit, ancestryErr)
		}
		return ports.HeadAudit{}, fmt.Errorf("%w: verify HEAD ancestry: unexpected exit code %d", ErrGit, exitCode)
	}

	rangeSpec := req.PinnedRevision + ".." + head
	countOutput, err := p.runGit(ctx, workspacePath, "rev-list", "--count", rangeSpec, "--")
	if err != nil {
		return ports.HeadAudit{}, wrapGitContextErr(ctx, err)
	}
	total, parseErr := strconv.Atoi(strings.TrimSpace(string(countOutput)))
	if parseErr != nil {
		return ports.HeadAudit{}, fmt.Errorf("%w: parse commit count: %v", ErrGit, parseErr)
	}
	audit.TotalCommits = total
	if total == 0 {
		return audit, nil
	}

	limit := req.CommitLimit
	if limit <= 0 {
		limit = ports.DefaultAuditCommitLimit
	}
	format := "%H" + logFieldSep + "%P" + logFieldSep + "%an" + logFieldSep + "%ae" + logFieldSep + "%aI" + logFieldSep + "%s" + logRecordSep
	raw, _, err := p.runGitCapped(ctx, workspacePath, headAuditLogByteLimit,
		"log", "--reverse", fmt.Sprintf("--max-count=%d", limit), "--format="+format, rangeSpec, "--")
	if err != nil {
		return ports.HeadAudit{}, err
	}
	entries, _ := parseLogRecords(raw)
	for _, entry := range entries {
		audit.Commits = append(audit.Commits, ports.HeadAuditCommit{
			CommitID: entry.CommitID, AuthorName: printableOnly(entry.AuthorName), AuthorEmail: printableOnly(entry.AuthorEmail),
			AuthoredAt: entry.AuthoredAt, Subject: printableOnly(entry.Subject),
		})
	}
	return audit, nil
}

// printableOnly replaces control characters with '?', so a hostile commit
// subject or author name cannot forge extra lines or terminal escapes in the
// diagnostic an operator reads.
func printableOnly(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '?'
		}
		return r
	}, text)
}
