package sqlite

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

const (
	crashWorkerModeEnvironment = "AGENTKIT_SPIKE_CRASH_WORKER"
	crashWorkerDBEnvironment   = "AGENTKIT_SPIKE_CRASH_DB"
	crashWorkerTTLEnvironment  = "AGENTKIT_SPIKE_CRASH_TTL"
	crashWorkerReadyPrefix     = "AGENTKIT_SPIKE_CRASH_READY"
)

// TestCrashRestartReclaimsLeasedJobAndPreservesPinnedWorkflow uses a real
// child OS process as the first worker. The child deliberately never closes
// its Store: the parent terminates it with Process.Kill after the durable job
// claim has committed. This exercises SQLite/WAL recovery and lease fencing
// rather than simulating a crash by returning from a goroutine.
func TestCrashRestartReclaimsLeasedJobAndPreservesPinnedWorkflow(t *testing.T) {
	if os.Getenv(crashWorkerModeEnvironment) == "claim-and-hang" {
		runCrashWorkerProcess(t)
		return
	}

	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "agentkit-crash.db")
	store, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("open initial store: %v", err)
	}
	seedCrashResumeOwners(t, ctx, store)

	definition := crashResumeWorkflowDefinition()
	versionOne := compileCrashResumeWorkflowVersion(
		t,
		definition,
		"workflow-version-crash-v1",
		1,
		crashResumeWorkflowDocumentV1(),
		"skill-v1",
	)
	if _, err := store.PublishWorkflowVersion(ctx, definition, versionOne); err != nil {
		t.Fatalf("publish workflow v1: %v", err)
	}
	run, err := runtime.NewWorkflowRun(
		"workflow-run-crash",
		"project-crash",
		"work-item-crash",
		versionOne,
		"family-crash",
		1,
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
		OccurredAt:      time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("move workflow run to RUNNING: %v", err)
	}
	if _, err := store.EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID:             "job-crash",
		ProjectID:      "project-crash",
		Kind:           "EXECUTE_NODE",
		AggregateType:  "WorkflowRun",
		AggregateID:    string(run.ID),
		Payload:        json.RawMessage(`{"runId":"workflow-run-crash"}`),
		MaxClaims:      3,
		IdempotencyKey: "execute-workflow-run-crash",
	}); err != nil {
		t.Fatalf("enqueue durable job: %v", err)
	}

	// No coordinator connection remains open while the worker owns the job.
	// The only open Store below belongs to the child process that gets killed.
	if err := store.Close(); err != nil {
		t.Fatalf("close initial store: %v", err)
	}

	const crashedWorkerTTL = 900 * time.Millisecond
	ready := startAndHardKillCrashWorker(
		t,
		databasePath,
		crashedWorkerTTL,
		versionOne.ID(),
		versionOne.ContentHash(),
		"claim-and-hang",
	)
	if ready.JobID != "job-crash" {
		t.Fatalf("crashed worker claimed job %s, want job-crash", ready.JobID)
	}
	if ready.WorkflowVersionID != versionOne.ID() || ready.WorkflowVersionHash != versionOne.ContentHash() {
		t.Fatalf(
			"crashed worker loaded workflow pin %s/%s, want %s/%s",
			ready.WorkflowVersionID,
			ready.WorkflowVersionHash,
			versionOne.ID(),
			versionOne.ContentHash(),
		)
	}

	// Opening after the hard kill represents a fresh coordinator process. Poll
	// the database clock until the lease expires; RecoverExpiredJobs must not
	// depend on any in-memory state from the killed worker.
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
	if replacementLease.Token <= ready.LeaseToken {
		t.Fatalf("replacement lease token = %d, want greater than stale token %d",
			replacementLease.Token, ready.LeaseToken)
	}

	staleLease := ports.JobLease{
		JobID: ready.JobID,
		Owner: "worker-before-crash",
		Token: ready.LeaseToken,
	}
	if err := restarted.CompleteJob(ctx, staleLease); !errors.Is(err, ports.ErrJobLeaseLost) {
		t.Fatalf("stale CompleteJob() error = %v, want ErrJobLeaseLost", err)
	}
	if _, err := restarted.FinalizeWorkflowRun(ctx, ports.WorkerWorkflowRunFinalization{
		Transition: ports.WorkflowRunTransition{
			RunID:           run.ID,
			ExpectedState:   runtime.WorkflowRunRunning,
			ExpectedVersion: 2,
			NextState:       runtime.WorkflowRunFailed,
			SharedState:     json.RawMessage(`{"terminal":"stale-worker"}`),
			OccurredAt:      time.Date(2026, 8, 28, 10, 0, 30, 0, time.UTC),
		},
		JobLease:      staleLease,
		EventID:       "event-stale-worker",
		CorrelationID: "crash-restart-stale",
	}); !errors.Is(err, ports.ErrJobLeaseLost) {
		t.Fatalf("stale FinalizeWorkflowRun() error = %v, want ErrJobLeaseLost", err)
	}
	stillRunning, err := restarted.LoadWorkflowRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("load workflow run after rejected stale completion: %v", err)
	}
	if stillRunning.State != runtime.WorkflowRunRunning || stillRunning.Version != 2 {
		t.Fatalf("stale worker changed workflow run: state=%s version=%d",
			stillRunning.State, stillRunning.Version)
	}

	// Publishing v2 after restart must not move the already-running R1 pin.
	versionTwo := compileCrashResumeWorkflowVersion(
		t,
		definition,
		"workflow-version-crash-v2",
		2,
		crashResumeWorkflowDocumentV2(),
		"skill-v2",
	)
	if _, err := restarted.PublishWorkflowVersion(ctx, definition, versionTwo); err != nil {
		t.Fatalf("publish workflow v2 after restart: %v", err)
	}
	pinnedRun, err := restarted.LoadWorkflowRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("load pinned workflow run after v2 publish: %v", err)
	}
	if pinnedRun.WorkflowVersionID != versionOne.ID() || pinnedRun.WorkflowVersionHash != versionOne.ContentHash() {
		t.Fatalf("workflow run pin moved after restart/v2 publish: %s/%s",
			pinnedRun.WorkflowVersionID, pinnedRun.WorkflowVersionHash)
	}
	pinnedVersion, err := restarted.LoadWorkflowVersion(ctx, pinnedRun.WorkflowVersionID)
	if err != nil {
		t.Fatalf("load exact pinned workflow version: %v", err)
	}
	if pinnedVersion.ContentHash() != versionOne.ContentHash() ||
		!bytes.Equal(pinnedVersion.CanonicalContent(), versionOne.CanonicalContent()) {
		t.Fatalf("pinned workflow snapshot/hash changed across crash restart")
	}

	assertExactlyOneTerminalWorkflowTransition(t, ctx, restarted, run.ID, replacementLease)

	var durableState string
	var durableToken uint64
	if err := restarted.db.QueryRowContext(ctx,
		`SELECT state, lease_token FROM durable_jobs WHERE id = ?`, ready.JobID,
	).Scan(&durableState, &durableToken); err != nil {
		t.Fatalf("read final durable job state: %v", err)
	}
	if durableState != string(ports.JobSucceeded) || durableToken != replacementLease.Token {
		t.Fatalf("final durable job = %s@%d, want SUCCEEDED@%d",
			durableState, durableToken, replacementLease.Token)
	}
}

