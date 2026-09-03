package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/scopeguard"
	"github.com/taQuangLing/agent-workflow/internal/app/worker"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// The tests below (V3-09) close the gaps a full audit of the pre-existing
// AcquireWriteLeases/HeartbeatWriteLeases/ValidateWriteLease/ReleaseWriteLeases
// implementation found, without changing any of that implementation: batch
// order by RepositoryID, all-or-none acquisition and the fencing/generation
// guarantees were already correct and already covered above (this file) and
// by TestSPK09QuarantineRecreateFencesStaleGeneration
// (spk09_workspace_quarantine_test.go, real quarantine/recreate generation
// supersession) and TestWriteLeaseRaceHasOneWinnerForSameRepository (the
// 100-iteration race this task's own Verify line names). HeartbeatWriteLeases
// itself, however, had zero coverage anywhere in the repository — no test,
// no spike scenario, no production caller — before this task; the tests
// below exercise it for the first time, plus two narrow gaps the audit found
// nothing else already proving: a direct (non-quarantine) generation
// mismatch rejection, and AK-ARCH-013's "different repositories may hold
// WriteLeases at the same time" half (only the same-repository exclusivity
// half had a prior test).

func TestDurableJobClaimIsExclusive(t *testing.T) {
	store := openSchedulingTestStore(t)
	seedSchedulingFixture(t, store)
	enqueueSchedulingTestJob(t, store, "job-race", "job-race-key", 3)

	const workerCount = 12
	start := make(chan struct{})
	var winners atomic.Int32
	var unavailable atomic.Int32
	errorsSeen := make(chan error, workerCount)
	var workers sync.WaitGroup
	for index := 0; index < workerCount; index++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			<-start
			_, _, err := store.ClaimJob(context.Background(), workerName(index), 2*time.Second)
			switch {
			case err == nil:
				winners.Add(1)
			case errors.Is(err, ports.ErrNoJobAvailable):
				unavailable.Add(1)
			default:
				errorsSeen <- err
			}
		}(index)
	}
	close(start)
	workers.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		t.Errorf("unexpected claim error: %v", err)
	}
	if got := winners.Load(); got != 1 {
		t.Fatalf("claim winners = %d, want exactly 1", got)
	}
	if got := unavailable.Load(); got != workerCount-1 {
		t.Fatalf("unavailable claims = %d, want %d", got, workerCount-1)
	}
}

func TestDurableJobTakeoverFencesExpiredOwner(t *testing.T) {
	store := openSchedulingTestStore(t)
	seedSchedulingFixture(t, store)
	enqueueSchedulingTestJob(t, store, "job-takeover", "job-takeover-key", 3)

	_, first, err := store.ClaimJob(context.Background(), "worker-1", 5*time.Millisecond)
	if err != nil {
		t.Fatalf("first ClaimJob() error = %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		recovered, err := store.RecoverExpiredJobs(context.Background())
		if err != nil {
			t.Fatalf("RecoverExpiredJobs() error = %v", err)
		}
		if recovered == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("durable job lease did not expire before deadline")
		}
		time.Sleep(5 * time.Millisecond)
	}

	_, second, err := store.ClaimJob(context.Background(), "worker-2", 2*time.Second)
	if err != nil {
		t.Fatalf("takeover ClaimJob() error = %v", err)
	}
	if second.Token <= first.Token {
		t.Fatalf("takeover token = %d, want greater than stale token %d", second.Token, first.Token)
	}
	if err := store.CompleteJob(context.Background(), first); !errors.Is(err, ports.ErrJobLeaseLost) {
		t.Fatalf("stale CompleteJob() error = %v, want ErrJobLeaseLost", err)
	}
	if err := store.CompleteJob(context.Background(), second); err != nil {
		t.Fatalf("authoritative CompleteJob() error = %v", err)
	}
}

