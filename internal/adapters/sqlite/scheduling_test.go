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
