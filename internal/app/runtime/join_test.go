package runtime_test

import (
	"context"
	"encoding/json"
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

// V4-11's own "policy matrix, restart, impossible quorum, duplicate
// completion" tests (docs/design/06-v4-runtime-engine.md). joinPolicyDocument
// builds start -> fork(FORK, one branch per branchKeys) -> {each key}_step
// (AGENT, real resolvable ProfileRef/PolicyRefs, mirroring schedule_test.go's
// own agentExecutableDocument) -> join(JOIN mode/quorumCount) -> end, so
// this file's own tests can drive each branch to a real SUCCEEDED/FAILED
// ExecutionAttempt through the exact same ScheduleExecutableNodeRun/
// ExecuteNodeHandler pipeline fork_test.go's own branch-failure test
// already established.
func joinPolicyDocument(t *testing.T, mode workflow.JoinMode, quorumCount uint32, branchKeys []string) workflow.WorkflowDocument {
	t.Helper()
	buildID := sharedTestAdapterBuild(t).ID()
	nodes := []workflow.Node{
		{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
		{Key: "fork", Type: workflow.NodeFork, Outcomes: branchKeys},
	}
	edges := []workflow.Edge{{Key: "start-to-fork", From: "start", Outcome: "next", To: "fork"}}
	for _, key := range branchKeys {
		nodeKey := key + "_step"
		nodes = append(nodes, workflow.Node{
			Key: nodeKey, Type: workflow.NodeAgent, Outcomes: []string{"done"},
			Agent: &workflow.AgentNodeConfig{
				ProfileRef:     definition.DependencyPin{Kind: definition.KindAgentProfile, DefinitionID: "agent-profile-def", VersionID: "agent-profile-v1"},
				PolicyRefs:     fullyResolvablePolicyRefs(),
				AdapterBuildID: &buildID,
			},
		})
		edges = append(edges,
			workflow.Edge{Key: "fork-to-" + key, From: "fork", Outcome: key, To: nodeKey},
			workflow.Edge{Key: key + "-to-join", From: nodeKey, Outcome: "done", To: "join"},
		)
	}
	nodes = append(nodes,
		workflow.Node{Key: "join", Type: workflow.NodeJoin, Outcomes: []string{"joined"}, Join: &workflow.JoinNodeConfig{Mode: mode, QuorumCount: quorumCount}},
		workflow.Node{Key: "end", Type: workflow.NodeEnd},
	)
	edges = append(edges, workflow.Edge{Key: "join-to-end", From: "join", Outcome: "joined", To: "end"})
	return workflow.WorkflowDocument{SchemaVersion: "1", Nodes: nodes, Edges: edges}
}

// driveBranchOutcome schedules and executes nodeRunID's own real
// ExecutionAttempt to completion (SUCCEEDED with outcome "done", or FAILED
// with a non-retryable code per this package's own shared
// attemptPolicyDocument fixture) through the exact same pipeline
// fork_test.go's own branch-failure test already uses.
func driveBranchOutcome(t *testing.T, uow *fake.UnitOfWork, ids idsource.Source, runID, nodeRunID string, succeed bool) {
	t.Helper()
	ctx := context.Background()
	scheduled, err := runtime.ScheduleExecutableNodeRun(ctx, uow, ids, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: runID, NodeRunID: nodeRunID, CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("ScheduleExecutableNodeRun(%s): %v", nodeRunID, err)
	}
	job := claimableExecuteNodeJob(t, uow, scheduled.AttemptID)
	var executor *fake.NodeExecutor
	if succeed {
		executor = &fake.NodeExecutor{Result: ports.NodeExecutionResult{State: runtimedomain.ExecutionAttemptSucceeded, SelectedOutcome: "done"}}
	} else {
		executor = &fake.NodeExecutor{Result: ports.NodeExecutionResult{State: runtimedomain.ExecutionAttemptFailed}}
	}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{}, fake.IsolationEnforcementChecker{}, sharedTestAgentRegistry(t))
	if err := handler.Handle(ctx, job); err != nil {
		t.Fatalf("Handle(%s): %v", nodeRunID, err)
	}
}

// publishJoinPolicyFixtures publishes the one shared AgentProfile/Policy
// set every branch node in joinPolicyDocument's own output points at. Takes
// ports.UnitOfWork (not *fake.UnitOfWork) so this same helper backs both
// this file's own fake tests and join_sqlite_test.go's own real-backend
// restart test.
func publishJoinPolicyFixtures(t *testing.T, uow ports.UnitOfWork) {
	t.Helper()
	registerSharedTestAdapterBuild(t, uow)
	publishAgentProfileVersion(t, uow, "agent-profile-def", "agent-profile-v1", validAgentProfileDocument())
	publishPolicyVersion(t, uow, "attempt-policy-def", "attempt-policy-v1", attemptPolicyDocument(600))
	publishPolicyVersion(t, uow, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())
}

type joinDecidedPayload struct {
	RunID          string `json:"runId"`
	JoinNodeRunID  string `json:"joinNodeRunId"`
	JoinNodeKey    string `json:"joinNodeKey"`
	ForkNodeRunID  string `json:"forkNodeRunId"`
	Mode           string `json:"mode"`
	QuorumCount    uint32 `json:"quorumCount"`
	SucceededCount int    `json:"succeededCount"`
	FailedCount    int    `json:"failedCount"`
	CancelledCount int    `json:"cancelledCount"`
	ActiveCount    int    `json:"activeCount"`
	Verdict        string `json:"verdict"`
	Reason         string `json:"reason"`
}

func joinDecidedEventsForFork(t *testing.T, uow *fake.UnitOfWork, forkNodeRunID string) []joinDecidedPayload {
	t.Helper()
	events := uow.Snapshot.Events().(*fake.EventsRepository).Items()
	var out []joinDecidedPayload
	for _, e := range events {
		if e.EventType != runtime.JoinDecidedEventType {
			continue
		}
		var p joinDecidedPayload
		if err := json.Unmarshal([]byte(e.PayloadJSON), &p); err != nil {
			t.Fatalf("decode %s payload: %v", runtime.JoinDecidedEventType, err)
		}
		if p.ForkNodeRunID == forkNodeRunID {
			out = append(out, p)
		}
	}
	return out
}

func nodeRoutedEventFor(t *testing.T, uow *fake.UnitOfWork, aggregateID string) *nodeRoutedPayload {
	t.Helper()
	events := uow.Snapshot.Events().(*fake.EventsRepository).Items()
	for _, e := range events {
		if e.EventType != runtime.NodeRoutedEventType || e.AggregateID != aggregateID {
			continue
		}
		var p nodeRoutedPayload
		if err := json.Unmarshal([]byte(e.PayloadJSON), &p); err != nil {
			t.Fatalf("decode %s payload: %v", runtime.NodeRoutedEventType, err)
		}
		return &p
	}
	return nil
}

// TestJoin_AllMode_SucceedsOnceEveryBranchSucceeds proves the ALL policy's
// own success path: both branches SUCCEEDED, the JOIN's own NodeRun
// resolves SUCCEEDED with its one declared outcome and routes downstream
// to "end" in the same transaction the second branch's own arrival
// commits.
func TestJoin_AllMode_SucceedsOnceEveryBranchSucceeds(t *testing.T) {
	uow, ids, runID, _, hop := forkScheduleFixture(t, joinPolicyDocument(t, workflow.JoinModeAll, 0, []string{"a", "b"}))
	publishJoinPolicyFixtures(t, uow)

	branchA := findForkedBranch(hop.ForkedBranches, "a")
	branchB := findForkedBranch(hop.ForkedBranches, "b")
	if branchA == nil || branchB == nil {
		t.Fatalf("ForkedBranches = %+v, want a and b", hop.ForkedBranches)
	}

	driveBranchOutcome(t, uow, ids, runID, branchA.NodeRunID, true)
	decided := joinDecidedEventsForFork(t, uow, hop.NextNodeRunID)
	if len(decided) != 0 {
		t.Fatalf("JOIN_DECIDED events after only branch A = %+v, want none yet (branch B still ACTIVE)", decided)
	}

	driveBranchOutcome(t, uow, ids, runID, branchB.NodeRunID, true)
	decided = joinDecidedEventsForFork(t, uow, hop.NextNodeRunID)
	if len(decided) != 1 {
		t.Fatalf("JOIN_DECIDED events = %+v, want exactly 1", decided)
	}
	if decided[0].Verdict != "SUCCEEDED" || decided[0].Mode != "ALL" || decided[0].SucceededCount != 2 || decided[0].ActiveCount != 0 {
		t.Fatalf("JOIN_DECIDED = %+v, want SUCCEEDED/ALL/succeeded=2/active=0", decided[0])
	}

	joinNodeRun, err := uow.Snapshot.Runtime().GetNodeRun(context.Background(), decided[0].JoinNodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun(join): %v", err)
	}
	if joinNodeRun.State != runtimedomain.NodeRunSucceeded || joinNodeRun.SelectedOutcome != "joined" {
		t.Fatalf("join node run = %+v, want SUCCEEDED/joined", joinNodeRun)
	}

	routed := nodeRoutedEventFor(t, uow, decided[0].JoinNodeRunID)
	if routed == nil || routed.NextNodeKey != "end" {
		t.Fatalf("NODE_ROUTED for join = %+v, want NextNodeKey=end", routed)
	}
}

// TestJoin_AllMode_FailsAsSoonAsOneBranchFails_WithoutWaitingForOthers
// proves ALL's own short-circuit: a single FAILED branch makes the policy
// mathematically impossible regardless of the other, still-ACTIVE branch —
// the JOIN's own NodeRun is decided FAILED immediately, never routes
// anywhere, and the still-ACTIVE branch's own token is left untouched (no
// early cancellation, per the locked Alpha policy).
func TestJoin_AllMode_FailsAsSoonAsOneBranchFails_WithoutWaitingForOthers(t *testing.T) {
	uow, ids, runID, _, hop := forkScheduleFixture(t, joinPolicyDocument(t, workflow.JoinModeAll, 0, []string{"a", "b"}))
	publishJoinPolicyFixtures(t, uow)

	branchA := findForkedBranch(hop.ForkedBranches, "a")
	branchB := findForkedBranch(hop.ForkedBranches, "b")

	driveBranchOutcome(t, uow, ids, runID, branchA.NodeRunID, false)

	decided := joinDecidedEventsForFork(t, uow, hop.NextNodeRunID)
	if len(decided) != 1 {
		t.Fatalf("JOIN_DECIDED events = %+v, want exactly 1 (short-circuit fail)", decided)
	}
	if decided[0].Verdict != "FAILED" || decided[0].Reason != runtime.JoinPolicyUnsatisfiableReason || decided[0].ActiveCount != 1 {
		t.Fatalf("JOIN_DECIDED = %+v, want FAILED/JOIN_POLICY_UNSATISFIABLE/active=1 (branch B still running)", decided[0])
	}

	joinNodeRun, err := uow.Snapshot.Runtime().GetNodeRun(context.Background(), decided[0].JoinNodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun(join): %v", err)
	}
	if joinNodeRun.State != runtimedomain.NodeRunFailed || joinNodeRun.SelectedOutcome != "" {
		t.Fatalf("join node run = %+v, want FAILED with no outcome, no route", joinNodeRun)
	}
	if routed := nodeRoutedEventFor(t, uow, decided[0].JoinNodeRunID); routed != nil {
		t.Fatalf("NODE_ROUTED for join = %+v, want none (a FAILED join never routes)", routed)
	}

	branchBToken, err := uow.Snapshot.Runtime().GetBranchTokenByID(context.Background(), branchB.BranchTokenID)
	if err != nil {
		t.Fatalf("GetBranchTokenByID(b): %v", err)
	}
	if branchBToken.State != runtimedomain.BranchTokenActive {
		t.Fatalf("branch b token = %+v, want unchanged ACTIVE (no early cancellation in V4-11)", branchBToken)
	}
}

// TestJoin_AnyMode_WaitsForFullTerminationEvenAfterThresholdMetEarly is the
// locked Alpha correction: ANY's own threshold (>=1 SUCCEEDED) is
// mathematically satisfied the instant branch A succeeds, but the JOIN
// must stay WAITING — never routing downstream — until every branch for
// this fork occurrence has reached a terminal state, so a still-running
// sibling branch is never raced against downstream work.
func TestJoin_AnyMode_WaitsForFullTerminationEvenAfterThresholdMetEarly(t *testing.T) {
	uow, ids, runID, _, hop := forkScheduleFixture(t, joinPolicyDocument(t, workflow.JoinModeAny, 0, []string{"a", "b"}))
	publishJoinPolicyFixtures(t, uow)

	branchA := findForkedBranch(hop.ForkedBranches, "a")
	branchB := findForkedBranch(hop.ForkedBranches, "b")

	driveBranchOutcome(t, uow, ids, runID, branchA.NodeRunID, true)

	if decided := joinDecidedEventsForFork(t, uow, hop.NextNodeRunID); len(decided) != 0 {
		t.Fatalf("JOIN_DECIDED events after only branch A succeeded = %+v, want none yet (ANY threshold already met, but branch B still ACTIVE)", decided)
	}

	driveBranchOutcome(t, uow, ids, runID, branchB.NodeRunID, false)
	decided := joinDecidedEventsForFork(t, uow, hop.NextNodeRunID)
	if len(decided) != 1 {
		t.Fatalf("JOIN_DECIDED events = %+v, want exactly 1", decided)
	}
	if decided[0].Verdict != "SUCCEEDED" || decided[0].SucceededCount != 1 || decided[0].FailedCount != 1 || decided[0].ActiveCount != 0 {
		t.Fatalf("JOIN_DECIDED = %+v, want SUCCEEDED/succeeded=1/failed=1/active=0", decided[0])
	}

	joinNodeRun, err := uow.Snapshot.Runtime().GetNodeRun(context.Background(), decided[0].JoinNodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun(join) after both terminal: %v", err)
	}
	if joinNodeRun.State != runtimedomain.NodeRunSucceeded {
		t.Fatalf("join node run after both branches terminal = %+v, want SUCCEEDED", joinNodeRun)
	}
}

// TestJoin_AnyMode_FailsWhenEveryBranchFails proves ANY's own short-circuit
// on the failure side: zero SUCCEEDED and zero ACTIVE means ANY can never
// be satisfied.
func TestJoin_AnyMode_FailsWhenEveryBranchFails(t *testing.T) {
	uow, ids, runID, _, hop := forkScheduleFixture(t, joinPolicyDocument(t, workflow.JoinModeAny, 0, []string{"a", "b"}))
	publishJoinPolicyFixtures(t, uow)

	branchA := findForkedBranch(hop.ForkedBranches, "a")
	branchB := findForkedBranch(hop.ForkedBranches, "b")

	driveBranchOutcome(t, uow, ids, runID, branchA.NodeRunID, false)
	if decided := joinDecidedEventsForFork(t, uow, hop.NextNodeRunID); len(decided) != 0 {
		t.Fatalf("JOIN_DECIDED after only branch A failed = %+v, want none yet (branch B still ACTIVE)", decided)
	}

	driveBranchOutcome(t, uow, ids, runID, branchB.NodeRunID, false)
	decided := joinDecidedEventsForFork(t, uow, hop.NextNodeRunID)
	if len(decided) != 1 || decided[0].Verdict != "FAILED" || decided[0].Reason != runtime.JoinPolicyUnsatisfiableReason {
		t.Fatalf("JOIN_DECIDED = %+v, want exactly 1 FAILED/JOIN_POLICY_UNSATISFIABLE", decided)
	}
}

// TestJoin_QuorumMode_ImpossibleQuorum_FailsWithoutWaitingForRemaining is
// this task's own explicit "impossible quorum" Verify-line test: QUORUM(2)
// over 3 branches, two FAIL — the third, still-ACTIVE branch can no longer
// bring SUCCEEDED+ACTIVE up to 2, so the JOIN decides FAILED immediately
// without waiting for that third branch.
func TestJoin_QuorumMode_ImpossibleQuorum_FailsWithoutWaitingForRemaining(t *testing.T) {
	uow, ids, runID, _, hop := forkScheduleFixture(t, joinPolicyDocument(t, workflow.JoinModeQuorum, 2, []string{"a", "b", "c"}))
	publishJoinPolicyFixtures(t, uow)

	branchA := findForkedBranch(hop.ForkedBranches, "a")
	branchB := findForkedBranch(hop.ForkedBranches, "b")
	branchC := findForkedBranch(hop.ForkedBranches, "c")

	driveBranchOutcome(t, uow, ids, runID, branchA.NodeRunID, false)
	if decided := joinDecidedEventsForFork(t, uow, hop.NextNodeRunID); len(decided) != 0 {
		t.Fatalf("JOIN_DECIDED after only branch A failed = %+v, want none yet (2 branches still ACTIVE, quorum 2 of 3 still reachable)", decided)
	}

	driveBranchOutcome(t, uow, ids, runID, branchB.NodeRunID, false)
	decided := joinDecidedEventsForFork(t, uow, hop.NextNodeRunID)
	if len(decided) != 1 {
		t.Fatalf("JOIN_DECIDED events = %+v, want exactly 1 (impossible quorum short-circuit)", decided)
	}
	if decided[0].Verdict != "FAILED" || decided[0].Reason != runtime.JoinPolicyUnsatisfiableReason || decided[0].ActiveCount != 1 {
		t.Fatalf("JOIN_DECIDED = %+v, want FAILED/JOIN_POLICY_UNSATISFIABLE/active=1 (branch C still running)", decided[0])
	}

	branchCToken, err := uow.Snapshot.Runtime().GetBranchTokenByID(context.Background(), branchC.BranchTokenID)
	if err != nil {
		t.Fatalf("GetBranchTokenByID(c): %v", err)
	}
	if branchCToken.State != runtimedomain.BranchTokenActive {
		t.Fatalf("branch c token = %+v, want unchanged ACTIVE (no early cancellation)", branchCToken)
	}
}

// TestJoin_QuorumMode_SucceedsOnceMetAndAllTerminal proves quorum success
// also waits for full termination even once the threshold is already
// mathematically locked in: A and B succeed (meeting QuorumCount=2) while
// C is still ACTIVE — the join must stay WAITING until C itself
// terminates (here, by failing) before resolving SUCCEEDED.
func TestJoin_QuorumMode_SucceedsOnceMetAndAllTerminal(t *testing.T) {
	uow, ids, runID, _, hop := forkScheduleFixture(t, joinPolicyDocument(t, workflow.JoinModeQuorum, 2, []string{"a", "b", "c"}))
	publishJoinPolicyFixtures(t, uow)

	branchA := findForkedBranch(hop.ForkedBranches, "a")
	branchB := findForkedBranch(hop.ForkedBranches, "b")
	branchC := findForkedBranch(hop.ForkedBranches, "c")

	driveBranchOutcome(t, uow, ids, runID, branchA.NodeRunID, true)
	driveBranchOutcome(t, uow, ids, runID, branchB.NodeRunID, true)
	if decided := joinDecidedEventsForFork(t, uow, hop.NextNodeRunID); len(decided) != 0 {
		t.Fatalf("JOIN_DECIDED after quorum met early = %+v, want none yet (branch C still ACTIVE)", decided)
	}

	driveBranchOutcome(t, uow, ids, runID, branchC.NodeRunID, false)
	decided := joinDecidedEventsForFork(t, uow, hop.NextNodeRunID)
	if len(decided) != 1 || decided[0].Verdict != "SUCCEEDED" || decided[0].SucceededCount != 2 || decided[0].FailedCount != 1 {
		t.Fatalf("JOIN_DECIDED = %+v, want exactly 1 SUCCEEDED/succeeded=2/failed=1", decided)
	}
}

// TestJoin_DuplicateCompletion_LateArrivalAfterAlreadyDecidedIsNoOp is this
// task's own explicit "duplicate completion" Verify-line test: after ALL
// already decided FAILED from branch A's own failure (branch B still
// ACTIVE at that point), branch B later actually SUCCEEDS for real — its
// own token bookkeeping proceeds normally, but the JOIN itself, already
// terminal, must never be re-decided, re-evented or routed a second time.
func TestJoin_DuplicateCompletion_LateArrivalAfterAlreadyDecidedIsNoOp(t *testing.T) {
	uow, ids, runID, _, hop := forkScheduleFixture(t, joinPolicyDocument(t, workflow.JoinModeAll, 0, []string{"a", "b"}))
	publishJoinPolicyFixtures(t, uow)

	branchA := findForkedBranch(hop.ForkedBranches, "a")
	branchB := findForkedBranch(hop.ForkedBranches, "b")

	driveBranchOutcome(t, uow, ids, runID, branchA.NodeRunID, false)
	firstDecision := joinDecidedEventsForFork(t, uow, hop.NextNodeRunID)
	if len(firstDecision) != 1 || firstDecision[0].Verdict != "FAILED" {
		t.Fatalf("JOIN_DECIDED after branch A failed = %+v, want exactly 1 FAILED", firstDecision)
	}

	// Branch B's own late, real completion — arrives after the JOIN is
	// already terminal.
	driveBranchOutcome(t, uow, ids, runID, branchB.NodeRunID, true)

	branchBToken, err := uow.Snapshot.Runtime().GetBranchTokenByID(context.Background(), branchB.BranchTokenID)
	if err != nil {
		t.Fatalf("GetBranchTokenByID(b): %v", err)
	}
	if branchBToken.State != runtimedomain.BranchTokenSucceeded {
		t.Fatalf("branch b token = %+v, want SUCCEEDED (its own bookkeeping is unaffected by the join's own prior decision)", branchBToken)
	}

	secondDecision := joinDecidedEventsForFork(t, uow, hop.NextNodeRunID)
	if len(secondDecision) != 1 {
		t.Fatalf("JOIN_DECIDED events after late arrival = %+v, want still exactly 1 (no re-decision)", secondDecision)
	}

	joinNodeRun, err := uow.Snapshot.Runtime().GetNodeRun(context.Background(), firstDecision[0].JoinNodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun(join): %v", err)
	}
	if joinNodeRun.State != runtimedomain.NodeRunFailed {
		t.Fatalf("join node run after late arrival = %+v, want still FAILED, never re-decided", joinNodeRun)
	}
	if routed := nodeRoutedEventFor(t, uow, firstDecision[0].JoinNodeRunID); routed != nil {
		t.Fatalf("NODE_ROUTED for join after late arrival = %+v, want none (a FAILED join never routes)", routed)
	}
}

// joinSharedStateDocument is start -> fork(FORK, branches a/b) -> {a,b}_step
// (AGENT, ProfileRef deliberately never resolved — this fixture is only
// ever driven via seedRunningNodeRun+AdvanceRun directly, mirroring
// advance_test.go's own agentSingleOutcomeDocument, never scheduled for
// real execution) -> join(JOIN ALL) -> end, declaring two shared-state
// fields each owned/written by a DIFFERENT branch — the fixture
// TestJoin_SharedStatePatchesFromDifferentBranches_BothMergeByTheTimeJoinRoutes
// needs.
func joinSharedStateDocument() workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "fork", Type: workflow.NodeFork, Outcomes: []string{"a", "b"}},
			{Key: "a_step", Type: workflow.NodeAgent, Outcomes: []string{"done"}, Agent: &workflow.AgentNodeConfig{
				ProfileRef: definition.DependencyPin{Kind: definition.KindAgentProfile, DefinitionID: "agent-profile-def", VersionID: "unresolved-v1"},
			}},
			{Key: "b_step", Type: workflow.NodeAgent, Outcomes: []string{"done"}, Agent: &workflow.AgentNodeConfig{
				ProfileRef: definition.DependencyPin{Kind: definition.KindAgentProfile, DefinitionID: "agent-profile-def", VersionID: "unresolved-v1"},
			}},
			{Key: "join", Type: workflow.NodeJoin, Outcomes: []string{"joined"}, Join: &workflow.JoinNodeConfig{Mode: workflow.JoinModeAll}},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-to-fork", From: "start", Outcome: "next", To: "fork"},
			{Key: "fork-to-a", From: "fork", Outcome: "a", To: "a_step"},
			{Key: "fork-to-b", From: "fork", Outcome: "b", To: "b_step"},
			{Key: "a-to-join", From: "a_step", Outcome: "done", To: "join"},
			{Key: "b-to-join", From: "b_step", Outcome: "done", To: "join"},
			{Key: "join-to-end", From: "join", Outcome: "joined", To: "end"},
		},
		SharedState: []workflow.SharedStateField{
			{Name: "fromA", Type: workflow.SharedStateTypeString, Owner: "a_step", Writers: []string{"a_step"}, Readers: []string{"end"}, MergeRule: workflow.MergeRuleLastWriteWins},
			{Name: "fromB", Type: workflow.SharedStateTypeString, Owner: "b_step", Writers: []string{"b_step"}, Readers: []string{"end"}, MergeRule: workflow.MergeRuleLastWriteWins},
		},
	}
}

