package workspaceprovision_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/gitworktree"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	appwork "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
	"github.com/taQuangLing/agent-workflow/internal/app/workspaceprovision"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// This file exercises the real stack end to end -- sqlite persistence, the
// real internal/adapters/gitworktree.Provider (real git subprocess),
// internal/app/workerpool.Pool claiming real durable jobs -- for this
// task's own explicit Verify line: "multi-repo provision/restart/partial
// failure tests". handler_test.go (fake-based) already covers the
// handler's own business logic in isolation; these three tests instead
// prove the same guarantees survive real sqlite transactions, a real Git
// worktree adapter and (for the restart test) a real process-death/
// recovery cycle.

func createFixtureGitRepository(t *testing.T, repositoryPath string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("Git is required for this end-to-end test: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(repositoryPath), 0o755); err != nil {
		t.Fatalf("create repository parent: %v", err)
	}
	runFixtureGit(t, "", "init", "--initial-branch=main", repositoryPath)
	runFixtureGit(t, repositoryPath, "config", "user.name", "Agent Kit Test")
	runFixtureGit(t, repositoryPath, "config", "user.email", "agent-kit@example.invalid")
	if err := os.WriteFile(filepath.Join(repositoryPath, "service.txt"), []byte("v0\n"), 0o600); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	runFixtureGit(t, repositoryPath, "add", "--", "service.txt")
	runFixtureGit(t, repositoryPath, "commit", "-m", "initial fixture")
	return repositoryPath
}

func runFixtureGit(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	commandArguments := arguments
	if directory != "" {
		commandArguments = append([]string{"-C", directory}, arguments...)
	}
	command := exec.Command("git", commandArguments...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", commandArguments, err, output)
	}
}

func openProvisionTestStore(t *testing.T, name string) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func provisionPoolConfig(owner string) workerpool.Config {
	return workerpool.Config{
		Concurrency: 2, Owner: owner, LeaseTTL: 500 * time.Millisecond,
		HeartbeatEvery: 100 * time.Millisecond, PollInterval: 20 * time.Millisecond,
		ShutdownGrace: 2 * time.Second, RecoveryInterval: 200 * time.Millisecond,
	}
}

// mustSeedProject creates projectID exactly once. Callers seeding more than
// one repository under the same project must call this separately, once,
// before mustSeedActiveRepositorySQLite for each of that project's own
// repositories — unlike the fake package's own CatalogRepository.CreateProject
// (an in-memory map write, silently idempotent), the real sqlite adapter's
// projects.id is a genuine PRIMARY KEY: a second CreateProject for the same
// ID fails outright, exactly like any other real duplicate insert.
func mustSeedProject(t *testing.T, uow ports.UnitOfWork, projectID string) {
	t.Helper()
	ctx := context.Background()
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(ctx, ports.CreateProjectRequest{ID: projectID, Name: "project " + projectID})
		return err
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
}

// mustSeedActiveRepositorySQLite registers repositoryID (pointed at the
// real local Git path localPath) and drives it all the way to ACTIVE by
// direct CAS transitions, bypassing the real V3-02 probe worker entirely
// (out of this package's own scope, exactly like handler_test.go's own
// fake-backed mustSeedActiveRepository) so
// internal/app/work.CreateRootWorkItem's own same-project/ACTIVE-repository
// validation passes. Callers must seed the owning project first via
// mustSeedProject.
func mustSeedActiveRepositorySQLite(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID, localPath, defaultRef string) {
	t.Helper()
	ctx := context.Background()
	cmd := ports.Command{
		ID: "cmd-register-" + repositoryID, IdempotencyKey: "idem-register-" + repositoryID, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope(projectID), RequestedAt: time.Now().UTC(),
		Type: "RegisterRepository", RequestHash: "hash-register-" + repositoryID,
	}
	if _, err := catalog.RegisterRepository(ctx, uow, ids, cmd, catalog.RegisterRepositoryRequest{
		RepositoryID: repositoryID, ProjectID: projectID, Name: "svc-" + repositoryID,
		RemoteLocator: localPath, DefaultRef: defaultRef,
	}); err != nil {
		t.Fatalf("RegisterRepository: %v", err)
	}
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		probing, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: repositoryID, ExpectedStatus: project.RepositoryRegistering, ExpectedVersion: 1,
			NextStatus: project.RepositoryProbing,
		})
		if err != nil {
			return err
		}
		_, err = tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: repositoryID, ExpectedStatus: project.RepositoryProbing, ExpectedVersion: probing.Version,
			NextStatus: project.RepositoryActive,
		})
		return err
	}); err != nil {
		t.Fatalf("drive repository %s to ACTIVE: %v", repositoryID, err)
	}
}

