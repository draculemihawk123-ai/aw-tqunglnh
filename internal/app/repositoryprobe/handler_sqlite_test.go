package repositoryprobe_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/repoprobe"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/repositoryprobe"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

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

func openProbeTestStore(t *testing.T, name string) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func poolConfig(owner string) workerpool.Config {
	return workerpool.Config{
		Concurrency: 1, Owner: owner, LeaseTTL: 500 * time.Millisecond,
		HeartbeatEvery: 100 * time.Millisecond, PollInterval: 20 * time.Millisecond,
		ShutdownGrace: 2 * time.Second, RecoveryInterval: 200 * time.Millisecond,
	}
}

// waitForRepositoryStatus polls the Repository through the same
// sqlite.NewUnitOfWork(store) read path every other caller in this file
// uses, until it reaches want or a 5s deadline elapses.
func waitForRepositoryStatus(t *testing.T, store *sqlite.Store, repositoryID string, want project.RepositoryStatus) project.Repository {
	t.Helper()
	uow := sqlite.NewUnitOfWork(store)
	deadline := time.Now().Add(5 * time.Second)
	var last project.Repository
	for time.Now().Before(deadline) {
		var getErr error
		err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
			var err error
			last, err = tx.Catalog().GetRepository(context.Background(), repositoryID)
			return err
		})
		getErr = err
		if getErr == nil && last.Status == want {
			return last
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("repository %s did not reach status %s within the deadline; last observed = %+v", repositoryID, want, last)
	return project.Repository{}
}

func registerRealRepository(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID, localPath, defaultRef string) string {
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
	result, err := catalog.RegisterRepository(ctx, uow, ids, cmd, catalog.RegisterRepositoryRequest{
		RepositoryID: repositoryID, ProjectID: projectID, Name: "svc-" + repositoryID,
		RemoteLocator: localPath, DefaultRef: defaultRef,
	})
	if err != nil {
		t.Fatalf("RegisterRepository: %v", err)
	}
	return result.ProbeJobID
}

// TestEndToEnd_ValidRepository_RegistersProbesAndBecomesActive exercises
// the full real stack -- sqlite persistence, the real repoprobe.Prober
// (real git subprocess), workerpool.Pool claiming the real durable job --
// for the success path: a valid, clean local Git repository is registered
// (REGISTERING), the pool claims and runs the REPOSITORY_PROBE job, and
// the Repository ends ACTIVE with a real resolved base commit and exactly
// one repository_probe_attempts row.
func TestEndToEnd_ValidRepository_RegistersProbesAndBecomesActive(t *testing.T) {
	fixtureRoot := t.TempDir()
	repoPath := createFixtureGitRepository(t, filepath.Join(fixtureRoot, "service"))
	if err := os.MkdirAll(filepath.Join(repoPath, "service-a"), 0o755); err != nil {
		t.Fatalf("mkdir service-a: %v", err)
	}

	store := openProbeTestStore(t, "e2e-valid.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	prober, err := repoprobe.New(repoprobe.Config{})
	if err != nil {
		t.Fatalf("repoprobe.New: %v", err)
	}
	handler := repositoryprobe.New(uow, ids, prober)
	registry := workerpool.NewRegistry()
	registry.Register(catalog.RepositoryProbeJobKind, handler)
	pool, err := workerpool.New(store, registry, poolConfig("w"))
	if err != nil {
		t.Fatalf("workerpool.New: %v", err)
	}

	registerRealRepository(t, uow, ids, "project-1", "repo-1", repoPath, "main")

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- pool.Run(ctx) }()

	repo := waitForRepositoryStatus(t, store, "repo-1", project.RepositoryActive)
	cancel()
	if err := <-runErr; err != nil {
		t.Fatalf("pool.Run: %v", err)
	}

	if repo.LastProbeErrorCode != nil {
		t.Fatalf("LastProbeErrorCode = %v, want nil", repo.LastProbeErrorCode)
	}

	var attempts []ports.RepositoryProbeAttempt
	if err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		attempts, err = tx.Catalog().ListRepositoryProbeAttempts(context.Background(), "repo-1")
		return err
	}); err != nil {
		t.Fatalf("ListRepositoryProbeAttempts: %v", err)
	}
	if len(attempts) != 1 || attempts[0].Result == nil || *attempts[0].Result != project.RepositoryActive {
		t.Fatalf("attempts = %+v, want exactly 1 with Result=ACTIVE", attempts)
	}
	if attempts[0].BaseCommit == nil || *attempts[0].BaseCommit == "" {
		t.Fatal("attempts[0].BaseCommit is empty, want a real resolved commit")
	}

	var components []project.Component
	if err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		component, err := tx.Catalog().GetComponent(context.Background(), "id-2")
		if err != nil {
			return err
		}
		components = append(components, component)
		return nil
	}); err != nil {
		t.Fatalf("GetComponent: %v", err)
	}
	if len(components) != 1 || components[0].Name != "service-a" {
		t.Fatalf("components = %+v, want the discovered service-a directory", components)
	}
}

