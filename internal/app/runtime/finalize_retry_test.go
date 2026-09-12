package runtime_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// V4-06's own retry-vs-exhaustion tests. Every other test in this package
// pins attemptPolicyDocument's own fixed AttemptRules (MaxAttempts=3,
// BackoffSeconds=30, no RetryableErrorCodes at all) — deliberately
// non-retryable for ANY code, which is exactly what
// TestExecuteNodeHandler_Failure_NonRetryableCode_FailsNodeRun and
// TestExecuteNodeHandler_AttemptDeadlineFinalizesTimedOut (execute_test.go)
// already exercise. These tests need a pinned policy that actually declares
// a RetryableErrorCodes entry, so they publish their own.

// scheduledExecutionFixtureWithAttemptPolicy is scheduledExecutionFixture's
// own sibling (execute_test.go): identical fixture, but the pinned ATTEMPT
// PolicyVersion is exactly attemptDoc, not the fixed attemptPolicyDocument
// every other test in this package publishes.
func scheduledExecutionFixtureWithAttemptPolicy(t *testing.T, attemptDoc policy.PolicyDocument) (uow *fake.UnitOfWork, ids idsource.Source, runID, nodeRunID, attemptID string) {
	t.Helper()
	buildID := sharedTestAdapterBuild(t).ID()
	uow, ids, runID, nodeRunID = scheduleFixture(t, agentExecutableDocument("agent-profile-v1", fullyResolvablePolicyRefs(), &buildID))
	registerSharedTestAdapterBuild(t, uow)
	publishAgentProfileVersion(t, uow, "agent-profile-def", "agent-profile-v1", validAgentProfileDocument())
	publishPolicyVersion(t, uow, "attempt-policy-def", "attempt-policy-v1", attemptDoc)
	publishPolicyVersion(t, uow, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())

	scheduled, err := runtime.ScheduleExecutableNodeRun(context.Background(), uow, ids, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: runID, NodeRunID: nodeRunID, CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("ScheduleExecutableNodeRun: %v", err)
	}
	return uow, ids, runID, nodeRunID, scheduled.AttemptID
}

func retryableAttemptPolicyDocument(maxAttempts, backoffSeconds, timeoutSeconds uint32, codes ...errorcode.Code) policy.PolicyDocument {
	return policy.PolicyDocument{
		Category: policy.CategoryAttempt,
		Attempt: &policy.AttemptRules{
			MaxAttempts: maxAttempts, BackoffSeconds: backoffSeconds, TimeoutSeconds: timeoutSeconds,
			RetryableErrorCodes: codes,
		},
	}
}