// TestCrashAfterAtomicFinalizationDoesNotDuplicateTerminalState covers the
// apparent boundary "outcome committed, job not acknowledged". There is no
// durable boundary there: FinalizeWorkflowRun commits both records in one
// SQLite transaction. The child is killed immediately after that transaction
// and a replacement process must observe one terminal run and no job to claim.
func TestCrashAfterAtomicFinalizationDoesNotDuplicateTerminalState(t *testing.T) {
	if os.Getenv(crashWorkerModeEnvironment) == "finalize-and-hang" {
		runCrashWorkerProcess(t)
		return
	}
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "agentkit-finalized-crash.db")
	store, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	seedCrashResumeOwners(t, ctx, store)
	definition := crashResumeWorkflowDefinition()
	version := compileCrashResumeWorkflowVersion(t, definition, "workflow-version-finalized-crash", 1, crashResumeWorkflowDocumentV1(), "skill-v1")
	if _, err := store.PublishWorkflowVersion(ctx, definition, version); err != nil {
		t.Fatal(err)
	}
	run, err := runtime.NewWorkflowRun("workflow-run-crash", "project-crash", "work-item-crash", version, "family-crash", 1, json.RawMessage(`{"checkpoint":"created"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.StartWorkflowRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompareAndSwapWorkflowRun(ctx, ports.WorkflowRunTransition{
		RunID: run.ID, ExpectedState: runtime.WorkflowRunCreated, ExpectedVersion: 1,
		NextState: runtime.WorkflowRunRunning, SharedState: json.RawMessage(`{"checkpoint":"dispatched"}`),
		OccurredAt: time.Date(2026, 8, 28, 11, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: "job-crash", ProjectID: "project-crash", Kind: "EXECUTE_NODE", AggregateType: "WorkflowRun",
		AggregateID: string(run.ID), Payload: json.RawMessage(`{"runId":"workflow-run-crash"}`),
		MaxClaims: 3, IdempotencyKey: "atomic-finalization-crash",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	_ = startAndHardKillCrashWorker(t, databasePath, 5*time.Second, version.ID(), version.ContentHash(), "finalize-and-hang")

	restarted, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	terminal, err := restarted.LoadWorkflowRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if terminal.State != runtime.WorkflowRunSucceeded || terminal.Version != 3 {
		t.Fatalf("restarted run = state:%s version:%d, want SUCCEEDED@3", terminal.State, terminal.Version)
	}
	var jobState string
	if err := restarted.db.QueryRowContext(ctx, `SELECT state FROM durable_jobs WHERE id = 'job-crash'`).Scan(&jobState); err != nil {
		t.Fatal(err)
	}
	if jobState != string(ports.JobSucceeded) {
		t.Fatalf("job after crash = %s, want SUCCEEDED", jobState)
	}
	if recovered, err := restarted.RecoverExpiredJobs(ctx); err != nil || recovered != 0 {
		t.Fatalf("RecoverExpiredJobs() = %d, %v; want 0, nil", recovered, err)
	}
	if _, _, err := restarted.ClaimJob(ctx, "replacement", time.Second); !errors.Is(err, ports.ErrNoJobAvailable) {
		t.Fatalf("replacement claimed completed job: %v", err)
	}
	var events int
	if err := restarted.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM domain_events WHERE aggregate_type = 'WorkflowRun' AND aggregate_id = ?`, run.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("terminal event count = %d, want 1", events)
	}
}

