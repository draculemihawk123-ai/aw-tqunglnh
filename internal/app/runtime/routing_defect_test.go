package runtime_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// V11-03: a published workflow whose node carries a CyclePolicy whose escalation
// edge stays inside its own cycle. Workflow.Compile refuses such a document now,
// but versions published before that rule still exist and still load
// (workflow.Rehydrate), so the runtime must fail them cleanly. Observed before the
// fix: the routing error aborted FinalizeExecutionAttempt's whole transaction, the
// node that had just finished stayed RUNNING with a lost lease, and the run sat in
// RUNNING until the recovery reaper marked that attempt INDETERMINATE
// (OWNERSHIP_LOST_MUTATING) 30 seconds later — no error anywhere.
//
//	start -> implement(AGENT; CyclePolicy{3,"abort"}; done -> loop, abort -> end)
//	loop(AGENT; CyclePolicy{1,"esc"}; next -> implement, esc -> implement)   <- inside the cycle
func defectiveEscalationDocument(t *testing.T) workflow.WorkflowDocument {
	t.Helper()
	buildID := sharedTestAdapterBuild(t).ID()
	agent := func(key string, outcomes []string, policy *workflow.CyclePolicy) workflow.Node {
		return workflow.Node{
			Key: key, Type: workflow.NodeAgent, Outcomes: outcomes, CyclePolicy: policy,
			Agent: &workflow.AgentNodeConfig{
				ProfileRef: definition.DependencyPin{Kind: definition.KindAgentProfile, DefinitionID: "agent-profile-def", VersionID: "agent-profile-v1"},
				PolicyRefs: fullyResolvablePolicyRefs(), AdapterBuildID: &buildID,
			},
		}
	}
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			agent("implement", []string{"done", "abort"}, &workflow.CyclePolicy{MaxIterations: 3, EscalationOutcome: "abort"}),
			agent("loop", []string{"next", "esc"}, &workflow.CyclePolicy{MaxIterations: 1, EscalationOutcome: "esc"}),
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-implement", From: "start", Outcome: "next", To: "implement"},
			{Key: "implement-loop", From: "implement", Outcome: "done", To: "loop"},
			{Key: "implement-abort", From: "implement", Outcome: "abort", To: "end"},
			{Key: "loop-implement", From: "loop", Outcome: "next", To: "implement"},
			{Key: "loop-esc", From: "loop", Outcome: "esc", To: "implement"},
		},
	}
}

func defectiveEscalationPublishRequest(t *testing.T) (workflow.WorkflowDefinition, workflow.PublishRequest) {
	t.Helper()
	definitionRecord := workflow.WorkflowDefinition{ID: "wf-def-1", Name: "defect", Status: workflow.DefinitionActive, Version: 1}
	return definitionRecord, workflow.PublishRequest{
		VersionID: "wf-v-1", VersionNumber: 1, Document: defectiveEscalationDocument(t),
		Dependencies: workflow.DependencyManifest{Pins: []workflow.DependencyPin{{Kind: "skill", Key: "implement", Version: "1", Hash: "sha256:dependency-1"}}},
		PublishedBy:  "operator-1", PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestWorkflowCompile_RefusesWhatRehydrateStillLoads(t *testing.T) {
	definitionRecord, request := defectiveEscalationPublishRequest(t)
	if _, err := workflow.Compile(definitionRecord, request); err == nil {
		t.Fatal("Compile accepted a cyclePolicy whose escalation edge stays inside its own cycle")
	}
	if _, err := workflow.Rehydrate(definitionRecord, request); err != nil {
		t.Fatalf("Rehydrate must still load a version published before the rule existed: %v", err)
	}
}

// publishLegacyWorkflowVersionDocument stores the defective version the way an
// installation that predates the V11-03 compile rule holds it.
func publishLegacyWorkflowVersionDocument(t *testing.T, uow ports.UnitOfWork, projectID string) workflow.WorkflowVersion {
	t.Helper()
	definitionRecord, request := defectiveEscalationPublishRequest(t)
	pid := project.ProjectID(projectID)
	definitionRecord.ProjectID = &pid
	candidate, err := workflow.Rehydrate(definitionRecord, request)
	if err != nil {
		t.Fatalf("rehydrate defective workflow: %v", err)
	}
	var published workflow.WorkflowVersion
	ctx := context.Background()
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		p, err := tx.Definitions().PublishWorkflowVersion(ctx, definitionRecord, candidate)
		published = p
		return err
	}); err != nil {
		t.Fatalf("publish defective workflow version: %v", err)
	}
	return published
}