// TestExecuteNodeHandler_RetryableFailure_CreatesNextAttemptWithBackoff
// proves the retryable, budget-remains branch: a new ExecutionAttempt
// (AttemptNumber 2) is created QUEUED, its own EXECUTE_NODE job's
// AvailableAt is exactly clk.Now() + BackoffSeconds (a fake clock.Fixed
// makes this deterministic), the NodeRun stays RUNNING (never routed, never
// failed) and the ORIGINAL Attempt keeps its own FAILED/EXECUTION_FAILED/
// PROVIDER_UNAVAILABLE classification unchanged.
func TestExecuteNodeHandler_RetryableFailure_CreatesNextAttemptWithBackoff(t *testing.T) {
	attemptDoc := retryableAttemptPolicyDocument(3, 30, 600, errorcode.CodeProviderUnavailable)
	uow, ids, _, nodeRunID, attemptID := scheduledExecutionFixtureWithAttemptPolicy(t, attemptDoc)
	job := claimableExecuteNodeJob(t, uow, attemptID)

	fixedNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewFixed(fixedNow)

	executor := &fake.NodeExecutor{Result: ports.NodeExecutionResult{
		State: runtimedomain.ExecutionAttemptFailed, ErrorCode: errorcode.CodeProviderUnavailable,
	}}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clk, fake.IsolationEnforcementChecker{}, sharedTestAgentRegistry(t), nil)
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	original, err := uow.Snapshot.Runtime().GetExecutionAttempt(context.Background(), attemptID)
	if err != nil {
		t.Fatalf("GetExecutionAttempt(original): %v", err)
	}
	if original.State != runtimedomain.ExecutionAttemptFailed ||
		original.TerminationReason != runtimedomain.TerminationReasonExecutionFailed ||
		original.FailureCode != errorcode.CodeProviderUnavailable {
		t.Fatalf("original attempt = %+v, want FAILED/EXECUTION_FAILED/PROVIDER_UNAVAILABLE unchanged", original)
	}

	nodeRun, err := uow.Snapshot.Runtime().GetNodeRun(context.Background(), nodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun: %v", err)
	}
	if nodeRun.State != runtimedomain.NodeRunRunning {
		t.Fatalf("node run = %+v, want unchanged RUNNING while a retry is pending", nodeRun)
	}

	attempts := uow.Snapshot.Runtime().(*fake.RuntimeRepository).Attempts()
	if len(attempts) != 2 {
		t.Fatalf("attempt count = %d, want exactly 2 (original + retry)", len(attempts))
	}
	var next runtimedomain.ExecutionAttempt
	found := false
	for id, a := range attempts {
		if id != attemptID {
			next, found = a, true
		}
	}
	if !found {
		t.Fatalf("no retry attempt found among %+v", attempts)
	}
	if next.AttemptNumber != 2 || next.State != runtimedomain.ExecutionAttemptQueued {
		t.Fatalf("retry attempt = %+v, want AttemptNumber=2 QUEUED", next)
	}
	if next.ExecutionProfileHash != original.ExecutionProfileHash || next.ProviderKey != original.ProviderKey {
		t.Fatalf("retry attempt = %+v, want same ExecutionProfileHash/ProviderKey as original %+v", next, original)
	}

	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	var retryJob *ports.EnqueueJobRequest
	for i := range jobs {
		if jobs[i].Kind == runtime.ExecuteNodeJobKind && jobs[i].AggregateID == string(next.ID) {
			retryJob = &jobs[i]
		}
	}
	if retryJob == nil {
		t.Fatalf("no %s job found for retry attempt %s among %+v", runtime.ExecuteNodeJobKind, next.ID, jobs)
	}
	wantAvailableAt := fixedNow.Add(30 * time.Second)
	if !retryJob.AvailableAt.Equal(wantAvailableAt) {
		t.Fatalf("retry job AvailableAt = %v, want %v (now + BackoffSeconds)", retryJob.AvailableAt, wantAvailableAt)
	}
	if retryJob.IdempotencyKey != "execute-"+string(next.ID) {
		t.Fatalf("retry job IdempotencyKey = %s, want execute-%s", retryJob.IdempotencyKey, next.ID)
	}
}

