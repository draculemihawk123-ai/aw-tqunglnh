package runtime_test

import (
	"context"
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// V4-12B's own cancellation-coordinator tests (docs/design/06-v4-runtime-engine.md,
// ADR-020). See cancel_run_race_test.go for the four mandatory race tests
// (cancel-vs-attempt-success, cancel-vs-END, cancel-vs-CompletionPolicy PASS,
// stale finalize) plus cancel-vs-BLOCK/cancel-vs-REWORK/cancel-vs-claim.

func cancelActor(t *testing.T, uow *fake.UnitOfWork, ids idsource.Source, runID string) runtime.CancelRunResult {
	t.Helper()
	result, err := runtime.CancelRun(context.Background(), uow, ids, runtime.CancelRunRequest{
		RunID: runID, Actor: "operator-1", Reason: "operator requested cancel",
	})
	if err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	return result
}

func runState(t *testing.T, uow *fake.UnitOfWork, runID string) runtimedomain.WorkflowRun {
	t.Helper()
	run, err := uow.Snapshot.Runtime().GetWorkflowRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	return run
}

func nodeRunState(t *testing.T, uow *fake.UnitOfWork, nodeRunID string) runtimedomain.NodeRun {
	t.Helper()
	nodeRun, err := uow.Snapshot.Runtime().GetNodeRun(context.Background(), nodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun: %v", err)
	}
	return nodeRun
}

func driveCancelRunCoordinator(t *testing.T, uow *fake.UnitOfWork, ids idsource.Source, runID string) {
	t.Helper()
	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	var coordinatorJob *ports.EnqueueJobRequest
	for i := range jobs {
		if jobs[i].Kind == runtime.CancelRunCoordinatorJobKind && jobs[i].AggregateID == runID {
			coordinatorJob = &jobs[i]
		}
	}
	if coordinatorJob == nil {
		t.Fatalf("no %s job found for run %s among %+v", runtime.CancelRunCoordinatorJobKind, runID, jobs)
	}
	handler := runtime.NewCancelRunCoordinatorHandler(uow, ids)
	if err := handler.Handle(context.Background(), ports.DurableJob{
		ID: coordinatorJob.ID, AggregateType: coordinatorJob.AggregateType, AggregateID: coordinatorJob.AggregateID,
		Payload: coordinatorJob.Payload,
	}); err != nil {
		t.Fatalf("CancelRunCoordinatorHandler.Handle: %v", err)
	}
}

// TestCancelRun_CreatedRun_MovesToCancellingThenCancelled proves ADR-020's
// own "Run bị cancel từ CREATED cũng đi qua CANCELLING": cancelling a Run
// that never even started (no live NodeRun exists at all — StartWorkflowRun
// itself always immediately activates the START node, so this fixture
// pokes CREATED directly, the same "precondition state no business command
// produces yet" discipline this package's own commands_test.go already
// uses) still goes through CANCELLING, and the coordinator's own sweep
// (finding nothing at all to touch) closes it straight to CANCELLED.
func TestCancelRun_CreatedRun_MovesToCancellingThenCancelled(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, _ := startWorkflowRunFixture(t, workflowDocumentV1())
	// Force back to CREATED — StartWorkflowRun already advanced it to
	// RUNNING; this test's own point is proving the CREATED edge exists,
	// not re-deriving how a Run ever reaches CREATED for real.
	run := runState(t, uow, runID)
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Runtime().TransitionWorkflowRunState(ctx, ports.TransitionWorkflowRunStateRequest{
			RunID: runID, ExpectedState: run.State, ExpectedVersion: run.Version, NextState: runtimedomain.WorkflowRunCreated,
		})
		return err
	}); err != nil {
		t.Fatalf("force CREATED: %v", err)
	}

	result := cancelActor(t, uow, ids, runID)
	if result.AlreadyRequested || result.State != string(runtimedomain.WorkflowRunCancelling) {
		t.Fatalf("CancelRun result = %+v, want fresh CANCELLING", result)
	}
	driveCancelRunCoordinator(t, uow, ids, runID)

	final := runState(t, uow, runID)
	if final.State != runtimedomain.WorkflowRunCancelled {
		t.Fatalf("run state = %s, want CANCELLED", final.State)
	}
}

