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
// its trivially-satisfied default (isolation enforceable, a real
// non-drifting adapter build pinned, no required capability, exactly one
// write-scoped repository) and varies exactly the one axis it means to
// exercise.
//
// Audit finding (2026-09-08): GC-INV-23 makes a pinned AdapterBuildVersion
// mandatory for an AGENT node, not optional — every test below now pins a
// real, valid, non-drifting default build UNLESS it opts out via
// noAdapterBuild (only the one test proving the missing-pin case itself
// blocks does that) or supplies its own via adapterBuild (only the drift
// test, which needs to mutate the executable after scheduling).
type admissionFixtureOptions struct {
	requiredCapabilities   []string
	grantedCapabilities    []string
	extraWriteRepositories []string
	adapterBuild           *domainadapterbuild.Build
	// noAdapterBuild skips this fixture's own default AdapterBuild
	// entirely — set only by the test that proves a missing pin blocks
	// admission (GC-INV-23).
	noAdapterBuild bool
}

// defaultAdmissionBuildAndRegistry builds a fresh, trivially-valid,
// never-drifting AdapterBuild plus a matching agentregistry.Registry whose
// own fake.AgentExecutor reports the IDENTICAL capabilities — the pair
// admissionFixture pins by default now that GC-INV-23 makes a pin
// mandatory. Every OTHER admission check test (isolation/capability/
// multi-repo/etc.) needs this pair to exist purely so the drift check
// itself passes cleanly and lets the test's own intended check run.
func defaultAdmissionBuildAndRegistry(t *testing.T) (domainadapterbuild.Build, *agentregistry.Registry) {
	t.Helper()
	executablePath := writeAdmissionExecutable(t, "default-fixture-binary-v1")
	capabilities := ports.AgentCapabilities{
		Provider: ports.ProviderClaude, AdapterVersion: "claude-stream-json/v1", ProtocolVersion: "claude-stream-json/v1",
		SupportsStart: true, SupportsResume: true, SupportsCancel: true,
	}
	build := admissionPinnedBuild(t, executablePath, capabilities)
	registry, err := agentregistry.New(context.Background(), &fake.AgentExecutor{CapabilitiesResult: capabilities})
	if err != nil {
		t.Fatalf("agentregistry.New: %v", err)
	}
	return build, registry
}

