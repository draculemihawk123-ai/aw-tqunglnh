package run_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	clirun "github.com/taQuangLing/agent-workflow/internal/delivery/cli/run"
)

func newRunDeps(t *testing.T) (clirun.Dependencies, idsource.Source) {
	t.Helper()
	_, uow := openRunCLITestStore(t, "run-cli.db")
	ids := idsource.NewSequential("cli-run")
	return clirun.Dependencies{UOW: uow, IDs: ids}, ids
}

func newRunDepsWithStore(t *testing.T) (clirun.Dependencies, idsource.Source, *sqlite.Store) {
	t.Helper()
	store, uow := openRunCLITestStore(t, "run-cli.db")
	ids := idsource.NewSequential("cli-run")
	return clirun.Dependencies{UOW: uow, IDs: ids}, ids, store
}

func TestRunStart_FreshThenReplay(t *testing.T) {
	deps, ids := newRunDeps(t)
	root := readyWorkItemFixture(t, deps.UOW, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, deps.UOW, "project-1", "wf-def-1", "wf-v-1", workflowDocumentV1())

	var stdout, stderr bytes.Buffer
	args := []string{"--workflow-version-id", string(version.ID()), "--idempotency-key", "idem-start-1", root.WorkItemID}
	if err := clirun.Start(context.Background(), deps, args, &stdout, &stderr); err != nil {
		t.Fatalf("Start (fresh): %v, stderr=%s", err, stderr.String())
	}

	var envelope cli.ResultEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v\nstdout=%s", err, stdout.String())
	}
	if envelope.Replayed {
		t.Fatal("fresh Start reported Replayed=true, want false")
	}
	if envelope.IdempotencyKey != "idem-start-1" {
		t.Fatalf("IdempotencyKey = %q, want idem-start-1", envelope.IdempotencyKey)
	}
	resultBytes, err := json.Marshal(envelope.Result)
	if err != nil {
		t.Fatalf("re-marshal result: %v", err)
	}
	var startResult clirun.StartResult
	if err := json.Unmarshal(resultBytes, &startResult); err != nil {
		t.Fatalf("decode StartResult: %v", err)
	}
	if startResult.RunID == "" {
		t.Fatal("StartResult.RunID is empty")
	}
	if startResult.State != "RUNNING" {
		t.Fatalf("StartResult.State = %q, want RUNNING", startResult.State)
	}

	// Replay: identical args (same idempotency key + same request) must
	// replay the exact same RunID rather than starting a second run.
	var stdout2, stderr2 bytes.Buffer
	if err := clirun.Start(context.Background(), deps, args, &stdout2, &stderr2); err != nil {
		t.Fatalf("Start (replay): %v, stderr=%s", err, stderr2.String())
	}
	var envelope2 cli.ResultEnvelope
	if err := json.Unmarshal(stdout2.Bytes(), &envelope2); err != nil {
		t.Fatalf("decode replay envelope: %v", err)
	}
	if !envelope2.Replayed {
		t.Fatal("retry with identical idempotency key + request reported Replayed=false, want true")
	}
	resultBytes2, _ := json.Marshal(envelope2.Result)
	var startResult2 clirun.StartResult
	if err := json.Unmarshal(resultBytes2, &startResult2); err != nil {
		t.Fatalf("decode replayed StartResult: %v", err)
	}
	if startResult2.RunID != startResult.RunID {
		t.Fatalf("replayed RunID = %q, want %q (identical to the fresh call)", startResult2.RunID, startResult.RunID)
	}
}

func TestRunStart_GeneratedIdempotencyKeyReturned(t *testing.T) {
	deps, ids := newRunDeps(t)
	root := readyWorkItemFixture(t, deps.UOW, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, deps.UOW, "project-1", "wf-def-1", "wf-v-1", workflowDocumentV1())

	var stdout, stderr bytes.Buffer
	args := []string{"--workflow-version-id", string(version.ID()), root.WorkItemID}
	if err := clirun.Start(context.Background(), deps, args, &stdout, &stderr); err != nil {
		t.Fatalf("Start: %v, stderr=%s", err, stderr.String())
	}
	var envelope cli.ResultEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.IdempotencyKey == "" {
		t.Fatal("omitted --idempotency-key: expected a generated key to be returned, got empty")
	}
}

