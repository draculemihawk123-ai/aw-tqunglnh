package runtime_test

// V9-02 (ADR-031 decision 6) — the maker that is sent back by a failing check
// receives that check's evidence and a WHAT/WHY/FIX summary:
//
//	implement (AGENT) -> check (COMMAND or MACHINE_GATE, failureOutcome failed)
//	     ^                    |   \--passed--> end
//	     \------failed--------/    \--escalated--> escalated_end   (cyclePolicy)
//
// Driven against the fake store with real scheduling, finalization, executors
// and request assembly; the real-process version lives in
// internal/integration/v5accept.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/skill"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// makerCheckLoop is the running fixture: a fake store holding a published
// maker -> check loop, with helpers to walk it one step at a time.
type makerCheckLoop struct {
	t     *testing.T
	uow   *fake.UnitOfWork
	ids   idsource.Source
	store ports.ArtifactStore
	runID string
	// makerNodeRunID is the maker activation waiting to be scheduled.
	makerNodeRunID string
	checkKind      workflow.NodeType
}

func newMakerCheckLoop(t *testing.T, checkKind workflow.NodeType) *makerCheckLoop {
	t.Helper()
	ctx := context.Background()
	build := assembleFixtureBuild(t)
	buildID := build.ID()
	store := artifactstoreForTest(t)

	check := workflow.Node{
		Key: "check", Outcomes: []string{"passed", "failed", "escalated"},
		CyclePolicy: &workflow.CyclePolicy{MaxIterations: 2, EscalationOutcome: "escalated"},
	}
	switch checkKind {
	case workflow.NodeCommand:
		check.Type = workflow.NodeCommand
		check.Command = &workflow.CommandNodeConfig{
			CommandRef:     definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "command-def-1", VersionID: "command-v1"},
			PolicyRefs:     fullyResolvablePolicyRefs(),
			FailureOutcome: "failed",
		}
	case workflow.NodeMachineGate:
		check.Type = workflow.NodeMachineGate
		check.MachineGate = &workflow.MachineGateNodeConfig{
			GateRef:        definition.DependencyPin{Kind: definition.KindGate, DefinitionID: "gate-def-1", VersionID: "gate-v1"},
			PolicyRefs:     fullyResolvablePolicyRefs(),
			FailureOutcome: "failed",
		}
	default:
		t.Fatalf("unsupported check kind %s", checkKind)
	}
	document := workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			// The fixtures require the first executable node to be called implement.
			{Key: "implement", Type: workflow.NodeAgent, Outcomes: []string{"done"}, Agent: &workflow.AgentNodeConfig{
				ProfileRef:     definition.DependencyPin{Kind: definition.KindAgentProfile, DefinitionID: "agent-profile-def", VersionID: "agent-profile-v1"},
				PolicyRefs:     fullyResolvablePolicyRefs(),
				AdapterBuildID: &buildID,
				Role:           workflow.AgentRoleMaker,
			}},
			check,
			{Key: "end", Type: workflow.NodeEnd},
			{Key: "escalated_end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-to-implement", From: "start", Outcome: "next", To: "implement"},
			{Key: "implement-to-check", From: "implement", Outcome: "done", To: "check"},
			{Key: "check-to-end", From: "check", Outcome: "passed", To: "end"},
			{Key: "check-to-implement", From: "check", Outcome: "failed", To: "implement"},
			{Key: "check-to-escalated", From: "check", Outcome: "escalated", To: "escalated_end"},
		},
	}
	u, seq, rID, nrID := scheduleFixture(t, document)
	if err := u.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, _, err := tx.AdapterBuilds().InsertIfAbsent(ctx, build)
		return err
	}); err != nil {
		t.Fatalf("register pinned adapter build: %v", err)
	}

	// The maker's context route and profile.
	skillDoc := oneResourceSkillDocument("golden-rule", "always follow this", skill.Selector{}, true)
	publishSkillVersion(t, u, "skill-def-1", "skill-v1", skillDoc)
	hash := resourceContentHash(t, "skill-v1", "golden-rule", skillDoc)
	publishPolicyVersion(t, u, "context-policy-def", "context-policy-v1", policy.PolicyDocument{
		Category: policy.CategoryContext,
		Context: &policy.ContextRules{
			Selector: []string{"*"}, Budget: policy.ContextBudget{MaxTokens: 65536},
			ResourceRefs: []policy.ResourceRef{{OwnerVersionID: "skill-v1", ResourceKey: "golden-rule", ContentHash: hash}},
		},
	})
	publishAgentProfileVersionOnly(t, u, "agent-profile-def", "agent-profile-v1", validAgentProfileDocument())
	publishPolicyVersion(t, u, "attempt-policy-def", "attempt-policy-v1", attemptPolicyDocument(600))
	publishPolicyVersion(t, u, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())

	// The check's command (and gate).
	scriptDoc := oneResourceSkillDocument("check-script", "#!/bin/sh\nexit 0\n", skill.Selector{}, true)
	publishSkillVersion(t, u, "check-skill-def-1", "check-skill-v1", scriptDoc)
	scriptHash := resourceContentHash(t, "check-skill-v1", "check-script", scriptDoc)
	publishCommandVersion(t, u, "command-def-1", "command-v1", command.CommandDocument{
		Executable:          command.ExecutableRef{OwnerVersionID: "check-skill-v1", ResourceKey: "check-script", ContentHash: scriptHash},
		Argv:                []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}},
		CwdRepositoryTarget: "repo-1",
		Compatibility:       command.Compatibility{OS: []string{"linux", "windows", "darwin"}},
		NetworkAccess:       command.NetworkAccessNone,
		TimeoutSeconds:      600,
		Output:              command.OutputContract{CaptureStdout: true, CaptureStderr: true, MaxOutputBytes: 65536},
	})
	if checkKind == workflow.NodeMachineGate {
		publishGateVersion(t, u, "gate-def-1", "gate-v1", gate.GateDocument{
			CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "command-def-1", VersionID: "command-v1"},
			Criteria:   []gate.Criterion{{Name: "unit tests", EvidenceKey: "UNIT_TESTS"}, {Name: "lint", EvidenceKey: "LINT"}},
		})
	}
	return &makerCheckLoop{t: t, uow: u, ids: seq, store: store, runID: rID, makerNodeRunID: nrID, checkKind: checkKind}
}

