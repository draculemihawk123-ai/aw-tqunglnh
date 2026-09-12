// V5-15E — "Full conformance matrix" (docs/design/07-v5-execution-
// evidence.md V5-15's own literal "Thực hiện" line: "workflow agent→
// command→gate→checker→ReleaseSet→end/completion"; user's own binding
// 5-part PR split, full text in baocaov5checklist.md's own "V5-15" section
// and memory agent-kit-v5-15-acceptance-gate-contract.md).
//
// V5-15A…D each composed a SUBSET of this graph (A: COMMAND+MACHINE_GATE
// only; B: AGENT(maker)[+MACHINE_GATE]; C/D: one executor kind at a time,
// each proving its own fault-injection path) — none of them ever ran all
// four real executor roles the design doc names together in ONE Run. This
// file closes that real, previously-unexercised composition gap: a real
// AGENT(MAKER) claims "done", a real COMMAND then really spawns and writes
// a real marker (mirroring v5AcceptScripts' own doc comment for why this
// lives outside repo-a's own git worktree — GateNodeExecutor's own
// strict-read-only diff check downstream would otherwise always fail,
// regardless of write scope or commit state), a real MACHINE_GATE reports
// a real PASS verdict derived from that marker, and finally a real
// AGENT(CHECKER) — forced read-only regardless of the owning WorkItem's
// own real grant (V5-12) — really confirms a clean diff and claims "done"
// itself. Only then does a real gitworktree.Provider.CreateLocalCommit +
// real ReleaseSet + real EvaluateCompletionCandidate carry the Run to
// SUCCEEDED/WorkItem DONE — exactly mirroring V5-15A's own proven sequence,
// just with the maker/checker AGENT bookends now real too.
package v5accept

import (
	"context"
	"os"
	"path/filepath"
	stdruntime "runtime"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	appwork "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// v5AcceptFullCompositionDocument is the literal 4-role graph the design
// doc names: START -> AGENT(maker) -> COMMAND -> MACHINE_GATE ->
// AGENT(checker) -> END.
func v5AcceptFullCompositionDocument(agentBuildID string) workflow.WorkflowDocument {
	attemptPermissionRefs := []definition.DependencyPin{
		{Kind: definition.KindPolicy, DefinitionID: "v5e-attempt-policy-def", VersionID: "v5e-attempt-policy-v1"},
		{Kind: definition.KindPolicy, DefinitionID: "v5e-permission-policy-def", VersionID: "v5e-permission-policy-v1"},
	}
	makerOutcome := "done"
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{
				Key: "maker", Type: workflow.NodeAgent, Outcomes: []string{makerOutcome},
				Agent: &workflow.AgentNodeConfig{
					ProfileRef:     definition.DependencyPin{Kind: definition.KindAgentProfile, DefinitionID: "v5e-agent-profile-def", VersionID: "v5e-agent-profile-v1"},
					PolicyRefs:     attemptPermissionRefs,
					AdapterBuildID: &agentBuildID,
					Role:           workflow.AgentRoleMaker,
				},
			},
			{
				Key: "test_a", Type: workflow.NodeCommand, Outcomes: []string{"passed"},
				Command: &workflow.CommandNodeConfig{
					CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "v5e-maker-cmd-def", VersionID: "v5e-maker-cmd-v1"},
					PolicyRefs: attemptPermissionRefs,
				},
			},
			{
				Key: "gate_b", Type: workflow.NodeMachineGate, Outcomes: []string{"passed"},
				MachineGate: &workflow.MachineGateNodeConfig{
					GateRef:    definition.DependencyPin{Kind: definition.KindGate, DefinitionID: "v5e-gate-def", VersionID: "v5e-gate-v1"},
					PolicyRefs: attemptPermissionRefs,
				},
			},
			{
				Key: "checker", Type: workflow.NodeAgent, Outcomes: []string{makerOutcome},
				Agent: &workflow.AgentNodeConfig{
					ProfileRef:     definition.DependencyPin{Kind: definition.KindAgentProfile, DefinitionID: "v5e-agent-profile-def", VersionID: "v5e-agent-profile-v1"},
					PolicyRefs:     attemptPermissionRefs,
					AdapterBuildID: &agentBuildID,
					Role:           workflow.AgentRoleChecker,
				},
			},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-maker", From: "start", Outcome: "next", To: "maker"},
			{Key: "maker-test_a", From: "maker", Outcome: makerOutcome, To: "test_a"},
			{Key: "test_a-gate_b", From: "test_a", Outcome: "passed", To: "gate_b"},
			{Key: "gate_b-checker", From: "gate_b", Outcome: "passed", To: "checker"},
			{Key: "checker-end", From: "checker", Outcome: makerOutcome, To: "end"},
		},
	}
}