func admissionFixture(t *testing.T, opts admissionFixtureOptions) (uow *fake.UnitOfWork, ids idsource.Source, runID, nodeRunID, attemptID string, registry *agentregistry.Registry) {
	t.Helper()
	granted := opts.grantedCapabilities
	if granted == nil {
		granted = []string{"INTEGRATION_MULTI_REPOSITORY_WRITE"}
	}

	var build *domainadapterbuild.Build
	switch {
	case opts.noAdapterBuild:
		// Intentionally no build, no registry — the one test proving a
		// missing pin blocks admission (GC-INV-23) wants exactly this.
	case opts.adapterBuild != nil:
		build = opts.adapterBuild
		// The caller supplied its own build (the drift test — it needs to
		// mutate the executable after scheduling) but still needs a
		// registry that can resolve its own provider/capabilities to
		// probe against.
		r, err := agentregistry.New(context.Background(), &fake.AgentExecutor{CapabilitiesResult: ports.AgentCapabilities{
			Provider: ports.ProviderClaude, AdapterVersion: "claude-stream-json/v1", ProtocolVersion: "claude-stream-json/v1",
			SupportsStart: true, SupportsResume: true, SupportsCancel: true,
		}})
		if err != nil {
			t.Fatalf("agentregistry.New: %v", err)
		}
		registry = r
	default:
		defaultBuild, defaultRegistry := defaultAdmissionBuildAndRegistry(t)
		build = &defaultBuild
		registry = defaultRegistry
	}

	var adapterBuildID *string
	if build != nil {
		id := build.ID()
		adapterBuildID = &id
	}

	uow, ids, runID, nodeRunID = scheduleFixture(t, agentExecutableDocument("agent-profile-v1", fullyResolvablePolicyRefs(), adapterBuildID))

	if build != nil {
		// Must be registered BEFORE scheduling: resolveExecutionProfile
		// (schedule.go) fails the whole scheduling transaction closed if
		// a declared AdapterBuildID cannot be resolved.
		if err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
			_, _, err := tx.AdapterBuilds().InsertIfAbsent(context.Background(), *build)
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
	return uow, ids, runID, nodeRunID, scheduled.AttemptID, registry
}

func TestAdmission_IsolationUnavailable_BlocksBeforeSpawn(t *testing.T) {
	uow, ids, _, nodeRunID, attemptID, registry := admissionFixture(t, admissionFixtureOptions{})
	job := claimableExecuteNodeJob(t, uow, attemptID)

	executor := &fake.NodeExecutor{}
	handler := runtime.NewExecuteNodeHandler(
		uow, ids, executor, clock.System{},
		fake.IsolationEnforcementChecker{Err: errors.New("no real OS enforcement")}, registry, nil,
	)
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	assertBlockedReason(t, uow, attemptID, nodeRunID, runtimedomain.TerminationReasonIsolationEnforcementUnavailable, workdomain.BlockerIsolationEnforcementUnavailable)
	if executor.Calls != 0 {
		t.Fatalf("executor.Calls = %d, want 0", executor.Calls)
	}
}

// TestAdmission_NoAdapterBuildPinned_BlocksBeforeSpawn is the audit finding
// (2026-09-08) fix: GC-INV-23 ("Attempt pin immutable AdapterBuildVersion")
// makes a pin mandatory for an AGENT node — an AGENT node that declares NO
// AdapterBuildID must be blocked with ADAPTER_BUILD_DRIFT before it ever
// reaches RUNNING, not silently admitted as "nothing to verify".
func TestAdmission_NoAdapterBuildPinned_BlocksBeforeSpawn(t *testing.T) {
	uow, ids, _, nodeRunID, attemptID, registry := admissionFixture(t, admissionFixtureOptions{noAdapterBuild: true})
	job := claimableExecuteNodeJob(t, uow, attemptID)

	executor := &fake.NodeExecutor{}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{}, fake.IsolationEnforcementChecker{}, registry, nil)
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	assertBlockedReason(t, uow, attemptID, nodeRunID, runtimedomain.TerminationReasonAdapterBuildDrift, workdomain.BlockerAdapterBuildDrift)
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

	uow, ids, _, nodeRunID, attemptID, registry := admissionFixture(t, admissionFixtureOptions{adapterBuild: &build})

	// Drift: the executable changes after the build was pinned and
	// scheduled.
	if err := os.WriteFile(executablePath, []byte("binary-v2-swapped"), 0o755); err != nil {
		t.Fatalf("swap fixture: %v", err)
	}

	job := claimableExecuteNodeJob(t, uow, attemptID)
	executor := &fake.NodeExecutor{}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{}, fake.IsolationEnforcementChecker{}, registry, nil)
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	assertBlockedReason(t, uow, attemptID, nodeRunID, runtimedomain.TerminationReasonAdapterBuildDrift, workdomain.BlockerAdapterBuildDrift)
	if executor.Calls != 0 {
		t.Fatalf("executor.Calls = %d, want 0", executor.Calls)
	}
}

func TestAdmission_CapabilityRequirementUnsatisfied_BlocksBeforeSpawn(t *testing.T) {
	uow, ids, _, nodeRunID, attemptID, registry := admissionFixture(t, admissionFixtureOptions{
		requiredCapabilities: []string{"SANDBOXED_FILESYSTEM"},
		grantedCapabilities:  []string{"INTEGRATION_MULTI_REPOSITORY_WRITE"}, // does not include SANDBOXED_FILESYSTEM
	})
	job := claimableExecuteNodeJob(t, uow, attemptID)

	executor := &fake.NodeExecutor{}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{}, fake.IsolationEnforcementChecker{}, registry, nil)
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	assertBlockedReason(t, uow, attemptID, nodeRunID, runtimedomain.TerminationReasonCapabilityRequirementUnsatisfied, workdomain.BlockerCapabilityRequirementUnsatisfied)
	if executor.Calls != 0 {
		t.Fatalf("executor.Calls = %d, want 0", executor.Calls)
	}
}

