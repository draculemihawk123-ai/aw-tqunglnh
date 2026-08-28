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
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

func TestProviderMultiRepositoryWorkspaceSetAndChildReuse(t *testing.T) {
	t.Parallel()

	fixtureRoot := t.TempDir()
	userRepository, userBase := createGitRepository(t, filepath.Join(fixtureRoot, "sources", "user service"), "user-v0\n")
	webRepository, webBase := createGitRepository(t, filepath.Join(fixtureRoot, "sources", "web app"), "web-v0\n")
	provider := newTestProvider(t, filepath.Join(fixtureRoot, "managed workspaces"))

	context := context.Background()
	familyID := work.TaskFamilyID("family-cross-service")
	setID := workspace.WorkspaceSetID("workspace-set-cross-service")
	userSpec := ports.ProvisionSpec{
		RepositoryID:    project.RepositoryID("repo-user"),
		LocalRepository: userRepository,
		BaseRef:         "HEAD",
		FamilyID:        familyID,
		WorkspaceSetID:  setID,
		Generation:      1,
	}
	webSpec := ports.ProvisionSpec{
		RepositoryID:    project.RepositoryID("repo-web"),
		LocalRepository: webRepository,
		BaseRef:         "HEAD",
		FamilyID:        familyID,
		WorkspaceSetID:  setID,
		Generation:      1,
	}

	userHandle, err := provider.Provision(context, userSpec)
	if err != nil {
		t.Fatalf("provision user workspace: %v", err)
	}
	webHandle, err := provider.Provision(context, webSpec)
	if err != nil {
		t.Fatalf("provision web workspace: %v", err)
	}
	if userHandle == webHandle {
		t.Fatal("different repositories in a WorkspaceSet received the same handle")
	}

	userInspection, err := provider.Inspect(context, userHandle)
	if err != nil {
		t.Fatalf("inspect user workspace: %v", err)
	}
	webInspection, err := provider.Inspect(context, webHandle)
	if err != nil {
		t.Fatalf("inspect web workspace: %v", err)
	}
	assertInspectionIdentity(t, userInspection, userSpec, userBase)
	assertInspectionIdentity(t, webInspection, webSpec, webBase)

	// A child WorkItem does not provision. Application state gives it the same
	// family-owned handle for the repository in its effective scope.
	familyHandles := map[project.RepositoryID]ports.WorkspaceHandle{
		userSpec.RepositoryID: userHandle,
		webSpec.RepositoryID:  webHandle,
	}
	childUserHandle := familyHandles[userSpec.RepositoryID]
	if childUserHandle != userHandle {
		t.Fatal("child did not reuse the family-owned repository handle")
	}
	childInspection, err := provider.Inspect(context, childUserHandle)
	if err != nil {
		t.Fatalf("inspect child-reused workspace: %v", err)
	}
	if childInspection.FamilyID != familyID || childInspection.WorkspaceSetID != setID {
		t.Fatal("child-reused handle resolved outside its TaskFamily/WorkspaceSet")
	}

	// Provision is idempotent for the exact same family/repository/generation.
	userHandleAgain, err := provider.Provision(context, userSpec)
	if err != nil {
		t.Fatalf("idempotent provision: %v", err)
	}
	if userHandleAgain != userHandle {
		t.Fatal("idempotent provision returned a different handle")
	}
	conflictingSpec := userSpec
	conflictingSpec.WorkspaceSetID = workspace.WorkspaceSetID("another-workspace-set")
	if _, err := provider.Provision(context, conflictingSpec); !errors.Is(err, ErrProvisionConflict) {
		t.Fatalf("conflicting workspace-set provision error = %v, want ErrProvisionConflict", err)
	}

	revisionSet, err := workspace.NewRevisionSet([]workspace.Revision{
		userInspection.CurrentRevision,
		webInspection.CurrentRevision,
	})
	if err != nil {
		t.Fatalf("create multi-repository RevisionSet: %v", err)
	}
	if len(revisionSet.Entries()) != 2 || revisionSet.ContentHash() == "" {
		t.Fatal("multi-repository RevisionSet is incomplete")
	}

	assertWorktreeCount(t, userRepository, 2)
	assertWorktreeCount(t, webRepository, 2)
	assertOpaqueHandle(t, userHandle, provider.root, string(familyID), string(userSpec.RepositoryID))
	assertOpaqueHandle(t, webHandle, provider.root, string(familyID), string(webSpec.RepositoryID))
}