// start schedules nodeRunID, marks its attempt running and gives it a job
// lease, as the worker would.
func (l *makerCheckLoop) start(nodeRunID string) (attemptID string, lease ports.JobLease) {
	l.t.Helper()
	ctx := context.Background()
	scheduled, err := runtime.ScheduleExecutableNodeRun(ctx, l.uow, l.ids, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: l.runID, NodeRunID: nodeRunID, CorrelationID: "corr-1",
	})
	if err != nil || !scheduled.Scheduled {
		l.t.Fatalf("ScheduleExecutableNodeRun(%s) = %+v, %v", nodeRunID, scheduled, err)
	}
	markAttemptRunning(l.t, l.uow, l.runID, nodeRunID, scheduled.AttemptID)
	jobID := ports.JobID("job-" + scheduled.AttemptID)
	if err := l.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
			ID: jobID, ProjectID: "project-1", Kind: "EXECUTE_NODE",
			AggregateType: "ExecutionAttempt", AggregateID: scheduled.AttemptID, IdempotencyKey: "idem-" + scheduled.AttemptID,
		})
		return err
	}); err != nil {
		l.t.Fatalf("EnqueueJob: %v", err)
	}
	lease = ports.JobLease{JobID: jobID, Owner: "worker-1", Token: 1, LeaseUntil: time.Now().Add(time.Hour)}
	l.uow.Snapshot.Jobs().(*fake.JobsRepository).SetActiveLease(string(jobID), lease)
	return scheduled.AttemptID, lease
}

// runMaker schedules the waiting maker activation, completes it with outcome
// done, and returns its attempt id and the NodeRun the run advanced to (the
// check).
func (l *makerCheckLoop) runMaker() (makerAttemptID, checkNodeRunID string) {
	l.t.Helper()
	attemptID, lease := l.start(l.makerNodeRunID)
	finalized, err := runtime.FinalizeExecutionAttempt(context.Background(), l.uow, l.ids, clock.System{}, nil, runtime.FinalizeExecutionAttemptRequest{
		RunID: l.runID, NodeRunID: l.makerNodeRunID, AttemptID: attemptID, ExpectedVersion: loadAttemptVersion(l.t, l.uow, attemptID),
		NextState: runtimedomain.ExecutionAttemptSucceeded, TerminationReason: runtimedomain.TerminationReasonCompleted,
		SelectedOutcome: "done", JobLease: lease, CorrelationID: "corr-1",
	})
	if err != nil || !finalized.Advanced || finalized.AdvanceResult.NextNodeKey != "check" {
		l.t.Fatalf("finalize maker = %+v, %v, want an advance to check", finalized, err)
	}
	return attemptID, finalized.AdvanceResult.NextNodeRunID
}