// TestEndToEnd_InvalidRepository_BecomesBlockedThenRetrySucceeds is the
// full failure-then-typed-retry path: a repository is registered pointing
// at a local_path that is not a Git repository at all, the pool runs it
// to BLOCKED with a real classified error, and RetryRepositoryProbe
// (called only after the underlying problem is actually fixed --
// initializing a real Git repository at that same path) drives it through
// a fresh probe to ACTIVE.
func TestEndToEnd_InvalidRepository_BecomesBlockedThenRetrySucceeds(t *testing.T) {
	fixtureRoot := t.TempDir()
	notARepoPath := filepath.Join(fixtureRoot, "not-a-repo-yet")
	if err := os.MkdirAll(notARepoPath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	store := openProbeTestStore(t, "e2e-invalid-then-retry.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	prober, err := repoprobe.New(repoprobe.Config{})
	if err != nil {
		t.Fatalf("repoprobe.New: %v", err)
	}
	handler := repositoryprobe.New(uow, ids, prober)
	registry := workerpool.NewRegistry()
	registry.Register(catalog.RepositoryProbeJobKind, handler)
	pool, err := workerpool.New(store, registry, poolConfig("w"))
	if err != nil {
		t.Fatalf("workerpool.New: %v", err)
	}

	registerRealRepository(t, uow, ids, "project-1", "repo-1", notARepoPath, "main")

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	runErr := make(chan error, 1)
	go func() { runErr <- pool.Run(ctx) }()
	blocked := waitForRepositoryStatus(t, store, "repo-1", project.RepositoryBlocked)
	cancel()
	if err := <-runErr; err != nil {
		t.Fatalf("pool.Run (first): %v", err)
	}
	if blocked.LastProbeErrorCode == nil || *blocked.LastProbeErrorCode != "INVALID_ARGUMENT" {
		t.Fatalf("LastProbeErrorCode = %v, want INVALID_ARGUMENT", blocked.LastProbeErrorCode)
	}

	// Fix the underlying problem, then retry.
	createFixtureGitRepository(t, notARepoPath)
	retryCmd := ports.Command{
		ID: "cmd-retry-1", IdempotencyKey: "idem-retry-1", Actor: "actor-1", CorrelationID: "corr-1",
		Scope: ports.ProjectScope("project-1"), ExpectedVersion: blocked.Version, RequestedAt: time.Now().UTC(),
		Type: "RetryRepositoryProbe", RequestHash: "hash-retry-1",
	}
	if _, err := catalog.RetryRepositoryProbe(context.Background(), uow, ids, retryCmd, catalog.RetryRepositoryProbeRequest{
		RepositoryID: "repo-1", ProjectID: "project-1",
	}); err != nil {
		t.Fatalf("RetryRepositoryProbe: %v", err)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel2()
	runErr2 := make(chan error, 1)
	go func() { runErr2 <- pool.Run(ctx2) }()
	active := waitForRepositoryStatus(t, store, "repo-1", project.RepositoryActive)
	cancel2()
	if err := <-runErr2; err != nil {
		t.Fatalf("pool.Run (retry): %v", err)
	}
	if active.LastProbeErrorCode != nil {
		t.Fatalf("LastProbeErrorCode after successful retry = %v, want nil", active.LastProbeErrorCode)
	}

	var attempts []ports.RepositoryProbeAttempt
	if err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		attempts, err = tx.Catalog().ListRepositoryProbeAttempts(context.Background(), "repo-1")
		return err
	}); err != nil {
		t.Fatalf("ListRepositoryProbeAttempts: %v", err)
	}
	if len(attempts) != 2 {
		t.Fatalf("attempts = %+v, want exactly 2 (the original BLOCKED attempt, then the retry's own ACTIVE attempt)", attempts)
	}
	if attempts[0].Result == nil || *attempts[0].Result != project.RepositoryBlocked {
		t.Fatalf("attempts[0] = %+v, want Result=BLOCKED", attempts[0])
	}
	if attempts[1].Result == nil || *attempts[1].Result != project.RepositoryActive {
		t.Fatalf("attempts[1] = %+v, want Result=ACTIVE", attempts[1])
	}
	if attempts[0].JobID == attempts[1].JobID {
		t.Fatalf("both attempts share job id %q, want the retry to have minted a fresh job (never resurrect the dead one)", attempts[0].JobID)
	}
}

// TestEndToEnd_TwoPoolsRaceSameProbeJob_NoDuplicateProcessing is the real
// concurrency proof "active probe idempotent" (docs/design/01-system-design.md
// §6.1) demands end to end: two independent Pool instances (mirroring
// internal/app/workerpool's own TestPool_TwoPoolsRaceRecovery_NoDuplicateProcessing)
// race to claim and process the exact same REPOSITORY_PROBE job, sharing
// one real sqlite store, one real repoprobe.Prober and one real git
// fixture. Exactly one must actually run the probe; the Repository must
// end at exactly one applied transition beyond REGISTERING (version 3,
// ACTIVE) and repository_probe_attempts must hold exactly one row for
// that job -- never two, never a wrong version from a double-apply.
func TestEndToEnd_TwoPoolsRaceSameProbeJob_NoDuplicateProcessing(t *testing.T) {
	fixtureRoot := t.TempDir()
	repoPath := createFixtureGitRepository(t, filepath.Join(fixtureRoot, "service"))

	store := openProbeTestStore(t, "e2e-two-pools-race.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	prober, err := repoprobe.New(repoprobe.Config{})
	if err != nil {
		t.Fatalf("repoprobe.New: %v", err)
	}
	var probeCalls atomic.Int32
	countingProber := countingProberWrapper{inner: prober, calls: &probeCalls}
	handler := repositoryprobe.New(uow, ids, countingProber)
	registry := workerpool.NewRegistry()
	registry.Register(catalog.RepositoryProbeJobKind, handler)

	poolA, err := workerpool.New(store, registry, poolConfig("pool-a"))
	if err != nil {
		t.Fatalf("workerpool.New (A): %v", err)
	}
	poolB, err := workerpool.New(store, registry, poolConfig("pool-b"))
	if err != nil {
		t.Fatalf("workerpool.New (B): %v", err)
	}

	jobID := registerRealRepository(t, uow, ids, "project-1", "repo-1", repoPath, "main")
	if jobID == "" {
		t.Fatal("registerRealRepository returned an empty probe job id")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	errA := make(chan error, 1)
	errB := make(chan error, 1)
	go func() { errA <- poolA.Run(ctx) }()
	go func() { errB <- poolB.Run(ctx) }()

	repo := waitForRepositoryStatus(t, store, "repo-1", project.RepositoryActive)
	time.Sleep(150 * time.Millisecond) // give a would-be duplicate claim a real chance to also fire
	cancel()
	if err := <-errA; err != nil {
		t.Fatalf("pool.Run (A): %v", err)
	}
	if err := <-errB; err != nil {
		t.Fatalf("pool.Run (B): %v", err)
	}

	if got := probeCalls.Load(); got != 1 {
		t.Fatalf("probe calls = %d, want exactly 1 (two pools racing must never both process the same job)", got)
	}
	if repo.Version != 3 { // 1 (REGISTERING) -> 2 (PROBING) -> 3 (ACTIVE), exactly once
		t.Fatalf("repo.Version = %d, want exactly 3 (one applied probe, never a double-apply)", repo.Version)
	}

	var attempts []ports.RepositoryProbeAttempt
	if err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		var err error
		attempts, err = tx.Catalog().ListRepositoryProbeAttempts(context.Background(), "repo-1")
		return err
	}); err != nil {
		t.Fatalf("ListRepositoryProbeAttempts: %v", err)
	}
	if len(attempts) != 1 {
		t.Fatalf("attempts = %+v, want exactly 1 row -- the UNIQUE(job_id) idempotency guarantee holding under real concurrent workers", attempts)
	}
	if attempts[0].JobID != jobID {
		t.Fatalf("attempts[0].JobID = %q, want %q", attempts[0].JobID, jobID)
	}
}

// countingProberWrapper counts real Probe calls without changing their
// outcome -- a thin pass-through around the real repoprobe.Prober, so the
// two-pool race test can assert "the real git inspection itself only ran
// once" rather than only inferring it from the final persisted state.
type countingProberWrapper struct {
	inner ports.RepositoryProber
	calls *atomic.Int32
}

func (c countingProberWrapper) Probe(ctx context.Context, localPath, defaultRef string) (ports.RepositoryProbeEvidence, error) {
	c.calls.Add(1)
	return c.inner.Probe(ctx, localPath, defaultRef)
}
