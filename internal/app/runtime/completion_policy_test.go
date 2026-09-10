package runtime_test

// V5-11 PR1's own test suite (2026-09-10) — EvaluateCompletionCandidate
// (completion_policy.go). Reuses workflowDocumentV1 (advance_test.go, a
// plain start->end document) and readyFixture (commands_test.go) rather
// than inventing new WorkItem/repository setup; Evidence/ExecutionAttempt/
// NodeRun fixtures here are seeded directly via repository calls
// (seedRunEvidence below), bypassing the real CommandNodeExecutor pipeline
// entirely — this file's own tests are about EvaluateCompletionCandidate's
// own evidence-gathering and disposition logic, not about how a real
// executor produces Evidence (already covered by
// command_node_executor_test.go/gate_node_executor_test.go).

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
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// publishCompletionPolicyVersion compiles+seeds a real COMPLETION-category
// PolicyVersion directly into uow's own fake Definitions repository
// (mirrors internal/app/workflowcompiler's own seedVersion test helper) and
// returns its real, deterministic CompiledHash — needed to build a
// matching workflow.DependencyPin entry for the WorkflowVersion's own
// Dependencies manifest.
func publishCompletionPolicyVersion(t *testing.T, uow *fake.UnitOfWork, definitionID, versionID string, doc policy.PolicyDocument) definition.VersionFields {
	t.Helper()
	fields, err := policy.Compile(
		policy.PolicyDefinition{
			ID: policy.PolicyDefinitionID(definitionID),
			Fields: definition.Fields{
				Kind: definition.KindPolicy, Scope: definition.GlobalScope(),
				Name: "policy", Status: definition.StatusDraft, Version: 1,
			},
		},
		policy.PublishRequest{
			VersionID: policy.PolicyVersionID(versionID), VersionNumber: 1, SchemaVersion: 1,
			Document: doc, PublishedBy: "operator-1", PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	)
	if err != nil {
		t.Fatalf("policy.Compile(%s): %v", versionID, err)
	}
	uow.Snapshot.Definitions().(*fake.DefinitionsRepository).Seed(fields)
	return fields
}

// completionCandidateFixture publishes a real COMPLETION-category
// PolicyVersion, a WorkflowVersion (from baseDocument) pinning it via
// CompletionPolicyRef, starts and drives the Run all the way to a real
// completion candidate (VERIFYING) — everything EvaluateCompletionCandidate
// itself needs already durable. Mirrors startWorkflowRunFixture's own shape
// (advance_test.go) but with a caller-chosen Dependencies manifest, since
// that helper's own publishWorkflowVersionDocument hardcodes a single
// unrelated skill dependency with no room for a policy pin, and drives
// AdvanceRun in a loop (rather than a single hardcoded hop) so any
// START->...->END shape reaches VERIFYING, not just workflowDocumentV1's own
// plain two-node graph (documentWithReworkEdge's own three-node shape needs
// two hops). A nil policyDoc publishes no CompletionPolicy at all, leaving
// CompletionPolicyRef unset — the FAIL/no-pin test case's own fixture shape.
func completionCandidateFixture(t *testing.T, baseDocument workflow.WorkflowDocument, policyDoc *policy.PolicyDocument) (uow *fake.UnitOfWork, ids idsource.Source, run runtimedomain.WorkflowRun) {
	t.Helper()
	ctx := context.Background()
	uow = fake.New()
	ids = idsource.NewSequential("id")

	document := baseDocument
	dependencies := workflow.DependencyManifest{}
	if policyDoc != nil {
		policyFields := publishCompletionPolicyVersion(t, uow, "completion-policy-1", "completion-policy-1-v1", *policyDoc)
		document.CompletionPolicyRef = &definition.DependencyPin{
			Kind: definition.KindPolicy, DefinitionID: policyFields.DefinitionID(), VersionID: policyFields.ID(),
		}
		dependencies = workflow.DependencyManifest{Pins: []workflow.DependencyPin{
			{Kind: string(definition.KindPolicy), Key: policyFields.DefinitionID(), Version: policyFields.ID(), Hash: policyFields.CompiledHash()},
		}}
	}

	root := readyFixture(t, uow, ids, "project-1", "repo-1")
	pid := project.ProjectID("project-1")
	def := workflow.WorkflowDefinition{ID: "wf-def-1", ProjectID: &pid, Name: "workflow wf-def-1", Status: workflow.DefinitionActive, Version: 1}
	candidate, err := workflow.Compile(def, workflow.PublishRequest{
		VersionID: "wf-v-1", VersionNumber: 1, Document: document, Dependencies: dependencies,
		PublishedBy: "operator-1", PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("compile workflow: %v", err)
	}
	var version workflow.WorkflowVersion
	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		v, err := tx.Definitions().PublishWorkflowVersion(ctx, def, candidate)
		version = v
		return err
	})
	if err != nil {
		t.Fatalf("publish workflow version: %v", err)
	}

	cmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	started, err := runtime.StartWorkflowRun(ctx, uow, ids, cmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	nodeRunID := started.NodeRunID
	for {
		run, err = uow.Snapshot.Runtime().GetWorkflowRun(ctx, started.RunID)
		if err != nil {
			t.Fatalf("GetWorkflowRun: %v", err)
		}
		if run.State == runtimedomain.WorkflowRunVerifying {
			break
		}
		hop, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: started.RunID, NodeRunID: nodeRunID})
		if err != nil {
			t.Fatalf("AdvanceRun: %v", err)
		}
		if !hop.Advanced {
			t.Fatalf("AdvanceRun did not advance (nodeRunID=%s) — would loop forever", nodeRunID)
		}
		nodeRunID = hop.NextNodeRunID
	}
	return uow, ids, run
}

