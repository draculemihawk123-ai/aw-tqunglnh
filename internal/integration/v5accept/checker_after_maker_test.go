// V9-01 — "Checker và gate đánh giá được thay đổi của maker trong cùng run"
// (docs/design/12-v9-harness-alignment.md V9-01; ADR-030 in
// docs/architecture/02-architecture-decisions.md; gap G1 in
// docs/harness-engineering/15-doi-chieu-v9.md).
//
// Before V9-01 a CHECKER-role AGENT or a MACHINE_GATE placed AFTER a MAKER in
// the same run ALWAYS failed with SCOPE_VIOLATION: read-only was measured as
// "the diff against the pinned commit is empty", and the maker's changes stay
// uncommitted in the worktree until ReleaseSet/local commit (ADR-014). The
// scenarios here drive the real thing end to end — the fake provider CLI as a
// real process that really writes a file in the real git worktree, a real
// gitworktree.Provider snapshotting real tree objects, a real sqlite store, a
// real worker pool — and prove "read-only" now means "changed nothing since
// THIS attempt started":
//
//   - maker writes a file, checker changes nothing: the run completes (the
//     G1 regression — this fails on a build without ADR-030);
//   - maker writes a file, checker changes a file again: still
//     SCOPE_VIOLATION;
//   - maker writes a file, a MACHINE_GATE whose evaluator reads that very
//     file: the verdict reflects the NEW content (PASS), and a gate after a
//     maker that wrote nothing still reports FAIL.
//
// The maker and the checker are two processes of the same fake CLI sharing
// one inherited environment, so the writes are steered per node: the engine
// spawns a MAKER in its repository mount (AGENTKIT_HELPER_WRITE_IN_CWD makes
// it change a file there) and a CHECKER in an empty scratch directory (the
// same write lands outside every mount), see providers/fixtures.go.
package v5accept

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	stdruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

const (
	// v9MakerWrittenFile is the tracked file (committed as "v0\n" in the
	// fixture repository) the fake maker rewrites inside its repository
	// mount, and the file the gate evaluator reads.
	v9MakerWrittenFile = "service.txt"
	// v9MakerContentMarker is the first line the fake CLI writes
	// (providers/fixtures.go AGENTKIT_HELPER_WRITE_IN_CWD must write exactly
	// this line); the gate evaluator compares the file's first line to it.
	v9MakerContentMarker = "written by fake CLI in its working directory"
	// v9GateEvidenceKey is the one criterion of the gate scenario.
	v9GateEvidenceKey = "MAKER_OUTPUT_PRESENT"
	// v9GateTimeoutSeconds is the evaluator's command timeout. It is a
	// ceiling, not an expectation: the script returns as soon as it has read
	// one file.
	v9GateTimeoutSeconds = 120
)

// v9AgentSetup holds what every scenario here needs to run real AGENT nodes.
type v9AgentSetup struct {
	buildID  string
	registry *agentregistry.Registry
	executor *runtime.AgentNodeExecutor
}

// newV9AgentSetup publishes the policies and agent profile the AGENT nodes
// pin and registers the real fake provider CLI's adapter build — the wiring
// checker_write_test.go already uses, shared by this file's scenarios.
func newV9AgentSetup(t *testing.T, f *v5AcceptFixture) v9AgentSetup {
	t.Helper()
	adapter := f.newClaudeAdapter(t)
	agentBuildID := f.registerAgentBuild(t, adapter)
	agentRegistry := f.newAgentRegistry(t, adapter)

	publishPolicyVersion(t, f.uow, "v9-context-policy-def", "v9-context-policy-v1", v5AcceptContextPolicyDocument())
	publishAgentProfileVersion(t, f.uow, "v9-agent-profile-def", "v9-agent-profile-v1", "v9-context-policy-def", "v9-context-policy-v1")
	publishPolicyVersion(t, f.uow, "v9-attempt-policy-def", "v9-attempt-policy-v1", v5AcceptAttemptPolicyDocument(120))
	publishPolicyVersion(t, f.uow, "v9-permission-policy-def", "v9-permission-policy-v1", v5AcceptDriftPermissionPolicyDocument())
	return v9AgentSetup{buildID: agentBuildID, registry: agentRegistry, executor: f.newAgentExecutor(agentRegistry)}
}

