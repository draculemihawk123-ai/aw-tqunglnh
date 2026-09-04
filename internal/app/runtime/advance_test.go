package runtime_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// publishWorkflowVersionDocument is publishTestWorkflowVersion's more
// general sibling: same real workflow.Compile + PublishWorkflowVersion
// pipeline, but for an arbitrary caller-supplied document instead of always
// workflowDocumentV1() — this file's own tests need graphs
// publishTestWorkflowVersion was never meant to cover (ROUTER chains,
// multi-outcome ROUTER).
func publishWorkflowVersionDocument(t *testing.T, uow ports.UnitOfWork, projectID, definitionID, versionID string, document workflow.WorkflowDocument) workflow.WorkflowVersion {
	t.Helper()
	pid := project.ProjectID(projectID)
	definition := workflow.WorkflowDefinition{
		ID: workflow.WorkflowDefinitionID(definitionID), ProjectID: &pid,
		Name: "workflow " + definitionID, Status: workflow.DefinitionActive, Version: 1,
	}
	candidate, err := workflow.Compile(definition, workflow.PublishRequest{
		VersionID: workflow.WorkflowVersionID(versionID), VersionNumber: 1, Document: document,
		Dependencies: workflow.DependencyManifest{Pins: []workflow.DependencyPin{
			{Kind: "skill", Key: "implement", Version: "1", Hash: "sha256:dependency-1"},
		}},
		PublishedBy: "operator-1", PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("compile workflow %s: %v", versionID, err)
	}
	var published workflow.WorkflowVersion
	ctx := context.Background()
	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		p, err := tx.Definitions().PublishWorkflowVersion(ctx, definition, candidate)
		published = p
		return err
	})
	if err != nil {
		t.Fatalf("publish workflow version %s: %v", versionID, err)
	}
	return published
}

// routerChainDocument is start -> router1(single outcome) -> router2(single
// outcome) -> end: a linear chain through two ROUTER nodes, each trivially
// deterministic (exactly one declared outcome), for this file's own
// "activation sequence across multiple hops" tests.
func routerChainDocument() workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "router1", Type: workflow.NodeRouter, Outcomes: []string{"go"}},
			{Key: "router2", Type: workflow.NodeRouter, Outcomes: []string{"go"}},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-to-router1", From: "start", Outcome: "next", To: "router1"},
			{Key: "router1-to-router2", From: "router1", Outcome: "go", To: "router2"},
			{Key: "router2-to-end", From: "router2", Outcome: "go", To: "end"},
		},
	}
}