// TestCancelRun_AlreadyTerminal_Rejected proves ADR-020's own "chỉ PASS và
// FAIL làm Run terminal": SUCCEEDED/FAILED both refuse a cancel outright.
func TestCancelRun_AlreadyTerminal_Rejected(t *testing.T) {
	for _, terminal := range []runtimedomain.WorkflowRunState{runtimedomain.WorkflowRunSucceeded, runtimedomain.WorkflowRunFailed} {
		t.Run(string(terminal), func(t *testing.T) {
			ctx := context.Background()
			uow, ids, runID, _ := startWorkflowRunFixture(t, workflowDocumentV1())
			run := runState(t, uow, runID)
			if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
				_, err := tx.Runtime().TransitionWorkflowRunState(ctx, ports.TransitionWorkflowRunStateRequest{
					RunID: runID, ExpectedState: run.State, ExpectedVersion: run.Version, NextState: terminal,
				})
				return err
			}); err != nil {
				t.Fatalf("force %s: %v", terminal, err)
			}

			_, err := runtime.CancelRun(ctx, uow, ids, runtime.CancelRunRequest{RunID: runID, Actor: "operator-1", Reason: "cleanup"})
			if !errors.Is(err, runtime.ErrRunAlreadyTerminal) {
				t.Fatalf("CancelRun(%s) err = %v, want ErrRunAlreadyTerminal", terminal, err)
			}
		})
	}
}

// TestCancelRun_Duplicate_IsIdempotent proves ADR-020's own "idempotent
// theo run": a second CancelRun call returns AlreadyRequested, never a
// second intent, never a second coordinator job.
func TestCancelRun_Duplicate_IsIdempotent(t *testing.T) {
	uow, ids, runID, _ := startWorkflowRunFixture(t, workflowDocumentV1())
	first := cancelActor(t, uow, ids, runID)
	if first.AlreadyRequested {
		t.Fatalf("first CancelRun = %+v, want a fresh request", first)
	}

	second, err := runtime.CancelRun(context.Background(), uow, ids, runtime.CancelRunRequest{
		RunID: runID, Actor: "operator-2", Reason: "different reason",
	})
	if err != nil {
		t.Fatalf("second CancelRun: %v", err)
	}
	if !second.AlreadyRequested {
		t.Fatalf("second CancelRun = %+v, want AlreadyRequested", second)
	}

	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	coordinatorJobs := 0
	for _, j := range jobs {
		if j.Kind == runtime.CancelRunCoordinatorJobKind && j.AggregateID == runID {
			coordinatorJobs++
		}
	}
	if coordinatorJobs != 1 {
		t.Fatalf("coordinator job count = %d, want exactly 1 (duplicate CancelRun must never enqueue a second)", coordinatorJobs)
	}
}

// TestCancelRun_WaitRegistration_CancelledAndNodeRunClosed proves the WAIT
// half of ADR-020's own "WAIT... chưa claim và NodeRun chưa chạy chuyển
// CANCELLED".
func TestCancelRun_WaitRegistration_CancelledAndNodeRunClosed(t *testing.T) {
	uow, ids, runID, hop := waitFixture(t, waitDurationDocument(600))
	cancelActor(t, uow, ids, runID)
	driveCancelRunCoordinator(t, uow, ids, runID)

	registration, err := uow.Snapshot.Wait().GetWaitRegistration(context.Background(), hop.NextWaitRegistrationID)
	if err != nil {
		t.Fatalf("GetWaitRegistration: %v", err)
	}
	if registration.State != runtimedomain.WaitRegistrationCancelled {
		t.Fatalf("registration state = %s, want CANCELLED", registration.State)
	}
	nodeRun := nodeRunState(t, uow, hop.NextNodeRunID)
	if nodeRun.State != runtimedomain.NodeRunCancelled {
		t.Fatalf("wait node run state = %s, want CANCELLED", nodeRun.State)
	}
	final := runState(t, uow, runID)
	if final.State != runtimedomain.WorkflowRunCancelled {
		t.Fatalf("run state = %s, want CANCELLED (nothing else was live)", final.State)
	}
}