func TestProviderIsolatesRootFamiliesAndCapturesDiff(t *testing.T) {
	t.Parallel()

	fixtureRoot := t.TempDir()
	repositoryPath, baseRevision := createGitRepository(t, filepath.Join(fixtureRoot, "sources", "shared service"), "base\n")
	provider := newTestProvider(t, filepath.Join(fixtureRoot, "workspaces"))
	context := context.Background()

	firstSpec := ports.ProvisionSpec{
		RepositoryID:    project.RepositoryID("repo-shared"),
		LocalRepository: repositoryPath,
		BaseRef:         baseRevision,
		FamilyID:        work.TaskFamilyID("family-one"),
		WorkspaceSetID:  workspace.WorkspaceSetID("set-one"),
		Generation:      1,
	}
	secondSpec := firstSpec
	secondSpec.FamilyID = work.TaskFamilyID("family-two")
	secondSpec.WorkspaceSetID = workspace.WorkspaceSetID("set-two")

	firstHandle, err := provider.Provision(context, firstSpec)
	if err != nil {
		t.Fatalf("provision first family: %v", err)
	}
	secondHandle, err := provider.Provision(context, secondSpec)
	if err != nil {
		t.Fatalf("provision second family: %v", err)
	}
	if firstHandle == secondHandle {
		t.Fatal("two root families received the same mutable workspace")
	}
	firstPath, err := provider.workspacePath(firstHandle)
	if err != nil {
		t.Fatalf("resolve first test workspace: %v", err)
	}
	secondPath, err := provider.workspacePath(secondHandle)
	if err != nil {
		t.Fatalf("resolve second test workspace: %v", err)
	}
	if samePath(firstPath, secondPath) {
		t.Fatal("two root-family handles resolved to one worktree")
	}

	writeTestFile(t, filepath.Join(firstPath, "service.txt"), "changed-only-by-family-one\n")
	firstInspection, err := provider.Inspect(context, firstHandle)
	if err != nil {
		t.Fatalf("inspect first family: %v", err)
	}
	secondInspection, err := provider.Inspect(context, secondHandle)
	if err != nil {
		t.Fatalf("inspect second family: %v", err)
	}
	if !firstInspection.Dirty {
		t.Fatal("first family change was not detected")
	}
	if secondInspection.Dirty {
		t.Fatal("first family change leaked into second family worktree")
	}
	secondContent, err := os.ReadFile(filepath.Join(secondPath, "service.txt"))
	if err != nil {
		t.Fatalf("read isolated second-family file: %v", err)
	}
	if string(secondContent) != "base\n" {
		t.Fatalf("second family observed leaked content %q", string(secondContent))
	}

	base := workspace.Revision{
		RepositoryID:        firstSpec.RepositoryID,
		VCSObjectID:         baseRevision,
		WorkspaceGeneration: firstSpec.Generation,
	}
	diff, err := provider.Diff(context, firstHandle, base)
	if err != nil {
		t.Fatalf("capture first-family diff: %v", err)
	}
	if diff.RepositoryID != firstSpec.RepositoryID || diff.BaseRevision != base {
		t.Fatal("diff lost repository/base revision provenance")
	}
	if len(diff.Files) != 1 || diff.Files[0].Path != "service.txt" {
		t.Fatalf("unexpected changed files: %#v", diff.Files)
	}
	if !strings.Contains(string(diff.Patch), "changed-only-by-family-one") {
		t.Fatalf("diff does not contain family change:\n%s", diff.Patch)
	}

	runTestGit(t, firstPath, "add", "--", "service.txt")
	runTestGit(t, firstPath, "commit", "-m", "family one change")
	committedRevision := strings.TrimSpace(runTestGit(t, firstPath, "rev-parse", "HEAD"))
	captured, err := provider.CaptureRevision(context, firstHandle)
	if err != nil {
		t.Fatalf("capture current revision: %v", err)
	}
	if captured.RepositoryID != firstSpec.RepositoryID || captured.VCSObjectID != committedRevision ||
		captured.WorkspaceGeneration != firstSpec.Generation {
		t.Fatalf("unexpected captured revision: %#v", captured)
	}
	if captured.VCSObjectID == baseRevision {
		t.Fatal("capture revision did not observe the family worktree commit")
	}
	secondRevision, err := provider.CaptureRevision(context, secondHandle)
	if err != nil {
		t.Fatalf("capture isolated second-family revision: %v", err)
	}
	if secondRevision.VCSObjectID != baseRevision {
		t.Fatalf("family-one commit leaked into family two: %#v", secondRevision)
	}
	assertWorktreeCount(t, repositoryPath, 3)
}