type crashWorkerReady struct {
	JobID               ports.JobID
	LeaseToken          uint64
	LeaseUntil          time.Time
	WorkflowVersionID   workflow.WorkflowVersionID
	WorkflowVersionHash string
}

func runCrashWorkerProcess(t *testing.T) {
	t.Helper()
	mode := os.Getenv(crashWorkerModeEnvironment)
	databasePath := strings.TrimSpace(os.Getenv(crashWorkerDBEnvironment))
	if databasePath == "" {
		t.Fatal("crash worker database path is required")
	}
	ttl, err := time.ParseDuration(os.Getenv(crashWorkerTTLEnvironment))
	if err != nil || ttl <= 0 {
		t.Fatalf("parse crash worker TTL: %v", err)
	}

	ctx := context.Background()
	store, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("crash worker open store: %v", err)
	}
	// Intentionally no Close: the parent must terminate this process while its
	// SQLite connection and durable lease are still live.
	run, err := store.LoadWorkflowRun(ctx, "workflow-run-crash")
	if err != nil {
		t.Fatalf("crash worker load workflow run: %v", err)
	}
	_, lease, err := store.ClaimJob(ctx, "worker-before-crash", ttl)
	if err != nil {
		t.Fatalf("crash worker claim job: %v", err)
	}
	if mode == "finalize-and-hang" {
		if _, err := store.FinalizeWorkflowRun(ctx, ports.WorkerWorkflowRunFinalization{
			Transition: ports.WorkflowRunTransition{
				RunID: run.ID, ExpectedState: runtime.WorkflowRunRunning, ExpectedVersion: 2,
				NextState: runtime.WorkflowRunSucceeded, SharedState: json.RawMessage(`{"terminal":"child"}`),
				OccurredAt: time.Date(2026, 8, 28, 11, 0, 1, 0, time.UTC),
			},
			JobLease: lease, EventID: "event-child-finalized", CorrelationID: "atomic-finalization-crash",
		}); err != nil {
			t.Fatalf("crash worker finalize: %v", err)
		}
	}
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

