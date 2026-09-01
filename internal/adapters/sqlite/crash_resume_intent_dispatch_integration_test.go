package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// Fixture identities shared between each top-level test and its crashed
// worker child. Each test opens its own temp SQLite file, so reusing the
// same literal ids across the two tests in this file is safe.
const (
	crashIntentRunID          = "workflow-run-crash-intent"
	crashIntentNodeRunID      = "node-run-crash-intent"
	crashIntentJobID          = "job-crash-intent"
	crashIntentIdempotencyKey = "dispatch-intent-crash"
)

// TestSPK04FaultBeforeIntentJobCommitLeavesNothing closes crash boundary 1/6
// of SPK-04: the worker is killed before it ever attempts
// Store.DispatchNodeIntent (not mid-transaction — that would race the kill
// signal against the commit non-deterministically). Nothing durable may
// exist afterward: no node run, no job, no domain event.
func TestSPK04FaultBeforeIntentJobCommitLeavesNothing(t *testing.T) {
	if os.Getenv(crashWorkerModeEnvironment) == "before-intent-commit-then-hang" {
		runBeforeIntentCommitWorkerProcess(t)
		return
	}

	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "agentkit-crash-before-intent.db")
	store, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("open initial store: %v", err)
	}
	seedCrashResumeOwners(t, ctx, store)
	definition := crashResumeWorkflowDefinition()
	versionOne := compileCrashResumeWorkflowVersion(t, definition, "workflow-version-crash-before-intent-v1", 1, crashResumeWorkflowDocumentV1(), "skill-v1")
	if _, err := store.PublishWorkflowVersion(ctx, definition, versionOne); err != nil {
		t.Fatalf("publish workflow v1: %v", err)
	}
	run, err := runtime.NewWorkflowRun(
		crashIntentRunID, "project-crash", "work-item-crash", versionOne, "family-crash", 1,
		json.RawMessage(`{"checkpoint":"created"}`),
	)
	if err != nil {
		t.Fatalf("create workflow run: %v", err)
	}
	if err := store.StartWorkflowRun(ctx, run); err != nil {
		t.Fatalf("start workflow run: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close initial store: %v", err)
	}

	const crashedWorkerTTL = 900 * time.Millisecond
	startAndHardKillCrashWorker(t, databasePath, crashedWorkerTTL, versionOne.ID(), versionOne.ContentHash(), "before-intent-commit-then-hang")

	restarted, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("open store after hard crash: %v", err)
	}
	defer func() {
		if err := restarted.Close(); err != nil {
			t.Errorf("close restarted store: %v", err)
		}
	}()

	var runState string
	var runVersion uint64
	if err := restarted.db.QueryRowContext(ctx,
		`SELECT state, version FROM workflow_runs WHERE id = ?`, crashIntentRunID,
	).Scan(&runState, &runVersion); err != nil {
		t.Fatalf("read workflow run after crash: %v", err)
	}
	if runState != string(runtime.WorkflowRunCreated) || runVersion != 1 {
		t.Fatalf("workflow run after crash = %s@%d, want untouched CREATED@1", runState, runVersion)
	}
	var nodeRunCount int
	if err := restarted.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM node_runs WHERE id = ?`, crashIntentNodeRunID,
	).Scan(&nodeRunCount); err != nil {
		t.Fatalf("count node runs after crash: %v", err)
	}
	if nodeRunCount != 0 {
		t.Fatalf("node run exists after a crash before any commit: count=%d", nodeRunCount)
	}
	var jobCount int
	if err := restarted.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM durable_jobs WHERE idempotency_key = ?`, crashIntentIdempotencyKey,
	).Scan(&jobCount); err != nil {
		t.Fatalf("count durable jobs after crash: %v", err)
	}
	if jobCount != 0 {
		t.Fatalf("job exists after a crash before any commit: count=%d", jobCount)
	}
	var eventCount int
	if err := restarted.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM domain_events WHERE aggregate_type = 'NodeRun' AND aggregate_id = ?`, crashIntentNodeRunID,
	).Scan(&eventCount); err != nil {
		t.Fatalf("count domain events after crash: %v", err)
	}
	if eventCount != 0 {
		t.Fatalf("domain event exists after a crash before any commit: count=%d", eventCount)
	}
}

// TestSPK04FaultAfterIntentJobCommitBeforeClaim closes crash boundary 2/6 of
// SPK-04: the worker commits a real Store.DispatchNodeIntent transaction,
// then is hard-killed before anyone claims the job it created. Everything
// that transaction wrote must exist, and a replacement worker must be able
// to claim the job exactly once.
func TestSPK04FaultAfterIntentJobCommitBeforeClaim(t *testing.T) {
	if os.Getenv(crashWorkerModeEnvironment) == "after-intent-commit-then-hang" {
		runAfterIntentCommitWorkerProcess(t)
		return
	}

	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "agentkit-crash-after-intent.db")
	store, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("open initial store: %v", err)
	}
	seedCrashResumeOwners(t, ctx, store)
	definition := crashResumeWorkflowDefinition()
	versionOne := compileCrashResumeWorkflowVersion(t, definition, "workflow-version-crash-after-intent-v1", 1, crashResumeWorkflowDocumentV1(), "skill-v1")
	if _, err := store.PublishWorkflowVersion(ctx, definition, versionOne); err != nil {
		t.Fatalf("publish workflow v1: %v", err)
	}
	run, err := runtime.NewWorkflowRun(
		crashIntentRunID, "project-crash", "work-item-crash", versionOne, "family-crash", 1,
		json.RawMessage(`{"checkpoint":"created"}`),
	)
	if err != nil {
		t.Fatalf("create workflow run: %v", err)
	}
	if err := store.StartWorkflowRun(ctx, run); err != nil {
		t.Fatalf("start workflow run: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close initial store: %v", err)
	}

	const crashedWorkerTTL = 900 * time.Millisecond
	ready := startAndHardKillCrashWorker(t, databasePath, crashedWorkerTTL, versionOne.ID(), versionOne.ContentHash(), "after-intent-commit-then-hang")
	if ready.JobID != crashIntentJobID {
		t.Fatalf("crashed worker dispatched job %s, want %s", ready.JobID, crashIntentJobID)
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

	var runState string
	if err := restarted.db.QueryRowContext(ctx,
		`SELECT state FROM workflow_runs WHERE id = ?`, crashIntentRunID,
	).Scan(&runState); err != nil {
		t.Fatalf("read workflow run after crash: %v", err)
	}
	if runState != string(runtime.WorkflowRunRunning) {
		t.Fatalf("workflow run state after crash = %s, want RUNNING", runState)
	}
	var nodeState string
	if err := restarted.db.QueryRowContext(ctx,
		`SELECT state FROM node_runs WHERE id = ?`, crashIntentNodeRunID,
	).Scan(&nodeState); err != nil {
		t.Fatalf("read node run after crash: %v", err)
	}
	if nodeState != string(runtime.NodeRunRunning) {
		t.Fatalf("node run state after crash = %s, want RUNNING", nodeState)
	}
	var jobState string
	if err := restarted.db.QueryRowContext(ctx,
		`SELECT state FROM durable_jobs WHERE id = ?`, crashIntentJobID,
	).Scan(&jobState); err != nil {
		t.Fatalf("read job after crash: %v", err)
	}
	if jobState != string(ports.JobAvailable) {
		t.Fatalf("job state after crash = %s, want AVAILABLE", jobState)
	}
	var eventCount int
	if err := restarted.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM domain_events WHERE aggregate_type = 'NodeRun' AND aggregate_id = ?`, crashIntentNodeRunID,
	).Scan(&eventCount); err != nil {
		t.Fatalf("count domain events after crash: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("domain events after crash = %d, want exactly 1", eventCount)
	}

	// The next step was not lost, and only one worker can claim it.
	claimed, _, err := restarted.ClaimJob(ctx, "worker-after-restart", 5*time.Second)
	if err != nil {
		t.Fatalf("replacement worker claim: %v", err)
	}
	if claimed.ID != crashIntentJobID {
		t.Fatalf("claimed job = %s, want %s", claimed.ID, crashIntentJobID)
	}
	if _, _, err := restarted.ClaimJob(ctx, "worker-second-claimant", 5*time.Second); !errors.Is(err, ports.ErrNoJobAvailable) {
		t.Fatalf("second claim error = %v, want ErrNoJobAvailable (exactly one claim)", err)
	}
}

