package runtime_test

import (
	"context"
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
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
func TestExecuteNodeHandler_MissingContextSnapshot_RejectsDispatchWithoutCallingExecutor(t *testing.T) {
	uow, ids, _, _, attemptID := scheduledExecutionFixture(t, 600)
	job := claimableExecuteNodeJob(t, uow, attemptID)

	// scheduledExecutionFixture already routed this attempt through the
	// real ScheduleExecutableNodeRun, which durably binds a snapshot
	// (V5-04) — delete it here to simulate a "row went missing" scenario a
	// real database can otherwise only reach via out-of-band corruption.
	uow.Snapshot.ContextSnapshots().(*fake.ContextSnapshotRepository).DeleteSnapshot(mustSnapshotID(t, uow, attemptID))

	executor := &fake.NodeExecutor{Result: ports.NodeExecutionResult{State: runtimedomain.ExecutionAttemptSucceeded, SelectedOutcome: "done"}}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{}, fake.IsolationEnforcementChecker{}, agentregistry.Empty())
	err := handler.Handle(context.Background(), job)
	if !errors.Is(err, runtime.ErrContextSnapshotUnverified) {
		t.Fatalf("Handle err = %v, want runtime.ErrContextSnapshotUnverified", err)
	}
	if executor.Calls != 0 {
		t.Fatalf("executor.Calls = %d, want 0 (spawn count must be zero on a failed precondition)", executor.Calls)
	}

	// The Attempt must be left RUNNING, never finalized — the same
	// "un-terminalized, not this task's authority" discipline this
	// handler's own doc comment already applies to cancel/lease-loss.
	attempt, err := uow.Snapshot.Runtime().GetExecutionAttempt(context.Background(), attemptID)
	if err != nil {
		t.Fatalf("GetExecutionAttempt: %v", err)
	}
	if attempt.State != runtimedomain.ExecutionAttemptRunning {
		t.Fatalf("attempt.State = %s, want RUNNING (never finalized on a precondition failure)", attempt.State)
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
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{}, fake.IsolationEnforcementChecker{}, agentregistry.Empty())
	err = handler.Handle(context.Background(), job)
	if !errors.Is(err, runtime.ErrContextSnapshotUnverified) {
		t.Fatalf("Handle err = %v, want runtime.ErrContextSnapshotUnverified", err)
	}
	if executor.Calls != 0 {
		t.Fatalf("executor.Calls = %d, want 0", executor.Calls)
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
