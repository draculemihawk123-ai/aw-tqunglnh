package spikeacceptance

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
	"runtime"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/worker"
	domainruntime "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// runSPK04Scenario closes SPK-04: SPK-03's flow (dispatch intent -> claim ->
// spawn process -> checkpoint -> process exits -> commit outcome -> dispatch
// next) run to completion seven times, hard-killing a real cmd/spike-worker
// child process at each of the six standard fault points
// (docs/spikes/01-go-core-spike-plan.md §9-§10) — boundary 5 gets both its
// read-only and mutating variant. Every worker behavior comes from
// internal/adapters/sqlite/crashworker.go, the exact same non-test code the
// crash_resume_*_integration_test.go files call; this scenario is the
// standalone-binary path those tests cannot exercise (agentkit-spike
// acceptance --full runs outside `go test`, so it has no test binary to
// re-invoke as its crashed worker).
func runSPK04Scenario(ctx context.Context, sc ScenarioContext) (SPKResult, error) {
	started := time.Now().UTC()
	if sc.Binaries.SpikeWorker == "" {
		return SPKResult{}, fmt.Errorf("spk04: ScenarioBinaries.SpikeWorker is required")
	}
	spikeWorkerPath := sc.Binaries.SpikeWorker

	var assertions []Assertion
	passed := true
	record := func(name string, ok bool, detail string) {
		assertions = append(assertions, Assertion{Name: name, Passed: ok, Detail: detail})
		if !ok {
			passed = false
		}
	}

	type boundaryOutcome struct {
		Boundary string `json:"boundary"`
		Mode     string `json:"mode"`
		JobID    string `json:"jobId"`
		Killed   bool   `json:"killed"`
	}
	var outcomes []boundaryOutcome
	run := func(boundary, mode string, fn func() (sqlite.CrashWorkerReady, error)) {
		ready, err := fn()
		outcomes = append(outcomes, boundaryOutcome{Boundary: boundary, Mode: mode, JobID: string(ready.JobID), Killed: err == nil})
		if err != nil {
			record(fmt.Sprintf("boundary %s (%s) completed without a harness error", boundary, mode), false, err.Error())
		}
	}

	run("1/6 before_intent_job_commit", sqlite.CrashModeBeforeIntentCommit, func() (sqlite.CrashWorkerReady, error) {
		return spk04Boundary1BeforeIntentCommit(ctx, spikeWorkerPath, record)
	})
	run("2/6 after_job_commit_before_claim", sqlite.CrashModeAfterIntentCommit, func() (sqlite.CrashWorkerReady, error) {
		return spk04Boundary2AfterIntentCommit(ctx, spikeWorkerPath, record)
	})
	run("3/6 after_job_claim_before_process_spawn", sqlite.CrashModeClaimAndHang, func() (sqlite.CrashWorkerReady, error) {
		return spk04Boundary3ClaimAndHang(ctx, spikeWorkerPath, record)
	})
	run("4/6 after_checkpoint_before_process_exit", sqlite.CrashModeCheckpointThenHang, func() (sqlite.CrashWorkerReady, error) {
		return spk04Boundary4CheckpointThenHang(ctx, spikeWorkerPath, record)
	})
	run("5/6 after_process_exit_before_outcome_commit (read-only)", sqlite.CrashModeProcessExitReadonly, func() (sqlite.CrashWorkerReady, error) {
		return spk04Boundary5ProcessExit(ctx, spikeWorkerPath, record, false)
	})
	run("5/6 after_process_exit_before_outcome_commit (mutating)", sqlite.CrashModeProcessExitMutating, func() (sqlite.CrashWorkerReady, error) {
		return spk04Boundary5ProcessExit(ctx, spikeWorkerPath, record, true)
	})
	run("6/6 after_outcome_commit_before_next_dispatch", sqlite.CrashModeNodeDispatch, func() (sqlite.CrashWorkerReady, error) {
		return spk04Boundary6NodeDispatch(ctx, spikeWorkerPath, record)
	})

	faultMatrixArtifact, err := sc.Bundle.PutJSON("processes/fault-matrix.json", outcomes)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk04: write fault matrix evidence: %w", err)
	}
	assertionsArtifact, err := sc.Bundle.PutJSON("runtime/boundary-assertions.json", assertions)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk04: write boundary assertions evidence: %w", err)
	}

	return SPKResult{
		Passed:     passed,
		Assertions: assertions,
		Correlation: CorrelationIDs{
			ProjectID: "project-crash", FamilyID: "family-crash",
			RunID: sqlite.CrashNodeDispatchRunID, NodeRunID: sqlite.CrashNodeDispatchNodeRunID, AttemptID: sqlite.CrashAttemptTermAttemptID,
		},
		Platform: Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
		Timing:   Timing{StartedAt: started, EndedAt: time.Now().UTC()},
		Artifacts: []ArtifactRef{
			{Kind: ArtifactKindProcesses, Artifact: faultMatrixArtifact},
			{Kind: ArtifactKindRuntime, Artifact: assertionsArtifact},
		},
	}, nil
}

