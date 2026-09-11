// V5-15A — "Real composition": Router + executors + CompletionPolicy +
// ReleaseSet, one real multi-node Run driven to SUCCEEDED/DONE, verified
// consistent across a real process restart (docs/design/07-v5-execution-
// evidence.md V5-15; user's own binding 5-part PR split, 2026-09-11, full
// text in baocaov5checklist.md's own "V5-15" section and memory
// agent-kit-v5-15-acceptance-gate-contract.md).
//
// Every fact this test treats as "what happened" comes from a real
// production path, never a hand-crafted row (the user's own contract,
// binding for every V5-15 PR): a real workspaceprovision.Handler
// provisions a real git worktree; a real CommandNodeExecutor spawns a
// real process (a real .bat/.sh, chosen per-OS at test time — see
// v5AcceptScripts' own doc comment) that writes a real marker file; a
// real GateNodeExecutor spawns a second real process that reports PASS
// only after actually finding that marker on disk; once the Run reaches
// VERIFYING (ADR-011's own completion CANDIDATE state), a real
// gitworktree.Provider.CreateLocalCommit produces the real VCS SHAs a
// real work.CreateReleaseSet/SealReleaseSet then seals; and
// runtime.EvaluateCompletionCandidate — running here for the first time
// ever against real SQLite — is what actually decides PASS from all of
// that and moves the Run on to SUCCEEDED.
package v5accept

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	stdruntime "runtime"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	appwork "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/artifact"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// v5AcceptHappyPathDocument is the minimal real multi-node graph V5-15A's
// own contract calls for: START -> COMMAND(maker, real spawn, writes a
// real output) -> MACHINE_GATE(checker, real spawn, real PASS verdict
// derived from that output) -> END. No AGENT/WAIT/APPROVAL/FORK node —
// self-decided scope narrowing (recorded in the V5-15A research handoff):
// COMMAND+MACHINE_GATE alone already exercises NodeExecutorRouter,
// CompletionPolicy and ReleaseSet for real with no "recorded provider"
// plumbing; AGENT is genuinely needed by later V5-15 parts (B's own
// claim-done, C's own provider loss), not this one.
func v5AcceptHappyPathDocument() workflow.WorkflowDocument {
	attemptPermissionRefs := []definition.DependencyPin{
		{Kind: definition.KindPolicy, DefinitionID: "v5a-attempt-policy-def", VersionID: "v5a-attempt-policy-v1"},
		{Kind: definition.KindPolicy, DefinitionID: "v5a-permission-policy-def", VersionID: "v5a-permission-policy-v1"},
	}
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{
				Key: "test_a", Type: workflow.NodeCommand, Outcomes: []string{"passed"},
				Command: &workflow.CommandNodeConfig{
					CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "v5a-maker-cmd-def", VersionID: "v5a-maker-cmd-v1"},
					PolicyRefs: attemptPermissionRefs,
				},
			},
			{
				Key: "gate_b", Type: workflow.NodeMachineGate, Outcomes: []string{"passed"},
				MachineGate: &workflow.MachineGateNodeConfig{
					GateRef:    definition.DependencyPin{Kind: definition.KindGate, DefinitionID: "v5a-gate-def", VersionID: "v5a-gate-v1"},
					PolicyRefs: attemptPermissionRefs,
				},
			},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-test_a", From: "start", Outcome: "next", To: "test_a"},
			{Key: "test_a-gate_b", From: "test_a", Outcome: "passed", To: "gate_b"},
			{Key: "gate_b-end", From: "gate_b", Outcome: "passed", To: "end"},
		},
	}
}

