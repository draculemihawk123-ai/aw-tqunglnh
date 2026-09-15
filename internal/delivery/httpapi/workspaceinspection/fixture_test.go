package workspaceinspection_test

// Real HTTP round-trip coverage for V6-10D (docs/design/08-v6-api-projections.md):
// every test in this package drives a REAL httpapi.Server (real TCP loopback
// listener, real middleware chain) backed by a REAL *sqlite.Store and a REAL
// internal/adapters/gitworktree.Provider running against a REAL temporary Git
// repository — never a mock, never a fabricated row, never a mocked git
// command. Mirrors internal/delivery/httpapi/evidence/fixture_test.go's own
// newTestEnv idiom for the server/sqlite half, and
// internal/app/workspaceinspection/queries_test.go's own fixture (real
// gitworktree.Provider + seedOwnershipChain) for the Git/ownership-chain
// half — this file combines both, backed by REAL sqlite persistence (via
// uow.WithSerializedWrite through the real tx.Catalog()/tx.Work() methods)
// rather than queries_test.go's own fake in-memory ports.UnitOfWork, since
// this task's own doctrine requires real command/router/repository code
// paths, never a direct database/state fabrication.

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
	"github.com/taQuangLing/agent-workflow/internal/app/logging"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	appinspection "github.com/taQuangLing/agent-workflow/internal/app/workspaceinspection"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	wsinspectionhttp "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/workspaceinspection"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

const (
	testSessionToken          = "test-workspace-inspection-session-token"
	testProjectID             = "project-1"
	testRepositoryID          = "repo-1"
	testFamilyID              = "family-1"
	testWorkspaceSetID        = "set-1"
	testRepositoryWorkspaceID = "rw-1"
)

func testPrincipal() httpapi.LocalPrincipalSnapshot {
	return httpapi.LocalPrincipalSnapshot{Actor: "local-operator", Roles: []string{"operator"}}
}

// testEnv is one real Server + real UnitOfWork + real gitworktree.Provider
// triple, torn down via t.Cleanup, plus every identifier a test needs to
// build a request URL against the one seeded RepositoryWorkspace.
type testEnv struct {
	base            string
	client          *http.Client
	uow             ports.UnitOfWork
	provider        *gitworktree.Provider
	handle          ports.WorkspaceHandle
	baseRevision    workspace.Revision
	currentRevision workspace.Revision
	sourceRepo      string
}

