package runtime_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// TestAdvanceRun_SQLite_PersistsAcrossRestart drives a real ROUTER-chain
// hop through the real sqlite backend, then closes and reopens the store,
// proving the routing decisions (NodeRun state/outcome/activation sequence)
// and the follow-up ADVANCE_RUN job all survive a process restart — the
// same restart discipline TestStartWorkflowRun_SQLite_PersistsAcrossRestart
// already established for V4-02.
func TestAdvanceRun_SQLite_PersistsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agentkit-advance-run.db")
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")

	root := readyFixtureSQLite(t, uow, ids, "project-1", "repo-1")
	version := publishWorkflowVersionDocument(t, uow, "project-1", "wf-def-1", "wf-v-1", routerChainDocument())

	cmd := ports.Command{
		ID: "cmd-start-1", IdempotencyKey: "idem-start-1", Actor: "actor-1", CorrelationID: "corr-1",
		Scope: ports.ProjectScope("project-1"), RequestedAt: time.Now().UTC(),
		Type: "StartWorkflowRun", RequestHash: "hash-start-1",
	}
	startResult, err := runtime.StartWorkflowRun(ctx, uow, ids, cmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}

	hop1, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: startResult.NodeRunID})
	if err != nil {
		t.Fatalf("hop 1 AdvanceRun: %v", err)
	}
	if hop1.NextNodeKey != "router1" || !hop1.NextAutoAdvanced {
		t.Fatalf("hop 1 = %+v, want router1 auto-advanced", hop1)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("close before restart: %v", err)
	}

	restarted, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen after restart: %v", err)
	}
	defer restarted.Close()

	startState, startVersion, err := restarted.LoadNodeRunState(ctx, startResult.NodeRunID)
	if err != nil {
		t.Fatalf("LoadNodeRunState(start) after restart: %v", err)
	}
	if startState != runtimedomain.NodeRunSucceeded || startVersion != 2 {
		t.Fatalf("resumed start node run = (state=%s, version=%d), want (SUCCEEDED, 2)", startState, startVersion)
	}

	router1State, router1Version, err := restarted.LoadNodeRunState(ctx, hop1.NextNodeRunID)
	if err != nil {
		t.Fatalf("LoadNodeRunState(router1) after restart: %v", err)
	}
	if router1State != runtimedomain.NodeRunRunning || router1Version != 1 {
		t.Fatalf("resumed router1 node run = (state=%s, version=%d), want (RUNNING, 1)", router1State, router1Version)
	}

	// The follow-up job for router1 survived restart and is claimable.
	restartedUow := sqlite.NewUnitOfWork(restarted)
	hop2, err := runtime.AdvanceRun(ctx, restartedUow, ids, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: hop1.NextNodeRunID})
	if err != nil {
		t.Fatalf("hop 2 AdvanceRun after restart: %v", err)
	}
	if hop2.NextNodeKey != "router2" || !hop2.NextAutoAdvanced {
		t.Fatalf("hop 2 = %+v, want router2 auto-advanced", hop2)
	}
}

// TestAdvanceRun_SQLite_SharedStatePatch_PersistsAndHashesCorrectly is the
// shared-state patch mechanism's own real-backend proof (correction found
// during review: the mechanism's own tests all ran against the fake
// UnitOfWork only, never sqlite).
func TestAdvanceRun_SQLite_SharedStatePatch_PersistsAndHashesCorrectly(t *testing.T) {
	ctx := context.Background()
	store := openSQLiteStore(t, "agentkit-advance-shared-state.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")

	root := readyFixtureSQLite(t, uow, ids, "project-1", "repo-1")
	version := publishWorkflowVersionDocument(t, uow, "project-1", "wf-def-1", "wf-v-1", sharedStateDocument())

	cmd := ports.Command{
		ID: "cmd-start-1", IdempotencyKey: "idem-start-1", Actor: "actor-1", CorrelationID: "corr-1",
		Scope: ports.ProjectScope("project-1"), RequestedAt: time.Now().UTC(),
		Type: "StartWorkflowRun", RequestHash: "hash-start-1",
	}
	startResult, err := runtime.StartWorkflowRun(ctx, uow, ids, cmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}

	result, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{
		RunID: startResult.RunID, NodeRunID: startResult.NodeRunID,
		SharedStatePatch: map[string]json.RawMessage{"counter": json.RawMessage("1")},
	})
	if err != nil {
		t.Fatalf("AdvanceRun: %v", err)
	}

	run, err := store.LoadWorkflowRun(ctx, runtimedomain.WorkflowRunID(startResult.RunID))
	if err != nil {
		t.Fatalf("LoadWorkflowRun: %v", err)
	}
	var state map[string]json.RawMessage
	if err := json.Unmarshal(run.SharedState, &state); err != nil {
		t.Fatalf("unmarshal shared state: %v", err)
	}
	if string(state["counter"]) != "1" {
		t.Fatalf("shared state counter = %s, want 1", state["counter"])
	}
	if run.Version != 2 {
		t.Fatalf("run version = %d, want 2 (one shared-state write)", run.Version)
	}

	nodeState, nodeVersion, err := store.LoadNodeRunState(ctx, result.NextNodeRunID)
	if err != nil {
		t.Fatalf("LoadNodeRunState(next): %v", err)
	}
	if nodeState != runtimedomain.NodeRunRunning || nodeVersion != 1 {
		t.Fatalf("next node run = (state=%s, version=%d), want (RUNNING, 1)", nodeState, nodeVersion)
	}
}

