// V5-15B — "Claim done" + "gate failure" (docs/design/07-v5-execution-
// evidence.md V5-15's own "inject claim done, gate fail..." line; user's
// own binding 5-part PR split, full text in baocaov5checklist.md's own
// "V5-15" section and memory agent-kit-v5-15-acceptance-gate-contract.md).
//
// Why this needs a real AGENT (V5-15A deliberately used COMMAND+
// MACHINE_GATE only, no AGENT): COMMAND/MACHINE_GATE have no free-text
// self-report channel at all — a COMMAND's own outcome is always derived
// deterministically from its real exit code (CommandNodeExecutor.classify
// never even accepts a "proposed" outcome — "proposed is always nil"), so
// there is no gap between what it "claims" and what really happened. An
// AGENT is different: its own real transcript can contain a real, correctly
// -parsed `<agentkit-outcome>` marker claiming "done" — a genuine
// self-report a false-completion oracle must never trust alone. This file
// proves that for real: a real claude.Adapter drives a real cmd/fake-claude
// process (internal/adapters/providers' own "recorded provider" fake-CLI
// protocol — no live network Claude/Codex call, exactly the "recorded
// fake" layer this task's own contract allows) to really claim "done" via
// a real, correctly-parsed outcome marker — and confirms, empirically,
// that this claim ALONE:
//   - never produces any Evidence row at all (confirmed by reading the
//     code: AgentNodeExecutor.buildEvidence reuses the exact same shared
//     buildEvidence free function COMMAND/GATE use, and never appends any
//     EvidenceEntries the way each of their own classify methods
//     explicitly does — an AGENT's own successful Attempt evidence is
//     purely a diff manifest + its own ProposedOutcome, nothing a
//     CompletionPolicy's own RequiredEvidenceKinds could ever match), so
//   - a CompletionPolicy requiring ANY evidence kind can only ever be
//     satisfied by a real, independent, downstream check (here, a real
//     MACHINE_GATE) — never by the maker's own claim, no matter how
//     correctly it parses.
//
// The SAME real MACHINE_GATE also closes "gate failure": V4's own already-
// existing NON_RETRYABLE_FAILURE -> transitionRunToFailedTx path
// (completion.go, confirmed by reading the code — nothing new built here)
// means a real Gate reporting FAIL never even reaches VERIFYING at all;
// the owning WorkflowRun fails directly, an even stronger fail-closed
// guarantee than reaching VERIFYING and then being BLOCKed by
// EvaluateCompletionCandidate.
//
// TestV5AcceptFalseCompletionOracle ties all three cases together as one
// real oracle: across every negative case (claim alone; claim + a real
// gate that FAILs), the real DONE count must be zero; only the case with
// a real claim AND a real, independently PASSing gate ever reaches DONE —
// docs/design/07-v5-execution-evidence.md's own literal bar, "false
// completion oracle đếm DONE sai bằng 0."
package v5accept

