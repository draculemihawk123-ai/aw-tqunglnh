package gitworktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// V11-07: selective local commit and parking, against real worktrees.

func (f *inspectionFixture) changeSet(t *testing.T) []ports.WorkingTreeFileChange {
	t.Helper()
	entries, err := f.provider.collectChangeSet(context.Background(), f.worktreePath(t), "HEAD")
	if err != nil {
		t.Fatalf("collectChangeSet: %v", err)
	}
	return entries
}

func (f *inspectionFixture) changedPaths(t *testing.T) []string {
	t.Helper()
	var paths []string
	for _, entry := range f.changeSet(t) {
		paths = append(paths, entry.Path)
	}
	return paths
}

// selection builds a drift-checked selection of paths from the current change set,
// the way the approval flow does from what the approver saw.
func (f *inspectionFixture) selection(t *testing.T, paths ...string) *ports.LocalCommitSelection {
	t.Helper()
	byPath := map[string]ports.WorkingTreeFileChange{}
	for _, entry := range f.changeSet(t) {
		byPath[entry.Path] = entry
	}
	sel := &ports.LocalCommitSelection{}
	for _, path := range paths {
		entry, ok := byPath[path]
		if !ok {
			t.Fatalf("%q is not in the change set %v", path, f.changedPaths(t))
		}
		sel.Files = append(sel.Files, ports.LocalCommitFile{Path: path, Status: entry.Status, ContentHash: entry.ContentHash})
	}
	return sel
}

func (f *inspectionFixture) commitSelection(t *testing.T, sel *ports.LocalCommitSelection) error {
	t.Helper()
	_, err := f.provider.CreateLocalCommit(context.Background(), ports.CreateLocalCommitRequest{
		Handle: f.handle, Message: "selective commit", AuthorName: "Reviewer", AuthorEmail: "reviewer@example.test", Selection: sel,
	})
	return err
}

func (f *inspectionFixture) headFiles(t *testing.T) []string {
	t.Helper()
	var names []string
	for _, name := range splitNul([]byte(f.gitOutput(t, "show", "--name-only", "-z", "--format=", "HEAD"))) {
		names = append(names, strings.TrimSpace(name))
	}
	sort.Strings(names)
	return names
}

func sorted(values ...string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSelectiveCommit_CommitsOnlyTheSelectedFiles(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	root := f.worktreePath(t)
	f.commitWorkspaceChange(t, "doomed.txt", "bye\n")
	if err := os.Remove(filepath.Join(root, "doomed.txt")); err != nil {
		t.Fatal(err)
	}
	writeAt(t, root, "service.txt", "line-1\nline-2\n") // modified
	writeAt(t, root, "docs/a.md", "a\n")                // untracked, selected
	writeAt(t, root, "docs/b.md", "b\n")                // untracked, not selected

	if err := f.commitSelection(t, f.selection(t, "service.txt", "docs/a.md", "doomed.txt")); err != nil {
		t.Fatalf("selective commit: %v", err)
	}
	if got, want := f.headFiles(t), sorted("service.txt", "docs/a.md", "doomed.txt"); !equalStrings(got, want) {
		t.Fatalf("HEAD holds %v, want exactly %v", got, want)
	}
	if got, want := f.changedPaths(t), []string{"docs/b.md"}; !equalStrings(got, want) {
		t.Fatalf("worktree still changed: %v, want %v (KEEP leaves the rest)", got, want)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "b.md")); err != nil {
		t.Fatalf("the unselected file must stay in the worktree: %v", err)
	}
}