// spk04Fixture is what every boundary's arrange step produces: an open store
// (the caller seeds boundary-specific rows next, then must Close it before
// spawning spike-worker) plus enough identity to assert the spawned worker
// observed the expected pin.
type spk04Fixture struct {
	Store        *sqlite.Store
	DatabasePath string
	Cleanup      func()
	Definition   workflow.WorkflowDefinition
	Version      workflow.WorkflowVersion
	Run          domainruntime.WorkflowRun
}

// spk04Arrange builds a fresh temp-directory SQLite database seeded with the
// crash-resume owners and one compiled+published workflow version pinned to
// runID, exactly like each crash_resume_*_integration_test.go file's own
// arrange step. When moveToRunning is true the run is moved CREATED->RUNNING
// before returning, matching the boundaries whose fault point occurs after
// dispatch has already begun.
func spk04Arrange(ctx context.Context, tempDirPrefix string, runID domainruntime.WorkflowRunID, versionID workflow.WorkflowVersionID, moveToRunning bool) (*spk04Fixture, error) {
	tempDir, err := os.MkdirTemp("", tempDirPrefix+"-*")
	if err != nil {
		return nil, fmt.Errorf("spk04: create temp dir: %w", err)
	}
	cleanup := func() { os.RemoveAll(tempDir) }
	databasePath := filepath.Join(tempDir, "agentkit.db")
	store, err := sqlite.Open(ctx, databasePath)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("spk04: open sqlite: %w", err)
	}
	fail := func(err error) (*spk04Fixture, error) {
		_ = store.Close()
		cleanup()
		return nil, err
	}
	if err := sqlite.SeedCrashResumeOwners(ctx, store); err != nil {
		return fail(fmt.Errorf("spk04: seed owners: %w", err))
	}
	definition := sqlite.CrashResumeWorkflowDefinition()
	version, err := sqlite.CompileCrashResumeWorkflowVersion(definition, versionID, 1, sqlite.CrashResumeWorkflowDocumentV1(), "skill-v1")
	if err != nil {
		return fail(fmt.Errorf("spk04: compile workflow version: %w", err))
	}
	if _, err := store.PublishWorkflowVersion(ctx, definition, version); err != nil {
		return fail(fmt.Errorf("spk04: publish workflow version: %w", err))
	}
	run, err := domainruntime.NewWorkflowRun(runID, "project-crash", "work-item-crash", version, "family-crash", 1, json.RawMessage(`{"checkpoint":"created"}`))
	if err != nil {
		return fail(fmt.Errorf("spk04: build workflow run: %w", err))
	}
	if err := store.StartWorkflowRun(ctx, run); err != nil {
		return fail(fmt.Errorf("spk04: start workflow run: %w", err))
	}
	if moveToRunning {
		if _, err := store.CompareAndSwapWorkflowRun(ctx, ports.WorkflowRunTransition{
			RunID: run.ID, ExpectedState: domainruntime.WorkflowRunCreated, ExpectedVersion: 1,
			NextState: domainruntime.WorkflowRunRunning, SharedState: json.RawMessage(`{"checkpoint":"worker-dispatched"}`),
			OccurredAt: time.Now().UTC(),
		}); err != nil {
			return fail(fmt.Errorf("spk04: move run to RUNNING: %w", err))
		}
	}
	return &spk04Fixture{Store: store, DatabasePath: databasePath, Cleanup: cleanup, Definition: definition, Version: version, Run: run}, nil
}