func TestAdmission_MultiRepositoryWriteWithoutGrant_BlocksBeforeSpawn(t *testing.T) {
	uow, ids, _, nodeRunID, attemptID, registry := admissionFixture(t, admissionFixtureOptions{
		grantedCapabilities:    []string{}, // no INTEGRATION_MULTI_REPOSITORY_WRITE
		extraWriteRepositories: []string{"repo-2"},
	})
	job := claimableExecuteNodeJob(t, uow, attemptID)

	executor := &fake.NodeExecutor{}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{}, fake.IsolationEnforcementChecker{}, registry, nil)
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
	uow, ids, _, nodeRunID, attemptID, registry := admissionFixture(t, admissionFixtureOptions{
		grantedCapabilities:    []string{},
		extraWriteRepositories: []string{"repo-2"},
	})
	job := claimableExecuteNodeJob(t, uow, attemptID)

	executor := &fake.NodeExecutor{}
	handler := runtime.NewExecuteNodeHandler(
		uow, ids, executor, clock.System{},
		fake.IsolationEnforcementChecker{Err: errors.New("no real OS enforcement")}, registry, nil,
	)
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	assertBlockedReason(t, uow, attemptID, nodeRunID, runtimedomain.TerminationReasonIsolationEnforcementUnavailable, workdomain.BlockerIsolationEnforcementUnavailable)
}

func TestAdmission_AllChecksPass_ProceedsToRunning(t *testing.T) {
	uow, ids, _, _, attemptID, registry := admissionFixture(t, admissionFixtureOptions{})
	job := claimableExecuteNodeJob(t, uow, attemptID)

	executor := &fake.NodeExecutor{Result: ports.NodeExecutionResult{State: runtimedomain.ExecutionAttemptSucceeded, SelectedOutcome: "done"}}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{}, fake.IsolationEnforcementChecker{}, registry, nil)
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

// claimableScheduleNodeRunJob finds the ScheduleNodeRunJobKind job
// RetryBlockedActivationHandler.Retry (or reactivateBlockedNodeRunTx)
// enqueued for nodeRunID — NodeSchedulingHandler.Handle needs no active
// lease (unlike ExecuteNodeHandler's own claimableExecuteNodeJob), so this
// builds a plain ports.DurableJob straight from the enqueued row.
func claimableScheduleNodeRunJob(t *testing.T, uow *fake.UnitOfWork, nodeRunID string) ports.DurableJob {
	t.Helper()
	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	for _, job := range jobs {
		if job.Kind == runtime.ScheduleNodeRunJobKind && job.AggregateID == nodeRunID {
			return ports.DurableJob{ID: job.ID, AggregateType: job.AggregateType, AggregateID: job.AggregateID, Payload: job.Payload}
		}
	}
	t.Fatalf("no %s job found for node run %s among %+v", runtime.ScheduleNodeRunJobKind, nodeRunID, jobs)
	return ports.DurableJob{}
}