// TestExecuteNodeHandler_WriteLeaseConflict_RetriesLikeAnyOtherRetryableCode
// proves the post-V5-15E write-lease-conflict contract's own retry half:
// CONFLICT (errorcode.CodeConflict) — the classification
// CommandNodeExecutor/AgentNodeExecutor.Execute now give a real
// ports.ErrWriteLeaseConflict from resolveExecutionResources, instead of
// the bare, always-non-retryable error it used to be wrapped as — goes
// through the exact same policy-driven retry decision
// TestExecuteNodeHandler_RetryableFailure_CreatesNextAttemptWithBackoff
// already proves for PROVIDER_UNAVAILABLE: decideRetryOrExhaustion has no
// special-cased vocabulary for any one errorcode.Code (fence mismatch/
// quarantine/scope violation stay non-retryable simply because no policy
// in this codebase ever lists them in RetryableErrorCodes, exactly as
// before this fix — nothing about how THOSE codes are classified changed).
// The real "does CommandNodeExecutor actually classify a genuine
// ErrWriteLeaseConflict as CodeConflict" half is proven by
// TestV5AcceptFullComposition_RealFourRoleGraphReachesSucceededAndSurvivesRestart
// (internal/integration/v5accept/full_composition_test.go): before this
// fix, that real scenario's own second write-mounting node deterministically
// hit a real conflict against the first node's never-released lease and
// failed outright; after it, the same real conflict — when it happens at
// all — would retry via this exact path rather than failing the Run.
func TestExecuteNodeHandler_WriteLeaseConflict_RetriesLikeAnyOtherRetryableCode(t *testing.T) {
	attemptDoc := retryableAttemptPolicyDocument(3, 30, 600, errorcode.CodeConflict)
	uow, ids, _, nodeRunID, attemptID := scheduledExecutionFixtureWithAttemptPolicy(t, attemptDoc)
	job := claimableExecuteNodeJob(t, uow, attemptID)

	fixedNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewFixed(fixedNow)

	executor := &fake.NodeExecutor{Result: ports.NodeExecutionResult{
		State: runtimedomain.ExecutionAttemptFailed, ErrorCode: errorcode.CodeConflict,
	}}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clk, fake.IsolationEnforcementChecker{}, sharedTestAgentRegistry(t), nil)
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	original, err := uow.Snapshot.Runtime().GetExecutionAttempt(context.Background(), attemptID)
	if err != nil {
		t.Fatalf("GetExecutionAttempt(original): %v", err)
	}
	if original.State != runtimedomain.ExecutionAttemptFailed ||
		original.TerminationReason != runtimedomain.TerminationReasonExecutionFailed ||
		original.FailureCode != errorcode.CodeConflict {
		t.Fatalf("original attempt = %+v, want FAILED/EXECUTION_FAILED/CONFLICT unchanged", original)
	}

	nodeRun, err := uow.Snapshot.Runtime().GetNodeRun(context.Background(), nodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun: %v", err)
	}
	if nodeRun.State != runtimedomain.NodeRunRunning {
		t.Fatalf("node run = %+v, want unchanged RUNNING while a retry is pending", nodeRun)
	}

	attempts := uow.Snapshot.Runtime().(*fake.RuntimeRepository).Attempts()
	if len(attempts) != 2 {
		t.Fatalf("attempt count = %d, want exactly 2 (original + retry)", len(attempts))
	}
	var next runtimedomain.ExecutionAttempt
	found := false
	for id, a := range attempts {
		if id != attemptID {
			next, found = a, true
		}
	}
	if !found {
		t.Fatalf("no retry attempt found among %+v", attempts)
	}
	if next.AttemptNumber != 2 || next.State != runtimedomain.ExecutionAttemptQueued {
		t.Fatalf("retry attempt = %+v, want AttemptNumber=2 QUEUED", next)
	}
}