func TestSelectiveCommit_AwkwardNamesAreLiteral(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	root := f.worktreePath(t)
	names := []string{"has space.txt", "ünï-çødé.txt", "-leading-dash.txt", "bracket[1]{brace}.txt", "sub dir/in dir.txt"}
	decoys := []string{"bracket1b.txt", "leading-dash.txt"} // what a glob or an option parse would also take
	for _, name := range append(append([]string{}, names...), decoys...) {
		writeAt(t, root, name, "content of "+name+"\n")
	}
	if runtime.GOOS != "windows" { // '*' and '?' are not valid in Windows file names
		names = append(names, "glob*.txt", "what?.txt")
		writeAt(t, root, "glob*.txt", "x\n")
		writeAt(t, root, "what?.txt", "x\n")
		writeAt(t, root, "globber.txt", "decoy\n")
	}

	if err := f.commitSelection(t, f.selection(t, names...)); err != nil {
		t.Fatalf("selective commit: %v", err)
	}
	if got, want := f.headFiles(t), sorted(names...); !equalStrings(got, want) {
		t.Fatalf("HEAD holds %v, want exactly %v", got, want)
	}
	for _, decoy := range decoys {
		if !strings.Contains(strings.Join(f.changedPaths(t), "\n"), decoy) {
			t.Errorf("decoy %q must not have been committed by a glob", decoy)
		}
	}
}

func TestSelectiveCommit_RefusesPathsThatAreNeverCommitted(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	root := f.worktreePath(t)
	writeAt(t, root, "real.txt", "x\n")
	head := strings.TrimSpace(f.gitOutput(t, "rev-parse", "HEAD"))

	for _, bad := range []string{"../outside.txt", "/etc/passwd", "C:/Windows/win.ini", ".git/config", ".GIT/hooks/pre-commit", "not-changed.txt", "docs/../../x"} {
		err := f.commitSelection(t, &ports.LocalCommitSelection{Files: []ports.LocalCommitFile{{Path: "real.txt"}, {Path: bad}}})
		if !errors.Is(err, ports.ErrPathNotInChangeSet) {
			t.Errorf("path %q: err = %v, want ErrPathNotInChangeSet", bad, err)
		}
	}
	if err := f.commitSelection(t, &ports.LocalCommitSelection{}); !errors.Is(err, ErrInvalidSpec) {
		t.Errorf("empty selection: err = %v, want ErrInvalidSpec", err)
	}
	if after := strings.TrimSpace(f.gitOutput(t, "rev-parse", "HEAD")); after != head {
		t.Fatalf("a refused selection moved HEAD: %s -> %s", head, after)
	}
	if got := f.gitOutput(t, "diff", "--cached", "--name-only"); got != "" {
		t.Fatalf("a refused selection left staged files: %q", got)
	}
}

func TestSelectiveCommit_RefusesWhenAFileChangedAfterItWasReviewed(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	root := f.worktreePath(t)
	writeAt(t, root, "reviewed.txt", "what the approver saw\n")
	writeAt(t, root, "stays.txt", "other\n")
	sel := f.selection(t, "reviewed.txt")
	head := strings.TrimSpace(f.gitOutput(t, "rev-parse", "HEAD"))

	writeAt(t, root, "reviewed.txt", "swapped after the review\n")
	if err := f.commitSelection(t, sel); !errors.Is(err, ports.ErrChangeSetDrift) {
		t.Fatalf("content changed: err = %v, want ErrChangeSetDrift", err)
	}

	writeAt(t, root, "reviewed.txt", "what the approver saw\n")
	if err := os.Remove(filepath.Join(root, "reviewed.txt")); err != nil {
		t.Fatal(err)
	}
	if err := f.commitSelection(t, sel); !errors.Is(err, ports.ErrChangeSetDrift) {
		t.Fatalf("file gone: err = %v, want ErrChangeSetDrift", err)
	}
	if after := strings.TrimSpace(f.gitOutput(t, "rev-parse", "HEAD")); after != head {
		t.Fatalf("drift must not commit anything: HEAD %s -> %s", head, after)
	}
	if got := f.gitOutput(t, "diff", "--cached", "--name-only"); got != "" {
		t.Fatalf("drift must not stage anything: %q", got)
	}
}

func TestSelectiveCommit_AFileStagedByAnyoneElseDoesNotRideAlong(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	root := f.worktreePath(t)
	writeAt(t, root, "chosen.txt", "chosen\n")
	writeAt(t, root, "sneaky.txt", "staged by hand\n")
	f.gitOutput(t, "add", "sneaky.txt")

	if err := f.commitSelection(t, f.selection(t, "chosen.txt")); err != nil {
		t.Fatalf("selective commit: %v", err)
	}
	if got, want := f.headFiles(t), []string{"chosen.txt"}; !equalStrings(got, want) {
		t.Fatalf("HEAD holds %v, want only %v", got, want)
	}
	if got := f.changedPaths(t); !equalStrings(got, []string{"sneaky.txt"}) {
		t.Fatalf("sneaky.txt must remain a change: %v", got)
	}
}

