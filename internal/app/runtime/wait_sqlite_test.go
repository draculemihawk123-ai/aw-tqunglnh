package runtime_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// sqliteWaitFixture is waitFixture's own real-backend twin: publishes
// waitDurationDocument(durationSeconds) against a real sqlite.Store, starts
// the run and advances it one hop (start -> pause), so the WAIT_TIMER job
// this task's own dispatch enqueues genuinely exists in durable_jobs.
func sqliteWaitFixture(t *testing.T, store *sqlite.Store, u ports.UnitOfWork, seq idsource.Source, durationSeconds uint32) (runID string, hop runtime.AdvanceRunResult) {
	t.Helper()
	ctx := context.Background()
	root := readyFixtureSQLite(t, u, seq, "project-1", "repo-1")
	version := publishWorkflowVersionDocument(t, u, "project-1", "wf-def-1", "wf-v-1", waitDurationDocument(durationSeconds))
	startCmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	startResult, err := runtime.StartWorkflowRun(ctx, u, seq, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	result, err := runtime.AdvanceRun(ctx, u, seq, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: startResult.NodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun (start->pause): %v", err)
	}
	if result.NextWaitTimerJobID == "" {
		t.Fatalf("hop = %+v, want a minted NextWaitTimerJobID", result)
	}
	return startResult.RunID, result
}

// claimWaitTimerJob claims jobs off store's own queue until it gets the
// WAIT_TIMER one — mirroring claimExecuteNodeJob's own "skip earlier
// ADVANCE_RUN jobs still sitting AVAILABLE" discipline.
func claimWaitTimerJob(t *testing.T, ctx context.Context, store *sqlite.Store, ttl time.Duration) (ports.DurableJob, ports.JobLease) {
	t.Helper()
	for i := 0; i < 5; i++ {
		job, lease, err := store.ClaimJob(ctx, "worker-1", ttl)
		if err != nil {
			t.Fatalf("ClaimJob: %v", err)
		}
		if job.Kind == runtime.WaitTimerJobKind {
			return job, lease
		}
	}
	t.Fatal("did not find a WAIT_TIMER job to claim within 5 attempts")
	return ports.DurableJob{}, ports.JobLease{}
}

// TestWaitTimeoutHandler_SQLite_PersistsAcrossRestart proves a DURATION
// wait's own registration and its WAIT_TIMER job survive a genuine process
// restart (close/reopen the same database file) and still fire correctly
// afterward — this task's own explicit "restart timer" Verify requirement.
func TestWaitTimeoutHandler_SQLite_PersistsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agentkit-wait-restart.db")

	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	u := sqlite.NewUnitOfWork(store)
	seq := idsource.NewSequential("id")

	runID, hop := sqliteWaitFixture(t, store, u, seq, 1) // due in 1 second

	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	time.Sleep(1200 * time.Millisecond) // past the 1s due time

	reopened, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen Open: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	reopenedUow := sqlite.NewUnitOfWork(reopened)

	job, lease := claimWaitTimerJob(t, ctx, reopened, 30*time.Second)
	var payload runtime.WaitTimerJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		t.Fatalf("decode timer job payload: %v", err)
	}
	if payload.WaitRegistrationID != hop.NextWaitRegistrationID || payload.RunID != runID {
		t.Fatalf("timer job payload = %+v, want registration %s run %s", payload, hop.NextWaitRegistrationID, runID)
	}

	handler := runtime.NewWaitTimeoutHandler(reopenedUow, seq)
	leaseUntil := lease.LeaseUntil
	if err := handler.Handle(ctx, ports.DurableJob{
		ID: job.ID, AggregateType: job.AggregateType, AggregateID: job.AggregateID,
		Payload: job.Payload, LeaseOwner: lease.Owner, LeaseToken: lease.Token, LeaseUntil: &leaseUntil,
	}); err != nil {
		t.Fatalf("Handle after restart: %v", err)
	}

	var registration runtimedomain.WaitRegistration
	var pauseNodeRun runtimedomain.NodeRun
	if err := reopenedUow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		registration, err = tx.Wait().GetWaitRegistration(ctx, hop.NextWaitRegistrationID)
		if err != nil {
			return err
		}
		pauseNodeRun, err = tx.Runtime().GetNodeRun(ctx, hop.NextNodeRunID)
		return err
	}); err != nil {
		t.Fatalf("read after restart: %v", err)
	}
	if registration.State != runtimedomain.WaitRegistrationElapsed {
		t.Fatalf("registration after restart = %+v, want ELAPSED", registration)
	}
	if pauseNodeRun.State != runtimedomain.NodeRunSucceeded || pauseNodeRun.SelectedOutcome != "resumed" {
		t.Fatalf("pause node run after restart = %+v, want SUCCEEDED/resumed", pauseNodeRun)
	}
}