// TestExecuteNodeHandler_RetryableFailure_BudgetExhausted_FailsNodeRun
// proves the retryable-but-exhausted branch: MaxAttempts=1 means the very
// first failure already exhausts the budget, so no retry Attempt is
// created — the NodeRun is CAS'd straight to FAILED and a
// NODE_RUN_FAILED/RETRY_EXHAUSTED event is appended, while the last
// Attempt's own TerminationReason/FailureCode are left exactly as they
// were (exhaustion is a NodeRun-level classification, never rewritten onto
// the Attempt that actually failed).
func TestExecuteNodeHandler_RetryableFailure_BudgetExhausted_FailsNodeRun(t *testing.T) {
	attemptDoc := retryableAttemptPolicyDocument(1, 30, 600, errorcode.CodeProviderUnavailable)
	uow, ids, _, nodeRunID, attemptID := scheduledExecutionFixtureWithAttemptPolicy(t, attemptDoc)
	job := claimableExecuteNodeJob(t, uow, attemptID)

	executor := &fake.NodeExecutor{Result: ports.NodeExecutionResult{
		State: runtimedomain.ExecutionAttemptFailed, ErrorCode: errorcode.CodeProviderUnavailable,
	}}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{}, fake.IsolationEnforcementChecker{}, sharedTestAgentRegistry(t), nil)
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	attempt, err := uow.Snapshot.Runtime().GetExecutionAttempt(context.Background(), attemptID)
	if err != nil {
		t.Fatalf("GetExecutionAttempt: %v", err)
	}
	if attempt.State != runtimedomain.ExecutionAttemptFailed ||
		attempt.TerminationReason != runtimedomain.TerminationReasonExecutionFailed ||
		attempt.FailureCode != errorcode.CodeProviderUnavailable {
		t.Fatalf("attempt = %+v, want FAILED/EXECUTION_FAILED/PROVIDER_UNAVAILABLE unchanged by exhaustion", attempt)
	}

	attempts := uow.Snapshot.Runtime().(*fake.RuntimeRepository).Attempts()
	if len(attempts) != 1 {
		t.Fatalf("attempt count = %d, want exactly 1 (exhausted budget must not create a retry)", len(attempts))
	}

	nodeRun, err := uow.Snapshot.Runtime().GetNodeRun(context.Background(), nodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun: %v", err)
	}
	if nodeRun.State != runtimedomain.NodeRunFailed {
		t.Fatalf("node run = %+v, want FAILED", nodeRun)
	}

	events := uow.Snapshot.Events().(*fake.EventsRepository).Items()
	var failedEvent *ports.DomainEvent
	for i := range events {
		if events[i].EventType == runtime.NodeRunFailedEventType && events[i].AggregateID == nodeRunID {
			failedEvent = &events[i]
		}
	}
	if failedEvent == nil {
		t.Fatalf("no %s event found among %+v", runtime.NodeRunFailedEventType, events)
	}
	var payload struct {
		FailureKind   string `json:"failureKind"`
		LastAttemptID string `json:"lastAttemptId"`
		AttemptsUsed  uint32 `json:"attemptsUsed"`
		MaxAttempts   uint32 `json:"maxAttempts"`
		LastErrorCode string `json:"lastErrorCode"`
	}
	if err := json.Unmarshal([]byte(failedEvent.PayloadJSON), &payload); err != nil {
		t.Fatalf("decode %s payload: %v", runtime.NodeRunFailedEventType, err)
	}
	if payload.FailureKind != string(runtime.NodeRunFailureKindRetryExhausted) {
		t.Fatalf("failureKind = %s, want %s", payload.FailureKind, runtime.NodeRunFailureKindRetryExhausted)
	}
	if payload.LastAttemptID != attemptID || payload.AttemptsUsed != 1 || payload.MaxAttempts != 1 ||
		payload.LastErrorCode != string(errorcode.CodeProviderUnavailable) {
		t.Fatalf("payload = %+v, want lastAttemptId=%s attemptsUsed=1 maxAttempts=1 lastErrorCode=PROVIDER_UNAVAILABLE", payload, attemptID)
	}
}

// TestFinalizeExecutionAttempt_BlockedState_MismatchedReasonRejectedNeverConsumesRetryBudget
// proves ExecutionAttemptBlocked never reaches the retry decision at all —
// V4-12A widened isFinalizableExecutionAttemptState to accept BLOCKED (it
// is no longer ErrUnsupportedFinalizeState outright), but a BLOCKED call
// whose own TerminationReason is not exactly SCOPE_EXPANSION_REQUIRED is
// still rejected before touching any durable state, and — see
// TestFinalizeExecutionAttempt_Blocked_RequestsScopeExpansion below for the
// positive case — a WELL-FORMED BLOCKED call is handled by
// requestScopeExpansionTx, never decideRetryOrExhaustion, so it can never
// consume AttemptPolicy retry budget nor spawn a next Attempt
// automatically either way.
func TestFinalizeExecutionAttempt_BlockedState_MismatchedReasonRejectedNeverConsumesRetryBudget(t *testing.T) {
	uow, ids, runID, nodeRunID, attemptID := scheduledExecutionFixture(t, 600)
	job := claimableExecuteNodeJob(t, uow, attemptID)
	seedRunningAttemptAndNodeRun(t, uow, nodeRunID, attemptID)

	lease := ports.JobLease{JobID: job.ID, Owner: job.LeaseOwner, Token: job.LeaseToken, LeaseUntil: *job.LeaseUntil}
	_, err := runtime.FinalizeExecutionAttempt(context.Background(), uow, ids, clock.System{}, nil, runtime.FinalizeExecutionAttemptRequest{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID, ExpectedVersion: 2,
		NextState: runtimedomain.ExecutionAttemptBlocked, TerminationReason: runtimedomain.TerminationReasonExecutionFailed,
		JobLease: lease,
	})
	if err == nil {
		t.Fatal("err = nil, want a rejection (BLOCKED requires TerminationReason=SCOPE_EXPANSION_REQUIRED)")
	}

	attempts := uow.Snapshot.Runtime().(*fake.RuntimeRepository).Attempts()
	if len(attempts) != 1 {
		t.Fatalf("attempt count = %d, want exactly 1 (rejected BLOCKED must not spawn a retry attempt)", len(attempts))
	}
	attempt, getErr := uow.Snapshot.Runtime().GetExecutionAttempt(context.Background(), attemptID)
	if getErr != nil {
		t.Fatalf("GetExecutionAttempt: %v", getErr)
	}
	if attempt.State != runtimedomain.ExecutionAttemptRunning {
		t.Fatalf("attempt = %+v, want unchanged RUNNING", attempt)
	}
	nodeRun, getErr := uow.Snapshot.Runtime().GetNodeRun(context.Background(), nodeRunID)
	if getErr != nil {
		t.Fatalf("GetNodeRun: %v", getErr)
	}
	if nodeRun.State != runtimedomain.NodeRunRunning {
		t.Fatalf("node run = %+v, want unchanged RUNNING", nodeRun)
	}
}

