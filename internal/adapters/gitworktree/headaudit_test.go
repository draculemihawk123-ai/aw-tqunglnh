package gitworktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// externalCommit makes a commit in the worktree the way an IDE or a terminal
// would: plain `git commit`, not CreateLocalCommit.
func (f *inspectionFixture) externalCommit(t *testing.T, relativePath, content, message string) string {
	t.Helper()
	workspacePath, err := f.provider.workspacePath(f.handle)
	if err != nil {
		t.Fatalf("resolve workspace path: %v", err)
	}
	writeTestFile(t, filepath.Join(workspacePath, relativePath), content)
	for _, args := range [][]string{
		{"add", "-A"},
		{"-c", "user.name=Ide User", "-c", "user.email=ide@example.invalid", "commit", "-q", "-m", message},
	} {
		command := exec.Command("git", append([]string{"-C", workspacePath}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	output, err := exec.Command("git", "-C", workspacePath, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(output))
}

func TestAuditHeadCleanWorktreeAtPinnedRevisionHasNoCommits(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)

	audit, err := f.provider.AuditHead(context.Background(), ports.AuditHeadRequest{Handle: f.handle, PinnedRevision: f.baseRevision})
	if err != nil {
		t.Fatalf("AuditHead: %v", err)
	}
	if audit.Head != f.baseRevision || !audit.FastForward || audit.Dirty || audit.TotalCommits != 0 || len(audit.Commits) != 0 {
		t.Fatalf("audit = %+v, want HEAD==pinned, fast-forward, clean, no commits", audit)
	}
}

func TestAuditHeadNamesExternalCommitsOldestFirst(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	first := f.externalCommit(t, "a.txt", "a\n", "first by hand")
	second := f.externalCommit(t, "b.txt", "b\n", "second by hand")

	audit, err := f.provider.AuditHead(context.Background(), ports.AuditHeadRequest{Handle: f.handle, PinnedRevision: f.baseRevision})
	if err != nil {
		t.Fatalf("AuditHead: %v", err)
	}
	if audit.Head != second || audit.Pinned != f.baseRevision || !audit.FastForward {
		t.Fatalf("audit = %+v, want head %s fast-forward from the base", audit, second)
	}
	if audit.TotalCommits != 2 || len(audit.Commits) != 2 {
		t.Fatalf("commits = %d total / %d listed, want 2/2", audit.TotalCommits, len(audit.Commits))
	}
	if audit.Commits[0].CommitID != first || audit.Commits[1].CommitID != second {
		t.Fatalf("commit order = %s,%s want %s,%s (oldest first)", audit.Commits[0].CommitID, audit.Commits[1].CommitID, first, second)
	}
	if audit.Commits[0].Subject != "first by hand" || audit.Commits[0].AuthorName != "Ide User" || audit.Commits[0].AuthorEmail != "ide@example.invalid" {
		t.Fatalf("first commit = %+v", audit.Commits[0])
	}
}

func TestAuditHeadBoundsTheListButCountsEverything(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	f.externalCommit(t, "a.txt", "a\n", "one")
	f.externalCommit(t, "b.txt", "b\n", "two")
	third := f.externalCommit(t, "c.txt", "c\n", "three")

	audit, err := f.provider.AuditHead(context.Background(), ports.AuditHeadRequest{Handle: f.handle, PinnedRevision: f.baseRevision, CommitLimit: 2})
	if err != nil {
		t.Fatalf("AuditHead: %v", err)
	}
	if audit.TotalCommits != 3 || len(audit.Commits) != 2 {
		t.Fatalf("commits = %d total / %d listed, want 3/2", audit.TotalCommits, len(audit.Commits))
	}
	if audit.Commits[1].CommitID != third {
		t.Fatalf("the newest commits are kept; last listed = %s, want %s", audit.Commits[1].CommitID, third)
	}
}

func TestAuditHeadReportsDirtyWorktree(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	workspacePath, err := f.provider.workspacePath(f.handle)
	if err != nil {
		t.Fatalf("resolve workspace path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspacePath, "loose.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write loose file: %v", err)
	}

	audit, err := f.provider.AuditHead(context.Background(), ports.AuditHeadRequest{Handle: f.handle, PinnedRevision: f.baseRevision})
	if err != nil {
		t.Fatalf("AuditHead: %v", err)
	}
	if !audit.Dirty {
		t.Fatalf("audit = %+v, want Dirty", audit)
	}
}

// After a reset to an older commit the pinned revision is no longer an ancestor
// of HEAD: adopting HEAD would silently drop what aw recorded.
func TestAuditHeadReportsNonFastForwardAfterReset(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	pinned := f.externalCommit(t, "a.txt", "a\n", "recorded by aw")
	workspacePath, err := f.provider.workspacePath(f.handle)
	if err != nil {
		t.Fatalf("resolve workspace path: %v", err)
	}
	if output, err := exec.Command("git", "-C", workspacePath, "reset", "-q", "--hard", f.baseRevision).CombinedOutput(); err != nil {
		t.Fatalf("git reset: %v\n%s", err, output)
	}

	audit, err := f.provider.AuditHead(context.Background(), ports.AuditHeadRequest{Handle: f.handle, PinnedRevision: pinned})
	if err != nil {
		t.Fatalf("AuditHead: %v", err)
	}
	if audit.FastForward || audit.Head != f.baseRevision {
		t.Fatalf("audit = %+v, want HEAD at base and not fast-forward", audit)
	}
}

func TestAuditHeadRejectsNonCommitPins(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	for name, pinned := range map[string]string{
		"ref expression": "HEAD~1",
		"option":         "--all",
		"unknown commit": strings.Repeat("a", 40),
		"empty":          "",
	} {
		_, err := f.provider.AuditHead(context.Background(), ports.AuditHeadRequest{Handle: f.handle, PinnedRevision: pinned})
		if !errors.Is(err, ErrInvalidSpec) {
			t.Fatalf("%s: err = %v, want ErrInvalidSpec", name, err)
		}
	}
}

// AuditHead is a read: HEAD, the index and the working tree are exactly what
// they were before the call.
func TestAuditHeadChangesNothing(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	f.externalCommit(t, "a.txt", "a\n", "by hand")
	workspacePath, err := f.provider.workspacePath(f.handle)
	if err != nil {
		t.Fatalf("resolve workspace path: %v", err)
	}
	snapshot := func() string {
		head, _ := exec.Command("git", "-C", workspacePath, "rev-parse", "HEAD").Output()
		status, _ := exec.Command("git", "-C", workspacePath, "status", "--porcelain=v1", "--untracked-files=all").Output()
		return string(head) + "|" + string(status)
	}
	before := snapshot()
	if _, err := f.provider.AuditHead(context.Background(), ports.AuditHeadRequest{Handle: f.handle, PinnedRevision: f.baseRevision}); err != nil {
		t.Fatalf("AuditHead: %v", err)
	}
	if after := snapshot(); after != before {
		t.Fatalf("worktree changed by AuditHead:\nbefore %q\nafter  %q", before, after)
	}
}
