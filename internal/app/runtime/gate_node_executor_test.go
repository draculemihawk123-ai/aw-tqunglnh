package runtime_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/agentevents"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/definitions"
	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/skill"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// gateExecutableDocument mirrors commandExecutableDocument's own exact
// node-key/edge topology, swapping the "implement" node's own
// Type/typed-config for MACHINE_GATE.
func gateExecutableDocument(gateDefID, gateVersionID string, policyRefs []definition.DependencyPin) workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "implement", Type: workflow.NodeMachineGate, Outcomes: []string{"done"}, MachineGate: &workflow.MachineGateNodeConfig{
				GateRef:    definition.DependencyPin{Kind: definition.KindGate, DefinitionID: gateDefID, VersionID: gateVersionID},
				PolicyRefs: policyRefs,
			}},
			{Key: "end", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-to-implement", From: "start", Outcome: "next", To: "implement"},
			{Key: "implement-to-end", From: "implement", Outcome: "done", To: "end"},
		},
	}
}

// publishGateVersion publishes a real GateVersion through the real V2-10
// application command — mirrors publishCommandVersion's own exact shape
// for internal/domain/gate.Compile instead.
func publishGateVersion(t *testing.T, uow ports.UnitOfWork, definitionID, versionID string, doc gate.GateDocument) {
	t.Helper()
	ctx := context.Background()
	createCmd := testCommand("idem-def-"+definitionID, "hash-def-"+definitionID, ports.InstallationScope(), "CreateDefinition")
	if _, err := definitions.CreateDefinition(ctx, uow, createCmd, definitions.CreateDefinitionRequest{
		DefinitionID: definitionID, Kind: definition.KindGate, Scope: definition.GlobalScope(), Name: "gate " + definitionID,
	}); err != nil {
		t.Fatalf("CreateDefinition(%s): %v", definitionID, err)
	}
	publishCmd := testCommand("idem-pub-"+versionID, "hash-pub-"+versionID, ports.InstallationScope(), "PublishDefinitionVersion")
	if _, err := definitions.PublishDefinitionVersion(ctx, uow, publishCmd, definitions.PublishDefinitionVersionRequest{
		DefinitionID: definitionID, Kind: definition.KindGate,
		Compile: func() (definition.VersionFields, error) {
			return gate.Compile(
				gate.GateDefinition{
					ID: gate.GateDefinitionID(definitionID),
					Fields: definition.Fields{
						Kind: definition.KindGate, Scope: definition.GlobalScope(),
						Name: "gate", Status: definition.StatusDraft, Version: 1,
					},
				},
				gate.PublishRequest{
					VersionID: gate.GateVersionID(versionID), VersionNumber: 1, SchemaVersion: 1,
					Document: doc, PublishedBy: "operator-1", PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				},
			)
		},
	}); err != nil {
		t.Fatalf("PublishDefinitionVersion(%s): %v", versionID, err)
	}
}

// gateFixtureOptions lets each test override just the GateDocument's own
// criteria; gateFixture below fills in golden-path defaults for the rest.
type gateFixtureOptions struct {
	criteria []gate.Criterion
}