// seedRunEvidence directly creates a terminal (SUCCEEDED) NodeRun and
// ExecutionAttempt for run — see this file's own package doc comment for
// why this bypasses the real executor pipeline — then a matching Evidence
// row referencing that Attempt. nodeKey need not exist in the published
// WorkflowDocument: computeRunNodeStateSummary silently ignores any NodeRun
// whose key it cannot resolve against the document when looking for END,
// and otherwise imposes no requirement that every NodeRun key be a real
// graph node.
func seedRunEvidence(t *testing.T, uow *fake.UnitOfWork, run runtimedomain.WorkflowRun, nodeKey, kind, verdict string) {
	t.Helper()
	ctx := context.Background()
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		nodeRun, err := runtimedomain.NewNodeRun(
			runtimedomain.NodeRunID("noderun-"+nodeKey), run.ID, nodeKey, 1, 0, nil, "input-hash", "",
		)
		if err != nil {
			return err
		}
		nodeRun.State = runtimedomain.NodeRunSucceeded
		if _, err := tx.Runtime().CreateNodeRun(ctx, nodeRun); err != nil {
			return err
		}

		attempt, err := runtimedomain.NewExecutionAttempt(
			runtimedomain.ExecutionAttemptID("attempt-"+nodeKey), nodeRun.ID, 1, "profile-hash", "test-provider", nil,
		)
		if err != nil {
			return err
		}
		attempt.State = runtimedomain.ExecutionAttemptSucceeded
		if _, err := tx.Runtime().CreateExecutionAttempt(ctx, attempt); err != nil {
			return err
		}

		evidence, err := runtimedomain.NewEvidence(
			runtimedomain.EvidenceID(string(attempt.ID)+":"+kind), run.ProjectID, run.WorkItemID, run.ID, nodeRun.ID, attempt.ID,
			kind, verdict, []string{"artifact-" + nodeKey}, workspace.RevisionSet{}, "policy-version-1",
			time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		)
		if err != nil {
			return err
		}
		_, err = tx.Runtime().CreateEvidence(ctx, evidence)
		return err
	})
	if err != nil {
		t.Fatalf("seed evidence for run %s node %s: %v", run.ID, nodeKey, err)
	}
}

func evaluateCompletionCandidateCmd(idempotencyKey string) ports.Command {
	return testCommand(idempotencyKey, "hash-"+idempotencyKey, ports.ProjectScope("project-1"), "EvaluateCompletionCandidate")
}

func requiredEvidenceCompletionPolicy() policy.PolicyDocument {
	return policy.PolicyDocument{
		Category:   policy.CategoryCompletion,
		Completion: &policy.CompletionRules{RequiredEvidenceKinds: []string{"TEST_RESULT"}},
	}
}