func v9PolicyRefs() []definition.DependencyPin {
	return []definition.DependencyPin{
		{Kind: definition.KindPolicy, DefinitionID: "v9-attempt-policy-def", VersionID: "v9-attempt-policy-v1"},
		{Kind: definition.KindPolicy, DefinitionID: "v9-permission-policy-def", VersionID: "v9-permission-policy-v1"},
	}
}

func v9AgentNode(key, agentBuildID string, role workflow.AgentRole, outcome string) workflow.Node {
	buildID := agentBuildID
	return workflow.Node{
		Key: key, Type: workflow.NodeAgent, Outcomes: []string{outcome},
		Agent: &workflow.AgentNodeConfig{
			ProfileRef:     definition.DependencyPin{Kind: definition.KindAgentProfile, DefinitionID: "v9-agent-profile-def", VersionID: "v9-agent-profile-v1"},
			PolicyRefs:     v9PolicyRefs(),
			AdapterBuildID: &buildID,
			Role:           role,
		},
	}
}

// v9MakerCheckerDocument is the G1 workflow: implement (MAKER) -> review
// (CHECKER) -> end.
func v9MakerCheckerDocument(agentBuildID string) workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			v9AgentNode("implement", agentBuildID, workflow.AgentRoleMaker, "done"),
			v9AgentNode("review", agentBuildID, workflow.AgentRoleChecker, "done"),
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-implement", From: "start", Outcome: "next", To: "implement"},
			{Key: "implement-review", From: "implement", Outcome: "done", To: "review"},
			{Key: "review-end", From: "review", Outcome: "done", To: "end"},
		},
	}
}

// v9MakerGateDocument is implement (MAKER) -> verify (MACHINE_GATE) -> end.
func v9MakerGateDocument(agentBuildID string) workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			v9AgentNode("implement", agentBuildID, workflow.AgentRoleMaker, "done"),
			{
				Key: "verify", Type: workflow.NodeMachineGate, Outcomes: []string{"passed"},
				MachineGate: &workflow.MachineGateNodeConfig{
					GateRef:    definition.DependencyPin{Kind: definition.KindGate, DefinitionID: "v9-gate-def", VersionID: "v9-gate-v1"},
					PolicyRefs: v9PolicyRefs(),
				},
			},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-implement", From: "start", Outcome: "next", To: "implement"},
			{Key: "implement-verify", From: "implement", Outcome: "done", To: "verify"},
			{Key: "verify-end", From: "verify", Outcome: "passed", To: "end"},
		},
	}
}

// v9GateScript is a real evaluator that reports PASS only if the first line of
// the file service.txt of the repository passed as its second argument is the
// maker's marker — i.e. only if it really reads the content the maker wrote.
// argv is ["run", <repository working directory>]. It uses shell builtins
// only: the evaluator is spawned with no inherited environment (no PATH), so
// an external tool such as grep or findstr is not something it can rely on.
func v9GateScript() (key, script string) {
	if stdruntime.GOOS == "windows" {
		return "gate-reads-maker.bat", "@echo off\r\n" +
			"set /p LINE=<\"%~2\\" + v9MakerWrittenFile + "\"\r\n" +
			"if \"%LINE%\"==\"" + v9MakerContentMarker + "\" goto pass\r\n" +
			"echo {\"" + v9GateEvidenceKey + "\":{\"verdict\":\"FAIL\"}}\r\n" +
			"exit /b 0\r\n" +
			":pass\r\n" +
			"echo {\"" + v9GateEvidenceKey + "\":{\"verdict\":\"PASS\"}}\r\n" +
			"exit /b 0\r\n"
	}
	return "gate-reads-maker.sh", "#!/bin/sh\n" +
		"line=\n" +
		"read line < \"$2/" + v9MakerWrittenFile + "\"\n" +
		"if [ \"$line\" = \"" + v9MakerContentMarker + "\" ]; then\n" +
		"  echo '{\"" + v9GateEvidenceKey + "\":{\"verdict\":\"PASS\"}}'\n" +
		"else\n" +
		"  echo '{\"" + v9GateEvidenceKey + "\":{\"verdict\":\"FAIL\"}}'\n" +
		"fi\n" +
		"exit 0\n"
}

