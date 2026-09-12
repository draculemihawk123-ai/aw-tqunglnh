package gitworktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// inspectionFixture provisions one real workspace backed by a real
// temporary Git repository, ready for ReadSource/ReadDiff/ReadRepositoryLog
// contract tests — no mocked git command anywhere in this file.
type inspectionFixture struct {
	provider     *Provider
	handle       ports.WorkspaceHandle
	sourceRepo   string
	baseRevision string
	inspection   ports.WorkspaceInspection
	repositoryID project.RepositoryID
	generation   uint64
}

func newInspectionFixture(t *testing.T) *inspectionFixture {
	t.Helper()
	fixtureRoot := t.TempDir()
	sourceRepo, baseRevision := createGitRepository(t, filepath.Join(fixtureRoot, "source"), "line-1\n")
	provider := newTestProvider(t, filepath.Join(fixtureRoot, "workspaces"))
	repositoryID := project.RepositoryID("repo-inspect")

	handle, err := provider.Provision(context.Background(), ports.ProvisionSpec{
		RepositoryID:    repositoryID,
		LocalRepository: sourceRepo,
		BaseRef:         baseRevision,
		FamilyID:        work.TaskFamilyID("family-inspect"),
		WorkspaceSetID:  workspace.WorkspaceSetID("set-inspect"),
		Generation:      1,
	})
	if err != nil {
		t.Fatalf("provision fixture workspace: %v", err)
	}
	inspection, err := provider.Inspect(context.Background(), handle)
	if err != nil {
		t.Fatalf("inspect fixture workspace: %v", err)
	}
	return &inspectionFixture{
		provider: provider, handle: handle, sourceRepo: sourceRepo, baseRevision: baseRevision,
		inspection: inspection, repositoryID: repositoryID, generation: inspection.Generation,
	}
}

// commitWorkspaceChange writes content to a file directly inside the
// workspace's own real working directory and commits it via the real
// CreateLocalCommit path — never a raw filesystem write to the source
// repository — refreshing f.inspection to the workspace's new live state
// afterward.
func (f *inspectionFixture) commitWorkspaceChange(t *testing.T, relativePath string, content string) workspace.Revision {
	t.Helper()
	workspacePath, err := f.provider.workspacePath(f.handle)
	if err != nil {
		t.Fatalf("resolve workspace path: %v", err)
	}
	targetPath := filepath.Join(workspacePath, relativePath)
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		t.Fatalf("create parent directory for %s: %v", relativePath, err)
	}
	writeTestFile(t, targetPath, content)
	revision, err := f.provider.CreateLocalCommit(context.Background(), ports.CreateLocalCommitRequest{
		Handle: f.handle, Message: "update " + relativePath,
		AuthorName: "Agent Kit Test", AuthorEmail: "agent-kit@example.invalid",
	})
	if err != nil {
		t.Fatalf("create local commit for %s: %v", relativePath, err)
	}
	inspection, err := f.provider.Inspect(context.Background(), f.handle)
	if err != nil {
		t.Fatalf("re-inspect workspace after commit: %v", err)
	}
	f.inspection = inspection
	return revision
}

func (f *inspectionFixture) baseRev() workspace.Revision {
	return f.inspection.BaseRevision
}

func (f *inspectionFixture) currentRev() workspace.Revision {
	return f.inspection.CurrentRevision
}

// --- ReadSource ---

func TestReadSourceReturnsExactBlobContentAtAuthorizedRevision(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)

	content, err := f.provider.ReadSource(context.Background(), ports.ReadSourceRequest{
		Handle: f.handle, Revision: f.baseRev(), Path: "service.txt", ByteLimit: 4096, LineLimit: 100,
	})
	if err != nil {
		t.Fatalf("ReadSource: %v", err)
	}
	if string(content.Content) != "line-1\n" {
		t.Fatalf("content = %q, want %q", content.Content, "line-1\n")
	}
	if content.Binary || content.Truncated {
		t.Fatalf("unexpected binary/truncated flags: %+v", content)
	}
	if content.TotalBytes != int64(len("line-1\n")) {
		t.Fatalf("TotalBytes = %d, want %d", content.TotalBytes, len("line-1\n"))
	}
	if content.LineCount != 1 {
		t.Fatalf("LineCount = %d, want 1", content.LineCount)
	}

	// The workspace's own CurrentRevision is equally authorized for a fresh,
	// unmodified workspace (Base == Current).
	if _, err := f.provider.ReadSource(context.Background(), ports.ReadSourceRequest{
		Handle: f.handle, Revision: f.currentRev(), Path: "service.txt", ByteLimit: 4096,
	}); err != nil {
		t.Fatalf("ReadSource at CurrentRevision: %v", err)
	}
}

