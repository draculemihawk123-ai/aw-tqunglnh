package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// Aliases onto crashworker_fixtures.go's exported constants: this file was
// written against these unexported names before the shared, non-test worker
// logic was extracted (docs/design/02-v0-spike-verdict.md V0-10B).
const (
	crashNodeDispatchRunID              = CrashNodeDispatchRunID
	crashNodeDispatchNodeRunID          = CrashNodeDispatchNodeRunID
	crashNodeDispatchJobID              = CrashNodeDispatchJobID
	crashNodeDispatchNextJobID          = CrashNodeDispatchNextJobID
	crashNodeDispatchNextIdempotencyKey = CrashNodeDispatchNextIdempotencyKey
)

// TestSPK04FaultAfterNodeCompleteBeforeNextDispatch closes fault point 6/6
// of SPK-04: node completion, job acknowledgement and downstream-job
// dispatch commit in one SQLite transaction (Store.CompleteNodeAndDispatchNext),
// so a worker killed right after that commit — while it still looks, from
// the outside, like it might be mid-dispatch — can never lose the next step
// or duplicate it. A restarted dispatcher is free to safely replay the exact
// same request.
func TestSPK04FaultAfterNodeCompleteBeforeNextDispatch(t *testing.T) {
	if os.Getenv(crashWorkerModeEnvironment) == "node-dispatch-then-hang" {
		runNodeDispatchWorkerProcess(t)
		return
	}

	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "agentkit-crash-node-dispatch.db")
	store, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("open initial store: %v", err)
	}
	seedCrashResumeOwners(t, ctx, store)

	definition := crashResumeWorkflowDefinition()
	versionOne := compileCrashResumeWorkflowVersion(t, definition, "workflow-version-crash-dispatch-v1", 1, crashResumeWorkflowDocumentV1(), "skill-v1")
	if _, err := store.PublishWorkflowVersion(ctx, definition, versionOne); err != nil {
		t.Fatalf("publish workflow v1: %v", err)
	}
	run, err := runtime.NewWorkflowRun(
		crashNodeDispatchRunID, "project-crash", "work-item-crash", versionOne, "family-crash", 1,
		json.RawMessage(`{"checkpoint":"created"}`),
	)
	if err != nil {
		t.Fatalf("create workflow run: %v", err)
	}
	if err := store.StartWorkflowRun(ctx, run); err != nil {
		t.Fatalf("start workflow run: %v", err)
	}
	if _, err := store.CompareAndSwapWorkflowRun(ctx, ports.WorkflowRunTransition{
		RunID:           run.ID,
		ExpectedState:   runtime.WorkflowRunCreated,
		ExpectedVersion: 1,
		NextState:       runtime.WorkflowRunRunning,
		SharedState:     json.RawMessage(`{"checkpoint":"worker-dispatched"}`),
		OccurredAt:      time.Date(2026, 8, 28, 14, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("move workflow run to RUNNING: %v", err)
	}
	if _, err := store.EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID:             crashNodeDispatchJobID,
		ProjectID:      "project-crash",
		Kind:           "EXECUTE_NODE",
		AggregateType:  "WorkflowRun",
		AggregateID:    string(run.ID),
		Payload:        json.RawMessage(`{"runId":"` + crashNodeDispatchRunID + `"}`),
		MaxClaims:      3,
		IdempotencyKey: "execute-" + crashNodeDispatchJobID,
	}); err != nil {
		t.Fatalf("enqueue durable job: %v", err)
	}
	seedCrashNodeDispatchNodeRun(t, ctx, store, run.ID)
	if err := store.Close(); err != nil {
		t.Fatalf("close initial store: %v", err)
	}

	const crashedWorkerTTL = 900 * time.Millisecond
	ready := startAndHardKillCrashWorker(t, databasePath, crashedWorkerTTL, versionOne.ID(), versionOne.ContentHash(), "node-dispatch-then-hang")
	if ready.JobID != crashNodeDispatchJobID {
		t.Fatalf("crashed worker claimed job %s, want %s", ready.JobID, crashNodeDispatchJobID)
	}

	restarted, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("open store after hard crash: %v", err)
	}
	defer func() {
		if err := restarted.Close(); err != nil {
			t.Errorf("close restarted store: %v", err)
		}
	}()

	// The worker only prints READY after CompleteNodeAndDispatchNext
	// returned successfully, so this already committed for real before the
	// hard kill. Verify the post-crash state directly first.
	var nodeState string
	var nodeVersion uint64
	if err := restarted.db.QueryRowContext(ctx,
		`SELECT state, version FROM node_runs WHERE id = ?`, crashNodeDispatchNodeRunID,
	).Scan(&nodeState, &nodeVersion); err != nil {
		t.Fatalf("read node run after crash: %v", err)
	}
	if nodeState != string(runtime.NodeRunSucceeded) || nodeVersion != 2 {
		t.Fatalf("node run after crash = %s@%d, want SUCCEEDED@2", nodeState, nodeVersion)
	}
	var currentJobState string
	if err := restarted.db.QueryRowContext(ctx,
		`SELECT state FROM durable_jobs WHERE id = ?`, crashNodeDispatchJobID,
	).Scan(&currentJobState); err != nil {
		t.Fatalf("read current job state after crash: %v", err)
	}
	if currentJobState != string(ports.JobSucceeded) {
		t.Fatalf("current job state after crash = %s, want SUCCEEDED", currentJobState)
	}
	assertCrashNodeDispatchInvariants(t, ctx, restarted)

	// A restarted dispatcher cannot know for certain whether the crash
	// happened before or after commit, so it must be free to safely replay
	// the exact same dispatch. This must never duplicate the node
	// transition, the domain event or the downstream job — that is exactly
	// what "no boundary there" (the whole point of the transaction being
	// atomic) has to mean in practice.
	replayLease := ports.JobLease{JobID: ports.JobID(crashNodeDispatchJobID), Owner: "worker-before-crash", Token: ready.LeaseToken}
	replayNodeRun, replayJob, err := restarted.CompleteNodeAndDispatchNext(ctx, ports.NodeCompletionDispatch{
		NodeRunID:       crashNodeDispatchNodeRunID,
		ExpectedVersion: 1,
		SelectedOutcome: "done",
		JobLease:        replayLease,
		NextJob:         crashNodeDispatchNextJobRequest(),
		EventID:         "event-node-dispatch-1",
		CorrelationID:   "crash-node-dispatch",
		OccurredAt:      time.Date(2026, 8, 28, 14, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("replay CompleteNodeAndDispatchNext() error = %v", err)
	}
	if replayNodeRun.State != runtime.NodeRunSucceeded || replayNodeRun.Version != 2 {
		t.Fatalf("replay node run = %+v, want SUCCEEDED@2", replayNodeRun)
	}
	if replayJob.ID != ports.JobID(crashNodeDispatchNextJobID) {
		t.Fatalf("replay returned job %s, want %s", replayJob.ID, crashNodeDispatchNextJobID)
	}
	assertCrashNodeDispatchInvariants(t, ctx, restarted)

	// A genuinely different downstream request (different idempotency key)
	// after the node already completed is a real conflict, not a replay,
	// and must be rejected rather than silently accepted.
	if _, _, err := restarted.CompleteNodeAndDispatchNext(ctx, ports.NodeCompletionDispatch{
		NodeRunID:       crashNodeDispatchNodeRunID,
		ExpectedVersion: 1,
		SelectedOutcome: "done",
		JobLease:        replayLease,
		NextJob: ports.EnqueueJobRequest{
			ID: "job-crash-dispatch-different", ProjectID: "project-crash", Kind: "EXECUTE_NODE",
			AggregateType: "WorkflowRun", AggregateID: crashNodeDispatchRunID,
			Payload: json.RawMessage(`{}`), MaxClaims: 3, IdempotencyKey: "different-idempotency-key-crash-dispatch",
		},
		EventID:       "event-node-dispatch-conflict",
		CorrelationID: "crash-node-dispatch",
		OccurredAt:    time.Date(2026, 8, 28, 14, 1, 0, 0, time.UTC),
	}); !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("conflicting CompleteNodeAndDispatchNext() error = %v, want ErrOptimisticConflict", err)
	}

	// The downstream job is genuinely dispatched: a fresh worker can claim
	// it — the next step was not lost.
	claimedNext, _, err := restarted.ClaimJob(ctx, "worker-next-node", 5*time.Second)
	if err != nil {
		t.Fatalf("claim downstream job: %v", err)
	}
	if claimedNext.ID != ports.JobID(crashNodeDispatchNextJobID) {
		t.Fatalf("claimed job = %s, want downstream job %s", claimedNext.ID, crashNodeDispatchNextJobID)
	}
}

