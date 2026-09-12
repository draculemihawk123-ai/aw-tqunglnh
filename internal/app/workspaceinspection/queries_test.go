package workspaceinspection_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/gitworktree"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/workspaceinspection"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// fixture wires internal/app/workspaceinspection.Queries against a real
// internal/adapters/gitworktree.Provider (a real temporary Git repository —
// never a mocked git command) and a fake, in-memory
// ports.UnitOfWork/ports.Tx (the same "fast, sqlite-free" persistence double
// every other internal/app/*/handler_test.go in this codebase already uses)
// holding a real, fully-linked Project -> Repository -> TaskFamily ->
// WorkspaceSet -> RepositoryWorkspace ownership chain.
type fixture struct {
	uow        *fake.UnitOfWork
	queries    *workspaceinspection.Queries
	provider   *gitworktree.Provider
	scope      workspaceinspection.WorkspaceScope
	base       workspace.Revision
	current    workspace.Revision
	handle     ports.WorkspaceHandle
	sourceRepo string
}

const (
	fixtureProjectID             = "project-1"
	fixtureRepositoryID          = "repo-1"
	fixtureFamilyID              = "family-1"
	fixtureWorkspaceSetID        = "set-1"
	fixtureRepositoryWorkspaceID = "rw-1"
)

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	sourceRepo, baseSHA := createGitRepository(t, filepath.Join(root, "source"), "line-1\n")
	provider, err := gitworktree.New(gitworktree.Config{Root: filepath.Join(root, "workspaces")})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}

	ctx := context.Background()
	handle, err := provider.Provision(ctx, ports.ProvisionSpec{
		RepositoryID:    project.RepositoryID(fixtureRepositoryID),
		LocalRepository: sourceRepo,
		BaseRef:         baseSHA,
		FamilyID:        work.TaskFamilyID(fixtureFamilyID),
		WorkspaceSetID:  workspace.WorkspaceSetID(fixtureWorkspaceSetID),
		Generation:      1,
	})
	if err != nil {
		t.Fatalf("provision fixture workspace: %v", err)
	}
	inspection, err := provider.Inspect(ctx, handle)
	if err != nil {
		t.Fatalf("inspect fixture workspace: %v", err)
	}

	uow := fake.New()
	seedOwnershipChain(t, uow, inspection, handle, workspace.RepositoryWorkspaceReady)

	return &fixture{
		uow: uow, queries: workspaceinspection.New(uow, provider), provider: provider,
		scope: workspaceinspection.WorkspaceScope{
			ProjectID: fixtureProjectID, RepositoryID: fixtureRepositoryID,
			WorkspaceSetID: fixtureWorkspaceSetID, RepositoryWorkspaceID: fixtureRepositoryWorkspaceID,
		},
		base: inspection.BaseRevision, current: inspection.CurrentRevision, handle: handle, sourceRepo: sourceRepo,
	}
}

// seedOwnershipChain persists a real Project -> Repository -> TaskFamily ->
// WorkspaceSet -> RepositoryWorkspace chain via the same real domain
// constructors and ports.Tx methods production code uses (never a
// hand-built row bypassing them, except the one documented exception every
// real handler in this codebase already uses too: directly setting
// RepositoryWorkspace.State/CurrentRevision after construction — see
// internal/app/workspaceprovision.Handler.finishReady's own identical
// pattern — since workspace.NewRepositoryWorkspace itself always starts a
// fresh row at PROVISIONING).
func seedOwnershipChain(
	t *testing.T,
	uow *fake.UnitOfWork,
	inspection ports.WorkspaceInspection,
	handle ports.WorkspaceHandle,
	state workspace.RepositoryWorkspaceState,
) {
	t.Helper()
	ctx := context.Background()
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if _, err := tx.Catalog().CreateProject(ctx, ports.CreateProjectRequest{ID: fixtureProjectID, Name: "Project One"}); err != nil {
			return err
		}
		if _, err := tx.Catalog().RegisterRepository(ctx, ports.RegisterRepositoryRequest{
			ID: fixtureRepositoryID, ProjectID: fixtureProjectID, Name: "repo",
			RemoteLocator: "https://example.invalid/repo.git", DefaultRef: "main",
		}); err != nil {
			return err
		}

		root, err := work.NewRootWorkItem(work.WorkItemID("wi-root"), project.ProjectID(fixtureProjectID), work.TaskFamilyID(fixtureFamilyID), "root")
		if err != nil {
			return err
		}
		family, err := work.NewTaskFamily(work.TaskFamilyID(fixtureFamilyID), root)
		if err != nil {
			return err
		}
		if _, err := tx.Work().CreateTaskFamily(ctx, family); err != nil {
			return err
		}

		set, err := workspace.NewWorkspaceSet(workspace.WorkspaceSetID(fixtureWorkspaceSetID), family)
		if err != nil {
			return err
		}
		if _, err := tx.Work().CreateWorkspaceSet(ctx, set); err != nil {
			return err
		}

		repository, err := tx.Catalog().GetRepository(ctx, fixtureRepositoryID)
		if err != nil {
			return err
		}
		rw, err := workspace.NewRepositoryWorkspace(
			workspace.RepositoryWorkspaceID(fixtureRepositoryWorkspaceID), set, repository, inspection.Generation,
			handle.String(), inspection.BranchRef, inspection.BaseRevision.VCSObjectID,
		)
		if err != nil {
			return err
		}
		rw.State = state
		rw.CurrentRevision = inspection.CurrentRevision.VCSObjectID
		_, err = tx.Work().CreateRepositoryWorkspace(ctx, rw)
		return err
	})
	if err != nil {
		t.Fatalf("seed ownership chain: %v", err)
	}
}