// publishV9Gate publishes the gate (and the command + skill it needs).
func publishV9Gate(t *testing.T, f *v5AcceptFixture) {
	t.Helper()
	gateKey, gateScript := v9GateScript()
	skillDoc := v5AcceptTwoResourceSkillDocument(gateKey, gateScript, "unused.txt", "unused")
	publishSkillVersion(t, f.uow, "v9-skill-def", "v9-skill-v1", skillDoc)
	gateHash := resourceContentHash(t, "v9-skill-v1", gateKey, skillDoc)
	publishCommandVersion(t, f.uow, "v9-gate-cmd-def", "v9-gate-cmd-v1", command.CommandDocument{
		Executable: command.ExecutableRef{OwnerVersionID: "v9-skill-v1", ResourceKey: gateKey, ContentHash: gateHash},
		Argv: []command.ArgvElement{
			{Kind: command.ArgvLiteral, Value: "run"},
			{Kind: command.ArgvPlaceholder, Value: v5AcceptRepositoryID},
		},
		PlaceholderAllowlist: []string{v5AcceptRepositoryID},
		CwdRepositoryTarget:  v5AcceptRepositoryID,
		Compatibility:        command.Compatibility{OS: []string{stdruntime.GOOS}},
		NetworkAccess:        command.NetworkAccessNone,
		TimeoutSeconds:       v9GateTimeoutSeconds,
		Output:               command.OutputContract{CaptureStdout: true, CaptureStderr: true, MaxOutputBytes: 1 << 16},
	})
	publishGateVersion(t, f.uow, "v9-gate-def", "v9-gate-v1", gate.GateDocument{
		CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "v9-gate-cmd-def", VersionID: "v9-gate-cmd-v1"},
		Criteria:   []gate.Criterion{{Name: "maker-output-present", EvidenceKey: v9GateEvidenceKey}},
	})
}

// v9Run is everything a scenario needs to know about the run it drove.
type v9Run struct {
	runID    string
	workDir  string // the repository mount's real working directory
	attempts map[string]runtimedomain.ExecutionAttempt
	nodeRuns map[string]runtimedomain.NodeRun
}

