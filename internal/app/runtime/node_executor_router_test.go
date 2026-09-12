package runtime_test

// This file is Part D of the combined V5-09/V5-10 acceptance-gap
// remediation PR (2026-09-10, "gộp 4 PR thành 1 luôn"): the review found
// no production composition ever selected a NodeExecutor by ExecutorKind —
// every existing test filled ExecuteNodeHandler's single executor slot
// with one hardcoded kind. TestExecuteNodeHandler_CommandExecutorKind_
// RoutesToRealCommandExecutorAndFinalizes below is the first test in this
// codebase that drives the REAL entry point, ExecuteNodeHandler.Handle,
// end to end through NodeExecutorRouter into a real *CommandNodeExecutor
// and a real fenced FinalizeExecutionAttempt — every other Command/Gate
// test calls executor.Execute directly and finalizes manually, never
// exercising Handle's own claim/dispatch/finalize sequence at all.
//
// A real cross-platform process-launch smoke test is deliberately NOT
// added here: internal/adapters/process/supervisor_test.go already spawns
// real child processes (TestSupervisorRunsExecutableWithoutShell et al.)
// under this repo's own Windows+Linux CI matrix — that is the actual
// "does a process really launch on this OS" contract. This package's own
// CommandNodeExecutor tests (including the one below) exist to prove
// orchestration/fencing logic against a fake.ProcessSupervisor, exactly
// like every other CommandNodeExecutor test already does; duplicating a
// real spawn here would test the adapter a second time, not the router.

import (
	"context"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/skill"
)

// commandRouterExecutionFixture mirrors commandFixture's own publish steps
// (command_node_executor_test.go) but stops right after
// ScheduleExecutableNodeRun, leaving the Attempt QUEUED behind the ONE
// real EXECUTE_NODE job that call itself already enqueues (schedule.go) —
// unlike commandFixture, this fixture must NOT pre-claim RUNNING or
// fabricate its own job/lease, since the whole point here is to drive
// ExecuteNodeHandler.Handle's own claim step for real.
func commandRouterExecutionFixture(t *testing.T) (
	uow *fake.UnitOfWork, ids idsource.Source, store ports.ArtifactStore, runID, nodeRunID, attemptID string,
) {
	t.Helper()
	ctx := context.Background()
	store = artifactstoreForTest(t)

	u, seq, rID, nrID := scheduleFixture(t, commandExecutableDocument("router-command-def-1", "router-command-v1", fullyResolvablePolicyRefs()))

	skillDoc := oneResourceSkillDocument("router-cmd-script", "#!/bin/sh\necho hello\n", skill.Selector{}, true)
	publishSkillVersion(t, u, "router-cmd-skill-def-1", "router-cmd-skill-v1", skillDoc)
	hash := resourceContentHash(t, "router-cmd-skill-v1", "router-cmd-script", skillDoc)

	publishCommandVersion(t, u, "router-command-def-1", "router-command-v1", command.CommandDocument{
		Executable:          command.ExecutableRef{OwnerVersionID: "router-cmd-skill-v1", ResourceKey: "router-cmd-script", ContentHash: hash},
		Argv:                []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}},
		CwdRepositoryTarget: "repo-1",
		Compatibility:       command.Compatibility{OS: []string{"linux", "windows", "darwin"}},
		NetworkAccess:       command.NetworkAccessNone,
		TimeoutSeconds:      600,
		Output:              command.OutputContract{CaptureStdout: true, CaptureStderr: true, MaxOutputBytes: 65536},
	})
	publishPolicyVersion(t, u, "attempt-policy-def", "attempt-policy-v1", attemptPolicyDocument(600))
	publishPolicyVersion(t, u, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())

	scheduled, err := runtime.ScheduleExecutableNodeRun(ctx, u, seq, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: rID, NodeRunID: nrID, CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("ScheduleExecutableNodeRun: %v", err)
	}
	return u, seq, store, rID, nrID, scheduled.AttemptID
}

func TestExecuteNodeHandler_CommandExecutorKind_RoutesToRealCommandExecutorAndFinalizes(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID := commandRouterExecutionFixture(t)
	job := claimableExecuteNodeJob(t, uow, attemptID)

	supervisor := &fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true}}
	commandExecutor, _, _ := newTestCommandNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{})
	router := &runtime.NodeExecutorRouter{Command: commandExecutor}

	handler := runtime.NewExecuteNodeHandler(uow, ids, router, clock.System{}, fake.IsolationEnforcementChecker{}, sharedTestAgentRegistry(t), &bridgeFakeWriteLeaseManager{})
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	attempt, err := uow.Snapshot.Runtime().GetExecutionAttempt(context.Background(), attemptID)
	if err != nil {
		t.Fatalf("GetExecutionAttempt: %v", err)
	}
	if attempt.State != runtimedomain.ExecutionAttemptSucceeded || attempt.TerminationReason != runtimedomain.TerminationReasonCompleted {
		t.Fatalf("attempt = %+v, want SUCCEEDED/COMPLETED", attempt)
	}

	nodeRun, err := uow.Snapshot.Runtime().GetNodeRun(context.Background(), nodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun: %v", err)
	}
	if nodeRun.State != runtimedomain.NodeRunSucceeded || nodeRun.SelectedOutcome != "done" {
		t.Fatalf("node run = %+v, want SUCCEEDED with outcome done", nodeRun)
	}
	if len(supervisor.Calls) != 1 {
		t.Fatalf("supervisor.Calls = %d, want exactly 1 — the router must actually dispatch through to the real CommandNodeExecutor", len(supervisor.Calls))
	}
	_ = runID
}

func TestNodeExecutorRouter_DispatchesToMatchingExecutorOnly(t *testing.T) {
	agent := &fake.NodeExecutor{Result: ports.NodeExecutionResult{State: runtimedomain.ExecutionAttemptSucceeded}}
	cmd := &fake.NodeExecutor{Result: ports.NodeExecutionResult{State: runtimedomain.ExecutionAttemptSucceeded}}
	gate := &fake.NodeExecutor{Result: ports.NodeExecutionResult{State: runtimedomain.ExecutionAttemptSucceeded}}
	router := &runtime.NodeExecutorRouter{Agent: agent, Command: cmd, Gate: gate}

	if _, err := router.Execute(context.Background(), ports.NodeExecutionRequest{ExecutorKind: string(runtimedomain.ExecutorKindCommand)}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if cmd.Calls != 1 || agent.Calls != 0 || gate.Calls != 0 {
		t.Fatalf("calls = agent:%d command:%d gate:%d, want command:1 and the other two untouched", agent.Calls, cmd.Calls, gate.Calls)
	}
}

func TestNodeExecutorRouter_UnrecognizedKind_FailsClosedWithoutPanicking(t *testing.T) {
	router := &runtime.NodeExecutorRouter{}
	if _, err := router.Execute(context.Background(), ports.NodeExecutionRequest{ExecutorKind: "BOGUS"}); err == nil {
		t.Fatal("Execute: want error for an unrecognized ExecutorKind, got nil")
	}
}

func TestNodeExecutorRouter_RecognizedButUnwiredKind_FailsClosedWithoutPanicking(t *testing.T) {
	router := &runtime.NodeExecutorRouter{} // Command deliberately left nil
	if _, err := router.Execute(context.Background(), ports.NodeExecutionRequest{ExecutorKind: string(runtimedomain.ExecutorKindCommand)}); err == nil {
		t.Fatal("Execute: want error for a recognized-but-unwired kind, got nil")
	}
}
