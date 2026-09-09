package runtime_test

import (
	"context"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/contextsnapshot"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// TestExecuteNodeHandler_MissingContextSnapshot_RejectsDispatchWithoutCallingExecutor
// is V5-04's own "missing resource" Verify scenario: an Attempt whose bound
// snapshot has gone missing (deleted, or never durably committed) must
// never reach the executor — dispatch precondition, not best-effort.
//
// Audit finding (2026-09-08): this test used to assert the Attempt was
// left RUNNING forever on this failure — a real livelock (no path in this
// codebase ever re-drives a RUNNING Attempt whose own job keeps failing
// this exact precondition), not a deliberate "not this task's authority"
// deferral. Handle now finalizes the Attempt FAILED/EXECUTION_FAILED
// instead (an existing, valid entry in ADR-020's closed state-reason
// matrix — no new vocabulary needed), and returns nil (the job itself
// succeeded: it correctly finalized a terminal Attempt).
func TestExecuteNodeHandler_MissingContextSnapshot_RejectsDispatchWithoutCallingExecutor(t *testing.T) {
	uow, ids, _, _, attemptID := scheduledExecutionFixture(t, 600)
	job := claimableExecuteNodeJob(t, uow, attemptID)

	// scheduledExecutionFixture already routed this attempt through the
	// real ScheduleExecutableNodeRun, which durably binds a snapshot
	// (V5-04) — delete it here to simulate a "row went missing" scenario a
	// real database can otherwise only reach via out-of-band corruption.
	uow.Snapshot.ContextSnapshots().(*fake.ContextSnapshotRepository).DeleteSnapshot(mustSnapshotID(t, uow, attemptID))

	executor := &fake.NodeExecutor{Result: ports.NodeExecutionResult{State: runtimedomain.ExecutionAttemptSucceeded, SelectedOutcome: "done"}}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{}, fake.IsolationEnforcementChecker{}, sharedTestAgentRegistry(t))
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v (want nil — the job itself succeeded by finalizing a terminal Attempt)", err)
	}
	if executor.Calls != 0 {
		t.Fatalf("executor.Calls = %d, want 0 (spawn count must be zero on a failed precondition)", executor.Calls)
	}

	attempt, err := uow.Snapshot.Runtime().GetExecutionAttempt(context.Background(), attemptID)
	if err != nil {
		t.Fatalf("GetExecutionAttempt: %v", err)
	}
	if attempt.State != runtimedomain.ExecutionAttemptFailed {
		t.Fatalf("attempt.State = %s, want FAILED (never left stuck RUNNING on a precondition failure)", attempt.State)
	}
	if attempt.TerminationReason != runtimedomain.TerminationReasonExecutionFailed {
		t.Fatalf("attempt.TerminationReason = %s, want EXECUTION_FAILED", attempt.TerminationReason)
	}
}

// TestExecuteNodeHandler_ContextSnapshotBoundToDifferentAttempt_RejectsDispatch
// is V5-04's own "mismatch" Verify scenario: a snapshot that resolves but
// is bound to a DIFFERENT AttemptID than the one dispatching must be
// rejected — the same "resolve from the referenced row itself, never trust
// the caller" discipline this codebase already applies elsewhere.
func TestExecuteNodeHandler_ContextSnapshotBoundToDifferentAttempt_RejectsDispatch(t *testing.T) {
	uow, ids, _, _, attemptID := scheduledExecutionFixture(t, 600)
	job := claimableExecuteNodeJob(t, uow, attemptID)

	snapshotID := mustSnapshotID(t, uow, attemptID)
	original, err := uow.Snapshot.ContextSnapshots().GetSnapshot(context.Background(), snapshotID)
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}
	mismatched, err := contextsnapshot.NewSnapshot(
		original.ID, original.ProjectID, original.WorkItemID, contextsnapshot.AttemptID("some-other-attempt"),
		original.MessageRefs, original.ResourceRefs, original.Revisions, original.CreatedAt,
	)
	if err != nil {
		t.Fatalf("NewSnapshot (mismatched): %v", err)
	}
	uow.Snapshot.ContextSnapshots().(*fake.ContextSnapshotRepository).Overwrite(mismatched)

	executor := &fake.NodeExecutor{Result: ports.NodeExecutionResult{State: runtimedomain.ExecutionAttemptSucceeded, SelectedOutcome: "done"}}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{}, fake.IsolationEnforcementChecker{}, sharedTestAgentRegistry(t))
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v (want nil — the job itself succeeded by finalizing a terminal Attempt)", err)
	}
	if executor.Calls != 0 {
		t.Fatalf("executor.Calls = %d, want 0", executor.Calls)
	}
	attempt, err := uow.Snapshot.Runtime().GetExecutionAttempt(context.Background(), attemptID)
	if err != nil {
		t.Fatalf("GetExecutionAttempt: %v", err)
	}
	if attempt.State != runtimedomain.ExecutionAttemptFailed {
		t.Fatalf("attempt.State = %s, want FAILED", attempt.State)
	}
}

