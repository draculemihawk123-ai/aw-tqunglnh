package runtime_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// TestScheduleExecutableNodeRun_SQLite_SchedulesAttemptAndJob is
// TestScheduleExecutableNodeRun_Agent_SchedulesAttemptAndJob's own real-
// backend proof: the same full AGENT resolution chain (real published
// AgentProfileVersion/PolicyVersions, real effective scope), but driven
// through the actual sqlite adapter (schedule_node_run.go's hand-written
// SQL: the PENDING->QUEUED CAS, the execution_attempts INSERT, and
// node_dispatch.go's effective_scope_json encode/decode round-trip) rather
// than the fake — the same "every mechanism gets at least one sqlite proof,
// not fake-only" discipline this package's other _sqlite_test.go files
// already established (advance_sqlite_test.go's own shared-state patch
// test cites the identical gap it closes).
func TestScheduleExecutableNodeRun_SQLite_SchedulesAttemptAndJob(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agentkit-schedule-node-run.db")
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")

	root := readyFixtureSQLite(t, uow, ids, "project-1", "repo-1")
	seedEffectiveScope(t, uow, root.WorkItemID, "repo-1")
	version := publishWorkflowVersionDocument(t, uow, "project-1", "wf-def-1", "wf-v-1",
		agentExecutableDocument("agent-profile-v1", fullyResolvablePolicyRefs(), nil))
	publishAgentProfileVersion(t, uow, "agent-profile-def", "agent-profile-v1", validAgentProfileDocument())
	publishPolicyVersion(t, uow, "attempt-policy-def", "attempt-policy-v1", attemptPolicyDocument(600))
	publishPolicyVersion(t, uow, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())

	startCmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	startResult, err := runtime.StartWorkflowRun(ctx, uow, ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	hop, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: startResult.NodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun (start->implement): %v", err)
	}
	if hop.NextNodeKey != "implement" || hop.NextScheduleJobID == "" {
		t.Fatalf("hop = %+v, want NextNodeKey=implement with a non-empty NextScheduleJobID", hop)
	}

	provider := fake.NewRuntimeExecutionConfigProvider()
	result, err := runtime.ScheduleExecutableNodeRun(ctx, uow, ids, provider, runtime.ScheduleExecutableNodeRunRequest{
		RunID: startResult.RunID, NodeRunID: hop.NextNodeRunID, CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("ScheduleExecutableNodeRun: %v", err)
	}
	if !result.Scheduled || result.AttemptID == "" || result.ExecuteJobID == "" {
		t.Fatalf("result = %+v, want Scheduled=true with a minted AttemptID and ExecuteJobID", result)
	}
	if !strings.HasPrefix(result.ExecutionProfileHash, "sha256:") {
		t.Fatalf("result.ExecutionProfileHash = %s, want a sha256: hash", result.ExecutionProfileHash)
	}

	state, nodeVersion, err := store.LoadNodeRunState(ctx, hop.NextNodeRunID)
	if err != nil {
		t.Fatalf("LoadNodeRunState: %v", err)
	}
	if state != runtimedomain.NodeRunQueued || nodeVersion != 2 {
		t.Fatalf("node run = (state=%s, version=%d), want (QUEUED, 2)", state, nodeVersion)
	}

	var nodeRun runtimedomain.NodeRun
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		nodeRun, err = tx.Runtime().GetNodeRun(ctx, hop.NextNodeRunID)
		return err
	}); err != nil {
		t.Fatalf("GetNodeRun: %v", err)
	}
	if nodeRun.ExecutionProfileHash != result.ExecutionProfileHash {
		t.Fatalf("node run ExecutionProfileHash = %s, want %s", nodeRun.ExecutionProfileHash, result.ExecutionProfileHash)
	}
	if len(nodeRun.EffectiveScope) != 1 || string(nodeRun.EffectiveScope[0].RepositoryID()) != "repo-1" {
		t.Fatalf("node run EffectiveScope = %+v, want exactly one entry for repo-1 (round-tripped through effective_scope_json)", nodeRun.EffectiveScope)
	}

	// Restart to prove the CAS/attempt/job all survived a real commit, not
	// just an in-memory *sql.Tx that never actually persisted.
	if err := store.Close(); err != nil {
		t.Fatalf("close before restart: %v", err)
	}
	restarted, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen after restart: %v", err)
	}
	defer restarted.Close()

	restartedState, restartedVersion, err := restarted.LoadNodeRunState(ctx, hop.NextNodeRunID)
	if err != nil {
		t.Fatalf("LoadNodeRunState after restart: %v", err)
	}
	if restartedState != runtimedomain.NodeRunQueued || restartedVersion != 2 {
		t.Fatalf("resumed node run = (state=%s, version=%d), want (QUEUED, 2)", restartedState, restartedVersion)
	}

	restartedUow := sqlite.NewUnitOfWork(restarted)
	replay, err := runtime.ScheduleExecutableNodeRun(ctx, restartedUow, ids, provider, runtime.ScheduleExecutableNodeRunRequest{
		RunID: startResult.RunID, NodeRunID: hop.NextNodeRunID, CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("replayed ScheduleExecutableNodeRun after restart: %v", err)
	}
	if replay.Scheduled {
		t.Fatalf("replayed result = %+v, want Scheduled=false (job survived restart, must not double-schedule)", replay)
	}
}
