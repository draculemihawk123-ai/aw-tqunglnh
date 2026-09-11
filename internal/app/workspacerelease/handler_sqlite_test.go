package workspacerelease_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
	"github.com/taQuangLing/agent-workflow/internal/app/workspacerelease"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// This file exercises the real stack end to end — sqlite persistence, the
// real internal/adapters/gitworktree.Provider (real git subprocess,
// including a genuine "git worktree remove" on Release), internal/app/
// workerpool.Pool claiming a real durable job — mirroring
// internal/app/workspacereconcile/handler_sqlite_test.go's own identical
// real-stack discipline for the sibling job, and this task's own explicit
// Verify-shaped bar: a real RequestWorkspaceSetRelease -> Handler.Handle
// round trip that genuinely releases a real workspace and flips both the
// RepositoryWorkspace and its owning WorkspaceSet to RELEASED.

func createReleaseFixtureGitRepository(t *testing.T, repositoryPath string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("Git is required for this end-to-end test: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(repositoryPath), 0o755); err != nil {
		t.Fatalf("create repository parent: %v", err)
	}
	runReleaseFixtureGit(t, "", "init", "--initial-branch=main", repositoryPath)
	runReleaseFixtureGit(t, repositoryPath, "config", "user.name", "Agent Kit Test")
	runReleaseFixtureGit(t, repositoryPath, "config", "user.email", "agent-kit@example.invalid")
	if err := os.WriteFile(filepath.Join(repositoryPath, "service.txt"), []byte("v0\n"), 0o600); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	runReleaseFixtureGit(t, repositoryPath, "add", "--", "service.txt")
	runReleaseFixtureGit(t, repositoryPath, "commit", "-m", "initial fixture")
	return repositoryPath
}

