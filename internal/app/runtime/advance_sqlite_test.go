package runtime_test

import (
	"context"
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