func startAndHardKillCrashWorker(
	t *testing.T,
	databasePath string,
	ttl time.Duration,
	expectedVersionID workflow.WorkflowVersionID,
	expectedVersionHash string,
	mode string,
) crashWorkerReady {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve current test executable: %v", err)
	}
	testName := "^TestCrashRestartReclaimsLeasedJobAndPreservesPinnedWorkflow$"
	if mode == "finalize-and-hang" {
		testName = "^TestCrashAfterAtomicFinalizationDoesNotDuplicateTerminalState$"
	}
	command := exec.Command(
		executable,
		"-test.run="+testName,
		"-test.v",
		"-test.timeout=30s",
	)
	command.Env = append(os.Environ(),
		crashWorkerModeEnvironment+"="+mode,
		crashWorkerDBEnvironment+"="+databasePath,
		crashWorkerTTLEnvironment+"="+ttl.String(),
	)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("create crash worker stdout pipe: %v", err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatalf("start crash worker process: %v", err)
	}

	waited := false
	t.Cleanup(func() {
		if waited {
			return
		}
		_ = command.Process.Kill()
		_ = command.Wait()
	})

	readyChannel := make(chan crashWorkerReady, 1)
	type scanResult struct {
		output string
		err    error
	}
	scanDone := make(chan scanResult, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		var output strings.Builder
		for scanner.Scan() {
			line := scanner.Text()
			output.WriteString(line)
			output.WriteByte('\n')
			if !strings.HasPrefix(line, crashWorkerReadyPrefix+" ") {
				continue
			}
			ready, parseErr := parseCrashWorkerReady(line)
			if parseErr != nil {
				scanDone <- scanResult{output: output.String(), err: parseErr}
				return
			}
			readyChannel <- ready
		}
		scanDone <- scanResult{output: output.String(), err: scanner.Err()}
	}()

	var ready crashWorkerReady
	select {
	case ready = <-readyChannel:
	case result := <-scanDone:
		t.Fatalf("crash worker exited before READY: scan=%v stdout=%q stderr=%q",
			result.err, result.output, stderr.String())
	case <-time.After(10 * time.Second):
		t.Fatalf("timeout waiting for crash worker READY; stderr=%q", stderr.String())
	}
	if ready.WorkflowVersionID != expectedVersionID || ready.WorkflowVersionHash != expectedVersionHash {
		t.Fatalf("child observed unexpected workflow pin: %s/%s",
			ready.WorkflowVersionID, ready.WorkflowVersionHash)
	}

	// Process.Kill is intentionally used instead of context cancellation or a
	// protocol shutdown, so no defer in the worker can release its lease.
	if err := command.Process.Kill(); err != nil {
		t.Fatalf("hard-kill crash worker: %v", err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("hard-killed crash worker exited successfully, want forced termination")
	}
	waited = true
	return ready
}

