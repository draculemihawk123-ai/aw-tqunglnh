// V9-02 — "Outcome cho kết quả kiểm tra máy, evidence khi fail"
// (docs/design/12-v9-harness-alignment.md V9-02; ADR-031 in
// docs/architecture/02-architecture-decisions.md; gap G2 in
// docs/harness-engineering/15-doi-chieu-v9.md).
//
// Before V9-02 a COMMAND that exited non-zero failed its attempt, left no
// evidence at all, and (after the retry policy) failed the NodeRun and the run;
// a MACHINE_GATE with a FAIL verdict did the same. There was no way to draw
// "the check failed -> back to the maker", and nothing for the maker to read.
// The scenarios here drive the real thing end to end — the fake provider CLI as
// a real process, real evaluator scripts as real processes, a real
// gitworktree.Provider, a real sqlite store, a real worker pool:
//
//   - build -> gate (FAIL once, then PASS) -> end under a cyclePolicy of
//     maxIterations 2: the second build's ContextSnapshot carries the first
//     failure's evidence, its prompt carries the WHAT/WHY/FIX summary, the real
//     provider process receives it, and the run completes;
//   - the same loop with a COMMAND that exits 1 and then 0;
//   - a check that never passes exhausts the loop and takes the cyclePolicy's
//     escalation outcome, and the run does not satisfy completion;
//   - a technical error of a check that declares a failureOutcome (the
//     evaluator crashes) still fails the run — it is not a functional failure;
//   - without a failureOutcome a non-zero exit still fails the run exactly as
//     before, but the evidence is now there (the G2 reproduction).
//
// Every script has a .bat twin built from batch builtins only: the evaluator
// and the command are spawned with no inherited PATH.
package v5accept

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	stdruntime "runtime"
	"sort"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

const (
	// v9fGateEvidenceKey is the one criterion of the gate scenarios.
	v9fGateEvidenceKey = "TESTS_GREEN"
	// v9fGateDetail is what the failing evaluation reports for the criterion; it
	// must reach the second build's prompt. No shell metacharacters: the .bat
	// twin echoes it inside a parenthesised block.
	v9fGateDetail = "2 tests red in pkg/add"
	// v9fCommandStderr is what the failing command writes to stderr.
	v9fCommandStderr = "FAIL: TestAdd want 3 got 2"
	// v9fCheckTimeoutSeconds is a ceiling, not an expectation: the scripts
	// return at once.
	v9fCheckTimeoutSeconds = 120
)

// v9fMode selects how a check script behaves.
type v9fMode int

const (
	// v9fFailThenPass fails the first time it runs (and leaves a marker file
	// outside every repository) and passes ever after.
	v9fFailThenPass v9fMode = iota
	// v9fAlwaysFail fails every time.
	v9fAlwaysFail
	// v9fCrash is a gate evaluator that exits non-zero without a verdict.
	v9fCrash
)

// v9fGateScript is the gate evaluator: it reports its criterion as FAIL or
// PASS per mode. Shell builtins only.
func v9fGateScript(markerPath string, mode v9fMode) (key, script string) {
	fail := `{"` + v9fGateEvidenceKey + `":{"verdict":"FAIL","detail":"` + v9fGateDetail + `"}}`
	pass := `{"` + v9fGateEvidenceKey + `":{"verdict":"PASS"}}`
	if stdruntime.GOOS == "windows" {
		switch mode {
		case v9fAlwaysFail:
			return "check-gate.bat", "@echo off\r\necho " + fail + "\r\nexit /b 0\r\n"
		case v9fCrash:
			return "check-gate.bat", "@echo off\r\nexit /b 1\r\n"
		default:
			return "check-gate.bat", "@echo off\r\nif exist \"" + markerPath + "\" (\r\n  echo " + pass + "\r\n) else (\r\n  echo run> \"" + markerPath + "\"\r\n  echo " + fail + "\r\n)\r\nexit /b 0\r\n"
		}
	}
	switch mode {
	case v9fAlwaysFail:
		return "check-gate.sh", "#!/bin/sh\necho '" + fail + "'\nexit 0\n"
	case v9fCrash:
		return "check-gate.sh", "#!/bin/sh\nexit 1\n"
	default:
		return "check-gate.sh", "#!/bin/sh\nif [ -f \"" + markerPath + "\" ]; then\n  echo '" + pass + "'\nelse\n  echo run > \"" + markerPath + "\"\n  echo '" + fail + "'\nfi\nexit 0\n"
	}
}

