// TestProjectWorkspaceGate is V3-12's own Verify scenario — the closing
// gate for the whole V3 "Project, WorkItem và WorkspaceSet" phase
// (docs/design/05-v3-project-workspace.md V3-12): "project hai repo, two
// root families, children, expansion, parallel leases, restart/release."
// It exercises the real, wired-together system end to end — through the
// real internal/app application commands (CreateRootWorkItem,
// CreateChildWorkItem, RequestScopeExpansion/ApproveScopeExpansion,
// RequestWorkspaceSetRelease), the real internal/app/workspaceprovision
// job handler running under a real internal/app/workerpool.Pool, the
// real internal/adapters/gitworktree.Provider against real temporary Git
// repositories, and V3-09's real, already-merged WriteLease service — the
// same "wire adapters and app-layer code together the way a real binary
// does" discipline internal/integration's own foundation_test.go (V1-12)
// and definitionplane_test.go (V2-12) already established, and the one
// package in this repo explicitly permitted to import both domain/app and
// adapters together (internal/archtest's own domain/app boundary rule
// carves this package out).
//
// V3-12's own "Hoàn thành khi" bar is narrow and explicit: prove the
// exact RevisionSet/provision evidence trace is complete and every
// invariant this whole V3 phase actually built holds — GC-ACC-06 ("Hai
// root task cùng repository nhận hai worktree khác nhau"), GC-ACC-07
// ("Một family scope hai repository tạo WorkspaceSet hai worktree; child
// reuse đúng WorkspaceSet") and GC-ACC-08 ("Hai sibling ghi hai
// repository khác nhau chạy song song; cùng repository bị serialize") —
// while deliberately NOT attempting real completion/evidence/ReleaseSet
// semantics, which this same design doc explicitly defers to V5 ("Hoàn
// thành khi: ... completion/evidence/ReleaseSet authority được để cho
// V5"). The "release" step this test's own name promises is therefore
// V3-11's own already-built eligibility PRIMITIVE (RequestWorkspaceSetRelease
// with a fake ports.ReleaseEligibilityAuthority) — proving eligibility is
// correctly gated and an intent/job is durably recorded — never a real
// physical Git/filesystem release, which has no executor anywhere in this
// codebase yet (V5-14's own future job, per V3-11's own explicit scope
// note).
package integration