// startV9Run starts the pool, creates the WorkItem, starts the run and waits
// for want. beforeStart, if non-nil, runs once the repository mount exists
// and before the run starts, receiving the mount's real working directory (a
// scenario that must point the fake CLI at a path inside it sets its
// environment here). attempts/nodeRuns are keyed by node key.
func startV9Run(
	t *testing.T, f *v5AcceptFixture, router *runtime.NodeExecutorRouter, agents *agentregistry.Registry,
	version workflow.WorkflowVersion, idPrefix string, want runtimedomain.WorkflowRunState, beforeStart func(workDir string),
) v9Run {
	t.Helper()
	ctx := context.Background()
	registry := f.registerHandlersWithAgents(router, idPrefix, agents)
	// A generous lease: nothing here is about lease expiry, and the executors
	// run real git commands between heartbeats.
	_, stopPool := f.startPoolWithConfig(t, registry, workerpool.Config{
		Concurrency: 1, Owner: "v9-accept", LeaseTTL: 60 * time.Second,
		HeartbeatEvery: time.Second, PollInterval: 10 * time.Millisecond,
		ShutdownGrace: 5 * time.Second, RecoveryInterval: time.Second,
	})
	defer stopPool()

	root := f.createRootWorkItem(t, "v9-root", workdomain.RepositoryWrite)
	child := f.createChildWorkItem(t, root.WorkItemID, "v9-child", workdomain.RepositoryWrite)
	baseHandle := f.repositoryWorkspaceHandle(t, root.WorkspaceSetID, v5AcceptRepositoryID, 1)
	workDir, err := f.provider.WorkingDirectory(ctx, baseHandle)
	if err != nil {
		t.Fatalf("WorkingDirectory: %v", err)
	}
	if beforeStart != nil {
		beforeStart(workDir)
	}

	startCmd := testCmd("v9-start-"+idPrefix, ports.ProjectScope(v5AcceptProjectID), "StartWorkflowRun")
	started, err := runtime.StartWorkflowRun(ctx, f.uow, f.ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: v5AcceptProjectID, WorkItemID: child.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	f.waitForRunState(t, started.RunID, want)
	stopPool()

	result := v9Run{runID: started.RunID, workDir: workDir, attempts: map[string]runtimedomain.ExecutionAttempt{}, nodeRuns: map[string]runtimedomain.NodeRun{}}
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		nodeRuns, err := tx.Runtime().ListNodeRunsForRun(ctx, started.RunID)
		if err != nil {
			return err
		}
		keyByNodeRun := map[runtimedomain.NodeRunID]string{}
		for _, nr := range nodeRuns {
			result.nodeRuns[nr.NodeKey] = nr
			keyByNodeRun[nr.ID] = nr.NodeKey
		}
		attempts, err := tx.Runtime().ListExecutionAttemptsForRun(ctx, started.RunID)
		if err != nil {
			return err
		}
		for _, a := range attempts {
			result.attempts[keyByNodeRun[a.NodeRunID]] = a
		}
		return nil
	}); err != nil {
		t.Fatalf("read final run state: %v", err)
	}
	return result
}

func requireAttemptState(t *testing.T, run v9Run, nodeKey string, want runtimedomain.ExecutionAttemptState) runtimedomain.ExecutionAttempt {
	t.Helper()
	attempt, ok := run.attempts[nodeKey]
	if !ok {
		t.Fatalf("no ExecutionAttempt for node %q; attempts=%+v", nodeKey, run.attempts)
	}
	if attempt.State != want {
		t.Fatalf("attempt of node %q state = %s (reason %s, code %s), want %s", nodeKey, attempt.State, attempt.TerminationReason, attempt.FailureCode, want)
	}
	return attempt
}