// gateFixture builds one fully-admitted, RUNNING-eligible ExecutionAttempt
// for a real, published GateVersion pinning a real, published
// CommandVersion (the evaluator) — mirrors commandFixture's own exact
// shape (command_node_executor_test.go).
func gateFixture(t *testing.T, opts gateFixtureOptions) (
	uow *fake.UnitOfWork, ids idsource.Source, store ports.ArtifactStore, runID, nodeRunID, attemptID string, jobLease ports.JobLease,
) {
	t.Helper()
	ctx := context.Background()
	store = artifactstoreForTest(t)

	u, seq, rID, nrID := scheduleFixture(t, gateExecutableDocument("gate-def-1", "gate-v1", fullyResolvablePolicyRefs()))

	skillDoc := oneResourceSkillDocument("gate-evaluator", "#!/bin/sh\necho '{}'\n", skill.Selector{}, true)
	publishSkillVersion(t, u, "gate-skill-def-1", "gate-skill-v1", skillDoc)
	hash := resourceContentHash(t, "gate-skill-v1", "gate-evaluator", skillDoc)

	publishCommandVersion(t, u, "gate-command-def-1", "gate-command-v1", command.CommandDocument{
		Executable:          command.ExecutableRef{OwnerVersionID: "gate-skill-v1", ResourceKey: "gate-evaluator", ContentHash: hash},
		Argv:                []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "evaluate"}},
		CwdRepositoryTarget: "repo-1",
		Compatibility:       command.Compatibility{OS: []string{"linux", "windows", "darwin"}},
		NetworkAccess:       command.NetworkAccessNone,
		TimeoutSeconds:      600,
		Output:              command.OutputContract{CaptureStdout: true, CaptureStderr: true, MaxOutputBytes: 65536},
	})

	criteria := opts.criteria
	if criteria == nil {
		criteria = []gate.Criterion{{Name: "lint", EvidenceKey: "lint"}}
	}
	publishGateVersion(t, u, "gate-def-1", "gate-v1", gate.GateDocument{
		CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: "gate-command-def-1", VersionID: "gate-command-v1"},
		Criteria:   criteria,
	})
	publishPolicyVersion(t, u, "attempt-policy-def", "attempt-policy-v1", attemptPolicyDocument(600))
	publishPolicyVersion(t, u, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())

	scheduled, err := runtime.ScheduleExecutableNodeRun(ctx, u, seq, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: rID, NodeRunID: nrID, CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("ScheduleExecutableNodeRun: %v", err)
	}
	attemptID = scheduled.AttemptID
	markAttemptRunning(t, u, rID, nrID, attemptID)

	jobID := ports.JobID("job-" + attemptID)
	if err := u.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
			ID: jobID, ProjectID: "project-1", Kind: "EXECUTE_NODE",
			AggregateType: "ExecutionAttempt", AggregateID: attemptID, IdempotencyKey: "idem-" + attemptID,
		})
		return err
	}); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	jobLease = ports.JobLease{JobID: jobID, Owner: "worker-1", Token: 1, LeaseUntil: time.Now().Add(time.Hour)}
	u.Snapshot.Jobs().(*fake.JobsRepository).SetActiveLease(string(jobID), jobLease)

	return u, seq, store, rID, nrID, attemptID, jobLease
}

func newTestGateNodeExecutor(
	uow *fake.UnitOfWork, ids idsource.Source, store ports.ArtifactStore, supervisor *fake.ProcessSupervisor, secrets ports.SecretResolver,
) (executor *runtime.GateNodeExecutor, interruptions *bridgeFakeInterruptionStore, reconciler *bridgeFakeWorkspaceReconciler) {
	registry := eventschema.NewRegistry()
	agentevents.RegisterEventSchemas(registry)
	interruptions = &bridgeFakeInterruptionStore{uow: uow}
	reconciler = &bridgeFakeWorkspaceReconciler{}
	workspaces := &bridgeFakeWorkspaceProvider{
		diff:            defaultInScopeDiff(),
		captureRevision: workspace.Revision{RepositoryID: "repo-1", VCSObjectID: fixtureRepo1PinnedRevision, WorkspaceGeneration: 1},
	}
	executor = runtime.NewGateNodeExecutor(
		uow, ids, store, workspaces, supervisor, secrets, registry, redact.NewMatcher(), bridgeFakeCheckpointStore{}, clock.System{},
		interruptions, reconciler,
	)
	return executor, interruptions, reconciler
}

func gateStdout(t *testing.T, entries map[string]map[string]string) []byte {
	t.Helper()
	out := make(map[string]struct {
		Verdict string `json:"verdict"`
		Detail  string `json:"detail,omitempty"`
		Reason  string `json:"reason,omitempty"`
	}, len(entries))
	for key, fields := range entries {
		out[key] = struct {
			Verdict string `json:"verdict"`
			Detail  string `json:"detail,omitempty"`
			Reason  string `json:"reason,omitempty"`
		}{Verdict: fields["verdict"], Detail: fields["detail"], Reason: fields["reason"]}
	}
	body, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal gate stdout fixture: %v", err)
	}
	return body
}