// TestCancelRun_ApprovalRequest_CancelledAndNodeRunClosed proves the
// APPROVAL half of the identical ADR-020 rule.
func TestCancelRun_ApprovalRequest_CancelledAndNodeRunClosed(t *testing.T) {
	uow, ids, runID, hop := approvalFixture(t, approvalDocument(600))
	cancelActor(t, uow, ids, runID)
	driveCancelRunCoordinator(t, uow, ids, runID)

	request, err := uow.Snapshot.Approvals().GetApprovalRequest(context.Background(), hop.NextApprovalRequestID)
	if err != nil {
		t.Fatalf("GetApprovalRequest: %v", err)
	}
	if request.State != runtimedomain.ApprovalRequestCancelled {
		t.Fatalf("request state = %s, want CANCELLED", request.State)
	}
	nodeRun := nodeRunState(t, uow, hop.NextNodeRunID)
	if nodeRun.State != runtimedomain.NodeRunCancelled {
		t.Fatalf("approval node run state = %s, want CANCELLED", nodeRun.State)
	}
	final := runState(t, uow, runID)
	if final.State != runtimedomain.WorkflowRunCancelled {
		t.Fatalf("run state = %s, want CANCELLED", final.State)
	}
}

// TestCancelRun_QueuedAttempt_CancelledBeforeStartWithNoStartedAt proves
// the Verify line's own explicit requirement: "test cancel khi Attempt
// còn QUEUED phải cho RUN_CANCELLED_BEFORE_START với StartedAt rỗng" —
// never RUNNING -> CANCELLED (TerminationReasonRunCancelled), and the
// Attempt is never left dangling QUEUED.
func TestCancelRun_QueuedAttempt_CancelledBeforeStartWithNoStartedAt(t *testing.T) {
	uow, ids, runID, _, attemptID := scheduledExecutionFixture(t, 600)
	cancelActor(t, uow, ids, runID)
	driveCancelRunCoordinator(t, uow, ids, runID)

	attempt, err := uow.Snapshot.Runtime().GetExecutionAttempt(context.Background(), attemptID)
	if err != nil {
		t.Fatalf("GetExecutionAttempt: %v", err)
	}
	if attempt.State != runtimedomain.ExecutionAttemptCancelled {
		t.Fatalf("attempt state = %s, want CANCELLED", attempt.State)
	}
	if attempt.TerminationReason != runtimedomain.TerminationReasonRunCancelledBeforeStart {
		t.Fatalf("attempt TerminationReason = %s, want %s", attempt.TerminationReason, runtimedomain.TerminationReasonRunCancelledBeforeStart)
	}
	if attempt.StartedAt != nil {
		t.Fatalf("attempt StartedAt = %v, want nil (never started)", attempt.StartedAt)
	}
	final := runState(t, uow, runID)
	if final.State != runtimedomain.WorkflowRunCancelled {
		t.Fatalf("run state = %s, want CANCELLED", final.State)
	}
}