func TestWriteLeaseBatchIsAtomicAndFencesPreviousGrant(t *testing.T) {
	store := openSchedulingTestStore(t)
	seedSchedulingFixture(t, store)

	enqueueSchedulingTestJob(t, store, "job-1", "job-1-key", 3)
	_, jobLease1, err := store.ClaimJob(context.Background(), "worker-1", 5*time.Second)
	if err != nil {
		t.Fatalf("claim job-1: %v", err)
	}
	enqueueSchedulingTestJob(t, store, "job-2", "job-2-key", 3)
	_, jobLease2, err := store.ClaimJob(context.Background(), "worker-2", 5*time.Second)
	if err != nil {
		t.Fatalf("claim job-2: %v", err)
	}

	webTarget := ports.WorkspaceLeaseTarget{
		RepositoryID:          "repo-web",
		RepositoryWorkspaceID: "rw-web",
		Generation:            1,
	}
	userTarget := ports.WorkspaceLeaseTarget{
		RepositoryID:          "repo-user",
		RepositoryWorkspaceID: "rw-user",
		Generation:            1,
	}

	firstGrants, err := store.AcquireWriteLeases(context.Background(), ports.AcquireWriteLeasesRequest{
		JobLease:  jobLease1,
		AttemptID: runtime.ExecutionAttemptID("attempt-1"),
		Targets:   []ports.WorkspaceLeaseTarget{webTarget},
		TTL:       5 * time.Second,
	})
	if err != nil {
		t.Fatalf("first AcquireWriteLeases() error = %v", err)
	}

	_, err = store.AcquireWriteLeases(context.Background(), ports.AcquireWriteLeasesRequest{
		JobLease:  jobLease2,
		AttemptID: runtime.ExecutionAttemptID("attempt-2"),
		Targets:   []ports.WorkspaceLeaseTarget{webTarget, userTarget},
		TTL:       5 * time.Second,
	})
	if !errors.Is(err, ports.ErrWriteLeaseConflict) {
		t.Fatalf("conflicting AcquireWriteLeases() error = %v, want ErrWriteLeaseConflict", err)
	}

	var rolledBackRows int
	if err := store.db.QueryRowContext(context.Background(), `
SELECT COUNT(*) FROM write_leases
WHERE repository_workspace_id = 'rw-user' AND generation = 1`).Scan(&rolledBackRows); err != nil {
		t.Fatalf("query rolled-back write lease: %v", err)
	}
	if rolledBackRows != 0 {
		t.Fatalf("partially acquired write leases = %d, want 0", rolledBackRows)
	}

	if err := store.ReleaseWriteLeases(context.Background(), firstGrants); err != nil {
		t.Fatalf("ReleaseWriteLeases() error = %v", err)
	}
	secondGrants, err := store.AcquireWriteLeases(context.Background(), ports.AcquireWriteLeasesRequest{
		JobLease:  jobLease2,
		AttemptID: runtime.ExecutionAttemptID("attempt-2"),
		Targets:   []ports.WorkspaceLeaseTarget{webTarget, userTarget},
		TTL:       5 * time.Second,
	})
	if err != nil {
		t.Fatalf("takeover AcquireWriteLeases() error = %v", err)
	}
	if len(secondGrants) != 2 {
		t.Fatalf("takeover grants = %d, want 2", len(secondGrants))
	}

	var webTakeover ports.WriteLeaseGrant
	for _, grant := range secondGrants {
		if err := store.ValidateWriteLease(context.Background(), grant); err != nil {
			t.Fatalf("ValidateWriteLease(%s) error = %v", grant.RepositoryWorkspaceID, err)
		}
		if grant.RepositoryWorkspaceID == webTarget.RepositoryWorkspaceID {
			webTakeover = grant
		}
	}
	if webTakeover.FenceToken <= firstGrants[0].FenceToken {
		t.Fatalf("write takeover token = %d, want greater than stale token %d",
			webTakeover.FenceToken, firstGrants[0].FenceToken)
	}
	if err := store.ValidateWriteLease(context.Background(), firstGrants[0]); !errors.Is(err, ports.ErrWriteLeaseLost) {
		t.Fatalf("stale ValidateWriteLease() error = %v, want ErrWriteLeaseLost", err)
	}
}