// TestV9AcceptCheckerAfterMaker_CompletesAndCheckerSeesMakerDiff is the G1
// regression: implement (MAKER, really writes service.txt) -> review
// (CHECKER, changes nothing) -> end must complete. On a build without
// ADR-030 the review attempt fails with SCOPE_VIOLATION (the maker's
// uncommitted change shows in its diff), the run FAILS, and this test never
// sees VERIFYING.
func TestV9AcceptCheckerAfterMaker_CompletesAndCheckerSeesMakerDiff(t *testing.T) {
	f := newV5AcceptFixture(t)
	ctx := context.Background()

	t.Setenv("AGENTKIT_HELPER_MODE", "outcome-success")
	t.Setenv("AGENTKIT_HELPER_OUTCOME", "done")
	t.Setenv("AGENTKIT_HELPER_WRITE_IN_CWD", v9MakerWrittenFile)

	agents := newV9AgentSetup(t, f)
	version := publishWorkflowVersion(t, f.uow, v5AcceptProjectID, "v9-mc-workflow-def", "v9-mc-workflow-v1", v9MakerCheckerDocument(agents.buildID), workflow.DependencyManifest{})
	router := &runtime.NodeExecutorRouter{Agent: agents.executor}

	run := startV9Run(t, f, router, agents.registry, version, "v9mc", runtimedomain.WorkflowRunVerifying, nil)

	for _, key := range []string{"implement", "review"} {
		if run.nodeRuns[key].State != runtimedomain.NodeRunSucceeded {
			t.Fatalf("node %q state = %s, want SUCCEEDED", key, run.nodeRuns[key].State)
		}
	}
	maker := requireAttemptState(t, run, "implement", runtimedomain.ExecutionAttemptSucceeded)
	checker := requireAttemptState(t, run, "review", runtimedomain.ExecutionAttemptSucceeded)
	if len(run.attempts) != 2 {
		t.Fatalf("attempts = %d, want exactly one per agent node (no retry)", len(run.attempts))
	}

	// The maker really changed the file inside the real worktree and left it
	// uncommitted — the situation that used to fail the checker.
	if content := runV5AcceptGit(t, run.workDir, "cat-file", "-p", ":"+v9MakerWrittenFile); strings.Contains(content, v9MakerContentMarker) {
		t.Fatalf("test setup: the maker's change must stay uncommitted/unstaged, but the index holds %q", content)
	}
	if status := runV5AcceptGit(t, run.workDir, "status", "--porcelain=v1"); !strings.Contains(status, v9MakerWrittenFile) {
		t.Fatalf("git status = %q, want the maker's uncommitted change to %s", status, v9MakerWrittenFile)
	}

	// The checker's InputTree was recorded before it ran, and it captured the
	// maker's uncommitted content; the maker (not read-only) records none.
	if len(maker.InputTrees) != 0 {
		t.Fatalf("maker attempt recorded InputTrees %v; only read-only attempts snapshot", maker.InputTrees)
	}
	inputTree := checker.InputTrees[v5AcceptRepositoryID]
	if inputTree == "" {
		t.Fatalf("checker attempt InputTrees = %v, want a tree for %s", checker.InputTrees, v5AcceptRepositoryID)
	}
	if content := runV5AcceptGit(t, run.workDir, "cat-file", "-p", inputTree+":"+v9MakerWrittenFile); !strings.Contains(content, v9MakerContentMarker) {
		t.Fatalf("checker InputTree holds %s = %q, want the maker's uncommitted content", v9MakerWrittenFile, content)
	}

	// V9-01 "evidence của checker trỏ tới diff manifest của maker": the
	// checker's ContextSnapshot names the maker's Evidence row, and that row
	// references the maker's diff manifest, which lists the changed file.
	var snapshotEvidenceIDs []string
	var makerEvidence []runtimedomain.Evidence
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		snapshot, err := tx.ContextSnapshots().GetSnapshotByAttemptID(ctx, string(checker.ID))
		if err != nil {
			return err
		}
		for _, ref := range snapshot.EvidenceRefs {
			snapshotEvidenceIDs = append(snapshotEvidenceIDs, ref.EvidenceID)
		}
		makerEvidence, err = tx.Runtime().ListEvidenceForAttempt(ctx, string(maker.ID))
		return err
	}); err != nil {
		t.Fatalf("read checker snapshot / maker evidence: %v", err)
	}
	if len(makerEvidence) != 1 || makerEvidence[0].Kind != runtimedomain.EvidenceKindAgentExecution {
		t.Fatalf("maker evidence = %+v, want exactly one %s row", makerEvidence, runtimedomain.EvidenceKindAgentExecution)
	}
	if makerEvidence[0].Verdict == runtimedomain.EvidenceVerdictSucceeded || makerEvidence[0].Verdict == string(gate.VerdictPass) {
		t.Fatalf("maker evidence verdict = %s: an agent's own record must never be a passing verdict completion accepts", makerEvidence[0].Verdict)
	}
	found := false
	for _, id := range snapshotEvidenceIDs {
		if id == string(makerEvidence[0].ID) {
			found = true
		}
	}
	if !found {
		t.Fatalf("checker snapshot evidence refs = %v, want to include the maker's evidence %s", snapshotEvidenceIDs, makerEvidence[0].ID)
	}
	manifestFiles := f.readDiffManifestPaths(t, makerEvidence[0].ArtifactReferences)
	if !containsString(manifestFiles, v9MakerWrittenFile) {
		t.Fatalf("maker's diff manifest files = %v, want %s", manifestFiles, v9MakerWrittenFile)
	}
}

