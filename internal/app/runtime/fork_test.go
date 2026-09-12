package runtime_test

import (
	"context"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// V4-10's own "fan out atomically, dispatch idempotently, terminalize a
// dead branch" tests (docs/design/06-v4-runtime-engine.md). forkExecutableDocument
// is start -> fork(FORK, two branches) -> {to_implement: implement(AGENT,
// real resolvable ProfileRef/PolicyRefs, mirroring schedule_test.go's own
// agentExecutableDocument), shortcut: straight to join} -> join(JOIN ALL)
// -> end. "shortcut" exercises the zero-hop "a branch's own first edge IS
// its outer join" case dispatchForkBranches' own doc comment describes;
// "to_implement" exercises the ordinary executable-node dispatch case, and
// is the one branch this file's own real-attempt tests schedule/execute/
// fail.
func forkExecutableDocument(t *testing.T) workflow.WorkflowDocument {
	t.Helper()
	buildID := sharedTestAdapterBuild(t).ID()
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "fork", Type: workflow.NodeFork, Outcomes: []string{"to_implement", "shortcut"}},
			{Key: "implement", Type: workflow.NodeAgent, Outcomes: []string{"done"}, Agent: &workflow.AgentNodeConfig{
				ProfileRef:     definition.DependencyPin{Kind: definition.KindAgentProfile, DefinitionID: "agent-profile-def", VersionID: "agent-profile-v1"},
				PolicyRefs:     fullyResolvablePolicyRefs(),
				AdapterBuildID: &buildID,
			}},
			{Key: "join", Type: workflow.NodeJoin, Outcomes: []string{"joined"}, Join: &workflow.JoinNodeConfig{Mode: workflow.JoinModeAll}},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-to-fork", From: "start", Outcome: "next", To: "fork"},
			{Key: "fork-to-implement", From: "fork", Outcome: "to_implement", To: "implement"},
			{Key: "fork-to-join-shortcut", From: "fork", Outcome: "shortcut", To: "join"},
			{Key: "implement-to-join", From: "implement", Outcome: "done", To: "join"},
			{Key: "join-to-end", From: "join", Outcome: "joined", To: "end"},
		},
	}
}

