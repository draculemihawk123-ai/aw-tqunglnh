package runtime_test

import (
	"context"
	"errors"
	"os"
	stdruntime "runtime"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	domainadapterbuild "github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// admissionFixtureOptions customizes the shared scheduling fixture for one
// admission check at a time — each test below fixes every OTHER axis at
// its trivially-satisfied default (isolation enforceable, no adapter build
// pinned, no required capability, exactly one write-scoped repository) and
// varies exactly the one axis it means to exercise.
type admissionFixtureOptions struct {
	requiredCapabilities   []string
	grantedCapabilities    []string
	extraWriteRepositories []string
	adapterBuild           *domainadapterbuild.Build
}

func admissionFixture(t *testing.T, opts admissionFixtureOptions) (uow *fake.UnitOfWork, ids idsource.Source, runID, nodeRunID, attemptID string) {
	t.Helper()
	granted := opts.grantedCapabilities
	if granted == nil {
		granted = []string{"INTEGRATION_MULTI_REPOSITORY_WRITE"}
	}
	var adapterBuildID *string
	if opts.adapterBuild != nil {
		id := opts.adapterBuild.ID()
		adapterBuildID = &id
	}

	uow, ids, runID, nodeRunID = scheduleFixture(t, agentExecutableDocument("agent-profile-v1", fullyResolvablePolicyRefs(), adapterBuildID))

	if opts.adapterBuild != nil {
		// Must be registered BEFORE scheduling: resolveExecutionProfile
		// (schedule.go) fails the whole scheduling transaction closed if
		// a declared AdapterBuildID cannot be resolved.
		if err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
			_, _, err := tx.AdapterBuilds().InsertIfAbsent(context.Background(), *opts.adapterBuild)
			return err
		}); err != nil {
			t.Fatalf("register pinned adapter build: %v", err)
		}
	}

	agentDoc := validAgentProfileDocument()
	agentDoc.RequiredCapabilities = opts.requiredCapabilities
	publishAgentProfileVersion(t, uow, "agent-profile-def", "agent-profile-v1", agentDoc)
	publishPolicyVersion(t, uow, "attempt-policy-def", "attempt-policy-v1", attemptPolicyDocument(600))
	publishPolicyVersion(t, uow, "permission-policy-def", "permission-policy-v1", policy.PolicyDocument{
		Category:   policy.CategoryPermission,
		Permission: &policy.PermissionRules{IsolationTier: policy.IsolationTierEnforcedIsolated, GrantedCapabilities: granted},
	})

	if len(opts.extraWriteRepositories) > 0 {
		var run runtimedomain.WorkflowRun
		if err := uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
			var err error
			run, err = tx.Runtime().GetWorkflowRun(context.Background(), runID)
			return err
		}); err != nil {
			t.Fatalf("load workflow run: %v", err)
		}
		for _, repo := range opts.extraWriteRepositories {
			mustCreateActiveRepository(t, uow, ids, "project-1", repo)
			seedEffectiveScope(t, uow, string(run.WorkItemID), repo)
		}
	}

	scheduled, err := runtime.ScheduleExecutableNodeRun(context.Background(), uow, ids, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: runID, NodeRunID: nodeRunID, CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("ScheduleExecutableNodeRun: %v", err)
	}
	return uow, ids, runID, nodeRunID, scheduled.AttemptID
}

func TestAdmission_IsolationUnavailable_BlocksBeforeSpawn(t *testing.T) {
	uow, ids, _, nodeRunID, attemptID := admissionFixture(t, admissionFixtureOptions{})
	job := claimableExecuteNodeJob(t, uow, attemptID)

	executor := &fake.NodeExecutor{}
	handler := runtime.NewExecuteNodeHandler(
		uow, ids, executor, clock.System{},
		fake.IsolationEnforcementChecker{Err: errors.New("no real OS enforcement")}, agentregistry.Empty(),
	)
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	assertBlockedReason(t, uow, attemptID, nodeRunID, runtimedomain.TerminationReasonIsolationEnforcementUnavailable, workdomain.BlockerIsolationEnforcementUnavailable)
	if executor.Calls != 0 {
		t.Fatalf("executor.Calls = %d, want 0", executor.Calls)
	}
}