// spawnAndHardKillSpikeWorker is the standalone-binary equivalent of
// internal/adapters/sqlite's startAndHardKillCrashWorker: it starts
// cmd/spike-worker with the given fault-point mode, waits for its READY
// line, then hard-kills it (Process.Kill, never a graceful shutdown, so no
// deferred cleanup in the worker can run).
func spawnAndHardKillSpikeWorker(spikeWorkerPath, databasePath string, ttl time.Duration, mode string) (sqlite.CrashWorkerReady, error) {
	command := exec.Command(spikeWorkerPath)
	command.Env = append(os.Environ(),
		sqlite.CrashWorkerModeEnvironment+"="+mode,
		sqlite.CrashWorkerDBEnvironment+"="+databasePath,
		sqlite.CrashWorkerTTLEnvironment+"="+ttl.String(),
	)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return sqlite.CrashWorkerReady{}, fmt.Errorf("create spike-worker stdout pipe: %w", err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return sqlite.CrashWorkerReady{}, fmt.Errorf("start spike-worker process: %w", err)
	}

	type scanResult struct {
		output string
		err    error
	}
	readyChannel := make(chan sqlite.CrashWorkerReady, 1)
	scanDone := make(chan scanResult, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		var output strings.Builder
		for scanner.Scan() {
			line := scanner.Text()
			output.WriteString(line)
			output.WriteByte('\n')
			if !strings.HasPrefix(line, sqlite.CrashWorkerReadyPrefix+" ") {
				continue
			}
			ready, parseErr := sqlite.ParseCrashWorkerReady(line)
			if parseErr != nil {
				scanDone <- scanResult{output: output.String(), err: parseErr}
				return
			}
			readyChannel <- ready
			return
		}
		scanDone <- scanResult{output: output.String(), err: scanner.Err()}
	}()

	var ready sqlite.CrashWorkerReady
	select {
	case ready = <-readyChannel:
	case result := <-scanDone:
		_ = command.Process.Kill()
		_ = command.Wait()
		return sqlite.CrashWorkerReady{}, fmt.Errorf("spike-worker exited before READY: scan=%v stdout=%q stderr=%q", result.err, result.output, stderr.String())
	case <-time.After(10 * time.Second):
		_ = command.Process.Kill()
		_ = command.Wait()
		return sqlite.CrashWorkerReady{}, fmt.Errorf("timeout waiting for spike-worker READY; stderr=%q", stderr.String())
	}

	// Process.Kill is intentionally used instead of context cancellation or a
	// protocol shutdown, so no defer in the worker can release its lease.
	if err := command.Process.Kill(); err != nil {
		return ready, fmt.Errorf("hard-kill spike-worker: %w", err)
	}
	if err := command.Wait(); err == nil {
		return ready, fmt.Errorf("hard-killed spike-worker exited successfully, want forced termination")
	}
	return ready, nil
}