func TestProviderReleaseIsSafeAndIdempotent(t *testing.T) {
	t.Parallel()

	fixtureRoot := t.TempDir()
	repositoryPath, baseRevision := createGitRepository(t, filepath.Join(fixtureRoot, "sources", "release service"), "base\n")
	provider := newTestProvider(t, filepath.Join(fixtureRoot, "managed"))
	context := context.Background()
	spec := ports.ProvisionSpec{
		RepositoryID:    project.RepositoryID("repo-release"),
		LocalRepository: repositoryPath,
		BaseRef:         baseRevision,
		FamilyID:        work.TaskFamilyID("family-release"),
		WorkspaceSetID:  workspace.WorkspaceSetID("set-release"),
		Generation:      1,
	}
	handle, err := provider.Provision(context, spec)
	if err != nil {
		t.Fatalf("provision release workspace: %v", err)
	}
	workspacePath, err := provider.workspacePath(handle)
	if err != nil {
		t.Fatalf("resolve release test workspace: %v", err)
	}

	writeTestFile(t, filepath.Join(workspacePath, "service.txt"), "dirty\n")
	if err := provider.Release(context, handle); !errors.Is(err, ErrWorkspaceDirty) {
		t.Fatalf("release dirty workspace error = %v, want ErrWorkspaceDirty", err)
	}
	if _, err := os.Stat(workspacePath); err != nil {
		t.Fatalf("dirty workspace was removed: %v", err)
	}
	runTestGit(t, workspacePath, "checkout", "--", "service.txt")

	if err := provider.Release(context, handle); err != nil {
		t.Fatalf("release clean workspace: %v", err)
	}
	if _, err := os.Stat(workspacePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("released workspace still exists or has unexpected error: %v", err)
	}
	if _, err := os.Stat(repositoryPath); err != nil {
		t.Fatalf("release damaged source repository: %v", err)
	}
	if err := provider.Release(context, handle); err != nil {
		t.Fatalf("idempotent release: %v", err)
	}
	released, err := provider.Inspect(context, handle)
	if err != nil {
		t.Fatalf("inspect released workspace: %v", err)
	}
	if released.State != ports.WorkspaceReleased {
		t.Fatalf("released workspace state = %q", released.State)
	}
	if _, err := provider.CaptureRevision(context, handle); !errors.Is(err, ErrWorkspaceReleased) {
		t.Fatalf("capture released workspace error = %v, want ErrWorkspaceReleased", err)
	}
	nextGenerationSpec := spec
	nextGenerationSpec.Generation = 2
	nextGenerationHandle, err := provider.Provision(context, nextGenerationSpec)
	if err != nil {
		t.Fatalf("provision next workspace generation: %v", err)
	}
	if nextGenerationHandle == handle {
		t.Fatal("new workspace generation reused the released generation handle")
	}
	nextGeneration, err := provider.Inspect(context, nextGenerationHandle)
	if err != nil {
		t.Fatalf("inspect next workspace generation: %v", err)
	}
	if nextGeneration.Generation != 2 || nextGeneration.BaseRevision.VCSObjectID != baseRevision {
		t.Fatalf("unexpected next-generation workspace: %#v", nextGeneration)
	}
	if err := provider.Release(context, nextGenerationHandle); err != nil {
		t.Fatalf("release next workspace generation: %v", err)
	}

	forged, err := ports.NewWorkspaceHandle(handlePrefix + strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("construct forged test handle: %v", err)
	}
	if err := provider.Release(context, forged); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("forged release error = %v, want ErrWorkspaceNotFound", err)
	}
	malicious, err := ports.NewWorkspaceHandle("../../outside")
	if err != nil {
		t.Fatalf("construct opaque malicious test handle: %v", err)
	}
	if err := provider.Release(context, malicious); !errors.Is(err, ErrInvalidHandle) {
		t.Fatalf("traversal handle release error = %v, want ErrInvalidHandle", err)
	}
}

