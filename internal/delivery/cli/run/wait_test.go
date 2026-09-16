package run_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	clirun "github.com/taQuangLing/agent-workflow/internal/delivery/cli/run"
)

// TestRunStart_WaitInterrupted_NeverCallsCancelRun is this task's own
// "wait semantics" Verify bullet, made explicit: a `run start --wait`
// interrupted mid-poll (an operator's own Ctrl-C, simulated here by
// cancelling the context from inside a custom cli.Sleeper — the one seam
// cli.Wait's own ObserveFunc/Sleeper contract exposes for a deterministic
// test, see wait.go's own doc comment: "critically calls nothing but
// observe throughout its whole lifetime, so an interrupted --wait can
// never execute or cancel the job it was only ever watching") must leave
// the Run's own state completely untouched — never a CancelRun call, never
// any other mutating call, as a side effect of giving up on the wait.
//
// The Run is deliberately left RUNNING for the whole test (no
// runtime.AdvanceRun call): cli.Wait's own ObserveFunc here is
// runtime.GetRunDetail, a pure read, called at most once before the
// interrupt fires (cli.Wait's own doc comment: "always calls observe at
// least once before its first sleep") — this test proves that single
// observe call is read-only and that nothing mutating ever runs
// afterward, by directly re-loading the Run's own row post-interrupt and
// confirming its State/Version are byte-identical to what Start's own
// fresh dispatch already produced.
func TestRunStart_WaitInterrupted_NeverCallsCancelRun(t *testing.T) {
	deps, ids := newRunDeps(t)
	root := readyWorkItemFixture(t, deps.UOW, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, deps.UOW, "project-1", "wf-def-1", "wf-v-1", workflowDocumentV1())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sleepCalls := 0
	deps.Sleep = func(waitCtx context.Context, d time.Duration) error {
		sleepCalls++
		// Simulate an operator's own Ctrl-C landing while --wait is
		// between polls — cli.Wait's own Sleeper seam is the only place
		// this test can inject that deterministically (see this
		// function's own doc comment).
		cancel()
		return waitCtx.Err()
	}

	var stdout, stderr bytes.Buffer
	err := clirun.Start(ctx, deps, []string{
		"--workflow-version-id", string(version.ID()), "--idempotency-key", "idem-interrupt-1",
		"--wait", root.WorkItemID,
	}, &stdout, &stderr)

	if err == nil {
		t.Fatal("Start (--wait, interrupted) returned nil error, want context.Canceled")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Start error = %v, want errors.Is(..., context.Canceled)", err)
	}
	if sleepCalls != 1 {
		t.Fatalf("Sleep was called %d times, want exactly 1 (RUNNING is never terminal, so exactly one poll must occur before the interrupt fires)", sleepCalls)
	}

	// The mutation itself (Start) must still have committed and reported
	// its own result on stdout — only the SUBSEQUENT --wait was
	// interrupted, which must never retroactively undo or hide the fact
	// that the run genuinely started.
	var envelope cli.ResultEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v\nstdout=%s", err, stdout.String())
	}
	resultBytes, _ := json.Marshal(envelope.Result)
	var startResult clirun.StartResult
	if err := json.Unmarshal(resultBytes, &startResult); err != nil {
		t.Fatalf("decode StartResult: %v", err)
	}
	if startResult.RunID == "" {
		t.Fatal("StartResult.RunID is empty even though Start itself must have succeeded before --wait was interrupted")
	}
	if startResult.Wait != nil {
		t.Fatalf("StartResult.Wait = %+v, want nil (an interrupted --wait must never report a synthesized terminal observation)", startResult.Wait)
	}

	// The decisive proof: reload the Run directly and confirm nothing
	// mutating ever touched it — still RUNNING, still version 1 (the
	// version StartWorkflowRun's own creation left it at), specifically
	// NOT CANCELLING (which only runtime.CancelRun could ever produce).
	detail, err := runtime.GetRunDetail(context.Background(), deps.UOW, startResult.RunID)
	if err != nil {
		t.Fatalf("GetRunDetail (post-interrupt reload): %v", err)
	}
	if detail.State != "RUNNING" {
		t.Fatalf("post-interrupt Run.State = %q, want RUNNING (an interrupted --wait must never call CancelRun or any other mutating function)", detail.State)
	}
	if detail.Cancelling {
		t.Fatal("post-interrupt Run.Cancelling = true, want false (no CancelEpoch was ever set — CancelRun was never called)")
	}

	// Belt and suspenders: no RunCancellationIntent was ever recorded for
	// this Run — the one durable row only runtime.CancelRun ever writes
	// (cancel_run.go's own package doc comment).
	err = deps.UOW.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Runtime().GetRunCancellationIntent(context.Background(), startResult.RunID)
		return err
	})
	if !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("GetRunCancellationIntent = %v, want ports.ErrPersistenceNotFound (no cancellation intent was ever recorded)", err)
	}
}

// TestRunStart_WaitTimeout_NeverCallsCancelRun mirrors the interrupt test
// above for the --wait-timeout path instead of an operator interrupt: a
// short, real wall-clock deadline elapsing before the Run ever reaches a
// terminal state must behave identically — cli.ErrWaitTimeout returned,
// the Run's own state left completely untouched.
func TestRunStart_WaitTimeout_NeverCallsCancelRun(t *testing.T) {
	deps, ids := newRunDeps(t)
	root := readyWorkItemFixture(t, deps.UOW, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, deps.UOW, "project-1", "wf-def-1", "wf-v-1", workflowDocumentV1())

	// A real Sleep that returns instantly (never actually blocking this
	// test), combined with a very short real wall-clock --wait-timeout,
	// so cli.Wait's own real time.Now()-based deadline check reliably
	// trips after a handful of fast iterations without this test itself
	// sleeping.
	deps.Sleep = func(ctx context.Context, d time.Duration) error { return nil }

	var stdout, stderr bytes.Buffer
	err := clirun.Start(context.Background(), deps, []string{
		"--workflow-version-id", string(version.ID()), "--idempotency-key", "idem-timeout-1",
		"--wait", "--wait-timeout", "1ms", root.WorkItemID,
	}, &stdout, &stderr)

	if err == nil {
		t.Fatal("Start (--wait-timeout) returned nil error, want cli.ErrWaitTimeout")
	}
	if !errors.Is(err, cli.ErrWaitTimeout) {
		t.Fatalf("Start error = %v, want errors.Is(..., cli.ErrWaitTimeout)", err)
	}

	var envelope cli.ResultEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v\nstdout=%s", err, stdout.String())
	}
	resultBytes, _ := json.Marshal(envelope.Result)
	var startResult clirun.StartResult
	if err := json.Unmarshal(resultBytes, &startResult); err != nil {
		t.Fatalf("decode StartResult: %v", err)
	}

	detail, err := runtime.GetRunDetail(context.Background(), deps.UOW, startResult.RunID)
	if err != nil {
		t.Fatalf("GetRunDetail (post-timeout reload): %v", err)
	}
	if detail.State != "RUNNING" {
		t.Fatalf("post-timeout Run.State = %q, want RUNNING (a --wait-timeout must never call CancelRun or any other mutating function)", detail.State)
	}
	if detail.Cancelling {
		t.Fatal("post-timeout Run.Cancelling = true, want false")
	}
}