// TestRetryBlockedActivation_CreatesNewAttemptNeverRevivesOld is V5-08's
// own required test (the full RetryBlockedActivation command is V5-08D's
// scope): after an admission-blocked Attempt's own cause is fixed, a real
// RetryBlockedActivationHandler.Retry call creates a new NodeRun activation
// (fresh ID, ActivationSequence+1, same NodeKey) that goes through the
// ordinary ScheduleExecutableNodeRun pipeline unchanged and gets its own
// new Attempt — the original BLOCKED Attempt is never revived, mirroring
// reactivateBlockedNodeRunTx's own exact shape (scope_expansion.go) for the
// OTHER blocker group.
func TestRetryBlockedActivation_CreatesNewAttemptNeverRevivesOld(t *testing.T) {
	uow, ids, runID, nodeRunID, attemptID, registry := admissionFixture(t, admissionFixtureOptions{})
	job := claimableExecuteNodeJob(t, uow, attemptID)

	blockedExecutor := &fake.NodeExecutor{}
	blockingChecker := fake.IsolationEnforcementChecker{Err: errors.New("no real OS enforcement")}
	handler := runtime.NewExecuteNodeHandler(uow, ids, blockedExecutor, clock.System{}, blockingChecker, registry, nil)
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle (first, blocked): %v", err)
	}
	assertBlockedReason(t, uow, attemptID, nodeRunID, runtimedomain.TerminationReasonIsolationEnforcementUnavailable, workdomain.BlockerIsolationEnforcementUnavailable)

	ctx := context.Background()

	// The underlying cause is fixed (isolation is enforceable now, a
	// non-blocking checker below) — Retry must create a new activation for
	// the SAME node key and resolve the admission blocker.
	retryHandler := runtime.NewRetryBlockedActivationHandler(uow, ids, fake.IsolationEnforcementChecker{}, registry)
	retryResult, err := retryHandler.Retry(ctx, runtime.RetryBlockedActivationRequest{
		NodeRunID: nodeRunID, Actor: "operator-1", Reason: "isolation enforcement is available now",
	})
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if !retryResult.Retried || retryResult.ReactivatedNodeRunID == "" {
		t.Fatalf("retryResult = %+v, want Retried with a ReactivatedNodeRunID", retryResult)
	}

	scheduleJob := claimableScheduleNodeRunJob(t, uow, retryResult.ReactivatedNodeRunID)
	schedulingHandler := runtime.NewNodeSchedulingHandler(uow, ids, fake.NewRuntimeExecutionConfigProvider())
	if err := schedulingHandler.Handle(ctx, scheduleJob); err != nil {
		t.Fatalf("NodeSchedulingHandler.Handle (retry): %v", err)
	}

	var retriedAttemptID string
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		attempts, err := tx.Runtime().ListExecutionAttemptsForRun(ctx, runID)
		if err != nil {
			return err
		}
		for _, a := range attempts {
			if string(a.NodeRunID) == retryResult.ReactivatedNodeRunID {
				retriedAttemptID = string(a.ID)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("list attempts for reactivated node run: %v", err)
	}
	if retriedAttemptID == "" || retriedAttemptID == attemptID {
		t.Fatalf("retriedAttemptID = %q, want a new, non-empty attempt id distinct from the blocked one %q", retriedAttemptID, attemptID)
	}

	retryJob := claimableExecuteNodeJob(t, uow, retriedAttemptID)
	retryExecutor := &fake.NodeExecutor{Result: ports.NodeExecutionResult{State: runtimedomain.ExecutionAttemptSucceeded, SelectedOutcome: "done"}}
	retryExecuteHandler := runtime.NewExecuteNodeHandler(uow, ids, retryExecutor, clock.System{}, fake.IsolationEnforcementChecker{}, registry, nil)
	if err := retryExecuteHandler.Handle(ctx, retryJob); err != nil {
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

	// The admission blocker itself must now be RESOLVED, and the WorkItem
	// back to ACTIVE (the SAME Run resumes, it never stopped) — never
	// READY (that would imply the Run that caused the block is gone).
	var blockers []workdomain.WorkItemBlocker
	var item workdomain.WorkItem
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		run, err := tx.Runtime().GetWorkflowRun(ctx, runID)
		if err != nil {
			return err
		}
		blockers, err = tx.Work().ListWorkItemBlockersForWorkItem(ctx, string(run.WorkItemID))
		if err != nil {
			return err
		}
		item, err = tx.Work().GetWorkItem(ctx, string(run.WorkItemID))
		return err
	}); err != nil {
		t.Fatalf("reload blockers/work item: %v", err)
	}
	found := false
	for _, blocker := range blockers {
		if blocker.SourceAttemptID == attemptID {
			found = true
			if blocker.State != workdomain.BlockerResolved {
				t.Fatalf("blocker.State = %s, want RESOLVED", blocker.State)
			}
		}
	}
	if !found {
		t.Fatalf("no blocker found for original attempt %s among %+v", attemptID, blockers)
	}
	if item.Status != workdomain.WorkItemActive {
		t.Fatalf("work item status = %s, want ACTIVE (the same run resumes)", item.Status)
	}

	// A second call against the exact same, now-no-longer-BLOCKED NodeRun
	// (a redelivery, or a second concurrent caller) must be an idempotent
	// no-op — never a second reactivation, never an error.
	secondResult, err := retryHandler.Retry(ctx, runtime.RetryBlockedActivationRequest{
		NodeRunID: nodeRunID, Actor: "operator-1", Reason: "redelivered",
	})
	if err != nil {
		t.Fatalf("second Retry: %v", err)
	}
	if !secondResult.AlreadyRetried || secondResult.Retried {
		t.Fatalf("second Retry result = %+v, want AlreadyRetried, not a second Retried", secondResult)
	}
}