// v5AcceptFullCompositionResult is everything TestV5AcceptConformanceMatrix
// (conformance_matrix_test.go) needs to sweep every oracle category against
// after buildV5AcceptFullCompositionRun below has already driven the Run to
// real SUCCEEDED/DONE.
type v5AcceptFullCompositionResult struct {
	f                  *v5AcceptFixture
	workItemID         string
	runID              string
	decisionArtifactID string
	releaseSetID       string
}

// buildV5AcceptFullCompositionRun builds a fresh real v5AcceptFixture and
// drives ONE real Run through the complete 4-role graph to real
// SUCCEEDED/DONE — the one shared builder both
// TestV5AcceptFullComposition_RealFourRoleGraphReachesSucceededAndSurvivesRestart
// and TestV5AcceptConformanceMatrix (conformance_matrix_test.go) call,
// rather than each re-deriving this ~100 lines of wiring independently.
func buildV5AcceptFullCompositionRun(t *testing.T) v5AcceptFullCompositionResult {
	t.Helper()
	f := newV5AcceptFixture(t)
	ctx := context.Background()

	t.Setenv("AGENTKIT_HELPER_MODE", "outcome-success")
	t.Setenv("AGENTKIT_HELPER_OUTCOME", "done")

	adapter := f.newClaudeAdapter(t)
	agentBuildID := f.registerAgentBuild(t, adapter)
	agentRegistry := f.newAgentRegistry(t, adapter)
	agentExecutor := f.newAgentExecutor(agentRegistry)

	publishPolicyVersion(t, f.uow, "v5e-context-policy-def", "v5e-context-policy-v1", v5AcceptContextPolicyDocument())
	publishAgentProfileVersion(t, f.uow, "v5e-agent-profile-def", "v5e-agent-profile-v1", "v5e-context-policy-def", "v5e-context-policy-v1")
	publishPolicyVersion(t, f.uow, "v5e-attempt-policy-def", "v5e-attempt-policy-v1", v5AcceptAttemptPolicyDocument(60))
	publishPolicyVersion(t, f.uow, "v5e-permission-policy-def", "v5e-permission-policy-v1", v5AcceptPermissionPolicyDocument())

	// The real COMMAND/MACHINE_GATE pair is byte-for-byte V5-15A's own
	// proven maker/gate scripts (v5AcceptScripts) — the marker-outside-
	// repo-a trick is a real architectural constraint (see that function's
	// own doc comment), not merely convention, and applies identically
	// here regardless of the two real AGENT bookends this file adds.
	markerPath := filepath.Join(f.fixtureRoot, "v5e-marker.txt")
	makerKey, makerScript, gateKey, gateScript := v5AcceptScripts(markerPath)
	skillDoc := v5AcceptTwoResourceSkillDocument(makerKey, makerScript, gateKey, gateScript)
	publishSkillVersion(t, f.uow, "v5e-skill-def", "v5e-skill-v1", skillDoc)
	makerHash := resourceContentHash(t, "v5e-skill-v1", makerKey, skillDoc)
	gateHash := resourceContentHash(t, "v5e-skill-v1", gateKey, skillDoc)

	osCompat := command.Compatibility{OS: []string{stdruntime.GOOS}}
	publishCommandVersion(t, f.uow, "v5e-maker-cmd-def", "v5e-maker-cmd-v1", command.CommandDocument{
		Executable:          command.ExecutableRef{OwnerVersionID: "v5e-skill-v1", ResourceKey: makerKey, ContentHash: makerHash},
		Argv:                []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}},
		CwdRepositoryTarget: v5AcceptRepositoryID,
		Compatibility:       osCompat,
		NetworkAccess:       command.NetworkAccessNone,
		TimeoutSeconds:      60,
		Output:              command.OutputContract{CaptureStdout: true, CaptureStderr: true, MaxOutputBytes: 1 << 16},
	})
	publishCommandVersion(t, f.uow, "v5e-gate-cmd-def", "v5e-gate-cmd-v1", command.CommandDocument{
		Executable:          command.ExecutableRef{OwnerVersionID: "v5e-skill-v1", ResourceKey: gateKey, ContentHash: gateHash},
		Argv:                []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}},
		CwdRepositoryTarget: v5AcceptRepositoryID,
		Compatibility:       osCompat,
		NetworkAccess:       command.NetworkAccessNone,
		TimeoutSeconds:      60,
		Output:              command.OutputContract{CaptureStdout: true, CaptureStderr: true, MaxOutputBytes: 1 << 16},
	})
	publishGateVersion(t, f.uow, "v5e-gate-def", "v5e-gate-v1", gate.GateDocument{
		CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "v5e-gate-cmd-def", VersionID: "v5e-gate-cmd-v1"},
		Criteria:   []gate.Criterion{{Name: "output-verified", EvidenceKey: v5AcceptGateEvidenceKey}},
	})

	// Neither real AGENT bookend produces any EvidenceEntries of its own on
	// a plain success claim (claim_done_test.go's own confirmed finding:
	// AgentNodeExecutor.buildEvidence never appends one the way COMMAND/
	// GATE's own classify methods do) — so the required-evidence set is
	// exactly V5-15A's own pair, unaffected by adding the two AGENT nodes.
	completionFields := publishPolicyVersion(t, f.uow, "v5e-completion-policy-def", "v5e-completion-policy-v1", policy.PolicyDocument{
		Category:   policy.CategoryCompletion,
		Completion: &policy.CompletionRules{RequiredEvidenceKinds: []string{runtimedomain.EvidenceKindCommandExecution, v5AcceptGateEvidenceKey}},
	})

	doc := v5AcceptFullCompositionDocument(agentBuildID)
	doc.CompletionPolicyRef = &definition.DependencyPin{Kind: definition.KindPolicy, DefinitionID: "v5e-completion-policy-def", VersionID: "v5e-completion-policy-v1"}
	dependencies := workflow.DependencyManifest{Pins: []workflow.DependencyPin{
		{Kind: string(definition.KindPolicy), Key: "v5e-completion-policy-def", Version: "v5e-completion-policy-v1", Hash: completionFields.CompiledHash()},
	}}
	version := publishWorkflowVersion(t, f.uow, v5AcceptProjectID, "v5e-workflow-def", "v5e-workflow-v1", doc, dependencies)

	router := &runtime.NodeExecutorRouter{Agent: agentExecutor, Command: f.newCommandExecutor(), Gate: f.newGateExecutor()}
	registry := f.registerHandlersWithAgents(router, "v5eh", agentRegistry)
	_, stopPool := f.startPool(t, registry)
	defer stopPool()

	// RepositoryWrite, matching V5-15A's own happy-path choice exactly (not
	// because any node here actually mutates repo-a — none do, the same
	// marker-outside-repo discipline every node respects — but because this
	// scenario ALSO needs the same real post-VERIFYING CreateLocalCommit
	// V5-15A already proved safe against a Write-scoped WorkItem; the real
	// CHECKER node forces its own resolved mount read-only regardless of
	// this grant either way, V5-12's own already-proven invariant).
	root := f.createRootWorkItem(t, "full-composition-root", workdomain.RepositoryWrite)
	child := f.createChildWorkItem(t, root.WorkItemID, "full-composition-child", workdomain.RepositoryWrite)

	baseHandle := f.repositoryWorkspaceHandle(t, root.WorkspaceSetID, v5AcceptRepositoryID, 1)
	baseRevision, err := f.provider.CaptureRevision(ctx, baseHandle)
	if err != nil {
		t.Fatalf("CaptureRevision (base): %v", err)
	}

	startCmd := testCmd("v5e-start", ports.ProjectScope(v5AcceptProjectID), "StartWorkflowRun")
	started, err := runtime.StartWorkflowRun(ctx, f.uow, f.ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: v5AcceptProjectID, WorkItemID: child.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	runID := started.RunID

	// The running pool alone drives start->maker->test_a->gate_b->checker->
	// end — real EXECUTE_NODE jobs through all three real executors, real
	// evidence, all the way to VERIFYING; nothing here manually advances a
	// NodeRun.
	run := f.waitForRunState(t, runID, runtimedomain.WorkflowRunVerifying)

	// repo-a's own working tree is still genuinely untouched at this point
	// (see v5AcceptScripts' own doc comment) — the same real, independent
	// "release preparation" write V5-15A already proved safe, here too.
	workingDirectory, err := f.provider.WorkingDirectory(ctx, baseHandle)
	if err != nil {
		t.Fatalf("WorkingDirectory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workingDirectory, "release-note.txt"), []byte("v5-15e full composition\n"), 0o600); err != nil {
		t.Fatalf("write release note: %v", err)
	}
	resultRevision, err := f.provider.CreateLocalCommit(ctx, ports.CreateLocalCommitRequest{
		Handle: baseHandle, Message: "v5-15e release note", AuthorName: "Agent Kit Test", AuthorEmail: "agent-kit@example.invalid",
	})
	if err != nil {
		t.Fatalf("CreateLocalCommit: %v", err)
	}
	if resultRevision.VCSObjectID == baseRevision.VCSObjectID {
		t.Fatalf("result revision %s did not change from base revision %s", resultRevision.VCSObjectID, baseRevision.VCSObjectID)
	}

	releaseCmd := testCmd("v5e-release-create", ports.ProjectScope(v5AcceptProjectID), "CreateReleaseSet")
	releaseResult, err := appwork.CreateReleaseSet(ctx, f.uow, f.ids, releaseCmd, appwork.CreateReleaseSetRequest{
		ProjectID: v5AcceptProjectID, FamilyID: root.FamilyID,
		Repositories: []appwork.RepositoryReleaseRequest{{
			RepositoryID: v5AcceptRepositoryID, BaseVCSObjectID: baseRevision.VCSObjectID, ResultVCSObjectID: resultRevision.VCSObjectID,
			Verdict: string(gate.VerdictPass),
		}},
	})
	if err != nil {
		t.Fatalf("CreateReleaseSet: %v", err)
	}
	sealCmd := testCmd("v5e-release-seal", ports.ProjectScope(v5AcceptProjectID), "SealReleaseSet")
	sealCmd.ExpectedVersion = releaseResult.Version
	sealResult, err := appwork.SealReleaseSet(ctx, f.uow, sealCmd, appwork.SealReleaseSetRequest{ReleaseSetID: releaseResult.ReleaseSetID})
	if err != nil {
		t.Fatalf("SealReleaseSet: %v", err)
	}
	if sealResult.State != string(workdomain.ReleaseSetSealed) {
		t.Fatalf("release set state = %s, want SEALED", sealResult.State)
	}

	evalCmd := testCmd("v5e-eval", ports.ProjectScope(v5AcceptProjectID), "EvaluateCompletionCandidate")
	evalCmd.ExpectedVersion = run.Version
	decision, err := runtime.EvaluateCompletionCandidate(ctx, f.uow, f.ids, clock.System{}, evalCmd, runtime.EvaluateCompletionCandidateRequest{RunID: runID})
	if err != nil {
		t.Fatalf("EvaluateCompletionCandidate: %v", err)
	}
	if decision.Outcome != runtime.CompletionOutcomePass {
		t.Fatalf("completion decision = %+v, want PASS", decision)
	}

	return v5AcceptFullCompositionResult{
		f: f, workItemID: child.WorkItemID, runID: runID,
		decisionArtifactID: decision.DecisionArtifactID, releaseSetID: releaseResult.ReleaseSetID,
	}
}

func TestV5AcceptFullComposition_RealFourRoleGraphReachesSucceededAndSurvivesRestart(t *testing.T) {
	result := buildV5AcceptFullCompositionRun(t)
	f := result.f

	// Checked before AND after a real restart, through the identical
	// assertion helper each time — the same discipline
	// assertHappyPathState's own doc comment establishes, extended here to
	// a Run that actually exercised all four real executor roles at once.
	f.assertFullCompositionState(t, result.workItemID, result.runID, result.decisionArtifactID, result.releaseSetID)

	f.restart(t)

	f.assertFullCompositionState(t, result.workItemID, result.runID, result.decisionArtifactID, result.releaseSetID)

	// "side effect không lặp" (docs/design/07-v5-execution-evidence.md V5-
	// 15's own literal bar): a second real EvaluateCompletionCandidate call
	// against the SAME already-SUCCEEDED Run, under a genuinely different
	// idempotency key (never the receipt-layer replay — a real, distinct
	// Command), must replay the SAME decision via
	// evaluateCompletionCandidateTx's own deterministic-DecisionArtifact-ID
	// check (completion_policy.go: the replay check runs BEFORE the
	// run.State != VERIFYING guard) rather than erroring or minting a
	// second COMPLETION_DECIDED event.
	ctx := context.Background()
	var runBeforeReplay runtimedomain.WorkflowRun
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		runBeforeReplay, err = tx.Runtime().GetWorkflowRun(ctx, result.runID)
		return err
	}); err != nil {
		t.Fatalf("read run before replay: %v", err)
	}
	replayCmd := testCmd("v5e-eval-replay", ports.ProjectScope(v5AcceptProjectID), "EvaluateCompletionCandidate")
	replayCmd.ExpectedVersion = runBeforeReplay.Version
	replayDecision, err := runtime.EvaluateCompletionCandidate(ctx, f.uow, f.ids, clock.System{}, replayCmd, runtime.EvaluateCompletionCandidateRequest{RunID: result.runID})
	if err != nil {
		t.Fatalf("replay EvaluateCompletionCandidate: %v", err)
	}
	if replayDecision.DecisionArtifactID != result.decisionArtifactID {
		t.Fatalf("replay decisionArtifactID = %s, want the original %s (a genuine duplicate, not a replay)", replayDecision.DecisionArtifactID, result.decisionArtifactID)
	}
	if replayDecision.Outcome != runtime.CompletionOutcomePass {
		t.Fatalf("replay decision = %+v, want the original PASS", replayDecision)
	}
	decidedCount := 0
	events, err := f.store.ListDomainEventsForProject(ctx, v5AcceptProjectID)
	if err != nil {
		t.Fatalf("ListDomainEventsForProject: %v", err)
	}
	for _, e := range events {
		if e.EventType == runtime.CompletionDecidedEventType && e.AggregateID == result.runID {
			decidedCount++
		}
	}
	if decidedCount != 1 {
		t.Fatalf("%s events for run %s = %d, want exactly 1 (replay must never mint a duplicate)", runtime.CompletionDecidedEventType, result.runID, decidedCount)
	}
}