func TestEvaluateCompletionCandidate_Pass_RequiredEvidencePresent(t *testing.T) {
	ctx := context.Background()
	doc := requiredEvidenceCompletionPolicy()
	uow, ids, run := completionCandidateFixture(t, workflowDocumentV1(), &doc)
	seedRunEvidence(t, uow, run, "implement", "TEST_RESULT", runtimedomain.EvidenceVerdictSucceeded)

	cmd := evaluateCompletionCandidateCmd("idem-eval-1")
	cmd.ExpectedVersion = run.Version
	result, err := runtime.EvaluateCompletionCandidate(ctx, uow, ids, clock.System{}, cmd, runtime.EvaluateCompletionCandidateRequest{RunID: string(run.ID)})
	if err != nil {
		t.Fatalf("EvaluateCompletionCandidate: %v", err)
	}
	if result.Outcome != runtime.CompletionOutcomePass {
		t.Fatalf("result = %+v, want PASS", result)
	}

	updatedRun, err := uow.Snapshot.Runtime().GetWorkflowRun(ctx, string(run.ID))
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	if updatedRun.State != runtimedomain.WorkflowRunSucceeded {
		t.Fatalf("run.State = %s, want SUCCEEDED", updatedRun.State)
	}
	item, err := uow.Snapshot.Work().GetWorkItem(ctx, string(run.WorkItemID))
	if err != nil {
		t.Fatalf("GetWorkItem: %v", err)
	}
	if item.Status != workdomain.WorkItemDone {
		t.Fatalf("work item status = %s, want DONE", item.Status)
	}

	artifact, err := uow.Snapshot.Runtime().GetDecisionArtifact(ctx, result.DecisionArtifactID)
	if err != nil {
		t.Fatalf("GetDecisionArtifact: %v", err)
	}
	if artifact.Kind != runtime.CompletionDecisionKind {
		t.Fatalf("artifact.Kind = %q, want %q", artifact.Kind, runtime.CompletionDecisionKind)
	}

	found := false
	for _, e := range uow.Snapshot.Events().(*fake.EventsRepository).Items() {
		if e.EventType == runtime.CompletionDecidedEventType && e.AggregateID == string(run.ID) {
			found = true
		}
	}
	if !found {
		t.Fatal("no COMPLETION_DECIDED event found")
	}
}

func TestEvaluateCompletionCandidate_Block_MissingEvidence(t *testing.T) {
	ctx := context.Background()
	doc := requiredEvidenceCompletionPolicy()
	uow, ids, run := completionCandidateFixture(t, workflowDocumentV1(), &doc)
	// No evidence seeded at all.

	cmd := evaluateCompletionCandidateCmd("idem-eval-1")
	cmd.ExpectedVersion = run.Version
	result, err := runtime.EvaluateCompletionCandidate(ctx, uow, ids, clock.System{}, cmd, runtime.EvaluateCompletionCandidateRequest{RunID: string(run.ID)})
	if err != nil {
		t.Fatalf("EvaluateCompletionCandidate: %v", err)
	}
	if result.Outcome != runtime.CompletionOutcomeBlock {
		t.Fatalf("result = %+v, want BLOCK", result)
	}
	if !strings.Contains(result.Reason, runtime.ReasonCompletionRequirementsUnmet) {
		t.Fatalf("result.Reason = %q, want it to mention %q", result.Reason, runtime.ReasonCompletionRequirementsUnmet)
	}

	updatedRun, err := uow.Snapshot.Runtime().GetWorkflowRun(ctx, string(run.ID))
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	if updatedRun.State != runtimedomain.WorkflowRunBlocked {
		t.Fatalf("run.State = %s, want BLOCKED", updatedRun.State)
	}
	item, err := uow.Snapshot.Work().GetWorkItem(ctx, string(run.WorkItemID))
	if err != nil {
		t.Fatalf("GetWorkItem: %v", err)
	}
	if item.Status != workdomain.WorkItemBlocked {
		t.Fatalf("work item status = %s, want BLOCKED", item.Status)
	}

	// BLOCK deliberately opens no WorkItemBlocker row (ADR-021's own outcome
	// table only names one for FAIL) — confirm none exists.
	blockers, err := uow.Snapshot.Work().ListWorkItemBlockersForWorkItem(ctx, string(run.WorkItemID))
	if err != nil {
		t.Fatalf("ListWorkItemBlockers: %v", err)
	}
	if len(blockers) != 0 {
		t.Fatalf("blockers = %+v, want none for BLOCK", blockers)
	}
}