// TestRunStart_PinConflict_ReturnsTypedError proves the "pin conflict"
// Verify bullet: a WorkItem already pinned to one WorkflowVersionID (by a
// prior successful start) rejects a SECOND `run start` naming a different
// WorkflowVersionID with runtime.ErrWorkflowVersionMismatch — a typed
// conflict, never a silent overwrite of the pin.
//
// runtime.StartWorkflowRunRequest carries no ExpectedVersion field at all
// (confirmed by reading commands.go before writing this test, per this
// task's own brief) — the "pin" this bullet is actually about is the
// WorkItem's own pinned WorkflowVersionID, checked inside
// runtime.StartWorkflowRun itself (ErrWorkflowVersionMismatch), not an
// optimistic-concurrency --expected-version flag this subcommand does not
// have.
func TestRunStart_PinConflict_ReturnsTypedError(t *testing.T) {
	deps, ids, store := newRunDepsWithStore(t)
	root := readyWorkItemFixture(t, deps.UOW, ids, "project-1", "repo-1")
	versionA := publishTestWorkflowVersion(t, deps.UOW, "project-1", "wf-def-pinned", "wf-v-a", workflowDocumentV1())
	versionB := publishTestWorkflowVersion(t, deps.UOW, "project-1", "wf-def-requested", "wf-v-b", workflowDocumentV1())

	// Pin the WorkItem to versionA directly (there is no application
	// command that sets WorkItem.WorkflowVersionID today — starting a run
	// itself flips the WorkItem READY->ACTIVE, which would trip
	// ErrWorkItemNotReady before ever reaching the pin check this test
	// means to exercise) — the identical
	// SetWorkItemWorkflowVersionForTest helper
	// internal/delivery/httpapi/run/start_test.go's own
	// TestStartWorkflowRun_HTTP_PinnedWorkflowVersionMismatch_ReturnsConflict
	// uses for the exact same reason.
	if err := store.SetWorkItemWorkflowVersionForTest(context.Background(), root.WorkItemID, string(versionA.ID())); err != nil {
		t.Fatalf("pin work item to a workflow version: %v", err)
	}

	var stdout2, stderr2 bytes.Buffer
	err := clirun.Start(context.Background(), deps, []string{
		"--workflow-version-id", string(versionB.ID()), "--idempotency-key", "idem-b", root.WorkItemID,
	}, &stdout2, &stderr2)
	if err == nil {
		t.Fatal("second Start with a different --workflow-version-id and a fresh idempotency key succeeded, want ErrWorkflowVersionMismatch")
	}
	if !errors.Is(err, runtime.ErrWorkflowVersionMismatch) {
		t.Fatalf("Start error = %v, want errors.Is(..., runtime.ErrWorkflowVersionMismatch)", err)
	}
	if stdout2.Len() != 0 {
		t.Fatalf("a failed Start must never write a partial stdout document, got %q", stdout2.String())
	}
}

func TestRunStart_MissingWorkItemArgument(t *testing.T) {
	deps, _ := newRunDeps(t)
	var stdout, stderr bytes.Buffer
	err := clirun.Start(context.Background(), deps, []string{"--workflow-version-id", "v1"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("Start with no <workItemId> argument succeeded, want a usage error")
	}
}

func TestRunStart_Wait_ObservesTerminalState(t *testing.T) {
	deps, ids := newRunDeps(t)
	root := readyWorkItemFixture(t, deps.UOW, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, deps.UOW, "project-1", "wf-def-1", "wf-v-1", workflowDocumentV1())

	var stdout, stderr bytes.Buffer
	args := []string{
		"--workflow-version-id", string(version.ID()), "--idempotency-key", "idem-wait-1",
		"--wait", "--wait-timeout", "0s", root.WorkItemID,
	}

	// deps.Sleep is left nil (DefaultSleeper) but Timeout=0 disables the
	// deadline, and observe reports RUNNING (never terminal) forever — so
	// this exercises the "Wait never blocks the test" shape by advancing
	// the run to a terminal state from a second goroutine-free approach:
	// drive the run to SUCCEEDED directly before polling starts, via the
	// real runtime.AdvanceRun hop (start -> end is a one-hop workflow),
	// so the FIRST observe call already sees a terminal state, matching
	// cli.Wait's own "always calls observe at least once before its
	// first sleep... a job that is already terminal when --wait starts
	// returns immediately with zero delay" contract.
	deps.Sleep = func(ctx context.Context, d time.Duration) error {
		t.Fatal("Wait should have observed a terminal state on its first poll and never needed to sleep")
		return nil
	}

	// Start first (fresh, no --wait) to learn the RunID, then force the
	// Run straight to SUCCEEDED via the raw repository CAS (bypassing the
	// full ADR-021 completion-policy/evidence/approval-gate pipeline,
	// which is unrelated to what this test means to prove about --wait's
	// own polling mechanics) — mirrors this codebase's own established
	// "hand-seed a narrow, targeted state via a direct repository call"
	// convention (seedBlockedNodeRun, fixture_test.go) — then issue the
	// real --wait call against the SAME idempotency key so Start's own
	// dispatch replays instantly and --wait's first observe already
	// finds a terminal Run.
	var setupOut, setupErr bytes.Buffer
	setupDeps := deps
	setupDeps.Sleep = nil
	if err := clirun.Start(context.Background(), setupDeps, []string{
		"--workflow-version-id", string(version.ID()), "--idempotency-key", "idem-wait-1", root.WorkItemID,
	}, &setupOut, &setupErr); err != nil {
		t.Fatalf("setup Start: %v, stderr=%s", err, setupErr.String())
	}
	var setupEnvelope cli.ResultEnvelope
	if err := json.Unmarshal(setupOut.Bytes(), &setupEnvelope); err != nil {
		t.Fatalf("decode setup envelope: %v", err)
	}
	setupResultBytes, _ := json.Marshal(setupEnvelope.Result)
	var setupResult clirun.StartResult
	if err := json.Unmarshal(setupResultBytes, &setupResult); err != nil {
		t.Fatalf("decode setup StartResult: %v", err)
	}
	if err := forceRunSucceeded(deps.UOW, setupResult.RunID); err != nil {
		t.Fatalf("force run %s SUCCEEDED: %v", setupResult.RunID, err)
	}

	if err := clirun.Start(context.Background(), deps, args, &stdout, &stderr); err != nil {
		t.Fatalf("Start (--wait): %v, stderr=%s", err, stderr.String())
	}
	var envelope cli.ResultEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	resultBytes, _ := json.Marshal(envelope.Result)
	var startResult clirun.StartResult
	if err := json.Unmarshal(resultBytes, &startResult); err != nil {
		t.Fatalf("decode StartResult: %v", err)
	}
	if startResult.Wait == nil {
		t.Fatal("StartResult.Wait is nil, want a populated terminal RunDetail observation")
	}
	if startResult.Wait.State != "SUCCEEDED" {
		t.Fatalf("StartResult.Wait.State = %q, want SUCCEEDED", startResult.Wait.State)
	}
}