func parseCrashWorkerReady(line string) (crashWorkerReady, error) {
	fields := strings.Fields(line)
	if len(fields) != 6 || fields[0] != crashWorkerReadyPrefix {
		return crashWorkerReady{}, fmt.Errorf("invalid crash worker READY line %q", line)
	}
	token, err := strconv.ParseUint(fields[2], 10, 64)
	if err != nil {
		return crashWorkerReady{}, fmt.Errorf("parse crash worker lease token: %w", err)
	}
	leaseUntil, err := time.Parse(time.RFC3339Nano, fields[3])
	if err != nil {
		return crashWorkerReady{}, fmt.Errorf("parse crash worker lease expiry: %w", err)
	}
	return crashWorkerReady{
		JobID:               ports.JobID(fields[1]),
		LeaseToken:          token,
		LeaseUntil:          leaseUntil,
		WorkflowVersionID:   workflow.WorkflowVersionID(fields[4]),
		WorkflowVersionHash: fields[5],
	}, nil
}

func waitForExpiredJobRecovery(
	t *testing.T,
	ctx context.Context,
	store *Store,
	leaseUntil time.Time,
) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		recovered, err := store.RecoverExpiredJobs(ctx)
		if err != nil {
			t.Fatalf("recover expired job after crash: %v", err)
		}
		if recovered == 1 {
			return
		}
		if recovered != 0 {
			t.Fatalf("recovered %d jobs, want exactly one crashed job", recovered)
		}
		if time.Now().After(deadline) {
			t.Fatalf("lease was not recovered by deadline; database lease_until=%s",
				leaseUntil.Format(time.RFC3339Nano))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func assertExactlyOneTerminalWorkflowTransition(
	t *testing.T,
	ctx context.Context,
	store *Store,
	runID runtime.WorkflowRunID,
	lease ports.JobLease,
) {
	t.Helper()
	transitions := []ports.WorkerWorkflowRunFinalization{
		{
			Transition: ports.WorkflowRunTransition{
				RunID:           runID,
				ExpectedState:   runtime.WorkflowRunRunning,
				ExpectedVersion: 2,
				NextState:       runtime.WorkflowRunSucceeded,
				SharedState:     json.RawMessage(`{"terminal":"worker-a"}`),
				OccurredAt:      time.Date(2026, 8, 28, 10, 1, 0, 0, time.UTC),
			},
			JobLease:      lease,
			EventID:       "event-worker-a",
			CorrelationID: "crash-restart-terminal",
		},
		{
			Transition: ports.WorkflowRunTransition{
				RunID:           runID,
				ExpectedState:   runtime.WorkflowRunRunning,
				ExpectedVersion: 2,
				NextState:       runtime.WorkflowRunFailed,
				SharedState:     json.RawMessage(`{"terminal":"worker-b"}`),
				OccurredAt:      time.Date(2026, 8, 28, 10, 1, 0, 1, time.UTC),
			},
			JobLease:      lease,
			EventID:       "event-worker-b",
			CorrelationID: "crash-restart-terminal",
		},
	}

	start := make(chan struct{})
	results := make(chan error, len(transitions))
	var workers sync.WaitGroup
	for _, transition := range transitions {
		transition := transition
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, err := store.FinalizeWorkflowRun(ctx, transition)
			results <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	winners := 0
	rejected := 0
	for err := range results {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, ports.ErrOptimisticConflict), errors.Is(err, ports.ErrJobLeaseLost):
			rejected++
		default:
			t.Fatalf("terminal workflow CAS returned unexpected error: %v", err)
		}
	}
	if winners != 1 || rejected != 1 {
		t.Fatalf("terminal workflow finalization results = winners:%d rejected:%d, want 1/1",
			winners, rejected)
	}
	var eventCount int
	if err := store.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM domain_events
WHERE aggregate_type = 'WorkflowRun' AND aggregate_id = ? AND event_type = 'WORKFLOW_RUN_FINALIZED'`, runID).Scan(&eventCount); err != nil {
		t.Fatalf("count finalization events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("finalization events = %d, want exactly 1", eventCount)
	}

	terminal, err := store.LoadWorkflowRun(ctx, runID)
	if err != nil {
		t.Fatalf("load terminal workflow run: %v", err)
	}
	if terminal.Version != 3 || terminal.FinishedAt == nil ||
		(terminal.State != runtime.WorkflowRunSucceeded && terminal.State != runtime.WorkflowRunFailed) {
		t.Fatalf("terminal workflow run = state:%s version:%d finished:%v",
			terminal.State, terminal.Version, terminal.FinishedAt)
	}
}

func seedCrashResumeOwners(t *testing.T, ctx context.Context, store *Store) {
	t.Helper()
	const timestamp = "2026-08-28T00:00:00Z"
	_, err := store.db.ExecContext(ctx, `
INSERT INTO projects(id, name, status, version, created_at, updated_at)
VALUES ('project-crash', 'Crash/restart spike', 'ACTIVE', 1, ?, ?);

INSERT INTO task_families(
  id, project_id, root_work_item_id, scope_version, status, version, created_at, updated_at
) VALUES ('family-crash', 'project-crash', 'work-item-crash', 1, 'ACTIVE', 1, ?, ?);

INSERT INTO work_items(
  id, project_id, kind, parent_id, family_id, title, status, version, created_at, updated_at
) VALUES (
  'work-item-crash', 'project-crash', 'ROOT', NULL, 'family-crash',
  'Crash/restart spike', 'ACTIVE', 1, ?, ?
);`,
		timestamp, timestamp,
		timestamp, timestamp,
		timestamp, timestamp,
	)
	if err != nil {
		t.Fatalf("seed crash/restart owners: %v", err)
	}
}

func crashResumeWorkflowDefinition() workflow.WorkflowDefinition {
	projectID := project.ProjectID("project-crash")
	return workflow.WorkflowDefinition{
		ID:        "workflow-definition-crash",
		ProjectID: &projectID,
		Name:      "Crash/restart workflow",
		Status:    workflow.DefinitionActive,
		Version:   1,
	}
}

func compileCrashResumeWorkflowVersion(
	t *testing.T,
	definition workflow.WorkflowDefinition,
	id workflow.WorkflowVersionID,
	versionNumber uint64,
	document workflow.WorkflowDocument,
	dependencyVersion string,
) workflow.WorkflowVersion {
	t.Helper()
	version, err := workflow.Compile(definition, workflow.PublishRequest{
		VersionID:     id,
		VersionNumber: versionNumber,
		Document:      document,
		Dependencies: workflow.DependencyManifest{Pins: []workflow.DependencyPin{
			{
				Kind:    "skill",
				Key:     "implement",
				Version: dependencyVersion,
				Hash:    "sha256:" + dependencyVersion,
			},
		}},
		PublishedBy: "crash-restart-spike",
		PublishedAt: time.Date(2026, 8, 28, int(versionNumber), 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("compile crash/restart workflow %s: %v", id, err)
	}
	return version
}

func crashResumeWorkflowDocumentV1() workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"execute"}},
			{Key: "implement", Type: workflow.NodeAgent, Outcomes: []string{"done"}, ExecutorRef: "agent/default"},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-implement", From: "start", Outcome: "execute", To: "implement"},
			{Key: "implement-end", From: "implement", Outcome: "done", To: "end"},
		},
	}
}

func crashResumeWorkflowDocumentV2() workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"execute"}},
			{Key: "implement", Type: workflow.NodeAgent, Outcomes: []string{"verify"}, ExecutorRef: "agent/default"},
			{Key: "verify", Type: workflow.NodeCommand, Outcomes: []string{"done"}, ExecutorRef: "command/test"},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-implement", From: "start", Outcome: "execute", To: "implement"},
			{Key: "implement-verify", From: "implement", Outcome: "verify", To: "verify"},
			{Key: "verify-end", From: "verify", Outcome: "done", To: "end"},
		},
	}
}