func TestReadSourceEnforcesByteLimitWithTypedTruncation(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	longContent := strings.Repeat("abcdefghij\n", 50) // 550 bytes
	revision := f.commitWorkspaceChange(t, "service.txt", longContent)

	result, err := f.provider.ReadSource(context.Background(), ports.ReadSourceRequest{
		Handle: f.handle, Revision: revision, Path: "service.txt", ByteLimit: 100,
	})
	if err != nil {
		t.Fatalf("ReadSource: %v", err)
	}
	if !result.Truncated {
		t.Fatal("expected Truncated=true for content exceeding ByteLimit")
	}
	if int64(len(result.Content)) != 100 {
		t.Fatalf("len(Content) = %d, want exactly ByteLimit 100", len(result.Content))
	}
	if result.TotalBytes != int64(len(longContent)) {
		t.Fatalf("TotalBytes = %d, want real size %d even though truncated", result.TotalBytes, len(longContent))
	}
}

func TestReadSourceEnforcesLineLimitWithTypedTruncation(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	tenLines := strings.Repeat("row\n", 10)
	revision := f.commitWorkspaceChange(t, "service.txt", tenLines)

	result, err := f.provider.ReadSource(context.Background(), ports.ReadSourceRequest{
		Handle: f.handle, Revision: revision, Path: "service.txt", ByteLimit: 4096, LineLimit: 3,
	})
	if err != nil {
		t.Fatalf("ReadSource: %v", err)
	}
	if !result.Truncated || result.LineCount != 3 {
		t.Fatalf("expected line-truncation at 3 lines, got Truncated=%v LineCount=%d", result.Truncated, result.LineCount)
	}
	if string(result.Content) != "row\nrow\nrow\n" {
		t.Fatalf("Content = %q, want exactly first 3 lines", result.Content)
	}
}

func TestReadSourceDetectsBinaryContent(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	binaryContent := string([]byte{'B', 'I', 'N', 0x00, 'A', 'R', 'Y', 0x01, 0x02})
	revision := f.commitWorkspaceChange(t, "asset.bin", binaryContent)

	result, err := f.provider.ReadSource(context.Background(), ports.ReadSourceRequest{
		Handle: f.handle, Revision: revision, Path: "asset.bin", ByteLimit: 4096,
	})
	if err != nil {
		t.Fatalf("ReadSource: %v", err)
	}
	if !result.Binary {
		t.Fatal("expected Binary=true for content containing a NUL byte")
	}
}

func TestReadSourceRejectsPathTraversal(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	for _, badPath := range []string{
		"../secret.txt", "..\\secret.txt", "/etc/passwd", "a/../../b",
		"a/./b", "", "-rf", "a/",
	} {
		badPath := badPath
		t.Run(fmt.Sprintf("path=%q", badPath), func(t *testing.T) {
			t.Parallel()
			_, err := f.provider.ReadSource(context.Background(), ports.ReadSourceRequest{
				Handle: f.handle, Revision: f.baseRev(), Path: badPath, ByteLimit: 4096,
			})
			if !errors.Is(err, ErrInvalidSpec) {
				t.Fatalf("ReadSource(path=%q) error = %v, want ErrInvalidSpec", badPath, err)
			}
		})
	}
}

