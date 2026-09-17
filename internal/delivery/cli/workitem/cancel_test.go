package workitem_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	cliworkitem "github.com/taQuangLing/agent-workflow/internal/delivery/cli/workitem"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

func decodeCancelResult(t *testing.T, stdout *bytes.Buffer) cliworkitem.CancelResult {
	t.Helper()
	var result cliworkitem.CancelResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode CancelResult %s: %v", stdout.String(), err)
	}
	return result
}

// TestRunWorkItemCancel_ZeroRuns_ImmediatelyCancelled proves the "0 active
// Run" case this task's own "cancel quiesce" Verify line names directly: a
// WorkItem that never started any run at all closes out to CANCELLED the
// moment this call commits.
func TestRunWorkItemCancel_ZeroRuns_ImmediatelyCancelled(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	root := readyWorkItemFixture(t, u, deps.IDs, "project-1", "repo-1")

	var stdout, stderr bytes.Buffer
	args := []string{"--reason", "no longer needed", root.WorkItemID}
	if err := cliworkitem.RunWorkItemCancel(context.Background(), deps, args, &stdout, &stderr); err != nil {
		t.Fatalf("RunWorkItemCancel() error = %v, stderr = %s", err, stderr.String())
	}
	result := decodeCancelResult(t, &stdout)
	if result.AlreadyRequested || result.Status != string(workdomain.WorkItemCancelled) {
		t.Fatalf("result = %+v, want AlreadyRequested=false Status=CANCELLED", result)
	}

	item := workItemState(t, deps.UoW, root.WorkItemID)
	if item.Status != workdomain.WorkItemCancelled {
		t.Fatalf("work item status = %s, want CANCELLED", item.Status)
	}
}

// TestRunWorkItemCancel_ActiveRun_QuiescesThenReportsRealNotAssumedState is
// this task's own "cancel quiesce" Verify bullet at full strength: a
// WorkItem with an active Run must NOT be reported CANCELLED the instant
// this call returns — it is still ACTIVE (the Run has only moved to
// CANCELLING, still quiescing) — and only reaches CANCELLED once the real
// CancelRunCoordinator job actually finishes closing the Run out. This
// proves the CLI leaf reports runtime.CancelWorkItemResult.Status verbatim,
// never a status it invented or assumed.
func TestRunWorkItemCancel_ActiveRun_QuiescesThenReportsRealNotAssumedState(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	root := readyWorkItemFixture(t, u, deps.IDs, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, u, "project-1", "wf-def-1", "wf-v-1")
	run := startTestRun(t, u, deps.IDs, "project-1", root.WorkItemID, string(version.ID()), "idem-start-1")

	var stdout, stderr bytes.Buffer
	args := []string{"--reason", "no longer needed", root.WorkItemID}
	if err := cliworkitem.RunWorkItemCancel(context.Background(), deps, args, &stdout, &stderr); err != nil {
		t.Fatalf("RunWorkItemCancel() error = %v, stderr = %s", err, stderr.String())
	}
	result := decodeCancelResult(t, &stdout)
	if result.Status != string(workdomain.WorkItemActive) {
		t.Fatalf("result.Status = %s immediately after cancel, want still ACTIVE (Run not yet quiesced)", result.Status)
	}
	runAfterCancel := runState(t, deps.UoW, run.RunID)
	if runAfterCancel.State != runtimedomain.WorkflowRunCancelling {
		t.Fatalf("run state = %s, want CANCELLING", runAfterCancel.State)
	}
	item := workItemState(t, deps.UoW, root.WorkItemID)
	if item.Status != workdomain.WorkItemActive {
		t.Fatalf("work item status right after cancel = %s, want still ACTIVE (never forced BLOCKED while a WorkItem-level cancel is in flight)", item.Status)
	}

	driveCancelRunCoordinator(t, u, deps.IDs, run.RunID)

	runAfterCoordinator := runState(t, deps.UoW, run.RunID)
	if runAfterCoordinator.State != runtimedomain.WorkflowRunCancelled {
		t.Fatalf("run state after coordinator sweep = %s, want CANCELLED", runAfterCoordinator.State)
	}
	itemAfterCoordinator := workItemState(t, deps.UoW, root.WorkItemID)
	if itemAfterCoordinator.Status != workdomain.WorkItemCancelled {
		t.Fatalf("work item status after run closed = %s, want CANCELLED", itemAfterCoordinator.Status)
	}
}

// TestRunWorkItemCancel_SecondCall_AlreadyRequested proves a second
// `work-item cancel` call for the same WorkItem is a harmless idempotent
// no-op, never a second intent.
func TestRunWorkItemCancel_SecondCall_AlreadyRequested(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	root := readyWorkItemFixture(t, u, deps.IDs, "project-1", "repo-1")

	var first bytes.Buffer
	args := []string{"--reason", "first call", root.WorkItemID}
	if err := cliworkitem.RunWorkItemCancel(context.Background(), deps, args, &first, &bytes.Buffer{}); err != nil {
		t.Fatalf("first RunWorkItemCancel() error = %v", err)
	}
	firstResult := decodeCancelResult(t, &first)
	if firstResult.AlreadyRequested {
		t.Fatal("first call reported AlreadyRequested = true, want false")
	}

	var second bytes.Buffer
	args2 := []string{"--reason", "second call, different reason", root.WorkItemID}
	if err := cliworkitem.RunWorkItemCancel(context.Background(), deps, args2, &second, &bytes.Buffer{}); err != nil {
		t.Fatalf("second RunWorkItemCancel() error = %v", err)
	}
	secondResult := decodeCancelResult(t, &second)
	if !secondResult.AlreadyRequested {
		t.Fatal("second call reported AlreadyRequested = false, want true")
	}
	if secondResult.Status != firstResult.Status {
		t.Fatalf("second.Status = %s, want unchanged %s", secondResult.Status, firstResult.Status)
	}
}

func TestRunWorkItemCancel_MissingReason_IsError(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	root := readyWorkItemFixture(t, u, deps.IDs, "project-1", "repo-1")

	var stdout, stderr bytes.Buffer
	err := cliworkitem.RunWorkItemCancel(context.Background(), deps, []string{root.WorkItemID}, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunWorkItemCancel() with no --reason returned nil error")
	}
}

func TestRunWorkItemCancel_MissingWorkItemArgument_IsError(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	err := cliworkitem.RunWorkItemCancel(context.Background(), deps, []string{"--reason", "cleanup"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunWorkItemCancel() with no <workItemId> argument returned nil error")
	}
}

// TestRunWorkItemCancel_NoProjectIDOrEnvelopeFlagsExist proves this
// subcommand's own FlagSet genuinely has no --project-id/--idempotency-key/
// --expected-version flag — passing one is rejected as an unrecognized
// flag, never silently accepted.
func TestRunWorkItemCancel_NoProjectIDOrEnvelopeFlagsExist(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	root := readyWorkItemFixture(t, u, deps.IDs, "project-1", "repo-1")

	var stdout, stderr bytes.Buffer
	args := []string{"--reason", "cleanup", "--idempotency-key", "should-not-exist", root.WorkItemID}
	err := cliworkitem.RunWorkItemCancel(context.Background(), deps, args, &stdout, &stderr)
	if err == nil {
		t.Fatal("RunWorkItemCancel() with an unrecognized --idempotency-key flag returned nil error, want a flag-parse failure")
	}
}