// forkScheduleFixture starts a run over document (which must be a FORK
// document shaped like forkExecutableDocument's own start->fork prefix),
// seeds an EffectiveScope the way scheduleFixture (schedule_test.go) does
// (needed if a caller goes on to ScheduleExecutableNodeRun an AGENT
// branch), and advances start->fork once.
func forkScheduleFixture(t *testing.T, document workflow.WorkflowDocument) (uow *fake.UnitOfWork, ids idsource.Source, runID, startNodeRunID string, forkHop runtime.AdvanceRunResult) {
	t.Helper()
	ctx := context.Background()
	u := fake.New()
	seq := idsource.NewSequential("id")
	root := readyFixture(t, u, seq, "project-1", "repo-1")
	seedEffectiveScope(t, u, root.WorkItemID, "repo-1")
	version := publishWorkflowVersionDocument(t, u, "project-1", "wf-def-1", "wf-v-1", document)

	cmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	startResult, err := runtime.StartWorkflowRun(ctx, u, seq, cmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	hop, err := runtime.AdvanceRun(ctx, u, seq, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: startResult.NodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun (start->fork): %v", err)
	}
	return u, seq, startResult.RunID, startResult.NodeRunID, hop
}

func findForkedBranch(branches []runtime.ForkedBranch, branchKey string) *runtime.ForkedBranch {
	for i := range branches {
		if branches[i].BranchKey == branchKey {
			return &branches[i]
		}
	}
	return nil
}

// TestAdvanceRun_Fork_FansOutToEveryBranchAtomically proves HE-14-M09's own
// "create tokens atomically": one AdvanceRun call on the FORK's own
// upstream node creates the FORK's own NodeRun (SUCCEEDED, blank
// SelectedOutcome — a FORK takes every declared outcome at once, never
// just one), a fresh ACTIVE BranchToken plus a real, correctly BranchTokenID-
// tagged NodeRun for the "to_implement" branch (with its own
// ScheduleNodeRunJobKind job enqueued), a fresh BranchToken already
// SUCCEEDED at "join" for the "shortcut" branch (whose own first edge IS
// its outer join — no NodeRun created for it at all), and one NODE_FORKED
// domain event recording the whole fan-out.
func TestAdvanceRun_Fork_FansOutToEveryBranchAtomically(t *testing.T) {
	ctx := context.Background()
	uow, _, _, _, hop := forkScheduleFixture(t, forkExecutableDocument(t))

	if hop.NextNodeKey != "fork" || hop.NextNodeRunID == "" {
		t.Fatalf("hop = %+v, want NextNodeKey=fork with a minted NextNodeRunID", hop)
	}
	forkNodeRun, err := uow.Snapshot.Runtime().GetNodeRun(ctx, hop.NextNodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun(fork): %v", err)
	}
	if forkNodeRun.State != runtimedomain.NodeRunSucceeded || forkNodeRun.SelectedOutcome != "" {
		t.Fatalf("fork node run = %+v, want SUCCEEDED with a blank SelectedOutcome", forkNodeRun)
	}
	if len(hop.ForkedBranches) != 2 {
		t.Fatalf("ForkedBranches = %+v, want exactly 2", hop.ForkedBranches)
	}

	implementBranch := findForkedBranch(hop.ForkedBranches, "to_implement")
	shortcutBranch := findForkedBranch(hop.ForkedBranches, "shortcut")
	if implementBranch == nil || shortcutBranch == nil {
		t.Fatalf("ForkedBranches = %+v, want branch keys to_implement and shortcut", hop.ForkedBranches)
	}

	if implementBranch.ReachedJoin || implementBranch.NodeRunID == "" || implementBranch.ScheduleJobID == "" {
		t.Fatalf("to_implement branch = %+v, want a scheduled NodeRun, not ReachedJoin", implementBranch)
	}
	implementNodeRun, err := uow.Snapshot.Runtime().GetNodeRun(ctx, implementBranch.NodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun(implement): %v", err)
	}
	if implementNodeRun.BranchTokenID == nil || string(*implementNodeRun.BranchTokenID) != implementBranch.BranchTokenID {
		t.Fatalf("implement node run = %+v, want BranchTokenID=%s", implementNodeRun, implementBranch.BranchTokenID)
	}
	if implementNodeRun.State != runtimedomain.NodeRunPending {
		t.Fatalf("implement node run = %+v, want PENDING (ScheduleExecutableNodeRun owns the PENDING->RUNNING transition, V4-04)", implementNodeRun)
	}
	implementToken, err := uow.Snapshot.Runtime().GetBranchTokenByID(ctx, implementBranch.BranchTokenID)
	if err != nil {
		t.Fatalf("GetBranchTokenByID(implement): %v", err)
	}
	if implementToken.State != runtimedomain.BranchTokenActive || implementToken.CurrentNodeKey != "implement" || string(implementToken.ForkNodeRunID) != hop.NextNodeRunID {
		t.Fatalf("implement token = %+v, want ACTIVE at implement owned by fork node run %s", implementToken, hop.NextNodeRunID)
	}

	if !shortcutBranch.ReachedJoin || shortcutBranch.NodeRunID != "" {
		t.Fatalf("shortcut branch = %+v, want ReachedJoin=true with no NodeRun", shortcutBranch)
	}
	shortcutToken, err := uow.Snapshot.Runtime().GetBranchTokenByID(ctx, shortcutBranch.BranchTokenID)
	if err != nil {
		t.Fatalf("GetBranchTokenByID(shortcut): %v", err)
	}
	if shortcutToken.State != runtimedomain.BranchTokenSucceeded || shortcutToken.CurrentNodeKey != "join" {
		t.Fatalf("shortcut token = %+v, want SUCCEEDED at join", shortcutToken)
	}

	tokens, err := uow.Snapshot.Runtime().ListBranchTokensForRun(ctx, string(forkNodeRun.RunID))
	if err != nil {
		t.Fatalf("ListBranchTokensForRun: %v", err)
	}
	if len(tokens) != 2 {
		t.Fatalf("ListBranchTokensForRun = %+v, want exactly 2 tokens", tokens)
	}

	events := uow.Snapshot.Events().(*fake.EventsRepository).Items()
	found := false
	for _, e := range events {
		if e.EventType == runtime.NodeForkedEventType && e.AggregateID == hop.NextNodeRunID {
			found = true
		}
	}
	if !found {
		t.Fatalf("no %s event found for fork node run %s among %+v", runtime.NodeForkedEventType, hop.NextNodeRunID, events)
	}

	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	scheduleJobFound := false
	for _, j := range jobs {
		if j.Kind == runtime.ScheduleNodeRunJobKind && j.AggregateID == implementBranch.NodeRunID {
			scheduleJobFound = true
		}
	}
	if !scheduleJobFound {
		t.Fatalf("no %s job found for implement branch %s among %+v", runtime.ScheduleNodeRunJobKind, implementBranch.NodeRunID, jobs)
	}
}

// TestAdvanceRun_Fork_DuplicateDispatch_IsIdempotent proves a redelivered
// job (a second AdvanceRun call for the exact same upstream NodeRunID —
// this task's own Verify line's "duplicate dispatch") never re-runs the
// fan-out: advanceRunTx's own idempotent-replay guard (this file's own
// package doc comment) already rejects it before dispatchForkBranches is
// ever reached, since the upstream NodeRun is no longer RUNNING/WAITING
// after the first successful hop — no second BranchToken/NodeRun/job/event
// is created.
func TestAdvanceRun_Fork_DuplicateDispatch_IsIdempotent(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, startNodeRunID, hop := forkScheduleFixture(t, forkExecutableDocument(t))

	tokensBefore, err := uow.Snapshot.Runtime().ListBranchTokensForRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListBranchTokensForRun (before): %v", err)
	}
	jobsBefore := len(uow.Snapshot.Jobs().(*fake.JobsRepository).Items())
	eventsBefore := len(uow.Snapshot.Events().(*fake.EventsRepository).Items())

	replay, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: runID, NodeRunID: startNodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun (duplicate delivery): %v", err)
	}
	if replay.Advanced {
		t.Fatalf("replay = %+v, want Advanced=false (idempotent no-op)", replay)
	}
	if len(replay.ForkedBranches) != 0 {
		t.Fatalf("replay = %+v, want no re-derived ForkedBranches", replay)
	}

	tokensAfter, err := uow.Snapshot.Runtime().ListBranchTokensForRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListBranchTokensForRun (after): %v", err)
	}
	if len(tokensAfter) != len(tokensBefore) {
		t.Fatalf("branch token count = %d after replay, want unchanged %d", len(tokensAfter), len(tokensBefore))
	}
	if got := len(uow.Snapshot.Jobs().(*fake.JobsRepository).Items()); got != jobsBefore {
		t.Fatalf("job count = %d after replay, want unchanged %d", got, jobsBefore)
	}
	if got := len(uow.Snapshot.Events().(*fake.EventsRepository).Items()); got != eventsBefore {
		t.Fatalf("event count = %d after replay, want unchanged %d", got, eventsBefore)
	}

	// The FORK's own NodeRun and its branches are exactly as the first
	// dispatch left them — a replay must never mutate them.
	forkNodeRun, err := uow.Snapshot.Runtime().GetNodeRun(ctx, hop.NextNodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun(fork): %v", err)
	}
	if forkNodeRun.State != runtimedomain.NodeRunSucceeded {
		t.Fatalf("fork node run = %+v, want unchanged SUCCEEDED", forkNodeRun)
	}
}

