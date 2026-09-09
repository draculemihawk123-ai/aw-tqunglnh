package runtime_test

import (
	"context"
	"errors"
	"strings"
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
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/skill"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// commandExecutableDocument mirrors agentExecutableDocument's own exact
// node-key/edge topology (schedule_test.go), swapping the "implement"
// node's own Type/typed-config for COMMAND — scheduleFixture's own
// hardcoded "hop.NextNodeKey != implement" assertion depends on this
// shape being preserved.
func commandExecutableDocument(commandDefID, commandVersionID string, policyRefs []definition.DependencyPin) workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "implement", Type: workflow.NodeCommand, Outcomes: []string{"done"}, Command: &workflow.CommandNodeConfig{
				CommandRef: definition.DependencyPin{Kind: definition.KindCommand, DefinitionID: commandDefID, VersionID: commandVersionID},
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

// publishCommandVersion publishes a real CommandVersion through the real
// V2-10 application command (definitions.PublishDefinitionVersion) —
// mirrors publishAgentProfileVersionOnly's own exact shape
// (schedule_test.go) for internal/domain/command.Compile instead.
func publishCommandVersion(t *testing.T, uow ports.UnitOfWork, definitionID, versionID string, doc command.CommandDocument) {
	t.Helper()
	ctx := context.Background()
	createCmd := testCommand("idem-def-"+definitionID, "hash-def-"+definitionID, ports.InstallationScope(), "CreateDefinition")
	if _, err := definitions.CreateDefinition(ctx, uow, createCmd, definitions.CreateDefinitionRequest{
		DefinitionID: definitionID, Kind: definition.KindCommand, Scope: definition.GlobalScope(), Name: "command " + definitionID,
	}); err != nil {
		t.Fatalf("CreateDefinition(%s): %v", definitionID, err)
	}
	publishCmd := testCommand("idem-pub-"+versionID, "hash-pub-"+versionID, ports.InstallationScope(), "PublishDefinitionVersion")
	if _, err := definitions.PublishDefinitionVersion(ctx, uow, publishCmd, definitions.PublishDefinitionVersionRequest{
		DefinitionID: definitionID, Kind: definition.KindCommand,
		Compile: func() (definition.VersionFields, error) {
			return command.Compile(
				command.CommandDefinition{
					ID: command.CommandDefinitionID(definitionID),
					Fields: definition.Fields{
						Kind: definition.KindCommand, Scope: definition.GlobalScope(),
						Name: "command", Status: definition.StatusDraft, Version: 1,
					},
				},
				command.PublishRequest{
					VersionID: command.CommandVersionID(versionID), VersionNumber: 1, SchemaVersion: 1,
					Document: doc, PublishedBy: "operator-1", PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
				},
			)
		},
	}); err != nil {
		t.Fatalf("PublishDefinitionVersion(%s): %v", versionID, err)
	}
}

// commandFixtureOptions lets each test override just the CommandDocument
// fields it cares about; commandFixture below fills in golden-path
// defaults for the rest.
type commandFixtureOptions struct {
	argv                 []command.ArgvElement
	placeholderAllowlist []string
	cwdRepositoryTarget  string
	envAllowlist         []string
	secretRefs           []string
	timeoutSeconds       uint32
}

// commandFixture builds one fully-admitted, RUNNING-eligible ExecutionAttempt
// for a real, published CommandVersion — a real Skill resource backs the
// Command's own Executable (content-hash-verified the identical way
// AssembleAgentExecutionRequest's own Resources already are), a real
// EXECUTE_NODE JobLease is claimed (agentevents.Sink's own fencing
// requires one), and repo-1 is WRITE-scoped (seedEffectiveScope,
// scheduleFixture's own readyFixture chain) so a mutating-command test can
// exercise the exact same write-lease/cancellation path AGENT already
// does.
func commandFixture(t *testing.T, opts commandFixtureOptions) (
	uow *fake.UnitOfWork, ids idsource.Source, store ports.ArtifactStore, runID, nodeRunID, attemptID string, jobLease ports.JobLease,
) {
	t.Helper()
	ctx := context.Background()
	store = artifactstoreForTest(t)

	u, seq, rID, nrID := scheduleFixture(t, commandExecutableDocument("command-def-1", "command-v1", fullyResolvablePolicyRefs()))

	skillDoc := oneResourceSkillDocument("cmd-script", "#!/bin/sh\necho hello\n", skill.Selector{}, true)
	publishSkillVersion(t, u, "cmd-skill-def-1", "cmd-skill-v1", skillDoc)
	hash := resourceContentHash(t, "cmd-skill-v1", "cmd-script", skillDoc)

	argv := opts.argv
	if argv == nil {
		argv = []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}}
	}
	allowlist := opts.placeholderAllowlist
	if allowlist == nil {
		for _, e := range argv {
			if e.Kind == command.ArgvPlaceholder {
				allowlist = append(allowlist, e.Value)
			}
		}
	}
	cwdTarget := opts.cwdRepositoryTarget
	if cwdTarget == "" {
		cwdTarget = "repo-1"
	}
	timeoutSeconds := opts.timeoutSeconds
	if timeoutSeconds == 0 {
		timeoutSeconds = 600
	}

	publishCommandVersion(t, u, "command-def-1", "command-v1", command.CommandDocument{
		Executable:           command.ExecutableRef{OwnerVersionID: "cmd-skill-v1", ResourceKey: "cmd-script", ContentHash: hash},
		Argv:                 argv,
		PlaceholderAllowlist: allowlist,
		CwdRepositoryTarget:  cwdTarget,
		Compatibility:        command.Compatibility{OS: []string{"linux", "windows", "darwin"}},
		EnvAllowlist:         opts.envAllowlist,
		NetworkAccess:        command.NetworkAccessNone,
		SecretRefs:           opts.secretRefs,
		TimeoutSeconds:       timeoutSeconds,
		Output:               command.OutputContract{CaptureStdout: true, CaptureStderr: true, MaxOutputBytes: 65536},
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

// newTestCommandNodeExecutor wires a CommandNodeExecutor against uow/store
// plus a fresh fake.ProcessSupervisor/fake.SecretResolver a test configures
// itself — mirrors bridgeFixture's own eventschema.Registry/redact.Matcher/
// checkpoint-store construction exactly (agent_node_executor_test.go), and
// returns the same interruptions/reconciler fakes so a cancellation test
// can assert against them.
func newTestCommandNodeExecutor(
	uow *fake.UnitOfWork, ids idsource.Source, store ports.ArtifactStore, supervisor *fake.ProcessSupervisor, secrets ports.SecretResolver,
) (executor *runtime.CommandNodeExecutor, interruptions *bridgeFakeInterruptionStore, reconciler *bridgeFakeWorkspaceReconciler) {
	registry := eventschema.NewRegistry()
	agentevents.RegisterEventSchemas(registry)
	interruptions = &bridgeFakeInterruptionStore{uow: uow}
	reconciler = &bridgeFakeWorkspaceReconciler{}
	workspaces := &bridgeFakeWorkspaceProvider{
		diff:            defaultInScopeDiff(),
		captureRevision: workspace.Revision{RepositoryID: "repo-1", VCSObjectID: fixtureRepo1PinnedRevision, WorkspaceGeneration: 1},
	}
	executor = runtime.NewCommandNodeExecutor(
		uow, ids, store, workspaces, &bridgeFakeWriteLeaseManager{}, supervisor, secrets,
		registry, redact.NewMatcher(), bridgeFakeCheckpointStore{}, clock.System{}, interruptions, reconciler,
	)
	return executor, interruptions, reconciler
}

func TestCommandNodeExecutor_Success_ResolvesArgvCwdAndFinalizesEndToEnd(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := commandFixture(t, commandFixtureOptions{
		argv: []command.ArgvElement{
			{Kind: command.ArgvLiteral, Value: "run"},
			{Kind: command.ArgvPlaceholder, Value: "repo-1"},
			{Kind: command.ArgvLiteral, Value: "literal && never-run | still-one-argument"},
		},
	})
	supervisor := &fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true}}
	executor, _, _ := newTestCommandNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{})

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
		t.Fatalf("result.Evidence = %+v, want exactly one output artifact ref", result.Evidence)
	}

	if len(supervisor.Calls) != 1 {
		t.Fatalf("supervisor.Calls = %d, want exactly 1", len(supervisor.Calls))
	}
	spec := supervisor.Calls[0]
	wantArgv := []string{"run", "bridge-fixture-working-directory", "literal && never-run | still-one-argument"}
	if len(spec.Argv) != len(wantArgv) {
		t.Fatalf("spec.Argv = %#v, want %#v", spec.Argv, wantArgv)
	}
	for i := range wantArgv {
		if spec.Argv[i] != wantArgv[i] {
			t.Fatalf("spec.Argv[%d] = %q, want %q — argv must never be re-parsed through a shell", i, spec.Argv[i], wantArgv[i])
		}
	}
	if spec.WorkingDirectory != "bridge-fixture-working-directory" {
		t.Fatalf("spec.WorkingDirectory = %q, want the resolved repo-1 mount", spec.WorkingDirectory)
	}
}