func TestEvaluateCompletionCandidate_Fail_NoCompletionPolicyPinned(t *testing.T) {
	ctx := context.Background()
	uow, ids, run := completionCandidateFixture(t, workflowDocumentV1(), nil)

	cmd := evaluateCompletionCandidateCmd("idem-eval-1")
	cmd.ExpectedVersion = run.Version
	result, err := runtime.EvaluateCompletionCandidate(ctx, uow, ids, clock.System{}, cmd, runtime.EvaluateCompletionCandidateRequest{RunID: string(run.ID)})
	if err != nil {
		t.Fatalf("EvaluateCompletionCandidate: %v", err)
	}
	if result.Outcome != runtime.CompletionOutcomeFail {
		t.Fatalf("result = %+v, want FAIL", result)
	}
	if !strings.Contains(result.Reason, runtime.ReasonNoCompletionPolicyPinned) {
		t.Fatalf("result.Reason = %q, want it to mention %q", result.Reason, runtime.ReasonNoCompletionPolicyPinned)
	}

	updatedRun, err := uow.Snapshot.Runtime().GetWorkflowRun(ctx, string(run.ID))
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	if updatedRun.State != runtimedomain.WorkflowRunFailed {
		t.Fatalf("run.State = %s, want FAILED", updatedRun.State)
	}
	item, err := uow.Snapshot.Work().GetWorkItem(ctx, string(run.WorkItemID))
	if err != nil {
		t.Fatalf("GetWorkItem: %v", err)
	}
	if item.Status != workdomain.WorkItemBlocked {
		t.Fatalf("work item status = %s, want BLOCKED", item.Status)
	}

	blockers, err := uow.Snapshot.Work().ListWorkItemBlockersForWorkItem(ctx, string(run.WorkItemID))
	if err != nil {
		t.Fatalf("ListWorkItemBlockers: %v", err)
	}
	if len(blockers) != 1 || blockers[0].Type != workdomain.BlockerCompletionPolicyFailed {
		t.Fatalf("blockers = %+v, want exactly one COMPLETION_POLICY_FAILED", blockers)
	}
}

// TestEvaluateCompletionCandidate_Replay_DifferentIdempotencyKeySameResult
// proves contract 2's own deeper, candidate-level replay guarantee:
// "retries với transport idempotency key KHÁC vẫn hội tụ cùng semantic
// decision vì artifact ID đến từ chính completion candidate" — calling
// again with a DIFFERENT cmd.IdempotencyKey (so the outer, per-invocation
// receipt layer cannot itself short-circuit this) still returns the exact
// same decision, and never attempts a second state transition (the CAS
// would fail — the Run is no longer VERIFYING — if replay didn't return
// before reaching it).
func TestEvaluateCompletionCandidate_Replay_DifferentIdempotencyKeySameResult(t *testing.T) {
	ctx := context.Background()
	doc := requiredEvidenceCompletionPolicy()
	uow, ids, run := completionCandidateFixture(t, workflowDocumentV1(), &doc)
	seedRunEvidence(t, uow, run, "implement", "TEST_RESULT", runtimedomain.EvidenceVerdictSucceeded)

	cmd1 := evaluateCompletionCandidateCmd("idem-eval-1")
	cmd1.ExpectedVersion = run.Version
	first, err := runtime.EvaluateCompletionCandidate(ctx, uow, ids, clock.System{}, cmd1, runtime.EvaluateCompletionCandidateRequest{RunID: string(run.ID)})
	if err != nil {
		t.Fatalf("EvaluateCompletionCandidate (first): %v", err)
	}
	if first.Outcome != runtime.CompletionOutcomePass {
		t.Fatalf("first = %+v, want PASS", first)
	}

	cmd2 := evaluateCompletionCandidateCmd("idem-eval-2")
	cmd2.ExpectedVersion = run.Version // stale on purpose — replay must not even reach this check
	second, err := runtime.EvaluateCompletionCandidate(ctx, uow, ids, clock.System{}, cmd2, runtime.EvaluateCompletionCandidateRequest{RunID: string(run.ID)})
	if err != nil {
		t.Fatalf("EvaluateCompletionCandidate (second, different idempotency key): %v", err)
	}
	if second != first {
		t.Fatalf("second = %+v, want identical to first %+v (replay)", second, first)
	}
}