func TestGateNodeExecutor_AllCriteriaPass_FinalizesSucceeded(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := gateFixture(t, gateFixtureOptions{})
	supervisor := &fake.ProcessSupervisor{
		Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true},
		Stdout: string(gateStdout(t, map[string]map[string]string{"lint": {"verdict": "PASS"}})),
	}
	executor, _, _ := newTestGateNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{})

	result, err := executor.Execute(context.Background(), ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.State != runtimedomain.ExecutionAttemptSucceeded || result.SelectedOutcome != "done" {
		t.Fatalf("result = %+v, want SUCCEEDED with outcome done", result)
	}
	if result.Evidence == nil || len(result.Evidence.OutputArtifactRefs) != 1 {
		t.Fatalf("result.Evidence = %+v, want exactly one gate-result artifact ref", result.Evidence)
	}
	if len(supervisor.Calls) != 1 {
		t.Fatalf("supervisor.Calls = %d, want exactly 1", len(supervisor.Calls))
	}
	// Scratch cwd must never be a real repository mount.
	if supervisor.Calls[0].WorkingDirectory == "bridge-fixture-working-directory" {
		t.Fatalf("spec.WorkingDirectory = %q, want a scratch directory, never the repository's own real mount", supervisor.Calls[0].WorkingDirectory)
	}
}

func TestGateNodeExecutor_OneCriterionFails_FinalizesFailed(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := gateFixture(t, gateFixtureOptions{
		criteria: []gate.Criterion{{Name: "lint", EvidenceKey: "lint"}, {Name: "tests", EvidenceKey: "tests"}},
	})
	supervisor := &fake.ProcessSupervisor{
		Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true},
		Stdout: string(gateStdout(t, map[string]map[string]string{
			"lint":  {"verdict": "PASS"},
			"tests": {"verdict": "FAIL", "detail": "3 tests failed"},
		})),
	}
	executor, _, _ := newTestGateNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{})

	result, err := executor.Execute(context.Background(), ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.State != runtimedomain.ExecutionAttemptFailed || result.ErrorCode != errorcode.CodeExecutionFailed {
		t.Fatalf("result = %+v, want FAILED/CodeExecutionFailed", result)
	}
}

func TestGateNodeExecutor_NonzeroExit_AllCriteriaError(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := gateFixture(t, gateFixtureOptions{})
	supervisor := &fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 1, TreeQuiesced: true}}
	executor, _, _ := newTestGateNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{})

	result, err := executor.Execute(context.Background(), ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.State != runtimedomain.ExecutionAttemptFailed || result.ErrorCode != errorcode.CodeExecutionFailed {
		t.Fatalf("result = %+v, want FAILED/CodeExecutionFailed — a nonzero exit can never leave any criterion trusted enough to PASS", result)
	}
}

func TestGateNodeExecutor_MissingOutput_AllCriteriaError(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := gateFixture(t, gateFixtureOptions{})
	supervisor := &fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true}} // no Stdout set
	executor, _, _ := newTestGateNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{})

	result, err := executor.Execute(context.Background(), ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.State != runtimedomain.ExecutionAttemptFailed {
		t.Fatalf("result = %+v, want FAILED — missing evidence must never resolve to PASS", result)
	}
}

func TestGateNodeExecutor_MissingCriterionKey_ResolvesNotRun(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := gateFixture(t, gateFixtureOptions{
		criteria: []gate.Criterion{{Name: "lint", EvidenceKey: "lint"}},
	})
	supervisor := &fake.ProcessSupervisor{
		Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true},
		Stdout: "{}", // valid JSON, but never mentions "lint"
	}
	executor, _, _ := newTestGateNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{})

	result, err := executor.Execute(context.Background(), ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.State != runtimedomain.ExecutionAttemptFailed {
		t.Fatalf("result = %+v, want FAILED — a criterion never reported by the evaluator resolves NOT_RUN, never PASS", result)
	}
}

func TestGateNodeExecutor_NotApplicableWithoutReason_FinalizesFailed(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := gateFixture(t, gateFixtureOptions{})
	supervisor := &fake.ProcessSupervisor{
		Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true},
		Stdout: string(gateStdout(t, map[string]map[string]string{"lint": {"verdict": "NOT_APPLICABLE"}})), // no reason
	}
	executor, _, _ := newTestGateNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{})

	result, err := executor.Execute(context.Background(), ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.State != runtimedomain.ExecutionAttemptFailed {
		t.Fatalf("result = %+v, want FAILED — NOT_APPLICABLE with no reason must never be trusted", result)
	}
}