func TestCommandNodeExecutor_EnvAllowlist_PassedAsInheritedEnvironment(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := commandFixture(t, commandFixtureOptions{
		envAllowlist: []string{"PATH", "HOME"},
	})
	supervisor := &fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true}}
	executor, _, _ := newTestCommandNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{})

	if _, err := executor.Execute(context.Background(), ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(supervisor.Calls) != 1 {
		t.Fatalf("supervisor.Calls = %d, want exactly 1", len(supervisor.Calls))
	}
	// EnvAllowlist is a set (command.Compile's own documentSetPaths marks
	// it as one — order carries no meaning), so the compiler is free to
	// canonicalize its order; this only checks membership, not sequence.
	got := supervisor.Calls[0].InheritedEnvironment
	gotSet := map[string]bool{}
	for _, name := range got {
		gotSet[name] = true
	}
	if len(got) != 2 || !gotSet["PATH"] || !gotSet["HOME"] {
		t.Fatalf("spec.InheritedEnvironment = %#v, want exactly {PATH, HOME}", got)
	}
}

func TestCommandNodeExecutor_SecretRef_ResolvedIntoEnvironmentNeverArgv(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := commandFixture(t, commandFixtureOptions{
		secretRefs: []string{"MY_SECRET"},
	})
	supervisor := &fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true}}
	executor, _, _ := newTestCommandNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{Values: map[string]string{"MY_SECRET": "sh-secret-value"}})

	if _, err := executor.Execute(context.Background(), ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(supervisor.Calls) != 1 {
		t.Fatalf("supervisor.Calls = %d, want exactly 1", len(supervisor.Calls))
	}
	spec := supervisor.Calls[0]
	if spec.Environment["MY_SECRET"] != "sh-secret-value" {
		t.Fatalf("spec.Environment[MY_SECRET] = %q, want the resolved secret value", spec.Environment["MY_SECRET"])
	}
	for _, arg := range spec.Argv {
		if strings.Contains(arg, "sh-secret-value") {
			t.Fatalf("spec.Argv = %#v, a secret value must never appear in argv", spec.Argv)
		}
	}
}

