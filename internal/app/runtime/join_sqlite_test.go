package runtime_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// claimExecuteNodeJobRetrying is claimExecuteNodeJob's own sibling with a
// wider retry budget: this file's own fixtures enqueue more stray non-
// EXECUTE_NODE jobs ahead of the target than claimExecuteNodeJob's own
// fixed 5-attempt bound allows for (StartWorkflowRun's own ADVANCE_RUN job
// plus ONE ScheduleNodeRunJobKind job per FORK branch, all left
// unprocessed exactly like that helper's own doc comment already
// describes for its own single-node fixture).
func claimExecuteNodeJobRetrying(t *testing.T, ctx context.Context, store *sqlite.Store) (ports.DurableJob, ports.JobLease) {
	t.Helper()
	for i := 0; i < 20; i++ {
		job, lease, err := store.ClaimJob(ctx, "worker-1", 30*time.Second)
		if err != nil {
			t.Fatalf("ClaimJob: %v", err)
		}
		if job.Kind == runtime.ExecuteNodeJobKind {
			return job, lease
		}
	}
	t.Fatal("did not find an EXECUTE_NODE job to claim within 20 attempts")
	return ports.DurableJob{}, ports.JobLease{}
}

// seedRunningAndFinalizeSQLite drives nodeRunID's own already-scheduled
// (QUEUED) ExecutionAttempt all the way to a real, fenced SUCCEEDED/FAILED
// finalize against store — the exact "seed RUNNING, claim the real
// EXECUTE_NODE job, call FinalizeExecutionAttempt directly" sequence
// sqliteExecutionFixture/claimExecuteNodeJob
// (finalize_execution_attempt_sqlite_test.go) already established, reused
// here rather than going through ExecuteNodeHandler (which needs a
// ports.DurableJob wired up with a NodeExecutor, more machinery than this
// test's own restart-focused scope needs). Returns the real
// FinalizeExecutionAttemptResult so a caller can inspect
// result.AdvanceResult.JoinNodeRunID directly — there is no event-read API
// on ports.EventsRepository (Append-only) to recover it any other way.
func seedRunningAndFinalizeSQLite(
	t *testing.T, ctx context.Context, uow ports.UnitOfWork, store *sqlite.Store, ids idsource.Source,
	runID, nodeRunID, attemptID string, succeed bool,
) runtime.FinalizeExecutionAttemptResult {
	t.Helper()
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		currentNodeRun, err := tx.Runtime().GetNodeRun(ctx, nodeRunID)
		if err != nil {
			return err
		}
		if _, err := tx.Runtime().TransitionNodeRun(ctx, ports.TransitionNodeRunRequest{
			NodeRunID: nodeRunID, ExpectedState: runtimedomain.NodeRunQueued, ExpectedVersion: currentNodeRun.Version,
			NextState: runtimedomain.NodeRunRunning,
		}); err != nil {
			return err
		}
		currentAttempt, err := tx.Runtime().GetExecutionAttempt(ctx, attemptID)
		if err != nil {
			return err
		}
		_, err = tx.Runtime().TransitionExecutionAttempt(ctx, ports.TransitionExecutionAttemptRequest{
			AttemptID: attemptID, ExpectedState: runtimedomain.ExecutionAttemptQueued, ExpectedVersion: currentAttempt.Version,
			NextState: runtimedomain.ExecutionAttemptRunning,
		})
		return err
	}); err != nil {
		t.Fatalf("seed RUNNING attempt/node run %s: %v", nodeRunID, err)
	}

	_, lease := claimExecuteNodeJobRetrying(t, ctx, store)

	req := runtime.FinalizeExecutionAttemptRequest{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID, ExpectedVersion: 2, JobLease: lease,
	}
	if succeed {
		req.NextState = runtimedomain.ExecutionAttemptSucceeded
		req.TerminationReason = runtimedomain.TerminationReasonCompleted
		req.SelectedOutcome = "done"
	} else {
		req.NextState = runtimedomain.ExecutionAttemptFailed
		req.TerminationReason = runtimedomain.TerminationReasonExecutionFailed
		req.FailureCode = "EXECUTION_FAILED"
	}
	result, err := runtime.FinalizeExecutionAttempt(ctx, uow, ids, clock.System{}, req)
	if err != nil {
		t.Fatalf("FinalizeExecutionAttempt(%s, succeed=%v): %v", nodeRunID, succeed, err)
	}
	return result
}