// TestEvaluateCompletionCandidate_Conflict_InputsChangedBetweenCalls proves
// the OTHER half of contract 2's replay check: the SAME completion
// candidate (RunID, EndNodeRunID never changes) but genuinely DIFFERENT
// recomputed inputs (evidence added between the two calls) must be
// rejected as a conflict, never silently re-decided or silently replayed
// with the stale result.
func TestEvaluateCompletionCandidate_Conflict_InputsChangedBetweenCalls(t *testing.T) {
	ctx := context.Background()
	doc := requiredEvidenceCompletionPolicy()
	uow, ids, run := completionCandidateFixture(t, workflowDocumentV1(), &doc)
	// No evidence yet — first call decides BLOCK and persists that as the
	// candidate's own DecisionArtifact.

	cmd1 := evaluateCompletionCandidateCmd("idem-eval-1")
	cmd1.ExpectedVersion = run.Version
	first, err := runtime.EvaluateCompletionCandidate(ctx, uow, ids, clock.System{}, cmd1, runtime.EvaluateCompletionCandidateRequest{RunID: string(run.ID)})
	if err != nil {
		t.Fatalf("EvaluateCompletionCandidate (first): %v", err)
	}
	if first.Outcome != runtime.CompletionOutcomeBlock {
		t.Fatalf("first = %+v, want BLOCK", first)
	}

	// Now the candidate's own recomputed inputs genuinely differ.
	seedRunEvidence(t, uow, run, "implement", "TEST_RESULT", runtimedomain.EvidenceVerdictSucceeded)

	cmd2 := evaluateCompletionCandidateCmd("idem-eval-2")
	cmd2.ExpectedVersion = run.Version
	if _, err := runtime.EvaluateCompletionCandidate(ctx, uow, ids, clock.System{}, cmd2, runtime.EvaluateCompletionCandidateRequest{RunID: string(run.ID)}); err != runtime.ErrCompletionDecisionConflict {
		t.Fatalf("EvaluateCompletionCandidate (second, changed inputs): err = %v, want ErrCompletionDecisionConflict", err)
	}
}

// seedNonTerminalSiblingRun directly inserts a SECOND WorkflowRun for the
// same WorkItemID as run, in a live (RUNNING) state — the join contract's
// own invariant-violation scenario (2026-09-10: a WorkItem's own
// non-ACTIVE-driving Run history is normally always terminal by the time a
// sibling reaches VERIFYING; this fixture forces the violation directly,
// the same "poke state no real command produces yet" discipline
// readyFixture/mustCreateActiveRepository already use elsewhere in this
// package). Bypasses StartWorkflowRun's own READY-only precondition
// entirely, since forcing a genuinely inconsistent pair of Runs is exactly
// the point.
func seedNonTerminalSiblingRun(t *testing.T, uow *fake.UnitOfWork, run runtimedomain.WorkflowRun) {
	t.Helper()
	ctx := context.Background()
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		manifest, err := tx.Runtime().GetExecutionManifest(ctx, string(run.ID))
		if err != nil {
			return err
		}
		version, err := tx.Definitions().GetWorkflowVersion(ctx, string(manifest.WorkflowVersionID))
		if err != nil {
			return err
		}
		sibling, err := runtimedomain.NewWorkflowRun(
			runtimedomain.WorkflowRunID("sibling-run-1"), run.ProjectID, run.WorkItemID, version, run.FamilyID, 1, run.SharedState,
		)
		if err != nil {
			return err
		}
		sibling.State = runtimedomain.WorkflowRunRunning
		_, err = tx.Runtime().CreateWorkflowRun(ctx, sibling)
		return err
	})
	if err != nil {
		t.Fatalf("seed non-terminal sibling run for work item %s: %v", run.WorkItemID, err)
	}
}

func TestEvaluateCompletionCandidate_Block_NonTerminalSiblingRun(t *testing.T) {
	ctx := context.Background()
	doc := requiredEvidenceCompletionPolicy()
	uow, ids, run := completionCandidateFixture(t, workflowDocumentV1(), &doc)
	seedRunEvidence(t, uow, run, "implement", "TEST_RESULT", runtimedomain.EvidenceVerdictSucceeded)
	seedNonTerminalSiblingRun(t, uow, run)

	cmd := evaluateCompletionCandidateCmd("idem-eval-1")
	cmd.ExpectedVersion = run.Version
	result, err := runtime.EvaluateCompletionCandidate(ctx, uow, ids, clock.System{}, cmd, runtime.EvaluateCompletionCandidateRequest{RunID: string(run.ID)})
	if err != nil {
		t.Fatalf("EvaluateCompletionCandidate: %v", err)
	}
	// Even though the ladder itself is satisfied (real evidence seeded
	// above), the non-terminal sibling run invariant takes priority.
	if result.Outcome != runtime.CompletionOutcomeBlock {
		t.Fatalf("result = %+v, want BLOCK", result)
	}
	if !strings.Contains(result.Reason, runtime.ReasonWorkItemRunStateInconsistent) {
		t.Fatalf("result.Reason = %q, want it to mention %q", result.Reason, runtime.ReasonWorkItemRunStateInconsistent)
	}
}