func crashNodeDispatchNextJobRequest() ports.EnqueueJobRequest {
	return CrashNodeDispatchNextJobRequest()
}

func assertCrashNodeDispatchInvariants(t *testing.T, ctx context.Context, store *Store) {
	t.Helper()
	var nextJobCount int
	if err := store.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM durable_jobs WHERE idempotency_key = ?`, crashNodeDispatchNextIdempotencyKey,
	).Scan(&nextJobCount); err != nil {
		t.Fatalf("count downstream jobs: %v", err)
	}
	if nextJobCount != 1 {
		t.Fatalf("downstream jobs = %d, want exactly 1", nextJobCount)
	}
	var eventCount int
	if err := store.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM domain_events WHERE aggregate_type = 'NodeRun' AND aggregate_id = ?`, crashNodeDispatchNodeRunID,
	).Scan(&eventCount); err != nil {
		t.Fatalf("count node completion events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("node completion events = %d, want exactly 1", eventCount)
	}
}

// runNodeDispatchWorkerProcess and seedCrashNodeDispatchNodeRun delegate to
// the shared RunCrashWorker (internal/adapters/sqlite/crashworker.go) and
// crashworker_fixtures.go's exported seed helper.
func runNodeDispatchWorkerProcess(t *testing.T) {
	t.Helper()
	if err := RunCrashWorker(CrashModeNodeDispatch, testBinarySpawner); err != nil {
		t.Fatal(err)
	}
}

func seedCrashNodeDispatchNodeRun(t *testing.T, ctx context.Context, store *Store, runID runtime.WorkflowRunID) {
	t.Helper()
	if err := SeedCrashNodeDispatchNodeRun(ctx, store, runID); err != nil {
		t.Fatal(err)
	}
}
