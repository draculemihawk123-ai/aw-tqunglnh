package sqlite

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// This file is the shared, non-test home for the crashed-worker child
// process logic that internal/adapters/sqlite's crash_resume_*_integration_test.go
// files and cmd/spike-worker both drive (docs/design/02-v0-spike-verdict.md
// V0-10B). It has to live in real (non _test.go) source: a _test.go file's
// declarations only exist inside a `go test` binary, but cmd/spike-worker is
// a standalone binary built with a plain `go build` and needs these exact
// same symbols. The *_test.go files that used to own this logic now call
// into it instead of duplicating it.

// Env vars a crash-worker child process reads to learn its fault point, the
// database to open and how long its claimed lease should live.
const (
	CrashWorkerModeEnvironment = "AGENTKIT_SPIKE_CRASH_WORKER"
	CrashWorkerDBEnvironment   = "AGENTKIT_SPIKE_CRASH_DB"
	CrashWorkerTTLEnvironment  = "AGENTKIT_SPIKE_CRASH_TTL"
	// CrashWorkerReadyPrefix marks the one stdout line a crash-worker child
	// prints once its fault point's own durable commit (if any) has genuinely
	// happened — the signal a parent waits for before hard-killing it.
	CrashWorkerReadyPrefix = "AGENTKIT_SPIKE_CRASH_READY"

	// CrashCheckpointProviderModeEnvironment/ReadyPrefix are the checkpoint
	// fault point's own separate protocol for its fake-provider grandchild
	// process, distinct from the worker's own READY line above.
	CrashCheckpointProviderModeEnvironment = "AGENTKIT_SPIKE_CRASH_CKPT_PROVIDER"
	CrashCheckpointProviderReadyPrefix     = "AGENTKIT_SPIKE_CRASH_CKPT_PROVIDER_READY"
)

// The eight crash-worker fault-point modes a child process can be told to
// play via CrashWorkerModeEnvironment. Six of them (all but ClaimAndHang and
// FinalizeAndHang) are SPK-04's six standard fault points
// (docs/spikes/01-go-core-spike-plan.md §9): ClaimAndHang stands in for
// "after_job_claim_before_process_spawn" (boundary 3) and CheckpointThenHang
// for "after_checkpoint_before_process_exit" (boundary 4) — FinalizeAndHang
// is not one of the six; it belongs to a related but separate atomic-
// finalization idempotency proof.
const (
	CrashModeClaimAndHang        = "claim-and-hang"
	CrashModeFinalizeAndHang     = "finalize-and-hang"
	CrashModeCheckpointThenHang  = "checkpoint-then-hang"
	CrashModeBeforeIntentCommit  = "before-intent-commit-then-hang"
	CrashModeAfterIntentCommit   = "after-intent-commit-then-hang"
	CrashModeProcessExitReadonly = "process-exit-readonly-then-hang"
	CrashModeProcessExitMutating = "process-exit-mutating-then-hang"
	CrashModeNodeDispatch        = "node-dispatch-then-hang"
	// CrashModeNoopExit is not a fault point: it is the standalone-binary
	// equivalent of the test-binary trick `-test.run=^$` (run zero tests, exit
	// 0 immediately) that the process-exit fault points use as "a real
	// external process that completes cleanly". See SelfSpawner.
	CrashModeNoopExit = "noop-exit"
)

// CrashWorkerReady is what a crash-worker child reports on its READY line:
// enough for the parent to both verify it observed the right durable state
// and to reason about lease expiry after the hard kill.
type CrashWorkerReady struct {
	JobID               ports.JobID
	LeaseToken          uint64
	LeaseUntil          time.Time
	WorkflowVersionID   workflow.WorkflowVersionID
	WorkflowVersionHash string
}

func writeCrashWorkerReady(jobID ports.JobID, leaseToken uint64, leaseUntil time.Time, versionID workflow.WorkflowVersionID, versionHash string) {
	fmt.Printf(
		"%s %s %d %s %s %s\n",
		CrashWorkerReadyPrefix,
		jobID,
		leaseToken,
		leaseUntil.UTC().Format(time.RFC3339Nano),
		versionID,
		versionHash,
	)
	_ = os.Stdout.Sync()
}