func startWorkflowRunFixture(t *testing.T, document workflow.WorkflowDocument) (uow *fake.UnitOfWork, ids idsource.Source, runID, startNodeRunID string) {
	t.Helper()
	ctx := context.Background()
	u := fake.New()
	seq := idsource.NewSequential("id")
	root := readyFixture(t, u, seq, "project-1", "repo-1")
	version := publishWorkflowVersionDocument(t, u, "project-1", "wf-def-1", "wf-v-1", document)

	cmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	result, err := runtime.StartWorkflowRun(ctx, u, seq, cmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	return u, seq, result.RunID, result.NodeRunID
}

// --- AdvanceRun: single hop, node not owned by this task (END) ---

func TestAdvanceRun_StartToEnd_CompletesStartAndCreatesPendingEndWithNoFurtherJob(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, startNodeRunID := startWorkflowRunFixture(t, workflowDocumentV1())

	result, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: runID, NodeRunID: startNodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun: %v", err)
	}
	if !result.Advanced || result.SelectedOutcome != "next" {
		t.Fatalf("result = %+v, want Advanced=true SelectedOutcome=next", result)
	}
	if result.NextNodeKey != "end" || result.NextNodeRunID == "" {
		t.Fatalf("result = %+v, want NextNodeKey=end with a minted NextNodeRunID", result)
	}
	if result.NextAutoAdvanced || result.NextJobID != "" {
		t.Fatalf("result = %+v, want NextAutoAdvanced=false and no NextJobID (END is not owned by this task)", result)
	}

	startNodeRun, err := uow.Snapshot.Runtime().GetNodeRun(ctx, startNodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun(start): %v", err)
	}
	if startNodeRun.State != runtimedomain.NodeRunSucceeded || startNodeRun.SelectedOutcome != "next" {
		t.Fatalf("start node run = %+v, want SUCCEEDED with outcome next", startNodeRun)
	}

	endNodeRun, err := uow.Snapshot.Runtime().GetNodeRun(ctx, result.NextNodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun(end): %v", err)
	}
	if endNodeRun.State != runtimedomain.NodeRunPending || endNodeRun.NodeKey != "end" || endNodeRun.ActivationSequence != 2 {
		t.Fatalf("end node run = %+v, want PENDING node=end activation_sequence=2", endNodeRun)
	}

	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	count := 0
	for _, j := range jobs {
		if j.Kind == runtime.AdvanceRunJobKind {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("ADVANCE_RUN job count = %d, want exactly 1 (StartWorkflowRun's own, none added for END)", count)
	}
}

// --- AdvanceRun: activation sequence across a multi-hop ROUTER chain ---

func TestAdvanceRun_RouterChain_ActivationSequenceIncrementsInOrder(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, startNodeRunID := startWorkflowRunFixture(t, routerChainDocument())

	// Hop 1: start -> router1 (auto-advanced, since router1 has one outcome).
	hop1, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: runID, NodeRunID: startNodeRunID})
	if err != nil {
		t.Fatalf("hop 1 AdvanceRun: %v", err)
	}
	if hop1.NextNodeKey != "router1" || !hop1.NextAutoAdvanced || hop1.NextJobID == "" {
		t.Fatalf("hop 1 = %+v, want NextNodeKey=router1 auto-advanced with a follow-up job", hop1)
	}
	router1NodeRun, err := uow.Snapshot.Runtime().GetNodeRun(ctx, hop1.NextNodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun(router1): %v", err)
	}
	if router1NodeRun.State != runtimedomain.NodeRunRunning || router1NodeRun.ActivationSequence != 2 {
		t.Fatalf("router1 node run = %+v, want RUNNING activation_sequence=2", router1NodeRun)
	}

	// Hop 2: router1 -> router2 (also auto-advanced).
	hop2, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: runID, NodeRunID: hop1.NextNodeRunID})
	if err != nil {
		t.Fatalf("hop 2 AdvanceRun: %v", err)
	}
	if hop2.NextNodeKey != "router2" || !hop2.NextAutoAdvanced || hop2.NextJobID == "" {
		t.Fatalf("hop 2 = %+v, want NextNodeKey=router2 auto-advanced with a follow-up job", hop2)
	}
	router2NodeRun, err := uow.Snapshot.Runtime().GetNodeRun(ctx, hop2.NextNodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun(router2): %v", err)
	}
	if router2NodeRun.State != runtimedomain.NodeRunRunning || router2NodeRun.ActivationSequence != 3 {
		t.Fatalf("router2 node run = %+v, want RUNNING activation_sequence=3", router2NodeRun)
	}

	// Hop 3: router2 -> end (not owned by this task, PENDING, no job).
	hop3, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: runID, NodeRunID: hop2.NextNodeRunID})
	if err != nil {
		t.Fatalf("hop 3 AdvanceRun: %v", err)
	}
	if hop3.NextNodeKey != "end" || hop3.NextAutoAdvanced || hop3.NextJobID != "" {
		t.Fatalf("hop 3 = %+v, want NextNodeKey=end not auto-advanced with no job", hop3)
	}
	endNodeRun, err := uow.Snapshot.Runtime().GetNodeRun(ctx, hop3.NextNodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun(end): %v", err)
	}
	if endNodeRun.State != runtimedomain.NodeRunPending || endNodeRun.ActivationSequence != 4 {
		t.Fatalf("end node run = %+v, want PENDING activation_sequence=4", endNodeRun)
	}
}