// newTestEnv provisions a real Git repository + real gitworktree.Provider
// workspace, seeds a real Project->Repository->TaskFamily->WorkspaceSet->
// RepositoryWorkspace ownership chain via real sqlite persistence at
// initialState, and starts a real httpapi.Server with this package's own
// RegisterRoutes wired to a real appinspection.Queries over both.
func newTestEnv(t *testing.T, initialState workspace.RepositoryWorkspaceState) *testEnv {
	t.Helper()
	root := t.TempDir()
	sourceRepo, baseSHA := createGitRepository(t, filepath.Join(root, "source"), "line-1\n")
	provider, err := gitworktree.New(gitworktree.Config{Root: filepath.Join(root, "workspaces")})
	if err != nil {
		t.Fatalf("gitworktree.New: %v", err)
	}

	ctx := context.Background()
	handle, err := provider.Provision(ctx, ports.ProvisionSpec{
		RepositoryID:    project.RepositoryID(testRepositoryID),
		LocalRepository: sourceRepo,
		BaseRef:         baseSHA,
		FamilyID:        work.TaskFamilyID(testFamilyID),
		WorkspaceSetID:  workspace.WorkspaceSetID(testWorkspaceSetID),
		Generation:      1,
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	inspection, err := provider.Inspect(ctx, handle)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}

	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "ws-inspect-http.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	uow := sqlite.NewUnitOfWork(store)

	seedOwnershipChain(t, uow, inspection, handle, initialState)

	queries := appinspection.New(uow, provider)
	reg := httpapi.NewRouteRegistry()
	wsinspectionhttp.RegisterRoutes(reg, wsinspectionhttp.Dependencies{Queries: queries})

	server, err := httpapi.NewServer(httpapi.Config{
		Host: "127.0.0.1", Port: 0, Routes: reg, IDs: idsource.Random{},
		Logger:       logging.New(io.Discard, logging.JSON, redact.NewMatcher()),
		MaxBodyBytes: 16 << 20, Token: testSessionToken, Principal: testPrincipal(),
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve() }()
	t.Cleanup(func() {
		if err := server.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
		if err := <-done; err != nil {
			t.Errorf("Serve returned error after Shutdown: %v", err)
		}
	})

	return &testEnv{
		base: "http://" + server.Addr(), client: &http.Client{Timeout: 30 * time.Second},
		uow: uow, provider: provider, handle: handle,
		baseRevision: inspection.BaseRevision, currentRevision: inspection.CurrentRevision, sourceRepo: sourceRepo,
	}
}

// seedOwnershipChain mirrors
// internal/app/workspaceinspection/queries_test.go's own identical helper
// exactly, except every write goes through uow's own real
// uow.WithSerializedWrite/tx.Catalog()/tx.Work() methods against a real
// *sqlite.Store, never a fake in-memory double.
func seedOwnershipChain(
	t *testing.T,
	uow ports.UnitOfWork,
	inspection ports.WorkspaceInspection,
	handle ports.WorkspaceHandle,
	state workspace.RepositoryWorkspaceState,
) {
	t.Helper()
	ctx := context.Background()
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if _, err := tx.Catalog().CreateProject(ctx, ports.CreateProjectRequest{ID: testProjectID, Name: "Project One"}); err != nil {
			return err
		}
		if _, err := tx.Catalog().RegisterRepository(ctx, ports.RegisterRepositoryRequest{
			ID: testRepositoryID, ProjectID: testProjectID, Name: "repo",
			RemoteLocator: "https://example.invalid/repo.git", DefaultRef: "main",
		}); err != nil {
			return err
		}

		root, err := work.NewRootWorkItem(work.WorkItemID("wi-root"), project.ProjectID(testProjectID), work.TaskFamilyID(testFamilyID), "root")
		if err != nil {
			return err
		}
		family, err := work.NewTaskFamily(work.TaskFamilyID(testFamilyID), root)
		if err != nil {
			return err
		}
		if _, err := tx.Work().CreateTaskFamily(ctx, family); err != nil {
			return err
		}

		set, err := workspace.NewWorkspaceSet(workspace.WorkspaceSetID(testWorkspaceSetID), family)
		if err != nil {
			return err
		}
		if _, err := tx.Work().CreateWorkspaceSet(ctx, set); err != nil {
			return err
		}

		repository, err := tx.Catalog().GetRepository(ctx, testRepositoryID)
		if err != nil {
			return err
		}
		rw, err := workspace.NewRepositoryWorkspace(
			workspace.RepositoryWorkspaceID(testRepositoryWorkspaceID), set, repository, inspection.Generation,
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

// sourcePath builds the getWorkspaceSource request path for env's own
// seeded RepositoryWorkspace, overridable per-field via mutate.
func (e *testEnv) sourceURL(query map[string]string) string {
	return e.base + "/projects/" + testProjectID + "/repository-workspaces/" + testRepositoryWorkspaceID + "/source" + encodeQuery(query)
}

func (e *testEnv) diffURL(query map[string]string) string {
	return e.base + "/projects/" + testProjectID + "/repository-workspaces/" + testRepositoryWorkspaceID + "/diff" + encodeQuery(query)
}

func (e *testEnv) repositoryLogURL(query map[string]string) string {
	return e.base + "/projects/" + testProjectID + "/repository-workspaces/" + testRepositoryWorkspaceID + "/repository-log" + encodeQuery(query)
}

func encodeQuery(query map[string]string) string {
	if len(query) == 0 {
		return ""
	}
	parts := make([]string, 0, len(query))
	for k, v := range query {
		parts = append(parts, k+"="+urlEscape(v))
	}
	return "?" + strings.Join(parts, "&")
}

// urlEscape is a tiny, dependency-free query-value escaper sufficient for
// this package's own test fixtures (object ids, small ASCII paths) — real
// production request-building never happens client-side in this codebase, so
// no shared helper already exists to reuse.
func urlEscape(v string) string {
	replacer := strings.NewReplacer(
		"%", "%25", " ", "%20", "&", "%26", "#", "%23", "+", "%2B",
	)
	return replacer.Replace(v)
}

// do issues a real HTTP GET against e's own real server.
func (e *testEnv) do(t *testing.T, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set(httpapi.SessionTokenHeader, testSessionToken)
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

// scopeQuery returns the {repositoryId, workspaceSetId} query pair every
// route in this package requires — the common baseline every test's own
// per-route query map starts from.
func scopeQuery() map[string]string {
	return map[string]string{"repositoryId": testRepositoryID, "workspaceSetId": testWorkspaceSetID}
}

// --- shared real-git test helpers (no mocked git command anywhere) ---
// Mirrors internal/app/workspaceinspection/queries_test.go's own identical
// helpers exactly.

func createGitRepository(t *testing.T, repositoryPath string, content string) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("Git is required for workspace inspection HTTP tests: %v", err)
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
// queries_test.go's own identical helper.
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