// TestV9AcceptCheckerAfterMaker_CheckerWriteIsScopeViolation: the maker's
// uncommitted change is no longer charged to the checker, but a change the
// CHECKER itself makes still is. Both processes append a line to the same
// repository file; the checker's append makes its output tree differ from its
// input tree.
func TestV9AcceptCheckerAfterMaker_CheckerWriteIsScopeViolation(t *testing.T) {
	f := newV5AcceptFixture(t)

	t.Setenv("AGENTKIT_HELPER_MODE", "outcome-success")
	t.Setenv("AGENTKIT_HELPER_OUTCOME", "done")

	agents := newV9AgentSetup(t, f)
	version := publishWorkflowVersion(t, f.uow, v5AcceptProjectID, "v9-cw-workflow-def", "v9-cw-workflow-v1", v9MakerCheckerDocument(agents.buildID), workflow.DependencyManifest{})
	router := &runtime.NodeExecutorRouter{Agent: agents.executor}

	run := startV9Run(t, f, router, agents.registry, version, "v9cw", runtimedomain.WorkflowRunFailed, func(workDir string) {
		t.Setenv("AGENTKIT_HELPER_APPEND_PATH", filepath.Join(workDir, "touched-by-both.txt"))
	})

	if len(run.attempts) != 2 {
		t.Fatalf("attempts = %d, want 2 (maker succeeded, checker's scope violation is non-retryable)", len(run.attempts))
	}
	requireAttemptState(t, run, "implement", runtimedomain.ExecutionAttemptSucceeded)
	checker := requireAttemptState(t, run, "review", runtimedomain.ExecutionAttemptFailed)
	if checker.TerminationReason != runtimedomain.TerminationReasonScopeViolation || checker.FailureCode != errorcode.CodeScopeViolation {
		t.Fatalf("checker attempt = reason %s code %s, want SCOPE_VIOLATION/SCOPE_VIOLATION", checker.TerminationReason, checker.FailureCode)
	}
	if checker.InputTrees[v5AcceptRepositoryID] == "" {
		t.Fatalf("checker attempt InputTrees = %v, want the tree recorded before it ran", checker.InputTrees)
	}
}