func mustCreateRootWorkItemSQLite(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID string, repositoryIDs ...string) appwork.CreateRootWorkItemResult {
	t.Helper()
	ctx := context.Background()
	grants := make([]appwork.ScopeGrantRequest, len(repositoryIDs))
	for i, repositoryID := range repositoryIDs {
		grants[i] = appwork.ScopeGrantRequest{
			RepositoryID: repositoryID, Access: string(workdomain.RepositoryWrite),
			PathScopes: []string{"**"}, Reason: "root task",
		}
	}
	cmd := ports.Command{
		ID: "cmd-root-1", IdempotencyKey: "idem-root-1", Actor: "actor-1", CorrelationID: "corr-1",
		Scope: ports.ProjectScope(projectID), Type: "CreateRootWorkItem", RequestHash: "hash-root-1",
		RequestedAt: time.Now().UTC(),
	}
	result, err := appwork.CreateRootWorkItem(ctx, uow, ids, cmd, appwork.CreateRootWorkItemRequest{
		ProjectID: projectID, Title: "Root task", InitialScope: grants,
	})
	if err != nil {
		t.Fatalf("CreateRootWorkItem: %v", err)
	}
	return result
}

// waitForWorkspaceSetState polls store through the same
// sqlite.NewUnitOfWork(store) read path every other caller in this file
// uses, until familyID's own WorkspaceSet reaches want or an 8s deadline
// elapses.
func waitForWorkspaceSetState(t *testing.T, store *sqlite.Store, familyID string, want workspace.WorkspaceSetState) workspace.WorkspaceSet {
	t.Helper()
	uow := sqlite.NewUnitOfWork(store)
	deadline := time.Now().Add(8 * time.Second)
	var last workspace.WorkspaceSet
	for time.Now().Before(deadline) {
		err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
			var err error
			last, err = tx.Work().GetWorkspaceSetByFamilyID(context.Background(), familyID)
			return err
		})
		if err == nil && last.State == want {
			return last
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("workspace set for family %s did not reach state %s within the deadline; last observed = %+v", familyID, want, last)
	return workspace.WorkspaceSet{}
}

