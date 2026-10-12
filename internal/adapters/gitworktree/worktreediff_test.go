package gitworktree

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// V11-06: ReadWorkingTreeDiff against real worktrees. Every test that reads checks
// the call changed nothing (HEAD, `git status`, the index file).

type worktreeState struct{ head, status, indexSum string }

func (f *inspectionFixture) worktreePath(t *testing.T) string {
	t.Helper()
	path, err := f.provider.workspacePath(f.handle)
	if err != nil {
		t.Fatalf("resolve workspace path: %v", err)
	}
	return path
}

func (f *inspectionFixture) gitOutput(t *testing.T, args ...string) string {
	t.Helper()
	output, err := exec.Command("git", append([]string{"-C", f.worktreePath(t)}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(output)
}

func (f *inspectionFixture) snapshotState(t *testing.T) worktreeState {
	t.Helper()
	gitDir := strings.TrimSpace(f.gitOutput(t, "rev-parse", "--absolute-git-dir"))
	index, err := os.ReadFile(filepath.Join(gitDir, "index"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read index: %v", err)
	}
	sum := sha256.Sum256(index)
	return worktreeState{
		head:     strings.TrimSpace(f.gitOutput(t, "rev-parse", "HEAD")),
		status:   f.gitOutput(t, "status", "--porcelain=v1", "--untracked-files=all"),
		indexSum: hex.EncodeToString(sum[:]),
	}
}

func (f *inspectionFixture) workingTreeDiff(t *testing.T, byteLimit int64, fileLimit int) ports.WorkingTreeDiff {
	t.Helper()
	before := f.snapshotState(t)
	diff, err := f.provider.ReadWorkingTreeDiff(context.Background(), ports.ReadWorkingTreeDiffRequest{
		Handle: f.handle, BaseRevision: f.currentRev(), ByteLimit: byteLimit, FileLimit: fileLimit,
	})
	if err != nil {
		t.Fatalf("ReadWorkingTreeDiff: %v", err)
	}
	if after := f.snapshotState(t); after != before {
		t.Fatalf("ReadWorkingTreeDiff changed the worktree:\nbefore %+v\nafter  %+v", before, after)
	}
	return diff
}

func writeAt(t *testing.T, root, name, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", name, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func findChange(t *testing.T, diff ports.WorkingTreeDiff, path string) ports.WorkingTreeFileChange {
	t.Helper()
	for _, change := range diff.Files {
		if change.Path == path {
			return change
		}
	}
	t.Fatalf("no change for %q among %+v", path, diff.Files)
	return ports.WorkingTreeFileChange{}
}

func TestReadWorkingTreeDiff_CleanWorktreeHasNoChanges(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	diff := f.workingTreeDiff(t, 4096, 100)
	if diff.TotalFiles != 0 || len(diff.Files) != 0 || len(diff.Patch) != 0 || diff.FilesTruncated || diff.PatchTruncated {
		t.Fatalf("diff = %+v, want nothing", diff)
	}
}

func TestReadWorkingTreeDiff_ReportsModifiedAddedAndDeletedFiles(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	root := f.worktreePath(t)
	f.commitWorkspaceChange(t, "doomed.txt", "bye\n") // committed first, deleted below
	if err := os.Remove(filepath.Join(root, "doomed.txt")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	writeAt(t, root, "service.txt", "line-1\nline-2\n")     // modified (was "line-1\n")
	writeAt(t, root, "docs/new.md", "# new\nsecond line\n") // untracked

	diff := f.workingTreeDiff(t, 1<<16, 100)
	if diff.TotalFiles != 3 || len(diff.Files) != 3 {
		t.Fatalf("files = %+v, want 3", diff.Files)
	}
	if c := findChange(t, diff, "service.txt"); c.Status != ports.WorkingTreeFileModified || c.Untracked || c.Additions != 1 || c.Deletions != 0 || c.ContentHash == "" {
		t.Errorf("service.txt = %+v", c)
	}
	if c := findChange(t, diff, "docs/new.md"); c.Status != ports.WorkingTreeFileAdded || !c.Untracked || c.Additions != 2 || c.SizeBytes != int64(len("# new\nsecond line\n")) {
		t.Errorf("docs/new.md = %+v", c)
	}
	if c := findChange(t, diff, "doomed.txt"); c.Status != ports.WorkingTreeFileDeleted || c.ContentHash != "" {
		t.Errorf("doomed.txt = %+v", c)
	}
	patch := string(diff.Patch)
	for _, want := range []string{"+line-2", "diff --git a/docs/new.md b/docs/new.md", "+# new", "+second line", "-bye"} {
		if !strings.Contains(patch, want) {
			t.Errorf("patch does not contain %q:\n%s", want, patch)
		}
	}
}

func TestReadWorkingTreeDiff_BinaryFileIsANameAndASizeNotContent(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	root := f.worktreePath(t)
	binary := append([]byte("PNG\x00\x01\x02"), bytes.Repeat([]byte{0xff}, 4000)...)
	if err := os.WriteFile(filepath.Join(root, "logo.png"), binary, 0o644); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	diff := f.workingTreeDiff(t, 1<<16, 100)
	c := findChange(t, diff, "logo.png")
	if !c.Binary || c.SizeBytes != int64(len(binary)) || c.Additions != 0 {
		t.Fatalf("logo.png = %+v, want a binary file of %d bytes", c, len(binary))
	}
	if bytes.Contains(diff.Patch, []byte{0xff, 0xff, 0xff}) || !strings.Contains(string(diff.Patch), "Binary file logo.png") {
		t.Fatalf("patch must name the binary file and carry none of its bytes: %q", diff.Patch)
	}
}

// Names a shell, a glob or an option parser would mangle come through verbatim.
func TestReadWorkingTreeDiff_UnusualFileNames(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	root := f.worktreePath(t)
	names := []string{"with space.txt", "-leading-dash.txt", "bracket[1]{brace}.txt", "unicode-tệp-ảnh.txt", "dir with space/inner file.txt"}
	for _, name := range names {
		writeAt(t, root, name, "x\n")
	}
	diff := f.workingTreeDiff(t, 1<<16, 100)
	if diff.TotalFiles != len(names) {
		t.Fatalf("files = %+v, want %d", diff.Files, len(names))
	}
	for _, name := range names {
		findChange(t, diff, name)
	}
}

func TestReadWorkingTreeDiff_StagedChangesAreShownAndStayStaged(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	root := f.worktreePath(t)
	writeAt(t, root, "staged.txt", "staged\n")
	f.gitOutput(t, "add", "staged.txt")
	diff := f.workingTreeDiff(t, 1<<16, 100) // also proves HEAD, status and the index are unchanged
	if c := findChange(t, diff, "staged.txt"); c.Status != ports.WorkingTreeFileAdded || c.Untracked {
		t.Fatalf("staged.txt = %+v, want an added, tracked file", c)
	}
	if status := f.gitOutput(t, "status", "--porcelain=v1"); !strings.HasPrefix(status, "A  staged.txt") {
		t.Fatalf("staged.txt is no longer staged: %q", status)
	}
}

func TestReadWorkingTreeDiff_DigestChangesIffTheChangeSetDoes(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	root := f.worktreePath(t)
	writeAt(t, root, "a.txt", "aaaa\n")
	first := f.workingTreeDiff(t, 4096, 10).ChangeSetDigest
	if again := f.workingTreeDiff(t, 4096, 10).ChangeSetDigest; again != first {
		t.Fatalf("digest of an unchanged worktree moved: %s then %s", first, again)
	}
	writeAt(t, root, "a.txt", "bbbb\n") // same size, different bytes
	if changed := f.workingTreeDiff(t, 4096, 10).ChangeSetDigest; changed == first {
		t.Fatal("digest did not change when a file's content changed")
	}
	writeAt(t, root, "b.txt", "new\n")
	third := f.workingTreeDiff(t, 4096, 10).ChangeSetDigest
	if third == first {
		t.Fatal("digest did not change when a file was added")
	}
}

func TestReadWorkingTreeDiff_TruncationIsReportedAndTheTotalStillCountsEverything(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	root := f.worktreePath(t)
	for i := 0; i < 5; i++ {
		writeAt(t, root, "f"+string(rune('a'+i))+".txt", strings.Repeat("line\n", 100))
	}
	diff := f.workingTreeDiff(t, 300, 2)
	if diff.TotalFiles != 5 || len(diff.Files) != 2 || !diff.FilesTruncated {
		t.Fatalf("files = %d of %d truncated=%v, want 2 of 5 truncated", len(diff.Files), diff.TotalFiles, diff.FilesTruncated)
	}
	if !diff.PatchTruncated || int64(len(diff.Patch)) > 300 {
		t.Fatalf("patch = %d bytes truncated=%v, want it cut at the 300-byte limit", len(diff.Patch), diff.PatchTruncated)
	}
}

// A symlink is reported as a symlink, hashed by its link text, and nothing it
// points at (here a secret outside the worktree) is ever read.
func TestReadWorkingTreeDiff_NeverFollowsASymlinkOutOfTheWorktree(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	root := f.worktreePath(t)
	secretDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(secretDir, "secret.txt"), []byte("TOP-SECRET-CONTENT\n"), 0o644); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	if err := os.Symlink(filepath.Join(secretDir, "secret.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("cannot create a symlink on this machine: %v", err)
	}
	diff := f.workingTreeDiff(t, 1<<16, 100)
	c := findChange(t, diff, "link.txt")
	if !c.Symlink || c.Binary {
		t.Fatalf("link.txt = %+v, want a symlink", c)
	}
	if strings.Contains(string(diff.Patch), "TOP-SECRET-CONTENT") {
		t.Fatalf("the patch leaked the content a symlink points to: %s", diff.Patch)
	}
}

func TestReadWorkingTreeDiff_RefusesAnUnauthorizedBase(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	bogus := f.currentRev()
	bogus.VCSObjectID = strings.Repeat("a", 40)
	_, err := f.provider.ReadWorkingTreeDiff(context.Background(), ports.ReadWorkingTreeDiffRequest{
		Handle: f.handle, BaseRevision: bogus, ByteLimit: 4096, FileLimit: 10,
	})
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("err = %v, want ErrInvalidSpec", err)
	}
}

func TestReadWorkingTreeDiff_RefusesAnOversizeChangeSet(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	root := f.worktreePath(t)
	for i := 0; i < maxWorkingTreeEntries+1; i++ {
		if err := os.WriteFile(filepath.Join(root, "n"+itoa(i)), nil, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	_, err := f.provider.ReadWorkingTreeDiff(context.Background(), ports.ReadWorkingTreeDiffRequest{
		Handle: f.handle, BaseRevision: f.currentRev(), ByteLimit: 4096, FileLimit: 10,
	})
	if !errors.Is(err, ErrWorkingTreeTooLarge) {
		t.Fatalf("err = %v, want ErrWorkingTreeTooLarge", err)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var digits []byte
	for ; i > 0; i /= 10 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
	}
	return string(digits)
}
