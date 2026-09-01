package spikeacceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/worker"
	domainruntime "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// runSPK03Scenario closes SPK-03: a genuinely killed worker OS process (a
// real cmd/spike-worker child, hard-killed — the same CheckpointThenHang
// fault point SPK-04's boundary 4/6 exercises) joined with
// checkpoint/context recovery and interrupted-attempt recovery
// (docs/design/02-v0-spike-verdict.md V0-10C). The worker child spawns a
// real fake-provider grandchild, persists a Checkpoint+ContextSnapshot only
// after hearing back from it, then is hard-killed with no graceful
// shutdown. A replacement worker recovers purely from SQLite: it terminates
// the interrupted attempt out of RUNNING (this fixture never acquires a
// WriteLease, so it always classifies read-only -> LOST) via
// worker.ReconcileInterruptedAttempt, then starts a distinct execution
// attempt from the checkpoint's canonical ContextSnapshot, always calling
// AgentExecutor.Start, never Resume.
func runSPK03Scenario(ctx context.Context, sc ScenarioContext) (SPKResult, error) {
	started := time.Now().UTC()
	if sc.Binaries.SpikeWorker == "" {
		return SPKResult{}, fmt.Errorf("spk03: ScenarioBinaries.SpikeWorker is required")
	}
	var assertions []Assertion
	passed := true
	record := func(name string, ok bool, detail string) {
		assertions = append(assertions, Assertion{Name: name, Passed: ok, Detail: detail})
		if !ok {
			passed = false
		}
	}

	const jobID = "spk03-job"
	fixture, err := spk04Arrange(ctx, "spk03", sqlite.CrashCheckpointRunID, "workflow-version-spk03-v1", true)
	if err != nil {
		return SPKResult{}, err
	}
	defer fixture.Cleanup()
	if _, err := fixture.Store.EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: jobID, ProjectID: "project-crash", Kind: "EXECUTE_NODE", AggregateType: "WorkflowRun",
		AggregateID: string(fixture.Run.ID), Payload: json.RawMessage(`{"runId":"` + sqlite.CrashCheckpointRunID + `"}`),
		MaxClaims: 3, IdempotencyKey: "spk03-execute",
	}); err != nil {
		return SPKResult{}, fmt.Errorf("spk03: enqueue job: %w", err)
	}
	if err := sqlite.SeedCrashCheckpointNodeRunAndAttempt(ctx, fixture.Store, fixture.Run.ID); err != nil {
		return SPKResult{}, fmt.Errorf("spk03: seed node run/attempt: %w", err)
	}
	if err := fixture.Store.Close(); err != nil {
		return SPKResult{}, fmt.Errorf("spk03: close arrange store: %w", err)
	}

	ready, err := spawnAndHardKillSpikeWorker(sc.Binaries.SpikeWorker, fixture.DatabasePath, spk04CrashedWorkerTTL, sqlite.CrashModeCheckpointThenHang)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk03: spawn/kill spike-worker: %w", err)
	}
	record("crashed worker claimed the expected job", string(ready.JobID) == jobID, string(ready.JobID))

	restarted, err := sqlite.Open(ctx, fixture.DatabasePath)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk03: reopen after crash: %w", err)
	}
	defer restarted.Close()
	if err := waitForExpiredJobRecovery(ctx, restarted); err != nil {
		return SPKResult{}, fmt.Errorf("spk03: %w", err)
	}
	claimed, _, err := restarted.ClaimJob(ctx, "worker-after-restart", 5*time.Second)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk03: replacement claim: %w", err)
	}
	record("replacement worker reclaims the stale job (claim count 2)",
		string(claimed.ID) == jobID && claimed.ClaimCount == 2, fmt.Sprintf("id=%s count=%d", claimed.ID, claimed.ClaimCount))

	// The checkpoint and its ContextSnapshot must have survived the hard
	// kill: they were written by a process whose SQLite connection was never
	// closed and which was terminated with no graceful shutdown.
	checkpoint, err := restarted.LoadLatestCheckpoint(ctx, sqlite.CrashCheckpointInterruptedAttempt)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk03: load checkpoint: %w", err)
	}
	record("checkpoint survived the hard kill",
		checkpoint.ID == sqlite.CrashCheckpointID && checkpoint.Sequence == 1 && checkpoint.ContextSnapshotID == sqlite.CrashCheckpointContextSnapshotID,
		fmt.Sprintf("%+v", checkpoint))
	if _, err := restarted.LoadContextSnapshot(ctx, checkpoint.ContextSnapshotID); err != nil {
		return SPKResult{}, fmt.Errorf("spk03: load context snapshot: %w", err)
	}
	record("the checkpoint's context snapshot survived the hard kill", true, "")

	// The interrupted attempt must never be left stranded at RUNNING: this
	// fixture's crashed worker never acquires a WriteLease, so recovery must
	// classify it read-only and terminate it LOST — never inferring anything
	// from the killed process's own (unobserved) exit code.
	recovery, err := worker.ReconcileInterruptedAttempt(ctx, restarted, restarted, worker.InterruptedAttemptRecoveryRequest{
		AttemptID: sqlite.CrashCheckpointInterruptedAttempt, ExpectedAttemptVersion: 1,
		TerminationEventID: "spk03-terminate-event", CorrelationID: "spk03", OccurredAt: time.Now().UTC(),
	})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk03: reconcile interrupted attempt: %w", err)
	}
	record("interrupted attempt is classified read-only and terminated LOST",
		recovery.NextState == domainruntime.ExecutionAttemptLost && recovery.Reason == domainruntime.TerminationReasonProcessExitBeforeOutcomeCommit,
		fmt.Sprintf("%+v", recovery))

	attemptState, attemptVersion, err := restarted.LoadExecutionAttemptState(ctx, sqlite.CrashCheckpointInterruptedAttempt)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk03: load attempt state after recovery: %w", err)
	}
	record("interrupted attempt durably reflects LOST@2, no longer RUNNING",
		attemptState == domainruntime.ExecutionAttemptLost && attemptVersion == 2, fmt.Sprintf("state=%s version=%d", attemptState, attemptVersion))

	dupErr := restarted.TerminateInterruptedAttempt(ctx, ports.AttemptTerminationUpdate{
		AttemptID: sqlite.CrashCheckpointInterruptedAttempt, ExpectedVersion: 1,
		NextState: domainruntime.ExecutionAttemptLost, Reason: domainruntime.TerminationReasonProcessExitBeforeOutcomeCommit,
		EventID: "spk03-duplicate-terminate-event", CorrelationID: "spk03", OccurredAt: time.Now().UTC(),
	})
	record("a duplicate termination at the stale version is rejected", errors.Is(dupErr, ports.ErrOptimisticConflict), fmt.Sprintf("error=%v", dupErr))

	// Replacement execution: always a new attempt id, always Start, never
	// Resume, sourced only from the durable ContextSnapshot/Checkpoint chain
	// — nothing carried over from the killed process's memory.
	recorder := &spk03FreshRecoveryExecutor{}
	workingDirectory, err := os.MkdirTemp("", "spk03-workdir-*")
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk03: create working directory: %w", err)
	}
	defer os.RemoveAll(workingDirectory)
	execResult, err := worker.StartFreshFromLatestCheckpoint(
		ctx, restarted, recorder, sqlite.CrashCheckpointInterruptedAttempt,
		ports.AgentExecutionRequest{AttemptID: sqlite.CrashCheckpointReplacementAttempt, WorkingDirectory: workingDirectory, Timeout: time.Second},
		ports.AgentEventSinkFunc(func(context.Context, ports.AgentEvent) error { return nil }),
	)
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk03: start fresh from latest checkpoint: %w", err)
	}
	record("replacement execution starts fresh from checkpoint, never resumes",
		execResult.AttemptID == sqlite.CrashCheckpointReplacementAttempt && recorder.startCalls == 1 && recorder.resumeCalls == 0,
		fmt.Sprintf("result=%+v start=%d resume=%d", execResult, recorder.startCalls, recorder.resumeCalls))
	record("replacement attempt id differs from the interrupted attempt id",
		sqlite.CrashCheckpointReplacementAttempt != checkpoint.AttemptID, sqlite.CrashCheckpointReplacementAttempt)

	checkpointArtifact, err := sc.Bundle.PutJSON("runtime/checkpoint-recovery.json", map[string]any{
		"checkpointId": checkpoint.ID, "contextSnapshotId": checkpoint.ContextSnapshotID,
		"interruptedAttempt": sqlite.CrashCheckpointInterruptedAttempt, "replacementAttempt": sqlite.CrashCheckpointReplacementAttempt,
		"recovery": recovery,
	})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk03: write checkpoint recovery evidence: %w", err)
	}
	processArtifact, err := sc.Bundle.PutJSON("processes/fault-point.json", map[string]any{
		"boundary": "after_checkpoint_before_process_exit", "mode": sqlite.CrashModeCheckpointThenHang, "jobId": ready.JobID, "killed": true,
	})
	if err != nil {
		return SPKResult{}, fmt.Errorf("spk03: write process evidence: %w", err)
	}

	return SPKResult{
		Passed:     passed,
		Assertions: assertions,
		Correlation: CorrelationIDs{
			ProjectID: "project-crash", FamilyID: "family-crash",
			RunID: sqlite.CrashCheckpointRunID, NodeRunID: sqlite.CrashCheckpointNodeRunID, AttemptID: sqlite.CrashCheckpointInterruptedAttempt,
		},
		Platform: Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
		Timing:   Timing{StartedAt: started, EndedAt: time.Now().UTC()},
		Artifacts: []ArtifactRef{
			{Kind: ArtifactKindRuntime, Artifact: checkpointArtifact},
			{Kind: ArtifactKindProcesses, Artifact: processArtifact},
		},
	}, nil
}

