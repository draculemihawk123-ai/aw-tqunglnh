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

// TestAdvanceRun_SQLite_Fork_PersistsAcrossRestart is this task's own
// "partial crash" proof (Verify line): the entire FORK fan-out —
// dispatchForkBranches' own atomically-created BranchTokens, branch
// NodeRuns, and the FORK's own NodeRun and NODE_FORKED event — all commit
// in ONE WithSerializedWrite transaction (advanceRunTx), so a process crash
// can only ever observe either none of it (never durably observed) or all
// of it, never a half-fanned-out FORK. This test proves the "all of it"
// side survives a real close/reopen of the underlying sqlite.Store, mirroring
// TestAdvanceRun_SQLite_CycleExhaustion_PersistsAcrossRestart's own
// structure (V4-07).
func TestAdvanceRun_SQLite_Fork_PersistsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agentkit-fork-restart.db")

	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	u := sqlite.NewUnitOfWork(store)
	seq := idsource.NewSequential("id")

	root := readyFixtureSQLite(t, u, seq, "project-1", "repo-1")
	seedEffectiveScope(t, u, root.WorkItemID, "repo-1")
	version := publishWorkflowVersionDocument(t, u, "project-1", "wf-def-1", "wf-v-1", forkExecutableDocument(t))
	startCmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	startResult, err := runtime.StartWorkflowRun(ctx, u, seq, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}

	hop, err := runtime.AdvanceRun(ctx, u, seq, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: startResult.NodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun (start->fork): %v", err)
	}
	if hop.NextNodeKey != "fork" || len(hop.ForkedBranches) != 2 {
		t.Fatalf("hop = %+v, want NextNodeKey=fork with 2 ForkedBranches", hop)
	}
	implementBranch := findForkedBranch(hop.ForkedBranches, "to_implement")
	shortcutBranch := findForkedBranch(hop.ForkedBranches, "shortcut")
	if implementBranch == nil || shortcutBranch == nil {
		t.Fatalf("ForkedBranches = %+v, want to_implement and shortcut", hop.ForkedBranches)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen Open: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	reopenedUow := sqlite.NewUnitOfWork(reopened)

	var forkNodeRun, implementNodeRun runtimedomain.NodeRun
	var implementToken, shortcutToken runtimedomain.BranchToken
	var tokens []runtimedomain.BranchToken
	if err := reopenedUow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		forkNodeRun, err = tx.Runtime().GetNodeRun(ctx, hop.NextNodeRunID)
		if err != nil {
			return err
		}
		implementNodeRun, err = tx.Runtime().GetNodeRun(ctx, implementBranch.NodeRunID)
		if err != nil {
			return err
		}
		implementToken, err = tx.Runtime().GetBranchTokenByID(ctx, implementBranch.BranchTokenID)
		if err != nil {
			return err
		}
		shortcutToken, err = tx.Runtime().GetBranchTokenByID(ctx, shortcutBranch.BranchTokenID)
		if err != nil {
			return err
		}
		tokens, err = tx.Runtime().ListBranchTokensForRun(ctx, startResult.RunID)
		return err
	}); err != nil {
		t.Fatalf("read after restart: %v", err)
	}

	if forkNodeRun.State != runtimedomain.NodeRunSucceeded || forkNodeRun.SelectedOutcome != "" {
		t.Fatalf("fork node run after restart = %+v, want SUCCEEDED with a blank SelectedOutcome", forkNodeRun)
	}
	if implementNodeRun.BranchTokenID == nil || string(*implementNodeRun.BranchTokenID) != implementBranch.BranchTokenID {
		t.Fatalf("implement node run after restart = %+v, want BranchTokenID=%s", implementNodeRun, implementBranch.BranchTokenID)
	}
	if implementToken.State != runtimedomain.BranchTokenActive || implementToken.CurrentNodeKey != "implement" {
		t.Fatalf("implement token after restart = %+v, want ACTIVE at implement", implementToken)
	}
	if shortcutToken.State != runtimedomain.BranchTokenSucceeded || shortcutToken.CurrentNodeKey != "join" {
		t.Fatalf("shortcut token after restart = %+v, want SUCCEEDED at join", shortcutToken)
	}
	if len(tokens) != 2 {
		t.Fatalf("ListBranchTokensForRun after restart = %+v, want exactly 2 tokens", tokens)
	}
}