func assuranceLadderCompletionPolicy() policy.PolicyDocument {
	return policy.PolicyDocument{
		Category: policy.CategoryCompletion,
		Completion: &policy.CompletionRules{
			RequiredAssurance: []policy.AssuranceRequirement{
				{Level: policy.AssuranceStatic, RequiredEvidenceKinds: []string{"LINT_RESULT"}},
				{
					Level: policy.AssuranceHuman,
					RequiredApprovals: []policy.ApprovalRequirement{
						{AuthorizedRoles: []string{"tech-lead"}},
					},
				},
			},
		},
	}
}

// seedDecidedApprovalRequest directly inserts a DECIDED ApprovalRequest for
// run — bypassing the real APPROVAL-node/ResolveApproval command pipeline
// entirely (test setup, matching this file's own established seedRunEvidence
// discipline for the identical reason: this file tests EvaluateCompletionCandidate's
// own ladder evaluation, not how a real ApprovalRequest reaches DECIDED).
func seedDecidedApprovalRequest(t *testing.T, uow *fake.UnitOfWork, run runtimedomain.WorkflowRun, decidedRole string) {
	t.Helper()
	ctx := context.Background()
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		req, err := runtimedomain.NewApprovalRequest(
			runtimedomain.ApprovalRequestID("approval-1"), run.ProjectID, run.ID, "noderun-approval", "approve",
			[]string{decidedRole}, []string{}, time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), "escalate",
		)
		if err != nil {
			return err
		}
		decidedAt := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
		req.State = runtimedomain.ApprovalRequestDecided
		req.DecidedBy = "operator-1"
		req.DecidedRole = decidedRole
		req.DecidedOutcome = "approve"
		req.DecidedAt = &decidedAt
		_, err = tx.Approvals().CreateApprovalRequest(ctx, req)
		return err
	})
	if err != nil {
		t.Fatalf("seed decided approval request for run %s: %v", run.ID, err)
	}
}

func TestEvaluateCompletionCandidate_Pass_AssuranceLadderWithApproval(t *testing.T) {
	ctx := context.Background()
	doc := assuranceLadderCompletionPolicy()
	uow, ids, run := completionCandidateFixture(t, workflowDocumentV1(), &doc)
	seedRunEvidence(t, uow, run, "lint", "LINT_RESULT", string(gate.VerdictPass))
	seedDecidedApprovalRequest(t, uow, run, "tech-lead")

	cmd := evaluateCompletionCandidateCmd("idem-eval-1")
	cmd.ExpectedVersion = run.Version
	result, err := runtime.EvaluateCompletionCandidate(ctx, uow, ids, clock.System{}, cmd, runtime.EvaluateCompletionCandidateRequest{RunID: string(run.ID)})
	if err != nil {
		t.Fatalf("EvaluateCompletionCandidate: %v", err)
	}
	if result.Outcome != runtime.CompletionOutcomePass {
		t.Fatalf("result = %+v, want PASS", result)
	}
}

func TestEvaluateCompletionCandidate_Block_AssuranceLadderApprovalMissing(t *testing.T) {
	ctx := context.Background()
	doc := assuranceLadderCompletionPolicy()
	uow, ids, run := completionCandidateFixture(t, workflowDocumentV1(), &doc)
	seedRunEvidence(t, uow, run, "lint", "LINT_RESULT", string(gate.VerdictPass))
	// No approval decided at all — the HUMAN level requirement is unsatisfied.

	cmd := evaluateCompletionCandidateCmd("idem-eval-1")
	cmd.ExpectedVersion = run.Version
	result, err := runtime.EvaluateCompletionCandidate(ctx, uow, ids, clock.System{}, cmd, runtime.EvaluateCompletionCandidateRequest{RunID: string(run.ID)})
	if err != nil {
		t.Fatalf("EvaluateCompletionCandidate: %v", err)
	}
	if result.Outcome != runtime.CompletionOutcomeBlock {
		t.Fatalf("result = %+v, want BLOCK", result)
	}
	if !strings.Contains(result.Reason, "HUMAN") {
		t.Fatalf("result.Reason = %q, want it to mention the unsatisfied HUMAN level", result.Reason)
	}
}