// spk03FreshRecoveryExecutor is a minimal in-memory ports.AgentExecutor
// fake, the same shape as internal/adapters/sqlite's checkpoint_store_test.go
// freshRecoveryExecutor: it only needs to prove Start is called and Resume
// never is, not exercise a real provider CLI (SPK-11/SPK-12 already prove
// that against real fake-claude/fake-codex subprocesses).
type spk03FreshRecoveryExecutor struct {
	startCalls  int
	resumeCalls int
}

func (*spk03FreshRecoveryExecutor) Capabilities(context.Context) (ports.AgentCapabilities, error) {
	return ports.AgentCapabilities{Provider: ports.ProviderCodex, SupportsStart: true}, nil
}

func (e *spk03FreshRecoveryExecutor) Start(_ context.Context, request ports.AgentExecutionRequest, _ ports.AgentEventSink) (ports.AgentExecutionResult, error) {
	e.startCalls++
	return ports.AgentExecutionResult{AttemptID: request.AttemptID, Provider: ports.ProviderCodex, Status: ports.AgentExecutionSucceeded}, nil
}

func (e *spk03FreshRecoveryExecutor) Resume(context.Context, ports.AgentExecutionRequest, ports.ProviderSessionRef, ports.AgentEventSink) (ports.AgentExecutionResult, error) {
	e.resumeCalls++
	return ports.AgentExecutionResult{}, errors.New("fresh recovery must not call resume")
}

func (*spk03FreshRecoveryExecutor) Cancel(context.Context, ports.ExecutionAttemptID) error {
	return nil
}

var _ ports.AgentExecutor = (*spk03FreshRecoveryExecutor)(nil)
