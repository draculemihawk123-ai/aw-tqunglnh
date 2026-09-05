package runtime_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// TestAdvanceRun_SQLite_EndReached_RunVerifyingPersistsAcrossRestart is
// this task's own "restart" proof, mirroring every other V4 task's own
// SQLite restart test: reaching END and the Run's own resulting
// RUNNING->VERIFYING transition (reconcileRunTerminalityTx) both commit in
// the SAME transaction as the END NodeRun itself — a real close/reopen of
// the underlying sqlite.Store between that commit and the read-back
// proves it durably survived, not just an in-memory *fake.UnitOfWork run.
func TestAdvanceRun_SQLite_EndReached_RunVerifyingPersistsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agentkit-completion-end-restart.db")

	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")

	root := readyFixtureSQLite(t, uow, ids, "project-1", "repo-1")
	version := publishWorkflowVersionDocument(t, uow, "project-1", "wf-def-1", "wf-v-1", workflowDocumentV1())
	startCmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	startResult, err := runtime.StartWorkflowRun(ctx, uow, ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}

	hop, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: startResult.NodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun (start->end): %v", err)
	}
	if hop.NextNodeKey != "end" {
		t.Fatalf("hop = %+v, want NextNodeKey=end", hop)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("close before restart: %v", err)
	}
	reopened, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen after restart: %v", err)
	}
	defer reopened.Close()
	reopenedUow := sqlite.NewUnitOfWork(reopened)

	var run runtimedomain.WorkflowRun
	var endNodeRun runtimedomain.NodeRun
	if err := reopenedUow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		run, err = tx.Runtime().GetWorkflowRun(ctx, startResult.RunID)
		if err != nil {
			return err
		}
		endNodeRun, err = tx.Runtime().GetNodeRun(ctx, hop.NextNodeRunID)
		return err
	}); err != nil {
		t.Fatalf("read after restart: %v", err)
	}
	if run.State != runtimedomain.WorkflowRunVerifying {
		t.Fatalf("run state after restart = %s, want VERIFYING", run.State)
	}
	if endNodeRun.State != runtimedomain.NodeRunSucceeded {
		t.Fatalf("end node run after restart = %+v, want SUCCEEDED", endNodeRun)
	}

	var itemStatus string
	if err := reopenedUow.WithReadOnly(ctx, func(tx ports.Tx) error {
		item, err := tx.Work().GetWorkItem(ctx, root.WorkItemID)
		if err != nil {
			return err
		}
		itemStatus = string(item.Status)
		return nil
	}); err != nil {
		t.Fatalf("GetWorkItem after restart: %v", err)
	}
	if itemStatus == "DONE" {
		t.Fatalf("work item status after restart = %s, want unchanged (never DONE)", itemStatus)
	}
}