// --- GetSource ---

func TestGetSourceHappyPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	result, err := f.queries.GetSource(context.Background(), workspaceinspection.GetSourceRequest{
		Scope: f.scope, Revision: f.base, Path: "service.txt", ByteLimit: 4096, LineLimit: 100,
	})
	if err != nil {
		t.Fatalf("GetSource: %v", err)
	}
	if string(result.Content) != "line-1\n" {
		t.Fatalf("Content = %q, want %q", result.Content, "line-1\n")
	}
}

func TestGetSourceAppliesDefaultLimitsWhenUnset(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	result, err := f.queries.GetSource(context.Background(), workspaceinspection.GetSourceRequest{
		Scope: f.scope, Revision: f.base, Path: "service.txt",
	})
	if err != nil {
		t.Fatalf("GetSource: %v", err)
	}
	if result.ByteLimit <= 0 || result.LineLimit <= 0 {
		t.Fatalf("expected positive default limits, got ByteLimit=%d LineLimit=%d", result.ByteLimit, result.LineLimit)
	}
}

func TestGetSourceRejectsProjectMismatch(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	scope := f.scope
	scope.ProjectID = "someone-elses-project"

	_, err := f.queries.GetSource(context.Background(), workspaceinspection.GetSourceRequest{
		Scope: scope, Revision: f.base, Path: "service.txt", ByteLimit: 4096,
	})
	if !errors.Is(err, workspaceinspection.ErrScopeMismatch) {
		t.Fatalf("GetSource(wrong project) error = %v, want ErrScopeMismatch", err)
	}
}

func TestGetSourceRejectsRepositoryMismatch(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	scope := f.scope
	scope.RepositoryID = "someone-elses-repository"

	_, err := f.queries.GetSource(context.Background(), workspaceinspection.GetSourceRequest{
		Scope: scope, Revision: f.base, Path: "service.txt", ByteLimit: 4096,
	})
	if !errors.Is(err, workspaceinspection.ErrScopeMismatch) {
		t.Fatalf("GetSource(wrong repository) error = %v, want ErrScopeMismatch", err)
	}
}

func TestGetSourceRejectsWorkspaceSetMismatch(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	scope := f.scope
	scope.WorkspaceSetID = "someone-elses-workspace-set"

	_, err := f.queries.GetSource(context.Background(), workspaceinspection.GetSourceRequest{
		Scope: scope, Revision: f.base, Path: "service.txt", ByteLimit: 4096,
	})
	if !errors.Is(err, workspaceinspection.ErrScopeMismatch) {
		t.Fatalf("GetSource(wrong workspace set) error = %v, want ErrScopeMismatch", err)
	}
}

func TestGetSourceRejectsUnknownRepositoryWorkspace(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	scope := f.scope
	scope.RepositoryWorkspaceID = "does-not-exist"

	_, err := f.queries.GetSource(context.Background(), workspaceinspection.GetSourceRequest{
		Scope: scope, Revision: f.base, Path: "service.txt", ByteLimit: 4096,
	})
	if !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("GetSource(unknown repository workspace) error = %v, want ErrPersistenceNotFound", err)
	}
}