// ParseCrashWorkerReady decodes one CrashWorkerReadyPrefix-tagged stdout
// line. Exported so both the existing test-file orchestration and a
// standalone-binary parent (e.g. a future spike-worker-launching scenario)
// can parse it identically.
func ParseCrashWorkerReady(line string) (CrashWorkerReady, error) {
	fields := strings.Fields(line)
	if len(fields) != 6 || fields[0] != CrashWorkerReadyPrefix {
		return CrashWorkerReady{}, fmt.Errorf("invalid crash worker READY line %q", line)
	}
	token, err := strconv.ParseUint(fields[2], 10, 64)
	if err != nil {
		return CrashWorkerReady{}, fmt.Errorf("parse crash worker lease token: %w", err)
	}
	leaseUntil, err := time.Parse(time.RFC3339Nano, fields[3])
	if err != nil {
		return CrashWorkerReady{}, fmt.Errorf("parse crash worker lease expiry: %w", err)
	}
	return CrashWorkerReady{
		JobID:               ports.JobID(fields[1]),
		LeaseToken:          token,
		LeaseUntil:          leaseUntil,
		WorkflowVersionID:   workflow.WorkflowVersionID(fields[4]),
		WorkflowVersionHash: fields[5],
	}, nil
}

// SpawnRole names an internal child-process role some crash-worker fault
// points spawn before hitting their own fault point: a fake provider
// (CheckpointThenHang) or a clean-exit no-op standing in for "a real
// external process just completed" (the process-exit fault points).
type SpawnRole string

const (
	SpawnRoleFakeProvider SpawnRole = "fake-provider"
	SpawnRoleNoopExit     SpawnRole = "noop-exit"
)

// SelfSpawner builds the *exec.Cmd used to re-invoke the current process in
// one of the SpawnRole roles above. How to do that differs by which binary
// is currently running: a re-invoked `go test` binary needs a
// `-test.run=^...$` argument so it does not run the whole package's test
// suite, while the standalone cmd/spike-worker binary just needs its own
// env-var dispatch (see cmd/spike-worker/main.go). RunCrashWorker stays
// agnostic of that difference by taking this as a parameter.
type SelfSpawner func(role SpawnRole) (*exec.Cmd, error)

// RunCrashWorker plays one of the eight crash-worker fault-point child-
// process roles (see the CrashMode* constants), reading its database path
// and lease TTL from CrashWorkerDBEnvironment/CrashWorkerTTLEnvironment. It
// always either returns a non-nil error before reaching its fault point, or
// prints its READY line and hangs forever (select{}) — there is no graceful
// return once READY has printed; the caller is expected to hard-kill this
// process. spawn is only used by the two fault points that need a child of
// their own (CheckpointThenHang, ProcessExit*); pass nil for any other mode.
func RunCrashWorker(mode string, spawn SelfSpawner) error {
	databasePath := strings.TrimSpace(os.Getenv(CrashWorkerDBEnvironment))
	if databasePath == "" {
		return fmt.Errorf("crash worker database path is required")
	}
	ttl, err := time.ParseDuration(os.Getenv(CrashWorkerTTLEnvironment))
	if err != nil || ttl <= 0 {
		return fmt.Errorf("parse crash worker TTL: %w", err)
	}
	switch mode {
	case CrashModeClaimAndHang:
		return runClaimAndOptionallyFinalizeWorker(databasePath, ttl, false)
	case CrashModeFinalizeAndHang:
		return runClaimAndOptionallyFinalizeWorker(databasePath, ttl, true)
	case CrashModeCheckpointThenHang:
		return runCheckpointThenHangWorker(databasePath, ttl, spawn)
	case CrashModeBeforeIntentCommit:
		return runBeforeIntentCommitWorker(databasePath, ttl)
	case CrashModeAfterIntentCommit:
		return runAfterIntentCommitWorker(databasePath, ttl)
	case CrashModeProcessExitReadonly:
		return runFaultAfterProcessExitWorker(databasePath, ttl, false, spawn)
	case CrashModeProcessExitMutating:
		return runFaultAfterProcessExitWorker(databasePath, ttl, true, spawn)
	case CrashModeNodeDispatch:
		return runNodeDispatchWorker(databasePath, ttl)
	case CrashModeNoopExit:
		return nil
	default:
		return fmt.Errorf("unknown crash worker mode %q", mode)
	}
}