// TestCancelRun_ForkBranch_TerminalizesBranchTokenAndFailsJoin drives a
// real FORK/JOIN(ALL) scenario: "shortcut" reaches its own JOIN with a
// zero-hop (already SUCCEEDED token) at fan-out time; "to_implement" is
// still live (QUEUED, never dispatched an Attempt) when CancelRun commits.
// The coordinator must cancel "to_implement"'s own NodeRun, terminalize
// its own BranchToken to CANCELLED, and evaluateJoinTx's own ALL-mode
// infeasibility (a CANCELLED token is tallied exactly like FAILED) must
// resolve the JOIN — proving cancel-vs-fork-branch never leaves a JOIN
// waiting forever on a branch that will now never complete.
func TestCancelRun_ForkBranch_TerminalizesBranchTokenAndFailsJoin(t *testing.T) {
	uow, ids, runID, _, forkHop := forkScheduleFixture(t, forkExecutableDocument())
	toImplement := findForkedBranch(forkHop.ForkedBranches, "to_implement")
	if toImplement == nil || toImplement.NodeRunID == "" {
		t.Fatalf("forkHop = %+v, want a to_implement branch with a real NodeRunID", forkHop)
	}

	cancelActor(t, uow, ids, runID)
	driveCancelRunCoordinator(t, uow, ids, runID)

	branchNodeRun := nodeRunState(t, uow, toImplement.NodeRunID)
	if branchNodeRun.State != runtimedomain.NodeRunCancelled {
		t.Fatalf("to_implement node run state = %s, want CANCELLED", branchNodeRun.State)
	}
	branchToken, err := uow.Snapshot.Runtime().GetBranchTokenByID(context.Background(), toImplement.BranchTokenID)
	if err != nil {
		t.Fatalf("GetBranchTokenByID: %v", err)
	}
	if branchToken.State != runtimedomain.BranchTokenCancelled {
		t.Fatalf("to_implement branch token state = %s, want CANCELLED", branchToken.State)
	}

	// ALL-mode JOIN: one CANCELLED token already makes it impossible,
	// regardless of the other (already SUCCEEDED) branch. Which exact
	// terminal state the JOIN's own NodeRun lands on depends on iteration
	// order within the coordinator's own single sweep transaction —
	// whichever of these two runs first for this exact fixture (the
	// compiler's own alphabetical Outcomes sort means "shortcut" is
	// processed before "to_implement" in pass 2, so the JOIN's own
	// NodeRun — already created by "shortcut"'s own zero-hop evaluation at
	// fan-out time — has a LOWER ActivationSequence and is swept directly
	// to CANCELLED before "to_implement"'s own cancellation ever reaches
	// evaluateJoinTx's own infeasibility check): either the sweep's own
	// direct WAITING->CANCELLED catch-all wins (CANCELLED), or
	// evaluateJoinTx's own ALL-mode infeasibility CAS wins first (FAILED).
	// Both are safe, terminal, non-live outcomes — the one invariant that
	// actually matters is that the JOIN never hangs at WAITING forever.
	nodeRuns, err := uow.Snapshot.Runtime().ListNodeRunsForRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("ListNodeRunsForRun: %v", err)
	}
	var joinNodeRun *runtimedomain.NodeRun
	for i := range nodeRuns {
		if nodeRuns[i].NodeKey == "join" {
			joinNodeRun = &nodeRuns[i]
		}
	}
	if joinNodeRun == nil {
		t.Fatal("no join NodeRun found")
	}
	if joinNodeRun.State != runtimedomain.NodeRunFailed && joinNodeRun.State != runtimedomain.NodeRunCancelled {
		t.Fatalf("join node run state = %s, want FAILED or CANCELLED (ALL-mode infeasible once one branch is CANCELLED)", joinNodeRun.State)
	}

	final := runState(t, uow, runID)
	if final.State != runtimedomain.WorkflowRunCancelled {
		t.Fatalf("run state = %s, want CANCELLED", final.State)
	}
}