// TestV9AcceptGateAfterMaker_VerdictReflectsMakersChange: implement (MAKER,
// really rewrites service.txt) -> verify (MACHINE_GATE whose evaluator reads
// that file). The gate must run (it used to fail with SCOPE_VIOLATION) and
// its verdict must come from the NEW content: PASS when the maker wrote the
// marker, FAIL when the maker wrote nothing and the file still holds "v0".
func TestV9AcceptGateAfterMaker_VerdictReflectsMakersChange(t *testing.T) {
	t.Run("maker wrote the file -> gate PASSES and the run completes", func(t *testing.T) {
		f := newV5AcceptFixture(t)
		t.Setenv("AGENTKIT_HELPER_MODE", "outcome-success")
		t.Setenv("AGENTKIT_HELPER_OUTCOME", "done")
		t.Setenv("AGENTKIT_HELPER_WRITE_IN_CWD", v9MakerWrittenFile)

		agents := newV9AgentSetup(t, f)
		publishV9Gate(t, f)
		version := publishWorkflowVersion(t, f.uow, v5AcceptProjectID, "v9-mg-workflow-def", "v9-mg-workflow-v1", v9MakerGateDocument(agents.buildID), workflow.DependencyManifest{})
		router := &runtime.NodeExecutorRouter{Agent: agents.executor, Gate: f.newGateExecutor()}

		run := startV9Run(t, f, router, agents.registry, version, "v9mg", runtimedomain.WorkflowRunVerifying, nil)

		requireAttemptState(t, run, "implement", runtimedomain.ExecutionAttemptSucceeded)
		gateAttempt := requireAttemptState(t, run, "verify", runtimedomain.ExecutionAttemptSucceeded)
		if gateAttempt.InputTrees[v5AcceptRepositoryID] == "" {
			t.Fatalf("gate attempt InputTrees = %v, want the tree recorded before the evaluator ran", gateAttempt.InputTrees)
		}
		// The recorded verdict is the evaluator's PASS for the criterion,
		// derived from the maker's content on disk.
		f.assertEvidenceArtifact(t, run.runID, run.nodeRuns["verify"].ID, v9GateEvidenceKey)
	})

	t.Run("maker wrote nothing -> the same gate FAILS", func(t *testing.T) {
		f := newV5AcceptFixture(t)
		t.Setenv("AGENTKIT_HELPER_MODE", "outcome-success")
		t.Setenv("AGENTKIT_HELPER_OUTCOME", "done")

		agents := newV9AgentSetup(t, f)
		publishV9Gate(t, f)
		version := publishWorkflowVersion(t, f.uow, v5AcceptProjectID, "v9-mg0-workflow-def", "v9-mg0-workflow-v1", v9MakerGateDocument(agents.buildID), workflow.DependencyManifest{})
		router := &runtime.NodeExecutorRouter{Agent: agents.executor, Gate: f.newGateExecutor()}

		run := startV9Run(t, f, router, agents.registry, version, "v9mg0", runtimedomain.WorkflowRunFailed, nil)

		requireAttemptState(t, run, "implement", runtimedomain.ExecutionAttemptSucceeded)
		gateAttempt := requireAttemptState(t, run, "verify", runtimedomain.ExecutionAttemptFailed)
		// A genuine FAIL verdict (EXECUTION_FAILED), not a scope violation:
		// the gate ran and evaluated; it simply did not find the marker.
		if gateAttempt.TerminationReason != runtimedomain.TerminationReasonExecutionFailed || gateAttempt.FailureCode != errorcode.CodeExecutionFailed {
			t.Fatalf("gate attempt = reason %s code %s, want EXECUTION_FAILED/EXECUTION_FAILED", gateAttempt.TerminationReason, gateAttempt.FailureCode)
		}
	})
}

// readDiffManifestPaths opens the diff-manifest artifacts named by
// artifactIDs and returns the paths of every file they list.
func (f *v5AcceptFixture) readDiffManifestPaths(t *testing.T, artifactIDs []string) []string {
	t.Helper()
	ctx := context.Background()
	var paths []string
	for _, id := range artifactIDs {
		var ref ports.ArtifactRef
		if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
			row, err := tx.Artifacts().GetArtifact(ctx, id)
			if err != nil {
				return err
			}
			ref = ports.ArtifactRef{Locator: row.Locator, SHA256: row.ContentHash, Size: row.Size, ContentType: row.MediaType}
			return nil
		}); err != nil {
			t.Fatalf("GetArtifact(%s): %v", id, err)
		}
		body, err := f.artifacts.Open(ctx, ref)
		if err != nil {
			t.Fatalf("open artifact %s: %v", id, err)
		}
		raw, err := io.ReadAll(body)
		_ = body.Close()
		if err != nil {
			t.Fatalf("read artifact %s: %v", id, err)
		}
		var manifest ports.WorkspaceDiff
		if err := json.Unmarshal(raw, &manifest); err != nil {
			t.Fatalf("decode diff manifest %s: %v", id, err)
		}
		for _, file := range manifest.Files {
			paths = append(paths, file.Path)
		}
	}
	return paths
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