func TestGetSourceRejectsNonReadyWorkspace(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	sourceRepo, baseSHA := createGitRepository(t, filepath.Join(root, "source"), "line-1\n")
	provider, err := gitworktree.New(gitworktree.Config{Root: filepath.Join(root, "workspaces")})
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}
	ctx := context.Background()
	handle, err := provider.Provision(ctx, ports.ProvisionSpec{
		RepositoryID: project.RepositoryID(fixtureRepositoryID), LocalRepository: sourceRepo, BaseRef: baseSHA,
		FamilyID: work.TaskFamilyID(fixtureFamilyID), WorkspaceSetID: workspace.WorkspaceSetID(fixtureWorkspaceSetID), Generation: 1,
	})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	inspection, err := provider.Inspect(ctx, handle)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	uow := fake.New()
	seedOwnershipChain(t, uow, inspection, handle, workspace.RepositoryWorkspaceQuarantined)
	queries := workspaceinspection.New(uow, provider)

	_, err = queries.GetSource(ctx, workspaceinspection.GetSourceRequest{
		Scope: workspaceinspection.WorkspaceScope{
			ProjectID: fixtureProjectID, RepositoryID: fixtureRepositoryID,
			WorkspaceSetID: fixtureWorkspaceSetID, RepositoryWorkspaceID: fixtureRepositoryWorkspaceID,
		},
		Revision: inspection.BaseRevision, Path: "service.txt", ByteLimit: 4096,
	})
	if !errors.Is(err, workspaceinspection.ErrWorkspaceNotReady) {
		t.Fatalf("GetSource(quarantined workspace) error = %v, want ErrWorkspaceNotReady", err)
	}
}

func TestGetSourcePropagatesAdapterRejectionOfArbitraryRef(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	badRevision := workspace.Revision{RepositoryID: f.base.RepositoryID, VCSObjectID: "HEAD", WorkspaceGeneration: f.base.WorkspaceGeneration}

	_, err := f.queries.GetSource(context.Background(), workspaceinspection.GetSourceRequest{
		Scope: f.scope, Revision: badRevision, Path: "service.txt", ByteLimit: 4096,
	})
	if !errors.Is(err, gitworktree.ErrInvalidSpec) {
		t.Fatalf("GetSource(arbitrary ref) error = %v, want gitworktree.ErrInvalidSpec", err)
	}
}

func TestGetSourcePropagatesAdapterPathTraversalRejection(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	_, err := f.queries.GetSource(context.Background(), workspaceinspection.GetSourceRequest{
		Scope: f.scope, Revision: f.base, Path: "../outside.txt", ByteLimit: 4096,
	})
	if !errors.Is(err, gitworktree.ErrInvalidSpec) {
		t.Fatalf("GetSource(path traversal) error = %v, want gitworktree.ErrInvalidSpec", err)
	}
}

func TestGetSourceObservesContextCancellation(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := f.queries.GetSource(ctx, workspaceinspection.GetSourceRequest{
		Scope: f.scope, Revision: f.base, Path: "service.txt", ByteLimit: 4096,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GetSource(cancelled ctx) error = %v, want context.Canceled", err)
	}
}

// --- GetDiff ---

func TestGetDiffHappyPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	current := commitWorkspaceChange(t, f.provider, f.handle, "service.txt", "line-1\nline-2\n")

	diff, err := f.queries.GetDiff(context.Background(), workspaceinspection.GetDiffRequest{
		Scope: f.scope, BaseRevision: f.base, ResultRevision: current, ByteLimit: 8192, FileLimit: 10,
	})
	if err != nil {
		t.Fatalf("GetDiff: %v", err)
	}
	if len(diff.Files) != 1 || diff.Files[0].Path != "service.txt" {
		t.Fatalf("Files = %+v, want exactly one entry for service.txt", diff.Files)
	}
}

func TestGetDiffRejectsScopeMismatch(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	scope := f.scope
	scope.ProjectID = "wrong-project"

	_, err := f.queries.GetDiff(context.Background(), workspaceinspection.GetDiffRequest{
		Scope: scope, BaseRevision: f.base, ResultRevision: f.current, ByteLimit: 8192, FileLimit: 10,
	})
	if !errors.Is(err, workspaceinspection.ErrScopeMismatch) {
		t.Fatalf("GetDiff(wrong project) error = %v, want ErrScopeMismatch", err)
	}
}