// runCheck executes the check with supervisor and finalizes it, returning its
// attempt id and what the run advanced to.
func (l *makerCheckLoop) runCheck(checkNodeRunID string, supervisor *fake.ProcessSupervisor) (checkAttemptID string, finalized runtime.FinalizeExecutionAttemptResult) {
	l.t.Helper()
	attemptID, lease := l.start(checkNodeRunID)
	req := ports.NodeExecutionRequest{AttemptID: attemptID, NodeRunID: checkNodeRunID, RunID: l.runID, JobLease: lease}
	var result ports.NodeExecutionResult
	var err error
	if l.checkKind == workflow.NodeCommand {
		executor, _, _ := newTestCommandNodeExecutor(l.uow, l.ids, l.store, supervisor, fake.SecretResolver{})
		result, err = executor.Execute(context.Background(), req)
	} else {
		executor, _, _ := newTestGateNodeExecutor(l.uow, l.ids, l.store, supervisor, fake.SecretResolver{})
		result, err = executor.Execute(context.Background(), req)
	}
	if err != nil {
		l.t.Fatalf("Execute check: %v", err)
	}
	return attemptID, finalizeResult(l.t, l.uow, l.ids, l.runID, checkNodeRunID, attemptID, lease, result)
}

func (l *makerCheckLoop) snapshotEvidenceIDs(attemptID string) []string {
	l.t.Helper()
	ctx := context.Background()
	var ids []string
	if err := l.uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		snapshot, err := tx.ContextSnapshots().GetSnapshotByAttemptID(ctx, attemptID)
		if err != nil {
			return err
		}
		for _, ref := range snapshot.EvidenceRefs {
			ids = append(ids, ref.EvidenceID)
		}
		return nil
	}); err != nil {
		l.t.Fatalf("read snapshot of attempt %s: %v", attemptID, err)
	}
	return ids
}

// prompt assembles the request of attemptID and returns its decoded prompt.
func (l *makerCheckLoop) prompt(nodeRunID, attemptID string) map[string]json.RawMessage {
	l.t.Helper()
	req, err := runtime.AssembleAgentExecutionRequest(context.Background(), l.uow, l.store, runtime.AssembleAgentExecutionRequestRequest{
		RunID: l.runID, NodeRunID: nodeRunID, AttemptID: attemptID,
	})
	if err != nil {
		l.t.Fatalf("AssembleAgentExecutionRequest: %v", err)
	}
	var prompt map[string]json.RawMessage
	if err := json.Unmarshal([]byte(req.Prompt), &prompt); err != nil {
		l.t.Fatalf("decode prompt: %v", err)
	}
	return prompt
}

type promptCheckFailure struct {
	CheckNode   string   `json:"checkNode"`
	EvidenceIDs []string `json:"evidenceIds"`
	What        string   `json:"what"`
	Why         string   `json:"why"`
	Fix         string   `json:"fix"`
}

func decodeCheckFailures(t *testing.T, prompt map[string]json.RawMessage) []promptCheckFailure {
	t.Helper()
	raw, ok := prompt["checkFailures"]
	if !ok {
		return nil
	}
	var failures []promptCheckFailure
	if err := json.Unmarshal(raw, &failures); err != nil {
		t.Fatalf("decode checkFailures: %v", err)
	}
	return failures
}

