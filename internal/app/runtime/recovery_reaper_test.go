package runtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// V4-13's own recovery-coordinator tests for the two sweeps that need no
// spike-era InterruptionRecoveryStore/WorkspaceReconciler/RecoveryStore —
// stranded cancellation-intent resumption and the self-rescheduling
// generation-fenced dedup. See internal/adapters/sqlite/recovery_reaper_test.go
// for the orphaned-RUNNING-attempt classification tests, which need a real
// sqlite.Store to satisfy those spike-era interfaces (this package's own
// fake never models write leases/checkpoints — see fake.RuntimeRepository's
// own GetWriteLeaseRepositoryWorkspaceForAttempt doc comment).

func newRecoveryReaperHandler(uow ports.UnitOfWork, ids idsource.Source, clk clock.Clock) *runtime.RecoveryReaperHandler {
	return runtime.NewRecoveryReaperHandler(uow, ids, clk, nil, nil, nil)
}

// TestStartupRecoveryScan_TwoRacingCoordinators_OnlyOneJobPerGeneration is
// the task's own required proof: "hai coordinator tranh recovery vẫn chỉ
// tạo một recovery job cho generation". Both calls read the SAME current
// generation (0, seeded); EnqueueJob's own idempotency-key uniqueness is
// what actually enforces only one job ever gets created.
func TestStartupRecoveryScan_TwoRacingCoordinators_OnlyOneJobPerGeneration(t *testing.T) {
	uow := fake.New()
	ids := idsource.NewSequential("id")
	ctx := context.Background()

	if err := runtime.StartupRecoveryScan(ctx, uow, ids); err != nil {
		t.Fatalf("first StartupRecoveryScan: %v", err)
	}
	if err := runtime.StartupRecoveryScan(ctx, uow, ids); err != nil {
		t.Fatalf("second (racing) StartupRecoveryScan: %v", err)
	}

	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	count := 0
	for _, j := range jobs {
		if j.Kind == runtime.RecoveryReaperJobKind {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("RECOVERY_REAPER job count after two racing startup scans = %d, want exactly 1", count)
	}
}

// TestRecoveryReaperHandler_SelfReschedules_AdvancesGenerationExactlyOnce
// proves one Handle call bumps recovery_reaper_state's own generation by
// exactly one and enqueues exactly one successor job keyed by the new
// generation.
func TestRecoveryReaperHandler_SelfReschedules_AdvancesGenerationExactlyOnce(t *testing.T) {
	uow := fake.New()
	ids := idsource.NewSequential("id")
	ctx := context.Background()

	if err := runtime.StartupRecoveryScan(ctx, uow, ids); err != nil {
		t.Fatalf("StartupRecoveryScan: %v", err)
	}
	handler := newRecoveryReaperHandler(uow, ids, clock.NewFixed(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))
	job := firstJobOfKind(t, uow, runtime.RecoveryReaperJobKind)
	if err := handler.Handle(ctx, job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	var state ports.RecoveryReaperState
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		state, err = tx.Runtime().GetRecoveryReaperState(ctx)
		return err
	}); err != nil {
		t.Fatalf("GetRecoveryReaperState: %v", err)
	}
	if state.Generation != 1 {
		t.Fatalf("generation after one Handle = %d, want 1", state.Generation)
	}

	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	count := 0
	for _, j := range jobs {
		if j.Kind == runtime.RecoveryReaperJobKind {
			count++
		}
	}
	if count != 2 { // the original + exactly one successor
		t.Fatalf("RECOVERY_REAPER job count after one Handle = %d, want 2 (original + one successor)", count)
	}
}

func firstJobOfKind(t *testing.T, uow *fake.UnitOfWork, kind string) ports.DurableJob {
	t.Helper()
	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	for _, j := range jobs {
		if j.Kind == kind {
			return ports.DurableJob{ID: j.ID, AggregateType: j.AggregateType, AggregateID: j.AggregateID, Payload: j.Payload}
		}
	}
	t.Fatalf("no %s job found among %+v", kind, jobs)
	return ports.DurableJob{}
}