// TestJoin_SharedStatePatchesFromDifferentBranches_BothMergeByTheTimeJoinRoutes
// proves this task's own "shared-state merge validation" scope item: two
// DIFFERENT branches, each patching a DIFFERENT declared field on their own
// last hop into JOIN, both survive into the final WorkflowRun.SharedState
// by the time the JOIN itself routes downstream — the EXISTING hop-by-hop
// applySharedStatePatch+CAS mechanism (HE-14-M04, V4-03) already applies
// identically to a branch's own hop as to any other, so this proves that
// generic mechanism composes correctly across a FORK/JOIN convergence
// rather than requiring any new merge machinery of its own.
func TestJoin_SharedStatePatchesFromDifferentBranches_BothMergeByTheTimeJoinRoutes(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, _, hop := forkScheduleFixture(t, joinSharedStateDocument())

	branchA := findForkedBranch(hop.ForkedBranches, "a")
	branchB := findForkedBranch(hop.ForkedBranches, "b")

	seedRunningNodeRun(t, uow, branchA.NodeRunID)
	if _, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{
		RunID: runID, NodeRunID: branchA.NodeRunID, Outcome: "done",
		SharedStatePatch: map[string]json.RawMessage{"fromA": rawJSON(t, "a-value")},
	}); err != nil {
		t.Fatalf("AdvanceRun(a): %v", err)
	}

	seedRunningNodeRun(t, uow, branchB.NodeRunID)
	if _, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{
		RunID: runID, NodeRunID: branchB.NodeRunID, Outcome: "done",
		SharedStatePatch: map[string]json.RawMessage{"fromB": rawJSON(t, "b-value")},
	}); err != nil {
		t.Fatalf("AdvanceRun(b): %v", err)
	}

	decided := joinDecidedEventsForFork(t, uow, hop.NextNodeRunID)
	if len(decided) != 1 || decided[0].Verdict != "SUCCEEDED" {
		t.Fatalf("JOIN_DECIDED = %+v, want exactly 1 SUCCEEDED", decided)
	}

	run, err := uow.Snapshot.Runtime().GetWorkflowRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	var state map[string]json.RawMessage
	if err := json.Unmarshal(run.SharedState, &state); err != nil {
		t.Fatalf("unmarshal shared state: %v", err)
	}
	if string(state["fromA"]) != `"a-value"` || string(state["fromB"]) != `"b-value"` {
		t.Fatalf("shared state = %s, want both fromA and fromB present", run.SharedState)
	}
}
