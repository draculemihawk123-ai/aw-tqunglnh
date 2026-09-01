package sqlite

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/worker"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// Fixture identities shared verbatim between the three processes below: the
// top-level test, the crashed worker child and the fake-provider grandchild
// all compile from this same source, so there is no need to echo them over
// stdout the way the genuinely dynamic lease/job values are.
const (
	crashCheckpointProviderModeEnvironment = "AGENTKIT_SPIKE_CRASH_CKPT_PROVIDER"
	crashCheckpointProviderReadyPrefix     = "AGENTKIT_SPIKE_CRASH_CKPT_PROVIDER_READY"
	crashCheckpointRunID                   = "workflow-run-crash-ckpt"
	crashCheckpointNodeRunID               = "node-run-crash-ckpt"
	crashCheckpointInterruptedAttempt      = "attempt-before-crash-ckpt"
	crashCheckpointID                      = "checkpoint-crash-ckpt-1"
	crashCheckpointContextSnapshotID       = "context-crash-ckpt-1"
	crashCheckpointReplacementAttempt      = "attempt-after-crash-ckpt"
)

// TestSPK03HardCrashJoinsCheckpointContextRecovery joins the two pieces that
// were previously proven separately: a genuinely killed worker OS process
// (see crash_resume_integration_test.go) and checkpoint/context recovery
// (see checkpoint_store_test.go). The worker child spawns a real fake
// provider grandchild process, persists a Checkpoint+ContextSnapshot only
// after it hears back from that process, then is hard-killed with no
// graceful shutdown. A replacement worker recovers purely from SQLite,
// always calling AgentExecutor.Start (never Resume), from a new attempt id.
//
// Known limitation (V0-02 scope, see docs/design/02-v0-spike-verdict.md):
// this does not transition the interrupted execution_attempts row to
// LOST/INDETERMINATE — no store or domain function does that anywhere in
// the codebase yet, and it is not one of V0-02's listed steps. The row is
// left at state='RUNNING' after the crash; only the replacement attempt id
// and the exactly-once terminal transition are asserted.
func TestSPK03HardCrashJoinsCheckpointContextRecovery(t *testing.T) {
	if os.Getenv(crashCheckpointProviderModeEnvironment) == "1" {
		runCrashCheckpointFakeProviderProcess(t)
		return
	}
	if os.Getenv(crashWorkerModeEnvironment) == "checkpoint-then-hang" {
		runCrashCheckpointWorkerProcess(t)
		return
	}

	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "agentkit-crash-checkpoint.db")
	store, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("open initial store: %v", err)
	}
	seedCrashResumeOwners(t, ctx, store)

	definition := crashResumeWorkflowDefinition()
	versionOne := compileCrashResumeWorkflowVersion(t, definition, "workflow-version-crash-ckpt-v1", 1, crashResumeWorkflowDocumentV1(), "skill-v1")
	if _, err := store.PublishWorkflowVersion(ctx, definition, versionOne); err != nil {
		t.Fatalf("publish workflow v1: %v", err)
	}
	run, err := runtime.NewWorkflowRun(
		crashCheckpointRunID, "project-crash", "work-item-crash", versionOne, "family-crash", 1,
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
		OccurredAt:      time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("move workflow run to RUNNING: %v", err)
	}
	if _, err := store.EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID:             "job-crash-ckpt",
		ProjectID:      "project-crash",
		Kind:           "EXECUTE_NODE",
		AggregateType:  "WorkflowRun",
		AggregateID:    string(run.ID),
		Payload:        json.RawMessage(`{"runId":"` + crashCheckpointRunID + `"}`),
		MaxClaims:      3,
		IdempotencyKey: "execute-workflow-run-crash-ckpt",
	}); err != nil {
		t.Fatalf("enqueue durable job: %v", err)
	}
	seedCrashCheckpointNodeRunAndAttempt(t, ctx, store, run.ID)

	if err := store.Close(); err != nil {
		t.Fatalf("close initial store: %v", err)
	}

	const crashedWorkerTTL = 900 * time.Millisecond
	ready := startAndHardKillCrashWorker(t, databasePath, crashedWorkerTTL, versionOne.ID(), versionOne.ContentHash(), "checkpoint-then-hang")
	if ready.JobID != "job-crash-ckpt" {
		t.Fatalf("crashed worker claimed job %s, want job-crash-ckpt", ready.JobID)
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
	waitForExpiredJobRecovery(t, ctx, restarted, ready.LeaseUntil)

	jobAfterRestart, replacementLease, err := restarted.ClaimJob(ctx, "worker-after-restart", 5*time.Second)
	if err != nil {
		t.Fatalf("replacement worker claim: %v", err)
	}
	if jobAfterRestart.ID != ready.JobID || jobAfterRestart.ClaimCount != 2 {
		t.Fatalf("replacement claim = job %s count %d, want %s count 2",
			jobAfterRestart.ID, jobAfterRestart.ClaimCount, ready.JobID)
	}

	// The checkpoint and its ContextSnapshot must have survived the hard
	// kill: they were written by a process whose SQLite connection was never
	// closed and which was terminated with no graceful shutdown.
	persistedCheckpoint, err := restarted.LoadLatestCheckpoint(ctx, crashCheckpointInterruptedAttempt)
	if err != nil {
		t.Fatalf("load persisted checkpoint after crash: %v", err)
	}
	if persistedCheckpoint.ID != crashCheckpointID || persistedCheckpoint.Sequence != 1 ||
		persistedCheckpoint.ContextSnapshotID != crashCheckpointContextSnapshotID {
		t.Fatalf("unexpected persisted checkpoint after crash: %+v", persistedCheckpoint)
	}
	if _, err := restarted.LoadContextSnapshot(ctx, persistedCheckpoint.ContextSnapshotID); err != nil {
		t.Fatalf("load persisted context snapshot after crash: %v", err)
	}

	// Replacement execution: always a new attempt id, always Start, never
	// Resume, sourced only from the durable ContextSnapshot/Checkpoint chain
	// — nothing carried over from the killed process's memory.
	recorder := &freshRecoveryExecutor{}
	result, err := worker.StartFreshFromLatestCheckpoint(
		ctx, restarted, recorder, crashCheckpointInterruptedAttempt,
		ports.AgentExecutionRequest{AttemptID: crashCheckpointReplacementAttempt, WorkingDirectory: t.TempDir(), Timeout: time.Second},
		ports.AgentEventSinkFunc(func(context.Context, ports.AgentEvent) error { return nil }),
	)
	if err != nil {
		t.Fatalf("StartFreshFromLatestCheckpoint() error = %v", err)
	}
	if result.AttemptID != crashCheckpointReplacementAttempt || recorder.startCalls != 1 || recorder.resumeCalls != 0 {
		t.Fatalf("replacement execution = result:%+v start:%d resume:%d", result, recorder.startCalls, recorder.resumeCalls)
	}
	if crashCheckpointReplacementAttempt == persistedCheckpoint.AttemptID {
		t.Fatalf("replacement attempt id %s must differ from the interrupted attempt id", crashCheckpointReplacementAttempt)
	}

	assertExactlyOneTerminalWorkflowTransition(t, ctx, restarted, run.ID, replacementLease)

	var durableState string
	if err := restarted.db.QueryRowContext(ctx,
		`SELECT state FROM durable_jobs WHERE id = ?`, ready.JobID,
	).Scan(&durableState); err != nil {
		t.Fatalf("read final durable job state: %v", err)
	}
	if durableState != string(ports.JobSucceeded) {
		t.Fatalf("final durable job state = %s, want SUCCEEDED", durableState)
	}
}