func runReleaseFixtureGit(t *testing.T, directory string, arguments ...string) string {
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

func openReleaseHandlerTestStore(t *testing.T, name string) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func mustSeedActiveRepositoryForRelease(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID, localPath string) {
	t.Helper()
	ctx := context.Background()
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(ctx, ports.CreateProjectRequest{ID: projectID, Name: "project " + projectID})
		return err
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	cmd := ports.Command{
		ID: "cmd-register-" + repositoryID, IdempotencyKey: "idem-register-" + repositoryID, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope(projectID), RequestedAt: time.Now().UTC(),
		Type: "RegisterRepository", RequestHash: "hash-register-" + repositoryID,
	}
	if _, err := catalog.RegisterRepository(ctx, uow, ids, cmd, catalog.RegisterRepositoryRequest{
		RepositoryID: repositoryID, ProjectID: projectID, Name: "svc-" + repositoryID,
		RemoteLocator: localPath, DefaultRef: "main",
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

func mustCreateRootWorkItemForRelease(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID string) appwork.CreateRootWorkItemResult {
	t.Helper()
	ctx := context.Background()
	cmd := ports.Command{
		ID: "cmd-root-" + repositoryID, IdempotencyKey: "idem-root-" + repositoryID, Actor: "actor-1", CorrelationID: "corr-1",
		Scope: ports.ProjectScope(projectID), Type: "CreateRootWorkItem", RequestHash: "hash-root-" + repositoryID,
		RequestedAt: time.Now().UTC(),
	}
	result, err := appwork.CreateRootWorkItem(ctx, uow, ids, cmd, appwork.CreateRootWorkItemRequest{
		ProjectID: projectID, Title: "Root task", InitialScope: []appwork.ScopeGrantRequest{{
			RepositoryID: repositoryID, Access: string(workdomain.RepositoryWrite), PathScopes: []string{"**"}, Reason: "root task",
		}},
	})
	if err != nil {
		t.Fatalf("CreateRootWorkItem: %v", err)
	}
	return result
}

// releaseRealFixture bundles one real, on-disk-git-backed RepositoryWorkspace
// at generation 1, READY, plus its owning WorkspaceSet — ready for a
// release scenario to act on, mirroring workspacereconcile_test's own
// realFixture exactly.
type releaseRealFixture struct {
	store          *sqlite.Store
	uow            ports.UnitOfWork
	ids            idsource.Source
	provider       *gitworktree.Provider
	projectID      string
	familyID       string
	workspaceSetID string
	repositoryID   string
	rw             workspace.RepositoryWorkspace
	repoPath       string
}

func newReleaseRealFixture(t *testing.T, dbName string) *releaseRealFixture {
	t.Helper()
	fixtureRoot := t.TempDir()
	repoPath := createReleaseFixtureGitRepository(t, filepath.Join(fixtureRoot, "repo-a"))

	store := openReleaseHandlerTestStore(t, dbName)
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	provider, err := gitworktree.New(gitworktree.Config{Root: filepath.Join(fixtureRoot, "workspaces")})
	if err != nil {
		t.Fatalf("gitworktree.New: %v", err)
	}

	mustSeedActiveRepositoryForRelease(t, uow, ids, "project-1", "repo-a", repoPath)
	root := mustCreateRootWorkItemForRelease(t, uow, ids, "project-1", "repo-a")

	// The WORKSPACE_PROVISION job CreateRootWorkItem itself enqueues is run
	// through the real workerpool (claim -> Handle -> complete), not called
	// directly, so its own durable_jobs row is genuinely marked SUCCEEDED —
	// unlike workspacereconcile_test's identical-looking realFixture (which
	// calls provisionHandler.Handle directly and moves on), this package's
	// own RequestWorkspaceSetRelease eligibility gate checks
	// HasActiveJobForAggregateIDs against the WorkspaceSet's own ID too (not
	// just each RepositoryWorkspace's), so a provisioning job left "active"
	// in the queue would wrongly block every release request below.
	provisionHandler := workspaceprovision.New(uow, ids, provider)
	provisionRegistry := workerpool.NewRegistry()
	provisionRegistry.Register(appwork.WorkspaceProvisionJobKind, provisionHandler)
	provisionPool, err := workerpool.New(store, provisionRegistry, workerpool.Config{
		Concurrency: 1, Owner: "w", LeaseTTL: 5 * time.Second, HeartbeatEvery: time.Second,
		PollInterval: 20 * time.Millisecond, ShutdownGrace: 2 * time.Second, RecoveryInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("workerpool.New (provision): %v", err)
	}
	provisionJobID := ports.JobID(root.ProvisionedRepositories[0].ProvisionJobID)
	provisionCtx, cancelProvision := context.WithTimeout(context.Background(), 10*time.Second)
	provisionRunErr := make(chan error, 1)
	go func() { provisionRunErr <- provisionPool.Run(provisionCtx) }()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		state, err := store.LoadDurableJobState(context.Background(), provisionJobID)
		if err == nil && (state == ports.JobSucceeded || state == ports.JobFailed || state == ports.JobDead) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancelProvision()
	<-provisionRunErr
	if state, err := store.LoadDurableJobState(context.Background(), provisionJobID); err != nil || state != ports.JobSucceeded {
		t.Fatalf("WORKSPACE_PROVISION job state = %v (err=%v), want SUCCEEDED", state, err)
	}

	var rw workspace.RepositoryWorkspace
	if err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		rw, err = tx.Work().GetRepositoryWorkspace(context.Background(), root.WorkspaceSetID, "repo-a", 1)
		return err
	}); err != nil {
		t.Fatalf("read provisioned repository workspace: %v", err)
	}
	if rw.State != workspace.RepositoryWorkspaceReady {
		t.Fatalf("provisioned repository workspace state = %s, want READY", rw.State)
	}

	return &releaseRealFixture{
		store: store, uow: uow, ids: ids, provider: provider,
		projectID: "project-1", familyID: root.FamilyID, workspaceSetID: root.WorkspaceSetID,
		repositoryID: "repo-a", rw: rw, repoPath: repoPath,
	}
}

func (f *releaseRealFixture) workingDirectory(t *testing.T) string {
	t.Helper()
	handle, err := ports.NewWorkspaceHandle(f.rw.Locator)
	if err != nil {
		t.Fatalf("NewWorkspaceHandle: %v", err)
	}
	dir, err := f.provider.WorkingDirectory(context.Background(), handle)
	if err != nil {
		t.Fatalf("WorkingDirectory: %v", err)
	}
	return dir
}

func (f *releaseRealFixture) reloadRepositoryWorkspace(t *testing.T) workspace.RepositoryWorkspace {
	t.Helper()
	var rw workspace.RepositoryWorkspace
	if err := f.uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		rw, err = tx.Work().GetRepositoryWorkspace(context.Background(), f.workspaceSetID, f.repositoryID, f.rw.Generation)
		return err
	}); err != nil {
		t.Fatalf("reload repository workspace: %v", err)
	}
	return rw
}

func (f *releaseRealFixture) reloadWorkspaceSet(t *testing.T) workspace.WorkspaceSet {
	t.Helper()
	var set workspace.WorkspaceSet
	if err := f.uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		set, err = tx.Work().GetWorkspaceSetByFamilyID(context.Background(), f.familyID)
		return err
	}); err != nil {
		t.Fatalf("reload workspace set: %v", err)
	}
	return set
}

