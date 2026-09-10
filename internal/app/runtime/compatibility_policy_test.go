package runtime_test

// This file is the V5-09 acceptance-gap remediation's own test suite for
// Part C of the combined PR (2026-09-10, "gộp 4 PR thành 1 luôn"): OS
// compatibility and NetworkAccess/PolicyRefs enforcement, both wired into
// verifyCommandCompatibilityAndPolicy (command_node_executor.go), called
// from gatherCommandExecutionInputs before ever spawning.

import (
	"context"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

func TestCommandNodeExecutor_IncompatibleOS_FailsClosedWithoutSpawning(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := commandFixture(t, commandFixtureOptions{
		argv:          []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}},
		compatibility: &command.Compatibility{OS: []string{"plan9"}}, // never matches the real test worker's own GOOS
	})
	supervisor := &fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true}}
	executor, _, _ := newTestCommandNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{})

	result, err := executor.Execute(context.Background(), ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.State != runtimedomain.ExecutionAttemptFailed {
		t.Fatalf("result = %+v, want FAILED — a Command declaring an OS this worker isn't must never spawn", result)
	}
	if len(supervisor.Calls) != 0 {
		t.Fatalf("supervisor.Calls = %d, want 0 — incompatible OS must fail before ever spawning", len(supervisor.Calls))
	}
}

func TestCommandNodeExecutor_NetworkAccessAllowedWithoutGrant_FailsClosedWithoutSpawning(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := commandFixture(t, commandFixtureOptions{
		argv:          []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}},
		networkAccess: command.NetworkAccessAllowed, // no policyRefs granting NETWORK_ACCESS
	})
	supervisor := &fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true}}
	executor, _, _ := newTestCommandNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{})

	result, err := executor.Execute(context.Background(), ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.State != runtimedomain.ExecutionAttemptFailed {
		t.Fatalf("result = %+v, want FAILED — NetworkAccess=ALLOWED with no granting policy must never spawn", result)
	}
	if len(supervisor.Calls) != 0 {
		t.Fatalf("supervisor.Calls = %d, want 0", len(supervisor.Calls))
	}
}

func TestCommandNodeExecutor_NetworkAccessAllowedWithGrant_Succeeds(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := commandFixture(t, commandFixtureOptions{
		argv:          []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}},
		networkAccess: command.NetworkAccessAllowed,
		policyRefs:    []definition.DependencyPin{{Kind: definition.KindPolicy, DefinitionID: "network-policy-def", VersionID: "network-policy-v1"}},
	})
	publishPolicyVersion(t, uow, "network-policy-def", "network-policy-v1", policy.PolicyDocument{
		Category: policy.CategoryPermission,
		Permission: &policy.PermissionRules{
			IsolationTier: policy.IsolationTierEnforcedIsolated, GrantedCapabilities: []string{"NETWORK_ACCESS"},
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
	if result.State != runtimedomain.ExecutionAttemptSucceeded {
		t.Fatalf("result = %+v, want SUCCEEDED — a real pinned policy grants NETWORK_ACCESS", result)
	}
	if len(supervisor.Calls) != 1 {
		t.Fatalf("supervisor.Calls = %d, want exactly 1", len(supervisor.Calls))
	}
}

func TestCommandNodeExecutor_UnresolvablePolicyRef_FailsClosedWithoutSpawning(t *testing.T) {
	uow, ids, store, runID, nodeRunID, attemptID, jobLease := commandFixture(t, commandFixtureOptions{
		argv:       []command.ArgvElement{{Kind: command.ArgvLiteral, Value: "run"}},
		policyRefs: []definition.DependencyPin{{Kind: definition.KindPolicy, DefinitionID: "missing-policy-def", VersionID: "missing-policy-v1"}},
	})
	// Deliberately never published — PolicyRefs are now read and verified
	// unconditionally, not only when NetworkAccess=ALLOWED.
	supervisor := &fake.ProcessSupervisor{Result: ports.ProcessResult{ExitCode: 0, TreeQuiesced: true}}
	executor, _, _ := newTestCommandNodeExecutor(uow, ids, store, supervisor, fake.SecretResolver{})

	result, err := executor.Execute(context.Background(), ports.NodeExecutionRequest{
		AttemptID: attemptID, NodeRunID: nodeRunID, RunID: runID, JobLease: jobLease,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.State != runtimedomain.ExecutionAttemptFailed {
		t.Fatalf("result = %+v, want FAILED — an unresolvable PolicyRef must never be silently ignored", result)
	}
	if len(supervisor.Calls) != 0 {
		t.Fatalf("supervisor.Calls = %d, want 0", len(supervisor.Calls))
	}
}