func TestMakerAfterFailingCommand_ReceivesEvidenceRefAndSummary(t *testing.T) {
	loop := newMakerCheckLoop(t, workflow.NodeCommand)

	// First pass: the maker has no failure to learn from.
	firstMakerAttempt, checkNodeRunID := loop.runMaker()
	if refs := loop.snapshotEvidenceIDs(firstMakerAttempt); len(refs) != 0 {
		t.Fatalf("first maker snapshot evidence refs = %v, want none", refs)
	}
	firstPrompt := loop.prompt(loop.makerNodeRunID, firstMakerAttempt)
	if _, present := firstPrompt["checkFailures"]; present {
		t.Fatalf("first maker prompt has a checkFailures section: %s", firstPrompt["checkFailures"])
	}

	// The check fails (exit 1): the run goes back to the maker.
	checkAttempt, finalized := loop.runCheck(checkNodeRunID, &fake.ProcessSupervisor{
		Result: ports.ProcessResult{ExitCode: 1, TreeQuiesced: true},
		Stderr: strings.Repeat("noise line\n", 2000) + "--- FAIL: TestAdd (0.00s)\n    add_test.go:12: want 3, got 2\nFAIL\n",
	})
	if !finalized.Advanced || finalized.AdvanceResult.NextNodeKey != "implement" {
		t.Fatalf("finalize check = %+v, want the failureOutcome edge back to implement", finalized)
	}
	secondMakerNodeRunID := finalized.AdvanceResult.NextNodeRunID
	secondMakerAttempt, _ := loop.start(secondMakerNodeRunID)

	// ContextSnapshot: exactly the failing check attempt's evidence row.
	wantEvidenceID := checkAttempt + ":COMMAND_EXECUTION"
	refs := loop.snapshotEvidenceIDs(secondMakerAttempt)
	if len(refs) != 1 || refs[0] != wantEvidenceID {
		t.Fatalf("second maker snapshot evidence refs = %v, want [%s]", refs, wantEvidenceID)
	}

	// Prompt: a labelled section with WHAT/WHY/FIX; WHY is the BOUNDED TAIL of stderr.
	failures := decodeCheckFailures(t, loop.prompt(secondMakerNodeRunID, secondMakerAttempt))
	if len(failures) != 1 {
		t.Fatalf("checkFailures = %+v, want exactly one", failures)
	}
	failure := failures[0]
	if failure.CheckNode != "check" || len(failure.EvidenceIDs) != 1 || failure.EvidenceIDs[0] != wantEvidenceID {
		t.Fatalf("failure = %+v, want check node and the evidence id", failure)
	}
	if !strings.Contains(failure.What, "exited with code 1") {
		t.Fatalf("failure.What = %q, want it to say the command exited with code 1", failure.What)
	}
	if !strings.Contains(failure.Why, "want 3, got 2") || !strings.Contains(failure.Why, "FAIL") {
		t.Fatalf("failure.Why = %q, want the end of stderr", failure.Why)
	}
	if len(failure.Why) > 5000 {
		t.Fatalf("failure.Why is %d bytes, want it bounded (the stderr was ~22 KB)", len(failure.Why))
	}
	if !strings.Contains(failure.Why, "earlier output omitted") {
		t.Fatalf("failure.Why = %q, want the cut to be visible", failure.Why)
	}
	if strings.TrimSpace(failure.Fix) == "" {
		t.Fatal("failure.Fix is empty, want the FIX line")
	}

	// Same snapshot, same bytes: assembly stays deterministic.
	again := loop.prompt(secondMakerNodeRunID, secondMakerAttempt)
	if string(again["checkFailures"]) != string(mustMarshal(t, failures)) {
		t.Fatalf("re-assembly rendered a different section: %s", again["checkFailures"])
	}
}

func TestMakerAfterFailingGate_ReceivesFailedCriteria(t *testing.T) {
	loop := newMakerCheckLoop(t, workflow.NodeMachineGate)
	_, checkNodeRunID := loop.runMaker()

	checkAttempt, finalized := loop.runCheck(checkNodeRunID, &fake.ProcessSupervisor{
		Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true},
		Stdout: string(gateStdout(t, map[string]map[string]string{
			"UNIT_TESTS": {"verdict": "FAIL", "detail": "3 tests failed in pkg/add"},
			"LINT":       {"verdict": "PASS"},
		})),
	})
	if !finalized.Advanced || finalized.AdvanceResult.NextNodeKey != "implement" {
		t.Fatalf("finalize gate = %+v, want the failureOutcome edge back to implement", finalized)
	}
	secondMakerNodeRunID := finalized.AdvanceResult.NextNodeRunID
	secondMakerAttempt, _ := loop.start(secondMakerNodeRunID)

	// Both criteria rows of the failing attempt are pinned (one artifact covers
	// them), and the prompt shows only the criterion that did not pass.
	refs := loop.snapshotEvidenceIDs(secondMakerAttempt)
	if len(refs) != 2 || refs[0] != checkAttempt+":LINT" || refs[1] != checkAttempt+":UNIT_TESTS" {
		t.Fatalf("second maker snapshot evidence refs = %v, want the LINT and UNIT_TESTS rows of %s", refs, checkAttempt)
	}
	failures := decodeCheckFailures(t, loop.prompt(secondMakerNodeRunID, secondMakerAttempt))
	if len(failures) != 1 {
		t.Fatalf("checkFailures = %+v, want exactly one (one failing attempt, not one per criterion)", failures)
	}
	if !strings.Contains(failures[0].What, "gate verdict FAIL") || !strings.Contains(failures[0].What, "1 of 2") {
		t.Fatalf("failure.What = %q, want the verdict and the count of failed criteria", failures[0].What)
	}
	if !strings.Contains(failures[0].Why, "unit tests (UNIT_TESTS): FAIL") || !strings.Contains(failures[0].Why, "3 tests failed in pkg/add") {
		t.Fatalf("failure.Why = %q, want the failing criterion with its detail", failures[0].Why)
	}
	if strings.Contains(failures[0].Why, "LINT") {
		t.Fatalf("failure.Why = %q, must not list a criterion that passed", failures[0].Why)
	}
}