func TestAdmission_AdapterBuildDrift_BlocksBeforeSpawn(t *testing.T) {
	executablePath := writeAdmissionExecutable(t, "binary-v1")
	capabilities := ports.AgentCapabilities{
		Provider: ports.ProviderClaude, AdapterVersion: "claude-stream-json/v1", ProtocolVersion: "claude-stream-json/v1",
		SupportsStart: true, SupportsResume: true, SupportsCancel: true,
	}
	build := admissionPinnedBuild(t, executablePath, capabilities)

	uow, ids, _, nodeRunID, attemptID := admissionFixture(t, admissionFixtureOptions{adapterBuild: &build})

	// Drift: the executable changes after the build was pinned and
	// scheduled.
	if err := os.WriteFile(executablePath, []byte("binary-v2-swapped"), 0o755); err != nil {
		t.Fatalf("swap fixture: %v", err)
	}

	job := claimableExecuteNodeJob(t, uow, attemptID)
	executor := &fake.NodeExecutor{}
	registry, err := agentregistry.New(context.Background(), &fake.AgentExecutor{CapabilitiesResult: capabilities})
	if err != nil {
		t.Fatalf("agentregistry.New: %v", err)
	}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{}, fake.IsolationEnforcementChecker{}, registry)
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	assertBlockedReason(t, uow, attemptID, nodeRunID, runtimedomain.TerminationReasonAdapterBuildDrift, workdomain.BlockerAdapterBuildDrift)
	if executor.Calls != 0 {
		t.Fatalf("executor.Calls = %d, want 0", executor.Calls)
	}
}

func TestAdmission_CapabilityRequirementUnsatisfied_BlocksBeforeSpawn(t *testing.T) {
	uow, ids, _, nodeRunID, attemptID := admissionFixture(t, admissionFixtureOptions{
		requiredCapabilities: []string{"SANDBOXED_FILESYSTEM"},
		grantedCapabilities:  []string{"INTEGRATION_MULTI_REPOSITORY_WRITE"}, // does not include SANDBOXED_FILESYSTEM
	})
	job := claimableExecuteNodeJob(t, uow, attemptID)

	executor := &fake.NodeExecutor{}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{}, fake.IsolationEnforcementChecker{}, agentregistry.Empty())
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	assertBlockedReason(t, uow, attemptID, nodeRunID, runtimedomain.TerminationReasonCapabilityRequirementUnsatisfied, workdomain.BlockerCapabilityRequirementUnsatisfied)
	if executor.Calls != 0 {
		t.Fatalf("executor.Calls = %d, want 0", executor.Calls)
	}
}

func TestAdmission_MultiRepositoryWriteWithoutGrant_BlocksBeforeSpawn(t *testing.T) {
	uow, ids, _, nodeRunID, attemptID := admissionFixture(t, admissionFixtureOptions{
		grantedCapabilities:    []string{}, // no INTEGRATION_MULTI_REPOSITORY_WRITE
		extraWriteRepositories: []string{"repo-2"},
	})
	job := claimableExecuteNodeJob(t, uow, attemptID)

	executor := &fake.NodeExecutor{}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{}, fake.IsolationEnforcementChecker{}, agentregistry.Empty())
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	assertBlockedReason(t, uow, attemptID, nodeRunID, runtimedomain.TerminationReasonWriteCapabilityOrGrantMissing, workdomain.BlockerWriteCapabilityOrGrantMissing)
	if executor.Calls != 0 {
		t.Fatalf("executor.Calls = %d, want 0", executor.Calls)
	}
}

// TestAdmission_MultiplyFailingChecks_RecordsHighestPriorityReason proves
// the fixed priority order (confirmed with the user before writing this
// file): isolation > adapter drift > capability > multi-repo-write. Here
// isolation AND the multi-repo grant both fail at once; only isolation's
// own reason is ever recorded.
func TestAdmission_MultiplyFailingChecks_RecordsHighestPriorityReason(t *testing.T) {
	uow, ids, _, nodeRunID, attemptID := admissionFixture(t, admissionFixtureOptions{
		grantedCapabilities:    []string{},
		extraWriteRepositories: []string{"repo-2"},
	})
	job := claimableExecuteNodeJob(t, uow, attemptID)

	executor := &fake.NodeExecutor{}
	handler := runtime.NewExecuteNodeHandler(
		uow, ids, executor, clock.System{},
		fake.IsolationEnforcementChecker{Err: errors.New("no real OS enforcement")}, agentregistry.Empty(),
	)
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	assertBlockedReason(t, uow, attemptID, nodeRunID, runtimedomain.TerminationReasonIsolationEnforcementUnavailable, workdomain.BlockerIsolationEnforcementUnavailable)
}

