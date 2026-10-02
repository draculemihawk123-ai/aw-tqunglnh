package releasesetcommit

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// V9-09 / B1: a local commit requested against a worktree with NO change.
//
// Observed with the real binary before this fix: gitworktree's
// CreateLocalCommit answered ErrNothingToCommit, ExecuteReleaseSetLocalCommit
// wrapped it as an ordinary error, Handler.Handle returned it, and the worker
// pool therefore left the job un-completed — so the job was re-claimed after
// every lease expiry until claim_count hit max_claims and it went DEAD, while
// the operation row stayed REQUESTED forever. "Nothing to commit" is a
// determinate fact about the worktree, so the operation must close FAILED
// with the typed reason NO_CHANGES on the FIRST claim and the job must
// complete. These tests never wait on a clock: lease expiry (only needed to
// reproduce the old retry loop) is forced explicitly with
// sqlite.ExpireJobLeaseForTest.

// TestExecuteReleaseSetLocalCommit_NoChanges_FailsTerminalWithoutRetry runs
// the executor once against a clean worktree.
func TestExecuteReleaseSetLocalCommit_NoChanges_FailsTerminalWithoutRetry(t *testing.T) {
	fx := newExecuteFixture(t)
	// Deliberately NO writeTestFile: the worktree is exactly the base commit.
	result := fx.request(t, "req-1", "hash-1", "record repository result")
	job := fx.claim(t, "worker-1", 10*time.Minute)

	spy := &spyCreator{LocalCommitCreator: fx.provider}
	deps := fx.deps()
	deps.Creator = spy
	if err := ExecuteReleaseSetLocalCommit(fx.ctx, deps, job); err != nil {
		t.Fatalf("ExecuteReleaseSetLocalCommit (clean worktree) = %v, want nil: nothing-to-commit is a determinate terminal outcome, not a retryable error", err)
	}
	if spy.calls != 1 {
		t.Fatalf("CreateLocalCommit calls = %d, want exactly 1 (the clean-worktree answer is what is mapped)", spy.calls)
	}

	intent := fx.loadIntent(t, result.ReleaseSetLocalCommitID)
	if intent.State != workdomain.ReleaseSetLocalCommitFailed || intent.FailureReason != workdomain.FailureNoChanges {
		t.Fatalf("intent = state %s reason %q, want FAILED/NO_CHANGES", intent.State, intent.FailureReason)
	}
	if intent.ResultVCSObjectID != "" {
		t.Fatalf("result = %q, want empty — no commit was created", intent.ResultVCSObjectID)
	}
	// The operator-facing read model (the query behind both the HTTP status
	// route and `aw release-set local-commit status`/`--wait`) carries the
	// machine-readable reason.
	status, err := work.GetReleaseSetLocalCommitStatus(fx.ctx, fx.uow, result.ReleaseSetLocalCommitID)
	if err != nil {
		t.Fatalf("GetReleaseSetLocalCommitStatus: %v", err)
	}
	if status.State != "FAILED" || status.FailureReason != "NO_CHANGES" {
		t.Fatalf("status = %s/%q, want FAILED/NO_CHANGES", status.State, status.FailureReason)
	}
	if intent.CompletedAt == nil {
		t.Fatal("completedAt is nil on a terminal FAILED operation")
	}
	if count := fx.commitCount(t); count != "1" {
		t.Fatalf("commit count = %s, want 1 (the base commit only)", count)
	}
	if head := fx.headCommit(t); head != fx.baseRevision {
		t.Fatalf("workspace HEAD = %s, want unchanged base %s", head, fx.baseRevision)
	}

	// The job itself completed (so the pool never re-claims it): a second
	// CompleteJob with the same lease reports the lease as no longer active.
	jobLease := ports.JobLease{JobID: job.ID, Owner: job.LeaseOwner, Token: job.LeaseToken}
	if err := fx.store.CompleteJob(fx.ctx, jobLease); err == nil {
		t.Fatal("CompleteJob on the already-finished job succeeded — the executor left the job leased")
	}
	assertJobStates(t, fx, map[string]int{"SUCCEEDED": 1})

	// The write lease was released: a different job can take it immediately.
	if _, err := fx.store.EnqueueJob(fx.ctx, ports.EnqueueJobRequest{
		ID: "job-probe", ProjectID: "project-1", Kind: "PROBE", AggregateType: "Probe", AggregateID: "probe-1",
		MaxClaims: 1, IdempotencyKey: "probe-1",
	}); err != nil {
		t.Fatalf("enqueue probe job: %v", err)
	}
	probeJob, _, err := fx.store.ClaimJob(fx.ctx, "prober", 10*time.Minute)
	if err != nil {
		t.Fatalf("claim probe job: %v", err)
	}
	if _, err := fx.store.AcquireLocalCommitWriteLease(fx.ctx, ports.AcquireLocalCommitWriteLeaseRequest{
		JobLease: ports.JobLease{JobID: probeJob.ID, Owner: probeJob.LeaseOwner, Token: probeJob.LeaseToken},
		Target: ports.LocalCommitWriteLeaseTarget{
			RepositoryID: intent.RepositoryID, RepositoryWorkspaceID: workspace.RepositoryWorkspaceID(intent.RepositoryWorkspaceID), Generation: intent.ExpectedGeneration,
		},
		TTL: 10 * time.Minute,
	}); err != nil {
		t.Fatalf("acquire write lease after a NO_CHANGES failure: %v, want success (the lease must have been released)", err)
	}

	// A redelivered job for the now-terminal operation is a harmless no-op.
	if err := ExecuteReleaseSetLocalCommit(fx.ctx, deps, job); err != nil {
		t.Fatalf("replayed Execute: %v", err)
	}
	if spy.calls != 1 {
		t.Fatalf("CreateLocalCommit calls after replay = %d, want still 1", spy.calls)
	}
}