// TestRetryBlockedActivation_RevalidationStillFails_NoRepeatedBlockedActivation
// proves this task's own locked requirement: a retry that still fails
// leaves the existing blocker exactly as it was and creates no new
// activation at all — repeated 5 times, never producing a chain of blocked
// activations or more than one OPEN blocker.
func TestRetryBlockedActivation_RevalidationStillFails_NoRepeatedBlockedActivation(t *testing.T) {
	uow, ids, runID, nodeRunID, attemptID, registry := admissionFixture(t, admissionFixtureOptions{})
	job := claimableExecuteNodeJob(t, uow, attemptID)

	blockingChecker := fake.IsolationEnforcementChecker{Err: errors.New("still no real OS enforcement")}
	handler := runtime.NewExecuteNodeHandler(uow, ids, &fake.NodeExecutor{}, clock.System{}, blockingChecker, registry, nil)
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle (blocked): %v", err)
	}
	assertBlockedReason(t, uow, attemptID, nodeRunID, runtimedomain.TerminationReasonIsolationEnforcementUnavailable, workdomain.BlockerIsolationEnforcementUnavailable)

	ctx := context.Background()
	var nodeRunsBefore []runtimedomain.NodeRun
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		nodeRunsBefore, err = tx.Runtime().ListNodeRunsForRun(ctx, runID)
		return err
	}); err != nil {
		t.Fatalf("list node runs before retries: %v", err)
	}

	retryHandler := runtime.NewRetryBlockedActivationHandler(uow, ids, blockingChecker, registry)
	for i := 0; i < 5; i++ {
		result, err := retryHandler.Retry(ctx, runtime.RetryBlockedActivationRequest{
			NodeRunID: nodeRunID, Actor: "operator-1", Reason: "attempting retry",
		})
		if err != nil {
			t.Fatalf("Retry attempt %d: %v", i, err)
		}
		if result.Retried || result.AlreadyRetried {
			t.Fatalf("Retry attempt %d result = %+v, want a still-failing (non-retried) result", i, result)
		}
		if result.FailureReason != runtimedomain.TerminationReasonIsolationEnforcementUnavailable {
			t.Fatalf("Retry attempt %d FailureReason = %s, want ISOLATION_ENFORCEMENT_UNAVAILABLE", i, result.FailureReason)
		}
	}

	var nodeRuns []runtimedomain.NodeRun
	var blockers []workdomain.WorkItemBlocker
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		nodeRuns, err = tx.Runtime().ListNodeRunsForRun(ctx, runID)
		if err != nil {
			return err
		}
		run, err := tx.Runtime().GetWorkflowRun(ctx, runID)
		if err != nil {
			return err
		}
		blockers, err = tx.Work().ListWorkItemBlockersForWorkItem(ctx, string(run.WorkItemID))
		return err
	}); err != nil {
		t.Fatalf("reload node runs/blockers: %v", err)
	}
	if len(nodeRuns) != len(nodeRunsBefore) {
		t.Fatalf("node runs for this run = %d, want unchanged from before the retries (%d) — no new activation ever created by a failing retry", len(nodeRuns), len(nodeRunsBefore))
	}
	openCount := 0
	for _, blocker := range blockers {
		if blocker.State == workdomain.BlockerOpen {
			openCount++
		}
	}
	if openCount != 1 {
		t.Fatalf("open blockers = %d, want exactly 1 (5 failing retries must never open a second blocker)", openCount)
	}
}