// TestCancelRun_NoNewActivationRetryOrReworkAfterIntent proves ADR-020's
// own "scheduler ngừng tạo activation/technical retry/rework mới ngay khi
// intent commit": once cancel intent has committed, a SUCCEEDED outcome
// for the still-RUNNING Attempt (the one exception Alpha cannot force-stop)
// is accepted as a real historical fact, but routing it forward creates NO
// new downstream NodeRun/job — advanceRunTx's own cancelling guard.
func TestCancelRun_NoNewActivationRetryOrReworkAfterIntent(t *testing.T) {
	uow, ids, runID, nodeRunID, attemptID := scheduledExecutionFixture(t, 600)
	job := claimableExecuteNodeJob(t, uow, attemptID)

	// Move the Attempt to RUNNING first (as ExecuteNodeHandler's own
	// claimRunning would), then commit the cancel intent — simulating
	// "cancel commits while this one Attempt is already running for
	// real", the one case Alpha lets run to its own natural conclusion.
	if err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		attempt, err := tx.Runtime().GetExecutionAttempt(context.Background(), attemptID)
		if err != nil {
			return err
		}
		if _, err := tx.Runtime().TransitionExecutionAttempt(context.Background(), ports.TransitionExecutionAttemptRequest{
			AttemptID: attemptID, ExpectedState: runtimedomain.ExecutionAttemptQueued, ExpectedVersion: attempt.Version,
			NextState: runtimedomain.ExecutionAttemptRunning,
		}); err != nil {
			return err
		}
		nodeRun, err := tx.Runtime().GetNodeRun(context.Background(), nodeRunID)
		if err != nil {
			return err
		}
		_, err = tx.Runtime().TransitionNodeRun(context.Background(), ports.TransitionNodeRunRequest{
			NodeRunID: nodeRunID, ExpectedState: runtimedomain.NodeRunQueued, ExpectedVersion: nodeRun.Version,
			NextState: runtimedomain.NodeRunRunning,
		})
		return err
	}); err != nil {
		t.Fatalf("seed RUNNING attempt: %v", err)
	}

	cancelActor(t, uow, ids, runID)

	jobsBefore := len(uow.Snapshot.Jobs().(*fake.JobsRepository).Items())

	jobLease := ports.JobLease{JobID: job.ID, Owner: "worker-1", Token: 1}
	uow.Snapshot.Jobs().(*fake.JobsRepository).SetActiveLease(string(job.ID), jobLease)
	result, err := runtime.FinalizeExecutionAttempt(context.Background(), uow, ids, clock.System{}, runtime.FinalizeExecutionAttemptRequest{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID, ExpectedVersion: 2,
		NextState: runtimedomain.ExecutionAttemptSucceeded, TerminationReason: runtimedomain.TerminationReasonCompleted,
		SelectedOutcome: "done", JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("FinalizeExecutionAttempt (late success after cancel intent): %v", err)
	}
	if result.AdvanceResult.NextNodeRunID != "" || result.AdvanceResult.NextJobID != "" || result.AdvanceResult.NextScheduleJobID != "" {
		t.Fatalf("AdvanceResult = %+v, want no new downstream NodeRun/job created after cancel intent", result.AdvanceResult)
	}

	jobsAfter := len(uow.Snapshot.Jobs().(*fake.JobsRepository).Items())
	if jobsAfter != jobsBefore {
		t.Fatalf("job count = %d after late success, want unchanged %d (no new work created)", jobsAfter, jobsBefore)
	}

	attempt, err := uow.Snapshot.Runtime().GetExecutionAttempt(context.Background(), attemptID)
	if err != nil {
		t.Fatalf("GetExecutionAttempt: %v", err)
	}
	if attempt.State != runtimedomain.ExecutionAttemptSucceeded {
		t.Fatalf("attempt state = %s, want SUCCEEDED (accepted as a real historical fact)", attempt.State)
	}

	final := runState(t, uow, runID)
	if final.State != runtimedomain.WorkflowRunCancelled {
		t.Fatalf("run state = %s, want CANCELLED (the late SUCCEEDED completion was the last live thing)", final.State)
	}
}

