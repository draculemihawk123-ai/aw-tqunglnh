package runtime_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// V4-07's own "bounded cycle pass/exhaust/restart" tests
// (docs/design/06-v4-runtime-engine.md). boundedCycleDocument is
// start -> maker(AGENT, no CyclePolicy of its own) -> checker(AGENT,
// CyclePolicy{MaxIterations:2, EscalationOutcome:"escalate"}) with a
// checker--rework-->maker edge forming a 2-node cycle, checker--pass-->
// end_pass exiting normally, and checker--escalate-->end_escalated as the
// declared, compiler-verified escape route validateBoundedCycles requires
// before this document can ever publish. maker itself declares no
// CyclePolicy — its own Iteration is still tracked (V4-07's own "no
// hardcoded 0" fix), but nothing ever forces IT to escalate; only checker,
// the node that actually declares the policy, ever exhausts.
func boundedCycleDocument() workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "maker", Type: workflow.NodeAgent, Outcomes: []string{"done"}, Agent: &workflow.AgentNodeConfig{
				ProfileRef: definition.DependencyPin{Kind: definition.KindAgentProfile, DefinitionID: "agent-default", VersionID: "agent-default-v1"},
			}},
			{
				Key: "checker", Type: workflow.NodeAgent, Outcomes: []string{"pass", "rework", "escalate"},
				Agent: &workflow.AgentNodeConfig{
					ProfileRef: definition.DependencyPin{Kind: definition.KindAgentProfile, DefinitionID: "agent-default", VersionID: "agent-default-v1"},
				},
				CyclePolicy: &workflow.CyclePolicy{MaxIterations: 2, EscalationOutcome: "escalate"},
			},
			{Key: "end_pass", Type: workflow.NodeEnd},
			{Key: "end_escalated", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-to-maker", From: "start", Outcome: "next", To: "maker"},
			{Key: "maker-to-checker", From: "maker", Outcome: "done", To: "checker"},
			{Key: "checker-to-end-pass", From: "checker", Outcome: "pass", To: "end_pass"},
			{Key: "checker-to-maker-rework", From: "checker", Outcome: "rework", To: "maker"},
			{Key: "checker-to-end-escalated", From: "checker", Outcome: "escalate", To: "end_escalated"},
		},
	}
}

