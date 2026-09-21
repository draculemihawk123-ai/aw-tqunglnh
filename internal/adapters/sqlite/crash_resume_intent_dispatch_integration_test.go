package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// Aliases onto crashworker_fixtures.go's exported constants: this file was
// written against these unexported names before the shared, non-test worker
// logic was extracted (docs/design/02-v0-spike-verdict.md V0-10B).
const (
	crashIntentRunID          = CrashIntentRunID
	crashIntentNodeRunID      = CrashIntentNodeRunID
	crashIntentJobID          = CrashIntentJobID
	crashIntentIdempotencyKey = CrashIntentIdempotencyKey
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
	databasePath := migratedDatabasePath(t, "agentkit-crash-before-intent.db")
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
	databasePath := migratedDatabasePath(t, "agentkit-crash-after-intent.db")
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

// runBeforeIntentCommitWorkerProcess and runAfterIntentCommitWorkerProcess
// (boundaries 1 and 2) delegate to the shared RunCrashWorker
// (internal/adapters/sqlite/crashworker.go).
func runBeforeIntentCommitWorkerProcess(t *testing.T) {
	t.Helper()
	if err := RunCrashWorker(CrashModeBeforeIntentCommit, testBinarySpawner); err != nil {
		t.Fatal(err)
	}
}

func runAfterIntentCommitWorkerProcess(t *testing.T) {
	t.Helper()
	if err := RunCrashWorker(CrashModeAfterIntentCommit, testBinarySpawner); err != nil {
		t.Fatal(err)
	}
}