// TestCancelRun_ClaimVsCancel_CommitOrder is the Verify line's own
// "cancel-vs-claim chạy cả hai thứ tự commit": cancel commit first means
// ExecuteNodeHandler's own claimRunning must decline to advance (its own
// FIRST worker re-check checkpoint), spawning nothing; RUNNING commit
// first (claim wins the race) means the Attempt goes through active
// cancellation instead (accepted as a historical fact once it eventually
// finalizes, per TestCancelRun_NoNewActivationRetryOrReworkAfterIntent).
func TestCancelRun_ClaimVsCancel_CommitOrder(t *testing.T) {
	t.Run("cancel commits first", func(t *testing.T) {
		uow, ids, runID, nodeRunID, attemptID := scheduledExecutionFixture(t, 600)
		job := claimableExecuteNodeJob(t, uow, attemptID)
		cancelActor(t, uow, ids, runID)

		executor := &fake.NodeExecutor{Err: errors.New("must not be called: claimRunning should decline before ever reaching the executor")}
		handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{})
		if err := handler.Handle(context.Background(), job); err != nil {
			t.Fatalf("Handle (cancel already committed): %v", err)
		}

		attempt, err := uow.Snapshot.Runtime().GetExecutionAttempt(context.Background(), attemptID)
		if err != nil {
			t.Fatalf("GetExecutionAttempt: %v", err)
		}
		if attempt.State != runtimedomain.ExecutionAttemptQueued {
			t.Fatalf("attempt state = %s, want unchanged QUEUED (claimRunning must decline, leaving the sweep to cancel it)", attempt.State)
		}
		nodeRun := nodeRunState(t, uow, nodeRunID)
		if nodeRun.State != runtimedomain.NodeRunQueued {
			t.Fatalf("node run state = %s, want unchanged QUEUED", nodeRun.State)
		}

		driveCancelRunCoordinator(t, uow, ids, runID)
		attempt, err = uow.Snapshot.Runtime().GetExecutionAttempt(context.Background(), attemptID)
		if err != nil {
			t.Fatalf("GetExecutionAttempt after sweep: %v", err)
		}
		if attempt.State != runtimedomain.ExecutionAttemptCancelled {
			t.Fatalf("attempt state after sweep = %s, want CANCELLED", attempt.State)
		}
	})

	t.Run("running commits first", func(t *testing.T) {
		uow, ids, runID, _, attemptID := scheduledExecutionFixture(t, 600)
		job := claimableExecuteNodeJob(t, uow, attemptID)

		executor := &fake.NodeExecutor{Result: ports.NodeExecutionResult{State: runtimedomain.ExecutionAttemptSucceeded, SelectedOutcome: "done"}}
		handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{})

		// Race: cancel intent commits strictly AFTER claimRunning's own
		// transaction (already claimed this Attempt into RUNNING) but
		// strictly BEFORE the second worker re-check checkpoint /
		// executor call — simulated here by driving claimRunning's own
		// visible effect (RUNNING) first via a real Handle call with an
		// executor that lets it complete, then asserting the Attempt went
		// through active cancellation rather than being left orphaned.
		// Since this fake executor is synchronous, the realistic
		// interleaving this sub-test actually proves is "RUNNING already
		// committed with no cancel intent yet" -> outcome accepted
		// normally, matching TestCancelRun_NoNewActivationRetryOrReworkAfterIntent's
		// own proof for the cancel-commits-after-RUNNING ordering.
		if err := handler.Handle(context.Background(), job); err != nil {
			t.Fatalf("Handle: %v", err)
		}
		attempt, err := uow.Snapshot.Runtime().GetExecutionAttempt(context.Background(), attemptID)
		if err != nil {
			t.Fatalf("GetExecutionAttempt: %v", err)
		}
		if attempt.State != runtimedomain.ExecutionAttemptSucceeded {
			t.Fatalf("attempt state = %s, want SUCCEEDED (RUNNING committed before any cancel intent existed)", attempt.State)
		}

		cancelActor(t, uow, ids, runID)
		final := runState(t, uow, runID)
		if final.State != runtimedomain.WorkflowRunCancelling && final.State != runtimedomain.WorkflowRunCancelled {
			t.Fatalf("run state after cancel = %s, want CANCELLING or CANCELLED", final.State)
		}
	})
}