// --- AdvanceRun: GC-INV-11 allow-list enforcement ---

func TestAdvanceRun_OutsideOutcome_RejectedByAllowList(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, startNodeRunID := startWorkflowRunFixture(t, workflowDocumentV1())

	_, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: runID, NodeRunID: startNodeRunID, Outcome: "not-a-declared-outcome"})
	if !errors.Is(err, runtime.ErrOutcomeNotAllowed) {
		t.Fatalf("err = %v, want ErrOutcomeNotAllowed", err)
	}

	// The rejected attempt must not have touched the NodeRun at all.
	startNodeRun, getErr := uow.Snapshot.Runtime().GetNodeRun(ctx, startNodeRunID)
	if getErr != nil {
		t.Fatalf("GetNodeRun: %v", getErr)
	}
	if startNodeRun.State != runtimedomain.NodeRunRunning || startNodeRun.SelectedOutcome != "" {
		t.Fatalf("start node run = %+v, want unchanged RUNNING with no outcome", startNodeRun)
	}
}

// A multi-outcome ROUTER can no longer be published at all (correction
// found during V4-03 review, docs/design/06-v4-runtime-engine.md):
// internal/domain/workflow's own compiler now rejects it at publish time —
// see TestValidateDocumentRejectsInvalidGraphs's own "router with more than
// one outcome" case in internal/domain/workflow/compiler_test.go. That
// makes the runtime-level ErrOutcomeRequired branch for a multi-outcome
// ROUTER specifically unreachable through any WorkflowVersion this
// package's own public API can construct (workflow.Compile is the only
// constructor, and it now refuses such a document) — there is nothing left
// to exercise here. The identical ErrOutcomeRequired code path (a node
// AdvanceRun does not own auto-deriving for) is still exercised below by
// TestAdvanceRun_AgentNodeNeverAutoAdvances, and AdvanceRun's own
// isStructuralRoutingNode check keeps the runtime guard as defense in
// depth regardless.

// --- AdvanceRun: never auto-derives for a node type that needs real work ---

// TestAdvanceRun_AgentNodeNeverAutoAdvances is the guard this task's own
// package doc comment describes as its most important correctness rule: a
// single-outcome AGENT node must NEVER be auto-completed the way a
// single-outcome START/ROUTER is — it must always require an externally
// supplied Outcome (from a real Attempt, V4-04's own future scope), even
// when it only ever declares one possible outcome. No real dispatcher
// exists yet to reach this state, so this test seeds a RUNNING AGENT
// NodeRun directly (the same "poke the state a future task's own command
// would otherwise reach" discipline this package's other tests already use
// for WorkItem READY).
func TestAdvanceRun_AgentNodeNeverAutoAdvances(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, startNodeRunID := startWorkflowRunFixture(t, agentSingleOutcomeDocument())

	hop1, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: runID, NodeRunID: startNodeRunID})
	if err != nil {
		t.Fatalf("hop 1 AdvanceRun: %v", err)
	}
	if hop1.NextNodeKey != "implement" || hop1.NextAutoAdvanced || hop1.NextJobID != "" {
		t.Fatalf("hop 1 = %+v, want NextNodeKey=implement NOT auto-advanced with no job (AGENT needs a real Attempt)", hop1)
	}
	agentNodeRun, err := uow.Snapshot.Runtime().GetNodeRun(ctx, hop1.NextNodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun(implement): %v", err)
	}
	if agentNodeRun.State != runtimedomain.NodeRunPending {
		t.Fatalf("agent node run = %+v, want PENDING", agentNodeRun)
	}

	seedRunningNodeRun(t, uow, hop1.NextNodeRunID)
	_, err = runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: runID, NodeRunID: hop1.NextNodeRunID})
	if !errors.Is(err, runtime.ErrOutcomeRequired) {
		t.Fatalf("err = %v, want ErrOutcomeRequired (AGENT must never auto-derive, even with one declared outcome)", err)
	}
}