func TestAdmission_AllChecksPass_ProceedsToRunning(t *testing.T) {
	uow, ids, _, _, attemptID := admissionFixture(t, admissionFixtureOptions{})
	job := claimableExecuteNodeJob(t, uow, attemptID)

	executor := &fake.NodeExecutor{Result: ports.NodeExecutionResult{State: runtimedomain.ExecutionAttemptSucceeded, SelectedOutcome: "done"}}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{}, fake.IsolationEnforcementChecker{}, agentregistry.Empty())
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if executor.Calls != 1 {
		t.Fatalf("executor.Calls = %d, want 1 (admission passed, execution should proceed)", executor.Calls)
	}
}

// assertBlockedReason reloads the Attempt/NodeRun/WorkItemBlocker and
// proves the whole admission-blocker group landed atomically: Attempt
// QUEUED->BLOCKED with the exact reason, StartedAt empty, NodeRun also
// BLOCKED (never left stuck QUEUED), and a matching, OPEN WorkItemBlocker
// row exists.
func assertBlockedReason(
	t *testing.T, uow *fake.UnitOfWork, attemptID, nodeRunID string,
	wantReason runtimedomain.TerminationReason, wantBlockerType workdomain.BlockerType,
) {
	t.Helper()
	ctx := context.Background()
	var attempt runtimedomain.ExecutionAttempt
	var nodeRun runtimedomain.NodeRun
	var blockers []workdomain.WorkItemBlocker
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		attempt, err = tx.Runtime().GetExecutionAttempt(ctx, attemptID)
		if err != nil {
			return err
		}
		nodeRun, err = tx.Runtime().GetNodeRun(ctx, nodeRunID)
		if err != nil {
			return err
		}
		run, err := tx.Runtime().GetWorkflowRun(ctx, string(nodeRun.RunID))
		if err != nil {
			return err
		}
		blockers, err = tx.Work().ListWorkItemBlockersForWorkItem(ctx, string(run.WorkItemID))
		return err
	}); err != nil {
		t.Fatalf("reload attempt/node run/blockers: %v", err)
	}

	if attempt.State != runtimedomain.ExecutionAttemptBlocked {
		t.Fatalf("attempt.State = %s, want BLOCKED", attempt.State)
	}
	if attempt.TerminationReason != wantReason {
		t.Fatalf("attempt.TerminationReason = %v, want %s", attempt.TerminationReason, wantReason)
	}
	if attempt.StartedAt != nil {
		t.Fatalf("attempt.StartedAt = %v, want nil (never started)", *attempt.StartedAt)
	}
	if nodeRun.State != runtimedomain.NodeRunBlocked {
		t.Fatalf("nodeRun.State = %s, want BLOCKED", nodeRun.State)
	}
	found := false
	for _, blocker := range blockers {
		if blocker.Type == wantBlockerType && blocker.SourceAttemptID == attemptID {
			found = true
			if blocker.State != workdomain.BlockerOpen {
				t.Fatalf("blocker.State = %s, want OPEN", blocker.State)
			}
		}
	}
	if !found {
		t.Fatalf("no %s blocker found for attempt %s among %+v", wantBlockerType, attemptID, blockers)
	}
}