// TestAdvanceRun_SQLite_SharedStatePatch_RollbackOnMidTransactionFailure
// forces a real failure at the LAST write AdvanceRun's own transaction body
// makes — the follow-up ADVANCE_RUN job's own idempotency_key uniquely
// colliding with a job pre-seeded outside the transaction — after the
// shared-state write, NodeRun transition, downstream NodeRun activation and
// NODE_ROUTED event have all already been written to the live *sql.Tx, and
// requires every one of those to roll back, the same
// "rollback/duplicate/concurrent" verify discipline
// TestStartWorkflowRun_SQLite_RollbackOnMidTransactionFailure_NoOrphanRows
// (V4-02) already established for StartWorkflowRun.
func TestAdvanceRun_SQLite_SharedStatePatch_RollbackOnMidTransactionFailure(t *testing.T) {
	ctx := context.Background()
	store := openSQLiteStore(t, "agentkit-advance-shared-state-rollback.db")
	uow := sqlite.NewUnitOfWork(store)
	setupIDs := idsource.NewSequential("setup")

	root := readyFixtureSQLite(t, uow, setupIDs, "project-1", "repo-1")
	version := publishWorkflowVersionDocument(t, uow, "project-1", "wf-def-1", "wf-v-1", sharedStateDocument())

	cmd := ports.Command{
		ID: "cmd-start-1", IdempotencyKey: "idem-start-1", Actor: "actor-1", CorrelationID: "corr-1",
		Scope: ports.ProjectScope("project-1"), RequestedAt: time.Now().UTC(),
		Type: "StartWorkflowRun", RequestHash: "hash-start-1",
	}
	// StartWorkflowRun mints RunID/ManifestID/NodeRunID/JobID as calls 1-4 of
	// its own idsource.Source; AdvanceRun then mints NextNodeRunID/JobID as
	// calls 5-6 of the SAME source — a dedicated Sequential source starting
	// fresh here makes the eventual follow-up JobID's idempotency key
	// ("advance-run-5") deterministic.
	runIDs := idsource.NewSequential("run")
	startResult, err := runtime.StartWorkflowRun(ctx, uow, runIDs, cmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}

	conflictingIdempotencyKey := "advance-run-5"
	if _, err := store.EnqueueJob(ctx, ports.EnqueueJobRequest{
		ID: "pre-existing-job", ProjectID: "project-1", Kind: "PRE_EXISTING",
		AggregateType: "Probe", AggregateID: "probe-1", MaxClaims: 1,
		IdempotencyKey: conflictingIdempotencyKey,
	}); err != nil {
		t.Fatalf("seed conflicting job: %v", err)
	}

	_, err = runtime.AdvanceRun(ctx, uow, runIDs, runtime.AdvanceRunRequest{
		RunID: startResult.RunID, NodeRunID: startResult.NodeRunID,
		SharedStatePatch: map[string]json.RawMessage{"counter": json.RawMessage("1")},
	})
	if err == nil {
		t.Fatal("AdvanceRun succeeded, want a failure from the seeded idempotency-key collision")
	}

	run, err := store.LoadWorkflowRun(ctx, runtimedomain.WorkflowRunID(startResult.RunID))
	if err != nil {
		t.Fatalf("LoadWorkflowRun: %v", err)
	}
	if run.Version != 1 {
		t.Fatalf("run version = %d, want unchanged 1 (rollback must undo the shared-state write too)", run.Version)
	}
	var state map[string]json.RawMessage
	if err := json.Unmarshal(run.SharedState, &state); err != nil {
		t.Fatalf("unmarshal shared state: %v", err)
	}
	if _, wrote := state["counter"]; wrote {
		t.Fatalf("shared state = %s, want no counter field (rolled-back write must not persist)", run.SharedState)
	}

	startState, startVersion, err := store.LoadNodeRunState(ctx, startResult.NodeRunID)
	if err != nil {
		t.Fatalf("LoadNodeRunState(start): %v", err)
	}
	if startState != runtimedomain.NodeRunRunning || startVersion != 1 {
		t.Fatalf("start node run = (state=%s, version=%d), want unchanged (RUNNING, 1)", startState, startVersion)
	}

	nodeCount, err := store.CountNodeRuns(ctx)
	if err != nil {
		t.Fatalf("CountNodeRuns: %v", err)
	}
	if nodeCount != 1 {
		t.Fatalf("node run count = %d, want 1 (only start — the downstream activation must not have survived rollback)", nodeCount)
	}

	eventCount, err := store.CountDomainEventsByType(ctx, runtime.NodeRoutedEventType)
	if err != nil {
		t.Fatalf("CountDomainEventsByType: %v", err)
	}
	if eventCount != 0 {
		t.Fatalf("NODE_ROUTED event count = %d, want 0 (rollback must remove it)", eventCount)
	}
}