func TestCommandNodeExecutor_SecretUnresolvable_FailsClosedWithoutSpawning(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := commandFixture(t, commandFixtureOptions{
		secretRefs: []string{"MISSING_SECRET"},
	})
	supervisor := &fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true}}
	executor, _, _ := newTestCommandNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{})

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
		t.Fatalf("supervisor.Calls = %d, want 0 — a command must never spawn when a declared secret cannot be resolved", len(supervisor.Calls))
	}
}

func TestCommandNodeExecutor_NonzeroExit_FinalizesFailed(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := commandFixture(t, commandFixtureOptions{})
	supervisor := &fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 7, TreeQuiesced: true}}
	executor, _, _ := newTestCommandNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{})

	result, err := executor.Execute(context.Background(), ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.State != runtimedomain.ExecutionAttemptFailed || result.TerminationReason != runtimedomain.TerminationReasonExecutionFailed ||
		result.ErrorCode != errorcode.CodeExecutionFailed {
		t.Fatalf("result = %+v, want FAILED/EXECUTION_FAILED/CodeExecutionFailed", result)
	}
}

func TestCommandNodeExecutor_ProcessOwnTimeout_FinalizesFailed(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := commandFixture(t, commandFixtureOptions{})
	supervisor := &fake.ProcessSupervisor{Result: ports.ProcessResult{TimedOut: true, TreeQuiesced: true}}
	executor, _, _ := newTestCommandNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{})

	result, err := executor.Execute(context.Background(), ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.State != runtimedomain.ExecutionAttemptFailed || result.ErrorCode != errorcode.CodeTimeout {
		t.Fatalf("result = %+v, want FAILED/CodeTimeout", result)
	}
}

