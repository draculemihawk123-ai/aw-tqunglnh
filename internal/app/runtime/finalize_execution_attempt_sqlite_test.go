package runtime_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// sqliteExecutionFixture is scheduledExecutionFixture's own real-backend
// twin: publishes the same full AGENT resolution chain against a real
// sqlite.Store, schedules the NodeRun (ScheduleExecutableNodeRun), then
// drives both the NodeRun and its Attempt QUEUED->RUNNING directly
// (claimRunning's own job, normally done by ExecuteNodeHandler) — the deep
// JobLease/WriteLease fencing edge cases this file's own tests exercise are
// SQLite-only per this task's own test-layering decision (a fake test only
// proves application flow, never real fencing — see baocaov4checklist.md).
func sqliteExecutionFixture(t *testing.T) (uow ports.UnitOfWork, store *sqlite.Store, ids idsource.Source, runID, nodeRunID, attemptID string) {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agentkit-finalize-execution-attempt.db")
	s, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	u := sqlite.NewUnitOfWork(s)
	seq := idsource.NewSequential("id")

	root := readyFixtureSQLite(t, u, seq, "project-1", "repo-1")
	seedEffectiveScope(t, u, root.WorkItemID, "repo-1")
	version := publishWorkflowVersionDocument(t, u, "project-1", "wf-def-1", "wf-v-1",
		agentExecutableDocument("agent-profile-v1", fullyResolvablePolicyRefs(), nil))
	publishAgentProfileVersion(t, u, "agent-profile-def", "agent-profile-v1", validAgentProfileDocument())
	publishPolicyVersion(t, u, "attempt-policy-def", "attempt-policy-v1", attemptPolicyDocument(600))
	publishPolicyVersion(t, u, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())

	startCmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	startResult, err := runtime.StartWorkflowRun(ctx, u, seq, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	hop, err := runtime.AdvanceRun(ctx, u, seq, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: startResult.NodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun (start->implement): %v", err)
	}

	scheduled, err := runtime.ScheduleExecutableNodeRun(ctx, u, seq, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: startResult.RunID, NodeRunID: hop.NextNodeRunID, CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("ScheduleExecutableNodeRun: %v", err)
	}

	// claimRunning's own job (execute.go), driven directly.
	if err := u.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		currentNodeRun, err := tx.Runtime().GetNodeRun(ctx, hop.NextNodeRunID)
		if err != nil {
			return err
		}
		if _, err := tx.Runtime().TransitionNodeRun(ctx, ports.TransitionNodeRunRequest{
			NodeRunID: hop.NextNodeRunID, ExpectedState: runtimedomain.NodeRunQueued, ExpectedVersion: currentNodeRun.Version,
			NextState: runtimedomain.NodeRunRunning,
		}); err != nil {
			return err
		}
		currentAttempt, err := tx.Runtime().GetExecutionAttempt(ctx, scheduled.AttemptID)
		if err != nil {
			return err
		}
		_, err = tx.Runtime().TransitionExecutionAttempt(ctx, ports.TransitionExecutionAttemptRequest{
			AttemptID: scheduled.AttemptID, ExpectedState: runtimedomain.ExecutionAttemptQueued, ExpectedVersion: currentAttempt.Version,
			NextState: runtimedomain.ExecutionAttemptRunning,
		})
		return err
	}); err != nil {
		t.Fatalf("seed RUNNING attempt/node run: %v", err)
	}

	return u, s, seq, startResult.RunID, hop.NextNodeRunID, scheduled.AttemptID
}

// claimExecuteNodeJob claims jobs off store's own queue until it gets the
// EXECUTE_NODE one — this fixture never processes StartWorkflowRun's own
// ADVANCE_RUN job or AdvanceRun's own SCHEDULE_NODE_RUN job through their
// real handlers (it calls AdvanceRun/ScheduleExecutableNodeRun directly
// instead), so both are left sitting AVAILABLE, older than (and therefore
// claimed before) the EXECUTE_NODE job a bare ClaimJob call would reach.
func claimExecuteNodeJob(t *testing.T, ctx context.Context, store *sqlite.Store, ttl time.Duration) (ports.DurableJob, ports.JobLease) {
	t.Helper()
	for i := 0; i < 5; i++ {
		job, lease, err := store.ClaimJob(ctx, "worker-1", ttl)
		if err != nil {
			t.Fatalf("ClaimJob: %v", err)
		}
		if job.Kind == runtime.ExecuteNodeJobKind {
			return job, lease
		}
	}
	t.Fatal("did not find an EXECUTE_NODE job to claim within 5 attempts")
	return ports.DurableJob{}, ports.JobLease{}
}

