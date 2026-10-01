package gitworktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// treeFixture provisions one worktree whose base commit holds service.txt,
// keep.txt and remove.txt, and returns the provider, the worktree's own
// handle and its real path.
type treeFixture struct {
	provider *Provider
	handle   ports.WorkspaceHandle
	path     string
}

func newTreeFixture(t *testing.T) treeFixture {
	t.Helper()
	fixtureRoot := t.TempDir()
	repositoryPath, _ := createGitRepository(t, filepath.Join(fixtureRoot, "sources", "tree service"), "base\n")
	writeTestFile(t, filepath.Join(repositoryPath, "keep.txt"), "keep\n")
	writeTestFile(t, filepath.Join(repositoryPath, "remove.txt"), "remove\n")
	runTestGit(t, repositoryPath, "add", "--", "keep.txt", "remove.txt")
	runTestGit(t, repositoryPath, "commit", "-m", "more fixture files")

	provider := newTestProvider(t, filepath.Join(fixtureRoot, "workspaces"))
	handle, err := provider.Provision(context.Background(), ports.ProvisionSpec{
		RepositoryID:    project.RepositoryID("repo-tree"),
		LocalRepository: repositoryPath,
		BaseRef:         "HEAD",
		FamilyID:        work.TaskFamilyID("family-tree"),
		WorkspaceSetID:  workspace.WorkspaceSetID("set-tree"),
		Generation:      1,
	})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	workspacePath, err := provider.workspacePath(handle)
	if err != nil {
		t.Fatalf("resolve workspace path: %v", err)
	}
	return treeFixture{provider: provider, handle: handle, path: workspacePath}
}

func (f treeFixture) snapshot(t *testing.T) string {
	t.Helper()
	treeID, err := f.provider.SnapshotTree(context.Background(), f.handle)
	if err != nil {
		t.Fatalf("SnapshotTree: %v", err)
	}
	return treeID
}

func (f treeFixture) git(t *testing.T, arguments ...string) string {
	t.Helper()
	return runTestGit(t, f.path, arguments...)
}

func TestSnapshotTree_CleanWorkspaceEqualsHeadTree(t *testing.T) {
	t.Parallel()
	f := newTreeFixture(t)

	got := f.snapshot(t)
	want := strings.TrimSpace(f.git(t, "rev-parse", "HEAD^{tree}"))
	if got != want {
		t.Fatalf("SnapshotTree on a clean workspace = %s, want HEAD^{tree} %s (ADR-030: clean worktree keeps the pre-V9-01 behavior)", got, want)
	}
}