// v9fCommandScript is the COMMAND check: exit 1 with a message on stderr, or
// exit 0, per mode.
func v9fCommandScript(markerPath string, mode v9fMode) (key, script string) {
	if stdruntime.GOOS == "windows" {
		if mode == v9fAlwaysFail {
			return "check-command.bat", "@echo off\r\necho " + v9fCommandStderr + " 1>&2\r\nexit /b 1\r\n"
		}
		return "check-command.bat", "@echo off\r\nif exist \"" + markerPath + "\" exit /b 0\r\necho run> \"" + markerPath + "\"\r\necho " + v9fCommandStderr + " 1>&2\r\nexit /b 1\r\n"
	}
	if mode == v9fAlwaysFail {
		return "check-command.sh", "#!/bin/sh\necho '" + v9fCommandStderr + "' >&2\nexit 1\n"
	}
	return "check-command.sh", "#!/bin/sh\nif [ -f \"" + markerPath + "\" ]; then\n  exit 0\nfi\necho run > \"" + markerPath + "\"\necho '" + v9fCommandStderr + "' >&2\nexit 1\n"
}

// v9fPublishCheck publishes the script, the command (and gate) around it, and
// returns the check node of kind with failureOutcome set when declareFailure.
func v9fPublishCheck(t *testing.T, f *v5AcceptFixture, kind workflow.NodeType, mode v9fMode, declareFailure bool) workflow.Node {
	t.Helper()
	markerPath := filepath.Join(f.fixtureRoot, "v9f-check-marker.txt")
	var key, script string
	if kind == workflow.NodeMachineGate {
		key, script = v9fGateScript(markerPath, mode)
	} else {
		key, script = v9fCommandScript(markerPath, mode)
	}
	skillDoc := v5AcceptTwoResourceSkillDocument(key, script, "unused.txt", "unused")
	publishSkillVersion(t, f.uow, "v9f-skill-def", "v9f-skill-v1", skillDoc)
	hash := resourceContentHash(t, "v9f-skill-v1", key, skillDoc)
	publishCommandVersion(t, f.uow, "v9f-cmd-def", "v9f-cmd-v1", command.CommandDocument{
		Executable:          command.ExecutableRef{OwnerVersionID: "v9f-skill-v1", ResourceKey: key, ContentHash: hash},
		Argv:                []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}},
		CwdRepositoryTarget: v5AcceptRepositoryID,
		Compatibility:       command.Compatibility{OS: []string{stdruntime.GOOS}},
		NetworkAccess:       command.NetworkAccessNone,
		TimeoutSeconds:      v9fCheckTimeoutSeconds,
		Output:              command.OutputContract{CaptureStdout: true, CaptureStderr: true, MaxOutputBytes: 1 << 16},
	})
	failure := ""
	if declareFailure {
		failure = "failed"
	}
	if kind == workflow.NodeMachineGate {
		publishGateVersion(t, f.uow, "v9f-gate-def", "v9f-gate-v1", gate.GateDocument{
			CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "v9f-cmd-def", VersionID: "v9f-cmd-v1"},
			Criteria:   []gate.Criterion{{Name: "tests-green", EvidenceKey: v9fGateEvidenceKey}},
		})
		return workflow.Node{
			Key: "check", Type: workflow.NodeMachineGate, Outcomes: []string{"passed", "failed"},
			MachineGate: &workflow.MachineGateNodeConfig{
				GateRef:    definition.DependencyPin{Kind: definition.KindGate, DefinitionID: "v9f-gate-def", VersionID: "v9f-gate-v1"},
				PolicyRefs: v9PolicyRefs(), FailureOutcome: failure,
			},
		}
	}
	return workflow.Node{
		Key: "check", Type: workflow.NodeCommand, Outcomes: []string{"passed", "failed"},
		Command: &workflow.CommandNodeConfig{
			CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "v9f-cmd-def", VersionID: "v9f-cmd-v1"},
			PolicyRefs: v9PolicyRefs(), FailureOutcome: failure,
		},
	}
}