func TestGetDiffPropagatesAdapterRejectionOfUnauthorizedRevision(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	writeTestFile(t, filepath.Join(f.sourceRepo, "service.txt"), "unauthorized\n")
	runTestGit(t, f.sourceRepo, "add", "--", "service.txt")
	runTestGit(t, f.sourceRepo, "commit", "-m", "unauthorized")
	unauthorized := strings.TrimSpace(runTestGit(t, f.sourceRepo, "rev-parse", "HEAD"))
	revision := workspace.Revision{RepositoryID: f.base.RepositoryID, VCSObjectID: unauthorized, WorkspaceGeneration: f.base.WorkspaceGeneration}

	_, err := f.queries.GetDiff(context.Background(), workspaceinspection.GetDiffRequest{
		Scope: f.scope, BaseRevision: revision, ResultRevision: f.current, ByteLimit: 8192, FileLimit: 10,
	})
	if !errors.Is(err, gitworktree.ErrInvalidSpec) {
		t.Fatalf("GetDiff(unauthorized base) error = %v, want gitworktree.ErrInvalidSpec", err)
	}
}

// --- GetRepositoryLog ---

func TestGetRepositoryLogPaginates(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	var anchor workspace.Revision
	for i := 0; i < 3; i++ {
		anchor = commitWorkspaceChange(t, f.provider, f.handle, "service.txt", fmt.Sprintf("revision-%d\n", i))
	}

	page1, err := f.queries.GetRepositoryLog(context.Background(), workspaceinspection.GetRepositoryLogRequest{
		Scope: f.scope, Anchor: anchor, Limit: 2, ByteLimit: 1 << 16,
	})
	if err != nil {
		t.Fatalf("GetRepositoryLog page 1: %v", err)
	}
	if len(page1.Entries) != 2 || page1.NextCursor == "" {
		t.Fatalf("page1 = %+v, want 2 entries and a non-empty cursor", page1)
	}

	page2, err := f.queries.GetRepositoryLog(context.Background(), workspaceinspection.GetRepositoryLogRequest{
		Scope: f.scope, Anchor: anchor, Cursor: page1.NextCursor, Limit: 2, ByteLimit: 1 << 16,
	})
	if err != nil {
		t.Fatalf("GetRepositoryLog page 2: %v", err)
	}
	if len(page2.Entries) == 0 {
		t.Fatal("page2 returned zero entries")
	}
	if page2.Entries[0].CommitID == page1.Entries[0].CommitID {
		t.Fatal("page2 repeated page1's own first entry — cursor did not advance")
	}
}

func TestGetRepositoryLogRejectsScopeMismatch(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	scope := f.scope
	scope.RepositoryID = "wrong-repo"

	_, err := f.queries.GetRepositoryLog(context.Background(), workspaceinspection.GetRepositoryLogRequest{
		Scope: scope, Anchor: f.base, Limit: 5, ByteLimit: 1 << 16,
	})
	if !errors.Is(err, workspaceinspection.ErrScopeMismatch) {
		t.Fatalf("GetRepositoryLog(wrong repository) error = %v, want ErrScopeMismatch", err)
	}
}

// --- shared real-git test helpers (no mocked git command anywhere) ---

func createGitRepository(t *testing.T, repositoryPath string, content string) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("Git is required for workspace inspection contract tests: %v", err)
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

// commitWorkspaceChange writes content into handle's own real working
// directory (resolved via the real ports.WorkspaceDirectoryResolver
// contract, never a path this test derives itself) and commits it via the
// real CreateLocalCommit path.
func commitWorkspaceChange(t *testing.T, provider *gitworktree.Provider, handle ports.WorkspaceHandle, relativePath, content string) workspace.Revision {
	t.Helper()
	ctx := context.Background()
	workspacePath, err := provider.WorkingDirectory(ctx, handle)
	if err != nil {
		t.Fatalf("resolve workspace directory: %v", err)
	}
	targetPath := filepath.Join(workspacePath, relativePath)
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		t.Fatalf("create parent directory for %s: %v", relativePath, err)
	}
	writeTestFile(t, targetPath, content)
	revision, err := provider.CreateLocalCommit(ctx, ports.CreateLocalCommitRequest{
		Handle: handle, Message: "update " + relativePath,
		AuthorName: "Agent Kit Test", AuthorEmail: "agent-kit@example.invalid",
	})
	if err != nil {
		t.Fatalf("create local commit for %s: %v", relativePath, err)
	}
	return revision
}