// runBeforeIntentCommitWorkerProcess is the crashed-worker child for
// boundary 1. It deliberately never calls DispatchNodeIntent — proving
// "before commit" by never attempting the transaction, not by racing a real
// call against the kill signal.
func runBeforeIntentCommitWorkerProcess(t *testing.T) {
	t.Helper()
	databasePath := strings.TrimSpace(os.Getenv(crashWorkerDBEnvironment))
	if databasePath == "" {
		t.Fatal("crash before-intent worker database path is required")
	}
	ttl, err := time.ParseDuration(os.Getenv(crashWorkerTTLEnvironment))
	if err != nil || ttl <= 0 {
		t.Fatalf("parse crash before-intent worker TTL: %v", err)
	}

	ctx := context.Background()
	store, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("crash before-intent worker open store: %v", err)
	}
	// Intentionally no Close.

	run, err := store.LoadWorkflowRun(ctx, crashIntentRunID)
	if err != nil {
		t.Fatalf("crash before-intent worker load workflow run: %v", err)
	}

	fmt.Printf(
		"%s %s %d %s %s %s\n",
		crashWorkerReadyPrefix,
		"no-job-dispatched-yet",
		uint64(0),
		time.Now().UTC().Add(ttl).Format(time.RFC3339Nano),
		run.WorkflowVersionID,
		run.WorkflowVersionHash,
	)
	_ = os.Stdout.Sync()
	select {}
}