// requestAndRunRelease runs the full public/internal pipeline for f's own
// current WorkspaceSet: RequestWorkspaceSetRelease (public command,
// enqueues the job) followed by Handler.Handle (internal job handler, real
// Provider.Release and real Lifecycle mutation) against the exact job
// RequestWorkspaceSetRelease enqueued.
func (f *releaseRealFixture) requestAndRunRelease(t *testing.T, idempotencyKey string, expectedVersion uint64) error {
	t.Helper()
	ctx := context.Background()
	cmd := ports.Command{
		ID: "cmd-" + idempotencyKey, IdempotencyKey: idempotencyKey, Actor: "operator-1", CorrelationID: "corr-1",
		Scope: ports.ProjectScope(f.projectID), ExpectedVersion: expectedVersion, RequestedAt: time.Now().UTC(),
		Type: "RequestWorkspaceSetRelease", RequestHash: "hash-" + idempotencyKey,
	}
	result, err := workspacerelease.RequestWorkspaceSetRelease(ctx, f.uow, f.ids, authorizedFake(), cmd, workspacerelease.RequestWorkspaceSetReleaseRequest{
		FamilyID: f.familyID, ProjectID: f.projectID,
	})
	if err != nil {
		return err
	}

	releaseHandler := workspacerelease.NewHandler(f.uow, f.ids, f.provider, f.store)
	registry := workerpool.NewRegistry()
	registry.Register(workspacerelease.WorkspaceSetReleaseJobKind, releaseHandler)
	pool, err := workerpool.New(f.store, registry, workerpool.Config{
		Concurrency: 1, Owner: "w", LeaseTTL: 5 * time.Second, HeartbeatEvery: time.Second,
		PollInterval: 20 * time.Millisecond, ShutdownGrace: 2 * time.Second, RecoveryInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("workerpool.New: %v", err)
	}
	poolCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- pool.Run(poolCtx) }()

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		state, err := f.store.LoadDurableJobState(ctx, ports.JobID(result.ReleaseJobID))
		if err == nil && (state == ports.JobSucceeded || state == ports.JobFailed || state == ports.JobDead) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-runErr

	state, err := f.store.LoadDurableJobState(ctx, ports.JobID(result.ReleaseJobID))
	if err != nil {
		t.Fatalf("LoadDurableJobState: %v", err)
	}
	if state != ports.JobSucceeded {
		t.Fatalf("WORKSPACE_SET_RELEASE job state = %s, want SUCCEEDED", state)
	}
	return nil
}

