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
)

// TestFinalizeExecutionAttempt_SQLite_Blocked_OriginSurvivesRestart is this
// task's own "restart" proof, mirroring every other V4 task's own SQLite
// restart test: the ScopeExpansionOrigin row (attempt_scope_expansion_origins,
// migration 0022) commits in the SAME transaction as the Attempt/NodeRun's
// own CAS to BLOCKED — a real close/reopen of the underlying sqlite.Store
// between that commit and the read-back proves the origin (and its RESERVED
// RequestID) durably survived, not just an in-memory *fake.UnitOfWork run.
//
// This test inlines sqliteExecutionFixture's own setup (this file's own
// sibling fixture, in finalize_execution_attempt_sqlite_test.go) rather than
// calling it directly, purely so it can keep the dbPath around for the
// close/reopen below — that fixture's own signature has no reason to expose
// a path none of its other callers ever need.
func TestFinalizeExecutionAttempt_SQLite_Blocked_OriginSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agentkit-scope-expansion-restart.db")
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
	runID, nodeRunID := startResult.RunID, hop.NextNodeRunID

	scheduled, err := runtime.ScheduleExecutableNodeRun(ctx, uow, ids, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: runID, NodeRunID: nodeRunID, CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("ScheduleExecutableNodeRun: %v", err)
	}
	attemptID := scheduled.AttemptID

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
		t.Fatalf("seed RUNNING attempt/node run: %v", err)
	}

	_, lease := claimExecuteNodeJob(t, ctx, store, 30*time.Second)

	proposal := &runtimedomain.ScopeExpansionProposal{
		RequestedGrants: []runtimedomain.ScopeGrantProposal{
			{RepositoryID: "repo-2", Access: "WRITE", PathScopes: []string{"services/repo-2"}, Reason: "need repo-2 too"},
		},
		Reason: "need repo-2 too",
	}
	result, err := runtime.FinalizeExecutionAttempt(ctx, uow, ids, clock.System{}, nil, runtime.FinalizeExecutionAttemptRequest{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID, ExpectedVersion: 2,
		NextState: runtimedomain.ExecutionAttemptBlocked, TerminationReason: runtimedomain.TerminationReasonScopeExpansionRequired,
		RequestedScopeExpansion: proposal, JobLease: lease,
	})
	if err != nil {
		t.Fatalf("FinalizeExecutionAttempt: %v", err)
	}
	if !result.ScopeExpansionRequested || result.ScopeExpansionRequestID == "" || result.ScopeExpansionJobID == "" {
		t.Fatalf("result = %+v, want ScopeExpansionRequested with a non-empty RequestID/JobID", result)
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

	var byAttempt, byRequest runtimedomain.ScopeExpansionOrigin
	if err := reopenedUow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		byAttempt, err = tx.Runtime().GetScopeExpansionOriginByAttemptID(ctx, attemptID)
		if err != nil {
			return err
		}
		byRequest, err = tx.Runtime().GetScopeExpansionOriginByRequestID(ctx, result.ScopeExpansionRequestID)
		return err
	}); err != nil {
		t.Fatalf("read origin after restart: %v", err)
	}

	if byAttempt.RequestID != result.ScopeExpansionRequestID {
		t.Fatalf("origin (by attempt) after restart = %+v, want RequestID=%s", byAttempt, result.ScopeExpansionRequestID)
	}
	if byRequest.AttemptID != runtimedomain.ScopeExpansionOriginID(attemptID) {
		t.Fatalf("origin (by request) after restart = %+v, want AttemptID=%s", byRequest, attemptID)
	}
	if byAttempt.ReconcileStatus != runtimedomain.ScopeExpansionReconcilePending || byAttempt.PollGeneration != 0 {
		t.Fatalf("origin after restart = %+v, want PENDING/pollGeneration=0", byAttempt)
	}
	if len(byAttempt.Proposal.RequestedGrants) != 1 || byAttempt.Proposal.RequestedGrants[0].RepositoryID != "repo-2" {
		t.Fatalf("origin.Proposal after restart = %+v, want the repo-2 grant preserved", byAttempt.Proposal)
	}

	var attempt runtimedomain.ExecutionAttempt
	var nodeRun runtimedomain.NodeRun
	if err := reopenedUow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		attempt, err = tx.Runtime().GetExecutionAttempt(ctx, attemptID)
		if err != nil {
			return err
		}
		nodeRun, err = tx.Runtime().GetNodeRun(ctx, nodeRunID)
		return err
	}); err != nil {
		t.Fatalf("read attempt/node run after restart: %v", err)
	}
	if attempt.State != runtimedomain.ExecutionAttemptBlocked {
		t.Fatalf("attempt after restart = %+v, want BLOCKED", attempt)
	}
	if nodeRun.State != runtimedomain.NodeRunBlocked {
		t.Fatalf("node run after restart = %+v, want BLOCKED", nodeRun)
	}
}