func TestProviderRejectsRepositoryInsideManagedRoot(t *testing.T) {
	t.Parallel()

	fixtureRoot := t.TempDir()
	managedRoot := filepath.Join(fixtureRoot, "managed")
	provider := newTestProvider(t, managedRoot)
	repositoryPath, _ := createGitRepository(t, filepath.Join(managedRoot, "source-inside-root"), "base\n")
	spec := ports.ProvisionSpec{
		RepositoryID:    project.RepositoryID("repo-unsafe"),
		LocalRepository: repositoryPath,
		BaseRef:         "HEAD",
		FamilyID:        work.TaskFamilyID("family-unsafe"),
		WorkspaceSetID:  workspace.WorkspaceSetID("set-unsafe"),
		Generation:      1,
	}
	if _, err := provider.Provision(context.Background(), spec); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("provision source inside managed root error = %v, want ErrUnsafePath", err)
	}
}

func newTestProvider(t *testing.T, root string) *Provider {
	t.Helper()
	provider, err := New(Config{Root: root})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	return provider
}

func createGitRepository(t *testing.T, repositoryPath string, content string) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("Git is required for workspace contract tests: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(repositoryPath), 0o755); err != nil {
		t.Fatalf("create repository parent: %v", err)
	}
	runTestGit(t, "", "init", "--initial-branch=main", repositoryPath)
	runTestGit(t, repositoryPath, "config", "user.name", "Agent Kit Test")
	runTestGit(t, repositoryPath, "config", "user.email", "agent-kit@example.invalid")
	runTestGit(t, repositoryPath, "config", "core.autocrlf", "false")
	writeTestFile(t, filepath.Join(repositoryPath, "service.txt"), content)
	runTestGit(t, repositoryPath, "add", "--", "service.txt")
	runTestGit(t, repositoryPath, "commit", "-m", "initial fixture")
	return repositoryPath, strings.TrimSpace(runTestGit(t, repositoryPath, "rev-parse", "HEAD"))
}

func runTestGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	commandArguments := arguments
	if directory != "" {
		commandArguments = append([]string{"-C", directory}, arguments...)
	}
	command := exec.Command("git", commandArguments...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", commandArguments, err, output)
	}
	return string(output)
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func assertInspectionIdentity(
	t *testing.T,
	inspection ports.WorkspaceInspection,
	spec ports.ProvisionSpec,
	baseRevision string,
) {
	t.Helper()
	if inspection.RepositoryID != spec.RepositoryID || inspection.FamilyID != spec.FamilyID ||
		inspection.WorkspaceSetID != spec.WorkspaceSetID || inspection.Generation != spec.Generation {
		t.Fatalf("inspection identity mismatch: %#v", inspection)
	}
	if inspection.BaseRevision.VCSObjectID != baseRevision || inspection.CurrentRevision.VCSObjectID != baseRevision {
		t.Fatalf("inspection did not pin exact base revision: %#v", inspection)
	}
	if inspection.State != ports.WorkspaceReady || inspection.Dirty {
		t.Fatalf("new workspace is not clean and ready: %#v", inspection)
	}
}

func assertOpaqueHandle(t *testing.T, handle ports.WorkspaceHandle, forbidden ...string) {
	t.Helper()
	if handle.IsZero() {
		t.Fatal("workspace handle is empty")
	}
	for _, value := range forbidden {
		if value != "" && strings.Contains(handle.String(), value) {
			t.Fatalf("opaque handle %q leaked %q", handle.String(), value)
		}
	}
}

func assertWorktreeCount(t *testing.T, repositoryPath string, expected int) {
	t.Helper()
	output := runTestGit(t, repositoryPath, "worktree", "list", "--porcelain")
	count := 0
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			count++
		}
	}
	if count != expected {
		t.Fatalf("worktree count = %d, want %d\n%s", count, expected, output)
	}
}