// TestAdvanceRun_SQLite_Join_PersistsAcrossRestart is this task's own
// "restart" Verify-line test: branch A's own real, fenced completion (and
// the JOIN's own resulting WAITING NodeRun it creates, per this task's own
// locked "first arrival creates it WAITING" contract) commits BEFORE a
// real close/reopen of the underlying sqlite.Store; branch B's own real
// completion happens AFTER reopening, on a brand-new sqlite.Store/UnitOfWork
// instance — proving the JOIN's own get-or-create/verdict logic correctly
// observes durable state across a genuine process-restart boundary, not
// just within one in-memory *fake.UnitOfWork run.
func TestAdvanceRun_SQLite_Join_PersistsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agentkit-join-restart.db")

	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")

	root := readyFixtureSQLite(t, uow, ids, "project-1", "repo-1")
	seedEffectiveScope(t, uow, root.WorkItemID, "repo-1")
	version := publishWorkflowVersionDocument(t, uow, "project-1", "wf-def-1", "wf-v-1",
		joinPolicyDocument(workflow.JoinModeAll, 0, []string{"a", "b"}))
	publishJoinPolicyFixtures(t, uow)

	startCmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	startResult, err := runtime.StartWorkflowRun(ctx, uow, ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	hop, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: startResult.NodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun (start->fork): %v", err)
	}
	branchA := findForkedBranch(hop.ForkedBranches, "a")
	branchB := findForkedBranch(hop.ForkedBranches, "b")
	if branchA == nil || branchB == nil {
		t.Fatalf("ForkedBranches = %+v, want a and b", hop.ForkedBranches)
	}

	provider := fake.NewRuntimeExecutionConfigProvider()
	scheduledA, err := runtime.ScheduleExecutableNodeRun(ctx, uow, ids, provider, runtime.ScheduleExecutableNodeRunRequest{
		RunID: startResult.RunID, NodeRunID: branchA.NodeRunID, CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("ScheduleExecutableNodeRun(a): %v", err)
	}
	resultA := seedRunningAndFinalizeSQLite(t, ctx, uow, store, ids, startResult.RunID, branchA.NodeRunID, scheduledA.AttemptID, true)
	if !resultA.AdvanceResult.ReachedJoin || resultA.AdvanceResult.JoinNodeRunID == "" {
		t.Fatalf("branch A advance result = %+v, want ReachedJoin=true with a non-empty JoinNodeRunID", resultA.AdvanceResult)
	}
	if resultA.AdvanceResult.JoinDecided {
		t.Fatalf("branch A advance result = %+v, want JoinDecided=false (branch B has not run yet)", resultA.AdvanceResult)
	}
	joinNodeRunID := resultA.AdvanceResult.JoinNodeRunID

	var joinNodeRunBeforeRestart runtimedomain.NodeRun
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		joinNodeRunBeforeRestart, err = tx.Runtime().GetNodeRun(ctx, joinNodeRunID)
		return err
	}); err != nil {
		t.Fatalf("GetNodeRun(join) before restart: %v", err)
	}
	if joinNodeRunBeforeRestart.State != runtimedomain.NodeRunWaiting {
		t.Fatalf("join node run before restart = %+v, want WAITING (branch B still ACTIVE)", joinNodeRunBeforeRestart)
	}

	// Restart BETWEEN the two branches' own completions — the JOIN's own
	// WAITING NodeRun (created by branch A's own arrival) must survive
	// this exactly like any other durable state.
	if err := store.Close(); err != nil {
		t.Fatalf("close before restart: %v", err)
	}
	reopened, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen after restart: %v", err)
	}
	defer reopened.Close()
	reopenedUow := sqlite.NewUnitOfWork(reopened)

	scheduledB, err := runtime.ScheduleExecutableNodeRun(ctx, reopenedUow, ids, provider, runtime.ScheduleExecutableNodeRunRequest{
		RunID: startResult.RunID, NodeRunID: branchB.NodeRunID, CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("ScheduleExecutableNodeRun(b) after restart: %v", err)
	}
	resultB := seedRunningAndFinalizeSQLite(t, ctx, reopenedUow, reopened, ids, startResult.RunID, branchB.NodeRunID, scheduledB.AttemptID, true)
	if !resultB.AdvanceResult.ReachedJoin || resultB.AdvanceResult.JoinNodeRunID != joinNodeRunID {
		t.Fatalf("branch B advance result = %+v, want ReachedJoin=true with JoinNodeRunID=%s (same JOIN, get-or-create found it)", resultB.AdvanceResult, joinNodeRunID)
	}
	if !resultB.AdvanceResult.JoinDecided || resultB.AdvanceResult.JoinVerdict != "SUCCEEDED" {
		t.Fatalf("branch B advance result = %+v, want JoinDecided=true/JoinVerdict=SUCCEEDED (both branches now terminal)", resultB.AdvanceResult)
	}
	if resultB.AdvanceResult.JoinRoute == nil || resultB.AdvanceResult.JoinRoute.NextNodeKey != "end" {
		t.Fatalf("branch B advance result JoinRoute = %+v, want NextNodeKey=end", resultB.AdvanceResult.JoinRoute)
	}

	var tokens []runtimedomain.BranchToken
	var joinNodeRunAfterRestart runtimedomain.NodeRun
	if err := reopenedUow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		tokens, err = tx.Runtime().ListBranchTokensForFork(ctx, hop.NextNodeRunID)
		if err != nil {
			return err
		}
		joinNodeRunAfterRestart, err = tx.Runtime().GetNodeRun(ctx, joinNodeRunID)
		return err
	}); err != nil {
		t.Fatalf("read after restart: %v", err)
	}
	if len(tokens) != 2 {
		t.Fatalf("tokens after restart = %+v, want exactly 2", tokens)
	}
	for _, tok := range tokens {
		if tok.State != runtimedomain.BranchTokenSucceeded {
			t.Fatalf("token %+v after restart, want SUCCEEDED", tok)
		}
	}
	if joinNodeRunAfterRestart.State != runtimedomain.NodeRunSucceeded || joinNodeRunAfterRestart.SelectedOutcome != "joined" {
		t.Fatalf("join node run after restart = %+v, want SUCCEEDED/joined", joinNodeRunAfterRestart)
	}
}