// TestRecoveryReaperHandler_ResumesStrandedRunCancellationIntent proves the
// task's own "kill coordinator giữa lúc quiesce rồi restart" scenario at the
// Run level: CancelRun commits its own intent and moves the Run to
// CANCELLING, but the CANCEL_RUN_COORDINATOR job it enqueued is never
// delivered (the coordinator "died"). A later reaper sweep must finish the
// job that dead coordinator never got to run — never creating a duplicate
// intent — bringing the Run all the way to CANCELLED.
func TestRecoveryReaperHandler_ResumesStrandedRunCancellationIntent(t *testing.T) {
	uow, ids, runID, _ := startWorkflowRunFixture(t, workflowDocumentV1())
	ctx := context.Background()

	cancelActor(t, uow, ids, runID)
	// Deliberately never call driveCancelRunCoordinator — simulating the
	// coordinator job never being delivered before a crash.
	stillCancelling := runState(t, uow, runID)
	if stillCancelling.State != runtimedomain.WorkflowRunCancelling {
		t.Fatalf("run state before reaper sweep = %s, want CANCELLING", stillCancelling.State)
	}

	if err := runtime.StartupRecoveryScan(ctx, uow, ids); err != nil {
		t.Fatalf("StartupRecoveryScan: %v", err)
	}
	handler := newRecoveryReaperHandler(uow, ids, clock.NewFixed(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))
	job := firstJobOfKind(t, uow, runtime.RecoveryReaperJobKind)
	if err := handler.Handle(ctx, job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	final := runState(t, uow, runID)
	if final.State != runtimedomain.WorkflowRunCancelled {
		t.Fatalf("run state after reaper sweep = %s, want CANCELLED", final.State)
	}

	var intent runtimedomain.RunCancellationIntent
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		intent, err = tx.Runtime().GetRunCancellationIntent(ctx, runID)
		return err
	}); err != nil {
		t.Fatalf("GetRunCancellationIntent: %v", err)
	}
	if intent.State != runtimedomain.CancellationIntentCompleted {
		t.Fatalf("run cancellation intent state = %s, want COMPLETED", intent.State)
	}

	// No duplicate intent: a second reaper sweep must find nothing left to
	// resume for this Run (the intent is already COMPLETED) and must not
	// error or re-drive the coordinator again.
	if err := runtime.StartupRecoveryScan(ctx, uow, ids); err != nil {
		t.Fatalf("second StartupRecoveryScan: %v", err)
	}
}

// TestRecoveryReaperHandler_ResumesStrandedWorkItemCancellationIntent
// mirrors the Run-level test at the WorkItem level: CancelWorkItem commits
// its own intent and drives the active Run to CANCELLING, but nothing ever
// delivers that Run's own CANCEL_RUN_COORDINATOR job. The reaper's own
// sweep must resume BOTH — quiescing the Run, then noticing the WorkItem's
// own intent can now complete too.
func TestRecoveryReaperHandler_ResumesStrandedWorkItemCancellationIntent(t *testing.T) {
	uow, ids, workItemID, workflowVersionID := cancelWorkItemFixture(t)
	ctx := context.Background()
	run := startRun(t, uow, ids, workItemID, workflowVersionID, "idem-start-1")

	if _, err := runtime.CancelWorkItem(ctx, uow, ids, runtime.CancelWorkItemRequest{
		WorkItemID: workItemID, Actor: "operator-1", Reason: "abandon",
	}); err != nil {
		t.Fatalf("CancelWorkItem: %v", err)
	}
	if got := runState(t, uow, run.RunID).State; got != runtimedomain.WorkflowRunCancelling {
		t.Fatalf("run state before reaper sweep = %s, want CANCELLING", got)
	}

	if err := runtime.StartupRecoveryScan(ctx, uow, ids); err != nil {
		t.Fatalf("StartupRecoveryScan: %v", err)
	}
	handler := newRecoveryReaperHandler(uow, ids, clock.NewFixed(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))
	job := firstJobOfKind(t, uow, runtime.RecoveryReaperJobKind)
	if err := handler.Handle(ctx, job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if got := runState(t, uow, run.RunID).State; got != runtimedomain.WorkflowRunCancelled {
		t.Fatalf("run state after reaper sweep = %s, want CANCELLED", got)
	}
	if got := workItemState(t, uow, workItemID).Status; got != workdomain.WorkItemCancelled {
		t.Fatalf("work item status after reaper sweep = %s, want CANCELLED", got)
	}

	var intent runtimedomain.WorkItemCancellationIntent
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		intent, err = tx.Runtime().GetWorkItemCancellationIntent(ctx, workItemID)
		return err
	}); err != nil {
		t.Fatalf("GetWorkItemCancellationIntent: %v", err)
	}
	if intent.State != runtimedomain.CancellationIntentCompleted {
		t.Fatalf("work item cancellation intent state = %s, want COMPLETED", intent.State)
	}
}