// runCrashCheckpointWorkerProcess is the crashed-worker child. It never
// closes its Store and is hard-killed by the parent (see
// startAndHardKillCrashWorker), so nothing it does after printing the READY
// line, including releasing the fake-provider grandchild, can be relied on.
func runCrashCheckpointWorkerProcess(t *testing.T) {
	t.Helper()
	databasePath := strings.TrimSpace(os.Getenv(crashWorkerDBEnvironment))
	if databasePath == "" {
		t.Fatal("crash checkpoint worker database path is required")
	}
	ttl, err := time.ParseDuration(os.Getenv(crashWorkerTTLEnvironment))
	if err != nil || ttl <= 0 {
		t.Fatalf("parse crash checkpoint worker TTL: %v", err)
	}

	ctx := context.Background()
	store, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("crash checkpoint worker open store: %v", err)
	}
	// Intentionally no Close: the parent terminates this process while its
	// SQLite connection, durable lease and just-written checkpoint are live.

	run, err := store.LoadWorkflowRun(ctx, crashCheckpointRunID)
	if err != nil {
		t.Fatalf("crash checkpoint worker load workflow run: %v", err)
	}
	_, lease, err := store.ClaimJob(ctx, "worker-before-crash", ttl)
	if err != nil {
		t.Fatalf("crash checkpoint worker claim job: %v", err)
	}

	providerEvent := spawnCrashCheckpointFakeProvider(t)

	revisions, err := workspace.NewRevisionSet([]workspace.Revision{{
		RepositoryID: project.RepositoryID("repo-crash-ckpt"), VCSObjectID: "rev-crash-ckpt-1", WorkspaceGeneration: 1,
	}})
	if err != nil {
		t.Fatalf("crash checkpoint worker build revision set: %v", err)
	}
	snapshot, err := runtime.NewContextSnapshot(runtime.ContextSnapshotInput{
		ID:        crashCheckpointContextSnapshotID,
		AttemptID: crashCheckpointInterruptedAttempt,
		Messages:  []runtime.ContextMessage{{Role: runtime.ContextRoleSystem, Content: "provider event: " + providerEvent}},
		Revisions: revisions,
		CreatedAt: time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("crash checkpoint worker build context snapshot: %v", err)
	}
	// Persisted before the worker signals READY, i.e. strictly before the
	// parent hard-kills it: the checkpoint is durable at the moment of crash.
	if _, err := store.StoreContextSnapshot(ctx, "project-crash", snapshot); err != nil {
		t.Fatalf("crash checkpoint worker persist context snapshot: %v", err)
	}
	checkpoint, err := runtime.NewCheckpoint(
		crashCheckpointID, run.ID, crashCheckpointNodeRunID, crashCheckpointInterruptedAttempt,
		1, 1, snapshot.ID(), revisions, "sha256:crash-checkpoint-shared-state", nil,
		time.Date(2026, 8, 28, 12, 0, 1, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("crash checkpoint worker build checkpoint: %v", err)
	}
	if _, err := store.StoreCheckpoint(ctx, checkpoint); err != nil {
		t.Fatalf("crash checkpoint worker persist checkpoint: %v", err)
	}

	// Same READY line shape as runCrashWorkerProcess: startAndHardKillCrashWorker
	// and parseCrashWorkerReady are reused as-is.
	fmt.Printf(
		"%s %s %d %s %s %s\n",
		crashWorkerReadyPrefix,
		lease.JobID,
		lease.Token,
		lease.LeaseUntil.UTC().Format(time.RFC3339Nano),
		run.WorkflowVersionID,
		run.WorkflowVersionHash,
	)
	_ = os.Stdout.Sync()
	select {}
}

// spawnCrashCheckpointFakeProvider starts the fake-provider grandchild and
// waits for its single readiness line. The grandchild is never explicitly
// waited on or killed: the worker itself is about to be hard-killed with no
// cleanup path, exactly like a real crash, so the provider is left to
// self-terminate on its own bounded timer instead.
func spawnCrashCheckpointFakeProvider(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve current test executable for fake provider: %v", err)
	}
	command := exec.Command(
		executable,
		"-test.run=^TestSPK03HardCrashJoinsCheckpointContextRecovery$",
		"-test.v",
		"-test.timeout=30s",
	)
	command.Env = append(os.Environ(), crashCheckpointProviderModeEnvironment+"=1")
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("create fake provider stdout pipe: %v", err)
	}
	if err := command.Start(); err != nil {
		t.Fatalf("start fake provider process: %v", err)
	}

	type scanResult struct {
		event string
		err   error
	}
	readyChannel := make(chan scanResult, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, crashCheckpointProviderReadyPrefix+" ") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) != 2 {
				readyChannel <- scanResult{err: fmt.Errorf("invalid fake provider READY line %q", line)}
				return
			}
			readyChannel <- scanResult{event: fields[1]}
			return
		}
		readyChannel <- scanResult{err: fmt.Errorf("fake provider exited before signalling a checkpoint-worthy event: %v", scanner.Err())}
	}()

	select {
	case result := <-readyChannel:
		if result.err != nil {
			t.Fatalf("fake provider readiness: %v", result.err)
		}
		return result.event
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for fake provider READY")
		return ""
	}
}