func TestSnapshotTree_IncludesTrackedChangesAndUntrackedExcludesIgnored(t *testing.T) {
	t.Parallel()
	f := newTreeFixture(t)

	writeTestFile(t, filepath.Join(f.path, "service.txt"), "modified by an earlier node\n")
	writeTestFile(t, filepath.Join(f.path, "fresh.txt"), "untracked\n")
	if err := os.MkdirAll(filepath.Join(f.path, "nested", "deeper"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(f.path, "nested", "deeper", "leaf.txt"), "leaf\n")
	writeTestFile(t, filepath.Join(f.path, ".gitignore"), "*.log\nbuild/\n")
	writeTestFile(t, filepath.Join(f.path, "debug.log"), "ignored\n")
	if err := os.MkdirAll(filepath.Join(f.path, "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(f.path, "build", "out.bin"), "ignored\n")
	if err := os.Remove(filepath.Join(f.path, "remove.txt")); err != nil {
		t.Fatal(err)
	}

	treeID := f.snapshot(t)
	listing := strings.Fields(f.git(t, "ls-tree", "-r", "--name-only", treeID))
	want := []string{".gitignore", "fresh.txt", "keep.txt", "nested/deeper/leaf.txt", "service.txt"}
	if !reflect.DeepEqual(listing, want) {
		t.Fatalf("snapshot tree entries = %v, want %v (tracked + untracked non-ignored, deleted file gone, ignored files excluded)", listing, want)
	}
	if content := f.git(t, "show", treeID+":service.txt"); content != "modified by an earlier node\n" {
		t.Fatalf("snapshot content of service.txt = %q, want the modified working-tree content", content)
	}
}

func TestSnapshotTree_LeavesIndexRefsAndStatusUntouched(t *testing.T) {
	// Not parallel: it redirects the temp directory to prove the private
	// index directory is cleaned up.
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	t.Setenv("TMP", temp)
	t.Setenv("TEMP", temp)
	f := newTreeFixture(t)

	writeTestFile(t, filepath.Join(f.path, "service.txt"), "dirty\n")
	writeTestFile(t, filepath.Join(f.path, "fresh.txt"), "untracked\n")
	// Leave one change staged so "the real index is untouched" is a
	// meaningful claim rather than a vacuous empty-index one.
	writeTestFile(t, filepath.Join(f.path, "keep.txt"), "staged\n")
	f.git(t, "add", "--", "keep.txt")

	// Observe everything first: `git status` may itself refresh (rewrite) the
	// real index, so the index bytes are read only after the last such
	// command and immediately before SnapshotTree.
	statusBefore := f.git(t, "status", "--porcelain=v1", "--untracked-files=all")
	cachedBefore := f.git(t, "diff", "--cached", "--name-only")
	refsBefore := f.git(t, "for-each-ref")
	headBefore := f.git(t, "rev-parse", "HEAD")
	branchesBefore := f.git(t, "branch", "--list", "--all")
	if cachedBefore != "keep.txt\n" {
		t.Fatalf("test setup: staged set = %q, want keep.txt", cachedBefore)
	}
	indexPath := strings.TrimSpace(f.git(t, "rev-parse", "--path-format=absolute", "--git-path", "index"))
	indexBefore, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read real index before: %v", err)
	}

	treeID := f.snapshot(t)
	_ = f.snapshot(t) // twice: the second call must not trip over the first

	indexAfter, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read real index after: %v", err)
	}
	if string(indexBefore) != string(indexAfter) {
		t.Fatal("SnapshotTree modified the worktree's real index file")
	}
	if got := f.git(t, "diff", "--cached", "--name-only"); got != cachedBefore {
		t.Fatalf("staged set after snapshot = %q, want unchanged %q", got, cachedBefore)
	}
	if got := f.git(t, "status", "--porcelain=v1", "--untracked-files=all"); got != statusBefore {
		t.Fatalf("git status changed across SnapshotTree:\nbefore: %q\nafter:  %q", statusBefore, got)
	}
	if got := f.git(t, "for-each-ref"); got != refsBefore {
		t.Fatalf("refs changed across SnapshotTree:\nbefore: %q\nafter:  %q", refsBefore, got)
	}
	if got := f.git(t, "branch", "--list", "--all"); got != branchesBefore {
		t.Fatalf("branches changed across SnapshotTree:\nbefore: %q\nafter:  %q", branchesBefore, got)
	}
	if got := f.git(t, "rev-parse", "HEAD"); got != headBefore {
		t.Fatalf("HEAD moved across SnapshotTree: %q -> %q", headBefore, got)
	}
	// The result is a tree, not a commit.
	if kind := strings.TrimSpace(f.git(t, "cat-file", "-t", treeID)); kind != "tree" {
		t.Fatalf("SnapshotTree returned a %s object, want a tree", kind)
	}
	entries, err := os.ReadDir(temp)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "agentkit-tree-index-") {
			t.Fatalf("temporary index directory %s was left behind", entry.Name())
		}
	}
}

func TestSnapshotTree_IgnoresInheritedGitIndexFile(t *testing.T) {
	// Not parallel: it sets a process-wide environment variable.
	f := newTreeFixture(t)
	writeTestFile(t, filepath.Join(f.path, "service.txt"), "dirty\n")
	want := strings.TrimSpace(f.git(t, "write-tree")) // sanity: real index == HEAD content here

	decoy := filepath.Join(t.TempDir(), "decoy-index")
	t.Setenv("GIT_INDEX_FILE", decoy)
	treeID, err := f.provider.SnapshotTree(context.Background(), f.handle)
	if unsetErr := os.Unsetenv("GIT_INDEX_FILE"); unsetErr != nil {
		t.Fatal(unsetErr)
	}
	if err != nil {
		t.Fatalf("SnapshotTree with an inherited GIT_INDEX_FILE: %v", err)
	}
	if treeID == want {
		t.Fatalf("snapshot %s equals the stale index tree — the dirty working tree was not captured", treeID)
	}
	if content := f.git(t, "show", treeID+":service.txt"); content != "dirty\n" {
		t.Fatalf("snapshot content = %q, want the working-tree content", content)
	}
	if _, statErr := os.Stat(decoy); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("the inherited GIT_INDEX_FILE %s was written to (stat err = %v)", decoy, statErr)
	}
}