// TestAdvanceRun_BoundedCycle_PassesUntilExhaustedThenForcesEscalation walks
// the full rework loop through checker's MaxIterations=2 valid rounds
// (checker's own Iteration goes 0, 1, 2 across three normal activations —
// one initial activation plus two valid rework rounds) and proves the
// FOURTH attempt to reactivate checker (candidate Iteration 3 > MaxIterations
// 2) is rejected. The exhaustion check fires on the hop that would CREATE
// checker's next activation — i.e. the 4th maker's own "done" outcome, not
// checker's own "rework" outcome — since V4-07's own locked semantics check
// the node ABOUT TO BE reactivated (downstreamNode), never the node
// currently completing.
func TestAdvanceRun_BoundedCycle_PassesUntilExhaustedThenForcesEscalation(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, startNodeRunID := startWorkflowRunFixture(t, boundedCycleDocument())

	hop, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: runID, NodeRunID: startNodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun (start->maker): %v", err)
	}
	makerID := hop.NextNodeRunID

	for round := 0; round < 3; round++ {
		seedRunningNodeRun(t, uow, makerID)
		makerHop, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: runID, NodeRunID: makerID, Outcome: "done"})
		if err != nil {
			t.Fatalf("round %d: AdvanceRun (maker->checker): %v", round, err)
		}
		if makerHop.CycleExhausted {
			t.Fatalf("round %d: maker->checker hop unexpectedly reports CycleExhausted (still within budget)", round)
		}
		checkerID := makerHop.NextNodeRunID
		checkerNodeRun, err := uow.Snapshot.Runtime().GetNodeRun(ctx, checkerID)
		if err != nil {
			t.Fatalf("round %d: GetNodeRun(checker): %v", round, err)
		}
		if checkerNodeRun.Iteration != uint32(round) {
			t.Fatalf("round %d: checker Iteration = %d, want %d", round, checkerNodeRun.Iteration, round)
		}

		seedRunningNodeRun(t, uow, checkerID)
		checkerHop, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: runID, NodeRunID: checkerID, Outcome: "rework"})
		if err != nil {
			t.Fatalf("round %d: AdvanceRun (checker->rework->maker): %v", round, err)
		}
		if checkerHop.CycleExhausted {
			t.Fatalf("round %d: checker->maker rework hop unexpectedly reports CycleExhausted (maker itself has no CyclePolicy)", round)
		}
		if checkerHop.NextNodeKey != "maker" {
			t.Fatalf("round %d: rework hop = %+v, want NextNodeKey=maker", round, checkerHop)
		}
		makerID = checkerHop.NextNodeRunID
	}

	// The 4th maker's own "done": would create checker's 4th activation
	// (candidate Iteration 3 > MaxIterations 2) — forced escalation instead.
	seedRunningNodeRun(t, uow, makerID)
	finalHop, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: runID, NodeRunID: makerID, Outcome: "done", JobID: "job-exhaust-1"})
	if err != nil {
		t.Fatalf("AdvanceRun (maker->checker exhausted): %v", err)
	}
	if !finalHop.CycleExhausted {
		t.Fatalf("final hop = %+v, want CycleExhausted=true", finalHop)
	}
	if finalHop.SkippedNodeKey != "checker" || finalHop.SkippedNodeRunID == "" {
		t.Fatalf("final hop = %+v, want a non-empty SkippedNodeRunID for checker", finalHop)
	}
	if finalHop.NextNodeKey != "end_escalated" || finalHop.NextNodeRunID == "" {
		t.Fatalf("final hop = %+v, want NextNodeKey=end_escalated (the forced escalation target)", finalHop)
	}

	// The maker outcome that TRIGGERED this ("done") is still the one
	// honestly recorded on the upstream (4th) maker NodeRun — a scheduler
	// running out of cycle budget never rewrites a real, completed outcome.
	fourthMaker, err := uow.Snapshot.Runtime().GetNodeRun(ctx, makerID)
	if err != nil {
		t.Fatalf("GetNodeRun(fourth maker): %v", err)
	}
	if fourthMaker.State != runtimedomain.NodeRunSucceeded || fourthMaker.SelectedOutcome != "done" {
		t.Fatalf("fourth maker = %+v, want SUCCEEDED with its own real outcome done, unchanged by exhaustion", fourthMaker)
	}

	skipped, err := uow.Snapshot.Runtime().GetNodeRun(ctx, finalHop.SkippedNodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun(skipped): %v", err)
	}
	if skipped.State != runtimedomain.NodeRunSkipped || skipped.SelectedOutcome != "escalate" || skipped.Iteration != 3 {
		t.Fatalf("skipped node run = %+v, want SKIPPED/escalate/Iteration=3", skipped)
	}

	escalationTarget, err := uow.Snapshot.Runtime().GetNodeRun(ctx, finalHop.NextNodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun(escalation target): %v", err)
	}
	if escalationTarget.NodeKey != "end_escalated" {
		t.Fatalf("escalation target = %+v, want node key end_escalated", escalationTarget)
	}

	events := uow.Snapshot.Events().(*fake.EventsRepository).Items()
	var cycleEvent *ports.DomainEvent
	for i := range events {
		if events[i].EventType == runtime.NodeCycleExhaustedEventType && events[i].AggregateID == finalHop.SkippedNodeRunID {
			cycleEvent = &events[i]
		}
	}
	if cycleEvent == nil {
		t.Fatalf("no %s event found for skipped node %s among %+v", runtime.NodeCycleExhaustedEventType, finalHop.SkippedNodeRunID, events)
	}
	var payload struct {
		NodeKey             string `json:"nodeKey"`
		MaxIterations       uint32 `json:"maxIterations"`
		AttemptedIteration  uint32 `json:"attemptedIteration"`
		TriggeringNodeRunID string `json:"triggeringNodeRunId"`
		TriggeringOutcome   string `json:"triggeringOutcome"`
		EscalationOutcome   string `json:"escalationOutcome"`
		EscalationNodeRunID string `json:"escalationNodeRunId"`
		JobID               string `json:"jobId"`
	}
	if err := json.Unmarshal([]byte(cycleEvent.PayloadJSON), &payload); err != nil {
		t.Fatalf("decode %s payload: %v", runtime.NodeCycleExhaustedEventType, err)
	}
	if payload.NodeKey != "checker" || payload.MaxIterations != 2 || payload.AttemptedIteration != 3 ||
		payload.TriggeringNodeRunID != makerID || payload.TriggeringOutcome != "done" ||
		payload.EscalationOutcome != "escalate" || payload.EscalationNodeRunID != finalHop.NextNodeRunID || payload.JobID != "job-exhaust-1" {
		t.Fatalf("payload = %+v, want maxIterations=2 attemptedIteration=3 triggeringNodeRunId=%s triggeringOutcome=done escalationOutcome=escalate escalationNodeRunId=%s jobId=job-exhaust-1",
			payload, makerID, finalHop.NextNodeRunID)
	}

	// NODE_ROUTED for the same hop describes the LITERAL edge target
	// (checker's own node key), pointing at the SKIPPED row — never
	// silently rewritten to the escalation target.
	var routedEvent *ports.DomainEvent
	for i := range events {
		if events[i].EventType == runtime.NodeRoutedEventType && events[i].AggregateID == makerID {
			e := events[i]
			routedEvent = &e
		}
	}
	if routedEvent == nil {
		t.Fatalf("no %s event found for maker %s among %+v", runtime.NodeRoutedEventType, makerID, events)
	}
	var routedPayload struct {
		NextNodeRunID string `json:"nextNodeRunId"`
		NextNodeKey   string `json:"nextNodeKey"`
	}
	if err := json.Unmarshal([]byte(routedEvent.PayloadJSON), &routedPayload); err != nil {
		t.Fatalf("decode %s payload: %v", runtime.NodeRoutedEventType, err)
	}
	if routedPayload.NextNodeKey != "checker" || routedPayload.NextNodeRunID != finalHop.SkippedNodeRunID {
		t.Fatalf("NODE_ROUTED payload = %+v, want nextNodeKey=checker nextNodeRunId=%s (the skipped row, the true edge target)", routedPayload, finalHop.SkippedNodeRunID)
	}
}