// v9fLoopDocument is build (MAKER, cyclePolicy maxIterations 2) -> check ->
// end, with check --failed--> build and build --escalated--> escalated_end.
func v9fLoopDocument(agentBuildID string, check workflow.Node) workflow.WorkflowDocument {
	build := v9AgentNode("build", agentBuildID, workflow.AgentRoleMaker, "done")
	build.Outcomes = []string{"done", "escalated"}
	build.CyclePolicy = &workflow.CyclePolicy{MaxIterations: 2, EscalationOutcome: "escalated"}
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			build,
			check,
			{Key: "end", Type: workflow.NodeEnd},
			{Key: "escalated_end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-build", From: "start", Outcome: "next", To: "build"},
			{Key: "build-check", From: "build", Outcome: "done", To: "check"},
			{Key: "build-escalated", From: "build", Outcome: "escalated", To: "escalated_end"},
			{Key: "check-end", From: "check", Outcome: "passed", To: "end"},
			{Key: "check-build", From: "check", Outcome: "failed", To: "build"},
		},
	}
}

// v9fWithCompletion pins a completion policy requiring requiredKind into doc
// and returns the dependency manifest that goes with it.
func v9fWithCompletion(t *testing.T, f *v5AcceptFixture, doc *workflow.WorkflowDocument, requiredKind string) workflow.DependencyManifest {
	t.Helper()
	fields := publishPolicyVersion(t, f.uow, "v9f-completion-policy-def", "v9f-completion-policy-v1", policy.PolicyDocument{
		Category:   policy.CategoryCompletion,
		Completion: &policy.CompletionRules{RequiredEvidenceKinds: []string{requiredKind}},
	})
	doc.CompletionPolicyRef = &definition.DependencyPin{Kind: definition.KindPolicy, DefinitionID: "v9f-completion-policy-def", VersionID: "v9f-completion-policy-v1"}
	return workflow.DependencyManifest{Pins: []workflow.DependencyPin{
		{Kind: string(definition.KindPolicy), Key: "v9f-completion-policy-def", Version: "v9f-completion-policy-v1", Hash: fields.CompiledHash()},
	}}
}

// v9fHistory is every activation of a run in activation order, with the
// attempts of each.
type v9fHistory struct {
	nodeRuns []runtimedomain.NodeRun
	attempts map[runtimedomain.NodeRunID][]runtimedomain.ExecutionAttempt
}

func (h v9fHistory) byKey(key string) []runtimedomain.NodeRun {
	var out []runtimedomain.NodeRun
	for _, nr := range h.nodeRuns {
		if nr.NodeKey == key {
			out = append(out, nr)
		}
	}
	return out
}

func (h v9fHistory) onlyAttempt(t *testing.T, nr runtimedomain.NodeRun) runtimedomain.ExecutionAttempt {
	t.Helper()
	attempts := h.attempts[nr.ID]
	if len(attempts) != 1 {
		t.Fatalf("node run %s (%s) has %d attempts, want exactly one (a deterministic result is never retried)", nr.ID, nr.NodeKey, len(attempts))
	}
	return attempts[0]
}

func readV9fHistory(t *testing.T, f *v5AcceptFixture, runID string) v9fHistory {
	t.Helper()
	ctx := context.Background()
	history := v9fHistory{attempts: map[runtimedomain.NodeRunID][]runtimedomain.ExecutionAttempt{}}
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		history.nodeRuns, err = tx.Runtime().ListNodeRunsForRun(ctx, runID)
		if err != nil {
			return err
		}
		attempts, err := tx.Runtime().ListExecutionAttemptsForRun(ctx, runID)
		if err != nil {
			return err
		}
		for _, a := range attempts {
			history.attempts[a.NodeRunID] = append(history.attempts[a.NodeRunID], a)
		}
		return nil
	}); err != nil {
		t.Fatalf("read run history: %v", err)
	}
	sort.Slice(history.nodeRuns, func(i, j int) bool {
		return history.nodeRuns[i].ActivationSequence < history.nodeRuns[j].ActivationSequence
	})
	return history
}