func TestV5AcceptHappyPath_RealCompositionReachesSucceededAndSurvivesRestart(t *testing.T) {
	f := newV5AcceptFixture(t)
	ctx := context.Background()

	// markerPath lives outside repo-a's own git worktree entirely — a real
	// absolute path under this fixture's own root — see v5AcceptScripts'
	// own doc comment for why (GateNodeExecutor's own strict-read-only
	// diff check covers every mount the owning WorkItem has ANY scope
	// over, not just ones a gate's own script references, so a MAKER that
	// wrote inside repo-a would always fail a later MACHINE_GATE node in
	// the same Run/WorkItem, regardless of write scope or commit state).
	markerPath := filepath.Join(f.fixtureRoot, "v5a-marker.txt")
	makerKey, makerScript, gateKey, gateScript := v5AcceptScripts(markerPath)
	skillDoc := v5AcceptTwoResourceSkillDocument(makerKey, makerScript, gateKey, gateScript)
	publishSkillVersion(t, f.uow, "v5a-skill-def", "v5a-skill-v1", skillDoc)
	makerHash := resourceContentHash(t, "v5a-skill-v1", makerKey, skillDoc)
	gateHash := resourceContentHash(t, "v5a-skill-v1", gateKey, skillDoc)

	osCompat := command.Compatibility{OS: []string{stdruntime.GOOS}}

	publishCommandVersion(t, f.uow, "v5a-maker-cmd-def", "v5a-maker-cmd-v1", command.CommandDocument{
		Executable:          command.ExecutableRef{OwnerVersionID: "v5a-skill-v1", ResourceKey: makerKey, ContentHash: makerHash},
		Argv:                []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}},
		CwdRepositoryTarget: v5AcceptRepositoryID,
		Compatibility:       osCompat,
		NetworkAccess:       command.NetworkAccessNone,
		TimeoutSeconds:      60,
		Output:              command.OutputContract{CaptureStdout: true, CaptureStderr: true, MaxOutputBytes: 1 << 16},
	})

	// gate_b's own underlying Command: cwdRepositoryTarget is schema-
	// required (command.ValidateDocument) but never actually consulted for
	// a Gate (GateNodeExecutor always spawns into a fresh scratch
	// directory instead — see that file's own package doc comment for
	// why), so this only needs to name a repository actually in scope.
	publishCommandVersion(t, f.uow, "v5a-gate-cmd-def", "v5a-gate-cmd-v1", command.CommandDocument{
		Executable:          command.ExecutableRef{OwnerVersionID: "v5a-skill-v1", ResourceKey: gateKey, ContentHash: gateHash},
		Argv:                []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}},
		CwdRepositoryTarget: v5AcceptRepositoryID,
		Compatibility:       osCompat,
		NetworkAccess:       command.NetworkAccessNone,
		TimeoutSeconds:      60,
		Output:              command.OutputContract{CaptureStdout: true, CaptureStderr: true, MaxOutputBytes: 1 << 16},
	})

	publishGateVersion(t, f.uow, "v5a-gate-def", "v5a-gate-v1", gate.GateDocument{
		CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "v5a-gate-cmd-def", VersionID: "v5a-gate-cmd-v1"},
		Criteria:   []gate.Criterion{{Name: "output-verified", EvidenceKey: v5AcceptGateEvidenceKey}},
	})

	publishPolicyVersion(t, f.uow, "v5a-attempt-policy-def", "v5a-attempt-policy-v1", v5AcceptAttemptPolicyDocument(60))
	publishPolicyVersion(t, f.uow, "v5a-permission-policy-def", "v5a-permission-policy-v1", v5AcceptPermissionPolicyDocument())

	// The completion policy requires BOTH the maker's own fixed
	// COMMAND_EXECUTION evidence kind AND the gate's own self-chosen
	// EvidenceKey — loadLatestReleaseSetGate's own doc comment already
	// warns that a family with zero ReleaseSets defaults the release gate
	// to satisfied=true, so this test still creates+seals a real one below
	// to actually exercise that path, never relying on the default.
	completionFields := publishPolicyVersion(t, f.uow, "v5a-completion-policy-def", "v5a-completion-policy-v1", policy.PolicyDocument{
		Category:   policy.CategoryCompletion,
		Completion: &policy.CompletionRules{RequiredEvidenceKinds: []string{runtimedomain.EvidenceKindCommandExecution, v5AcceptGateEvidenceKey}},
	})

	doc := v5AcceptHappyPathDocument()
	doc.CompletionPolicyRef = &definition.DependencyPin{Kind: definition.KindPolicy, DefinitionID: "v5a-completion-policy-def", VersionID: "v5a-completion-policy-v1"}
	dependencies := workflow.DependencyManifest{Pins: []workflow.DependencyPin{
		{Kind: string(definition.KindPolicy), Key: "v5a-completion-policy-def", Version: "v5a-completion-policy-v1", Hash: completionFields.CompiledHash()},
	}}
	version := publishWorkflowVersion(t, f.uow, v5AcceptProjectID, "v5a-workflow-def", "v5a-workflow-v1", doc, dependencies)

	router := &runtime.NodeExecutorRouter{Command: f.newCommandExecutor(), Gate: f.newGateExecutor()}
	registry := f.registerHandlers(router, "v5ah")
	_, stopPool := f.startPool(t, registry)
	defer stopPool()

	// A real ROOT WorkItem only anchors the TaskFamily/WorkspaceSet and
	// the family's own approved RepositoryScope grant (CreateRootWorkItem's
	// own InitialScope) — it deliberately never gains real EffectiveScope
	// itself (see createChildWorkItem's own doc comment). The Run this
	// test actually drives runs against a real CHILD WorkItem instead,
	// created via the one production command that does populate real
	// EffectiveScope without needing an AGENT-driven scope-expansion cycle.
	root := f.createRootWorkItem(t, "happy-path-root", workdomain.RepositoryWrite)
	child := f.createChildWorkItem(t, root.WorkItemID, "happy-path-child", workdomain.RepositoryWrite)

	baseHandle := f.repositoryWorkspaceHandle(t, root.WorkspaceSetID, v5AcceptRepositoryID, 1)
	baseRevision, err := f.provider.CaptureRevision(ctx, baseHandle)
	if err != nil {
		t.Fatalf("CaptureRevision (base): %v", err)
	}

	startCmd := testCmd("v5a-start", ports.ProjectScope(v5AcceptProjectID), "StartWorkflowRun")
	started, err := runtime.StartWorkflowRun(ctx, f.uow, f.ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: v5AcceptProjectID, WorkItemID: child.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	runID := started.RunID

	// The running pool alone drives start->test_a->gate_b->end — real
	// SCHEDULE_NODE_RUN/EXECUTE_NODE jobs, real CommandNodeExecutor/
	// GateNodeExecutor spawns, real evidence — all the way to VERIFYING
	// (ADR-011's own completion CANDIDATE state); nothing here manually
	// advances a NodeRun.
	run := f.waitForRunState(t, runID, runtimedomain.WorkflowRunVerifying)

	// repo-a's own working tree is still genuinely untouched at this point
	// (the maker's own real output lives entirely outside it — see
	// v5AcceptScripts' own doc comment for why) — a real, independent
	// real-world "release preparation" write, right here, is what gives
	// gitworktree.Provider.CreateLocalCommit (V5-10A's own typed local Git
	// operation) something real to commit. Nothing else in this Run reads
	// or re-pins a revision after VERIFYING (no more NodeRuns are ever
	// scheduled for a Run that has already reached its own completion
	// CANDIDATE state), so this is safe: unlike a commit attempted WHILE
	// gate_b could still run, it can never trip GateNodeExecutor's own
	// strict-read-only diff check.
	workingDirectory, err := f.provider.WorkingDirectory(ctx, baseHandle)
	if err != nil {
		t.Fatalf("WorkingDirectory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workingDirectory, "release-note.txt"), []byte("v5-15a happy path\n"), 0o600); err != nil {
		t.Fatalf("write release note: %v", err)
	}
	resultRevision, err := f.provider.CreateLocalCommit(ctx, ports.CreateLocalCommitRequest{
		Handle: baseHandle, Message: "v5-15a release note", AuthorName: "Agent Kit Test", AuthorEmail: "agent-kit@example.invalid",
	})
	if err != nil {
		t.Fatalf("CreateLocalCommit: %v", err)
	}
	if resultRevision.VCSObjectID == baseRevision.VCSObjectID {
		t.Fatalf("result revision %s did not change from base revision %s", resultRevision.VCSObjectID, baseRevision.VCSObjectID)
	}

	releaseCmd := testCmd("v5a-release-create", ports.ProjectScope(v5AcceptProjectID), "CreateReleaseSet")
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
	sealCmd := testCmd("v5a-release-seal", ports.ProjectScope(v5AcceptProjectID), "SealReleaseSet")
	sealCmd.ExpectedVersion = releaseResult.Version
	sealResult, err := appwork.SealReleaseSet(ctx, f.uow, sealCmd, appwork.SealReleaseSetRequest{ReleaseSetID: releaseResult.ReleaseSetID})
	if err != nil {
		t.Fatalf("SealReleaseSet: %v", err)
	}
	if sealResult.State != string(workdomain.ReleaseSetSealed) {
		t.Fatalf("release set state = %s, want SEALED", sealResult.State)
	}

	evalCmd := testCmd("v5a-eval", ports.ProjectScope(v5AcceptProjectID), "EvaluateCompletionCandidate")
	evalCmd.ExpectedVersion = run.Version
	decision, err := runtime.EvaluateCompletionCandidate(ctx, f.uow, f.ids, clock.System{}, evalCmd, runtime.EvaluateCompletionCandidateRequest{RunID: runID})
	if err != nil {
		t.Fatalf("EvaluateCompletionCandidate: %v", err)
	}
	if decision.Outcome != runtime.CompletionOutcomePass {
		t.Fatalf("completion decision = %+v, want PASS", decision)
	}

	// Checked before AND after a real restart, through the identical
	// assertion helper each time, so a restart-only regression can never
	// hide behind a pre-restart check that used different logic.
	f.assertHappyPathState(t, child.WorkItemID, runID, decision.DecisionArtifactID, releaseResult.ReleaseSetID)

	stopPool()
	f.restart(t)

	f.assertHappyPathState(t, child.WorkItemID, runID, decision.DecisionArtifactID, releaseResult.ReleaseSetID)
}

// assertHappyPathState re-reads WorkItem/WorkflowRun/every NodeRun/the
// DecisionArtifact/its own COMPLETION_DECIDED event/the ReleaseSet, plus
// every Evidence row's own referenced Artifact re-verified against the
// real ArtifactStore — everything the user's own PR A contract names
// ("Run, WorkItem, DecisionArtifact, events và ReleaseSet vẫn nhất
// quán"). Reads entirely through f.uow/f.store, whichever real store is
// current (pre-restart or freshly reopened) — the caller decides when to
// call this, this helper never assumes which.
func (f *v5AcceptFixture) assertHappyPathState(t *testing.T, workItemID, runID, decisionArtifactID, releaseSetID string) {
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
		t.Fatalf("read happy-path state: %v", err)
	}

	if run.State != runtimedomain.WorkflowRunSucceeded {
		t.Fatalf("run.State = %s, want SUCCEEDED", run.State)
	}
	if workItem.Status != workdomain.WorkItemDone {
		t.Fatalf("workItem.Status = %s, want DONE", workItem.Status)
	}
	if releaseSet.State != workdomain.ReleaseSetSealed {
		t.Fatalf("releaseSet.State = %s, want SEALED", releaseSet.State)
	}
	if decisionArtifact.Kind != runtime.CompletionDecisionKind {
		t.Fatalf("decision artifact kind = %s, want %s", decisionArtifact.Kind, runtime.CompletionDecisionKind)
	}

	nodeRunByKey := make(map[string]runtimedomain.NodeRun, len(nodeRuns))
	for _, nr := range nodeRuns {
		nodeRunByKey[nr.NodeKey] = nr
	}
	for _, key := range []string{"start", "test_a", "gate_b", "end"} {
		nr, ok := nodeRunByKey[key]
		if !ok {
			t.Fatalf("no NodeRun found for %q", key)
		}
		if nr.State != runtimedomain.NodeRunSucceeded {
			t.Fatalf("NodeRun %q state = %s, want SUCCEEDED", key, nr.State)
		}
	}

	f.assertEvidenceArtifact(t, runID, nodeRunByKey["test_a"].ID, runtimedomain.EvidenceKindCommandExecution)
	f.assertEvidenceArtifact(t, runID, nodeRunByKey["gate_b"].ID, v5AcceptGateEvidenceKey)

	events, err := f.store.ListDomainEventsForProject(ctx, v5AcceptProjectID)
	if err != nil {
		t.Fatalf("ListDomainEventsForProject: %v", err)
	}
	found := false
	for _, e := range events {
		if e.EventType == runtime.CompletionDecidedEventType && e.AggregateID == runID {
			found = true
		}
	}
	if !found {
		t.Fatal("no COMPLETION_DECIDED event found for this run")
	}
}