func TestExecuteNodeHandler_RoutingDefect_FailsTheRunClearlyInsteadOfHanging(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	root := readyFixture(t, uow, ids, "project-1", "repo-1")
	seedEffectiveScope(t, uow, root.WorkItemID, "repo-1")
	version := publishLegacyWorkflowVersionDocument(t, uow, "project-1")
	registerSharedTestAdapterBuild(t, uow)
	publishAgentProfileVersion(t, uow, "agent-profile-def", "agent-profile-v1", validAgentProfileDocument())
	publishPolicyVersion(t, uow, "attempt-policy-def", "attempt-policy-v1", attemptPolicyDocument(600))
	publishPolicyVersion(t, uow, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())

	start, err := runtime.StartWorkflowRun(ctx, uow, ids, testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun"),
		runtime.StartWorkflowRunRequest{ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID())})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	hop, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: start.RunID, NodeRunID: start.NodeRunID})
	if err != nil || hop.NextNodeKey != "implement" {
		t.Fatalf("AdvanceRun (start->implement) = %+v, %v", hop, err)
	}
	implementID := hop.NextNodeRunID

	// History: `loop` already ran twice (iterations 0 and 1), so activating it again
	// is iteration 2 > MaxIterations 1 — its escalation edge is forced.
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		for iteration := uint32(0); iteration <= 1; iteration++ {
			nodeRun, err := runtimedomain.NewNodeRun(
				runtimedomain.NodeRunID(ids.NewID()), runtimedomain.WorkflowRunID(start.RunID), "loop", uint64(10+iteration), iteration, nil, "state-hash", "")
			if err != nil {
				return err
			}
			nodeRun.State = runtimedomain.NodeRunSucceeded
			nodeRun.SelectedOutcome = "next"
			if _, err := tx.Runtime().CreateNodeRun(ctx, nodeRun); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed loop history: %v", err)
	}

	scheduled, err := runtime.ScheduleExecutableNodeRun(ctx, uow, ids, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: start.RunID, NodeRunID: implementID, CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("ScheduleExecutableNodeRun: %v", err)
	}
	job := claimableExecuteNodeJob(t, uow, scheduled.AttemptID)

	executor := &fake.NodeExecutor{Result: ports.NodeExecutionResult{
		State: runtimedomain.ExecutionAttemptSucceeded, TerminationReason: runtimedomain.TerminationReasonCompleted, SelectedOutcome: "done",
	}}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{}, fake.IsolationEnforcementChecker{}, sharedTestAgentRegistry(t), nil)
	if err := handler.Handle(ctx, job); err != nil {
		t.Fatalf("Handle: %v — a definition defect must not abort the finalize transaction", err)
	}

	attempt, err := uow.Snapshot.Runtime().GetExecutionAttempt(ctx, scheduled.AttemptID)
	if err != nil {
		t.Fatalf("GetExecutionAttempt: %v", err)
	}
	if attempt.State != runtimedomain.ExecutionAttemptSucceeded {
		t.Fatalf("attempt = %+v, want SUCCEEDED: the node did its work, it is the routing that is impossible", attempt)
	}
	nodeRun, err := uow.Snapshot.Runtime().GetNodeRun(ctx, implementID)
	if err != nil {
		t.Fatalf("GetNodeRun: %v", err)
	}
	if nodeRun.State != runtimedomain.NodeRunSucceeded || nodeRun.SelectedOutcome != "done" {
		t.Fatalf("node run = %+v, want SUCCEEDED/done — never left RUNNING", nodeRun)
	}
	run, err := uow.Snapshot.Runtime().GetWorkflowRun(ctx, start.RunID)
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	if run.State != runtimedomain.WorkflowRunFailed {
		t.Fatalf("run state = %s, want FAILED — a run must not sit in RUNNING with nothing left to run", run.State)
	}

	blockers, err := uow.Snapshot.Work().ListWorkItemBlockersForWorkItem(ctx, root.WorkItemID)
	if err != nil {
		t.Fatalf("ListWorkItemBlockersForWorkItem: %v", err)
	}
	var blocker *workdomain.WorkItemBlocker
	for i := range blockers {
		if blockers[i].Type == workdomain.BlockerRunFailed {
			blocker = &blockers[i]
		}
	}
	if blocker == nil {
		t.Fatalf("no RUN_FAILED blocker among %+v", blockers)
	}
	for _, want := range []string{"WORKFLOW_DEFINITION_DEFECT", "loop", "loop-esc", "implement", "publish a corrected version"} {
		if !strings.Contains(blocker.Reason, want) {
			t.Fatalf("blocker reason %q does not mention %q", blocker.Reason, want)
		}
	}
}