// --- SQLite: the retry chain survives a process restart ---

// sqliteExecutionFixtureWithAttemptPolicy is sqliteExecutionFixture's own
// sibling (finalize_execution_attempt_sqlite_test.go), parameterized by an
// already-open store/UnitOfWork/idsource.Source (rather than opening and
// t.Cleanup-registering its own, the way sqliteExecutionFixture does) so a
// restart test can close and reopen the exact same database file between
// scheduling/failing an Attempt and asserting the retry chain survived.
func sqliteExecutionFixtureWithAttemptPolicy(t *testing.T, u ports.UnitOfWork, seq idsource.Source, attemptDoc policy.PolicyDocument) (runID, nodeRunID, attemptID string) {
	t.Helper()
	ctx := context.Background()

	root := readyFixtureSQLite(t, u, seq, "project-1", "repo-1")
	seedEffectiveScope(t, u, root.WorkItemID, "repo-1")
	version := publishWorkflowVersionDocument(t, u, "project-1", "wf-def-1", "wf-v-1",
		agentExecutableDocument("agent-profile-v1", fullyResolvablePolicyRefs(), nil))
	publishAgentProfileVersion(t, u, "agent-profile-def", "agent-profile-v1", validAgentProfileDocument())
	publishPolicyVersion(t, u, "attempt-policy-def", "attempt-policy-v1", attemptDoc)
	publishPolicyVersion(t, u, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())

	startCmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	startResult, err := runtime.StartWorkflowRun(ctx, u, seq, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	hop, err := runtime.AdvanceRun(ctx, u, seq, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: startResult.NodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun (start->implement): %v", err)
	}

	scheduled, err := runtime.ScheduleExecutableNodeRun(ctx, u, seq, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: startResult.RunID, NodeRunID: hop.NextNodeRunID, CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("ScheduleExecutableNodeRun: %v", err)
	}

	if err := u.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		currentNodeRun, err := tx.Runtime().GetNodeRun(ctx, hop.NextNodeRunID)
		if err != nil {
			return err
		}
		if _, err := tx.Runtime().TransitionNodeRun(ctx, ports.TransitionNodeRunRequest{
			NodeRunID: hop.NextNodeRunID, ExpectedState: runtimedomain.NodeRunQueued, ExpectedVersion: currentNodeRun.Version,
			NextState: runtimedomain.NodeRunRunning,
		}); err != nil {
			return err
		}
		currentAttempt, err := tx.Runtime().GetExecutionAttempt(ctx, scheduled.AttemptID)
		if err != nil {
			return err
		}
		_, err = tx.Runtime().TransitionExecutionAttempt(ctx, ports.TransitionExecutionAttemptRequest{
			AttemptID: scheduled.AttemptID, ExpectedState: runtimedomain.ExecutionAttemptQueued, ExpectedVersion: currentAttempt.Version,
			NextState: runtimedomain.ExecutionAttemptRunning,
		})
		return err
	}); err != nil {
		t.Fatalf("seed RUNNING attempt/node run: %v", err)
	}

	return startResult.RunID, hop.NextNodeRunID, scheduled.AttemptID
}