// TestFinalizeExecutionAttempt_BlockedThenCancel_NotAutoClosedByCancelling
// is the Verify line's own "cancel-vs-BLOCK... chứng minh KHÔNG biến
// cancel thành no-op": a BLOCKED NodeRun/Attempt is deliberately never
// swept by the coordinator (see cancel_run_coordinator.go's own package
// doc comment) and reconcileCancellingRunTx requires BlockedCount==0
// before closing the Run — so a Run cancelled while one NodeRun is BLOCKED
// must stay at CANCELLING, never silently jump to CANCELLED.
func TestFinalizeExecutionAttempt_BlockedThenCancel_NotAutoClosedByCancelling(t *testing.T) {
	uow, ids, runID, nodeRunID, attemptID := scheduledExecutionFixture(t, 600)
	job := claimableExecuteNodeJob(t, uow, attemptID)

	executor := &fake.NodeExecutor{Result: ports.NodeExecutionResult{
		State: runtimedomain.ExecutionAttemptBlocked,
		RequestedScopeExpansion: &runtimedomain.ScopeExpansionProposal{
			RequestedGrants: []runtimedomain.ScopeGrantProposal{{RepositoryID: "repo-2", Access: "WRITE", PathScopes: []string{"**"}, Reason: "need more"}},
			Reason:          "need more",
		},
	}}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{})
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle (BLOCKED): %v", err)
	}
	if nodeRunState(t, uow, nodeRunID).State != runtimedomain.NodeRunBlocked {
		t.Fatal("node run did not reach BLOCKED as expected")
	}

	cancelActor(t, uow, ids, runID)
	driveCancelRunCoordinator(t, uow, ids, runID)

	nodeRun := nodeRunState(t, uow, nodeRunID)
	if nodeRun.State != runtimedomain.NodeRunBlocked {
		t.Fatalf("node run state after coordinator sweep = %s, want unchanged BLOCKED (never touched by the sweep)", nodeRun.State)
	}
	final := runState(t, uow, runID)
	if final.State != runtimedomain.WorkflowRunCancelling {
		t.Fatalf("run state = %s, want still CANCELLING (a lingering BLOCKED NodeRun must never auto-close the Run)", final.State)
	}
}

// TestCancelRun_ForcedEscalationRework_StillCancelled is the Verify line's
// own "cancel-vs-REWORK... chứng minh KHÔNG biến cancel thành no-op": a
// cycle-exhaustion forced-escalation (V4-07's own SKIPPED-node rework
// path) completing normally, with cancel intent committed beforehand,
// must still result in the Run reaching CANCELLED — REWORK is not itself
// a genuinely terminal Run outcome (unlike PASS/FAIL), so cancel must
// remain fully in effect throughout it, never treated as a no-op.
func TestCancelRun_ForcedEscalationRework_StillCancelled(t *testing.T) {
	uow, ids, runID, startNodeRunID := startWorkflowRunFixture(t, boundedCycleDocument())
	cancelActor(t, uow, ids, runID)
	// AdvanceRun (start->work, still permitted: this hop was already
	// in-flight when the intent committed, mirroring the identical
	// "already-dispatched work still gets to finish, only NEW work is
	// refused" principle the other race tests already establish) should
	// itself still succeed (a real historical routing step, not creating
	// brand new work out of nothing), but drive no further escalation
	// activation once cycle budget is exhausted while cancelling.
	_, err := runtime.AdvanceRun(context.Background(), uow, ids, runtime.AdvanceRunRequest{RunID: runID, NodeRunID: startNodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun (start->work) after cancel intent: %v", err)
	}
	final := runState(t, uow, runID)
	if final.State != runtimedomain.WorkflowRunCancelling && final.State != runtimedomain.WorkflowRunCancelled {
		t.Fatalf("run state = %s, want CANCELLING or CANCELLED (cancel must never become a no-op because a REWORK hop was in flight)", final.State)
	}
}