func (f *v5AcceptFixture) evidenceOfAttempt(t *testing.T, attemptID string) []runtimedomain.Evidence {
	t.Helper()
	ctx := context.Background()
	var rows []runtimedomain.Evidence
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		rows, err = tx.Runtime().ListEvidenceForAttempt(ctx, attemptID)
		return err
	}); err != nil {
		t.Fatalf("ListEvidenceForAttempt(%s): %v", attemptID, err)
	}
	return rows
}

func (f *v5AcceptFixture) snapshotEvidenceIDs(t *testing.T, attemptID string) []string {
	t.Helper()
	ctx := context.Background()
	var ids []string
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		snapshot, err := tx.ContextSnapshots().GetSnapshotByAttemptID(ctx, attemptID)
		if err != nil {
			return err
		}
		for _, ref := range snapshot.EvidenceRefs {
			ids = append(ids, ref.EvidenceID)
		}
		return nil
	}); err != nil {
		t.Fatalf("read context snapshot of attempt %s: %v", attemptID, err)
	}
	return ids
}

func (f *v5AcceptFixture) readArtifactText(t *testing.T, artifactID string) string {
	t.Helper()
	ctx := context.Background()
	var ref ports.ArtifactRef
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		row, err := tx.Artifacts().GetArtifact(ctx, artifactID)
		if err != nil {
			return err
		}
		ref = ports.ArtifactRef{Locator: row.Locator, SHA256: row.ContentHash, Size: row.Size, ContentType: row.MediaType}
		return nil
	}); err != nil {
		t.Fatalf("GetArtifact(%s): %v", artifactID, err)
	}
	body, err := f.artifacts.Open(ctx, ref)
	if err != nil {
		t.Fatalf("open artifact %s: %v", artifactID, err)
	}
	defer body.Close()
	raw, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read artifact %s: %v", artifactID, err)
	}
	return string(raw)
}

func (f *v5AcceptFixture) evaluateCompletion(t *testing.T, runID string) runtime.CompletionOutcome {
	t.Helper()
	ctx := context.Background()
	var run runtimedomain.WorkflowRun
	if err := f.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		run, err = tx.Runtime().GetWorkflowRun(ctx, runID)
		return err
	}); err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	cmd := testCmd("v9f-eval-"+runID, ports.ProjectScope(v5AcceptProjectID), "EvaluateCompletionCandidate")
	cmd.ExpectedVersion = run.Version
	decision, err := runtime.EvaluateCompletionCandidate(ctx, f.uow, f.ids, clock.System{}, cmd, runtime.EvaluateCompletionCandidateRequest{RunID: runID})
	if err != nil {
		t.Fatalf("EvaluateCompletionCandidate: %v", err)
	}
	return decision.Outcome
}

// v9fPrompt assembles the request of attempt (the way the worker does right
// before spawning) and returns the decoded prompt.
func (f *v5AcceptFixture) v9fPrompt(t *testing.T, runID string, nodeRun runtimedomain.NodeRun, attempt runtimedomain.ExecutionAttempt) map[string]json.RawMessage {
	t.Helper()
	req, err := runtime.AssembleAgentExecutionRequest(context.Background(), f.uow, f.artifacts, runtime.AssembleAgentExecutionRequestRequest{
		RunID: runID, NodeRunID: string(nodeRun.ID), AttemptID: string(attempt.ID),
	})
	if err != nil {
		t.Fatalf("AssembleAgentExecutionRequest: %v", err)
	}
	var prompt map[string]json.RawMessage
	if err := json.Unmarshal([]byte(req.Prompt), &prompt); err != nil {
		t.Fatalf("decode prompt: %v", err)
	}
	return prompt
}

type v9fCheckFailure struct {
	CheckNode   string   `json:"checkNode"`
	EvidenceIDs []string `json:"evidenceIds"`
	What        string   `json:"what"`
	Why         string   `json:"why"`
	Fix         string   `json:"fix"`
}

func v9fDecodeCheckFailures(t *testing.T, prompt map[string]json.RawMessage) []v9fCheckFailure {
	t.Helper()
	raw, ok := prompt["checkFailures"]
	if !ok {
		return nil
	}
	var failures []v9fCheckFailure
	if err := json.Unmarshal(raw, &failures); err != nil {
		t.Fatalf("decode checkFailures: %v", err)
	}
	return failures
}