func TestReadSourceRejectsSymlinkEntry(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)

	targetFile := filepath.Join(t.TempDir(), "target.txt")
	writeTestFile(t, targetFile, "wherever this points is irrelevant")
	blobHash := strings.TrimSpace(runTestGit(t, f.sourceRepo, "hash-object", "-w", targetFile))
	runTestGit(t, f.sourceRepo, "update-index", "--add", "--cacheinfo", "120000,"+blobHash+",link.txt")
	runTestGit(t, f.sourceRepo, "commit", "-m", "add tracked symlink entry")
	symlinkRevision := strings.TrimSpace(runTestGit(t, f.sourceRepo, "rev-parse", "HEAD"))

	// Re-provision a fresh workspace pinned at the commit that actually
	// contains the symlink entry, so it is itself an authorized revision.
	fixtureRoot := t.TempDir()
	provider := newTestProvider(t, fixtureRoot)
	handle, err := provider.Provision(context.Background(), ports.ProvisionSpec{
		RepositoryID: project.RepositoryID("repo-symlink"), LocalRepository: f.sourceRepo,
		BaseRef: symlinkRevision, FamilyID: work.TaskFamilyID("family-symlink"),
		WorkspaceSetID: workspace.WorkspaceSetID("set-symlink"), Generation: 1,
	})
	if err != nil {
		t.Fatalf("provision symlink workspace: %v", err)
	}
	inspection, err := provider.Inspect(context.Background(), handle)
	if err != nil {
		t.Fatalf("inspect symlink workspace: %v", err)
	}

	_, err = provider.ReadSource(context.Background(), ports.ReadSourceRequest{
		Handle: handle, Revision: inspection.BaseRevision, Path: "link.txt", ByteLimit: 4096,
	})
	if !errors.Is(err, ErrUnsupportedEntry) {
		t.Fatalf("ReadSource(symlink) error = %v, want ErrUnsupportedEntry", err)
	}
}

func TestReadSourceRejectsDirectoryEntry(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	revision := f.commitWorkspaceChange(t, "dir/nested.txt", "nested\n")

	_, err := f.provider.ReadSource(context.Background(), ports.ReadSourceRequest{
		Handle: f.handle, Revision: revision, Path: "dir", ByteLimit: 4096,
	})
	if !errors.Is(err, ErrUnsupportedEntry) {
		t.Fatalf("ReadSource(directory) error = %v, want ErrUnsupportedEntry", err)
	}
}

func TestReadSourcePathNotFound(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	_, err := f.provider.ReadSource(context.Background(), ports.ReadSourceRequest{
		Handle: f.handle, Revision: f.baseRev(), Path: "does-not-exist.txt", ByteLimit: 4096,
	})
	if !errors.Is(err, ErrPathNotFound) {
		t.Fatalf("ReadSource(missing path) error = %v, want ErrPathNotFound", err)
	}
}

func TestReadSourceRejectsArbitraryRefExpressions(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	for _, badRef := range []string{"HEAD", "main", "HEAD~1", "", "--upload-pack=x", f.baseRevision[:10]} {
		badRef := badRef
		t.Run(fmt.Sprintf("ref=%q", badRef), func(t *testing.T) {
			t.Parallel()
			revision := workspace.Revision{RepositoryID: f.repositoryID, VCSObjectID: badRef, WorkspaceGeneration: f.generation}
			_, err := f.provider.ReadSource(context.Background(), ports.ReadSourceRequest{
				Handle: f.handle, Revision: revision, Path: "service.txt", ByteLimit: 4096,
			})
			if !errors.Is(err, ErrInvalidSpec) {
				t.Fatalf("ReadSource(ref=%q) error = %v, want ErrInvalidSpec", badRef, err)
			}
		})
	}
}

func TestReadSourceRejectsRealButUnauthorizedRevision(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)

	// A real commit that exists in the shared object database (reachable
	// from the workspace's own worktree, since worktrees share one ODB
	// with their source repository) but was never pinned as this
	// workspace's own Base or Current revision.
	writeTestFile(t, filepath.Join(f.sourceRepo, "service.txt"), "unauthorized change\n")
	runTestGit(t, f.sourceRepo, "add", "--", "service.txt")
	runTestGit(t, f.sourceRepo, "commit", "-m", "unauthorized change made directly in the source repository")
	unauthorized := strings.TrimSpace(runTestGit(t, f.sourceRepo, "rev-parse", "HEAD"))

	revision := workspace.Revision{RepositoryID: f.repositoryID, VCSObjectID: unauthorized, WorkspaceGeneration: f.generation}
	_, err := f.provider.ReadSource(context.Background(), ports.ReadSourceRequest{
		Handle: f.handle, Revision: revision, Path: "service.txt", ByteLimit: 4096,
	})
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("ReadSource(real-but-unauthorized) error = %v, want ErrInvalidSpec", err)
	}
}

func TestReadSourceRejectsRevisionForWrongRepositoryOrGeneration(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)

	wrongRepository := f.baseRev()
	wrongRepository.RepositoryID = project.RepositoryID("someone-elses-repo")
	if _, err := f.provider.ReadSource(context.Background(), ports.ReadSourceRequest{
		Handle: f.handle, Revision: wrongRepository, Path: "service.txt", ByteLimit: 4096,
	}); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("ReadSource(wrong repository) error = %v, want ErrInvalidSpec", err)
	}

	wrongGeneration := f.baseRev()
	wrongGeneration.WorkspaceGeneration = f.generation + 1
	if _, err := f.provider.ReadSource(context.Background(), ports.ReadSourceRequest{
		Handle: f.handle, Revision: wrongGeneration, Path: "service.txt", ByteLimit: 4096,
	}); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("ReadSource(wrong generation) error = %v, want ErrInvalidSpec", err)
	}
}