// TestHandler_NoChanges_JobIsNotRetriedToDead drives the production Handler
// the way the worker pool does: claim, Handle, and — only if Handle returned
// an error, which the pool answers by leaving the job un-completed — let the
// lease lapse (forced, no sleeping) and let the recovery reaper re-offer it.
// The loop mirrors max_claims worth of retries. It must stop at the FIRST
// claim: the job is COMPLETED, never AVAILABLE again and never DEAD, and the
// operation is FAILED/NO_CHANGES rather than stuck REQUESTED.
func TestHandler_NoChanges_JobIsNotRetriedToDead(t *testing.T) {
	fx := newExecuteFixture(t)
	result := fx.request(t, "req-1", "hash-1", "record repository result")
	handler := NewHandler(fx.deps())

	claims := 0
	for claims < defaultLocalCommitJobMaxClaims+1 {
		job, _, err := fx.store.ClaimJob(fx.ctx, "worker-1", 10*time.Minute)
		if errors.Is(err, ports.ErrNoJobAvailable) {
			break // nothing left to claim: the job completed or is DEAD
		}
		if err != nil {
			t.Fatalf("ClaimJob #%d: %v", claims+1, err)
		}
		claims++
		if err := handler.Handle(fx.ctx, job); err == nil {
			break // completed in the same call, as the pool expects
		}
		// The pool would leave the job leased and wait for the lease to
		// lapse; force that instead of sleeping, then run the reaper.
		if err := sqlite.ExpireJobLeaseForTest(fx.ctx, fx.store, string(job.ID)); err != nil {
			t.Fatalf("ExpireJobLeaseForTest: %v", err)
		}
		if _, err := fx.store.RecoverExpiredJobs(fx.ctx); err != nil {
			t.Fatalf("RecoverExpiredJobs: %v", err)
		}
	}

	if claims != 1 {
		t.Fatalf("the job was claimed %d times, want exactly 1: a clean worktree must fail terminally on the first claim", claims)
	}
	assertJobStates(t, fx, map[string]int{"SUCCEEDED": 1})
	intent := fx.loadIntent(t, result.ReleaseSetLocalCommitID)
	if intent.State != workdomain.ReleaseSetLocalCommitFailed || intent.FailureReason != workdomain.FailureNoChanges {
		t.Fatalf("intent = state %s reason %q, want FAILED/NO_CHANGES (never stuck REQUESTED)", intent.State, intent.FailureReason)
	}
}