// TestExecuteNodeHandler_Fork_BranchFailure_TerminalizesOwnBranchToken
// proves the other half of this task's own locked spec ("NodeRun trong
// branch FAILED/CANCELLED phải terminalize token tương ứng trong cùng
// transaction"): driving the "to_implement" branch's own real
// ExecutionAttempt to a non-retryable FAILED (attemptPolicyDocument's own
// fixture declares no RetryableErrorCodes, mirroring
// TestExecuteNodeHandler_Failure_NonRetryableCode_FailsNodeRun) CASes that
// branch's own NodeRun to FAILED via decideRetryOrExhaustion (V4-06) AND,
// in that SAME transaction, CASes its own BranchToken to FAILED — so a
// later JOIN's own ALL/ANY/QUORUM evaluation (V4-11) never waits forever on
// a branch whose work already died. The "shortcut" branch's own token,
// unrelated to this failure, is left exactly as the fan-out created it.
func TestExecuteNodeHandler_Fork_BranchFailure_TerminalizesOwnBranchToken(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, _, hop := forkScheduleFixture(t, forkExecutableDocument(t))
	registerSharedTestAdapterBuild(t, uow)
	publishAgentProfileVersion(t, uow, "agent-profile-def", "agent-profile-v1", validAgentProfileDocument())
	publishPolicyVersion(t, uow, "attempt-policy-def", "attempt-policy-v1", attemptPolicyDocument(600))
	publishPolicyVersion(t, uow, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())

	implementBranch := findForkedBranch(hop.ForkedBranches, "to_implement")
	shortcutBranch := findForkedBranch(hop.ForkedBranches, "shortcut")
	if implementBranch == nil || shortcutBranch == nil {
		t.Fatalf("ForkedBranches = %+v, want to_implement and shortcut", hop.ForkedBranches)
	}

	scheduled, err := runtime.ScheduleExecutableNodeRun(ctx, uow, ids, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: runID, NodeRunID: implementBranch.NodeRunID, CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("ScheduleExecutableNodeRun: %v", err)
	}
	job := claimableExecuteNodeJob(t, uow, scheduled.AttemptID)

	executor := &fake.NodeExecutor{Result: ports.NodeExecutionResult{State: runtimedomain.ExecutionAttemptFailed}}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{}, fake.IsolationEnforcementChecker{}, sharedTestAgentRegistry(t), nil)
	if err := handler.Handle(ctx, job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	implementNodeRun, err := uow.Snapshot.Runtime().GetNodeRun(ctx, implementBranch.NodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun(implement): %v", err)
	}
	if implementNodeRun.State != runtimedomain.NodeRunFailed {
		t.Fatalf("implement node run = %+v, want FAILED (non-retryable code)", implementNodeRun)
	}

	implementToken, err := uow.Snapshot.Runtime().GetBranchTokenByID(ctx, implementBranch.BranchTokenID)
	if err != nil {
		t.Fatalf("GetBranchTokenByID(implement): %v", err)
	}
	if implementToken.State != runtimedomain.BranchTokenFailed {
		t.Fatalf("implement token = %+v, want FAILED (terminalized alongside its own NodeRun)", implementToken)
	}

	// The unrelated "shortcut" branch's own token is untouched.
	shortcutToken, err := uow.Snapshot.Runtime().GetBranchTokenByID(ctx, shortcutBranch.BranchTokenID)
	if err != nil {
		t.Fatalf("GetBranchTokenByID(shortcut): %v", err)
	}
	if shortcutToken.State != runtimedomain.BranchTokenSucceeded {
		t.Fatalf("shortcut token = %+v, want unchanged SUCCEEDED", shortcutToken)
	}
}