// --- SQLite: iteration counting and forced escalation survive a restart ---

func TestAdvanceRun_SQLite_CycleExhaustion_PersistsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agentkit-cycle-exhaustion-restart.db")

	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	u := sqlite.NewUnitOfWork(store)
	seq := idsource.NewSequential("id")

	root := readyFixtureSQLite(t, u, seq, "project-1", "repo-1")
	version := publishWorkflowVersionDocument(t, u, "project-1", "wf-def-1", "wf-v-1", boundedCycleDocument())
	startCmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	startResult, err := runtime.StartWorkflowRun(ctx, u, seq, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}

	hop, err := runtime.AdvanceRun(ctx, u, seq, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: startResult.NodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun (start->maker): %v", err)
	}
	makerID := hop.NextNodeRunID

	for round := 0; round < 3; round++ {
		seedRunningNodeRunSQLite(t, ctx, u, makerID)
		makerHop, err := runtime.AdvanceRun(ctx, u, seq, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: makerID, Outcome: "done"})
		if err != nil {
			t.Fatalf("round %d: AdvanceRun (maker->checker): %v", round, err)
		}
		checkerID := makerHop.NextNodeRunID
		seedRunningNodeRunSQLite(t, ctx, u, checkerID)
		checkerHop, err := runtime.AdvanceRun(ctx, u, seq, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: checkerID, Outcome: "rework"})
		if err != nil {
			t.Fatalf("round %d: AdvanceRun (checker->rework->maker): %v", round, err)
		}
		makerID = checkerHop.NextNodeRunID
	}

	seedRunningNodeRunSQLite(t, ctx, u, makerID)
	finalHop, err := runtime.AdvanceRun(ctx, u, seq, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: makerID, Outcome: "done"})
	if err != nil {
		t.Fatalf("AdvanceRun (maker->checker exhausted): %v", err)
	}
	if !finalHop.CycleExhausted {
		t.Fatalf("final hop = %+v, want CycleExhausted=true", finalHop)
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

	var skipped runtimedomain.NodeRun
	var escalationTarget runtimedomain.NodeRun
	if err := reopenedUow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		skipped, err = tx.Runtime().GetNodeRun(ctx, finalHop.SkippedNodeRunID)
		if err != nil {
			return err
		}
		escalationTarget, err = tx.Runtime().GetNodeRun(ctx, finalHop.NextNodeRunID)
		return err
	}); err != nil {
		t.Fatalf("read after restart: %v", err)
	}
	if skipped.State != runtimedomain.NodeRunSkipped || skipped.Iteration != 3 || skipped.SelectedOutcome != "escalate" {
		t.Fatalf("skipped node run after restart = %+v, want SKIPPED/Iteration=3/escalate", skipped)
	}
	if escalationTarget.NodeKey != "end_escalated" {
		t.Fatalf("escalation target after restart = %+v, want node key end_escalated", escalationTarget)
	}

	// GetMaxNodeIteration itself must also observe the durable value after
	// restart, not just the individual row — this is the actual query the
	// NEXT rework round (if any) would depend on.
	var maxIteration uint32
	var found bool
	if err := reopenedUow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		maxIteration, found, err = tx.Runtime().GetMaxNodeIteration(ctx, startResult.RunID, "checker")
		return err
	}); err != nil {
		t.Fatalf("GetMaxNodeIteration after restart: %v", err)
	}
	if !found || maxIteration != 3 {
		t.Fatalf("GetMaxNodeIteration(checker) after restart = (%d, %v), want (3, true)", maxIteration, found)
	}
}

// seedRunningNodeRunSQLite is seedRunningNodeRun's own sqlite-backed
// sibling (advance_test.go's own fake-only helper cannot run against a
// real ports.UnitOfWork/sqlite.Store).
func seedRunningNodeRunSQLite(t *testing.T, ctx context.Context, uow ports.UnitOfWork, nodeRunID string) {
	t.Helper()
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		current, err := tx.Runtime().GetNodeRun(ctx, nodeRunID)
		if err != nil {
			return err
		}
		_, err = tx.Runtime().TransitionNodeRun(ctx, ports.TransitionNodeRunRequest{
			NodeRunID: nodeRunID, ExpectedState: current.State, ExpectedVersion: current.Version,
			NextState: runtimedomain.NodeRunRunning,
		})
		return err
	})
	if err != nil {
		t.Fatalf("seed running node run %s: %v", nodeRunID, err)
	}
}