func TestFinalizeExecutionAttempt_SQLite_ExpiredLease_RollsBackEverything(t *testing.T) {
	ctx := context.Background()
	uow, store, ids, runID, nodeRunID, attemptID := sqliteExecutionFixture(t)

	// A short TTL, then a real sleep past it — ValidateActiveJob/CompleteJob
	// only ever trust the database's own authoritative lease_until column
	// (never a caller-supplied JobLease.LeaseUntil value, which this test
	// originally tried to tamper with before discovering that field is
	// purely descriptive on the caller's own side), so this is the only way
	// to actually exercise a genuinely expired lease.
	_, lease := claimExecuteNodeJob(t, ctx, store, 50*time.Millisecond)
	time.Sleep(100 * time.Millisecond)

	_, err := runtime.FinalizeExecutionAttempt(ctx, uow, ids, clock.System{}, runtime.FinalizeExecutionAttemptRequest{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID, ExpectedVersion: 2,
		NextState: runtimedomain.ExecutionAttemptSucceeded, TerminationReason: runtimedomain.TerminationReasonCompleted,
		SelectedOutcome: "done", JobLease: lease,
	})
	if !errors.Is(err, ports.ErrJobLeaseLost) {
		t.Fatalf("err = %v, want ErrJobLeaseLost", err)
	}
	assertAttemptNodeRunUnchanged(t, ctx, uow, nodeRunID, attemptID)
}

func TestFinalizeExecutionAttempt_SQLite_WrongOwnerAndToken_RollsBackEverything(t *testing.T) {
	ctx := context.Background()
	uow, store, ids, runID, nodeRunID, attemptID := sqliteExecutionFixture(t)

	_, lease := claimExecuteNodeJob(t, ctx, store, 30*time.Second)

	wrongOwner := ports.JobLease{JobID: lease.JobID, Owner: "someone-else", Token: lease.Token, LeaseUntil: lease.LeaseUntil}
	_, err := runtime.FinalizeExecutionAttempt(ctx, uow, ids, clock.System{}, runtime.FinalizeExecutionAttemptRequest{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID, ExpectedVersion: 2,
		NextState: runtimedomain.ExecutionAttemptSucceeded, TerminationReason: runtimedomain.TerminationReasonCompleted,
		SelectedOutcome: "done", JobLease: wrongOwner,
	})
	if !errors.Is(err, ports.ErrJobLeaseLost) {
		t.Fatalf("wrong owner err = %v, want ErrJobLeaseLost", err)
	}

	wrongToken := ports.JobLease{JobID: lease.JobID, Owner: lease.Owner, Token: lease.Token + 1, LeaseUntil: lease.LeaseUntil}
	_, err = runtime.FinalizeExecutionAttempt(ctx, uow, ids, clock.System{}, runtime.FinalizeExecutionAttemptRequest{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID, ExpectedVersion: 2,
		NextState: runtimedomain.ExecutionAttemptSucceeded, TerminationReason: runtimedomain.TerminationReasonCompleted,
		SelectedOutcome: "done", JobLease: wrongToken,
	})
	if !errors.Is(err, ports.ErrJobLeaseLost) {
		t.Fatalf("wrong token err = %v, want ErrJobLeaseLost", err)
	}
	assertAttemptNodeRunUnchanged(t, ctx, uow, nodeRunID, attemptID)
}