// TestRetryBlockedActivation_RunNotRetryable_Rejected proves the "cancel
// fence": a retry against a Run that has already left RUNNING/WAITING
// (CANCELLING here) is rejected outright — reactivating now would create
// orphaned work with nothing left to route it into.
func TestRetryBlockedActivation_RunNotRetryable_Rejected(t *testing.T) {
	uow, ids, runID, nodeRunID, attemptID, registry := admissionFixture(t, admissionFixtureOptions{})
	job := claimableExecuteNodeJob(t, uow, attemptID)

	blockingChecker := fake.IsolationEnforcementChecker{Err: errors.New("no real OS enforcement")}
	handler := runtime.NewExecuteNodeHandler(uow, ids, &fake.NodeExecutor{}, clock.System{}, blockingChecker, registry, nil)
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle (blocked): %v", err)
	}
	assertBlockedReason(t, uow, attemptID, nodeRunID, runtimedomain.TerminationReasonIsolationEnforcementUnavailable, workdomain.BlockerIsolationEnforcementUnavailable)

	ctx := context.Background()
	if _, err := runtime.CancelRun(ctx, uow, ids, runtime.CancelRunRequest{RunID: runID, Actor: "actor-1", Reason: "test cancel"}); err != nil {
		t.Fatalf("CancelRun: %v", err)
	}

	retryHandler := runtime.NewRetryBlockedActivationHandler(uow, ids, fake.IsolationEnforcementChecker{}, registry)
	if _, err := retryHandler.Retry(ctx, runtime.RetryBlockedActivationRequest{
		NodeRunID: nodeRunID, Actor: "operator-1", Reason: "isolation now enforceable",
	}); !errors.Is(err, runtime.ErrRunNotRetryable) {
		t.Fatalf("Retry err = %v, want ErrRunNotRetryable", err)
	}
}

// TestRetryBlockedActivation_AdapterDrift_NeverRepinsToNewerBuild proves
// this task's own locked "không repin Run": once a NodeRun is BLOCKED
// because its own pinned AdapterBuildVersion drifted, registering a
// SECOND, perfectly valid, non-drifting build afterward has zero effect on
// a retry — Retry only ever re-probes the SAME immutable pin
// loadExecutionProfile reads back, never substitutes a newer one.
func TestRetryBlockedActivation_AdapterDrift_NeverRepinsToNewerBuild(t *testing.T) {
	executablePath := writeAdmissionExecutable(t, "binary-v1")
	capabilities := ports.AgentCapabilities{
		Provider: ports.ProviderClaude, AdapterVersion: "claude-stream-json/v1", ProtocolVersion: "claude-stream-json/v1",
		SupportsStart: true, SupportsResume: true, SupportsCancel: true,
	}
	originalBuild := admissionPinnedBuild(t, executablePath, capabilities)

	uow, ids, _, nodeRunID, attemptID, registry := admissionFixture(t, admissionFixtureOptions{adapterBuild: &originalBuild})

	// Drift: the executable changes after the build was pinned and
	// scheduled — the SAME drift TestAdmission_AdapterBuildDrift_BlocksBeforeSpawn
	// induces.
	if err := os.WriteFile(executablePath, []byte("binary-v2-swapped"), 0o755); err != nil {
		t.Fatalf("swap fixture: %v", err)
	}

	job := claimableExecuteNodeJob(t, uow, attemptID)
	handler := runtime.NewExecuteNodeHandler(uow, ids, &fake.NodeExecutor{}, clock.System{}, fake.IsolationEnforcementChecker{}, registry, nil)
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle (blocked): %v", err)
	}
	assertBlockedReason(t, uow, attemptID, nodeRunID, runtimedomain.TerminationReasonAdapterBuildDrift, workdomain.BlockerAdapterBuildDrift)

	ctx := context.Background()

	// A second, perfectly valid, non-drifting build now exists — a
	// well-intentioned operator might expect a retry to pick this up.
	// It must not: RetryBlockedActivation re-probes the ORIGINAL pin only.
	newExecutablePath := writeAdmissionExecutable(t, "binary-v3-fresh-and-valid")
	newBuild := admissionPinnedBuild(t, newExecutablePath, capabilities)
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, _, err := tx.AdapterBuilds().InsertIfAbsent(ctx, newBuild)
		return err
	}); err != nil {
		t.Fatalf("register second build: %v", err)
	}

	retryHandler := runtime.NewRetryBlockedActivationHandler(uow, ids, fake.IsolationEnforcementChecker{}, registry)
	result, err := retryHandler.Retry(ctx, runtime.RetryBlockedActivationRequest{
		NodeRunID: nodeRunID, Actor: "operator-1", Reason: "a newer build now exists",
	})
	if err != nil {
		t.Fatalf("Retry: %v", err)
	}
	if result.Retried {
		t.Fatal("Retry succeeded, want it to still fail — the drifted ORIGINAL pin was never replaced by the newer build")
	}
	if result.FailureReason != runtimedomain.TerminationReasonAdapterBuildDrift {
		t.Fatalf("result.FailureReason = %s, want ADAPTER_BUILD_DRIFT (the original pin is still drifted)", result.FailureReason)
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