// waitForRepositoryWorkspaceTerminal polls until (workspaceSetID,
// repositoryID)'s own generation-1 RepositoryWorkspace reaches READY or
// FAILED, or an 8s deadline elapses. A WorkspaceSet can already flip to
// BLOCKED the moment one required repository's own job finishes FAILED,
// even while a sibling repository's own job is still in flight (this
// package's own aggregateWorkspaceSet only requires anyFailed, never every
// required repository's own job to have finished) — a caller that cancels
// the pool's context right after waitForWorkspaceSetState(..., BLOCKED)
// without also waiting for every sibling job to reach its own terminal
// state risks racing workerpool's own ShutdownGrace against a job still
// genuinely in flight, especially on a loaded test machine.
func waitForRepositoryWorkspaceTerminal(t *testing.T, store *sqlite.Store, workspaceSetID, repositoryID string) workspace.RepositoryWorkspace {
	t.Helper()
	uow := sqlite.NewUnitOfWork(store)
	deadline := time.Now().Add(8 * time.Second)
	var last workspace.RepositoryWorkspace
	for time.Now().Before(deadline) {
		var rw workspace.RepositoryWorkspace
		err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
			var err error
			rw, err = tx.Work().GetRepositoryWorkspace(context.Background(), workspaceSetID, repositoryID, 1)
			return err
		})
		if err == nil {
			last = rw
			if rw.State == workspace.RepositoryWorkspaceReady || rw.State == workspace.RepositoryWorkspaceFailed {
				return rw
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("repository workspace %s/%s did not reach a terminal state within the deadline; last observed = %+v",
		workspaceSetID, repositoryID, last)
	return workspace.RepositoryWorkspace{}
}

// TestEndToEnd_MultiRepoProvision_BothReachReadyWithBaseRevisionSet is this
// task's own "multi-repo provision" Verify-line requirement: two real Git
// fixture repositories, both WORKSPACE_PROVISION jobs processed by the
// real handler against real sqlite and a real gitworktree.Provider, the
// WorkspaceSet itself reaching READY only once both RepositoryWorkspace
// rows are READY, with a correct base RevisionSet naming both.
func TestEndToEnd_MultiRepoProvision_BothReachReadyWithBaseRevisionSet(t *testing.T) {
	fixtureRoot := t.TempDir()
	repoAPath := createFixtureGitRepository(t, filepath.Join(fixtureRoot, "repo-a"))
	repoBPath := createFixtureGitRepository(t, filepath.Join(fixtureRoot, "repo-b"))

	store := openProvisionTestStore(t, "e2e-multi-repo.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")

	provider, err := gitworktree.New(gitworktree.Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("gitworktree.New: %v", err)
	}
	handler := workspaceprovision.New(uow, ids, provider)
	registry := workerpool.NewRegistry()
	registry.Register(appwork.WorkspaceProvisionJobKind, handler)
	pool, err := workerpool.New(store, registry, provisionPoolConfig("w"))
	if err != nil {
		t.Fatalf("workerpool.New: %v", err)
	}

	mustSeedProject(t, uow, "project-1")
	mustSeedActiveRepositorySQLite(t, uow, ids, "project-1", "repo-a", repoAPath, "main")
	mustSeedActiveRepositorySQLite(t, uow, ids, "project-1", "repo-b", repoBPath, "main")
	root := mustCreateRootWorkItemSQLite(t, uow, ids, "project-1", "repo-a", "repo-b")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- pool.Run(ctx) }()

	set := waitForWorkspaceSetState(t, store, root.FamilyID, workspace.WorkspaceSetReady)
	cancel()
	if err := <-runErr; err != nil {
		t.Fatalf("pool.Run: %v", err)
	}

	if set.BaseRevisionSet == nil {
		t.Fatal("WorkspaceSet.BaseRevisionSet is nil, want a computed base revision set")
	}
	for _, repositoryID := range []project.RepositoryID{"repo-a", "repo-b"} {
		revision, ok := set.BaseRevisionSet.RevisionFor(repositoryID)
		if !ok || revision.VCSObjectID == "" {
			t.Fatalf("BaseRevisionSet.RevisionFor(%s) = %+v, %v, want a real resolved commit", repositoryID, revision, ok)
		}
	}

	var rwA, rwB workspace.RepositoryWorkspace
	if err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		rwA, err = tx.Work().GetRepositoryWorkspace(context.Background(), root.WorkspaceSetID, "repo-a", 1)
		if err != nil {
			return err
		}
		rwB, err = tx.Work().GetRepositoryWorkspace(context.Background(), root.WorkspaceSetID, "repo-b", 1)
		return err
	}); err != nil {
		t.Fatalf("read repository workspaces: %v", err)
	}
	if rwA.State != workspace.RepositoryWorkspaceReady || rwB.State != workspace.RepositoryWorkspaceReady {
		t.Fatalf("rwA=%+v rwB=%+v, want both READY", rwA, rwB)
	}
	if rwA.Locator == "" || rwB.Locator == "" {
		t.Fatal("expected non-empty opaque locators for both repository workspaces")
	}
	if rwA.BaseRevision == "" || rwB.BaseRevision == "" {
		t.Fatal("expected a real resolved base revision for both repository workspaces")
	}
}