import (
	"context"
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

func TestProjectWorkspaceGate(t *testing.T) {
	ctx := context.Background()
	fixtureRoot := t.TempDir()
	repoAPath := createWorkplaneFixtureGitRepository(t, filepath.Join(fixtureRoot, "repo-a"))
	repoBPath := createWorkplaneFixtureGitRepository(t, filepath.Join(fixtureRoot, "repo-b"))

	dbPath := filepath.Join(t.TempDir(), "agentkit.db")
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open (clean start): %v", err)
	}
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")

	provider, err := gitworktree.New(gitworktree.Config{Root: t.TempDir()})
	if err != nil {
		t.Fatalf("gitworktree.New: %v", err)
	}
	// The handler's own ID source is deliberately NOT the test's shared
	// idsource.Sequential above: workerpool.Pool runs this handler from up
	// to Concurrency=3 goroutines at once (this test's whole point, per
	// GC-ACC-08's "hai sibling ghi khác repository chạy song song"), and
	// Sequential's own doc comment says it is "Not safe for concurrent
	// use." idsource.Random{} has no shared mutable state, so it is safe
	// under real concurrent Handle() calls — this test never asserts on an
	// exact minted RepositoryWorkspace ID value, only on identity/
	// inequality between two of them, so losing determinism here costs
	// nothing.
	handler := workspaceprovision.New(uow, idsource.Random{}, provider)
	registry := workerpool.NewRegistry()
	registry.Register(appwork.WorkspaceProvisionJobKind, handler)
	pool, err := workerpool.New(store, registry, workerpool.Config{
		Concurrency: 3, Owner: "w", LeaseTTL: 500 * time.Millisecond,
		HeartbeatEvery: 100 * time.Millisecond, PollInterval: 20 * time.Millisecond,
		ShutdownGrace: 2 * time.Second, RecoveryInterval: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("workerpool.New: %v", err)
	}

	// ---------------------------------------------------------------
	// Clean start: one Project, two repositories (V3-12's own "project
	// hai repo").
	// ---------------------------------------------------------------
	mustWorkplaneSeedProject(t, ctx, uow, "project-1")
	mustWorkplaneSeedActiveRepository(t, ctx, uow, ids, "project-1", "repo-a", repoAPath, "main")
	mustWorkplaneSeedActiveRepository(t, ctx, uow, ids, "project-1", "repo-b", repoBPath, "main")

	poolCtx, cancelPool := context.WithCancel(ctx)
	poolErr := make(chan error, 1)
	go func() { poolErr <- pool.Run(poolCtx) }()

	// ---------------------------------------------------------------
	// "two root families": family-1 scopes repo-a only; family-2 scopes
	// BOTH repositories — both reference repo-a, GC-ACC-06's own setup.
	// ---------------------------------------------------------------
	family1Root := mustCreateRootWorkItem(t, ctx, uow, ids, "project-1", "Root family 1", "repo-a")
	family2Root := mustCreateRootWorkItem(t, ctx, uow, ids, "project-1", "Root family 2", "repo-a", "repo-b")

	set1 := waitForWorkplaneSetState(t, store, family1Root.FamilyID, workspace.WorkspaceSetReady)
	set2 := waitForWorkplaneSetState(t, store, family2Root.FamilyID, workspace.WorkspaceSetReady)

	// WorkspaceSet reaching READY only proves the handler's own
	// business-state transaction committed — workerpool.Pool's own
	// CompleteJob call that marks the durable job row itself SUCCEEDED is
	// a SEPARATE step afterward (pool.go's runJob), which is deliberately
	// best-effort: under lease expiry it silently leaves the job LEASED
	// for another owner to reclaim, rather than failing the pool. That
	// reclaim needs the pool to keep running long enough for the recovery
	// reaper to notice and retry — but this test calls cancelPool() soon
	// after this point, so a job whose CompleteJob attempt lost its 500ms
	// lease (plausible on a loaded CI runner) would stay LEASED forever
	// once the pool stops, and later fail RequestWorkspaceSetRelease's own
	// "no active durable job" check. Waiting for each provisioning job's
	// own terminal state HERE — not just the business state — closes that
	// gap before this test does anything that depends on provisioning
	// being fully, durably done.
	for _, root := range []appwork.CreateRootWorkItemResult{family1Root, family2Root} {
		for _, provisioned := range root.ProvisionedRepositories {
			waitForDurableJobState(t, store, provisioned.ProvisionJobID, ports.JobSucceeded)
		}
	}

	rwFamily1RepoA := mustGetRepositoryWorkspace(t, ctx, uow, family1Root.WorkspaceSetID, "repo-a")
	rwFamily2RepoA := mustGetRepositoryWorkspace(t, ctx, uow, family2Root.WorkspaceSetID, "repo-a")
	rwFamily2RepoB := mustGetRepositoryWorkspace(t, ctx, uow, family2Root.WorkspaceSetID, "repo-b")

	// GC-ACC-06: "Hai root task cùng repository nhận hai worktree khác
	// nhau" — two independent RepositoryWorkspace rows for the SAME
	// repository, never shared across families.
	if rwFamily1RepoA.ID == rwFamily2RepoA.ID {
		t.Fatalf("family-1 and family-2 share the same RepositoryWorkspace %s for repo-a, want two independent worktrees", rwFamily1RepoA.ID)
	}
	if rwFamily1RepoA.Locator == rwFamily2RepoA.Locator {
		t.Fatalf("family-1 and family-2 share the same opaque locator %q for repo-a, want independent worktrees", rwFamily1RepoA.Locator)
	}

	// GC-ACC-07 (first half): "Một family scope hai repository tạo
	// WorkspaceSet hai worktree" — family-2's own WorkspaceSet has
	// exactly two RepositoryWorkspace rows.
	family2Workspaces := mustListWorkspaceSetRepositoryWorkspaces(t, ctx, uow, family2Root.WorkspaceSetID)
	if len(family2Workspaces) != 2 {
		t.Fatalf("family-2 WorkspaceSet has %d RepositoryWorkspace rows, want 2 (repo-a + repo-b)", len(family2Workspaces))
	}
	if set2.BaseRevisionSet == nil {
		t.Fatal("family-2 WorkspaceSet.BaseRevisionSet is nil, want a computed base revision set covering both repositories")
	}
	if _, ok := set2.BaseRevisionSet.RevisionFor("repo-a"); !ok {
		t.Fatal("family-2 base revision set is missing repo-a")
	}
	if _, ok := set2.BaseRevisionSet.RevisionFor("repo-b"); !ok {
		t.Fatal("family-2 base revision set is missing repo-b")
	}

	// ---------------------------------------------------------------
	// "children": a child of family-2, effective scope a subset of
	// family-2's own grant (repo-a only) — GC-ACC-07's second half,
	// "child reuse đúng WorkspaceSet": no new WorkspaceSet, no new
	// provision job.
	// ---------------------------------------------------------------
	jobCountBeforeChild := mustCountDurableJobsByKind(t, store, appwork.WorkspaceProvisionJobKind)
	workspaceSetCountBeforeChild := mustCountWorkspaceSets(t, store)

	child := mustCreateChildWorkItem(t, ctx, uow, ids, family2Root.WorkItemID, "repo-a")

	if got := mustCountWorkspaceSets(t, store); got != workspaceSetCountBeforeChild {
		t.Fatalf("workspace_sets count after creating a child = %d, want unchanged %d (child must reuse the family's own WorkspaceSet)", got, workspaceSetCountBeforeChild)
	}
	if got := mustCountDurableJobsByKind(t, store, appwork.WorkspaceProvisionJobKind); got != jobCountBeforeChild {
		t.Fatalf("%s job count after creating a child = %d, want unchanged %d (a child must never enqueue a new provision job)", appwork.WorkspaceProvisionJobKind, got, jobCountBeforeChild)
	}
	childFamily := mustGetTaskFamily(t, ctx, uow, child.FamilyID)
	if string(childFamily.ID) != family2Root.FamilyID {
		t.Fatalf("child FamilyID = %s, want the parent's own family %s", childFamily.ID, family2Root.FamilyID)
	}

	// ---------------------------------------------------------------
	// "expansion": approve adding repo-b to family-1 (currently repo-a
	// only) — reopens family-1's own WorkspaceSet (V3-08's own fix to
	// V3-06's aggregation) and provisions a THIRD, independent
	// RepositoryWorkspace for repo-b (family-1's own, distinct from
	// family-2's own repo-b row).
	// ---------------------------------------------------------------
	requestResult := mustRequestScopeExpansion(t, ctx, uow, ids, family1Root.FamilyID, "repo-b")
	approveResult := mustApproveScopeExpansion(t, ctx, uow, ids, requestResult.RequestID)
	if approveResult.NewScopeVersion != 2 {
		t.Fatalf("approved scope version = %d, want 2", approveResult.NewScopeVersion)
	}
	if len(approveResult.ProvisionedRepositories) != 1 {
		t.Fatalf("expansion provisioned %d repositories, want exactly 1 (repo-b)", len(approveResult.ProvisionedRepositories))
	}
	for _, provisioned := range approveResult.ProvisionedRepositories {
		waitForDurableJobState(t, store, provisioned.ProvisionJobID, ports.JobSucceeded)
	}

	set1Expanded := waitForWorkplaneSetState(t, store, family1Root.FamilyID, workspace.WorkspaceSetReady)
	if set1Expanded.Version <= set1.Version {
		t.Fatalf("family-1 WorkspaceSet version after expansion = %d, want greater than pre-expansion %d", set1Expanded.Version, set1.Version)
	}
	if _, ok := set1Expanded.BaseRevisionSet.RevisionFor("repo-a"); !ok {
		t.Fatal("family-1's expanded base revision set lost repo-a")
	}
	if _, ok := set1Expanded.BaseRevisionSet.RevisionFor("repo-b"); !ok {
		t.Fatal("family-1's expanded base revision set is missing the newly-added repo-b")
	}

	rwFamily1RepoB := mustGetRepositoryWorkspace(t, ctx, uow, family1Root.WorkspaceSetID, "repo-b")
	if rwFamily1RepoB.ID == rwFamily2RepoB.ID {
		t.Fatalf("family-1's newly-provisioned repo-b workspace %s collides with family-2's own %s, want two independent worktrees", rwFamily1RepoB.ID, rwFamily2RepoB.ID)
	}

	// Provisioning is done for this test — stop the pool cleanly before
	// driving raw ClaimJob/AcquireWriteLeases calls directly against the
	// store below (mirroring internal/adapters/sqlite/scheduling_test.go's
	// own pattern), so nothing races the pool's own claim loop over an
	// unrelated synthetic job kind it has no registered handler for.
	cancelPool()
	if err := <-poolErr; err != nil {
		t.Fatalf("pool.Run: %v", err)
	}

	// ---------------------------------------------------------------
	// "restart" (mirrors V1-12/V2-12's own kill/restart idiom): close
	// the store, reopen against the same database file, prove every
	// WorkspaceSet/RepositoryWorkspace state and RevisionSet survived.
	// ---------------------------------------------------------------
	if err := store.Close(); err != nil {
		t.Fatalf("Close (simulated restart): %v", err)
	}
	store2, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open (restart): %v", err)
	}
	defer store2.Close()
	uow2 := sqlite.NewUnitOfWork(store2)

	reloadedSet1 := mustGetWorkspaceSetByFamilyID(t, ctx, uow2, family1Root.FamilyID)
	if reloadedSet1.State != workspace.WorkspaceSetReady || reloadedSet1.BaseRevisionSet == nil {
		t.Fatalf("family-1 WorkspaceSet after restart = %+v, want READY with a base revision set", reloadedSet1)
	}
	reloadedSet2 := mustGetWorkspaceSetByFamilyID(t, ctx, uow2, family2Root.FamilyID)
	if reloadedSet2.State != workspace.WorkspaceSetReady || reloadedSet2.BaseRevisionSet == nil {
		t.Fatalf("family-2 WorkspaceSet after restart = %+v, want READY with a base revision set", reloadedSet2)
	}
	if reloadedSet1.BaseRevisionSet.ContentHash() != set1Expanded.BaseRevisionSet.ContentHash() {
		t.Fatal("family-1's own base revision set content hash changed across restart")
	}
	if reloadedSet2.BaseRevisionSet.ContentHash() != set2.BaseRevisionSet.ContentHash() {
		t.Fatal("family-2's own base revision set content hash changed across restart")
	}

	// ---------------------------------------------------------------
	// "parallel leases" (GC-ACC-08): two siblings writing to two
	// DIFFERENT repositories (family-1's own repo-a, family-2's own
	// repo-b) acquire WriteLeases simultaneously; a second attempt on
	// the SAME (family-2's repo-b) is serialized/rejected.
	//
	// write_leases.holder_attempt_id is a real foreign key against
	// execution_attempts — a V4/V5-era runtime concept this V3-era
	// codebase has no real production writer for yet (see
	// sqlite.SeedFixtureNodeRunAndAttempt's own doc comment). A valid
	// ExecutionAttemptID here only needs to exist to satisfy that FK; it
	// has no required relationship to which family/repository the lease
	// it later acquires actually targets — so this test seeds its own
	// small, wholly separate, throwaway owner chain
	// (sqlite.SeedFixtureOwners/SeedFixtureExecutionAttempt) rather than
	// reusing family-1/family-2's own real WorkItem rows for it.
	// ---------------------------------------------------------------
	if err := sqlite.SeedFixtureOwners(ctx, store2, "attempt-project", "attempt-family", "attempt-workitem"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	jobLeaseA := mustClaimSyntheticJob(t, ctx, store2, "attempt-project", "attempt-family", "attempt-workitem", "attempt-family1-repo-a", "job-sibling-a", "job-sibling-a-key")
	jobLeaseB := mustClaimSyntheticJob(t, ctx, store2, "attempt-project", "attempt-family", "attempt-workitem", "attempt-family2-repo-b", "job-sibling-b", "job-sibling-b-key")

	grantsA, err := store2.AcquireWriteLeases(ctx, ports.AcquireWriteLeasesRequest{
		JobLease: jobLeaseA, AttemptID: "attempt-family1-repo-a",
		Targets: []ports.WorkspaceLeaseTarget{{
			RepositoryID: "repo-a", RepositoryWorkspaceID: workspace.RepositoryWorkspaceID(rwFamily1RepoA.ID), Generation: 1,
		}},
		TTL: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("AcquireWriteLeases(family-1 repo-a) error = %v", err)
	}
	grantsB, err := store2.AcquireWriteLeases(ctx, ports.AcquireWriteLeasesRequest{
		JobLease: jobLeaseB, AttemptID: "attempt-family2-repo-b",
		Targets: []ports.WorkspaceLeaseTarget{{
			RepositoryID: "repo-b", RepositoryWorkspaceID: workspace.RepositoryWorkspaceID(rwFamily2RepoB.ID), Generation: 1,
		}},
		TTL: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("AcquireWriteLeases(family-2 repo-b) error = %v (siblings on different repositories must run in parallel)", err)
	}
	if err := store2.ValidateWriteLease(ctx, grantsA[0]); err != nil {
		t.Fatalf("ValidateWriteLease(family-1 repo-a) error = %v", err)
	}
	if err := store2.ValidateWriteLease(ctx, grantsB[0]); err != nil {
		t.Fatalf("ValidateWriteLease(family-2 repo-b) error = %v", err)
	}

	jobLeaseC := mustClaimSyntheticJob(t, ctx, store2, "attempt-project", "attempt-family", "attempt-workitem", "attempt-family2-repo-b-rival", "job-sibling-c", "job-sibling-c-key")
	_, err = store2.AcquireWriteLeases(ctx, ports.AcquireWriteLeasesRequest{
		JobLease: jobLeaseC, AttemptID: "attempt-family2-repo-b-rival",
		Targets: []ports.WorkspaceLeaseTarget{{
			RepositoryID: "repo-b", RepositoryWorkspaceID: workspace.RepositoryWorkspaceID(rwFamily2RepoB.ID), Generation: 1,
		}},
		TTL: 10 * time.Second,
	})
	if !errors.Is(err, ports.ErrWriteLeaseConflict) {
		t.Fatalf("rival AcquireWriteLeases(family-2 repo-b) error = %v, want ports.ErrWriteLeaseConflict (same repository must be serialized)", err)
	}

	// ---------------------------------------------------------------
	// "release" (V3-11's own eligibility primitive, fake authority —
	// completion/evidence/real ReleaseSet authority are explicitly V5's
	// job, never this test's own). Reject while family-2 still holds an
	// active lease on repo-b; release it; then the identical request
	// succeeds — writes an intent, enqueues one WORKSPACE_SET_RELEASE
	// job, never touches a real filesystem/Git release (no executor
	// exists anywhere in this codebase to do so).
	// ---------------------------------------------------------------
	family2Fresh := mustGetWorkspaceSetByFamilyID(t, ctx, uow2, family2Root.FamilyID)
	authority := alwaysAuthorizedRelease{}

	_, err = workspacerelease.RequestWorkspaceSetRelease(ctx, uow2, ids, authority,
		workplaneCommand("cmd-release-blocked", "RequestWorkspaceSetRelease", uint64(family2Fresh.Version)),
		workspacerelease.RequestWorkspaceSetReleaseRequest{FamilyID: family2Root.FamilyID, ProjectID: "project-1"},
	)
	if !errors.Is(err, workspacerelease.ErrWorkspaceSetHasActiveWriteLease) {
		t.Fatalf("RequestWorkspaceSetRelease with an active lease still held error = %v, want ErrWorkspaceSetHasActiveWriteLease", err)
	}

	if err := store2.ReleaseWriteLeases(ctx, grantsB); err != nil {
		t.Fatalf("ReleaseWriteLeases(family-2 repo-b) error = %v", err)
	}
	if err := store2.ReleaseWriteLeases(ctx, grantsA); err != nil {
		t.Fatalf("ReleaseWriteLeases(family-1 repo-a) error = %v", err)
	}

	family2AfterRelease := mustGetWorkspaceSetByFamilyID(t, ctx, uow2, family2Root.FamilyID)
	releaseResult, err := workspacerelease.RequestWorkspaceSetRelease(ctx, uow2, ids, authority,
		workplaneCommand("cmd-release-ok", "RequestWorkspaceSetRelease", uint64(family2AfterRelease.Version)),
		workspacerelease.RequestWorkspaceSetReleaseRequest{FamilyID: family2Root.FamilyID, ProjectID: "project-1"},
	)
	if err != nil {
		t.Fatalf("RequestWorkspaceSetRelease after leases cleared error = %v, want success", err)
	}
	if releaseResult.WorkspaceSetID != family2Root.WorkspaceSetID {
		t.Fatalf("release result workspace set = %s, want %s", releaseResult.WorkspaceSetID, family2Root.WorkspaceSetID)
	}
	releaseJobCount := mustCountDurableJobsByKind(t, store2, workspacerelease.WorkspaceSetReleaseJobKind)
	if releaseJobCount != 1 {
		t.Fatalf("%s job count = %d, want exactly 1", workspacerelease.WorkspaceSetReleaseJobKind, releaseJobCount)
	}
}

// --- fixtures and helpers ---

type alwaysAuthorizedRelease struct{}

func (alwaysAuthorizedRelease) IsReleaseAuthorized(ctx context.Context, familyID string) (bool, string, error) {
	return true, "", nil
}

func workplaneCommand(id, commandType string, expectedVersion uint64) ports.Command {
	return ports.Command{
		ID: id, IdempotencyKey: id, Actor: "operator-gate", CorrelationID: id,
		Scope: ports.InstallationScope(), RequestedAt: time.Now().UTC(),
		Type: commandType, RequestHash: id, ExpectedVersion: expectedVersion,
	}
}

func createWorkplaneFixtureGitRepository(t *testing.T, repositoryPath string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("Git is required for this end-to-end test: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(repositoryPath), 0o755); err != nil {
		t.Fatalf("create repository parent: %v", err)
	}
	runWorkplaneFixtureGit(t, "", "init", "--initial-branch=main", repositoryPath)
	runWorkplaneFixtureGit(t, repositoryPath, "config", "user.name", "Agent Kit Test")
	runWorkplaneFixtureGit(t, repositoryPath, "config", "user.email", "agent-kit@example.invalid")
	if err := os.WriteFile(filepath.Join(repositoryPath, "service.txt"), []byte("v0\n"), 0o600); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	runWorkplaneFixtureGit(t, repositoryPath, "add", "--", "service.txt")
	runWorkplaneFixtureGit(t, repositoryPath, "commit", "-m", "initial fixture")
	return repositoryPath
}

func runWorkplaneFixtureGit(t *testing.T, directory string, arguments ...string) {
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

func mustWorkplaneSeedProject(t *testing.T, ctx context.Context, uow ports.UnitOfWork, projectID string) {
	t.Helper()
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(ctx, ports.CreateProjectRequest{ID: projectID, Name: "project " + projectID})
		return err
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
}

func mustWorkplaneSeedActiveRepository(t *testing.T, ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID, localPath, defaultRef string) {
	t.Helper()
	cmd := ports.Command{
		ID: "cmd-register-" + repositoryID, IdempotencyKey: "idem-register-" + repositoryID, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope(projectID), RequestedAt: time.Now().UTC(),
		Type: "RegisterRepository", RequestHash: "hash-register-" + repositoryID,
	}
	if _, err := catalog.RegisterRepository(ctx, uow, ids, cmd, catalog.RegisterRepositoryRequest{
		RepositoryID: repositoryID, ProjectID: projectID, Name: "svc-" + repositoryID,
		RemoteLocator: localPath, DefaultRef: defaultRef,
	}); err != nil {
		t.Fatalf("RegisterRepository(%s): %v", repositoryID, err)
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

func mustCreateRootWorkItem(t *testing.T, ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, projectID, title string, repositoryIDs ...string) appwork.CreateRootWorkItemResult {
	t.Helper()
	grants := make([]appwork.ScopeGrantRequest, len(repositoryIDs))
	for i, repositoryID := range repositoryIDs {
		grants[i] = appwork.ScopeGrantRequest{
			RepositoryID: repositoryID, Access: string(workdomain.RepositoryWrite),
			PathScopes: []string{"**"}, Reason: "root task",
		}
	}
	cmd := ports.Command{
		ID: "cmd-root-" + title, IdempotencyKey: "idem-root-" + title, Actor: "actor-1", CorrelationID: "corr-1",
		Scope: ports.ProjectScope(projectID), Type: "CreateRootWorkItem", RequestHash: "hash-root-" + title,
		RequestedAt: time.Now().UTC(),
	}
	result, err := appwork.CreateRootWorkItem(ctx, uow, ids, cmd, appwork.CreateRootWorkItemRequest{
		ProjectID: projectID, Title: title, InitialScope: grants,
	})
	if err != nil {
		t.Fatalf("CreateRootWorkItem(%s): %v", title, err)
	}
	return result
}

func mustCreateChildWorkItem(t *testing.T, ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, parentWorkItemID string, repositoryIDs ...string) appwork.CreateChildWorkItemResult {
	t.Helper()
	grants := make([]appwork.ScopeGrantRequest, len(repositoryIDs))
	for i, repositoryID := range repositoryIDs {
		grants[i] = appwork.ScopeGrantRequest{
			RepositoryID: repositoryID, Access: string(workdomain.RepositoryWrite),
			PathScopes: []string{"**"}, Reason: "child task",
		}
	}
	cmd := ports.Command{
		ID: "cmd-child-1", IdempotencyKey: "idem-child-1", Actor: "actor-1", CorrelationID: "corr-1",
		Scope: ports.InstallationScope(), Type: "CreateChildWorkItem", RequestHash: "hash-child-1",
		RequestedAt: time.Now().UTC(),
	}
	result, err := appwork.CreateChildWorkItem(ctx, uow, ids, cmd, appwork.CreateChildWorkItemRequest{
		ParentWorkItemID: parentWorkItemID, Title: "Child task", ParentJoinPolicy: "WAIT_FOR_ALL",
		EffectiveScope: grants,
	})
	if err != nil {
		t.Fatalf("CreateChildWorkItem: %v", err)
	}
	return result
}

func mustRequestScopeExpansion(t *testing.T, ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, familyID string, repositoryIDs ...string) appwork.RequestScopeExpansionResult {
	t.Helper()
	grants := make([]appwork.ScopeGrantRequest, len(repositoryIDs))
	for i, repositoryID := range repositoryIDs {
		grants[i] = appwork.ScopeGrantRequest{
			RepositoryID: repositoryID, Access: string(workdomain.RepositoryWrite),
			PathScopes: []string{"**"}, Reason: "expand scope",
		}
	}
	cmd := ports.Command{
		ID: "cmd-expand-request", IdempotencyKey: "idem-expand-request", Actor: "actor-1", CorrelationID: "corr-1",
		Scope: ports.InstallationScope(), Type: "RequestScopeExpansion", RequestHash: "hash-expand-request",
		RequestedAt: time.Now().UTC(),
	}
	result, err := appwork.RequestScopeExpansion(ctx, uow, ids, cmd, appwork.RequestScopeExpansionRequest{
		FamilyID: familyID, RequestedGrants: grants, Reason: "V3-12 gate expansion",
	})
	if err != nil {
		t.Fatalf("RequestScopeExpansion: %v", err)
	}
	return result
}

func mustApproveScopeExpansion(t *testing.T, ctx context.Context, uow ports.UnitOfWork, ids idsource.Source, requestID string) appwork.ApproveScopeExpansionResult {
	t.Helper()
	cmd := ports.Command{
		ID: "cmd-expand-approve", IdempotencyKey: "idem-expand-approve", Actor: "actor-1", CorrelationID: "corr-1",
		Scope: ports.InstallationScope(), Type: "ApproveScopeExpansion", RequestHash: "hash-expand-approve",
		RequestedAt: time.Now().UTC(),
	}
	result, err := appwork.ApproveScopeExpansion(ctx, uow, ids, cmd, appwork.ApproveScopeExpansionRequest{RequestID: requestID})
	if err != nil {
		t.Fatalf("ApproveScopeExpansion: %v", err)
	}
	return result
}

func mustGetTaskFamily(t *testing.T, ctx context.Context, uow ports.UnitOfWork, familyID string) workdomain.TaskFamily {
	t.Helper()
	var family workdomain.TaskFamily
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		family, err = tx.Work().GetTaskFamily(ctx, familyID)
		return err
	}); err != nil {
		t.Fatalf("GetTaskFamily(%s): %v", familyID, err)
	}
	return family
}

func mustGetWorkspaceSetByFamilyID(t *testing.T, ctx context.Context, uow ports.UnitOfWork, familyID string) workspace.WorkspaceSet {
	t.Helper()
	var set workspace.WorkspaceSet
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		set, err = tx.Work().GetWorkspaceSetByFamilyID(ctx, familyID)
		return err
	}); err != nil {
		t.Fatalf("GetWorkspaceSetByFamilyID(%s): %v", familyID, err)
	}
	return set
}

func mustGetRepositoryWorkspace(t *testing.T, ctx context.Context, uow ports.UnitOfWork, workspaceSetID, repositoryID string) workspace.RepositoryWorkspace {
	t.Helper()
	var rw workspace.RepositoryWorkspace
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		rw, err = tx.Work().GetRepositoryWorkspace(ctx, workspaceSetID, repositoryID, 1)
		return err
	}); err != nil {
		t.Fatalf("GetRepositoryWorkspace(%s, %s): %v", workspaceSetID, repositoryID, err)
	}
	return rw
}