func TestFinalizeExecutionAttempt_SQLite_WriteLeaseWrongFenceToken_RollsBackEverything(t *testing.T) {
	ctx := context.Background()
	uow, store, ids, runID, nodeRunID, attemptID := sqliteExecutionFixture(t)

	_, lease := claimExecuteNodeJob(t, ctx, store, 30*time.Second)

	var repositoryWorkspaceID string
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		nodeRun, err := tx.Runtime().GetNodeRun(ctx, nodeRunID)
		if err != nil {
			return err
		}
		run, err := tx.Runtime().GetWorkflowRun(ctx, runID)
		if err != nil {
			return err
		}
		set, err := tx.Work().GetWorkspaceSetByFamilyID(ctx, string(run.FamilyID))
		if err != nil {
			return err
		}
		rw, err := tx.Work().GetRepositoryWorkspace(ctx, string(set.ID), string(nodeRun.EffectiveScope[0].RepositoryID()), 1)
		if err != nil {
			return err
		}
		repositoryWorkspaceID = string(rw.ID)
		return nil
	}); err != nil {
		t.Fatalf("resolve repository workspace: %v", err)
	}

	grants, err := store.AcquireWriteLeases(ctx, ports.AcquireWriteLeasesRequest{
		JobLease: lease, AttemptID: runtimedomain.ExecutionAttemptID(attemptID),
		Targets: []ports.WorkspaceLeaseTarget{{
			RepositoryID: "repo-1", RepositoryWorkspaceID: workspace.RepositoryWorkspaceID(repositoryWorkspaceID), Generation: 1,
		}},
		TTL: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("AcquireWriteLeases: %v", err)
	}
	if len(grants) != 1 {
		t.Fatalf("grants = %+v, want exactly 1", grants)
	}

	tamperedGrant := grants[0]
	tamperedGrant.FenceToken = tamperedGrant.FenceToken + 1000

	_, err = runtime.FinalizeExecutionAttempt(ctx, uow, ids, clock.System{}, runtime.FinalizeExecutionAttemptRequest{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID, ExpectedVersion: 2,
		NextState: runtimedomain.ExecutionAttemptSucceeded, TerminationReason: runtimedomain.TerminationReasonCompleted,
		SelectedOutcome: "done", JobLease: lease, WriteLeases: []ports.WriteLeaseGrant{tamperedGrant},
	})
	if !errors.Is(err, ports.ErrWriteLeaseLost) {
		t.Fatalf("err = %v, want ErrWriteLeaseLost", err)
	}
	assertAttemptNodeRunUnchanged(t, ctx, uow, nodeRunID, attemptID)
}

func TestFinalizeExecutionAttempt_SQLite_ConcurrentFinalize_ExactlyOneWinner(t *testing.T) {
	ctx := context.Background()
	uow, store, ids, runID, nodeRunID, attemptID := sqliteExecutionFixture(t)

	_, lease := claimExecuteNodeJob(t, ctx, store, 30*time.Second)

	const attempts = 5
	var wg sync.WaitGroup
	successes := make([]bool, attempts)
	errs := make([]error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := runtime.FinalizeExecutionAttempt(ctx, uow, ids, clock.System{}, runtime.FinalizeExecutionAttemptRequest{
				RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID, ExpectedVersion: 2,
				NextState: runtimedomain.ExecutionAttemptSucceeded, TerminationReason: runtimedomain.TerminationReasonCompleted,
				SelectedOutcome: "done", JobLease: lease,
			})
			successes[i] = err == nil
			errs[i] = err
		}(i)
	}
	wg.Wait()

	winners := 0
	for i, ok := range successes {
		if ok {
			winners++
		} else if !errors.Is(errs[i], ports.ErrOptimisticConflict) && !errors.Is(errs[i], ports.ErrJobLeaseLost) {
			t.Fatalf("attempt %d unexpected error = %v", i, errs[i])
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d, want exactly 1 (errors: %v)", winners, errs)
	}

	attempt, err := getExecutionAttemptSQLite(ctx, uow, attemptID)
	if err != nil {
		t.Fatalf("get attempt: %v", err)
	}
	if attempt.State != runtimedomain.ExecutionAttemptSucceeded {
		t.Fatalf("attempt = %+v, want exactly one SUCCEEDED", attempt)
	}
}

func assertAttemptNodeRunUnchanged(t *testing.T, ctx context.Context, uow ports.UnitOfWork, nodeRunID, attemptID string) {
	t.Helper()
	attempt, err := getExecutionAttemptSQLite(ctx, uow, attemptID)
	if err != nil {
		t.Fatalf("get attempt: %v", err)
	}
	if attempt.State != runtimedomain.ExecutionAttemptRunning {
		t.Fatalf("attempt = %+v, want unchanged RUNNING", attempt)
	}
	var nodeRun runtimedomain.NodeRun
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		nodeRun, err = tx.Runtime().GetNodeRun(ctx, nodeRunID)
		return err
	}); err != nil {
		t.Fatalf("get node run: %v", err)
	}
	if nodeRun.State != runtimedomain.NodeRunRunning {
		t.Fatalf("node run = %+v, want unchanged RUNNING", nodeRun)
	}
}

func getExecutionAttemptSQLite(ctx context.Context, uow ports.UnitOfWork, attemptID string) (runtimedomain.ExecutionAttempt, error) {
	var attempt runtimedomain.ExecutionAttempt
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		attempt, err = tx.Runtime().GetExecutionAttempt(ctx, attemptID)
		return err
	})
	return attempt, err
}
