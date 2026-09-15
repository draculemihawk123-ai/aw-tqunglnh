package releaseset_test

// This file exercises V6-10F's own "partial" Verify-line scenario end to
// end, against the real stack: a ReleaseSet with two repository entries,
// where one repository's own local-commit operation actually ran to
// COMMITTED (a real, temporary Git repository, a real gitworktree.Provider,
// the real internal/app/releasesetcommit.ExecuteReleaseSetLocalCommit
// worker — driven directly here, in test setup, never through this
// package's own HTTP handler, which V6-10F's own "Không làm" forbids from
// ever reaching a real Git adapter or worker itself) and the other stays
// REQUESTED (never executed) — proving the GET status route reports each
// operation's own accurate, un-conflated state, not an aggregate
// "some/all" summary. Mirrors internal/app/releasesetcommit/execute_test.go's
// own real-stack fixture technique (createTestGitRepository, gitworktree.New/
// Provision) — reimplemented locally here since that file's own unexported
// helpers belong to a different package.

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/gitworktree"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/releasesetcommit"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

func createTestGitRepository(t *testing.T, path string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is required for this test: %v", err)
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	runTestGitCommand(t, path, "init", "--initial-branch=main")
	runTestGitCommand(t, path, "config", "user.name", "Agent Kit Test")
	runTestGitCommand(t, path, "config", "user.email", "agent-kit@example.invalid")
	runTestGitCommand(t, path, "config", "core.autocrlf", "false")
	writeTestFile(t, filepath.Join(path, "service.txt"), "base\n")
	runTestGitCommand(t, path, "add", "-A")
	runTestGitCommand(t, path, "commit", "-m", "initial fixture")
	return strings.TrimSpace(runTestGitCommand(t, path, "rev-parse", "HEAD"))
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func runTestGitCommand(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	args := arguments
	if directory != "" {
		args = append([]string{"-C", directory}, arguments...)
	}
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(output)
}

// TestLocalCommitStatus_PartialAcrossTwoRepositories_ReportsEachEntryAccurately
// is V6-10F's own "partial" Verify-line scenario: a ReleaseSet with two
// repository entries, one COMMITTED (a real local commit actually ran) and
// one still REQUESTED — the per-operation GET status route must report
// each one's own real, distinct state, never conflate or aggregate them.
func TestLocalCommitStatus_PartialAcrossTwoRepositories_ReportsEachEntryAccurately(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	root := t.TempDir()

	// --- repo-a: a real, temporary Git repository + a real workspace ---
	repositoryPath := filepath.Join(root, "source")
	baseRevision := createTestGitRepository(t, repositoryPath)

	provider, err := gitworktree.New(gitworktree.Config{Root: filepath.Join(root, "workspaces")})
	if err != nil {
		t.Fatalf("gitworktree.New: %v", err)
	}
	handle, err := provider.Provision(ctx, ports.ProvisionSpec{
		RepositoryID: project.RepositoryID("repo-a"), LocalRepository: repositoryPath, BaseRef: baseRevision,
		FamilyID: workdomain.TaskFamilyID("family-1"), WorkspaceSetID: workspace.WorkspaceSetID("set-1"), Generation: 1,
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	workspacePath, err := provider.WorkingDirectory(ctx, handle)
	if err != nil {
		t.Fatalf("WorkingDirectory: %v", err)
	}
	writeTestFile(t, filepath.Join(workspacePath, "service.txt"), "changed\n")

	env.seedFamily(t, "project-1", "family-1")
	if err := sqlite.SeedFixtureRepositoryWorkspaceWithLocator(ctx, env.store, "project-1", "family-1", "set-1", "repo-a", "rw-a", handle.String()); err != nil {
		t.Fatalf("SeedFixtureRepositoryWorkspaceWithLocator: %v", err)
	}
	// repo-b: a second repository under the SAME family/WorkspaceSet, with
	// a plain, non-resolvable placeholder locator — its own local-commit
	// operation is requested below but deliberately never executed.
	if err := sqlite.SeedFixtureAdditionalRepositoryWorkspace(ctx, env.store, "project-1", "family-1", "set-1", "repo-b", "rw-b", "opaque:repo-b"); err != nil {
		t.Fatalf("SeedFixtureAdditionalRepositoryWorkspace: %v", err)
	}

	// One ReleaseSet, two entries.
	createResp := env.do(t, http.MethodPost, "/projects/project-1/task-families/family-1/release-sets", "idem-partial-create", "", map[string]any{
		"repositories": []any{
			repositoryReleaseJSON("repo-a", baseRevision, "placeholder-result-a", "PASS"),
			repositoryReleaseJSON("repo-b", "base-rev", "placeholder-result-b", "PASS"),
		},
	})
	if createResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(createResp.Body)
		t.Fatalf("create release set status = %d, want 201, body=%s", createResp.StatusCode, body)
	}
	var releaseSet workapp.ReleaseSetResult
	decodeInto(t, createResp, &releaseSet)

	// Request BOTH local commits over the real HTTP route.
	requestA := env.do(t, http.MethodPost, "/projects/project-1/release-sets/"+releaseSet.ReleaseSetID+"/local-commits", "idem-partial-lc-a", "", map[string]any{
		"expectedReleaseSetVersion": 1, "repositoryWorkspaceId": "rw-a", "expectedWorkspaceVersion": 1,
		"message": "commit repo-a's own result", "authorName": "Release Bot", "authorEmail": "release-bot@example.invalid",
	})
	if requestA.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(requestA.Body)
		t.Fatalf("request local commit (repo-a) status = %d, want 202, body=%s", requestA.StatusCode, body)
	}
	var acceptedA releasesetcommit.RequestReleaseSetLocalCommitResult
	decodeInto(t, requestA, &acceptedA)

	requestB := env.do(t, http.MethodPost, "/projects/project-1/release-sets/"+releaseSet.ReleaseSetID+"/local-commits", "idem-partial-lc-b", "", map[string]any{
		"expectedReleaseSetVersion": 1, "repositoryWorkspaceId": "rw-b", "expectedWorkspaceVersion": 1,
		"message": "commit repo-b's own result", "authorName": "Release Bot", "authorEmail": "release-bot@example.invalid",
	})
	if requestB.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(requestB.Body)
		t.Fatalf("request local commit (repo-b) status = %d, want 202, body=%s", requestB.StatusCode, body)
	}
	var acceptedB releasesetcommit.RequestReleaseSetLocalCommitResult
	decodeInto(t, requestB, &acceptedB)

	// Drive the REAL worker for repo-a's own operation only — outside the
	// HTTP layer entirely, exactly like a real durable-job worker process
	// would (this test file, never releaseset's own package, is allowed to
	// reach releasesetcommit.ExecuteReleaseSetLocalCommit/gitworktree
	// directly).
	job, _, err := env.store.ClaimJob(ctx, "worker-1", 10*time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob: %v", err)
	}
	deps := releasesetcommit.ExecuteReleaseSetLocalCommitDeps{
		UnitOfWork: env.uow, IDs: idsource.NewSequential("worker"), WriteLeases: env.store,
		MarkerReader: provider, Creator: provider, Lifecycle: env.store, WriteLeaseTTL: 10 * time.Minute,
	}
	if err := releasesetcommit.ExecuteReleaseSetLocalCommit(ctx, deps, job); err != nil {
		t.Fatalf("ExecuteReleaseSetLocalCommit(repo-a): %v", err)
	}

	// repo-a's own status: COMMITTED, with a real result distinct from base.
	statusAResp := env.do(t, http.MethodGet, "/projects/project-1/release-sets/"+releaseSet.ReleaseSetID+"/local-commits/"+acceptedA.ReleaseSetLocalCommitID, "", "", nil)
	if statusAResp.StatusCode != http.StatusOK {
		t.Fatalf("GET status (repo-a) = %d, want 200", statusAResp.StatusCode)
	}
	var statusA workapp.ReleaseSetLocalCommitStatus
	decodeInto(t, statusAResp, &statusA)
	if statusA.State != "COMMITTED" {
		t.Fatalf("statusA.State = %q, want COMMITTED", statusA.State)
	}
	if statusA.ResultVCSObjectID == "" || statusA.ResultVCSObjectID == baseRevision {
		t.Fatalf("statusA.ResultVCSObjectID = %q, want a new commit distinct from base %q", statusA.ResultVCSObjectID, baseRevision)
	}
	if statusA.ParentVCSObjectID != baseRevision {
		t.Fatalf("statusA.ParentVCSObjectID = %q, want %q", statusA.ParentVCSObjectID, baseRevision)
	}

	// repo-b's own status: still REQUESTED, exactly as it was left — never
	// silently conflated with repo-a's own now-COMMITTED result, and never
	// itself touched by driving repo-a's worker above.
	statusBResp := env.do(t, http.MethodGet, "/projects/project-1/release-sets/"+releaseSet.ReleaseSetID+"/local-commits/"+acceptedB.ReleaseSetLocalCommitID, "", "", nil)
	if statusBResp.StatusCode != http.StatusOK {
		t.Fatalf("GET status (repo-b) = %d, want 200", statusBResp.StatusCode)
	}
	var statusB workapp.ReleaseSetLocalCommitStatus
	decodeInto(t, statusBResp, &statusB)
	if statusB.State != "REQUESTED" {
		t.Fatalf("statusB.State = %q, want still REQUESTED", statusB.State)
	}
	if statusB.ResultVCSObjectID != "" {
		t.Fatalf("statusB.ResultVCSObjectID = %q, want empty — repo-b's own operation never ran", statusB.ResultVCSObjectID)
	}
	if statusB.ReleaseSetLocalCommitID == statusA.ReleaseSetLocalCommitID {
		t.Fatal("statusA/statusB unexpectedly share one ReleaseSetLocalCommitID")
	}
}