// waitForExpiredJobRecovery polls RecoverExpiredJobs until the crashed
// worker's stale lease is reclaimed, mirroring the integration tests' helper
// of the same purpose but without *testing.T.
func waitForExpiredJobRecovery(ctx context.Context, store *sqlite.Store) error {
	deadline := time.Now().Add(10 * time.Second)
	for {
		recovered, err := store.RecoverExpiredJobs(ctx)
		if err != nil {
			return fmt.Errorf("recover expired job after crash: %w", err)
		}
		if recovered == 1 {
			return nil
		}
		if recovered != 0 {
			return fmt.Errorf("recovered %d jobs, want exactly one crashed job", recovered)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("lease was not recovered by deadline")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

const spk04CrashedWorkerTTL = 900 * time.Millisecond

// spk04Boundary1BeforeIntentCommit closes fault point 1/6: killed before the
// worker ever attempts DispatchNodeIntent. Nothing durable may exist
// afterward beyond the CREATED@1 workflow run.
func spk04Boundary1BeforeIntentCommit(ctx context.Context, spikeWorker string, record func(string, bool, string)) (sqlite.CrashWorkerReady, error) {
	fixture, err := spk04Arrange(ctx, "spk04-b1", sqlite.CrashIntentRunID, "workflow-version-spk04-b1-v1", false)
	if err != nil {
		return sqlite.CrashWorkerReady{}, err
	}
	defer fixture.Cleanup()
	if err := fixture.Store.Close(); err != nil {
		return sqlite.CrashWorkerReady{}, fmt.Errorf("spk04 b1: close arrange store: %w", err)
	}

	ready, err := spawnAndHardKillSpikeWorker(spikeWorker, fixture.DatabasePath, spk04CrashedWorkerTTL, sqlite.CrashModeBeforeIntentCommit)
	if err != nil {
		return ready, fmt.Errorf("spk04 b1: spawn/kill: %w", err)
	}

	restarted, err := sqlite.Open(ctx, fixture.DatabasePath)
	if err != nil {
		return ready, fmt.Errorf("spk04 b1: reopen after crash: %w", err)
	}
	defer restarted.Close()

	run, err := restarted.LoadWorkflowRun(ctx, sqlite.CrashIntentRunID)
	if err != nil {
		return ready, fmt.Errorf("spk04 b1: load workflow run: %w", err)
	}
	record("b1: workflow run is untouched CREATED@1 after the crash",
		run.State == domainruntime.WorkflowRunCreated && run.Version == 1,
		fmt.Sprintf("state=%s version=%d", run.State, run.Version))

	_, _, nodeErr := restarted.LoadNodeRunState(ctx, sqlite.CrashIntentNodeRunID)
	record("b1: no node run exists after a crash before any commit", errors.Is(nodeErr, ports.ErrPersistenceNotFound), fmt.Sprintf("error=%v", nodeErr))

	jobCount, err := restarted.CountDurableJobsByIdempotencyKey(ctx, sqlite.CrashIntentIdempotencyKey)
	if err != nil {
		return ready, fmt.Errorf("spk04 b1: count durable jobs: %w", err)
	}
	record("b1: no job exists after a crash before any commit", jobCount == 0, fmt.Sprintf("count=%d", jobCount))

	eventCount, err := restarted.CountDomainEvents(ctx, "NodeRun", sqlite.CrashIntentNodeRunID)
	if err != nil {
		return ready, fmt.Errorf("spk04 b1: count domain events: %w", err)
	}
	record("b1: no domain event exists after a crash before any commit", eventCount == 0, fmt.Sprintf("count=%d", eventCount))

	return ready, nil
}

// spk04Boundary2AfterIntentCommit closes fault point 2/6: the worker commits
// a real DispatchNodeIntent transaction, then is hard-killed before anyone
// claims the job it created. Everything that transaction wrote must exist,
// claimable exactly once.
func spk04Boundary2AfterIntentCommit(ctx context.Context, spikeWorker string, record func(string, bool, string)) (sqlite.CrashWorkerReady, error) {
	fixture, err := spk04Arrange(ctx, "spk04-b2", sqlite.CrashIntentRunID, "workflow-version-spk04-b2-v1", false)
	if err != nil {
		return sqlite.CrashWorkerReady{}, err
	}
	defer fixture.Cleanup()
	if err := fixture.Store.Close(); err != nil {
		return sqlite.CrashWorkerReady{}, fmt.Errorf("spk04 b2: close arrange store: %w", err)
	}

	ready, err := spawnAndHardKillSpikeWorker(spikeWorker, fixture.DatabasePath, spk04CrashedWorkerTTL, sqlite.CrashModeAfterIntentCommit)
	if err != nil {
		return ready, fmt.Errorf("spk04 b2: spawn/kill: %w", err)
	}
	record("b2: crashed worker dispatched the expected job", string(ready.JobID) == sqlite.CrashIntentJobID,
		fmt.Sprintf("jobId=%s", ready.JobID))

	restarted, err := sqlite.Open(ctx, fixture.DatabasePath)
	if err != nil {
		return ready, fmt.Errorf("spk04 b2: reopen after crash: %w", err)
	}
	defer restarted.Close()

	run, err := restarted.LoadWorkflowRun(ctx, sqlite.CrashIntentRunID)
	if err != nil {
		return ready, fmt.Errorf("spk04 b2: load workflow run: %w", err)
	}
	record("b2: workflow run moved to RUNNING", run.State == domainruntime.WorkflowRunRunning, string(run.State))

	nodeState, _, err := restarted.LoadNodeRunState(ctx, sqlite.CrashIntentNodeRunID)
	if err != nil {
		return ready, fmt.Errorf("spk04 b2: load node run: %w", err)
	}
	record("b2: node run committed as RUNNING", nodeState == domainruntime.NodeRunRunning, string(nodeState))

	jobState, err := restarted.LoadDurableJobState(ctx, ports.JobID(sqlite.CrashIntentJobID))
	if err != nil {
		return ready, fmt.Errorf("spk04 b2: load job state: %w", err)
	}
	record("b2: job committed as AVAILABLE", jobState == ports.JobAvailable, string(jobState))

	eventCount, err := restarted.CountDomainEvents(ctx, "NodeRun", sqlite.CrashIntentNodeRunID)
	if err != nil {
		return ready, fmt.Errorf("spk04 b2: count domain events: %w", err)
	}
	record("b2: exactly one node-dispatch domain event committed", eventCount == 1, fmt.Sprintf("count=%d", eventCount))

	claimed, _, err := restarted.ClaimJob(ctx, "worker-after-restart", 5*time.Second)
	if err != nil {
		return ready, fmt.Errorf("spk04 b2: replacement claim: %w", err)
	}
	record("b2: the dispatched job was not lost, claimed by a replacement worker", string(claimed.ID) == sqlite.CrashIntentJobID, string(claimed.ID))

	_, _, secondErr := restarted.ClaimJob(ctx, "worker-second-claimant", 5*time.Second)
	record("b2: only one worker can claim the job", errors.Is(secondErr, ports.ErrNoJobAvailable), fmt.Sprintf("error=%v", secondErr))

	return ready, nil
}

// spk04Boundary3ClaimAndHang closes fault point 3/6: killed right after the
// worker claims its job, before it spawns any process. A replacement worker
// must reclaim the same job with a strictly greater fence/lease token.
func spk04Boundary3ClaimAndHang(ctx context.Context, spikeWorker string, record func(string, bool, string)) (sqlite.CrashWorkerReady, error) {
	fixture, err := spk04Arrange(ctx, "spk04-b3", sqlite.CrashClaimRunID, "workflow-version-spk04-b3-v1", true)
	if err != nil {
		return sqlite.CrashWorkerReady{}, err
	}
	defer fixture.Cleanup()
	if _, err := fixture.Store.EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: sqlite.CrashClaimJobID, ProjectID: "project-crash", Kind: "EXECUTE_NODE", AggregateType: "WorkflowRun",
		AggregateID: string(fixture.Run.ID), Payload: json.RawMessage(`{"runId":"` + sqlite.CrashClaimRunID + `"}`),
		MaxClaims: 3, IdempotencyKey: "spk04-b3-execute",
	}); err != nil {
		return sqlite.CrashWorkerReady{}, fmt.Errorf("spk04 b3: enqueue job: %w", err)
	}
	if err := fixture.Store.Close(); err != nil {
		return sqlite.CrashWorkerReady{}, fmt.Errorf("spk04 b3: close arrange store: %w", err)
	}

	ready, err := spawnAndHardKillSpikeWorker(spikeWorker, fixture.DatabasePath, spk04CrashedWorkerTTL, sqlite.CrashModeClaimAndHang)
	if err != nil {
		return ready, fmt.Errorf("spk04 b3: spawn/kill: %w", err)
	}
	record("b3: crashed worker claimed the expected job", string(ready.JobID) == sqlite.CrashClaimJobID, string(ready.JobID))
	record("b3: crashed worker observed the pinned workflow version",
		ready.WorkflowVersionID == fixture.Version.ID() && ready.WorkflowVersionHash == fixture.Version.ContentHash(),
		fmt.Sprintf("%s/%s", ready.WorkflowVersionID, ready.WorkflowVersionHash))

	restarted, err := sqlite.Open(ctx, fixture.DatabasePath)
	if err != nil {
		return ready, fmt.Errorf("spk04 b3: reopen after crash: %w", err)
	}
	defer restarted.Close()
	if err := waitForExpiredJobRecovery(ctx, restarted); err != nil {
		return ready, fmt.Errorf("spk04 b3: %w", err)
	}
	claimed, replacementLease, err := restarted.ClaimJob(ctx, "worker-after-restart", 5*time.Second)
	if err != nil {
		return ready, fmt.Errorf("spk04 b3: replacement claim: %w", err)
	}
	record("b3: replacement worker reclaims the stale job (claim count 2)",
		string(claimed.ID) == sqlite.CrashClaimJobID && claimed.ClaimCount == 2,
		fmt.Sprintf("id=%s count=%d", claimed.ID, claimed.ClaimCount))
	record("b3: replacement lease token strictly exceeds the crashed worker's",
		replacementLease.Token > ready.LeaseToken, fmt.Sprintf("old=%d new=%d", ready.LeaseToken, replacementLease.Token))

	return ready, nil
}

// spk04Boundary4CheckpointThenHang closes fault point 4/6: killed after a
// real fake-provider grandchild process signalled a checkpoint-worthy event
// and the worker persisted a Checkpoint+ContextSnapshot — but before that
// provider process would itself have exited.
func spk04Boundary4CheckpointThenHang(ctx context.Context, spikeWorker string, record func(string, bool, string)) (sqlite.CrashWorkerReady, error) {
	fixture, err := spk04Arrange(ctx, "spk04-b4", sqlite.CrashCheckpointRunID, "workflow-version-spk04-b4-v1", true)
	if err != nil {
		return sqlite.CrashWorkerReady{}, err
	}
	defer fixture.Cleanup()
	const jobID = "spk04-b4-job"
	if _, err := fixture.Store.EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: jobID, ProjectID: "project-crash", Kind: "EXECUTE_NODE", AggregateType: "WorkflowRun",
		AggregateID: string(fixture.Run.ID), Payload: json.RawMessage(`{"runId":"` + sqlite.CrashCheckpointRunID + `"}`),
		MaxClaims: 3, IdempotencyKey: "spk04-b4-execute",
	}); err != nil {
		return sqlite.CrashWorkerReady{}, fmt.Errorf("spk04 b4: enqueue job: %w", err)
	}
	if err := sqlite.SeedCrashCheckpointNodeRunAndAttempt(ctx, fixture.Store, fixture.Run.ID); err != nil {
		return sqlite.CrashWorkerReady{}, fmt.Errorf("spk04 b4: seed node run/attempt: %w", err)
	}
	if err := fixture.Store.Close(); err != nil {
		return sqlite.CrashWorkerReady{}, fmt.Errorf("spk04 b4: close arrange store: %w", err)
	}

	ready, err := spawnAndHardKillSpikeWorker(spikeWorker, fixture.DatabasePath, spk04CrashedWorkerTTL, sqlite.CrashModeCheckpointThenHang)
	if err != nil {
		return ready, fmt.Errorf("spk04 b4: spawn/kill: %w", err)
	}
	record("b4: crashed worker claimed the expected job", string(ready.JobID) == jobID, string(ready.JobID))

	restarted, err := sqlite.Open(ctx, fixture.DatabasePath)
	if err != nil {
		return ready, fmt.Errorf("spk04 b4: reopen after crash: %w", err)
	}
	defer restarted.Close()

	checkpoint, err := restarted.LoadLatestCheckpoint(ctx, sqlite.CrashCheckpointInterruptedAttempt)
	if err != nil {
		return ready, fmt.Errorf("spk04 b4: load checkpoint: %w", err)
	}
	record("b4: checkpoint survived the hard kill",
		checkpoint.ID == sqlite.CrashCheckpointID && checkpoint.Sequence == 1 && checkpoint.ContextSnapshotID == sqlite.CrashCheckpointContextSnapshotID,
		fmt.Sprintf("%+v", checkpoint))

	if _, err := restarted.LoadContextSnapshot(ctx, checkpoint.ContextSnapshotID); err != nil {
		return ready, fmt.Errorf("spk04 b4: load context snapshot: %w", err)
	}
	record("b4: the checkpoint's context snapshot survived the hard kill", true, "")

	if err := waitForExpiredJobRecovery(ctx, restarted); err != nil {
		return ready, fmt.Errorf("spk04 b4: %w", err)
	}
	claimed, _, err := restarted.ClaimJob(ctx, "worker-after-restart", 5*time.Second)
	if err != nil {
		return ready, fmt.Errorf("spk04 b4: replacement claim: %w", err)
	}
	record("b4: replacement worker reclaims the stale job (claim count 2)",
		string(claimed.ID) == jobID && claimed.ClaimCount == 2, fmt.Sprintf("id=%s count=%d", claimed.ID, claimed.ClaimCount))

	return ready, nil
}