// TestMakerAfterFailingCheck_OnlyTheLatestFailureAndTheLoopIsBounded: after
// fail -> fix -> fail the next activation sees the latest failure only, and past
// maxIterations the run escalates instead of looping.
func TestMakerAfterFailingCheck_OnlyTheLatestFailureAndTheLoopIsBounded(t *testing.T) {
	loop := newMakerCheckLoop(t, workflow.NodeCommand)
	_, checkNodeRunID := loop.runMaker()

	var lastCheckAttempt string
	var finalized runtime.FinalizeExecutionAttemptResult
	for round := 1; round <= 2; round++ {
		lastCheckAttempt, finalized = loop.runCheck(checkNodeRunID, &fake.ProcessSupervisor{
			Result: ports.ProcessResult{ExitCode: 1, TreeQuiesced: true}, Stderr: "failure of round " + string(rune('0'+round)),
		})
		if finalized.AdvanceResult.NextNodeKey != "implement" {
			t.Fatalf("round %d: advanced to %q, want back to implement", round, finalized.AdvanceResult.NextNodeKey)
		}
		loop.makerNodeRunID = finalized.AdvanceResult.NextNodeRunID
		makerAttempt, checkRunID := loop.runMaker()
		refs := loop.snapshotEvidenceIDs(makerAttempt)
		if len(refs) != 1 || refs[0] != lastCheckAttempt+":COMMAND_EXECUTION" {
			t.Fatalf("round %d: maker snapshot evidence refs = %v, want only the latest failure %s", round, refs, lastCheckAttempt)
		}
		failures := decodeCheckFailures(t, loop.prompt(loop.makerNodeRunID, makerAttempt))
		if len(failures) != 1 || !strings.Contains(failures[0].Why, "failure of round "+string(rune('0'+round))) || strings.Contains(failures[0].Why, "failure of round "+string(rune('0'+round-1))) {
			t.Fatalf("round %d: checkFailures = %+v, want the latest failure only", round, failures)
		}
		checkNodeRunID = checkRunID
	}

	// The check carries the cyclePolicy (maxIterations 2), so it may run for
	// iterations 0, 1 and 2. A third failure still sends the maker back once
	// more; the fourth activation of the check is the one that is exhausted,
	// and it takes the escalation outcome without running.
	_, finalized = loop.runCheck(checkNodeRunID, &fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 1, TreeQuiesced: true}, Stderr: "still failing"})
	if finalized.AdvanceResult.NextNodeKey != "implement" {
		t.Fatalf("third failure advanced to %q, want back to implement", finalized.AdvanceResult.NextNodeKey)
	}
	loop.makerNodeRunID = finalized.AdvanceResult.NextNodeRunID
	attemptID, lease := loop.start(loop.makerNodeRunID)
	exhausted, err := runtime.FinalizeExecutionAttempt(context.Background(), loop.uow, loop.ids, clock.System{}, nil, runtime.FinalizeExecutionAttemptRequest{
		RunID: loop.runID, NodeRunID: loop.makerNodeRunID, AttemptID: attemptID, ExpectedVersion: loadAttemptVersion(t, loop.uow, attemptID),
		NextState: runtimedomain.ExecutionAttemptSucceeded, TerminationReason: runtimedomain.TerminationReasonCompleted,
		SelectedOutcome: "done", JobLease: lease, CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("finalize third maker: %v", err)
	}
	if !exhausted.AdvanceResult.CycleExhausted || exhausted.AdvanceResult.NextNodeKey != "escalated_end" {
		t.Fatalf("advance = %+v, want the cycle exhausted and the run on the escalation outcome (escalated_end)", exhausted.AdvanceResult)
	}
}

// TestCheckWithFailureOutcome_PassRoutesToTheSuccessEdge: a check that passed routes to
// its success edge.
func TestCheckWithFailureOutcome_PassRoutesToTheSuccessEdge(t *testing.T) {
	loop := newMakerCheckLoop(t, workflow.NodeCommand)
	_, checkNodeRunID := loop.runMaker()
	_, finalized := loop.runCheck(checkNodeRunID, &fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true}})
	if finalized.AdvanceResult.NextNodeKey != "end" || finalized.AdvanceResult.SelectedOutcome != "passed" {
		t.Fatalf("finalize = %+v, want the success edge to end", finalized.AdvanceResult)
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return body
}