func TestSnapshotTreeAndDiffTrees_ReportExactChangedPaths(t *testing.T) {
	t.Parallel()
	f := newTreeFixture(t)
	ctx := context.Background()

	input := f.snapshot(t)
	if again := f.snapshot(t); again != input {
		t.Fatalf("two snapshots of an unchanged workspace differ: %s vs %s", input, again)
	}
	if paths, err := f.provider.DiffTrees(ctx, f.handle, input, input); err != nil || len(paths) != 0 {
		t.Fatalf("DiffTrees(x, x) = %v, %v; want no paths and no error", paths, err)
	}

	writeTestFile(t, filepath.Join(f.path, "service.txt"), "changed\n")    // modify tracked
	writeTestFile(t, filepath.Join(f.path, "new file.txt"), "created\n")   // untracked, with a space in the name
	if err := os.Remove(filepath.Join(f.path, "remove.txt")); err != nil { // delete tracked
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(f.path, "keep.txt"), filepath.Join(f.path, "kept.txt")); err != nil { // rename
		t.Fatal(err)
	}

	output := f.snapshot(t)
	if output == input {
		t.Fatal("snapshot did not change after the workspace changed")
	}
	got, err := f.provider.DiffTrees(ctx, f.handle, input, output)
	if err != nil {
		t.Fatalf("DiffTrees: %v", err)
	}
	want := []string{"keep.txt", "kept.txt", "new file.txt", "remove.txt", "service.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DiffTrees = %v, want %v (both sides of a rename, deletions, untracked and modified paths)", got, want)
	}
	// The same diff in the other direction names the same paths.
	reverse, err := f.provider.DiffTrees(ctx, f.handle, output, input)
	if err != nil || !reflect.DeepEqual(reverse, want) {
		t.Fatalf("DiffTrees(reverse) = %v, %v; want %v", reverse, err, want)
	}
}

func TestDiffTrees_MissingTreeIsADistinguishableSentinel(t *testing.T) {
	t.Parallel()
	f := newTreeFixture(t)
	ctx := context.Background()
	existing := f.snapshot(t)

	missing := strings.Repeat("ab", 20) // well-formed, names no object
	for _, pair := range [][2]string{{missing, existing}, {existing, missing}, {missing, missing}} {
		_, err := f.provider.DiffTrees(ctx, f.handle, pair[0], pair[1])
		if !errors.Is(err, ports.ErrTreeNotFound) {
			t.Fatalf("DiffTrees(%s, %s) error = %v, want ports.ErrTreeNotFound", pair[0][:8], pair[1][:8], err)
		}
		if errors.Is(err, ErrGit) {
			t.Fatalf("DiffTrees(%s, %s) error %v must be distinguishable from a generic git failure", pair[0][:8], pair[1][:8], err)
		}
	}

	if _, err := f.provider.DiffTrees(ctx, f.handle, "not-an-object-id", existing); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("DiffTrees(malformed id) error = %v, want ErrInvalidSpec", err)
	}
}

func TestSnapshotTree_RejectsUnknownAndReleasedWorkspace(t *testing.T) {
	t.Parallel()
	f := newTreeFixture(t)
	ctx := context.Background()

	bogus, err := ports.NewWorkspaceHandle("not-a-handle")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.provider.SnapshotTree(ctx, bogus); !errors.Is(err, ErrInvalidHandle) {
		t.Fatalf("SnapshotTree(invalid handle) error = %v, want ErrInvalidHandle", err)
	}

	if err := f.provider.Release(ctx, f.handle); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := f.provider.SnapshotTree(ctx, f.handle); !errors.Is(err, ErrWorkspaceReleased) {
		t.Fatalf("SnapshotTree(released workspace) error = %v, want ErrWorkspaceReleased", err)
	}
}