func TestReadSourceRejectsReleasedWorkspace(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	if err := f.provider.Release(context.Background(), f.handle); err != nil {
		t.Fatalf("release workspace: %v", err)
	}
	_, err := f.provider.ReadSource(context.Background(), ports.ReadSourceRequest{
		Handle: f.handle, Revision: f.baseRev(), Path: "service.txt", ByteLimit: 4096,
	})
	if !errors.Is(err, ErrWorkspaceReleased) {
		t.Fatalf("ReadSource(released) error = %v, want ErrWorkspaceReleased", err)
	}
}

func TestReadSourceObservesContextCancellation(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := f.provider.ReadSource(ctx, ports.ReadSourceRequest{
		Handle: f.handle, Revision: f.baseRev(), Path: "service.txt", ByteLimit: 4096,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadSource(cancelled ctx) error = %v, want context.Canceled", err)
	}
}

// --- ReadDiff ---

func TestReadDiffReturnsExactBaseResultPatch(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	current := f.commitWorkspaceChange(t, "service.txt", "line-1\nline-2\n")

	diff, err := f.provider.ReadDiff(context.Background(), ports.ReadDiffRequest{
		Handle: f.handle, BaseRevision: f.baseRev(), ResultRevision: current, ByteLimit: 8192, FileLimit: 10,
	})
	if err != nil {
		t.Fatalf("ReadDiff: %v", err)
	}
	if len(diff.Files) != 1 || diff.Files[0].Path != "service.txt" {
		t.Fatalf("Files = %+v, want exactly one entry for service.txt", diff.Files)
	}
	if diff.Files[0].Additions == 0 {
		t.Fatalf("expected at least one addition, got %+v", diff.Files[0])
	}
	if !bytes.Contains(diff.Patch, []byte("service.txt")) {
		t.Fatalf("Patch does not mention the changed path: %s", diff.Patch)
	}
	if diff.FilesTruncated || diff.PatchTruncated {
		t.Fatalf("unexpected truncation for a small diff: %+v", diff)
	}
}

func TestReadDiffEnforcesFileLimit(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	workspacePath, err := f.provider.workspacePath(f.handle)
	if err != nil {
		t.Fatalf("resolve workspace path: %v", err)
	}
	writeTestFile(t, filepath.Join(workspacePath, "a.txt"), "a\n")
	writeTestFile(t, filepath.Join(workspacePath, "b.txt"), "b\n")
	writeTestFile(t, filepath.Join(workspacePath, "c.txt"), "c\n")
	current, err := f.provider.CreateLocalCommit(context.Background(), ports.CreateLocalCommitRequest{
		Handle: f.handle, Message: "add three files", AuthorName: "Agent Kit Test", AuthorEmail: "agent-kit@example.invalid",
	})
	if err != nil {
		t.Fatalf("create local commit: %v", err)
	}

	diff, err := f.provider.ReadDiff(context.Background(), ports.ReadDiffRequest{
		Handle: f.handle, BaseRevision: f.baseRev(), ResultRevision: current, ByteLimit: 8192, FileLimit: 1,
	})
	if err != nil {
		t.Fatalf("ReadDiff: %v", err)
	}
	if !diff.FilesTruncated || len(diff.Files) != 1 {
		t.Fatalf("expected FilesTruncated=true with exactly 1 file, got FilesTruncated=%v len=%d", diff.FilesTruncated, len(diff.Files))
	}
}

func TestReadDiffEnforcesByteLimitOnPatch(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	current := f.commitWorkspaceChange(t, "service.txt", strings.Repeat("a whole line of content\n", 50))

	diff, err := f.provider.ReadDiff(context.Background(), ports.ReadDiffRequest{
		Handle: f.handle, BaseRevision: f.baseRev(), ResultRevision: current, ByteLimit: 50, FileLimit: 10,
	})
	if err != nil {
		t.Fatalf("ReadDiff: %v", err)
	}
	if !diff.PatchTruncated || int64(len(diff.Patch)) != 50 {
		t.Fatalf("expected PatchTruncated=true with exactly 50 bytes, got PatchTruncated=%v len=%d", diff.PatchTruncated, len(diff.Patch))
	}
}

func TestReadDiffDetectsBinaryFileChange(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	binaryContent := string([]byte{0x00, 0x01, 0x02, 'B', 'I', 'N'})
	current := f.commitWorkspaceChange(t, "asset.bin", binaryContent)

	diff, err := f.provider.ReadDiff(context.Background(), ports.ReadDiffRequest{
		Handle: f.handle, BaseRevision: f.baseRev(), ResultRevision: current, ByteLimit: 8192, FileLimit: 10,
	})
	if err != nil {
		t.Fatalf("ReadDiff: %v", err)
	}
	found := false
	for _, file := range diff.Files {
		if file.Path == "asset.bin" {
			found = true
			if !file.Binary {
				t.Fatalf("expected Binary=true for asset.bin, got %+v", file)
			}
		}
	}
	if !found {
		t.Fatalf("asset.bin missing from Files: %+v", diff.Files)
	}
}

func TestReadDiffRejectsRealButUnauthorizedRevision(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	writeTestFile(t, filepath.Join(f.sourceRepo, "service.txt"), "unauthorized\n")
	runTestGit(t, f.sourceRepo, "add", "--", "service.txt")
	runTestGit(t, f.sourceRepo, "commit", "-m", "unauthorized")
	unauthorized := strings.TrimSpace(runTestGit(t, f.sourceRepo, "rev-parse", "HEAD"))
	revision := workspace.Revision{RepositoryID: f.repositoryID, VCSObjectID: unauthorized, WorkspaceGeneration: f.generation}

	if _, err := f.provider.ReadDiff(context.Background(), ports.ReadDiffRequest{
		Handle: f.handle, BaseRevision: revision, ResultRevision: f.currentRev(), ByteLimit: 4096, FileLimit: 10,
	}); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("ReadDiff(unauthorized base) error = %v, want ErrInvalidSpec", err)
	}
	if _, err := f.provider.ReadDiff(context.Background(), ports.ReadDiffRequest{
		Handle: f.handle, BaseRevision: f.baseRev(), ResultRevision: revision, ByteLimit: 4096, FileLimit: 10,
	}); !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("ReadDiff(unauthorized result) error = %v, want ErrInvalidSpec", err)
	}
}