// TestRetryBlockedActivation_CreatesNewAttemptNeverRevivesOld is V5-08's
// own required test (the full RetryBlockedActivation command is V5-08D's
// scope): after an admission-blocked Attempt's own cause is fixed, a new
// NodeRun activation (fresh ID, ActivationSequence+1, same NodeKey) goes
// through the ordinary ScheduleExecutableNodeRun pipeline unchanged and
// gets its own new Attempt — the original BLOCKED Attempt is never
// revived, mirroring reactivateBlockedNodeRunTx's own exact shape
// (scope_expansion.go) for the OTHER blocker group.
func TestRetryBlockedActivation_CreatesNewAttemptNeverRevivesOld(t *testing.T) {
	uow, ids, runID, nodeRunID, attemptID := admissionFixture(t, admissionFixtureOptions{})
	job := claimableExecuteNodeJob(t, uow, attemptID)

	blockedExecutor := &fake.NodeExecutor{}
	blockingChecker := fake.IsolationEnforcementChecker{Err: errors.New("no real OS enforcement")}
	handler := runtime.NewExecuteNodeHandler(uow, ids, blockedExecutor, clock.System{}, blockingChecker, agentregistry.Empty())
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle (first, blocked): %v", err)
	}
	assertBlockedReason(t, uow, attemptID, nodeRunID, runtimedomain.TerminationReasonIsolationEnforcementUnavailable, workdomain.BlockerIsolationEnforcementUnavailable)

	ctx := context.Background()
	var blocked runtimedomain.NodeRun
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		blocked, err = tx.Runtime().GetNodeRun(ctx, nodeRunID)
		return err
	}); err != nil {
		t.Fatalf("reload blocked node run: %v", err)
	}

	// The underlying cause is fixed (isolation is enforceable now) and a
	// new activation is created for the SAME node key — mirroring
	// reactivateBlockedNodeRunTx's own "mint a new NodeRunID, bump
	// ActivationSequence, hand off to ScheduleExecutableNodeRun" shape.
	newNodeRunID := ids.NewID()
	var newActivation runtimedomain.NodeRun
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		nodeRun, err := runtimedomain.NewNodeRun(
			runtimedomain.NodeRunID(newNodeRunID), runtimedomain.WorkflowRunID(runID), blocked.NodeKey,
			blocked.ActivationSequence+1, blocked.Iteration, blocked.EffectiveScope, blocked.InputStateHash, "",
		)
		if err != nil {
			return err
		}
		newActivation, err = tx.Runtime().CreateNodeRun(ctx, nodeRun)
		return err
	}); err != nil {
		t.Fatalf("create retry activation: %v", err)
	}

	retried, err := runtime.ScheduleExecutableNodeRun(ctx, uow, ids, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: runID, NodeRunID: string(newActivation.ID), CorrelationID: "corr-retry",
	})
	if err != nil {
		t.Fatalf("ScheduleExecutableNodeRun (retry): %v", err)
	}
	if retried.AttemptID == attemptID {
		t.Fatal("retry must mint a new AttemptID, never reuse the blocked one")
	}

	retryJob := claimableExecuteNodeJob(t, uow, retried.AttemptID)
	retryExecutor := &fake.NodeExecutor{Result: ports.NodeExecutionResult{State: runtimedomain.ExecutionAttemptSucceeded, SelectedOutcome: "done"}}
	retryHandler := runtime.NewExecuteNodeHandler(uow, ids, retryExecutor, clock.System{}, fake.IsolationEnforcementChecker{}, agentregistry.Empty())
	if err := retryHandler.Handle(ctx, retryJob); err != nil {
		t.Fatalf("Handle (retry): %v", err)
	}
	if retryExecutor.Calls != 1 {
		t.Fatalf("retry executor.Calls = %d, want 1 (revalidated admission should pass)", retryExecutor.Calls)
	}

	// The ORIGINAL Attempt must still be exactly as it was left — BLOCKED,
	// never revived, never touched by the retry.
	var original runtimedomain.ExecutionAttempt
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		original, err = tx.Runtime().GetExecutionAttempt(ctx, attemptID)
		return err
	}); err != nil {
		t.Fatalf("reload original attempt: %v", err)
	}
	if original.State != runtimedomain.ExecutionAttemptBlocked {
		t.Fatalf("original attempt.State = %s, want it to remain BLOCKED (never revived)", original.State)
	}
}

func writeAdmissionExecutable(t *testing.T, content string) string {
	t.Helper()
	path := t.TempDir() + string(os.PathSeparator) + "provider-cli"
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable fixture: %v", err)
	}
	return path
}

func admissionPinnedBuild(t *testing.T, executablePath string, capabilities ports.AgentCapabilities) domainadapterbuild.Build {
	t.Helper()
	contentHash, err := adapterbuild.HashExecutableFile(executablePath)
	if err != nil {
		t.Fatalf("hash fixture: %v", err)
	}
	manifest := domainadapterbuild.CapabilityManifest{
		SupportsStart: capabilities.SupportsStart, SupportsResume: capabilities.SupportsResume, SupportsCancel: capabilities.SupportsCancel,
	}
	_, manifestHash, err := domainadapterbuild.HashCapabilityManifest(manifest)
	if err != nil {
		t.Fatalf("hash manifest: %v", err)
	}
	tuple := domainadapterbuild.CandidateTuple{
		ProviderKey: string(capabilities.Provider), ExecutablePath: executablePath,
		ExecutableContentHash: contentHash, ProtocolVersion: capabilities.ProtocolVersion,
		CapabilityManifestHash: manifestHash, OS: stdruntime.GOOS, Toolchain: stdruntime.Version(),
		ConfigIdentity: "default",
	}
	build, err := domainadapterbuild.NewBuild(domainadapterbuild.NewBuildRequest{
		Tuple: tuple, CapabilityManifest: manifest, RegisteredBy: "operator-1", RegisteredAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("construct pinned build: %v", err)
	}
	return build
}