// TestSignalWait_SQLite_ConcurrentSameSignalKey_ExactlyOneWinner races real
// concurrent SignalWait calls (each its own goroutine, its own command
// invocation with a distinct IdempotencyKey) reporting the SAME real
// external event (same SignalKey, same payload) against one real
// sqlite.Store — this task's own explicit "hai signal đồng thời cùng
// identity" Verify requirement. Exactly one wait_signals row may ever
// exist for (WaitRegistrationID, SignalKey), and exactly one caller may
// ever observe Won=true.
func TestSignalWait_SQLite_ConcurrentSameSignalKey_ExactlyOneWinner(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agentkit-wait-signal-race.db")
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	u := sqlite.NewUnitOfWork(store)
	seq := idsource.NewSequential("id")

	root := readyFixtureSQLite(t, u, seq, "project-1", "repo-1")
	version := publishWorkflowVersionDocument(t, u, "project-1", "wf-def-1", "wf-v-1", waitSignalDocument(600))
	startCmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	startResult, err := runtime.StartWorkflowRun(ctx, u, seq, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	hop, err := runtime.AdvanceRun(ctx, u, seq, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: startResult.NodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun (start->pause): %v", err)
	}

	const attempts = 5
	var wg sync.WaitGroup
	results := make([]runtime.SignalWaitResult, attempts)
	errs := make([]error, attempts)
	payload := json.RawMessage(`{"status":"green"}`)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cmd := testCommand(fmt.Sprintf("idem-signal-%d", i), fmt.Sprintf("hash-%d", i), ports.ProjectScope("project-1"), "SignalWait")
			results[i], errs[i] = runtime.SignalWait(ctx, u, idsource.NewSequential(fmt.Sprintf("id-race-%d", i)), cmd, runtime.SignalWaitRequest{
				RunID: startResult.RunID, WaitRegistrationID: hop.NextWaitRegistrationID, SignalKey: "delivery-shared", Payload: payload,
			})
		}(i)
	}
	wg.Wait()

	winners := 0
	for i, err := range errs {
		if err != nil {
			t.Fatalf("attempt %d unexpected error = %v", i, err)
		}
		if results[i].Won {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d, want exactly 1 (results: %+v)", winners, results)
	}

	registration, err := getWaitRegistrationSQLite(ctx, u, hop.NextWaitRegistrationID)
	if err != nil {
		t.Fatalf("get registration: %v", err)
	}
	if registration.State != runtimedomain.WaitRegistrationConsumed {
		t.Fatalf("registration = %+v, want exactly one CONSUMED", registration)
	}

	// Exactly one winner already proves routing happened at most once
	// (only the winner's own transaction ever calls advanceRunTx for this
	// hop) — confirm the pause NodeRun itself landed in the one consistent
	// terminal state a single route would produce.
	pauseNodeRun, err := getNodeRunSQLite(ctx, u, hop.NextNodeRunID)
	if err != nil {
		t.Fatalf("get pause node run: %v", err)
	}
	if pauseNodeRun.State != runtimedomain.NodeRunSucceeded || pauseNodeRun.SelectedOutcome != "resumed" {
		t.Fatalf("pause node run = %+v, want SUCCEEDED/resumed", pauseNodeRun)
	}
}

func getWaitRegistrationSQLite(ctx context.Context, uow ports.UnitOfWork, id string) (runtimedomain.WaitRegistration, error) {
	var registration runtimedomain.WaitRegistration
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		registration, err = tx.Wait().GetWaitRegistration(ctx, id)
		return err
	})
	return registration, err
}

func getNodeRunSQLite(ctx context.Context, uow ports.UnitOfWork, id string) (runtimedomain.NodeRun, error) {
	var nodeRun runtimedomain.NodeRun
	err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		nodeRun, err = tx.Runtime().GetNodeRun(ctx, id)
		return err
	})
	return nodeRun, err
}