// runCrashCheckpointFakeProviderProcess plays the fake-provider grandchild.
// It deliberately does not touch SQLite, ports.ProcessSupervisor or any
// AgentExecutor/provider-adapter code: V0-02 is scoped to the crash
// fixture/worker recovery/checkpoint join, not the provider domain, so this
// is a bare fixture process communicating over stdout only.
func runCrashCheckpointFakeProviderProcess(t *testing.T) {
	t.Helper()
	fmt.Printf("%s provider-checkpoint-event-1\n", crashCheckpointProviderReadyPrefix)
	_ = os.Stdout.Sync()
	time.Sleep(2 * time.Second)
}

// seedCrashCheckpointNodeRunAndAttempt seeds the one node_runs and one
// execution_attempts row that checkpoints/context_snapshots have real
// foreign keys against. No production Store method creates these yet (only
// the runtime/durable-job/lease surface is wired at this point in the
// spike), so this mirrors the raw-SQL seeding already used by
// seedSchedulingFixture in scheduling_test.go.
func seedCrashCheckpointNodeRunAndAttempt(t *testing.T, ctx context.Context, store *Store, runID runtime.WorkflowRunID) {
	t.Helper()
	const timestamp = "2026-08-28T12:00:00Z"
	// Two separate statements (not one semicolon-joined multi-statement
	// Exec): the driver's multi-statement placeholder distribution is only
	// exercised elsewhere in this package with literal identity columns and
	// `?` reserved for the repeated timestamp pair, and did not reliably
	// bind `?` ids/foreign keys across statement boundaries here.
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO node_runs(
  id, run_id, node_key, activation_sequence, iteration, state,
  input_state_hash, version, created_at, updated_at
) VALUES (?, ?, 'implement', 1, 0, 'RUNNING', 'sha256:input-crash-ckpt', 1, ?, ?);`,
		crashCheckpointNodeRunID, runID, timestamp, timestamp,
	); err != nil {
		t.Fatalf("seed crash checkpoint node run: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `
INSERT INTO execution_attempts(
  id, node_run_id, attempt_no, state, provider_key, execution_profile_hash,
  input_revision_set_json, version, created_at, updated_at
) VALUES (?, ?, 1, 'RUNNING', 'codex', 'sha256:profile-crash-ckpt', '[]', 1, ?, ?);`,
		crashCheckpointInterruptedAttempt, crashCheckpointNodeRunID, timestamp, timestamp,
	); err != nil {
		t.Fatalf("seed crash checkpoint execution attempt: %v", err)
	}
}
