package releasesetcommit

import (
	"context"
	"errors"
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
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// This file exercises the real stack — a real sqlite.Store and a real
// gitworktree.Provider against a real, temporary Git repository on disk —
// for every crash-safety guarantee this task exists to prove. Nothing here
// is faked or fabricated in the database directly: every commit is a real
// `git commit` this file's own fixture, or ExecuteReleaseSetLocalCommit
// itself, actually runs.

// executeFixture bundles one test's own real Project/TaskFamily/
// RepositoryWorkspace/ReleaseSet, backed by one real Git repository whose
// provisioned workspace's own Locator is shared between sqlite (the
// authoritative RepositoryWorkspace row) and gitworktree.Provider (the real
// filesystem).
type executeFixture struct {
	ctx           context.Context
	store         *sqlite.Store
	uow           ports.UnitOfWork
	ids           idsource.Source
	provider      *gitworktree.Provider
	workspacePath string
	baseRevision  string
	releaseSetID  string
}

func newExecuteFixture(t *testing.T) *executeFixture {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()

	repositoryPath := filepath.Join(root, "source")
	baseRevision := createTestGitRepository(t, repositoryPath)

	provider, err := gitworktree.New(gitworktree.Config{Root: filepath.Join(root, "workspaces")})
	if err != nil {
		t.Fatalf("gitworktree.New: %v", err)
	}
	handle, err := provider.Provision(ctx, ports.ProvisionSpec{
		RepositoryID: project.RepositoryID("repo-1"), LocalRepository: repositoryPath, BaseRef: baseRevision,
		FamilyID: workdomain.TaskFamilyID("family-1"), WorkspaceSetID: workspace.WorkspaceSetID("set-1"), Generation: 1,
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	workspacePath, err := provider.WorkingDirectory(ctx, handle)
	if err != nil {
		t.Fatalf("WorkingDirectory: %v", err)
	}

	store, err := sqlite.Open(ctx, filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	if err := sqlite.SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-root"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	if err := sqlite.SeedFixtureRepositoryWorkspaceWithLocator(
		ctx, store, "project-1", "family-1", "set-1", "repo-1", "rw-1", handle.String(),
	); err != nil {
		t.Fatalf("SeedFixtureRepositoryWorkspaceWithLocator: %v", err)
	}

	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")

	releaseSetResult, err := work.CreateReleaseSet(ctx, uow, ids,
		testCommand("create-release-set", "hash-create-release-set", ports.ProjectScope("project-1"), "CreateReleaseSet", 0),
		work.CreateReleaseSetRequest{
			ProjectID: "project-1", FamilyID: "family-1",
			Repositories: []work.RepositoryReleaseRequest{
				{RepositoryID: "repo-1", BaseVCSObjectID: baseRevision, ResultVCSObjectID: "placeholder-result", Verdict: string(gate.VerdictPass)},
			},
		})
	if err != nil {
		t.Fatalf("CreateReleaseSet: %v", err)
	}

	return &executeFixture{
		ctx: ctx, store: store, uow: uow, ids: ids, provider: provider,
		workspacePath: workspacePath, baseRevision: baseRevision, releaseSetID: releaseSetResult.ReleaseSetID,
	}
}

func testCommand(idempotencyKey, requestHash string, scope ports.CommandScope, cmdType string, expectedVersion uint64) ports.Command {
	return ports.Command{
		ID: "cmd-" + idempotencyKey, IdempotencyKey: idempotencyKey, Actor: "actor-1", CorrelationID: "corr-1",
		Scope: scope, ExpectedVersion: expectedVersion, RequestedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Type: cmdType, RequestHash: requestHash,
	}
}

func (fx *executeFixture) request(t *testing.T, idempotencyKey, requestHash, message string) RequestReleaseSetLocalCommitResult {
	t.Helper()
	result, err := RequestReleaseSetLocalCommit(fx.ctx, fx.uow, fx.ids,
		testCommand(idempotencyKey, requestHash, ports.ProjectScope("project-1"), "RequestReleaseSetLocalCommit", 0),
		RequestReleaseSetLocalCommitRequest{
			ProjectID: "project-1", ReleaseSetID: fx.releaseSetID, ExpectedReleaseSetVersion: 1,
			RepositoryWorkspaceID: "rw-1", ExpectedWorkspaceVersion: 1,
			Message: message, AuthorName: "Release Bot", AuthorEmail: "release-bot@example.invalid",
		})
	if err != nil {
		t.Fatalf("RequestReleaseSetLocalCommit: %v", err)
	}
	return result
}

func (fx *executeFixture) claim(t *testing.T, owner string, ttl time.Duration) ports.DurableJob {
	t.Helper()
	job, _, err := fx.store.ClaimJob(fx.ctx, owner, ttl)
	if err != nil {
		t.Fatalf("ClaimJob(%s): %v", owner, err)
	}
	return job
}

func (fx *executeFixture) deps() ExecuteReleaseSetLocalCommitDeps {
	return ExecuteReleaseSetLocalCommitDeps{
		UnitOfWork: fx.uow, IDs: fx.ids, WriteLeases: fx.store, MarkerReader: fx.provider, Creator: fx.provider,
		Lifecycle: fx.store, WriteLeaseTTL: 10 * time.Minute,
	}
}

func (fx *executeFixture) loadIntent(t *testing.T, id string) workdomain.ReleaseSetLocalCommit {
	t.Helper()
	intent, err := loadIntent(fx.ctx, fx.uow, id)
	if err != nil {
		t.Fatalf("loadIntent(%s): %v", id, err)
	}
	return intent
}

func (fx *executeFixture) loadRepositoryWorkspace(t *testing.T) ports.RepositoryWorkspaceRecord {
	t.Helper()
	record, err := loadRepositoryWorkspace(fx.ctx, fx.uow, "rw-1")
	if err != nil {
		t.Fatalf("loadRepositoryWorkspace: %v", err)
	}
	return record
}

func (fx *executeFixture) runGit(t *testing.T, args ...string) string {
	t.Helper()
	return runTestGitCommand(t, fx.workspacePath, args...)
}

func (fx *executeFixture) headCommit(t *testing.T) string {
	t.Helper()
	return strings.TrimSpace(fx.runGit(t, "rev-parse", "HEAD"))
}

func (fx *executeFixture) headMessage(t *testing.T) string {
	t.Helper()
	return fx.runGit(t, "log", "-1", "--format=%B", "HEAD")
}

func (fx *executeFixture) commitCount(t *testing.T) string {
	t.Helper()
	return strings.TrimSpace(fx.runGit(t, "rev-list", "--count", "HEAD"))
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

// spyCreator wraps a real ports.LocalCommitCreator and counts
// CreateLocalCommit calls — mirrors gitworktree's own spyLocalCommitCreator
// (localcommit_test.go).
type spyCreator struct {
	ports.LocalCommitCreator
	calls int
}

func (s *spyCreator) CreateLocalCommit(ctx context.Context, req ports.CreateLocalCommitRequest) (workspace.Revision, error) {
	s.calls++
	return s.LocalCommitCreator.CreateLocalCommit(ctx, req)
}

// --- happy path ---

func TestExecuteReleaseSetLocalCommit_HappyPath_CreatesRealCommitAndReleasesLease(t *testing.T) {
	fx := newExecuteFixture(t)
	writeTestFile(t, filepath.Join(fx.workspacePath, "service.txt"), "changed\n")

	result := fx.request(t, "req-1", "hash-1", "record repository result")
	job := fx.claim(t, "worker-1", 10*time.Minute)

	if err := ExecuteReleaseSetLocalCommit(fx.ctx, fx.deps(), job); err != nil {
		t.Fatalf("ExecuteReleaseSetLocalCommit: %v", err)
	}

	intent := fx.loadIntent(t, result.ReleaseSetLocalCommitID)
	if intent.State != workdomain.ReleaseSetLocalCommitCommitted {
		t.Fatalf("state = %s, want COMMITTED", intent.State)
	}
	if intent.ParentVCSObjectID != fx.baseRevision {
		t.Fatalf("parent = %s, want base revision %s", intent.ParentVCSObjectID, fx.baseRevision)
	}
	if intent.ResultVCSObjectID == "" || intent.ResultVCSObjectID == fx.baseRevision {
		t.Fatalf("result = %q, want a new commit distinct from base", intent.ResultVCSObjectID)
	}
	if head := fx.headCommit(t); head != intent.ResultVCSObjectID {
		t.Fatalf("workspace HEAD = %s, want the recorded result %s", head, intent.ResultVCSObjectID)
	}
	if !strings.Contains(fx.headMessage(t), markerTrailer(intent.Marker)) {
		t.Fatal("HEAD commit message is missing this operation's own marker trailer")
	}

	// Job completed: a second CompleteJob with the exact same lease must
	// now report ErrJobLeaseLost (state is no longer LEASED).
	jobLease := ports.JobLease{JobID: job.ID, Owner: job.LeaseOwner, Token: job.LeaseToken}
	if err := fx.store.CompleteJob(fx.ctx, jobLease); !errors.Is(err, ports.ErrJobLeaseLost) {
		t.Fatalf("CompleteJob (already completed) error = %v, want ErrJobLeaseLost", err)
	}

	// Write lease released: a fresh acquire attempt for the same target
	// must now succeed (no conflict).
	if _, err := fx.store.EnqueueJob(fx.ctx, ports.EnqueueJobRequest{
		ID: "job-probe", ProjectID: "project-1", Kind: "PROBE", AggregateType: "Probe", AggregateID: "probe-1",
		MaxClaims: 1, IdempotencyKey: "probe-1",
	}); err != nil {
		t.Fatalf("enqueue probe job: %v", err)
	}
	probeJob, _, err := fx.store.ClaimJob(fx.ctx, "prober", time.Minute)
	if err != nil {
		t.Fatalf("claim probe job: %v", err)
	}
	probeLease := ports.JobLease{JobID: probeJob.ID, Owner: probeJob.LeaseOwner, Token: probeJob.LeaseToken}
	if _, err := fx.store.AcquireLocalCommitWriteLease(fx.ctx, ports.AcquireLocalCommitWriteLeaseRequest{
		JobLease: probeLease,
		Target: ports.LocalCommitWriteLeaseTarget{
			RepositoryID: intent.RepositoryID, RepositoryWorkspaceID: workspace.RepositoryWorkspaceID(intent.RepositoryWorkspaceID), Generation: intent.ExpectedGeneration,
		},
		TTL: time.Minute,
	}); err != nil {
		t.Fatalf("acquire local commit write lease after release: %v, want success (previous lease should be released)", err)
	}

	if remotes := strings.TrimSpace(fx.runGit(t, "remote")); remotes != "" {
		t.Fatalf("workspace has remotes configured: %q — a real remote mutation would need one", remotes)
	}
}

// --- replay ---

// TestExecuteReleaseSetLocalCommit_Replay_NoSecondGitOperation is this
// task's own "replay ... no second Git operation" Verify-line scenario: a
// redelivered job (Handle called twice for the same, already-terminal
// intent) must never call LocalCommitCreator a second time.
func TestExecuteReleaseSetLocalCommit_Replay_NoSecondGitOperation(t *testing.T) {
	fx := newExecuteFixture(t)
	writeTestFile(t, filepath.Join(fx.workspacePath, "service.txt"), "changed\n")
	result := fx.request(t, "req-1", "hash-1", "record repository result")
	job := fx.claim(t, "worker-1", 10*time.Minute)

	spy := &spyCreator{LocalCommitCreator: fx.provider}
	deps := fx.deps()
	deps.Creator = spy

	if err := ExecuteReleaseSetLocalCommit(fx.ctx, deps, job); err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	if spy.calls != 1 {
		t.Fatalf("calls after first Execute = %d, want 1", spy.calls)
	}
	firstResult := fx.loadIntent(t, result.ReleaseSetLocalCommitID).ResultVCSObjectID

	// Redelivery: same job value handed to Handle a second time (the
	// intent is already COMMITTED in the database).
	if err := ExecuteReleaseSetLocalCommit(fx.ctx, deps, job); err != nil {
		t.Fatalf("second (replayed) Execute: %v", err)
	}
	if spy.calls != 1 {
		t.Fatalf("calls after replayed Execute = %d, want still 1 (no second Git operation)", spy.calls)
	}

	intent := fx.loadIntent(t, result.ReleaseSetLocalCommitID)
	if intent.State != workdomain.ReleaseSetLocalCommitCommitted || intent.ResultVCSObjectID != firstResult {
		t.Fatalf("intent after replay = %+v, want unchanged COMMITTED@%s", intent, firstResult)
	}
	if count := fx.commitCount(t); count != "2" {
		t.Fatalf("commit count = %s, want 2 (base + exactly one local commit)", count)
	}
}

// --- crash before Git ---

// TestExecuteReleaseSetLocalCommit_CrashBeforeGit_CleanRetryOneCommit is
// this task's own "crash before Git" Verify-line scenario: a worker that
// acquired the write lease but crashed before ever calling
// LocalCommitCreator must let a fresh worker retry cleanly, producing
// exactly one real commit.
func TestExecuteReleaseSetLocalCommit_CrashBeforeGit_CleanRetryOneCommit(t *testing.T) {
	fx := newExecuteFixture(t)
	writeTestFile(t, filepath.Join(fx.workspacePath, "service.txt"), "changed\n")
	result := fx.request(t, "req-1", "hash-1", "record repository result")
	intent := fx.loadIntent(t, result.ReleaseSetLocalCommitID)

	// Worker A claims the job and acquires the write lease, then "crashes"
	// — it never calls LocalCommitCreator at all.
	jobA := fx.claim(t, "worker-a", 60*time.Millisecond)
	jobLeaseA := ports.JobLease{JobID: jobA.ID, Owner: jobA.LeaseOwner, Token: jobA.LeaseToken}
	if _, err := fx.store.AcquireLocalCommitWriteLease(fx.ctx, ports.AcquireLocalCommitWriteLeaseRequest{
		JobLease: jobLeaseA,
		Target: ports.LocalCommitWriteLeaseTarget{
			RepositoryID: intent.RepositoryID, RepositoryWorkspaceID: workspace.RepositoryWorkspaceID(intent.RepositoryWorkspaceID), Generation: intent.ExpectedGeneration,
		},
		TTL: 60 * time.Millisecond,
	}); err != nil {
		t.Fatalf("acquire write lease (worker A): %v", err)
	}

	time.Sleep(150 * time.Millisecond) // let both the job lease and the write lease expire
	if _, err := fx.store.RecoverExpiredJobs(fx.ctx); err != nil {
		t.Fatalf("RecoverExpiredJobs: %v", err)
	}

	jobB := fx.claim(t, "worker-b", 10*time.Minute)
	spy := &spyCreator{LocalCommitCreator: fx.provider}
	deps := fx.deps()
	deps.Creator = spy
	if err := ExecuteReleaseSetLocalCommit(fx.ctx, deps, jobB); err != nil {
		t.Fatalf("Execute (worker B): %v", err)
	}
	if spy.calls != 1 {
		t.Fatalf("calls = %d, want exactly 1", spy.calls)
	}

	finalIntent := fx.loadIntent(t, result.ReleaseSetLocalCommitID)
	if finalIntent.State != workdomain.ReleaseSetLocalCommitCommitted {
		t.Fatalf("state = %s, want COMMITTED", finalIntent.State)
	}
	if count := fx.commitCount(t); count != "2" {
		t.Fatalf("commit count = %s, want 2 (base + exactly one local commit)", count)
	}
}

// --- crash after Git, before finalize ---

// TestExecuteReleaseSetLocalCommit_CrashAfterGitBeforeFinalize_ReusesExactCommit
// is this task's own single most safety-critical Verify-line scenario: a
// worker whose real `git commit` succeeded but crashed before the finalize
// transaction ever committed must never be followed by a second, duplicate
// commit — a fresh retry reconciles the exact same commit via its own
// marker and finalizes it, reusing it outright.
func TestExecuteReleaseSetLocalCommit_CrashAfterGitBeforeFinalize_ReusesExactCommit(t *testing.T) {
	fx := newExecuteFixture(t)
	writeTestFile(t, filepath.Join(fx.workspacePath, "service.txt"), "changed\n")
	result := fx.request(t, "req-1", "hash-1", "record repository result")
	intent := fx.loadIntent(t, result.ReleaseSetLocalCommitID)

	// Worker A: claim, acquire the write lease, pin the parent, and run
	// the REAL, mutating Git call — exactly what ExecuteReleaseSetLocalCommit
	// itself would do — then stop: simulate a crash right here, before
	// ever reaching the finalize transaction.
	jobA := fx.claim(t, "worker-a", 60*time.Millisecond)
	jobLeaseA := ports.JobLease{JobID: jobA.ID, Owner: jobA.LeaseOwner, Token: jobA.LeaseToken}
	if _, err := fx.store.AcquireLocalCommitWriteLease(fx.ctx, ports.AcquireLocalCommitWriteLeaseRequest{
		JobLease: jobLeaseA,
		Target: ports.LocalCommitWriteLeaseTarget{
			RepositoryID: intent.RepositoryID, RepositoryWorkspaceID: workspace.RepositoryWorkspaceID(intent.RepositoryWorkspaceID), Generation: intent.ExpectedGeneration,
		},
		TTL: 60 * time.Millisecond,
	}); err != nil {
		t.Fatalf("acquire write lease (worker A): %v", err)
	}
	pinned, err := pinParent(fx.ctx, fx.uow, intent, fx.baseRevision)
	if err != nil {
		t.Fatalf("pinParent (worker A): %v", err)
	}
	handle, err := ports.NewWorkspaceHandle(fx.loadRepositoryWorkspace(t).Workspace.Locator)
	if err != nil {
		t.Fatalf("NewWorkspaceHandle: %v", err)
	}
	revision, err := fx.provider.CreateLocalCommit(fx.ctx, ports.CreateLocalCommitRequest{
		Handle: handle, Message: commitMessageWithMarker(pinned.Message, pinned.Marker),
		AuthorName: pinned.AuthorName, AuthorEmail: pinned.AuthorEmail,
	})
	if err != nil {
		t.Fatalf("CreateLocalCommit (simulated worker A): %v", err)
	}
	// Crash here: never finalize, never release the write lease.

	time.Sleep(150 * time.Millisecond)
	if _, err := fx.store.RecoverExpiredJobs(fx.ctx); err != nil {
		t.Fatalf("RecoverExpiredJobs: %v", err)
	}

	jobB := fx.claim(t, "worker-b", 10*time.Minute)
	spy := &spyCreator{LocalCommitCreator: fx.provider}
	deps := fx.deps()
	deps.Creator = spy
	if err := ExecuteReleaseSetLocalCommit(fx.ctx, deps, jobB); err != nil {
		t.Fatalf("Execute (worker B): %v", err)
	}
	if spy.calls != 0 {
		t.Fatalf("calls = %d, want 0 — worker B must reuse the existing commit, never create a second one", spy.calls)
	}

	finalIntent := fx.loadIntent(t, result.ReleaseSetLocalCommitID)
	if finalIntent.State != workdomain.ReleaseSetLocalCommitCommitted {
		t.Fatalf("state = %s, want COMMITTED", finalIntent.State)
	}
	if finalIntent.ResultVCSObjectID != revision.VCSObjectID {
		t.Fatalf("result = %s, want the reused commit %s", finalIntent.ResultVCSObjectID, revision.VCSObjectID)
	}
	if finalIntent.ParentVCSObjectID != fx.baseRevision {
		t.Fatalf("parent = %s, want %s", finalIntent.ParentVCSObjectID, fx.baseRevision)
	}
	if count := fx.commitCount(t); count != "2" {
		t.Fatalf("commit count = %s, want exactly 2 (base + the ONE reused commit)", count)
	}
}

// --- drift / quarantine ---

// TestExecuteReleaseSetLocalCommit_MarkerDrift_QuarantinesAndFails is this
// task's own "mismatch blocks/quarantines" Verify-line scenario: a real
// commit at HEAD carries this operation's own exact marker, but its real
// parent does not match the parent this operation itself durably pinned —
// an anomaly that must never be silently reused. It quarantines the
// workspace and fails the operation with a typed FailureMarkerDrift,
// rather than proceeding as if it were a genuine reuse.
func TestExecuteReleaseSetLocalCommit_MarkerDrift_QuarantinesAndFails(t *testing.T) {
	fx := newExecuteFixture(t)
	writeTestFile(t, filepath.Join(fx.workspacePath, "service.txt"), "changed\n")
	result := fx.request(t, "req-1", "hash-1", "record repository result")
	intent := fx.loadIntent(t, result.ReleaseSetLocalCommitID)

	// Durably pin a DELIBERATELY WRONG parent (never what the real commit
	// below is actually built on top of) — simulating an anomaly this
	// operation's own reconciliation must catch, not silently accept.
	pinned, err := pinParent(fx.ctx, fx.uow, intent, "0000000000000000000000000000000000000000")
	if err != nil {
		t.Fatalf("pinParent: %v", err)
	}
	handle, err := ports.NewWorkspaceHandle(fx.loadRepositoryWorkspace(t).Workspace.Locator)
	if err != nil {
		t.Fatalf("NewWorkspaceHandle: %v", err)
	}
	if _, err := fx.provider.CreateLocalCommit(fx.ctx, ports.CreateLocalCommitRequest{
		Handle: handle, Message: commitMessageWithMarker(pinned.Message, pinned.Marker),
		AuthorName: pinned.AuthorName, AuthorEmail: pinned.AuthorEmail,
	}); err != nil {
		t.Fatalf("CreateLocalCommit (fixture): %v", err)
	}

	job := fx.claim(t, "worker-1", 10*time.Minute)
	spy := &spyCreator{LocalCommitCreator: fx.provider}
	deps := fx.deps()
	deps.Creator = spy
	if err := ExecuteReleaseSetLocalCommit(fx.ctx, deps, job); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if spy.calls != 0 {
		t.Fatalf("calls = %d, want 0 — drift must never be papered over with a new commit", spy.calls)
	}

	finalIntent := fx.loadIntent(t, result.ReleaseSetLocalCommitID)
	if finalIntent.State != workdomain.ReleaseSetLocalCommitFailed || finalIntent.FailureReason != workdomain.FailureMarkerDrift {
		t.Fatalf("intent = %+v, want FAILED/MARKER_DRIFT", finalIntent)
	}

	record := fx.loadRepositoryWorkspace(t)
	if record.Workspace.State != workspace.RepositoryWorkspaceQuarantined {
		t.Fatalf("repository workspace state = %s, want QUARANTINED", record.Workspace.State)
	}
}

// --- lease loss ---

// TestExecuteReleaseSetLocalCommit_WriteLeaseStolen_DoesNotFinalize is this
// task's own "lease loss" Verify-line scenario: once a DIFFERENT worker
// holds the local-commit write lease for this target, the original
// worker's own later attempt must fail to even re-acquire it — it can
// never reach, let alone commit, its own finalize transaction.
func TestExecuteReleaseSetLocalCommit_WriteLeaseStolen_DoesNotFinalize(t *testing.T) {
	fx := newExecuteFixture(t)
	writeTestFile(t, filepath.Join(fx.workspacePath, "service.txt"), "changed\n")
	result := fx.request(t, "req-1", "hash-1", "record repository result")
	intent := fx.loadIntent(t, result.ReleaseSetLocalCommitID)

	jobA := fx.claim(t, "worker-a", 10*time.Minute) // worker A's own JOB lease stays valid throughout
	jobLeaseA := ports.JobLease{JobID: jobA.ID, Owner: jobA.LeaseOwner, Token: jobA.LeaseToken}
	target := ports.LocalCommitWriteLeaseTarget{
		RepositoryID: intent.RepositoryID, RepositoryWorkspaceID: workspace.RepositoryWorkspaceID(intent.RepositoryWorkspaceID), Generation: intent.ExpectedGeneration,
	}
	if _, err := fx.store.AcquireLocalCommitWriteLease(fx.ctx, ports.AcquireLocalCommitWriteLeaseRequest{
		JobLease: jobLeaseA, Target: target, TTL: 60 * time.Millisecond,
	}); err != nil {
		t.Fatalf("acquire write lease (worker A): %v", err)
	}
	handle, err := ports.NewWorkspaceHandle(fx.loadRepositoryWorkspace(t).Workspace.Locator)
	if err != nil {
		t.Fatalf("NewWorkspaceHandle: %v", err)
	}
	pinned, err := pinParent(fx.ctx, fx.uow, intent, fx.baseRevision)
	if err != nil {
		t.Fatalf("pinParent: %v", err)
	}
	if _, err := fx.provider.CreateLocalCommit(fx.ctx, ports.CreateLocalCommitRequest{
		Handle: handle, Message: commitMessageWithMarker(pinned.Message, pinned.Marker),
		AuthorName: pinned.AuthorName, AuthorEmail: pinned.AuthorEmail,
	}); err != nil {
		t.Fatalf("CreateLocalCommit (worker A): %v", err)
	}

	// Worker A's own write lease TTL lapses; a genuinely different worker
	// (jobC) steals it before worker A ever reaches finalize.
	time.Sleep(120 * time.Millisecond)
	if _, err := fx.store.EnqueueJob(fx.ctx, ports.EnqueueJobRequest{
		ID: "job-c", ProjectID: "project-1", Kind: "PROBE", AggregateType: "Probe", AggregateID: "probe-c",
		MaxClaims: 1, IdempotencyKey: "probe-c",
	}); err != nil {
		t.Fatalf("enqueue job C: %v", err)
	}
	jobC, _, err := fx.store.ClaimJob(fx.ctx, "worker-c", 10*time.Minute)
	if err != nil {
		t.Fatalf("claim job C: %v", err)
	}
	jobLeaseC := ports.JobLease{JobID: jobC.ID, Owner: jobC.LeaseOwner, Token: jobC.LeaseToken}
	if _, err := fx.store.AcquireLocalCommitWriteLease(fx.ctx, ports.AcquireLocalCommitWriteLeaseRequest{
		JobLease: jobLeaseC, Target: target, TTL: 10 * time.Minute,
	}); err != nil {
		t.Fatalf("acquire write lease (worker C, stealing): %v", err)
	}

	// Worker A "wakes back up" (its own job lease is still valid — TTL was
	// 10 minutes) and tries to resume — it must NOT be able to finalize.
	deps := fx.deps()
	err = ExecuteReleaseSetLocalCommit(fx.ctx, deps, jobA)
	if err == nil {
		t.Fatal("Execute (worker A, resumed after write lease was stolen) = nil error, want a conflict — it must not finalize")
	}
	if !errors.Is(err, ports.ErrLocalCommitWriteLeaseConflict) {
		t.Fatalf("Execute (worker A, resumed) error = %v, want ErrLocalCommitWriteLeaseConflict", err)
	}

	finalIntent := fx.loadIntent(t, result.ReleaseSetLocalCommitID)
	if finalIntent.State != workdomain.ReleaseSetLocalCommitRequested {
		t.Fatalf("state = %s, want still REQUESTED — worker A must never have finalized", finalIntent.State)
	}
}