func TestGateNodeExecutor_NotApplicableWithReason_CountsAsPass(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := gateFixture(t, gateFixtureOptions{
		criteria: []gate.Criterion{{Name: "lint", EvidenceKey: "lint"}, {Name: "docs", EvidenceKey: "docs"}},
	})
	supervisor := &fake.ProcessSupervisor{
		Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true},
		Stdout: string(gateStdout(t, map[string]map[string]string{
			"lint": {"verdict": "PASS"},
			"docs": {"verdict": "NOT_APPLICABLE", "reason": "no docs changed in this revision"},
		})),
	}
	executor, _, _ := newTestGateNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{})

	result, err := executor.Execute(context.Background(), ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.State != runtimedomain.ExecutionAttemptSucceeded {
		t.Fatalf("result = %+v, want SUCCEEDED — NOT_APPLICABLE with a reason must never count against the overall verdict", result)
	}
}

func TestGateNodeExecutor_UnrecognizedVerdict_FinalizesFailed(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := gateFixture(t, gateFixtureOptions{})
	supervisor := &fake.ProcessSupervisor{
		Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true},
		Stdout: string(gateStdout(t, map[string]map[string]string{"lint": {"verdict": "MAYBE"}})),
	}
	executor, _, _ := newTestGateNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{})

	result, err := executor.Execute(context.Background(), ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.State != runtimedomain.ExecutionAttemptFailed {
		t.Fatalf("result = %+v, want FAILED — an unrecognized verdict string is tamper/malformed, never trusted", result)
	}
}

func TestGateNodeExecutor_StaleRevision_FailsClosedWithoutSpawning(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := gateFixture(t, gateFixtureOptions{})
	supervisor := &fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true}}
	registry := eventschema.NewRegistry()
	agentevents.RegisterEventSchemas(registry)
	// captureRevision deliberately does NOT match fixtureRepo1PinnedRevision
	// — simulating the repository having moved on since this Attempt's own
	// ContextSnapshot pinned it.
	workspaces := &bridgeFakeWorkspaceProvider{
		diff:            defaultInScopeDiff(),
		captureRevision: workspace.Revision{RepositoryID: "repo-1", VCSObjectID: "0000000000000000000000000000000000dead", WorkspaceGeneration: 1},
	}
	executor := runtime.NewGateNodeExecutor(
		uow, ids, store, workspaces, supervisor, fake.SecretResolver{}, registry, redact.NewMatcher(), bridgeFakeCheckpointStore{}, clock.System{},
		&bridgeFakeInterruptionStore{uow: uow}, &bridgeFakeWorkspaceReconciler{},
	)

	result, err := executor.Execute(context.Background(), ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.State != runtimedomain.ExecutionAttemptFailed || result.ErrorCode != errorcode.CodeValidationFailed {
		t.Fatalf("result = %+v, want FAILED/VALIDATION_FAILED", result)
	}
	if len(supervisor.Calls) != 0 {
		t.Fatalf("supervisor.Calls = %d, want 0 — a gate must never evaluate against a stale revision", len(supervisor.Calls))
	}
}

func TestGateNodeExecutor_ProcessCancelled_ReusesV508CReadOnlyPath(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := gateFixture(t, gateFixtureOptions{})
	if _, err := runtime.CancelRun(context.Background(), uow, ids, runtime.CancelRunRequest{
		RunID: runID, Actor: "actor-1", Reason: "test: mid-execution cancel",
	}); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}

	supervisor := &fake.ProcessSupervisor{Result: ports.ProcessResult{Cancelled: true, TreeQuiesced: true}}
	executor, interruptions, reconciler := newTestGateNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{})

	result, err := executor.Execute(context.Background(), ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.State != runtimedomain.ExecutionAttemptCancelled || result.TerminationReason != runtimedomain.TerminationReasonRunCancelled {
		t.Fatalf("result = %+v, want CANCELLED/RUN_CANCELLED — a gate is always read-only, so classifyCancellation's own simple branch must resolve it directly, never the mutating one", result)
	}
	if len(interruptions.terminations) != 0 || len(reconciler.quarantined) != 0 {
		t.Fatalf("interruptions/reconciler = %+v/%+v, want neither ever touched — a gate is structurally never a mutating attempt", interruptions.terminations, reconciler.quarantined)
	}
}