// spk04Boundary5ProcessExit closes fault point 5/6, in both its read-only
// and mutating variant: killed after a real external process already
// exited, before any outcome is durably committed. Recovery must classify
// the interrupted attempt from durable WriteLease evidence, never from the
// exit code the killed worker itself observed.
func spk04Boundary5ProcessExit(ctx context.Context, spikeWorker string, record func(string, bool, string), mutating bool) (sqlite.CrashWorkerReady, error) {
	label := "readonly"
	mode := sqlite.CrashModeProcessExitReadonly
	jobID := "spk04-b5-job-readonly"
	versionID := workflow.WorkflowVersionID("workflow-version-spk04-b5-readonly-v1")
	if mutating {
		label = "mutating"
		mode = sqlite.CrashModeProcessExitMutating
		jobID = "spk04-b5-job-mutating"
		versionID = "workflow-version-spk04-b5-mutating-v1"
	}

	fixture, err := spk04Arrange(ctx, "spk04-b5-"+label, sqlite.CrashAttemptTermRunID, versionID, true)
	if err != nil {
		return sqlite.CrashWorkerReady{}, err
	}
	defer fixture.Cleanup()
	if _, err := fixture.Store.EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: ports.JobID(jobID), ProjectID: "project-crash", Kind: "EXECUTE_NODE", AggregateType: "WorkflowRun",
		AggregateID: string(fixture.Run.ID), Payload: json.RawMessage(`{"runId":"` + sqlite.CrashAttemptTermRunID + `"}`),
		MaxClaims: 3, IdempotencyKey: "spk04-b5-execute-" + label,
	}); err != nil {
		return sqlite.CrashWorkerReady{}, fmt.Errorf("spk04 b5 %s: enqueue job: %w", label, err)
	}
	if err := sqlite.SeedCrashAttemptTermNodeRunAndAttempt(ctx, fixture.Store, fixture.Run.ID); err != nil {
		return sqlite.CrashWorkerReady{}, fmt.Errorf("spk04 b5 %s: seed node run/attempt: %w", label, err)
	}
	if mutating {
		if err := sqlite.SeedCrashAttemptTermWorkspace(ctx, fixture.Store); err != nil {
			return sqlite.CrashWorkerReady{}, fmt.Errorf("spk04 b5 %s: seed workspace: %w", label, err)
		}
	}
	if err := fixture.Store.Close(); err != nil {
		return sqlite.CrashWorkerReady{}, fmt.Errorf("spk04 b5 %s: close arrange store: %w", label, err)
	}

	ready, err := spawnAndHardKillSpikeWorker(spikeWorker, fixture.DatabasePath, spk04CrashedWorkerTTL, mode)
	if err != nil {
		return ready, fmt.Errorf("spk04 b5 %s: spawn/kill: %w", label, err)
	}
	record(fmt.Sprintf("b5 (%s): crashed worker claimed the expected job", label), string(ready.JobID) == jobID, string(ready.JobID))

	restarted, err := sqlite.Open(ctx, fixture.DatabasePath)
	if err != nil {
		return ready, fmt.Errorf("spk04 b5 %s: reopen after crash: %w", label, err)
	}
	defer restarted.Close()
	if err := waitForExpiredJobRecovery(ctx, restarted); err != nil {
		return ready, fmt.Errorf("spk04 b5 %s: %w", label, err)
	}
	if _, _, err := restarted.ClaimJob(ctx, "worker-after-restart", 5*time.Second); err != nil {
		return ready, fmt.Errorf("spk04 b5 %s: replacement claim: %w", label, err)
	}

	// The exit code observed by the killed worker (0, a clean exit) must
	// never leak into this classification: it is derived purely from durable
	// WriteLease evidence.
	nextState, reason, err := worker.ClassifyInterruptedAttempt(ctx, restarted, sqlite.CrashAttemptTermAttemptID)
	if err != nil {
		return ready, fmt.Errorf("spk04 b5 %s: classify interrupted attempt: %w", label, err)
	}
	record(fmt.Sprintf("b5 (%s): classification reason is process-exit-before-outcome-commit", label),
		reason == domainruntime.TerminationReasonProcessExitBeforeOutcomeCommit, string(reason))
	wantState := domainruntime.ExecutionAttemptLost
	if mutating {
		wantState = domainruntime.ExecutionAttemptIndeterminate
	}
	record(fmt.Sprintf("b5 (%s): classification state matches read-only/mutating history", label), nextState == wantState,
		fmt.Sprintf("got=%s want=%s", nextState, wantState))

	terminationUpdate := ports.AttemptTerminationUpdate{
		AttemptID: sqlite.CrashAttemptTermAttemptID, ExpectedVersion: 1, NextState: nextState, Reason: reason,
		EventID: "spk04-b5-" + label + "-event", CorrelationID: "spk04-b5-" + label, OccurredAt: time.Now().UTC(),
	}
	if err := restarted.TerminateInterruptedAttempt(ctx, terminationUpdate); err != nil {
		return ready, fmt.Errorf("spk04 b5 %s: terminate interrupted attempt: %w", label, err)
	}
	dupErr := restarted.TerminateInterruptedAttempt(ctx, terminationUpdate)
	record(fmt.Sprintf("b5 (%s): a duplicate termination at the same version is rejected", label),
		errors.Is(dupErr, ports.ErrOptimisticConflict), fmt.Sprintf("error=%v", dupErr))

	eventCount, err := restarted.CountDomainEvents(ctx, "ExecutionAttempt", sqlite.CrashAttemptTermAttemptID)
	if err != nil {
		return ready, fmt.Errorf("spk04 b5 %s: count domain events: %w", label, err)
	}
	record(fmt.Sprintf("b5 (%s): exactly one termination event committed, never duplicated", label), eventCount == 1, fmt.Sprintf("count=%d", eventCount))

	if mutating {
		currentRevision, err := restarted.LoadRepositoryWorkspaceRevision(ctx, sqlite.CrashAttemptTermRepositoryWorkspaceID)
		if err != nil {
			return ready, fmt.Errorf("spk04 b5 mutating: load workspace revision: %w", err)
		}
		verdict, err := worker.ReconcileMutatingAttempt(sqlite.CrashAttemptTermBaseRevision, currentRevision)
		if err != nil {
			return ready, fmt.Errorf("spk04 b5 mutating: reconcile mutating attempt: %w", err)
		}
		record("b5 (mutating): reconciliation finds no observed mutation", verdict == worker.ReconciliationClean, string(verdict))
	}

	return ready, nil
}