func TestReadDiffObservesContextCancellation(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := f.provider.ReadDiff(ctx, ports.ReadDiffRequest{
		Handle: f.handle, BaseRevision: f.baseRev(), ResultRevision: f.currentRev(), ByteLimit: 4096, FileLimit: 10,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadDiff(cancelled ctx) error = %v, want context.Canceled", err)
	}
}

// --- ReadRepositoryLog ---

func TestReadRepositoryLogPaginatesStably(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	for i := 0; i < 5; i++ {
		f.commitWorkspaceChange(t, "service.txt", fmt.Sprintf("revision-%d\n", i))
	}
	anchor := f.currentRev()

	seen := map[string]bool{}
	var order []string
	cursor := ""
	for pageIndex := 0; pageIndex < 10; pageIndex++ {
		page, err := f.provider.ReadRepositoryLog(context.Background(), ports.ReadRepositoryLogRequest{
			Handle: f.handle, Anchor: anchor, Cursor: cursor, Limit: 2, ByteLimit: 1 << 16,
		})
		if err != nil {
			t.Fatalf("ReadRepositoryLog page %d: %v", pageIndex, err)
		}
		if len(page.Entries) == 0 {
			t.Fatalf("page %d returned zero entries before NextCursor was empty", pageIndex)
		}
		for _, entry := range page.Entries {
			if seen[entry.CommitID] {
				t.Fatalf("commit %s returned twice across pages — cursor is not stable", entry.CommitID)
			}
			seen[entry.CommitID] = true
			order = append(order, entry.CommitID)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}

	// 1 base commit + 5 workspace commits = 6 total, matching real `git
	// rev-list --count` over the exact same anchor.
	workspacePath, err := f.provider.workspacePath(f.handle)
	if err != nil {
		t.Fatalf("resolve workspace path: %v", err)
	}
	countOutput := strings.TrimSpace(runTestGit(t, workspacePath, "rev-list", "--count", anchor.VCSObjectID))
	if countOutput != fmt.Sprintf("%d", len(order)) {
		t.Fatalf("paginated total = %d, want real rev-list count %s", len(order), countOutput)
	}
	if order[0] != anchor.VCSObjectID {
		t.Fatalf("first paginated entry = %s, want anchor %s", order[0], anchor.VCSObjectID)
	}
}

func TestReadRepositoryLogRejectsCursorOutsideAnchorAncestry(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	current := f.commitWorkspaceChange(t, "service.txt", "line-1\nline-2\n")

	writeTestFile(t, filepath.Join(f.sourceRepo, "unrelated.txt"), "unrelated\n")
	runTestGit(t, f.sourceRepo, "add", "--", "unrelated.txt")
	runTestGit(t, f.sourceRepo, "commit", "-m", "unrelated history")
	outsideCommit := strings.TrimSpace(runTestGit(t, f.sourceRepo, "rev-parse", "HEAD"))

	_, err := f.provider.ReadRepositoryLog(context.Background(), ports.ReadRepositoryLogRequest{
		Handle: f.handle, Anchor: current, Cursor: outsideCommit, Limit: 5, ByteLimit: 1 << 16,
	})
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("ReadRepositoryLog(cursor outside ancestry) error = %v, want ErrInvalidSpec", err)
	}
}

func TestReadRepositoryLogEnforcesByteLimit(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	for i := 0; i < 5; i++ {
		f.commitWorkspaceChange(t, "service.txt", fmt.Sprintf("revision-%d\n", i))
	}
	anchor := f.currentRev()

	page, err := f.provider.ReadRepositoryLog(context.Background(), ports.ReadRepositoryLogRequest{
		Handle: f.handle, Anchor: anchor, Limit: 10, ByteLimit: 40,
	})
	if err != nil {
		t.Fatalf("ReadRepositoryLog: %v", err)
	}
	if !page.Truncated {
		t.Fatal("expected Truncated=true when ByteLimit cuts the page short")
	}
	if len(page.Entries) >= 6 {
		t.Fatalf("expected fewer than the real 6-commit history under a tight ByteLimit, got %d entries", len(page.Entries))
	}
}

func TestReadRepositoryLogRejectsRealButUnauthorizedAnchor(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	writeTestFile(t, filepath.Join(f.sourceRepo, "service.txt"), "unauthorized\n")
	runTestGit(t, f.sourceRepo, "add", "--", "service.txt")
	runTestGit(t, f.sourceRepo, "commit", "-m", "unauthorized")
	unauthorized := strings.TrimSpace(runTestGit(t, f.sourceRepo, "rev-parse", "HEAD"))
	anchor := workspace.Revision{RepositoryID: f.repositoryID, VCSObjectID: unauthorized, WorkspaceGeneration: f.generation}

	_, err := f.provider.ReadRepositoryLog(context.Background(), ports.ReadRepositoryLogRequest{
		Handle: f.handle, Anchor: anchor, Limit: 5, ByteLimit: 1 << 16,
	})
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("ReadRepositoryLog(unauthorized anchor) error = %v, want ErrInvalidSpec", err)
	}
}