// TestCommandNodeExecutor_CommandOwnTimeoutTighterThanAttemptPolicy_Honored
// proves this task's own locked "tighter bound wins" decision
// (gatherCommandExecutionInputs's own doc comment): the CommandDocument's
// own self-declared TimeoutSeconds, when shorter than the AttemptPolicy's
// own timeout, is what actually reaches ProcessSpec.Timeout.
func TestCommandNodeExecutor_CommandOwnTimeoutTighterThanAttemptPolicy_Honored(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := commandFixture(t, commandFixtureOptions{timeoutSeconds: 5})
	supervisor := &fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true}}
	executor, _, _ := newTestCommandNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{})

	if _, err := executor.Execute(context.Background(), ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(supervisor.Calls) != 1 {
		t.Fatalf("supervisor.Calls = %d, want exactly 1", len(supervisor.Calls))
	}
	if supervisor.Calls[0].Timeout != 5*time.Second {
		t.Fatalf("spec.Timeout = %s, want 5s (the command's own tighter declared timeout, not the 600s attempt policy)", supervisor.Calls[0].Timeout)
	}
}

func TestCommandNodeExecutor_UnknownArgvPlaceholder_FailsClosedWithoutSpawning(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := commandFixture(t, commandFixtureOptions{
		argv:                 []command.ArgvElement{{Kind: command.ArgvPlaceholder, Value: "repo-nonexistent"}},
		placeholderAllowlist: []string{"repo-nonexistent"},
	})
	supervisor := &fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true}}
	executor, _, _ := newTestCommandNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{})

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
		t.Fatalf("supervisor.Calls = %d, want 0 — a command must never spawn when its own placeholder cannot be resolved against real scope", len(supervisor.Calls))
	}
}

func TestCommandNodeExecutor_CwdRepositoryTargetNotInScope_FailsClosedWithoutSpawning(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := commandFixture(t, commandFixtureOptions{
		cwdRepositoryTarget: "repo-nonexistent",
	})
	supervisor := &fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true}}
	executor, _, _ := newTestCommandNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{})

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
		t.Fatalf("supervisor.Calls = %d, want 0 — a command must never spawn when its own cwd target is outside real scope", len(supervisor.Calls))
	}
}

// TestCommandNodeExecutor_ProcessCancelled_ReusesV508CMutatingPath proves
// V5-09's own locked requirement: cancellation reuses V5-08C's path
// exactly, no separate terminate path. This fixture's own default scope
// grants repo-1 WRITE (seedEffectiveScope), so the mutating branch of
// classifyCancellation/handleMutatingCancellation is what a genuine
// cancellation reaches — already exhaustively tested in its own right
// (agent_node_executor_test.go's own 3 V5-08C tests); this test only
// proves CommandNodeExecutor's own wiring actually reaches it.
func TestCommandNodeExecutor_ProcessCancelled_ReusesV508CMutatingPath(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := commandFixture(t, commandFixtureOptions{})
	if _, err := runtime.CancelRun(context.Background(), uow, ids, runtime.CancelRunRequest{
		RunID: runID, Actor: "actor-1", Reason: "test: mid-execution cancel",
	}); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}

	supervisor := &fake.ProcessSupervisor{Result: ports.ProcessResult{Cancelled: true, TreeQuiesced: true}}
	executor, interruptions, reconciler := newTestCommandNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{})

	_, err := executor.Execute(context.Background(), ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	})
	if !errors.Is(err, runtime.ErrAttemptAlreadyTerminated) {
		t.Fatalf("Execute err = %v, want ErrAttemptAlreadyTerminated", err)
	}
	if len(interruptions.terminations) != 1 || interruptions.terminations[0].NextState != runtimedomain.ExecutionAttemptIndeterminate {
		t.Fatalf("interruptions.terminations = %+v, want exactly one INDETERMINATE termination", interruptions.terminations)
	}
	if len(reconciler.quarantined) != 0 {
		t.Fatalf("reconciler.quarantined = %+v, want none — the fixture's own captureRevision matches the pinned revision (clean)", reconciler.quarantined)
	}
}