func mustListWorkspaceSetRepositoryWorkspaces(t *testing.T, ctx context.Context, uow ports.UnitOfWork, workspaceSetID string) []workspace.RepositoryWorkspace {
	t.Helper()
	var workspaces []workspace.RepositoryWorkspace
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		workspaces, err = tx.Work().ListWorkspaceSetRepositoryWorkspaces(ctx, workspaceSetID)
		return err
	}); err != nil {
		t.Fatalf("ListWorkspaceSetRepositoryWorkspaces(%s): %v", workspaceSetID, err)
	}
	return workspaces
}

// waitForWorkplaneSetState polls store's own WorkspaceSet for familyID
// until it reaches want or an 8s deadline elapses — mirroring
// internal/app/workspaceprovision's own identical polling helper (a
// package-private test helper this package cannot import directly).
func waitForWorkplaneSetState(t *testing.T, store *sqlite.Store, familyID string, want workspace.WorkspaceSetState) workspace.WorkspaceSet {
	t.Helper()
	uow := sqlite.NewUnitOfWork(store)
	ctx := context.Background()
	deadline := time.Now().Add(8 * time.Second)
	var last workspace.WorkspaceSet
	for time.Now().Before(deadline) {
		err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
			var err error
			last, err = tx.Work().GetWorkspaceSetByFamilyID(ctx, familyID)
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

// waitForDurableJobState polls store for jobID's own State until it reaches
// want or an 8s deadline elapses — waitForWorkplaneSetState's own sibling,
// for a durable job's row rather than a WorkspaceSet's business state (see
// this file's own TestProjectWorkspaceGate comment on why the two are not
// interchangeable proof that provisioning is actually, durably done).
func waitForDurableJobState(t *testing.T, store *sqlite.Store, jobID string, want ports.JobState) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(8 * time.Second)
	var last ports.JobState
	for time.Now().Before(deadline) {
		state, err := store.LoadDurableJobState(ctx, ports.JobID(jobID))
		if err == nil {
			last = state
			if state == want {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("durable job %s did not reach state %s within the deadline; last observed = %s", jobID, want, last)
}

func mustCountWorkspaceSets(t *testing.T, store *sqlite.Store) int {
	t.Helper()
	count, err := store.CountWorkspaceSets(context.Background())
	if err != nil {
		t.Fatalf("CountWorkspaceSets: %v", err)
	}
	return count
}

func mustCountDurableJobsByKind(t *testing.T, store *sqlite.Store, kind string) int {
	t.Helper()
	count, err := store.CountDurableJobsByKind(context.Background(), kind)
	if err != nil {
		t.Fatalf("CountDurableJobsByKind(%s): %v", kind, err)
	}
	return count
}

// mustClaimSyntheticJob seeds a real runtime.ExecutionAttemptID (via
// sqlite.SeedFixtureExecutionAttempt, against an owner chain the caller
// already seeded with sqlite.SeedFixtureOwners) and a matching durable job,
// then claims that job — giving the caller a real ports.JobLease it can
// pass to AcquireWriteLeases alongside the now-valid attemptID, exactly the
// two-step "claim a job, then acquire a write lease under its own JobLease"
// sequence every real caller in this codebase follows.
func mustClaimSyntheticJob(t *testing.T, ctx context.Context, store *sqlite.Store, projectID, familyID, workItemID, attemptID, jobID, idempotencyKey string) ports.JobLease {
	t.Helper()
	if err := sqlite.SeedFixtureExecutionAttempt(ctx, store, projectID, familyID, workItemID, attemptID); err != nil {
		t.Fatalf("SeedFixtureExecutionAttempt(%s): %v", attemptID, err)
	}
	if _, err := store.EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: ports.JobID(jobID), ProjectID: project.ProjectID(projectID), Kind: "EXECUTE_NODE",
		AggregateType: "ExecutionAttempt", AggregateID: attemptID, Payload: []byte(`{}`),
		MaxClaims: 3, IdempotencyKey: idempotencyKey,
	}); err != nil {
		t.Fatalf("EnqueueJob(%s): %v", jobID, err)
	}
	_, lease, err := store.ClaimJob(ctx, "worker-"+jobID, 10*time.Second)
	if err != nil {
		t.Fatalf("ClaimJob(%s): %v", jobID, err)
	}
	return lease
}