// assertEvidenceArtifact re-reads nodeRunID's own ExecutionAttempt and its
// own Evidence row of kind, then re-verifies every one of that Evidence's
// own ArtifactReferences against the real ArtifactStore (a real
// SHA256/size check against whatever content is actually on disk right
// now) — never trusting a DecisionArtifact's bare say-so that Evidence
// existed at decision time.
func (f *v5AcceptFixture) assertEvidenceArtifact(t *testing.T, runID string, nodeRunID runtimedomain.NodeRunID, kind string) {
	t.Helper()
	ctx := context.Background()

	var evidence []runtimedomain.Evidence
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		attempts, err := tx.Runtime().ListExecutionAttemptsForRun(ctx, runID)
		if err != nil {
			return err
		}
		var attemptID string
		for _, a := range attempts {
			if a.NodeRunID == nodeRunID {
				attemptID = string(a.ID)
			}
		}
		if attemptID == "" {
			return fmt.Errorf("no ExecutionAttempt found for node run %s", nodeRunID)
		}
		evidence, err = tx.Runtime().ListEvidenceForAttempt(ctx, attemptID)
		return err
	}); err != nil {
		t.Fatalf("load evidence for node run %s: %v", nodeRunID, err)
	}

	var match *runtimedomain.Evidence
	for i := range evidence {
		if evidence[i].Kind == kind {
			match = &evidence[i]
		}
	}
	if match == nil {
		t.Fatalf("no Evidence of kind %s found for node run %s", kind, nodeRunID)
	}
	if match.Verdict != runtimedomain.EvidenceVerdictSucceeded && match.Verdict != string(gate.VerdictPass) {
		t.Fatalf("evidence %s verdict = %s, want a passing verdict", match.ID, match.Verdict)
	}
	if len(match.ArtifactReferences) == 0 {
		t.Fatalf("evidence %s has no artifact references", match.ID)
	}

	var artifactRow artifact.Artifact
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		artifactRow, err = tx.Artifacts().GetArtifact(ctx, match.ArtifactReferences[0])
		return err
	}); err != nil {
		t.Fatalf("GetArtifact(%s): %v", match.ArtifactReferences[0], err)
	}
	if err := f.artifacts.Verify(ctx, ports.ArtifactRef{
		Locator: artifactRow.Locator, SHA256: artifactRow.ContentHash, Size: artifactRow.Size, ContentType: artifactRow.MediaType,
	}); err != nil {
		t.Fatalf("Verify artifact %s: %v", artifactRow.ID, err)
	}
}