// TestEndToEnd_Release_ReadyWorkspaceSet_ReleasesRepositoryAndSet is the
// happy path: a real, genuinely-provisioned RepositoryWorkspace is released
// through the whole public/internal pipeline — the real on-disk git
// worktree is genuinely removed (ports.WorkspaceProvider.Release's own
// "git worktree remove"), the RepositoryWorkspace row flips to RELEASED,
// and the owning WorkspaceSet itself flips READY -> RELEASED.
func TestEndToEnd_Release_ReadyWorkspaceSet_ReleasesRepositoryAndSet(t *testing.T) {
	f := newReleaseRealFixture(t, "e2e-release-happy.db")
	dir := f.workingDirectory(t)

	if err := f.requestAndRunRelease(t, "idem-release-1", f.reloadWorkspaceSet(t).Version); err != nil {
		t.Fatalf("requestAndRunRelease: %v", err)
	}

	gotRW := f.reloadRepositoryWorkspace(t)
	if gotRW.State != workspace.RepositoryWorkspaceReleased {
		t.Fatalf("repository workspace after release = %+v, want RELEASED", gotRW)
	}
	gotSet := f.reloadWorkspaceSet(t)
	if gotSet.State != workspace.WorkspaceSetReleased {
		t.Fatalf("workspace set after release = %+v, want RELEASED", gotSet)
	}

	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("real worktree directory %s still exists after release (err=%v), want removed", dir, err)
	}
}

// TestEndToEnd_Release_IdempotentReplay_SecondRunIsNoOp is this task's own
// "idempotent cleanup" bar: replaying ExecuteWorkspaceSetRelease against
// the identical request after it already fully committed (mirroring a
// crash-recovery reclaim of the exact same job) must be a safe no-op —
// never a second Provider.Release call, never an error.
func TestEndToEnd_Release_IdempotentReplay_SecondRunIsNoOp(t *testing.T) {
	f := newReleaseRealFixture(t, "e2e-release-idempotent.db")

	if err := f.requestAndRunRelease(t, "idem-release-2", f.reloadWorkspaceSet(t).Version); err != nil {
		t.Fatalf("first requestAndRunRelease: %v", err)
	}
	afterFirst := f.reloadWorkspaceSet(t)
	if afterFirst.State != workspace.WorkspaceSetReleased {
		t.Fatalf("workspace set after first release = %+v, want RELEASED", afterFirst)
	}

	deps := workspacerelease.ExecuteWorkspaceSetReleaseDeps{UnitOfWork: f.uow, IDs: f.ids, Provider: f.provider, Lifecycle: f.store}
	request := workspacerelease.ExecuteWorkspaceSetReleaseRequest{
		WorkspaceSetID: f.workspaceSetID, FamilyID: f.familyID, ProjectID: f.projectID,
		ExpectedVersion: f.rw.Version, CorrelationID: "corr-replay",
		RepositoryWorkspaces: nil,
	}
	if err := workspacerelease.ExecuteWorkspaceSetRelease(context.Background(), deps, request); err != nil {
		t.Fatalf("replay ExecuteWorkspaceSetRelease: %v", err)
	}

	afterSecond := f.reloadWorkspaceSet(t)
	// BaseRevisionSet is a *workspace.RevisionSet — compared by field, not
	// by whole-struct ==, since two freshly-loaded pointers to equal
	// content are never the same pointer.
	if afterSecond.ID != afterFirst.ID || afterSecond.State != afterFirst.State || afterSecond.Version != afterFirst.Version {
		t.Fatalf("workspace set changed across the idempotent replay: first=%+v second=%+v", afterFirst, afterSecond)
	}
}