// TestExecuteReleaseSetLocalCommit_NoChanges_OnSealedReleaseSet_RetrySucceeds
// is the "abandon or retry" half of the B1 acceptance line. Docs
// (design/07 V5-10A: "SEALED/ABANDONED immutable") forbid abandoning a
// SEALED ReleaseSet, so the way forward after a determinate local-commit
// failure is to ask again. The operator seals, requests a local commit on the
// clean worktree (FAILED/NO_CHANGES), edits the worktree, and asks again with
// the SAME message and a new idempotency key. The first request's marker is
// derived only from request-time pins (ReleaseSet version, workspace
// generation, actor, message hash), so the identical second request derives
// the identical base marker; the FAILED NO_CHANGES operation never created a
// commit, so it must not block it — and that must hold repeatedly.
func TestExecuteReleaseSetLocalCommit_NoChanges_OnSealedReleaseSet_RetrySucceeds(t *testing.T) {
	fx := newExecuteFixture(t)
	sealed, err := work.SealReleaseSet(fx.ctx, fx.uow,
		testCommand("seal-1", "hash-seal", ports.ProjectScope("project-1"), "SealReleaseSet", 1),
		work.SealReleaseSetRequest{ReleaseSetID: fx.releaseSetID})
	if err != nil {
		t.Fatalf("SealReleaseSet: %v", err)
	}
	if sealed.State != string(workdomain.ReleaseSetSealed) {
		t.Fatalf("release set state = %s, want SEALED", sealed.State)
	}

	request := func(key string) (RequestReleaseSetLocalCommitResult, error) {
		return RequestReleaseSetLocalCommit(fx.ctx, fx.uow, fx.ids,
			testCommand(key, "hash-"+key, ports.ProjectScope("project-1"), "RequestReleaseSetLocalCommit", 0),
			RequestReleaseSetLocalCommitRequest{
				ProjectID: "project-1", ReleaseSetID: fx.releaseSetID, ExpectedReleaseSetVersion: sealed.Version,
				RepositoryWorkspaceID: "rw-1", ExpectedWorkspaceVersion: 1,
				Message: "record repository result", AuthorName: "Release Bot", AuthorEmail: "release-bot@example.invalid",
			})
	}
	mustRequest := func(key string) RequestReleaseSetLocalCommitResult {
		t.Helper()
		result, err := request(key)
		if err != nil {
			t.Fatalf("RequestReleaseSetLocalCommit(%s): %v", key, err)
		}
		return result
	}
	execute := func(label string) {
		t.Helper()
		job := fx.claim(t, "worker-1", 10*time.Minute)
		if err := ExecuteReleaseSetLocalCommit(fx.ctx, fx.deps(), job); err != nil {
			t.Fatalf("Execute (%s): %v", label, err)
		}
	}

	// Two consecutive clean-worktree failures: the ordinal keeps advancing.
	first := mustRequest("req-1")
	execute("first")
	second := mustRequest("req-2")
	execute("second")
	for _, failed := range []RequestReleaseSetLocalCommitResult{first, second} {
		if got := fx.loadIntent(t, failed.ReleaseSetLocalCommitID); got.State != workdomain.ReleaseSetLocalCommitFailed || got.FailureReason != workdomain.FailureNoChanges {
			t.Fatalf("intent %s = %s/%q, want FAILED/NO_CHANGES", failed.ReleaseSetLocalCommitID, got.State, got.FailureReason)
		}
	}
	if first.Marker == second.Marker {
		t.Fatal("the retry reused the failed operation's marker; markers are UNIQUE per operation")
	}

	writeTestFile(t, filepath.Join(fx.workspacePath, "service.txt"), "changed\n")
	third := mustRequest("req-3")
	if third.Marker == first.Marker || third.Marker == second.Marker {
		t.Fatal("the third request reused an earlier failed operation's marker")
	}
	execute("third")
	committed := fx.loadIntent(t, third.ReleaseSetLocalCommitID)
	if committed.State != workdomain.ReleaseSetLocalCommitCommitted {
		t.Fatalf("third intent state = %s, want COMMITTED", committed.State)
	}
	if count := fx.commitCount(t); count != "2" {
		t.Fatalf("commit count = %s, want 2 (base + the one retried commit)", count)
	}
	if !strings.Contains(fx.headMessage(t), markerTrailer(third.Marker)) {
		t.Fatal("HEAD commit message is missing the retried operation's own marker trailer")
	}

	// A genuine duplicate of the now-COMMITTED operation is NOT a retry: it
	// derives a marker that an operation which really committed (or is
	// still in flight) already owns, so it still collides.
	if _, err := request("req-4"); err == nil {
		t.Fatal("a duplicate request for an operation that did not fail NO_CHANGES succeeded; want a marker collision")
	}
}

func TestWithAttemptOrdinal(t *testing.T) {
	base := computeOperationMarker("rs-1", 1, "rw-1", 1, "actor-1", "sha256:hash-a")
	if got := withAttemptOrdinal(base, 0); got != base {
		t.Fatalf("attempt 0 = %s, want the base marker unchanged", got)
	}
	one, two := withAttemptOrdinal(base, 1), withAttemptOrdinal(base, 2)
	if one == base || two == base || one == two {
		t.Fatalf("ordinals must derive distinct markers: base=%s one=%s two=%s", base, one, two)
	}
	if again := withAttemptOrdinal(base, 1); again != one {
		t.Fatalf("withAttemptOrdinal is not deterministic: %s != %s", again, one)
	}
}

func assertJobStates(t *testing.T, fx *executeFixture, want map[string]int) {
	t.Helper()
	rows, err := fx.store.DebugListJobsByKind(fx.ctx, ReleaseSetLocalCommitJobKind)
	if err != nil {
		t.Fatalf("DebugListJobsByKind: %v", err)
	}
	got := map[string]int{}
	for _, row := range rows {
		got[row.State]++
	}
	if len(got) != len(want) {
		t.Fatalf("local-commit job states = %v, want %v", got, want)
	}
	for state, n := range want {
		if got[state] != n {
			t.Fatalf("local-commit job states = %v, want %v", got, want)
		}
	}
}
