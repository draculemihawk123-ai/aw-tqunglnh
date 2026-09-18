package workspace_test

// Real, in-process coverage for V6-15L's own repository-workspace
// source/diff/log subcommands, driving this package's own Run* functions
// directly against a REAL *sqlite.Store and a REAL
// internal/adapters/gitworktree.Provider running against a REAL temporary
// Git repository — never a mock, never a mocked git command. Mirrors
// internal/delivery/httpapi/workspaceinspection/fixture_test.go's own
// newTestEnv idiom almost verbatim, except this package drives the CLI
// leaf's own Run* functions directly rather than a real HTTP round trip.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/gitworktree"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	cliworkspace "github.com/taQuangLing/agent-workflow/internal/delivery/cli/workspace"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

const (
	inspectProjectID             = "project-1"
	inspectRepositoryID          = "repo-1"
	inspectFamilyID              = "family-1"
	inspectWorkspaceSetID        = "set-1"
	inspectRepositoryWorkspaceID = "rw-1"
)

// inspectionTestEnv is one real UnitOfWork + real gitworktree.Provider pair
// + this package's own Dependencies, torn down via t.Cleanup, plus every
// identifier a test needs to drive the one seeded RepositoryWorkspace.
type inspectionTestEnv struct {
	deps            cliworkspace.Dependencies
	uow             ports.UnitOfWork
	provider        *gitworktree.Provider
	handle          ports.WorkspaceHandle
	baseRevision    workspace.Revision
	currentRevision workspace.Revision
	sourceRepo      string
}