// TestEndToEnd_Restart_RemainingJobCompletesAndSetReachesReady is this
// task's own "restart" Verify-line requirement (judgment call 4): the
// store is killed mid-flight after one of two required repositories'
// own RepositoryWorkspace already reached READY but before the
// WorkspaceSet itself reaches READY -- the other repository's own
// WORKSPACE_PROVISION job is left claimed/leased, exactly like a real
// process crash -- then reopened against the same database file, proving
// the still-pending job is reclaimed and completes, and the WorkspaceSet
// eventually reaches READY.
func TestEndToEnd_Restart_RemainingJobCompletesAndSetReachesReady(t *testing.T) {
	fixtureRoot := t.TempDir()
	repoAPath := createFixtureGitRepository(t, filepath.Join(fixtureRoot, "repo-a"))
	repoBPath := createFixtureGitRepository(t, filepath.Join(fixtureRoot, "repo-b"))

	dbPath := filepath.Join(t.TempDir(), "e2e-restart.db")
	store, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}

	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	provider, err := gitworktree.New(gitworktree.Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("gitworktree.New: %v", err)
	}
	realHandler := workspaceprovision.New(uow, ids, provider)

	mustSeedProject(t, uow, "project-1")
	mustSeedActiveRepositorySQLite(t, uow, ids, "project-1", "repo-a", repoAPath, "main")
	mustSeedActiveRepositorySQLite(t, uow, ids, "project-1", "repo-b", repoBPath, "main")
	root := mustCreateRootWorkItemSQLite(t, uow, ids, "project-1", "repo-a", "repo-b")

	// registry1 lets exactly the FIRST claimed WORKSPACE_PROVISION job run
	// to real completion; every claim after that simulates a crash before
	// doing any real work (the job stays LEASED, its lease still ticking
	// down) -- mirroring internal/integration/foundation_test.go's own
	// "stuck" handler pattern for its identical kill/restart scenario. This
	// makes the test independent of which of the two repositories'
	// own job happens to be claimed first.
	var claims atomic.Int32
	firstDone := make(chan struct{})
	stuck := make(chan struct{}) // never closed: this handler simulates work interrupted by a crash
	registry1 := workerpool.NewRegistry()
	registry1.Register(appwork.WorkspaceProvisionJobKind, workerpool.HandlerFunc(func(ctx context.Context, job ports.DurableJob) error {
		if claims.Add(1) == 1 {
			err := realHandler.Handle(ctx, job)
			close(firstDone)
			return err
		}
		select {
		case <-stuck:
		case <-ctx.Done():
		}
		return ctx.Err()
	}))

	firstPoolLeaseTTL := 300 * time.Millisecond
	pool1, err := workerpool.New(store, registry1, workerpool.Config{
		Concurrency: 2, Owner: "w1", LeaseTTL: firstPoolLeaseTTL, HeartbeatEvery: firstPoolLeaseTTL / 4,
		PollInterval: 20 * time.Millisecond, ShutdownGrace: 0, RecoveryInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("workerpool.New (pool1): %v", err)
	}
	pool1Ctx, cancelPool1 := context.WithCancel(context.Background())
	pool1Done := make(chan error, 1)
	go func() { pool1Done <- pool1.Run(pool1Ctx) }()

	select {
	case <-firstDone:
	case <-time.After(5 * time.Second):
		t.Fatal("first WORKSPACE_PROVISION job was never completed by pool1")
	}
	// Give the second job's own claim a real chance to actually happen
	// (and get stuck) before killing -- otherwise there is nothing
	// orphaned left for the restart to prove it recovers.
	time.Sleep(150 * time.Millisecond)

	// Confirm the exact mid-flight state this task's own restart scenario
	// requires, before killing: one repository's own RepositoryWorkspace
	// already READY, the WorkspaceSet itself still only PROVISIONING.
	var midFlightSet workspace.WorkspaceSet
	if err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		midFlightSet, err = tx.Work().GetWorkspaceSetByFamilyID(context.Background(), root.FamilyID)
		return err
	}); err != nil {
		t.Fatalf("read workspace set before kill: %v", err)
	}
	if midFlightSet.State != workspace.WorkspaceSetProvisioning {
		t.Fatalf("WorkspaceSet.State before kill = %q, want PROVISIONING (mid-flight, one repository still pending)", midFlightSet.State)
	}
	readyBeforeKill := 0
	for _, repositoryID := range []string{"repo-a", "repo-b"} {
		var rw workspace.RepositoryWorkspace
		lookupErr := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
			var err error
			rw, err = tx.Work().GetRepositoryWorkspace(context.Background(), root.WorkspaceSetID, repositoryID, 1)
			return err
		})
		if lookupErr == nil && rw.State == workspace.RepositoryWorkspaceReady {
			readyBeforeKill++
		}
	}
	if readyBeforeKill != 1 {
		t.Fatalf("ready repository workspace count before kill = %d, want exactly 1", readyBeforeKill)
	}

	// --- kill: the process dies mid-job, with no graceful shutdown at all
	// -- cancelPool1 stops the claim loop immediately (ShutdownGrace: 0
	// means Run returns without waiting for the stuck handler), and
	// closing the store simulates the OS reclaiming this "process"'s
	// resources. The second job is left LEASED, its lease still ticking
	// down toward firstPoolLeaseTTL, exactly like a real crash.
	cancelPool1()
	<-pool1Done
	if err := store.Close(); err != nil {
		t.Fatalf("Close (simulated kill): %v", err)
	}
	time.Sleep(2 * firstPoolLeaseTTL) // let the orphaned lease actually expire

	// --- restart: reopen against the same database file, wire the real
	// handler (no stuck-simulation wrapper this time) and let the startup
	// recovery scan reclaim the orphaned job.
	store2, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open (restart): %v", err)
	}
	defer store2.Close()
	uow2 := sqlite.NewUnitOfWork(store2)
	handler2 := workspaceprovision.New(uow2, ids, provider)
	registry2 := workerpool.NewRegistry()
	registry2.Register(appwork.WorkspaceProvisionJobKind, handler2)
	pool2, err := workerpool.New(store2, registry2, provisionPoolConfig("w2"))
	if err != nil {
		t.Fatalf("workerpool.New (pool2): %v", err)
	}
	pool2Ctx, cancelPool2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelPool2()
	pool2Done := make(chan error, 1)
	go func() { pool2Done <- pool2.Run(pool2Ctx) }()

	finalSet := waitForWorkspaceSetState(t, store2, root.FamilyID, workspace.WorkspaceSetReady)
	cancelPool2()
	if err := <-pool2Done; err != nil {
		t.Fatalf("pool2.Run: %v", err)
	}

	if finalSet.BaseRevisionSet == nil {
		t.Fatal("WorkspaceSet.BaseRevisionSet is nil after restart, want a computed base revision set")
	}
	for _, repositoryID := range []project.RepositoryID{"repo-a", "repo-b"} {
		if _, ok := finalSet.BaseRevisionSet.RevisionFor(repositoryID); !ok {
			t.Fatalf("BaseRevisionSet missing revision for %s after restart", repositoryID)
		}
	}
}