import (
	"context"
	stdruntime "runtime"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// v5AcceptClaimDoneEvidenceKey is the one gate.Criterion.EvidenceKey the
// real, fixed-verdict gate script below reports — the ONLY evidence kind
// this file's own CompletionPolicy ever requires, deliberately never one
// an AGENT could produce on its own (see this file's own package doc
// comment for why no such kind can exist).
const v5AcceptClaimDoneEvidenceKey = "INDEPENDENT_CHECK"

// v5AcceptFixedVerdictGateScript is a real, cross-platform script that
// unconditionally reports verdict — deliberately trivial: an AGENT leaves
// no on-disk output at all for a real gate to inspect (fake-claude only
// ever emits its own protocol over stdout, never touches the filesystem),
// so this stands in for whatever a real independent check would
// separately confirm. What this scenario tests is not WHAT the gate
// checks, but that only ITS OWN real, separate verdict — never the
// maker's own self-report — can ever satisfy the completion policy.
func v5AcceptFixedVerdictGateScript(verdict string) (key, script string) {
	if stdruntime.GOOS == "windows" {
		return "gate-fixed.bat", "@echo off\r\necho {\"" + v5AcceptClaimDoneEvidenceKey + "\":{\"verdict\":\"" + verdict + "\"}}\r\nexit /b 0\r\n"
	}
	return "gate-fixed.sh", "#!/bin/sh\necho '{\"" + v5AcceptClaimDoneEvidenceKey + "\":{\"verdict\":\"" + verdict + "\"}}'\nexit 0\n"
}

// v5AcceptClaimDoneDocument is this file's own real multi-node graph:
// START -> AGENT(maker, real spawn, real "done" claim) -> [optional
// MACHINE_GATE(checker, real spawn, fixed verdict)] -> END.
func v5AcceptClaimDoneDocument(agentBuildID string, withGate bool) workflow.WorkflowDocument {
	attemptPermissionRefs := []definition.DependencyPin{
		{Kind: definition.KindPolicy, DefinitionID: "v5b-attempt-policy-def", VersionID: "v5b-attempt-policy-v1"},
		{Kind: definition.KindPolicy, DefinitionID: "v5b-permission-policy-def", VersionID: "v5b-permission-policy-v1"},
	}
	makerOutcome := "done"
	nodes := []workflow.Node{
		{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
		{
			Key: "maker", Type: workflow.NodeAgent, Outcomes: []string{makerOutcome},
			Agent: &workflow.AgentNodeConfig{
				ProfileRef:     definition.DependencyPin{Kind: definition.KindAgentProfile, DefinitionID: "v5b-agent-profile-def", VersionID: "v5b-agent-profile-v1"},
				PolicyRefs:     attemptPermissionRefs,
				AdapterBuildID: &agentBuildID,
			},
		},
	}
	edges := []workflow.Edge{{Key: "start-maker", From: "start", Outcome: "next", To: "maker"}}
	if withGate {
		nodes = append(nodes, workflow.Node{
			Key: "checker", Type: workflow.NodeMachineGate, Outcomes: []string{"passed"},
			MachineGate: &workflow.MachineGateNodeConfig{
				GateRef:    definition.DependencyPin{Kind: definition.KindGate, DefinitionID: "v5b-gate-def", VersionID: "v5b-gate-v1"},
				PolicyRefs: attemptPermissionRefs,
			},
		})
		edges = append(edges,
			workflow.Edge{Key: "maker-checker", From: "maker", Outcome: makerOutcome, To: "checker"},
			workflow.Edge{Key: "checker-end", From: "checker", Outcome: "passed", To: "end"},
		)
	} else {
		edges = append(edges, workflow.Edge{Key: "maker-end", From: "maker", Outcome: makerOutcome, To: "end"})
	}
	nodes = append(nodes, workflow.Node{Key: "end", Type: workflow.NodeEnd})
	return workflow.WorkflowDocument{SchemaVersion: "1", Nodes: nodes, Edges: edges}
}

// v5AcceptClaimDoneCaseResult is what each of this file's own 3 scenarios
// reports back to TestV5AcceptFalseCompletionOracle's own tally.
type v5AcceptClaimDoneCaseResult struct {
	runSucceeded                   bool
	workItemDone                   bool
	realIndependentEvidencePresent bool
}

func TestV5AcceptFalseCompletionOracle(t *testing.T) {
	results := map[string]v5AcceptClaimDoneCaseResult{
		"agent claim alone (no gate at all)":                  runV5AcceptClaimDoneCase(t, "agent-alone", false, ""),
		"agent claim + a real gate that independently FAILs":  runV5AcceptClaimDoneCase(t, "agent-plus-failing-gate", true, gate.VerdictFail),
		"agent claim + a real gate that independently PASSes": runV5AcceptClaimDoneCase(t, "agent-plus-passing-gate", true, gate.VerdictPass),
	}

	doneCount := 0
	for name, result := range results {
		if result.workItemDone {
			doneCount++
		}
		t.Logf("case %q: runSucceeded=%v workItemDone=%v independentEvidencePresent=%v", name, result.runSucceeded, result.workItemDone, result.realIndependentEvidencePresent)
	}
	// docs/design/07-v5-execution-evidence.md's own literal bar: "false
	// completion oracle đếm DONE sai bằng 0" — exactly one of the three
	// real cases above (the one with real, independent PASSing evidence)
	// may ever reach DONE; the other two (an unbacked claim; a real gate
	// that independently rejected it) must never count as a false
	// completion, regardless of what the maker itself claimed.
	if doneCount != 1 {
		t.Fatalf("false-completion oracle: %d real cases reached WorkItem DONE, want exactly 1 (the real-claim+real-independent-PASS case); results=%+v", doneCount, results)
	}
	if !results["agent claim + a real gate that independently PASSes"].workItemDone {
		t.Fatal("the one case with real, independent PASSing evidence did not itself reach DONE")
	}
	for _, negative := range []string{"agent claim alone (no gate at all)", "agent claim + a real gate that independently FAILs"} {
		if results[negative].workItemDone {
			t.Fatalf("case %q falsely reached DONE — a false completion", negative)
		}
	}
}

// runV5AcceptClaimDoneCase builds a fresh, real v5AcceptFixture and drives
// ONE real Run through it: a real AGENT claims "done", optionally followed
// by a real MACHINE_GATE reporting gateVerdict. withGate=false never
// publishes any Gate/Command-for-gate definitions at all — the maker's own
// claim is the WHOLE graph.
func runV5AcceptClaimDoneCase(t *testing.T, caseName string, withGate bool, gateVerdict gate.Verdict) v5AcceptClaimDoneCaseResult {
	t.Helper()
	f := newV5AcceptFixture(t)
	ctx := context.Background()

	t.Setenv("AGENTKIT_HELPER_MODE", "outcome-success")
	t.Setenv("AGENTKIT_HELPER_OUTCOME", "done")

	adapter := f.newClaudeAdapter(t)
	agentBuildID := f.registerAgentBuild(t, adapter)
	agentRegistry := f.newAgentRegistry(t, adapter)
	agentExecutor := f.newAgentExecutor(agentRegistry)

	publishPolicyVersion(t, f.uow, "v5b-context-policy-def", "v5b-context-policy-v1", v5AcceptContextPolicyDocument())
	publishAgentProfileVersion(t, f.uow, "v5b-agent-profile-def", "v5b-agent-profile-v1", "v5b-context-policy-def", "v5b-context-policy-v1")
	publishPolicyVersion(t, f.uow, "v5b-attempt-policy-def", "v5b-attempt-policy-v1", v5AcceptAttemptPolicyDocument(60))
	publishPolicyVersion(t, f.uow, "v5b-permission-policy-def", "v5b-permission-policy-v1", v5AcceptPermissionPolicyDocument())

	router := &runtime.NodeExecutorRouter{Agent: agentExecutor}
	if withGate {
		gateKey, gateScript := v5AcceptFixedVerdictGateScript(string(gateVerdict))
		skillDoc := v5AcceptTwoResourceSkillDocument(gateKey, gateScript, "unused.txt", "unused")
		publishSkillVersion(t, f.uow, "v5b-skill-def-"+caseName, "v5b-skill-v1-"+caseName, skillDoc)
		gateHash := resourceContentHash(t, "v5b-skill-v1-"+caseName, gateKey, skillDoc)
		publishCommandVersion(t, f.uow, "v5b-gate-cmd-def", "v5b-gate-cmd-v1", command.CommandDocument{
			Executable:          command.ExecutableRef{OwnerVersionID: "v5b-skill-v1-" + caseName, ResourceKey: gateKey, ContentHash: gateHash},
			Argv:                []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}},
			CwdRepositoryTarget: v5AcceptRepositoryID,
			Compatibility:       command.Compatibility{OS: []string{stdruntime.GOOS}},
			NetworkAccess:       command.NetworkAccessNone,
			TimeoutSeconds:      60,
			Output:              command.OutputContract{CaptureStdout: true, CaptureStderr: true, MaxOutputBytes: 1 << 16},
		})
		publishGateVersion(t, f.uow, "v5b-gate-def", "v5b-gate-v1", gate.GateDocument{
			CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "v5b-gate-cmd-def", VersionID: "v5b-gate-cmd-v1"},
			Criteria:   []gate.Criterion{{Name: "independent-check", EvidenceKey: v5AcceptClaimDoneEvidenceKey}},
		})
		router.Gate = f.newGateExecutor()
	}

	completionFields := publishPolicyVersion(t, f.uow, "v5b-completion-policy-def-"+caseName, "v5b-completion-policy-v1-"+caseName, policy.PolicyDocument{
		Category: policy.CategoryCompletion,
		// A withGate=false case still names a real evidence kind
		// (v5AcceptClaimDoneEvidenceKey) that NOTHING in this particular
		// document could ever produce — deliberately: the point is
		// proving the agent's own claim alone can never satisfy it, not
		// merely that an empty requirement trivially passes.
		Completion: &policy.CompletionRules{RequiredEvidenceKinds: []string{v5AcceptClaimDoneEvidenceKey}},
	})

	doc := v5AcceptClaimDoneDocument(agentBuildID, withGate)
	doc.CompletionPolicyRef = &definition.DependencyPin{Kind: definition.KindPolicy, DefinitionID: "v5b-completion-policy-def-" + caseName, VersionID: "v5b-completion-policy-v1-" + caseName}
	dependencies := workflow.DependencyManifest{Pins: []workflow.DependencyPin{
		{Kind: string(definition.KindPolicy), Key: "v5b-completion-policy-def-" + caseName, Version: "v5b-completion-policy-v1-" + caseName, Hash: completionFields.CompiledHash()},
	}}
	version := publishWorkflowVersion(t, f.uow, v5AcceptProjectID, "v5b-workflow-def-"+caseName, "v5b-workflow-v1-"+caseName, doc, dependencies)

	registry := f.registerHandlersWithAgents(router, "v5bh-"+caseName, agentRegistry)
	_, stopPool := f.startPool(t, registry)
	defer stopPool()

	// RepositoryRead, not Write: the real AGENT (fake-claude) and real Gate
	// scripts in this file never touch repo-a's own working tree at all —
	// a MACHINE_GATE is always forced read-only regardless (V5-05's own
	// forceReadOnlyMounts), and a real AGENT with no actual mutation needs
	// no WRITE grant either. Declaring WRITE anyway would make every
	// Attempt here "mutating" (resolved.hasWriteMount=true), which forces
	// classify's own quiescence check to require TreeQuiesced=true before
	// ever finalizing — a real, unrelated constraint this scenario has no
	// reason to also depend on.
	root := f.createRootWorkItem(t, caseName+"-root", workdomain.RepositoryRead)
	child := f.createChildWorkItem(t, root.WorkItemID, caseName+"-child", workdomain.RepositoryRead)

	startCmd := testCmd("v5b-start-"+caseName, ports.ProjectScope(v5AcceptProjectID), "StartWorkflowRun")
	started, err := runtime.StartWorkflowRun(ctx, f.uow, f.ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: v5AcceptProjectID, WorkItemID: child.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	runID := started.RunID

	if withGate && gateVerdict == gate.VerdictFail {
		// A real gate failing is a real NON_RETRYABLE_FAILURE — V4's own
		// existing transitionRunToFailedTx fires directly, the Run never
		// reaches VERIFYING at all (confirmed by reading completion.go —
		// nothing new built for this file). No EvaluateCompletionCandidate
		// call is even possible here; the Run's own terminal state alone
		// is the whole assertion.
		run := f.waitForRunState(t, runID, runtimedomain.WorkflowRunFailed)
		var workItem workdomain.WorkItem
		if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
			var err error
			workItem, err = tx.Work().GetWorkItem(ctx, child.WorkItemID)
			return err
		}); err != nil {
			t.Fatalf("GetWorkItem: %v", err)
		}
		return v5AcceptClaimDoneCaseResult{runSucceeded: run.State == runtimedomain.WorkflowRunSucceeded, workItemDone: workItem.Status == workdomain.WorkItemDone}
	}

	run := f.waitForRunState(t, runID, runtimedomain.WorkflowRunVerifying)
	evalCmd := testCmd("v5b-eval-"+caseName, ports.ProjectScope(v5AcceptProjectID), "EvaluateCompletionCandidate")
	evalCmd.ExpectedVersion = run.Version
	decision, err := runtime.EvaluateCompletionCandidate(ctx, f.uow, f.ids, clock.System{}, evalCmd, runtime.EvaluateCompletionCandidateRequest{RunID: runID})
	if err != nil {
		t.Fatalf("EvaluateCompletionCandidate: %v", err)
	}

	var finalRun runtimedomain.WorkflowRun
	var workItem workdomain.WorkItem
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		if finalRun, err = tx.Runtime().GetWorkflowRun(ctx, runID); err != nil {
			return err
		}
		workItem, err = tx.Work().GetWorkItem(ctx, child.WorkItemID)
		return err
	}); err != nil {
		t.Fatalf("read final state: %v", err)
	}

	if withGate {
		if decision.Outcome != runtime.CompletionOutcomePass {
			t.Fatalf("case %q: completion decision = %+v, want PASS (real independent gate evidence present)", caseName, decision)
		}
	} else {
		if decision.Outcome != runtime.CompletionOutcomeBlock {
			t.Fatalf("case %q: completion decision = %+v, want BLOCK (the agent's own claim alone must never satisfy required evidence)", caseName, decision)
		}
	}
	return v5AcceptClaimDoneCaseResult{
		runSucceeded: finalRun.State == runtimedomain.WorkflowRunSucceeded, workItemDone: workItem.Status == workdomain.WorkItemDone,
		realIndependentEvidencePresent: withGate,
	}
}