// assertFullCompositionState re-reads WorkItem/WorkflowRun/every NodeRun/
// the DecisionArtifact/the ReleaseSet — mirrors assertHappyPathState
// exactly (happy_path_test.go), extended with an explicit check that all
// four node kinds this graph actually used (AGENT maker, COMMAND, MACHINE_
// GATE, AGENT checker) each really reached a real terminal NodeRun state,
// proving the trace is complete across every role, not just present.
func (f *v5AcceptFixture) assertFullCompositionState(t *testing.T, workItemID, runID, decisionArtifactID, releaseSetID string) {
	t.Helper()
	ctx := context.Background()

	var run runtimedomain.WorkflowRun
	var workItem workdomain.WorkItem
	var nodeRuns []runtimedomain.NodeRun
	var decisionArtifact runtimedomain.DecisionArtifact
	var releaseSet workdomain.ReleaseSet
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		if run, err = tx.Runtime().GetWorkflowRun(ctx, runID); err != nil {
			return err
		}
		if workItem, err = tx.Work().GetWorkItem(ctx, workItemID); err != nil {
			return err
		}
		if nodeRuns, err = tx.Runtime().ListNodeRunsForRun(ctx, runID); err != nil {
			return err
		}
		if decisionArtifact, err = tx.Runtime().GetDecisionArtifact(ctx, decisionArtifactID); err != nil {
			return err
		}
		releaseSet, err = tx.Work().GetReleaseSet(ctx, releaseSetID)
		return err
	}); err != nil {
		t.Fatalf("read full-composition state: %v", err)
	}

	if run.State != runtimedomain.WorkflowRunSucceeded {
		t.Fatalf("run.State = %s, want SUCCEEDED", run.State)
	}
	if workItem.Status != workdomain.WorkItemDone {
		t.Fatalf("workItem.Status = %s, want DONE", workItem.Status)
	}
	if decisionArtifact.Kind != runtime.CompletionDecisionKind {
		t.Fatalf("decisionArtifact.Kind = %s, want %s", decisionArtifact.Kind, runtime.CompletionDecisionKind)
	}
	if releaseSet.State != workdomain.ReleaseSetSealed {
		t.Fatalf("releaseSet.State = %s, want SEALED", releaseSet.State)
	}

	wantByKey := map[string]runtimedomain.NodeRunState{
		"maker": runtimedomain.NodeRunSucceeded, "test_a": runtimedomain.NodeRunSucceeded,
		"gate_b": runtimedomain.NodeRunSucceeded, "checker": runtimedomain.NodeRunSucceeded,
	}
	seen := make(map[string]bool, len(wantByKey))
	for _, nr := range nodeRuns {
		want, ok := wantByKey[nr.NodeKey]
		if !ok {
			continue
		}
		seen[nr.NodeKey] = true
		if nr.State != want {
			t.Fatalf("NodeRun %s.State = %s, want %s", nr.NodeKey, nr.State, want)
		}
		if nr.ActivationSequence == 0 {
			t.Fatalf("NodeRun %s.ActivationSequence = 0, want a real positive activation sequence", nr.NodeKey)
		}
	}
	for key := range wantByKey {
		if !seen[key] {
			t.Fatalf("no NodeRun observed for node %q — the full 4-role graph did not really run end to end", key)
		}
	}
}