// TestFinalizeExecutionAttempt_SQLite_RetryChain_PersistsAcrossRestart
// proves the retry Attempt/job FinalizeExecutionAttempt's own
// decideRetryOrExhaustion creates are genuinely durable — not just visible
// within the same process/transaction — by closing the sqlite.Store after
// creating the retry chain and reopening the exact same database file
// before asserting on it.
func TestFinalizeExecutionAttempt_SQLite_RetryChain_PersistsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agentkit-finalize-retry-restart.db")

	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() }) // safety net if a fatal failure happens before the explicit Close below
	u := sqlite.NewUnitOfWork(store)
	seq := idsource.NewSequential("id")

	// BackoffSeconds=1 (rather than the 30s the fake-backed
	// CreatesNextAttemptWithBackoff test above already proves exactly) —
	// this test's own concern is restart durability, not backoff timing;
	// the short real sleep below (past AvailableAt) is what makes the
	// retry job genuinely claimable after reopening.
	attemptDoc := retryableAttemptPolicyDocument(3, 1, 600, errorcode.CodeProviderUnavailable)
	runID, nodeRunID, attemptID := sqliteExecutionFixtureWithAttemptPolicy(t, u, seq, attemptDoc)

	_, lease := claimExecuteNodeJob(t, ctx, store, 30*time.Second)
	_, err = runtime.FinalizeExecutionAttempt(ctx, u, seq, clock.System{}, nil, runtime.FinalizeExecutionAttemptRequest{
		RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID, ExpectedVersion: 2,
		NextState: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonExecutionFailed,
		FailureCode: errorcode.CodeProviderUnavailable, JobLease: lease,
	})
	if err != nil {
		t.Fatalf("FinalizeExecutionAttempt: %v", err)
	}
	time.Sleep(1200 * time.Millisecond) // past the retry job's own 1s AvailableAt backoff

	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen Open: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	reopenedUow := sqlite.NewUnitOfWork(reopened)

	original, err := getExecutionAttemptSQLite(ctx, reopenedUow, attemptID)
	if err != nil {
		t.Fatalf("get original attempt after restart: %v", err)
	}
	if original.State != runtimedomain.ExecutionAttemptFailed || original.FailureCode != errorcode.CodeProviderUnavailable {
		t.Fatalf("original attempt after restart = %+v, want FAILED/PROVIDER_UNAVAILABLE unchanged", original)
	}

	var nodeRun runtimedomain.NodeRun
	if err := reopenedUow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		nodeRun, err = tx.Runtime().GetNodeRun(ctx, nodeRunID)
		return err
	}); err != nil {
		t.Fatalf("get node run after restart: %v", err)
	}
	if nodeRun.State != runtimedomain.NodeRunRunning {
		t.Fatalf("node run after restart = %+v, want unchanged RUNNING while a retry is pending", nodeRun)
	}

	retryJob, retryLease := claimExecuteNodeJob(t, ctx, reopened, 30*time.Second)
	var retryPayload runtime.ExecuteNodeJobPayload
	if err := json.Unmarshal(retryJob.Payload, &retryPayload); err != nil {
		t.Fatalf("decode retry job payload: %v", err)
	}
	if retryPayload.AttemptID == attemptID {
		t.Fatalf("claimed job still targets the original attempt %s, want the retry attempt", attemptID)
	}
	retryAttempt, err := getExecutionAttemptSQLite(ctx, reopenedUow, retryPayload.AttemptID)
	if err != nil {
		t.Fatalf("get retry attempt after restart: %v", err)
	}
	if retryAttempt.AttemptNumber != 2 {
		t.Fatalf("retry attempt after restart = %+v, want AttemptNumber=2", retryAttempt)
	}
	_ = retryLease
}