// TestEvaluateCompletionCandidate_Block_ReleaseSetNotSealed proves contract
// 2's own "load the exact candidate-bound SEALED ReleaseSet" requirement:
// a family with a ReleaseSet that exists but is not yet SEALED gates PASS
// even when the completion ladder itself is fully satisfied. A family with
// NO ReleaseSet at all is deliberately NOT gated this way — see
// loadLatestReleaseSetGate's own doc comment (completion_policy.go) for
// that scope decision — so this is the one dedicated test proving the
// gate actually fires once a ReleaseSet genuinely exists.
func TestEvaluateCompletionCandidate_Block_ReleaseSetNotSealed(t *testing.T) {
	ctx := context.Background()
	doc := requiredEvidenceCompletionPolicy()
	uow, ids, run := completionCandidateFixture(t, workflowDocumentV1(), &doc)
	seedRunEvidence(t, uow, run, "implement", "TEST_RESULT", runtimedomain.EvidenceVerdictSucceeded)

	createCmd := testCommand("idem-release-1", "hash-release-1", ports.ProjectScope("project-1"), "CreateReleaseSet")
	if _, err := work.CreateReleaseSet(ctx, uow, ids, createCmd, work.CreateReleaseSetRequest{
		ProjectID: "project-1", FamilyID: string(run.FamilyID),
		Repositories: []work.RepositoryReleaseRequest{
			{RepositoryID: "repo-1", BaseVCSObjectID: strings.Repeat("a", 40), ResultVCSObjectID: strings.Repeat("b", 40), Verdict: string(gate.VerdictPass)},
		},
	}); err != nil {
		t.Fatalf("CreateReleaseSet: %v", err)
	}
	// Deliberately never sealed.

	cmd := evaluateCompletionCandidateCmd("idem-eval-1")
	cmd.ExpectedVersion = run.Version
	result, err := runtime.EvaluateCompletionCandidate(ctx, uow, ids, clock.System{}, cmd, runtime.EvaluateCompletionCandidateRequest{RunID: string(run.ID)})
	if err != nil {
		t.Fatalf("EvaluateCompletionCandidate: %v", err)
	}
	if result.Outcome != runtime.CompletionOutcomeBlock {
		t.Fatalf("result = %+v, want BLOCK", result)
	}
	if !strings.Contains(result.Reason, runtime.ReasonReleaseSetNotSealed) {
		t.Fatalf("result.Reason = %q, want it to mention %q", result.Reason, runtime.ReasonReleaseSetNotSealed)
	}
}

// documentWithReworkEdge is start -> implement(ROUTER, one outcome, auto-
// advances — HE-14-M07's own deterministic single-outcome case, no typed
// config needed) -> end, plus a COMPLETION_REWORK edge (V5-10B) from end
// back to implement with the given budget. Unlike workflowDocumentV1's own
// plain two-node graph, the rework TARGET here is a real, ordinarily-
// reachable node — the realistic shape ADR-021's own "reactivate the
// implement node" framing describes, and the one V5-10B's own validation
// requires (a COMPLETION_REWORK edge may never target END, and this file's
// own completionCandidateFixture already loops AdvanceRun however many
// hops a document actually needs, so the extra "implement" hop costs
// nothing here).
func documentWithReworkEdge(maxIterations uint32) workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "implement", Type: workflow.NodeRouter, Outcomes: []string{"done"}},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-to-implement", From: "start", Outcome: "next", To: "implement"},
			{Key: "implement-to-end", From: "implement", Outcome: "done", To: "end"},
			{
				Key: "end-to-implement-rework", From: "end", To: "implement",
				Kind: workflow.EdgeCompletionRework, ReworkPolicy: &workflow.ReworkPolicy{MaxIterations: maxIterations},
			},
		},
	}
}