func mustSnapshotID(t *testing.T, uow *fake.UnitOfWork, attemptID string) string {
	t.Helper()
	attempt, err := uow.Snapshot.Runtime().GetExecutionAttempt(context.Background(), attemptID)
	if err != nil {
		t.Fatalf("GetExecutionAttempt: %v", err)
	}
	if attempt.ContextSnapshotID == nil {
		t.Fatalf("attempt %s has no bound context snapshot", attemptID)
	}
	return string(*attempt.ContextSnapshotID)
}

// TestExecuteNodeHandler_ContextSnapshot_MessageRefInvalid_FinalizesFailed
// and its ResourceRef sibling below are the audit finding (2026-09-08) fix:
// verifyContextSnapshot used to stop at snapshot/binding/RevisionSet and
// never dereference a single MessageRef/ResourceRef — a snapshot pinning a
// Message or Resource that no longer resolves (deleted, or simply never
// existed — the exact TOCTOU gap between scheduling and dispatch this
// dispatch-time re-check exists to close) sailed through undetected. Both
// tests simulate "became invalid between scheduling and dispatch" by
// overwriting the already-scheduled snapshot with one ref changed to
// something that cannot resolve — the same Overwrite mechanism
// TestExecuteNodeHandler_ContextSnapshotBoundToDifferentAttempt_RejectsDispatch
// already uses.
func TestExecuteNodeHandler_ContextSnapshot_MessageRefInvalid_FinalizesFailed(t *testing.T) {
	uow, ids, _, _, attemptID := scheduledExecutionFixture(t, 600)
	job := claimableExecuteNodeJob(t, uow, attemptID)

	snapshotID := mustSnapshotID(t, uow, attemptID)
	original, err := uow.Snapshot.ContextSnapshots().GetSnapshot(context.Background(), snapshotID)
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}
	tampered, err := contextsnapshot.NewSnapshot(
		original.ID, original.ProjectID, original.WorkItemID, original.AttemptID,
		[]contextsnapshot.MessageRef{{MessageID: "message-that-does-not-exist"}}, original.ResourceRefs,
		original.Revisions, original.CreatedAt,
	)
	if err != nil {
		t.Fatalf("NewSnapshot (tampered): %v", err)
	}
	uow.Snapshot.ContextSnapshots().(*fake.ContextSnapshotRepository).Overwrite(tampered)

	executor := &fake.NodeExecutor{Result: ports.NodeExecutionResult{State: runtimedomain.ExecutionAttemptSucceeded, SelectedOutcome: "done"}}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{}, fake.IsolationEnforcementChecker{}, sharedTestAgentRegistry(t))
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v (want nil — the job itself succeeded by finalizing a terminal Attempt)", err)
	}
	if executor.Calls != 0 {
		t.Fatalf("executor.Calls = %d, want 0", executor.Calls)
	}
	attempt, err := uow.Snapshot.Runtime().GetExecutionAttempt(context.Background(), attemptID)
	if err != nil {
		t.Fatalf("GetExecutionAttempt: %v", err)
	}
	if attempt.State != runtimedomain.ExecutionAttemptFailed {
		t.Fatalf("attempt.State = %s, want FAILED (a MessageRef that cannot resolve must never reach the executor or livelock RUNNING)", attempt.State)
	}
}

func TestExecuteNodeHandler_ContextSnapshot_ResourceRefInvalid_FinalizesFailed(t *testing.T) {
	uow, ids, _, _, attemptID := scheduledExecutionFixture(t, 600)
	job := claimableExecuteNodeJob(t, uow, attemptID)

	snapshotID := mustSnapshotID(t, uow, attemptID)
	original, err := uow.Snapshot.ContextSnapshots().GetSnapshot(context.Background(), snapshotID)
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}
	tampered, err := contextsnapshot.NewSnapshot(
		original.ID, original.ProjectID, original.WorkItemID, original.AttemptID,
		original.MessageRefs, []contextsnapshot.ResourceRef{{
			OwnerVersionID: "skill-version-that-does-not-exist", ResourceKey: "some-key", ContentHash: "sha256:whatever",
		}}, original.Revisions, original.CreatedAt,
	)
	if err != nil {
		t.Fatalf("NewSnapshot (tampered): %v", err)
	}
	uow.Snapshot.ContextSnapshots().(*fake.ContextSnapshotRepository).Overwrite(tampered)

	executor := &fake.NodeExecutor{Result: ports.NodeExecutionResult{State: runtimedomain.ExecutionAttemptSucceeded, SelectedOutcome: "done"}}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{}, fake.IsolationEnforcementChecker{}, sharedTestAgentRegistry(t))
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v (want nil — the job itself succeeded by finalizing a terminal Attempt)", err)
	}
	if executor.Calls != 0 {
		t.Fatalf("executor.Calls = %d, want 0", executor.Calls)
	}
	attempt, err := uow.Snapshot.Runtime().GetExecutionAttempt(context.Background(), attemptID)
	if err != nil {
		t.Fatalf("GetExecutionAttempt: %v", err)
	}
	if attempt.State != runtimedomain.ExecutionAttemptFailed {
		t.Fatalf("attempt.State = %s, want FAILED (a ResourceRef that cannot resolve must never reach the executor or livelock RUNNING)", attempt.State)
	}
}