// spk04Boundary6NodeDispatch closes fault point 6/6: node completion, job
// acknowledgement and downstream-job dispatch commit in one transaction
// (Store.CompleteNodeAndDispatchNext), so a worker killed right after that
// commit can never lose the next step or duplicate it — a restarted
// dispatcher is free to safely replay the exact same request.
func spk04Boundary6NodeDispatch(ctx context.Context, spikeWorker string, record func(string, bool, string)) (sqlite.CrashWorkerReady, error) {
	fixture, err := spk04Arrange(ctx, "spk04-b6", sqlite.CrashNodeDispatchRunID, "workflow-version-spk04-b6-v1", true)
	if err != nil {
		return sqlite.CrashWorkerReady{}, err
	}
	defer fixture.Cleanup()
	if _, err := fixture.Store.EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: sqlite.CrashNodeDispatchJobID, ProjectID: "project-crash", Kind: "EXECUTE_NODE", AggregateType: "WorkflowRun",
		AggregateID: string(fixture.Run.ID), Payload: json.RawMessage(`{"runId":"` + sqlite.CrashNodeDispatchRunID + `"}`),
		MaxClaims: 3, IdempotencyKey: "execute-" + sqlite.CrashNodeDispatchJobID,
	}); err != nil {
		return sqlite.CrashWorkerReady{}, fmt.Errorf("spk04 b6: enqueue job: %w", err)
	}
	if err := sqlite.SeedCrashNodeDispatchNodeRun(ctx, fixture.Store, fixture.Run.ID); err != nil {
		return sqlite.CrashWorkerReady{}, fmt.Errorf("spk04 b6: seed node run: %w", err)
	}
	if err := fixture.Store.Close(); err != nil {
		return sqlite.CrashWorkerReady{}, fmt.Errorf("spk04 b6: close arrange store: %w", err)
	}

	ready, err := spawnAndHardKillSpikeWorker(spikeWorker, fixture.DatabasePath, spk04CrashedWorkerTTL, sqlite.CrashModeNodeDispatch)
	if err != nil {
		return ready, fmt.Errorf("spk04 b6: spawn/kill: %w", err)
	}
	record("b6: crashed worker claimed the expected job", string(ready.JobID) == sqlite.CrashNodeDispatchJobID, string(ready.JobID))

	restarted, err := sqlite.Open(ctx, fixture.DatabasePath)
	if err != nil {
		return ready, fmt.Errorf("spk04 b6: reopen after crash: %w", err)
	}
	defer restarted.Close()

	nodeState, nodeVersion, err := restarted.LoadNodeRunState(ctx, sqlite.CrashNodeDispatchNodeRunID)
	if err != nil {
		return ready, fmt.Errorf("spk04 b6: load node run: %w", err)
	}
	record("b6: node run committed SUCCEEDED@2 before the crash", nodeState == domainruntime.NodeRunSucceeded && nodeVersion == 2,
		fmt.Sprintf("state=%s version=%d", nodeState, nodeVersion))

	currentJobState, err := restarted.LoadDurableJobState(ctx, ports.JobID(sqlite.CrashNodeDispatchJobID))
	if err != nil {
		return ready, fmt.Errorf("spk04 b6: load job state: %w", err)
	}
	record("b6: current job committed SUCCEEDED before the crash", currentJobState == ports.JobSucceeded, string(currentJobState))

	// A restarted dispatcher cannot know for certain whether the crash
	// happened before or after commit, so it must be free to safely replay
	// the exact same dispatch — this must never duplicate the node
	// transition, the domain event or the downstream job.
	replayLease := ports.JobLease{JobID: ports.JobID(sqlite.CrashNodeDispatchJobID), Owner: "worker-before-crash", Token: ready.LeaseToken}
	replayNodeRun, replayJob, err := restarted.CompleteNodeAndDispatchNext(ctx, ports.NodeCompletionDispatch{
		NodeRunID: sqlite.CrashNodeDispatchNodeRunID, ExpectedVersion: 1, SelectedOutcome: "done",
		JobLease: replayLease, NextJob: sqlite.CrashNodeDispatchNextJobRequest(),
		EventID: "spk04-b6-event", CorrelationID: "spk04-b6", OccurredAt: time.Now().UTC(),
	})
	if err != nil {
		return ready, fmt.Errorf("spk04 b6: replay complete+dispatch: %w", err)
	}
	record("b6: replaying the same dispatch reproduces SUCCEEDED@2 idempotently",
		replayNodeRun.State == domainruntime.NodeRunSucceeded && replayNodeRun.Version == 2, fmt.Sprintf("%+v", replayNodeRun))
	record("b6: replay returns the same downstream job, not a duplicate", string(replayJob.ID) == sqlite.CrashNodeDispatchNextJobID, string(replayJob.ID))

	eventCount, err := restarted.CountDomainEvents(ctx, "NodeRun", sqlite.CrashNodeDispatchNodeRunID)
	if err != nil {
		return ready, fmt.Errorf("spk04 b6: count domain events: %w", err)
	}
	record("b6: exactly one node-completion event, never duplicated by the replay", eventCount == 1, fmt.Sprintf("count=%d", eventCount))

	// A genuinely different downstream request (different idempotency key)
	// after the node already completed is a real conflict, not a replay.
	_, _, conflictErr := restarted.CompleteNodeAndDispatchNext(ctx, ports.NodeCompletionDispatch{
		NodeRunID: sqlite.CrashNodeDispatchNodeRunID, ExpectedVersion: 1, SelectedOutcome: "done", JobLease: replayLease,
		NextJob: ports.EnqueueJobRequest{
			ID: "spk04-b6-different-job", ProjectID: "project-crash", Kind: "EXECUTE_NODE",
			AggregateType: "WorkflowRun", AggregateID: sqlite.CrashNodeDispatchRunID,
			Payload: json.RawMessage(`{}`), MaxClaims: 3, IdempotencyKey: "spk04-b6-different-key",
		},
		EventID: "spk04-b6-conflict-event", CorrelationID: "spk04-b6", OccurredAt: time.Now().UTC(),
	})
	record("b6: a genuinely different downstream request is rejected as a conflict, not accepted as a replay",
		errors.Is(conflictErr, ports.ErrOptimisticConflict), fmt.Sprintf("error=%v", conflictErr))

	claimedNext, _, err := restarted.ClaimJob(ctx, "worker-next-node", 5*time.Second)
	if err != nil {
		return ready, fmt.Errorf("spk04 b6: claim downstream job: %w", err)
	}
	record("b6: the downstream job was genuinely dispatched, claimable by a fresh worker",
		string(claimedNext.ID) == sqlite.CrashNodeDispatchNextJobID, string(claimedNext.ID))

	return ready, nil
}