func TestReadRepositoryLogObservesContextCancellation(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := f.provider.ReadRepositoryLog(ctx, ports.ReadRepositoryLogRequest{
		Handle: f.handle, Anchor: f.baseRev(), Limit: 5, ByteLimit: 1 << 16,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ReadRepositoryLog(cancelled ctx) error = %v, want context.Canceled", err)
	}
}

// --- runGitCapped ---

func TestRunGitCappedTruncatesAtExactByteLimit(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	workspacePath, err := f.provider.workspacePath(f.handle)
	if err != nil {
		t.Fatalf("resolve workspace path: %v", err)
	}
	data, truncated, err := f.provider.runGitCapped(context.Background(), workspacePath, 3, "cat-file", "-p", f.baseRev().VCSObjectID+":service.txt")
	if err != nil {
		t.Fatalf("runGitCapped: %v", err)
	}
	if !truncated || len(data) != 3 {
		t.Fatalf("truncated=%v len=%d, want truncated=true len=3", truncated, len(data))
	}
}

func TestRunGitCappedObservesPreCancelledContext(t *testing.T) {
	t.Parallel()
	f := newInspectionFixture(t)
	workspacePath, err := f.provider.workspacePath(f.handle)
	if err != nil {
		t.Fatalf("resolve workspace path: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = f.provider.runGitCapped(ctx, workspacePath, 1<<20, "log", "--format=%H")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runGitCapped(cancelled ctx) error = %v, want context.Canceled", err)
	}
}