// runV9fLoop publishes the loop with the given check, runs it to want and
// returns the run id.
func runV9fLoop(
	t *testing.T, f *v5AcceptFixture, kind workflow.NodeType, mode v9fMode, declareFailure bool, completionKind string,
	idPrefix string, want runtimedomain.WorkflowRunState,
) (agents v9AgentSetup, runID string) {
	t.Helper()
	t.Setenv("AGENTKIT_HELPER_MODE", "outcome-success")
	t.Setenv("AGENTKIT_HELPER_OUTCOME", "done")
	t.Setenv("AGENTKIT_CAPTURE_PATH", filepath.Join(f.fixtureRoot, "fake-cli-capture.json"))

	agents = newV9AgentSetup(t, f)
	check := v9fPublishCheck(t, f, kind, mode, declareFailure)
	doc := v9fLoopDocument(agents.buildID, check)
	dependencies := workflow.DependencyManifest{}
	if completionKind != "" {
		dependencies = v9fWithCompletion(t, f, &doc, completionKind)
	}
	version := publishWorkflowVersion(t, f.uow, v5AcceptProjectID, idPrefix+"-workflow-def", idPrefix+"-workflow-v1", doc, dependencies)
	router := &runtime.NodeExecutorRouter{Agent: agents.executor, Command: f.newCommandExecutor(), Gate: f.newGateExecutor()}
	run := startV9Run(t, f, router, agents.registry, version, idPrefix, want, nil)
	return agents, run.runID
}

// TestV9AcceptCheckFailureOutcome_GateFailsOnceThenPasses is the scenario of
// docs/design/12 V9-02 "Verify": build -> gate (failed) -> build -> gate
// (passed) -> end with maxIterations 2.
func TestV9AcceptCheckFailureOutcome_GateFailsOnceThenPasses(t *testing.T) {
	f := newV5AcceptFixture(t)
	_, runID := runV9fLoop(t, f, workflow.NodeMachineGate, v9fFailThenPass, true, v9fGateEvidenceKey, "v9fg", runtimedomain.WorkflowRunVerifying)

	history := readV9fHistory(t, f, runID)
	builds, checks := history.byKey("build"), history.byKey("check")
	if len(builds) != 2 || len(checks) != 2 || len(history.byKey("end")) != 1 || len(history.byKey("escalated_end")) != 0 {
		t.Fatalf("activations: build=%d check=%d end=%d escalated_end=%d, want 2/2/1/0", len(builds), len(checks), len(history.byKey("end")), len(history.byKey("escalated_end")))
	}

	// The first evaluation failed functionally: the attempt and the NodeRun
	// SUCCEEDED with outcome failed, no retry, evidence verdict FAIL.
	if checks[0].State != runtimedomain.NodeRunSucceeded || checks[0].SelectedOutcome != "failed" {
		t.Fatalf("first check = %s / %q, want SUCCEEDED / failed", checks[0].State, checks[0].SelectedOutcome)
	}
	firstCheck := history.onlyAttempt(t, checks[0])
	if firstCheck.State != runtimedomain.ExecutionAttemptSucceeded {
		t.Fatalf("first check attempt = %s (reason %s, code %s), want SUCCEEDED", firstCheck.State, firstCheck.TerminationReason, firstCheck.FailureCode)
	}
	firstEvidence := f.evidenceOfAttempt(t, string(firstCheck.ID))
	if len(firstEvidence) != 1 || firstEvidence[0].Kind != v9fGateEvidenceKey || firstEvidence[0].Verdict != string(gate.VerdictFail) {
		t.Fatalf("first check evidence = %+v, want one %s row with verdict FAIL", firstEvidence, v9fGateEvidenceKey)
	}
	// The second passed.
	if checks[1].State != runtimedomain.NodeRunSucceeded || checks[1].SelectedOutcome != "passed" {
		t.Fatalf("second check = %s / %q, want SUCCEEDED / passed", checks[1].State, checks[1].SelectedOutcome)
	}
	if rows := f.evidenceOfAttempt(t, string(history.onlyAttempt(t, checks[1]).ID)); len(rows) != 1 || rows[0].Verdict != string(gate.VerdictPass) {
		t.Fatalf("second check evidence = %+v, want one PASS row", rows)
	}

	// The second build's ContextSnapshot carries the first failure's evidence;
	// the first build's carries none.
	firstBuild, secondBuild := history.onlyAttempt(t, builds[0]), history.onlyAttempt(t, builds[1])
	if ids := f.snapshotEvidenceIDs(t, string(firstBuild.ID)); len(ids) != 0 {
		t.Fatalf("first build snapshot evidence refs = %v, want none", ids)
	}
	refs := f.snapshotEvidenceIDs(t, string(secondBuild.ID))
	if len(refs) != 1 || refs[0] != string(firstEvidence[0].ID) {
		t.Fatalf("second build snapshot evidence refs = %v, want [%s]", refs, firstEvidence[0].ID)
	}

	// ...and its prompt carries the summary: the failed criterion and its detail.
	prompt := f.v9fPrompt(t, runID, builds[1], secondBuild)
	failures := v9fDecodeCheckFailures(t, prompt)
	if len(failures) != 1 || failures[0].CheckNode != "check" {
		t.Fatalf("checkFailures = %+v, want exactly one for node check", failures)
	}
	if !strings.Contains(failures[0].What, "gate verdict FAIL") || !strings.Contains(failures[0].Why, v9fGateEvidenceKey) || !strings.Contains(failures[0].Why, v9fGateDetail) {
		t.Fatalf("checkFailures[0] = %+v, want the verdict, the failed criterion and its detail", failures[0])
	}
	if _, present := f.v9fPrompt(t, runID, builds[0], firstBuild)["checkFailures"]; present {
		t.Fatal("the first build's prompt has a checkFailures section, want none")
	}

	// The REAL provider process was handed that prompt: the fake CLI records
	// what it received on stdin, and the last agent invocation is the second build.
	var captured struct {
		Stdin string `json:"stdin"`
	}
	raw, err := os.ReadFile(filepath.Join(f.fixtureRoot, "fake-cli-capture.json"))
	if err != nil {
		t.Fatalf("read fake CLI capture: %v", err)
	}
	if err := json.Unmarshal(raw, &captured); err != nil {
		t.Fatalf("decode fake CLI capture: %v", err)
	}
	if !strings.Contains(captured.Stdin, "checkFailures") || !strings.Contains(captured.Stdin, v9fGateDetail) {
		t.Fatalf("the provider process received %q, want the check failure summary", captured.Stdin)
	}

	// fail -> fix -> pass completes: the latest activation of the check passed.
	if outcome := f.evaluateCompletion(t, runID); outcome != runtime.CompletionOutcomePass {
		t.Fatalf("completion outcome = %s, want PASS", outcome)
	}
}