// TestEndToEnd_Release_QuarantinedMidFlight_RefusesAndPreservesEvidence
// simulates a RepositoryWorkspace becoming QUARANTINED (V3-10, via the
// real ports.WorkspaceLifecycle.QuarantineRepositoryWorkspace) after
// RequestWorkspaceSetRelease already captured its payload but before
// Handler.Handle runs — this task's own "không release... quarantined
// workspace" bar. The whole release must be refused, and the
// RepositoryWorkspace's own QUARANTINED state (the evidence) must be left
// untouched.
func TestEndToEnd_Release_QuarantinedMidFlight_RefusesAndPreservesEvidence(t *testing.T) {
	f := newReleaseRealFixture(t, "e2e-release-quarantined.db")
	ctx := context.Background()

	setBeforeQuarantine := f.reloadWorkspaceSet(t)
	cmd := ports.Command{
		ID: "cmd-idem-release-q", IdempotencyKey: "idem-release-q", Actor: "operator-1", CorrelationID: "corr-1",
		Scope: ports.ProjectScope(f.projectID), ExpectedVersion: setBeforeQuarantine.Version, RequestedAt: time.Now().UTC(),
		Type: "RequestWorkspaceSetRelease", RequestHash: "hash-idem-release-q",
	}
	if _, err := workspacerelease.RequestWorkspaceSetRelease(ctx, f.uow, f.ids, authorizedFake(), cmd, workspacerelease.RequestWorkspaceSetReleaseRequest{
		FamilyID: f.familyID, ProjectID: f.projectID,
	}); err != nil {
		t.Fatalf("RequestWorkspaceSetRelease: %v", err)
	}

	if err := f.store.QuarantineRepositoryWorkspace(ctx, ports.QuarantineRepositoryWorkspaceUpdate{
		RepositoryWorkspaceID: f.rw.ID, ExpectedVersion: f.rw.Version, Reason: "SIMULATED_MID_FLIGHT_QUARANTINE",
		EventID: "event-mid-flight-quarantine", CorrelationID: "corr-quarantine", OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("QuarantineRepositoryWorkspace (simulated precondition): %v", err)
	}
	quarantined := f.reloadRepositoryWorkspace(t)
	if quarantined.State != workspace.RepositoryWorkspaceQuarantined {
		t.Fatalf("repository workspace after simulated quarantine = %+v, want QUARANTINED", quarantined)
	}

	// Handler.Handle is exercised directly against a hand-built job payload
	// matching releaseJobPayload's own (unexported) wire shape exactly:
	// releaseRepositoryWorkspaceEntry is unexported and this test file lives
	// in workspacerelease_test (external), the same boundary
	// ExecuteWorkspaceSetReleaseRequest.RepositoryWorkspaces itself sits
	// behind.
	handler := workspacerelease.NewHandler(f.uow, f.ids, f.provider, f.store)
	payload, err := json.Marshal(struct {
		WorkspaceSetID       string `json:"workspaceSetId"`
		FamilyID             string `json:"familyId"`
		ProjectID            string `json:"projectId"`
		ExpectedVersion      uint64 `json:"expectedVersion"`
		RepositoryWorkspaces []struct {
			RepositoryWorkspaceID string `json:"repositoryWorkspaceId"`
			RepositoryID          string `json:"repositoryId"`
			Generation            uint64 `json:"generation"`
		} `json:"repositoryWorkspaces"`
	}{
		WorkspaceSetID: f.workspaceSetID, FamilyID: f.familyID, ProjectID: f.projectID,
		ExpectedVersion: setBeforeQuarantine.Version,
		RepositoryWorkspaces: []struct {
			RepositoryWorkspaceID string `json:"repositoryWorkspaceId"`
			RepositoryID          string `json:"repositoryId"`
			Generation            uint64 `json:"generation"`
		}{{RepositoryWorkspaceID: string(f.rw.ID), RepositoryID: f.repositoryID, Generation: f.rw.Generation}},
	})
	if err != nil {
		t.Fatalf("marshal release job payload: %v", err)
	}
	job := ports.DurableJob{ID: ports.JobID("job-quarantine-exec"), Kind: workspacerelease.WorkspaceSetReleaseJobKind, Payload: payload}
	if err := handler.Handle(ctx, job); !errors.Is(err, workspacerelease.ErrRepositoryWorkspaceQuarantinedDuringRelease) {
		t.Fatalf("Handler.Handle() error = %v, want ErrRepositoryWorkspaceQuarantinedDuringRelease", err)
	}

	stillQuarantined := f.reloadRepositoryWorkspace(t)
	if stillQuarantined.State != workspace.RepositoryWorkspaceQuarantined || stillQuarantined.Version != quarantined.Version {
		t.Fatalf("repository workspace after refused release = %+v, want unchanged QUARANTINED@%d", stillQuarantined, quarantined.Version)
	}
	gotSet := f.reloadWorkspaceSet(t)
	if gotSet.State == workspace.WorkspaceSetReleased {
		t.Fatal("workspace set reached RELEASED despite a quarantined repository workspace refusing the release")
	}
}