// newInspectionTestEnv provisions a real Git repository + real
// gitworktree.Provider workspace, seeds a real
// Project->Repository->TaskFamily->WorkspaceSet->RepositoryWorkspace
// ownership chain via real sqlite persistence at initialState — mirrors
// internal/delivery/httpapi/workspaceinspection/fixture_test.go's own
// newTestEnv exactly.
func newInspectionTestEnv(t *testing.T, initialState workspace.RepositoryWorkspaceState) *inspectionTestEnv {
	t.Helper()
	root := t.TempDir()
	sourceRepo, baseSHA := createGitRepository(t, filepath.Join(root, "source"), "line-1\n")
	provider, err := gitworktree.New(gitworktree.Config{Root: filepath.Join(root, "workspaces")})
	if err != nil {
		t.Fatalf("gitworktree.New: %v", err)
	}

	ctx := context.Background()
	handle, err := provider.Provision(ctx, ports.ProvisionSpec{
		RepositoryID:    project.RepositoryID(inspectRepositoryID),
		LocalRepository: sourceRepo,
		BaseRef:         baseSHA,
		FamilyID:        work.TaskFamilyID(inspectFamilyID),
		WorkspaceSetID:  workspace.WorkspaceSetID(inspectWorkspaceSetID),
		Generation:      1,
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	inspection, err := provider.Inspect(ctx, handle)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}

	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "ws-inspect-cli.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	uow := sqlite.NewUnitOfWork(store)

	seedInspectionOwnershipChain(t, uow, inspection, handle, initialState)

	return &inspectionTestEnv{
		deps:            cliworkspace.Dependencies{UOW: uow, Reader: provider},
		uow:             uow,
		provider:        provider,
		handle:          handle,
		baseRevision:    inspection.BaseRevision,
		currentRevision: inspection.CurrentRevision,
		sourceRepo:      sourceRepo,
	}
}

// seedInspectionOwnershipChain mirrors
// internal/delivery/httpapi/workspaceinspection/fixture_test.go's own
// identical helper exactly.
func seedInspectionOwnershipChain(
	t *testing.T,
	uow ports.UnitOfWork,
	inspection ports.WorkspaceInspection,
	handle ports.WorkspaceHandle,
	state workspace.RepositoryWorkspaceState,
) {
	t.Helper()
	ctx := context.Background()
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if _, err := tx.Catalog().CreateProject(ctx, ports.CreateProjectRequest{ID: inspectProjectID, Name: "Project One"}); err != nil {
			return err
		}
		if _, err := tx.Catalog().RegisterRepository(ctx, ports.RegisterRepositoryRequest{
			ID: inspectRepositoryID, ProjectID: inspectProjectID, Name: "repo",
			RemoteLocator: "https://example.invalid/repo.git", DefaultRef: "main",
		}); err != nil {
			return err
		}

		root, err := work.NewRootWorkItem(work.WorkItemID("wi-root"), project.ProjectID(inspectProjectID), work.TaskFamilyID(inspectFamilyID), "root")
		if err != nil {
			return err
		}
		family, err := work.NewTaskFamily(work.TaskFamilyID(inspectFamilyID), root)
		if err != nil {
			return err
		}
		if _, err := tx.Work().CreateTaskFamily(ctx, family); err != nil {
			return err
		}

		set, err := workspace.NewWorkspaceSet(workspace.WorkspaceSetID(inspectWorkspaceSetID), family)
		if err != nil {
			return err
		}
		if _, err := tx.Work().CreateWorkspaceSet(ctx, set); err != nil {
			return err
		}

		repository, err := tx.Catalog().GetRepository(ctx, inspectRepositoryID)
		if err != nil {
			return err
		}
		rw, err := workspace.NewRepositoryWorkspace(
			workspace.RepositoryWorkspaceID(inspectRepositoryWorkspaceID), set, repository, inspection.Generation,
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

// --- shared real-git test helpers (no mocked git command anywhere) ---
// Mirrors internal/delivery/httpapi/workspaceinspection/fixture_test.go's
// own identical helpers exactly.

func createGitRepository(t *testing.T, repositoryPath string, content string) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("Git is required for workspace inspection CLI tests: %v", err)
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
// directory and commits it via the real CreateLocalCommit path — mirrors
// internal/delivery/httpapi/workspaceinspection/fixture_test.go's own
// identical helper.
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

// sourceArgs builds `repository-workspace source`'s own flag arguments —
// flags must precede the positional <repositoryWorkspaceId> argument, so
// any extra flags (e.g. --line-limit) are appended BEFORE it, never after.
func sourceArgs(revision workspace.Revision, treePath, output string, extra ...string) []string {
	args := []string{
		"--project-id", inspectProjectID, "--repository-id", inspectRepositoryID, "--workspace-set-id", inspectWorkspaceSetID,
		"--revision", revision.VCSObjectID, "--revision-generation", uintToString(revision.WorkspaceGeneration),
		"--path", treePath, "--output", output,
	}
	args = append(args, extra...)
	return append(args, inspectRepositoryWorkspaceID)
}

func diffArgs(base, result workspace.Revision) []string {
	return []string{
		"--project-id", inspectProjectID, "--repository-id", inspectRepositoryID, "--workspace-set-id", inspectWorkspaceSetID,
		"--base-revision", base.VCSObjectID, "--base-revision-generation", uintToString(base.WorkspaceGeneration),
		"--result-revision", result.VCSObjectID, "--result-revision-generation", uintToString(result.WorkspaceGeneration),
		inspectRepositoryWorkspaceID,
	}
}

func logArgs(anchor workspace.Revision, cursor string, limit string) []string {
	args := []string{
		"--project-id", inspectProjectID, "--repository-id", inspectRepositoryID, "--workspace-set-id", inspectWorkspaceSetID,
		"--anchor", anchor.VCSObjectID, "--anchor-generation", uintToString(anchor.WorkspaceGeneration),
	}
	if cursor != "" {
		args = append(args, "--cursor", cursor)
	}
	if limit != "" {
		args = append(args, "--limit", limit)
	}
	return append(args, inspectRepositoryWorkspaceID)
}

func uintToString(v uint64) string {
	return strconv.FormatUint(v, 10)
}