// TestV9AcceptCheckFailureOutcome_CommandExitsOneThenZero is the COMMAND
// variant: a `test` command that exits 1 and then 0.
func TestV9AcceptCheckFailureOutcome_CommandExitsOneThenZero(t *testing.T) {
	f := newV5AcceptFixture(t)
	_, runID := runV9fLoop(t, f, workflow.NodeCommand, v9fFailThenPass, true, runtimedomain.EvidenceKindCommandExecution, "v9fc", runtimedomain.WorkflowRunVerifying)

	history := readV9fHistory(t, f, runID)
	builds, checks := history.byKey("build"), history.byKey("check")
	if len(builds) != 2 || len(checks) != 2 {
		t.Fatalf("activations: build=%d check=%d, want 2/2", len(builds), len(checks))
	}
	if checks[0].State != runtimedomain.NodeRunSucceeded || checks[0].SelectedOutcome != "failed" || checks[1].SelectedOutcome != "passed" {
		t.Fatalf("checks = %s/%q then %s/%q, want SUCCEEDED/failed then SUCCEEDED/passed", checks[0].State, checks[0].SelectedOutcome, checks[1].State, checks[1].SelectedOutcome)
	}

	// Evidence of the failed run: verdict FAILED, with the exit code and the
	// stderr readable from its artifact.
	firstCheck := history.onlyAttempt(t, checks[0])
	if firstCheck.State != runtimedomain.ExecutionAttemptSucceeded {
		t.Fatalf("first check attempt = %s, want SUCCEEDED (the failure is a result, not an error)", firstCheck.State)
	}
	rows := f.evidenceOfAttempt(t, string(firstCheck.ID))
	if len(rows) != 1 || rows[0].Kind != runtimedomain.EvidenceKindCommandExecution || rows[0].Verdict != runtimedomain.EvidenceVerdictFailed {
		t.Fatalf("first check evidence = %+v, want one COMMAND_EXECUTION / FAILED row", rows)
	}
	var record struct {
		ExitCode  int      `json:"exitCode"`
		Argv      []string `json:"argv"`
		Stderr    string   `json:"stderr"`
		Truncated bool     `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(f.readArtifactText(t, rows[0].ArtifactReferences[0])), &record); err != nil {
		t.Fatalf("decode command record: %v", err)
	}
	if record.ExitCode != 1 || !strings.Contains(record.Stderr, v9fCommandStderr) || len(record.Argv) == 0 || record.Truncated {
		t.Fatalf("command record = %+v, want exit code 1, the stderr, the argv, not truncated", record)
	}

	// The second build was told.
	secondBuild := history.onlyAttempt(t, builds[1])
	if ids := f.snapshotEvidenceIDs(t, string(secondBuild.ID)); len(ids) != 1 || ids[0] != string(rows[0].ID) {
		t.Fatalf("second build snapshot evidence refs = %v, want [%s]", ids, rows[0].ID)
	}
	failures := v9fDecodeCheckFailures(t, f.v9fPrompt(t, runID, builds[1], secondBuild))
	if len(failures) != 1 || !strings.Contains(failures[0].What, "exited with code 1") || !strings.Contains(failures[0].Why, v9fCommandStderr) {
		t.Fatalf("checkFailures = %+v, want the exit code and the stderr", failures)
	}

	if outcome := f.evaluateCompletion(t, runID); outcome != runtime.CompletionOutcomePass {
		t.Fatalf("completion outcome = %s, want PASS", outcome)
	}
}

// TestV9AcceptCheckFailureOutcome_ExceedingTheLoopEscalates: a check that never
// passes uses up the maker's cyclePolicy (maxIterations 2: three maker runs)
// and the run takes the escalation outcome, ending at escalated_end. The last
// check failed, so the run does not satisfy completion.
func TestV9AcceptCheckFailureOutcome_ExceedingTheLoopEscalates(t *testing.T) {
	f := newV5AcceptFixture(t)
	_, runID := runV9fLoop(t, f, workflow.NodeMachineGate, v9fAlwaysFail, true, v9fGateEvidenceKey, "v9fe", runtimedomain.WorkflowRunVerifying)

	history := readV9fHistory(t, f, runID)
	builds, checks := history.byKey("build"), history.byKey("check")
	if len(checks) != 3 {
		t.Fatalf("check activations = %d, want 3 (maxIterations 2 = two rework rounds after the first run)", len(checks))
	}
	for i, check := range checks {
		if check.State != runtimedomain.NodeRunSucceeded || check.SelectedOutcome != "failed" {
			t.Fatalf("check #%d = %s / %q, want SUCCEEDED / failed", i, check.State, check.SelectedOutcome)
		}
		history.onlyAttempt(t, check) // no ATTEMPT-policy retry
	}
	if len(builds) != 4 {
		t.Fatalf("build activations = %d, want 4 (three runs and the exhausted one)", len(builds))
	}
	exhausted := builds[3]
	if exhausted.State != runtimedomain.NodeRunSkipped || exhausted.SelectedOutcome != "escalated" || len(history.attempts[exhausted.ID]) != 0 {
		t.Fatalf("fourth build = %s / %q with %d attempts, want SKIPPED / escalated and never run", exhausted.State, exhausted.SelectedOutcome, len(history.attempts[exhausted.ID]))
	}
	if len(history.byKey("escalated_end")) != 1 || len(history.byKey("end")) != 0 {
		t.Fatalf("escalated_end=%d end=%d, want the run to end at escalated_end", len(history.byKey("escalated_end")), len(history.byKey("end")))
	}
	if outcome := f.evaluateCompletion(t, runID); outcome != runtime.CompletionOutcomeBlock {
		t.Fatalf("completion outcome = %s, want BLOCK: the last check failed", outcome)
	}
}

// TestV9AcceptCheckFailureOutcome_TechnicalErrorStillFailsTheRun: a gate whose
// evaluator crashes (exit 1, no verdict) has verdict ERROR — a technical error,
// never the failureOutcome — so the run fails instead of looping to the maker.
func TestV9AcceptCheckFailureOutcome_TechnicalErrorStillFailsTheRun(t *testing.T) {
	f := newV5AcceptFixture(t)
	_, runID := runV9fLoop(t, f, workflow.NodeMachineGate, v9fCrash, true, "", "v9ft", runtimedomain.WorkflowRunFailed)

	history := readV9fHistory(t, f, runID)
	checks := history.byKey("check")
	if len(history.byKey("build")) != 1 || len(checks) != 1 {
		t.Fatalf("activations: build=%d check=%d, want 1/1 — an ERROR verdict must not send the run back to the maker", len(history.byKey("build")), len(checks))
	}
	if checks[0].State != runtimedomain.NodeRunFailed || checks[0].SelectedOutcome != "" {
		t.Fatalf("check = %s / %q, want FAILED with no outcome", checks[0].State, checks[0].SelectedOutcome)
	}
	attempt := history.attempts[checks[0].ID][0]
	if attempt.State != runtimedomain.ExecutionAttemptFailed {
		t.Fatalf("check attempt = %s, want FAILED", attempt.State)
	}
	if rows := f.evidenceOfAttempt(t, string(attempt.ID)); len(rows) != 1 || rows[0].Verdict != string(gate.VerdictError) {
		t.Fatalf("check evidence = %+v, want one ERROR row", rows)
	}
}

// TestV9AcceptCheckFailureOutcome_NoFailureOutcomeKeepsTheTransitionAndGainsEvidence
// is the G2 reproduction: a COMMAND without a failureOutcome that exits 1 still
// fails its NodeRun and the run, exactly as before — but before V9-02 it left
// no evidence at all, so nobody could tell why without re-running it.
func TestV9AcceptCheckFailureOutcome_NoFailureOutcomeKeepsTheTransitionAndGainsEvidence(t *testing.T) {
	f := newV5AcceptFixture(t)
	check := v9fPublishCheck(t, f, workflow.NodeCommand, v9fAlwaysFail, false)
	check.Outcomes = []string{"passed"}
	doc := workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			check,
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-check", From: "start", Outcome: "next", To: "check"},
			{Key: "check-end", From: "check", Outcome: "passed", To: "end"},
		},
	}
	publishPolicyVersion(t, f.uow, "v9-attempt-policy-def", "v9-attempt-policy-v1", v5AcceptAttemptPolicyDocument(120))
	publishPolicyVersion(t, f.uow, "v9-permission-policy-def", "v9-permission-policy-v1", v5AcceptDriftPermissionPolicyDocument())
	version := publishWorkflowVersion(t, f.uow, v5AcceptProjectID, "v9fn-workflow-def", "v9fn-workflow-v1", doc, workflow.DependencyManifest{})
	router := &runtime.NodeExecutorRouter{Command: f.newCommandExecutor()}
	run := startV9Run(t, f, router, agentregistry.Empty(), version, "v9fn", runtimedomain.WorkflowRunFailed, nil)

	history := readV9fHistory(t, f, run.runID)
	checks := history.byKey("check")
	if len(checks) != 1 || checks[0].State != runtimedomain.NodeRunFailed {
		t.Fatalf("check activations = %+v, want exactly one FAILED NodeRun", checks)
	}
	attempts := history.attempts[checks[0].ID]
	if len(attempts) != 1 || attempts[0].State != runtimedomain.ExecutionAttemptFailed || attempts[0].FailureCode != "EXECUTION_FAILED" {
		t.Fatalf("attempts = %+v, want one FAILED attempt with code EXECUTION_FAILED", attempts)
	}
	rows := f.evidenceOfAttempt(t, string(attempts[0].ID))
	if len(rows) != 1 || rows[0].Kind != "COMMAND_EXECUTION" || rows[0].Verdict != "FAILED" {
		t.Fatalf("evidence of the failed attempt = %+v, want one COMMAND_EXECUTION / FAILED row", rows)
	}
	text := f.readArtifactText(t, rows[0].ArtifactReferences[0])
	if !strings.Contains(text, `"exitCode":1`) || !strings.Contains(text, v9fCommandStderr) {
		t.Fatalf("evidence artifact = %s, want the exit code and the stderr", text)
	}
}