func TestWriteLeaseRequiresItsOriginalActiveJobFence(t *testing.T) {
	store := openSchedulingTestStore(t)
	seedSchedulingFixture(t, store)
	enqueueSchedulingTestJob(t, store, "job-linked-fence", "job-linked-fence-key", 3)

	// This test needs a deterministic baseline period before testing expiry. A
	// 5ms lease can expire while a busy Windows test runner schedules the first
	// validation, turning a fencing assertion into a timing race.
	_, firstJobLease, err := store.ClaimJob(context.Background(), "same-worker-name", 300*time.Millisecond)
	if err != nil {
		t.Fatalf("first ClaimJob() error = %v", err)
	}
	grants, err := store.AcquireWriteLeases(context.Background(), ports.AcquireWriteLeasesRequest{
		JobLease:  firstJobLease,
		AttemptID: runtime.ExecutionAttemptID("attempt-1"),
		Targets: []ports.WorkspaceLeaseTarget{{
			RepositoryID:          "repo-user",
			RepositoryWorkspaceID: "rw-user",
			Generation:            1,
		}},
		TTL: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("AcquireWriteLeases() error = %v", err)
	}
	if err := store.ValidateWriteLease(context.Background(), grants[0]); err != nil {
		t.Fatalf("baseline ValidateWriteLease() error = %v", err)
	}

	waitForRecoveredJob(t, store)
	_, replacementJobLease, err := store.ClaimJob(context.Background(), "same-worker-name", 2*time.Second)
	if err != nil {
		t.Fatalf("replacement ClaimJob() error = %v", err)
	}
	if replacementJobLease.Token <= firstJobLease.Token {
		t.Fatalf("replacement job token = %d, want greater than %d",
			replacementJobLease.Token, firstJobLease.Token)
	}
	if err := store.ValidateWriteLease(context.Background(), grants[0]); !errors.Is(err, ports.ErrWriteLeaseLost) {
		t.Fatalf("old write grant after job takeover error = %v, want ErrWriteLeaseLost", err)
	}
}

func TestFinalizerRejectsOutOfScopeDiffBeforeFencedSQLiteMutation(t *testing.T) {
	store := openSchedulingTestStore(t)
	seedSchedulingFixture(t, store)
	enqueueSchedulingTestJob(t, store, "job-scope", "job-scope-key", 3)
	_, jobLease, err := store.ClaimJob(context.Background(), "scope-worker", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	finalizer, err := worker.NewFinalizer(store)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := work.NewRepositoryScope(
		"family-1", 1, project.RepositoryID("repo-user"), work.RepositoryWrite,
		[]string{"src"}, "spike", "tester", time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = finalizer.Finalize(context.Background(), worker.FinalizationInput{
		Finalization: ports.WorkerWorkflowRunFinalization{
			Transition: ports.WorkflowRunTransition{
				RunID: "run-1", ExpectedState: runtime.WorkflowRunRunning, ExpectedVersion: 1,
				NextState: runtime.WorkflowRunSucceeded, SharedState: json.RawMessage(`{"result":"invalid-scope"}`),
				OccurredAt: time.Date(2026, 8, 28, 0, 1, 0, 0, time.UTC),
			},
			JobLease: jobLease, EventID: "event-scope-violation", CorrelationID: "spk-07-scope",
		},
		EffectiveScopes: []work.RepositoryScope{scope},
		Diffs: []ports.WorkspaceDiff{{
			RepositoryID: "repo-web", Files: []ports.FileStatus{{Path: "src/app.ts"}},
		}},
	})
	if !errors.Is(err, scopeguard.ErrScopeViolation) {
		t.Fatalf("Finalize() error = %v, want ErrScopeViolation", err)
	}
	var runState string
	var runVersion uint64
	if err := store.db.QueryRowContext(context.Background(), `SELECT state, version FROM workflow_runs WHERE id = 'run-1'`).Scan(&runState, &runVersion); err != nil {
		t.Fatal(err)
	}
	if runState != string(runtime.WorkflowRunRunning) || runVersion != 1 {
		t.Fatalf("scope violation mutated run: state=%s version=%d", runState, runVersion)
	}
	var jobState string
	if err := store.db.QueryRowContext(context.Background(), `SELECT state FROM durable_jobs WHERE id = 'job-scope'`).Scan(&jobState); err != nil {
		t.Fatal(err)
	}
	if jobState != string(ports.JobLeased) {
		t.Fatalf("scope violation acknowledged job: state=%s", jobState)
	}
}

func TestWriteLeaseRaceHasOneWinnerForSameRepository(t *testing.T) {
	store := openSchedulingTestStore(t)
	seedSchedulingFixture(t, store)
	enqueueSchedulingTestJob(t, store, "job-race-a", "job-race-a-key", 3)
	_, leaseA, err := store.ClaimJob(context.Background(), "worker-a", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	enqueueSchedulingTestJob(t, store, "job-race-b", "job-race-b-key", 3)
	_, leaseB, err := store.ClaimJob(context.Background(), "worker-b", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	target := ports.WorkspaceLeaseTarget{
		RepositoryID:          "repo-user",
		RepositoryWorkspaceID: "rw-user",
		Generation:            1,
	}

	var lastFence uint64
	for iteration := 0; iteration < 100; iteration++ {
		start := make(chan struct{})
		type result struct {
			grants []ports.WriteLeaseGrant
			err    error
		}
		results := make(chan result, 2)
		requests := []ports.AcquireWriteLeasesRequest{
			{JobLease: leaseA, AttemptID: "attempt-1", Targets: []ports.WorkspaceLeaseTarget{target}, TTL: 5 * time.Second},
			{JobLease: leaseB, AttemptID: "attempt-2", Targets: []ports.WorkspaceLeaseTarget{target}, TTL: 5 * time.Second},
		}
		for _, request := range requests {
			request := request
			go func() {
				<-start
				grants, err := store.AcquireWriteLeases(context.Background(), request)
				results <- result{grants: grants, err: err}
			}()
		}
		close(start)

		var winner []ports.WriteLeaseGrant
		conflicts := 0
		for count := 0; count < 2; count++ {
			result := <-results
			switch {
			case result.err == nil:
				if winner != nil {
					t.Fatalf("iteration %d produced two write-lease winners", iteration)
				}
				winner = result.grants
			case errors.Is(result.err, ports.ErrWriteLeaseConflict):
				conflicts++
			default:
				t.Fatalf("iteration %d unexpected acquire error: %v", iteration, result.err)
			}
		}
		if len(winner) != 1 || conflicts != 1 {
			t.Fatalf("iteration %d winners=%d conflicts=%d, want 1/1", iteration, len(winner), conflicts)
		}
		if winner[0].FenceToken <= lastFence {
			t.Fatalf("iteration %d fence=%d, want greater than %d", iteration, winner[0].FenceToken, lastFence)
		}
		lastFence = winner[0].FenceToken
		if err := store.ReleaseWriteLeases(context.Background(), winner); err != nil {
			t.Fatalf("iteration %d release error: %v", iteration, err)
		}
	}
}

// TestWriteLeaseHeartbeatExtendsLeaseAndBlocksConflictingAcquire is
// HeartbeatWriteLeases' first-ever exercise in this repository. It proves
// the heartbeat genuinely pushes lease_until forward (not a no-op): a rival
// acquire attempt that arrives after the ORIGINAL short TTL would have
// elapsed is still fenced out, and the heartbeated grant still validates.
func TestWriteLeaseHeartbeatExtendsLeaseAndBlocksConflictingAcquire(t *testing.T) {
	store := openSchedulingTestStore(t)
	seedSchedulingFixture(t, store)
	enqueueSchedulingTestJob(t, store, "job-hb-extend", "job-hb-extend-key", 3)
	_, jobLease, err := store.ClaimJob(context.Background(), "worker-hb", 5*time.Second)
	if err != nil {
		t.Fatalf("ClaimJob() error = %v", err)
	}
	target := ports.WorkspaceLeaseTarget{
		RepositoryID:          "repo-user",
		RepositoryWorkspaceID: "rw-user",
		Generation:            1,
	}
	grants, err := store.AcquireWriteLeases(context.Background(), ports.AcquireWriteLeasesRequest{
		JobLease: jobLease, AttemptID: runtime.ExecutionAttemptID("attempt-1"),
		Targets: []ports.WorkspaceLeaseTarget{target},
		TTL:     300 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("AcquireWriteLeases() error = %v", err)
	}
	originalLeaseUntil := grants[0].LeaseUntil

	heartbeated, err := store.HeartbeatWriteLeases(context.Background(), grants, 5*time.Second)
	if err != nil {
		t.Fatalf("HeartbeatWriteLeases() error = %v", err)
	}
	if len(heartbeated) != 1 {
		t.Fatalf("heartbeated grants = %d, want 1", len(heartbeated))
	}
	if diff := heartbeated[0].LeaseUntil.Sub(originalLeaseUntil); diff < time.Second {
		t.Fatalf("heartbeat extended lease by %s, want a real multi-second extension past the original TTL", diff)
	}

	// The original 300ms TTL would have expired by now if the heartbeat had
	// not genuinely pushed lease_until forward.
	waitPastLeaseUntil(t, originalLeaseUntil)
	enqueueSchedulingTestJob(t, store, "job-hb-rival", "job-hb-rival-key", 3)
	_, rivalLease, err := store.ClaimJob(context.Background(), "worker-hb-rival", 5*time.Second)
	if err != nil {
		t.Fatalf("rival ClaimJob() error = %v", err)
	}
	if _, err := store.AcquireWriteLeases(context.Background(), ports.AcquireWriteLeasesRequest{
		JobLease: rivalLease, AttemptID: runtime.ExecutionAttemptID("attempt-2"),
		Targets: []ports.WorkspaceLeaseTarget{target},
		TTL:     5 * time.Second,
	}); !errors.Is(err, ports.ErrWriteLeaseConflict) {
		t.Fatalf("rival AcquireWriteLeases() after heartbeat error = %v, want ErrWriteLeaseConflict", err)
	}
	if err := store.ValidateWriteLease(context.Background(), heartbeated[0]); err != nil {
		t.Fatalf("ValidateWriteLease() after heartbeat error = %v", err)
	}
}

// TestWriteLeaseHeartbeatRejectsStaleJobLease is this task's own Verify
// line, sub-scenario (b), against HeartbeatWriteLeases specifically (the
// pre-existing TestWriteLeaseRequiresItsOriginalActiveJobFence above proves
// the same JobLease-vs-WriteLease authority split for ValidateWriteLease):
// once the underlying JobLease has been reassigned to a new token, the
// original holder can no longer heartbeat its WriteLease forward, even
// though the WriteLease's own TTL has not yet elapsed.
func TestWriteLeaseHeartbeatRejectsStaleJobLease(t *testing.T) {
	store := openSchedulingTestStore(t)
	seedSchedulingFixture(t, store)
	enqueueSchedulingTestJob(t, store, "job-hb-stale", "job-hb-stale-key", 3)

	_, firstJobLease, err := store.ClaimJob(context.Background(), "same-worker-name", 300*time.Millisecond)
	if err != nil {
		t.Fatalf("first ClaimJob() error = %v", err)
	}
	grants, err := store.AcquireWriteLeases(context.Background(), ports.AcquireWriteLeasesRequest{
		JobLease: firstJobLease, AttemptID: runtime.ExecutionAttemptID("attempt-1"),
		Targets: []ports.WorkspaceLeaseTarget{{
			RepositoryID: "repo-user", RepositoryWorkspaceID: "rw-user", Generation: 1,
		}},
		TTL: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("AcquireWriteLeases() error = %v", err)
	}

	waitForRecoveredJob(t, store)
	_, replacementJobLease, err := store.ClaimJob(context.Background(), "same-worker-name", 2*time.Second)
	if err != nil {
		t.Fatalf("replacement ClaimJob() error = %v", err)
	}
	if replacementJobLease.Token <= firstJobLease.Token {
		t.Fatalf("replacement job token = %d, want greater than %d", replacementJobLease.Token, firstJobLease.Token)
	}

	if _, err := store.HeartbeatWriteLeases(context.Background(), grants, 5*time.Second); !errors.Is(err, ports.ErrWriteLeaseLost) {
		t.Fatalf("HeartbeatWriteLeases() with stale job lease error = %v, want ErrWriteLeaseLost", err)
	}
}

// TestWriteLeaseHeartbeatBatchIsAllOrNone proves HeartbeatWriteLeases shares
// AcquireWriteLeases' all-or-none semantics: one stale grant in a multi-target
// batch must abort the whole heartbeat, never partially extend the other,
// still-valid grant in the same call.
func TestWriteLeaseHeartbeatBatchIsAllOrNone(t *testing.T) {
	store := openSchedulingTestStore(t)
	seedSchedulingFixture(t, store)
	enqueueSchedulingTestJob(t, store, "job-hb-batch", "job-hb-batch-key", 3)
	_, jobLease, err := store.ClaimJob(context.Background(), "worker-hb-batch", 5*time.Second)
	if err != nil {
		t.Fatalf("ClaimJob() error = %v", err)
	}
	userTarget := ports.WorkspaceLeaseTarget{RepositoryID: "repo-user", RepositoryWorkspaceID: "rw-user", Generation: 1}
	webTarget := ports.WorkspaceLeaseTarget{RepositoryID: "repo-web", RepositoryWorkspaceID: "rw-web", Generation: 1}
	grants, err := store.AcquireWriteLeases(context.Background(), ports.AcquireWriteLeasesRequest{
		JobLease: jobLease, AttemptID: runtime.ExecutionAttemptID("attempt-1"),
		Targets: []ports.WorkspaceLeaseTarget{userTarget, webTarget},
		TTL:     5 * time.Second,
	})
	if err != nil {
		t.Fatalf("AcquireWriteLeases() error = %v", err)
	}
	if len(grants) != 2 {
		t.Fatalf("grants = %d, want 2", len(grants))
	}

	var userGrant, webGrant ports.WriteLeaseGrant
	for _, grant := range grants {
		switch grant.RepositoryWorkspaceID {
		case "rw-user":
			userGrant = grant
		case "rw-web":
			webGrant = grant
		}
	}
	if userGrant.RepositoryWorkspaceID == "" || webGrant.RepositoryWorkspaceID == "" {
		t.Fatal("did not receive grants for both targets")
	}

	// "repo-user" sorts before "repo-web", so the batch processes the (still
	// valid) user grant first. Presenting a fence token for the web grant
	// that no longer matches its row forces the batch to fail partway
	// through; the assertion below confirms the user grant's own successful
	// UPDATE was rolled back along with it, not left committed.
	corrupted := webGrant
	corrupted.FenceToken = webGrant.FenceToken + 1000
	mixed := []ports.WriteLeaseGrant{userGrant, corrupted}

	if _, err := store.HeartbeatWriteLeases(context.Background(), mixed, 5*time.Second); !errors.Is(err, ports.ErrWriteLeaseLost) {
		t.Fatalf("HeartbeatWriteLeases() with one stale grant error = %v, want ErrWriteLeaseLost", err)
	}

	var leaseUntilText string
	if err := store.db.QueryRowContext(context.Background(), `
SELECT lease_until FROM write_leases WHERE repository_workspace_id = 'rw-user' AND generation = 1`,
	).Scan(&leaseUntilText); err != nil {
		t.Fatalf("query rw-user lease_until: %v", err)
	}
	leaseUntil, err := parseDBTime(leaseUntilText)
	if err != nil {
		t.Fatal(err)
	}
	if !leaseUntil.Equal(userGrant.LeaseUntil) {
		t.Fatalf("rw-user lease_until = %s, want unchanged at %s (batch heartbeat must be all-or-none)",
			leaseUntil, userGrant.LeaseUntil)
	}
}

// TestReleaseWriteLeasesBatchIsAllOrNone mirrors the heartbeat batch
// all-or-none proof above for ReleaseWriteLeases: one stale grant in the
// batch must abort the whole release, never partially release the other,
// still-valid grant.
func TestReleaseWriteLeasesBatchIsAllOrNone(t *testing.T) {
	store := openSchedulingTestStore(t)
	seedSchedulingFixture(t, store)
	enqueueSchedulingTestJob(t, store, "job-release-batch", "job-release-batch-key", 3)
	_, jobLease, err := store.ClaimJob(context.Background(), "worker-release-batch", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	userTarget := ports.WorkspaceLeaseTarget{RepositoryID: "repo-user", RepositoryWorkspaceID: "rw-user", Generation: 1}
	webTarget := ports.WorkspaceLeaseTarget{RepositoryID: "repo-web", RepositoryWorkspaceID: "rw-web", Generation: 1}
	grants, err := store.AcquireWriteLeases(context.Background(), ports.AcquireWriteLeasesRequest{
		JobLease: jobLease, AttemptID: runtime.ExecutionAttemptID("attempt-1"),
		Targets: []ports.WorkspaceLeaseTarget{userTarget, webTarget},
		TTL:     5 * time.Second,
	})
	if err != nil {
		t.Fatalf("AcquireWriteLeases() error = %v", err)
	}
	var userGrant, webGrant ports.WriteLeaseGrant
	for _, grant := range grants {
		switch grant.RepositoryWorkspaceID {
		case "rw-user":
			userGrant = grant
		case "rw-web":
			webGrant = grant
		}
	}

	corrupted := webGrant
	corrupted.FenceToken = webGrant.FenceToken + 1000
	mixed := []ports.WriteLeaseGrant{userGrant, corrupted}
	if err := store.ReleaseWriteLeases(context.Background(), mixed); !errors.Is(err, ports.ErrWriteLeaseLost) {
		t.Fatalf("ReleaseWriteLeases() with one stale grant error = %v, want ErrWriteLeaseLost", err)
	}

	// rw-user must still be genuinely held: a rival cannot acquire it.
	enqueueSchedulingTestJob(t, store, "job-release-rival", "job-release-rival-key", 3)
	_, rivalLease, err := store.ClaimJob(context.Background(), "worker-release-rival", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AcquireWriteLeases(context.Background(), ports.AcquireWriteLeasesRequest{
		JobLease: rivalLease, AttemptID: runtime.ExecutionAttemptID("attempt-2"),
		Targets: []ports.WorkspaceLeaseTarget{userTarget},
		TTL:     5 * time.Second,
	}); !errors.Is(err, ports.ErrWriteLeaseConflict) {
		t.Fatalf("rival AcquireWriteLeases(rw-user) after aborted batch release error = %v, want ErrWriteLeaseConflict", err)
	}
}

// TestAcquireWriteLeasesRejectsGenerationMismatch closes this task's Verify
// sub-scenario (c) directly against AcquireWriteLeases: naming a generation
// number that does not match the RepositoryWorkspace's actual current
// generation is rejected outright, and grants nothing.
// TestSPK09QuarantineRecreateFencesStaleGeneration (in this package) proves
// the same guarantee through the real production path (quarantine then
// recreate at generation+1); this test isolates the check itself, with no
// quarantine machinery involved, against a plain wrong generation number.
func TestAcquireWriteLeasesRejectsGenerationMismatch(t *testing.T) {
	store := openSchedulingTestStore(t)
	seedSchedulingFixture(t, store)
	enqueueSchedulingTestJob(t, store, "job-gen-mismatch", "job-gen-mismatch-key", 3)
	_, jobLease, err := store.ClaimJob(context.Background(), "worker-gen", 5*time.Second)
	if err != nil {
		t.Fatalf("ClaimJob() error = %v", err)
	}
	if _, err := store.AcquireWriteLeases(context.Background(), ports.AcquireWriteLeasesRequest{
		JobLease: jobLease, AttemptID: runtime.ExecutionAttemptID("attempt-1"),
		Targets: []ports.WorkspaceLeaseTarget{{
			RepositoryID: "repo-user", RepositoryWorkspaceID: "rw-user", Generation: 2,
		}},
		TTL: 5 * time.Second,
	}); !errors.Is(err, ports.ErrWriteLeaseConflict) {
		t.Fatalf("AcquireWriteLeases() for a generation not matching the current row error = %v, want ErrWriteLeaseConflict", err)
	}

	var leaseCount int
	if err := store.db.QueryRowContext(context.Background(), `
SELECT COUNT(*) FROM write_leases WHERE repository_workspace_id = 'rw-user'`,
	).Scan(&leaseCount); err != nil {
		t.Fatal(err)
	}
	if leaseCount != 0 {
		t.Fatalf("write_leases rows for rw-user = %d, want 0 (mismatched generation must never be granted)", leaseCount)
	}
}

// TestWriteLeaseAllowsDifferentSiblingRepositoriesSimultaneously proves
// AK-ARCH-013's other half: two different attempts each hold a currently
// valid WriteLease on two DIFFERENT repositories' workspaces at the same
// time without conflict. (The same-repository exclusivity half already has
// dedicated coverage above: TestWriteLeaseRaceHasOneWinnerForSameRepository
// and TestWriteLeaseBatchIsAtomicAndFencesPreviousGrant.)
func TestWriteLeaseAllowsDifferentSiblingRepositoriesSimultaneously(t *testing.T) {
	store := openSchedulingTestStore(t)
	seedSchedulingFixture(t, store)
	enqueueSchedulingTestJob(t, store, "job-sibling-a", "job-sibling-a-key", 3)
	_, leaseA, err := store.ClaimJob(context.Background(), "worker-sibling-a", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	enqueueSchedulingTestJob(t, store, "job-sibling-b", "job-sibling-b-key", 3)
	_, leaseB, err := store.ClaimJob(context.Background(), "worker-sibling-b", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	userGrants, err := store.AcquireWriteLeases(context.Background(), ports.AcquireWriteLeasesRequest{
		JobLease: leaseA, AttemptID: runtime.ExecutionAttemptID("attempt-1"),
		Targets: []ports.WorkspaceLeaseTarget{{RepositoryID: "repo-user", RepositoryWorkspaceID: "rw-user", Generation: 1}},
		TTL:     5 * time.Second,
	})
	if err != nil {
		t.Fatalf("AcquireWriteLeases(repo-user) error = %v", err)
	}
	webGrants, err := store.AcquireWriteLeases(context.Background(), ports.AcquireWriteLeasesRequest{
		JobLease: leaseB, AttemptID: runtime.ExecutionAttemptID("attempt-2"),
		Targets: []ports.WorkspaceLeaseTarget{{RepositoryID: "repo-web", RepositoryWorkspaceID: "rw-web", Generation: 1}},
		TTL:     5 * time.Second,
	})
	if err != nil {
		t.Fatalf("AcquireWriteLeases(repo-web) error = %v", err)
	}

	if err := store.ValidateWriteLease(context.Background(), userGrants[0]); err != nil {
		t.Fatalf("ValidateWriteLease(repo-user) error = %v", err)
	}
	if err := store.ValidateWriteLease(context.Background(), webGrants[0]); err != nil {
		t.Fatalf("ValidateWriteLease(repo-web) error = %v", err)
	}
}

// TestSortedLeaseTargetsOrdersByRepositoryID is a direct unit test of the
// sort AcquireWriteLeases applies before acquiring anything (GC-INV-19:
// "Multi-repository write lease được acquire all-or-none theo thứ tự
// RepositoryID ổn định"). The behavioral tests above already prove the
// batch commits atomically; this isolates the ordering itself, including
// its stable tie-break and that it never mutates the caller's own slice.
func TestSortedLeaseTargetsOrdersByRepositoryID(t *testing.T) {
	input := []ports.WorkspaceLeaseTarget{
		{RepositoryID: "repo-c", RepositoryWorkspaceID: "rw-c", Generation: 1},
		{RepositoryID: "repo-a", RepositoryWorkspaceID: "rw-a-2", Generation: 2},
		{RepositoryID: "repo-a", RepositoryWorkspaceID: "rw-a-1", Generation: 1},
		{RepositoryID: "repo-b", RepositoryWorkspaceID: "rw-b", Generation: 1},
	}
	original := append([]ports.WorkspaceLeaseTarget(nil), input...)

	got := sortedLeaseTargets(input)

	want := []project.RepositoryID{"repo-a", "repo-a", "repo-b", "repo-c"}
	if len(got) != len(want) {
		t.Fatalf("sortedLeaseTargets length = %d, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].RepositoryID != id {
			t.Fatalf("sortedLeaseTargets[%d].RepositoryID = %s, want %s", i, got[i].RepositoryID, id)
		}
	}
	if got[0].RepositoryWorkspaceID != "rw-a-1" || got[1].RepositoryWorkspaceID != "rw-a-2" {
		t.Fatalf("sortedLeaseTargets did not tie-break same RepositoryID by workspace/generation: %+v", got)
	}
	for i := range input {
		if input[i] != original[i] {
			t.Fatalf("sortedLeaseTargets mutated its input slice at index %d", i)
		}
	}
}

// TestSortedWriteLeaseGrantsOrdersByRepositoryID is TestSortedLeaseTargetsOrdersByRepositoryID's
// counterpart for the identical sort HeartbeatWriteLeases and
// ReleaseWriteLeases apply to their own grant batches.
func TestSortedWriteLeaseGrantsOrdersByRepositoryID(t *testing.T) {
	input := []ports.WriteLeaseGrant{
		{RepositoryID: "repo-c", RepositoryWorkspaceID: "rw-c"},
		{RepositoryID: "repo-a", RepositoryWorkspaceID: "rw-a"},
		{RepositoryID: "repo-b", RepositoryWorkspaceID: "rw-b"},
	}
	original := append([]ports.WriteLeaseGrant(nil), input...)

	got := sortedWriteLeaseGrants(input)

	want := []project.RepositoryID{"repo-a", "repo-b", "repo-c"}
	for i, id := range want {
		if got[i].RepositoryID != id {
			t.Fatalf("sortedWriteLeaseGrants[%d].RepositoryID = %s, want %s", i, got[i].RepositoryID, id)
		}
	}
	for i := range input {
		if input[i] != original[i] {
			t.Fatalf("sortedWriteLeaseGrants mutated its input slice at index %d", i)
		}
	}
}

func openSchedulingTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "agentkit.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return store
}

func enqueueSchedulingTestJob(t *testing.T, store *Store, id, key string, maxClaims uint32) {
	t.Helper()
	_, err := store.EnqueueJob(context.Background(), ports.EnqueueJobRequest{
		ID:             ports.JobID(id),
		ProjectID:      "project-1",
		Kind:           "EXECUTE_NODE",
		AggregateType:  "ExecutionAttempt",
		AggregateID:    "attempt-1",
		Payload:        []byte(`{"attemptId":"attempt-1"}`),
		MaxClaims:      maxClaims,
		IdempotencyKey: key,
	})
	if err != nil {
		t.Fatalf("EnqueueJob(%s) error = %v", id, err)
	}
}

func waitForRecoveredJob(t *testing.T, store *Store) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		recovered, err := store.RecoverExpiredJobs(context.Background())
		if err != nil {
			t.Fatalf("RecoverExpiredJobs() error = %v", err)
		}
		if recovered == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("durable job lease did not expire before deadline")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitPastLeaseUntil blocks, deterministically, until the wall clock has
// passed a specific WriteLeaseGrant's own recorded LeaseUntil (plus a small
// safety margin for clock-read granularity). A job lease and a write lease
// acquired moments apart with the same TTL do not share one expiry instant:
// the write lease's clock started a little later, so it expires a little
// later too. waitForRecoveredJob alone only proves the job lease died; a
// caller that goes on to contend for the SAME write lease still needs this,
// or it can race a write lease that is technically still live for a few
// milliseconds (see docs/design/02-v0-spike-verdict.md V0-11A's follow-up
// finding, surfaced by a real Windows CI run, not reproduced locally).
func waitPastLeaseUntil(t *testing.T, leaseUntil time.Time) {
	t.Helper()
	deadline := leaseUntil.Add(2 * time.Second)
	for {
		now := time.Now().UTC()
		if now.After(leaseUntil.Add(20 * time.Millisecond)) {
			return
		}
		if now.After(deadline) {
			t.Fatalf("write lease did not pass its own LeaseUntil (%s) before deadline", leaseUntil)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func workerName(index int) string {
	const digits = "0123456789abcdef"
	return "worker-" + string(digits[index%len(digits)])
}

func seedSchedulingFixture(t *testing.T, store *Store) {
	t.Helper()
	const timestamp = "2026-08-28T00:00:00Z"
	_, err := store.db.ExecContext(context.Background(), `
INSERT INTO projects(id, name, status, version, created_at, updated_at)
VALUES ('project-1', 'Spike', 'ACTIVE', 1, ?, ?);

INSERT INTO repositories(id, project_id, name, local_path, default_ref, status, version, created_at, updated_at)
VALUES
  ('repo-user', 'project-1', 'user-service', 'C:/fixture/user', 'main', 'ACTIVE', 1, ?, ?),
  ('repo-web', 'project-1', 'web-app', 'C:/fixture/web', 'main', 'ACTIVE', 1, ?, ?);

INSERT INTO task_families(id, project_id, root_work_item_id, scope_version, status, version, created_at, updated_at)
VALUES ('family-1', 'project-1', 'work-root', 1, 'ACTIVE', 1, ?, ?);

INSERT INTO work_items(id, project_id, kind, parent_id, family_id, title, status, version, created_at, updated_at)
VALUES ('work-root', 'project-1', 'ROOT', NULL, 'family-1', 'Root', 'ACTIVE', 1, ?, ?);

INSERT INTO workspace_sets(id, project_id, family_id, state, version, created_at, updated_at)
VALUES ('workspace-set-1', 'project-1', 'family-1', 'READY', 1, ?, ?);

INSERT INTO repository_workspaces(
  id, project_id, workspace_set_id, family_id, repository_id, generation,
  locator, branch_ref, base_revision, current_revision, state, version, created_at, updated_at
) VALUES
  ('rw-user', 'project-1', 'workspace-set-1', 'family-1', 'repo-user', 1,
   'opaque:user', 'agentkit/family-1/user', 'user-base', 'user-base', 'READY', 1, ?, ?),
  ('rw-web', 'project-1', 'workspace-set-1', 'family-1', 'repo-web', 1,
   'opaque:web', 'agentkit/family-1/web', 'web-base', 'web-base', 'READY', 1, ?, ?);

INSERT INTO workflow_definitions(id, project_id, name, status, version, created_at, updated_at)
VALUES ('definition-1', 'project-1', 'Spike workflow', 'ACTIVE', 1, ?, ?);

INSERT INTO workflow_versions(
  id, definition_id, version_no, schema_version, canonical_content, content_hash,
  dependency_manifest, published_by, published_at
) VALUES ('workflow-version-1', 'definition-1', 1, 1, '{}', 'sha256:fixture', '{}', 'test', ?);

INSERT INTO workflow_runs(
  id, project_id, work_item_id, workflow_version_id, family_id, scope_version,
  state, shared_state_json, version, created_at, updated_at
) VALUES ('run-1', 'project-1', 'work-root', 'workflow-version-1', 'family-1', 1,
          'RUNNING', '{}', 1, ?, ?);

INSERT INTO node_runs(
  id, run_id, node_key, activation_sequence, iteration, state,
  input_state_hash, version, created_at, updated_at
) VALUES ('node-run-1', 'run-1', 'agent', 1, 0, 'RUNNING', 'sha256:input', 1, ?, ?);

INSERT INTO execution_attempts(
  id, node_run_id, attempt_no, state, provider_key, execution_profile_hash,
  input_revision_set_json, version, created_at, updated_at
) VALUES
  ('attempt-1', 'node-run-1', 1, 'RUNNING', 'codex', 'sha256:profile', '[]', 1, ?, ?),
  ('attempt-2', 'node-run-1', 2, 'RUNNING', 'claude', 'sha256:profile', '[]', 1, ?, ?);
`,
		timestamp, timestamp,
		timestamp, timestamp, timestamp, timestamp,
		timestamp, timestamp,
		timestamp, timestamp,
		timestamp, timestamp,
		timestamp, timestamp, timestamp, timestamp,
		timestamp, timestamp,
		timestamp,
		timestamp, timestamp,
		timestamp, timestamp,
		timestamp, timestamp, timestamp, timestamp,
	)
	if err != nil {
		t.Fatalf("seed scheduling fixture: %v", err)
	}
}