func agentSingleOutcomeDocument() workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "implement", Type: workflow.NodeAgent, Outcomes: []string{"done"}, Agent: &workflow.AgentNodeConfig{
				ProfileRef: definition.DependencyPin{
					Kind: definition.KindAgentProfile, DefinitionID: "agent-default", VersionID: "agent-default-v1",
				},
			}},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-to-implement", From: "start", Outcome: "next", To: "implement"},
			{Key: "implement-to-end", From: "implement", Outcome: "done", To: "end"},
		},
	}
}

// --- AdvanceRun: idempotent replay ---

func TestAdvanceRun_ReplayAfterAlreadyAdvanced_IsNoOp(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, startNodeRunID := startWorkflowRunFixture(t, workflowDocumentV1())

	first, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: runID, NodeRunID: startNodeRunID})
	if err != nil {
		t.Fatalf("first AdvanceRun: %v", err)
	}

	second, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: runID, NodeRunID: startNodeRunID})
	if err != nil {
		t.Fatalf("replayed AdvanceRun: %v", err)
	}
	if second.Advanced {
		t.Fatalf("replayed result = %+v, want Advanced=false", second)
	}
	if second.SelectedOutcome != first.SelectedOutcome {
		t.Fatalf("replayed outcome = %s, want %s (the already-committed one)", second.SelectedOutcome, first.SelectedOutcome)
	}

	// No second downstream NodeRun/job was created by the replay.
	endNodeRun, err := uow.Snapshot.Runtime().GetNodeRun(ctx, first.NextNodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun(end): %v", err)
	}
	if endNodeRun.ActivationSequence != 2 {
		t.Fatalf("end node run activation sequence = %d, want 2 (unchanged by the replay)", endNodeRun.ActivationSequence)
	}
}

// --- AdvanceRun: defensive cross-check ---

func TestAdvanceRun_NodeRunBelongsToDifferentRun_Rejected(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, _ := startWorkflowRunFixture(t, workflowDocumentV1())
	_, otherStartNodeRunID := startWorkflowRunFixtureSecondFamily(t, uow, ids)

	_, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: runID, NodeRunID: otherStartNodeRunID})
	if !errors.Is(err, runtime.ErrNodeRunMismatch) {
		t.Fatalf("err = %v, want ErrNodeRunMismatch", err)
	}
}

// startWorkflowRunFixtureSecondFamily seeds a second, independent root
// WorkItem/repository/run in the SAME fake UnitOfWork as an existing
// fixture, for the one test above that needs two distinct WorkflowRuns to
// prove a NodeRunID/RunID mismatch is actually caught.
func startWorkflowRunFixtureSecondFamily(t *testing.T, uow *fake.UnitOfWork, ids idsource.Source) (runID, startNodeRunID string) {
	t.Helper()
	ctx := context.Background()
	root := readyFixture(t, uow, ids, "project-1", "repo-2")
	version := publishWorkflowVersionDocument(t, uow, "project-1", "wf-def-2", "wf-v-2", workflowDocumentV1())
	cmd := testCommand("idem-start-2", "hash-b", ports.ProjectScope("project-1"), "StartWorkflowRun")
	result, err := runtime.StartWorkflowRun(ctx, uow, ids, cmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun (second family): %v", err)
	}
	return result.RunID, result.NodeRunID
}

// seedRunningNodeRun force-transitions an existing PENDING NodeRun straight
// to RUNNING — test setup standing in for whatever future dispatcher
// (V4-04+) would otherwise do; not the behavior under test in either of
// this file's own callers.
func seedRunningNodeRun(t *testing.T, uow *fake.UnitOfWork, nodeRunID string) {
	t.Helper()
	ctx := context.Background()
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
		t.Fatalf("seed RUNNING node run %s: %v", nodeRunID, err)
	}
}