// RunCrashCheckpointFakeProvider plays the bare fake-provider grandchild
// CheckpointThenHang spawns: it deliberately does not touch SQLite,
// ports.ProcessSupervisor or any AgentExecutor/provider-adapter code (that is
// out of scope here — see the checkpoint fault point's own doc comment),
// just signals one checkpoint-worthy event over stdout and self-terminates
// on a bounded timer rather than being explicitly waited on or killed.
func RunCrashCheckpointFakeProvider() {
	fmt.Printf("%s provider-checkpoint-event-1\n", CrashCheckpointProviderReadyPrefix)
	_ = os.Stdout.Sync()
	time.Sleep(2 * time.Second)
}

func runClaimAndOptionallyFinalizeWorker(databasePath string, ttl time.Duration, finalize bool) error {
	ctx := context.Background()
	store, err := Open(ctx, databasePath)
	if err != nil {
		return fmt.Errorf("crash worker open store: %w", err)
	}
	// Intentionally no Close: the parent terminates this process while its
	// SQLite connection and durable lease are still live.
	run, err := store.LoadWorkflowRun(ctx, CrashClaimRunID)
	if err != nil {
		return fmt.Errorf("crash worker load workflow run: %w", err)
	}
	_, lease, err := store.ClaimJob(ctx, "worker-before-crash", ttl)
	if err != nil {
		return fmt.Errorf("crash worker claim job: %w", err)
	}
	if finalize {
		if _, err := store.FinalizeWorkflowRun(ctx, ports.WorkerWorkflowRunFinalization{
			Transition: ports.WorkflowRunTransition{
				RunID: run.ID, ExpectedState: runtime.WorkflowRunRunning, ExpectedVersion: 2,
				NextState: runtime.WorkflowRunSucceeded, SharedState: json.RawMessage(`{"terminal":"child"}`),
				OccurredAt: time.Date(2026, 8, 28, 11, 0, 1, 0, time.UTC),
			},
			JobLease: lease, EventID: "event-child-finalized", CorrelationID: "atomic-finalization-crash",
		}); err != nil {
			return fmt.Errorf("crash worker finalize: %w", err)
		}
	}
	writeCrashWorkerReady(lease.JobID, lease.Token, lease.LeaseUntil, run.WorkflowVersionID, run.WorkflowVersionHash)
	select {}
}