func TestSelectiveCommit_BinaryFileCanBeSelected(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	root := f.worktreePath(t)
	if err := os.WriteFile(filepath.Join(root, "logo.bin"), []byte{0, 1, 2, 0, 255, 0}, 0o644); err != nil {
		t.Fatal(err)
	}
	writeAt(t, root, "notes.txt", "n\n")
	if err := f.commitSelection(t, f.selection(t, "logo.bin")); err != nil {
		t.Fatalf("selective commit: %v", err)
	}
	if got := f.headFiles(t); !equalStrings(got, []string{"logo.bin"}) {
		t.Fatalf("HEAD holds %v", got)
	}
}

func TestSelectiveCommit_CleanWorktreeHasNothingToCommit(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	err := f.commitSelection(t, &ports.LocalCommitSelection{Files: []ports.LocalCommitFile{{Path: "service.txt"}}})
	if !errors.Is(err, ports.ErrNothingToCommit) {
		t.Fatalf("err = %v, want ErrNothingToCommit", err)
	}
}

// A commit without a Selection is the old behavior: every change.
func TestSelectiveCommit_NilSelectionStillCommitsEverything(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	root := f.worktreePath(t)
	writeAt(t, root, "one.txt", "1\n")
	writeAt(t, root, "two.txt", "2\n")
	if err := f.commitSelection(t, nil); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if got, want := f.headFiles(t), sorted("one.txt", "two.txt"); !equalStrings(got, want) {
		t.Fatalf("HEAD holds %v, want %v", got, want)
	}
}

const testParkedRef = "refs/agentkit/parked/release-set-1"

func (f *inspectionFixture) leftoverFixture(t *testing.T) (root string) {
	t.Helper()
	root = f.worktreePath(t)
	f.commitWorkspaceChange(t, "doomed.txt", "to be deleted\n")
	if err := os.Remove(filepath.Join(root, "doomed.txt")); err != nil {
		t.Fatal(err)
	}
	writeAt(t, root, "service.txt", "line-1\nmodified by the agent\n")
	writeAt(t, root, "notes/untracked.md", "an untracked note\n")
	writeAt(t, root, "staged-new.txt", "staged but never committed\n")
	f.gitOutput(t, "add", "staged-new.txt")
	writeAt(t, root, "chosen.txt", "the file that gets committed\n")
	return root
}