// runAfterIntentCommitWorkerProcess is the crashed-worker child for
// boundary 2. It commits a real DispatchNodeIntent transaction, then hangs;
// the parent hard-kills it only after that commit succeeded.
func runAfterIntentCommitWorkerProcess(t *testing.T) {
	t.Helper()
	databasePath := strings.TrimSpace(os.Getenv(crashWorkerDBEnvironment))
	if databasePath == "" {
		t.Fatal("crash after-intent worker database path is required")
	}
	if _, err := time.ParseDuration(os.Getenv(crashWorkerTTLEnvironment)); err != nil {
		t.Fatalf("parse crash after-intent worker TTL: %v", err)
	}

	ctx := context.Background()
	store, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("crash after-intent worker open store: %v", err)
	}
	// Intentionally no Close: the parent terminates this process while its
	// SQLite connection is still live, right after the dispatch transaction
	// committed for real.

	run, err := store.LoadWorkflowRun(ctx, crashIntentRunID)
	if err != nil {
		t.Fatalf("crash after-intent worker load workflow run: %v", err)
	}

	_, newJob, err := store.DispatchNodeIntent(ctx, ports.NodeIntentDispatch{
		RunID:              crashIntentRunID,
		ExpectedRunVersion: 1,
		NodeRunID:          crashIntentNodeRunID,
		NodeKey:            "implement",
		ActivationSequence: 1,
		InputStateHash:     "sha256:input-crash-intent",
		Job: ports.EnqueueJobRequest{
			ID:             crashIntentJobID,
			ProjectID:      "project-crash",
			Kind:           "EXECUTE_NODE",
			AggregateType:  "WorkflowRun",
			AggregateID:    crashIntentRunID,
			Payload:        json.RawMessage(`{}`),
			MaxClaims:      3,
			IdempotencyKey: crashIntentIdempotencyKey,
		},
		EventID:       "event-crash-intent-1",
		CorrelationID: "crash-intent",
		OccurredAt:    time.Date(2026, 8, 28, 15, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("crash after-intent worker dispatch: %v", err)
	}

	fmt.Printf(
		"%s %s %d %s %s %s\n",
		crashWorkerReadyPrefix,
		newJob.ID,
		uint64(0),
		time.Now().UTC().Format(time.RFC3339Nano),
		run.WorkflowVersionID,
		run.WorkflowVersionHash,
	)
	_ = os.Stdout.Sync()
	select {}
}