func runCheckpointThenHangWorker(databasePath string, ttl time.Duration, spawn SelfSpawner) error {
	ctx := context.Background()
	store, err := Open(ctx, databasePath)
	if err != nil {
		return fmt.Errorf("crash checkpoint worker open store: %w", err)
	}
	// Intentionally no Close: the parent terminates this process while its
	// SQLite connection, durable lease and just-written checkpoint are live.
	run, err := store.LoadWorkflowRun(ctx, CrashCheckpointRunID)
	if err != nil {
		return fmt.Errorf("crash checkpoint worker load workflow run: %w", err)
	}
	_, lease, err := store.ClaimJob(ctx, "worker-before-crash", ttl)
	if err != nil {
		return fmt.Errorf("crash checkpoint worker claim job: %w", err)
	}

	providerEvent, err := spawnCrashCheckpointFakeProvider(spawn)
	if err != nil {
		return err
	}

	revisions, err := workspace.NewRevisionSet([]workspace.Revision{{
		RepositoryID: project.RepositoryID("repo-crash-ckpt"), VCSObjectID: "rev-crash-ckpt-1", WorkspaceGeneration: 1,
	}})
	if err != nil {
		return fmt.Errorf("crash checkpoint worker build revision set: %w", err)
	}
	snapshot, err := runtime.NewContextSnapshot(runtime.ContextSnapshotInput{
		ID:        CrashCheckpointContextSnapshotID,
		AttemptID: CrashCheckpointInterruptedAttempt,
		Messages:  []runtime.ContextMessage{{Role: runtime.ContextRoleSystem, Content: "provider event: " + providerEvent}},
		Revisions: revisions,
		CreatedAt: time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		return fmt.Errorf("crash checkpoint worker build context snapshot: %w", err)
	}
	// Persisted before the worker signals READY, i.e. strictly before the
	// parent hard-kills it: the checkpoint is durable at the moment of crash.
	if _, err := store.StoreContextSnapshot(ctx, "project-crash", snapshot); err != nil {
		return fmt.Errorf("crash checkpoint worker persist context snapshot: %w", err)
	}
	checkpoint, err := runtime.NewCheckpoint(
		CrashCheckpointID, run.ID, CrashCheckpointNodeRunID, CrashCheckpointInterruptedAttempt,
		1, 1, snapshot.ID(), revisions, "sha256:crash-checkpoint-shared-state", nil,
		time.Date(2026, 8, 28, 12, 0, 1, 0, time.UTC),
	)
	if err != nil {
		return fmt.Errorf("crash checkpoint worker build checkpoint: %w", err)
	}
	if _, err := store.StoreCheckpoint(ctx, checkpoint); err != nil {
		return fmt.Errorf("crash checkpoint worker persist checkpoint: %w", err)
	}

	writeCrashWorkerReady(lease.JobID, lease.Token, lease.LeaseUntil, run.WorkflowVersionID, run.WorkflowVersionHash)
	select {}
}

// spawnCrashCheckpointFakeProvider starts the fake-provider grandchild and
// waits for its single readiness line. The grandchild is never explicitly
// waited on or killed: the worker itself is about to be hard-killed with no
// cleanup path, exactly like a real crash, so the provider is left to
// self-terminate on its own bounded timer instead.
func spawnCrashCheckpointFakeProvider(spawn SelfSpawner) (string, error) {
	if spawn == nil {
		return "", fmt.Errorf("crash checkpoint worker requires a SelfSpawner for its fake-provider grandchild")
	}
	command, err := spawn(SpawnRoleFakeProvider)
	if err != nil {
		return "", fmt.Errorf("build fake provider spawn command: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("create fake provider stdout pipe: %w", err)
	}
	if err := command.Start(); err != nil {
		return "", fmt.Errorf("start fake provider process: %w", err)
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
			if !strings.HasPrefix(line, CrashCheckpointProviderReadyPrefix+" ") {
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
			return "", fmt.Errorf("fake provider readiness: %w", result.err)
		}
		return result.event, nil
	case <-time.After(10 * time.Second):
		return "", fmt.Errorf("timeout waiting for fake provider READY")
	}
}

func runBeforeIntentCommitWorker(databasePath string, ttl time.Duration) error {
	ctx := context.Background()
	store, err := Open(ctx, databasePath)
	if err != nil {
		return fmt.Errorf("crash before-intent worker open store: %w", err)
	}
	// Intentionally no Close.
	run, err := store.LoadWorkflowRun(ctx, CrashIntentRunID)
	if err != nil {
		return fmt.Errorf("crash before-intent worker load workflow run: %w", err)
	}
	// Deliberately never calls DispatchNodeIntent — proving "before commit" by
	// never attempting the transaction, not by racing a real call against the
	// kill signal.
	writeCrashWorkerReady("no-job-dispatched-yet", 0, time.Now().UTC().Add(ttl), run.WorkflowVersionID, run.WorkflowVersionHash)
	select {}
}

func runAfterIntentCommitWorker(databasePath string, ttl time.Duration) error {
	ctx := context.Background()
	store, err := Open(ctx, databasePath)
	if err != nil {
		return fmt.Errorf("crash after-intent worker open store: %w", err)
	}
	// Intentionally no Close: the parent terminates this process while its
	// SQLite connection is still live, right after the dispatch transaction
	// committed for real.
	run, err := store.LoadWorkflowRun(ctx, CrashIntentRunID)
	if err != nil {
		return fmt.Errorf("crash after-intent worker load workflow run: %w", err)
	}
	_, newJob, err := store.DispatchNodeIntent(ctx, ports.NodeIntentDispatch{
		RunID:              CrashIntentRunID,
		ExpectedRunVersion: 1,
		NodeRunID:          CrashIntentNodeRunID,
		NodeKey:            "implement",
		ActivationSequence: 1,
		InputStateHash:     "sha256:input-crash-intent",
		Job: ports.EnqueueJobRequest{
			ID:             CrashIntentJobID,
			ProjectID:      "project-crash",
			Kind:           "EXECUTE_NODE",
			AggregateType:  "WorkflowRun",
			AggregateID:    CrashIntentRunID,
			Payload:        json.RawMessage(`{}`),
			MaxClaims:      3,
			IdempotencyKey: CrashIntentIdempotencyKey,
		},
		EventID:       "event-crash-intent-1",
		CorrelationID: "crash-intent",
		OccurredAt:    time.Date(2026, 8, 28, 15, 0, 0, 0, time.UTC),
	})
	if err != nil {
		return fmt.Errorf("crash after-intent worker dispatch: %w", err)
	}
	writeCrashWorkerReady(newJob.ID, 0, time.Now().UTC(), run.WorkflowVersionID, run.WorkflowVersionHash)
	select {}
}

func runFaultAfterProcessExitWorker(databasePath string, ttl time.Duration, mutating bool, spawn SelfSpawner) error {
	ctx := context.Background()
	store, err := Open(ctx, databasePath)
	if err != nil {
		return fmt.Errorf("crash attempt-termination worker open store: %w", err)
	}
	// Intentionally no Close: the parent terminates this process while its
	// SQLite connection and durable lease are still live.
	run, err := store.LoadWorkflowRun(ctx, CrashAttemptTermRunID)
	if err != nil {
		return fmt.Errorf("crash attempt-termination worker load workflow run: %w", err)
	}
	_, lease, err := store.ClaimJob(ctx, "worker-before-crash", ttl)
	if err != nil {
		return fmt.Errorf("crash attempt-termination worker claim job: %w", err)
	}

	if mutating {
		if _, err := store.AcquireWriteLeases(ctx, ports.AcquireWriteLeasesRequest{
			JobLease:  lease,
			AttemptID: CrashAttemptTermAttemptID,
			Targets: []ports.WorkspaceLeaseTarget{{
				RepositoryID:          CrashAttemptTermRepositoryID,
				RepositoryWorkspaceID: CrashAttemptTermRepositoryWorkspaceID,
				Generation:            1,
			}},
			TTL: ttl,
		}); err != nil {
			return fmt.Errorf("crash attempt-termination worker acquire write lease: %w", err)
		}
	}

	// A real external process runs to completion and exits cleanly (code 0).
	// The worker is about to be killed with no chance to act on that exit;
	// nothing downstream may treat this clean exit as a committed success.
	if spawn == nil {
		return fmt.Errorf("crash attempt-termination worker requires a SelfSpawner for its external process")
	}
	external, err := spawn(SpawnRoleNoopExit)
	if err != nil {
		return fmt.Errorf("build external process spawn command: %w", err)
	}
	if err := external.Run(); err != nil {
		return fmt.Errorf("external process did not exit cleanly: %w", err)
	}

	writeCrashWorkerReady(lease.JobID, lease.Token, lease.LeaseUntil, run.WorkflowVersionID, run.WorkflowVersionHash)
	select {}
}

func runNodeDispatchWorker(databasePath string, ttl time.Duration) error {
	ctx := context.Background()
	store, err := Open(ctx, databasePath)
	if err != nil {
		return fmt.Errorf("crash node-dispatch worker open store: %w", err)
	}
	// Intentionally no Close: the parent terminates this process while its
	// SQLite connection is still live, right after the dispatch transaction
	// committed for real.
	run, err := store.LoadWorkflowRun(ctx, CrashNodeDispatchRunID)
	if err != nil {
		return fmt.Errorf("crash node-dispatch worker load workflow run: %w", err)
	}
	_, lease, err := store.ClaimJob(ctx, "worker-before-crash", ttl)
	if err != nil {
		return fmt.Errorf("crash node-dispatch worker claim job: %w", err)
	}
	if _, _, err := store.CompleteNodeAndDispatchNext(ctx, ports.NodeCompletionDispatch{
		NodeRunID:       CrashNodeDispatchNodeRunID,
		ExpectedVersion: 1,
		SelectedOutcome: "done",
		JobLease:        lease,
		NextJob:         CrashNodeDispatchNextJobRequest(),
		EventID:         "event-node-dispatch-1",
		CorrelationID:   "crash-node-dispatch",
		OccurredAt:      time.Date(2026, 8, 28, 14, 0, 0, 0, time.UTC),
	}); err != nil {
		return fmt.Errorf("crash node-dispatch worker complete+dispatch: %w", err)
	}
	writeCrashWorkerReady(lease.JobID, lease.Token, lease.LeaseUntil, run.WorkflowVersionID, run.WorkflowVersionHash)
	select {}
}