func TestEvaluateCompletionCandidate_Rework_ValidReworkEdgeUnderBudget(t *testing.T) {
	ctx := context.Background()
	doc := requiredEvidenceCompletionPolicy()
	uow, ids, run := completionCandidateFixture(t, documentWithReworkEdge(3), &doc)
	// No evidence seeded — the ladder is unsatisfied, but a valid,
	// under-budget rework edge exists, so this must resolve to REWORK, not
	// BLOCK.

	cmd := evaluateCompletionCandidateCmd("idem-eval-1")
	cmd.ExpectedVersion = run.Version
	result, err := runtime.EvaluateCompletionCandidate(ctx, uow, ids, clock.System{}, cmd, runtime.EvaluateCompletionCandidateRequest{RunID: string(run.ID)})
	if err != nil {
		t.Fatalf("EvaluateCompletionCandidate: %v", err)
	}
	if result.Outcome != runtime.CompletionOutcomeRework {
		t.Fatalf("result = %+v, want REWORK", result)
	}
	if result.ReworkNodeKey != "implement" || result.ReworkNodeRunID == "" {
		t.Fatalf("result = %+v, want a new activation on %q", result, "implement")
	}

	updatedRun, err := uow.Snapshot.Runtime().GetWorkflowRun(ctx, string(run.ID))
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	if updatedRun.State != runtimedomain.WorkflowRunRunning {
		t.Fatalf("run.State = %s, want RUNNING", updatedRun.State)
	}
	item, err := uow.Snapshot.Work().GetWorkItem(ctx, string(run.WorkItemID))
	if err != nil {
		t.Fatalf("GetWorkItem: %v", err)
	}
	if item.Status != workdomain.WorkItemActive {
		t.Fatalf("work item status = %s, want unchanged ACTIVE (ADR-021: REWORK leaves WorkItem ACTIVE)", item.Status)
	}

	newNodeRun, err := uow.Snapshot.Runtime().GetNodeRun(ctx, result.ReworkNodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun(%s): %v", result.ReworkNodeRunID, err)
	}
	if newNodeRun.NodeKey != "implement" || newNodeRun.State != runtimedomain.NodeRunPending {
		t.Fatalf("new node run = %+v, want NodeKey=implement State=PENDING", newNodeRun)
	}
}

// TestEvaluateCompletionCandidate_Block_ReworkBudgetExhausted proves
// GC-INV-29's own extension of ADR-021's rule: a rework edge that DOES
// exist but whose own budget is already spent is treated exactly like no
// edge at all — BLOCK, never a silent extra round. Simulated by seeding
// TWO prior SUCCEEDED "end" NodeRuns (round 1's original evaluation, round
// 2's first rework) ahead of the real one completionCandidateFixture
// itself created (round 3) — countEndReaches counts all of them, so this
// candidate is round 3 against a MaxIterations of 2.
func TestEvaluateCompletionCandidate_Block_ReworkBudgetExhausted(t *testing.T) {
	ctx := context.Background()
	doc := requiredEvidenceCompletionPolicy()
	uow, ids, run := completionCandidateFixture(t, documentWithReworkEdge(2), &doc)
	seedPriorEndReach(t, uow, run, "end-prior-1")
	seedPriorEndReach(t, uow, run, "end-prior-2")

	cmd := evaluateCompletionCandidateCmd("idem-eval-1")
	cmd.ExpectedVersion = run.Version
	result, err := runtime.EvaluateCompletionCandidate(ctx, uow, ids, clock.System{}, cmd, runtime.EvaluateCompletionCandidateRequest{RunID: string(run.ID)})
	if err != nil {
		t.Fatalf("EvaluateCompletionCandidate: %v", err)
	}
	if result.Outcome != runtime.CompletionOutcomeBlock {
		t.Fatalf("result = %+v, want BLOCK", result)
	}
	if !strings.Contains(result.Reason, runtime.ReasonReworkBudgetExhausted) {
		t.Fatalf("result.Reason = %q, want it to mention %q", result.Reason, runtime.ReasonReworkBudgetExhausted)
	}
}

// seedPriorEndReach directly inserts an extra SUCCEEDED "end" NodeRun —
// simulating an earlier round's own completion candidate having already
// reached END (test setup for the rework-budget test above; see this
// file's own package doc comment on why direct repository seeding is used
// throughout instead of actually driving multiple real REWORK rounds).
func seedPriorEndReach(t *testing.T, uow *fake.UnitOfWork, run runtimedomain.WorkflowRun, nodeRunID string) {
	t.Helper()
	ctx := context.Background()
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		nodeRun, err := runtimedomain.NewNodeRun(
			runtimedomain.NodeRunID(nodeRunID), run.ID, "end", 1, 0, nil, "input-hash", "",
		)
		if err != nil {
			return err
		}
		nodeRun.State = runtimedomain.NodeRunSucceeded
		_, err = tx.Runtime().CreateNodeRun(ctx, nodeRun)
		return err
	})
	if err != nil {
		t.Fatalf("seed prior end reach %s for run %s: %v", nodeRunID, run.ID, err)
	}
}