// TestEndToEnd_PartialFailure_OneReadyOneFailed_SetBlockedRowsKept is this
// task's own "partial failure" Verify-line requirement (judgment call 5):
// two real repositories in one WorkspaceSet, one provisions successfully
// (READY) and one fails for real (its own local path is not a Git
// repository at all, so the real gitworktree.Provider.Provision call
// itself fails) -- the WorkspaceSet itself must end BLOCKED, never
// silently READY, and the successful repository's own RepositoryWorkspace
// row must not be discarded/rolled back just because its sibling failed.
func TestEndToEnd_PartialFailure_OneReadyOneFailed_SetBlockedRowsKept(t *testing.T) {
	fixtureRoot := t.TempDir()
	repoAPath := createFixtureGitRepository(t, filepath.Join(fixtureRoot, "repo-a"))
	notARepoPath := filepath.Join(fixtureRoot, "not-a-repo")
	if err := os.MkdirAll(notARepoPath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	store := openProvisionTestStore(t, "e2e-partial-failure.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	provider, err := gitworktree.New(gitworktree.Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("gitworktree.New: %v", err)
	}
	handler := workspaceprovision.New(uow, ids, provider)
	registry := workerpool.NewRegistry()
	registry.Register(appwork.WorkspaceProvisionJobKind, handler)
	pool, err := workerpool.New(store, registry, provisionPoolConfig("w"))
	if err != nil {
		t.Fatalf("workerpool.New: %v", err)
	}

	mustSeedProject(t, uow, "project-1")
	mustSeedActiveRepositorySQLite(t, uow, ids, "project-1", "repo-a", repoAPath, "main")
	mustSeedActiveRepositorySQLite(t, uow, ids, "project-1", "repo-bad", notARepoPath, "main")
	root := mustCreateRootWorkItemSQLite(t, uow, ids, "project-1", "repo-a", "repo-bad")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- pool.Run(ctx) }()

	set := waitForWorkspaceSetState(t, store, root.FamilyID, workspace.WorkspaceSetBlocked)
	// The set can already be BLOCKED as soon as repo-bad's own job
	// finishes, even while repo-a's own job is still in flight — wait for
	// both to reach their own terminal state before canceling the pool, so
	// this test never races workerpool's own ShutdownGrace against a job
	// that is still genuinely running (see waitForRepositoryWorkspaceTerminal's
	// own doc comment).
	waitForRepositoryWorkspaceTerminal(t, store, root.WorkspaceSetID, "repo-a")
	waitForRepositoryWorkspaceTerminal(t, store, root.WorkspaceSetID, "repo-bad")
	cancel()
	if err := <-runErr; err != nil {
		t.Fatalf("pool.Run: %v", err)
	}

	if set.BaseRevisionSet != nil {
		t.Fatalf("BaseRevisionSet = %+v, want nil (a BLOCKED set must never get a base revision set)", set.BaseRevisionSet)
	}

	var rwA, rwBad workspace.RepositoryWorkspace
	if err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		rwA, err = tx.Work().GetRepositoryWorkspace(context.Background(), root.WorkspaceSetID, "repo-a", 1)
		if err != nil {
			return err
		}
		rwBad, err = tx.Work().GetRepositoryWorkspace(context.Background(), root.WorkspaceSetID, "repo-bad", 1)
		return err
	}); err != nil {
		t.Fatalf("read repository workspaces: %v", err)
	}
	if rwA.State != workspace.RepositoryWorkspaceReady {
		t.Fatalf("repo-a RepositoryWorkspace.State = %q, want READY (kept despite repo-bad's own failure)", rwA.State)
	}
	if rwA.Locator == "" || rwA.BaseRevision == "" {
		t.Fatal("repo-a's own RepositoryWorkspace is missing its real locator/base revision")
	}
	if rwBad.State != workspace.RepositoryWorkspaceFailed {
		t.Fatalf("repo-bad RepositoryWorkspace.State = %q, want FAILED", rwBad.State)
	}
	if rwBad.LastProvisionErrorCode == nil || *rwBad.LastProvisionErrorCode != "PROVISION_FAILED" {
		t.Fatalf("repo-bad LastProvisionErrorCode = %v, want PROVISION_FAILED", rwBad.LastProvisionErrorCode)
	}
}