func TestParkRemainingChanges_CleansTheWorktreeAndRestoreBringsEverythingBack(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	root := f.leftoverFixture(t)
	before := map[string]string{}
	for _, name := range []string{"service.txt", "notes/untracked.md", "staged-new.txt"} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		before[name] = string(data)
	}

	if err := f.commitSelection(t, f.selection(t, "chosen.txt")); err != nil {
		t.Fatalf("selective commit: %v", err)
	}
	parked, err := f.provider.ParkRemainingChanges(context.Background(), f.handle, testParkedRef)
	if err != nil {
		t.Fatalf("ParkRemainingChanges: %v", err)
	}
	if want := sorted("doomed.txt", "notes/untracked.md", "service.txt", "staged-new.txt"); !equalStrings(parked.Paths, want) {
		t.Fatalf("parked %v, want %v", parked.Paths, want)
	}
	if left := f.changedPaths(t); len(left) != 0 {
		t.Fatalf("the worktree must be clean after parking, still changed: %v", left)
	}
	if status := f.gitOutput(t, "status", "--porcelain=v1", "--untracked-files=all"); status != "" {
		t.Fatalf("git status after parking: %q", status)
	}
	if _, err := os.Stat(filepath.Join(root, "doomed.txt")); err != nil {
		t.Fatalf("a parked deletion must give the file back to the worktree: %v", err)
	}
	if got := strings.TrimSpace(f.gitOutput(t, "rev-parse", "--verify", testParkedRef+"^{commit}")); got == "" {
		t.Fatal("the parked ref does not exist")
	}

	// Parking again with nothing left is a no-op (crash recovery calls it twice).
	again, err := f.provider.ParkRemainingChanges(context.Background(), f.handle, testParkedRef)
	if err != nil || len(again.Paths) != 0 {
		t.Fatalf("second ParkRemainingChanges = %+v, %v; want nothing", again, err)
	}

	restored, err := f.provider.RestoreParkedChanges(context.Background(), f.handle, testParkedRef)
	if err != nil {
		t.Fatalf("RestoreParkedChanges: %v", err)
	}
	if !equalStrings(restored, parked.Paths) {
		t.Fatalf("restored %v, parked %v", restored, parked.Paths)
	}
	for name, want := range before {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil || string(data) != want {
			t.Errorf("%s after restore = %q, %v; want %q", name, data, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "doomed.txt")); !os.IsNotExist(err) {
		t.Errorf("the parked deletion must be a deletion again: %v", err)
	}
	if got := f.gitOutput(t, "diff", "--cached", "--name-only"); got != "" {
		t.Errorf("restore must not leave anything staged: %q", got)
	}
	if got, want := f.changedPaths(t), sorted("doomed.txt", "notes/untracked.md", "service.txt", "staged-new.txt"); !equalStrings(got, want) {
		t.Errorf("changed after restore = %v, want %v", got, want)
	}
	if out, err := execGit(f.worktreePath(t), "rev-parse", "--verify", "-q", testParkedRef); err == nil {
		t.Errorf("the parked ref should be gone after restore, got %s", out)
	}
}

func TestRestoreParkedChanges_RefusesToOverwriteNewerWork(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	root := f.leftoverFixture(t)
	if err := f.commitSelection(t, f.selection(t, "chosen.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := f.provider.ParkRemainingChanges(context.Background(), f.handle, testParkedRef); err != nil {
		t.Fatal(err)
	}

	// Someone edits a parked path while it is parked.
	writeAt(t, root, "service.txt", "line-1\nwritten while parked\n")
	_, err := f.provider.RestoreParkedChanges(context.Background(), f.handle, testParkedRef)
	if !errors.Is(err, ports.ErrParkedRestoreConflict) {
		t.Fatalf("err = %v, want ErrParkedRestoreConflict", err)
	}
	data, _ := os.ReadFile(filepath.Join(root, "service.txt"))
	if string(data) != "line-1\nwritten while parked\n" {
		t.Fatalf("a refused restore overwrote the newer content: %q", data)
	}
	if _, statErr := os.Stat(filepath.Join(root, "notes", "untracked.md")); !os.IsNotExist(statErr) {
		t.Fatal("a refused restore must write nothing at all")
	}
	if out, err := execGit(root, "rev-parse", "--verify", "-q", testParkedRef); err != nil {
		t.Fatalf("a refused restore must keep the parked ref: %s %v", out, err)
	}
}

func TestParkedRefsOnlyLiveUnderTheParkedNamespace(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	writeAt(t, f.worktreePath(t), "x.txt", "x\n")
	for _, ref := range []string{"refs/heads/main", "refs/tags/v1", "HEAD", "refs/agentkit/parked/", "refs/agentkit/parked/a b", "refs/agentkit/parked/../heads/main", "refs/agentkit/parked/x.lock", ""} {
		if _, err := f.provider.ParkRemainingChanges(context.Background(), f.handle, ref); !errors.Is(err, ErrInvalidSpec) {
			t.Errorf("Park %q: err = %v, want ErrInvalidSpec", ref, err)
		}
		if _, err := f.provider.RestoreParkedChanges(context.Background(), f.handle, ref); !errors.Is(err, ErrInvalidSpec) {
			t.Errorf("Restore %q: err = %v, want ErrInvalidSpec", ref, err)
		}
	}
	if got := f.changedPaths(t); !equalStrings(got, []string{"x.txt"}) {
		t.Fatalf("a refused ref must not touch the worktree: %v", got)
	}
}

// execGit runs git in dir and returns its stdout; the error is non-nil on a non-zero exit.
func execGit(dir string, args ...string) (string, error) {
	output, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return string(output), err
}
