package runtime_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// V4-12C's own blocker-resolution authority-matrix tests
// (docs/design/06-v4-runtime-engine.md, ADR-020). See cancel_work_item_test.go
// for the shared fixtures (cancelWorkItemFixture, startRun, workItemState,
// workItemBlockers) and CancelWorkItem's own dedicated tests.

// runCancelledBlockerFixture creates a WorkItem+Run, cancels it standalone
// via CancelRun, drives the coordinator to CANCELLED, and returns the real
// (never seeded) RUN_CANCELLED blocker this produces — the base fixture for
// every test that needs a genuinely-produced, genuinely-OPEN blocker with
// the WorkItem itself genuinely BLOCKED.
func runCancelledBlockerFixture(t *testing.T) (uow *fake.UnitOfWork, ids idsource.Source, workItemID, workflowVersionID string, blocker workdomain.WorkItemBlocker) {
	t.Helper()
	uow, ids, workItemID, workflowVersionID = cancelWorkItemFixture(t)
	run := startRun(t, uow, ids, workItemID, workflowVersionID, "idem-start-1")
	cancelActor(t, uow, ids, run.RunID)
	driveCancelRunCoordinator(t, uow, ids, run.RunID)
	if got := runState(t, uow, run.RunID).State; got != runtimedomain.WorkflowRunCancelled {
		t.Fatalf("run state = %s, want CANCELLED", got)
	}
	if got := workItemState(t, uow, workItemID).Status; got != workdomain.WorkItemBlocked {
		t.Fatalf("work item status = %s, want BLOCKED", got)
	}
	for _, b := range workItemBlockers(t, uow, workItemID) {
		if b.Type == workdomain.BlockerRunCancelled {
			blocker = b
		}
	}
	if blocker.ID == "" {
		t.Fatal("no RUN_CANCELLED blocker found")
	}
	return
}

// seedBlocker directly inserts a new, OPEN WorkItemBlocker of blockerType —
// V4-12C's own accepted test-setup shortcut (confirmed with the user) for
// the five blocker types with no real producer yet in this codebase (the
// four admission reasons, COMPLETION_POLICY_FAILED), used here to lock in
// ResolveWorkItemBlocker's own resolution-mode authority matrix ahead of
// whichever later task (V5-08, V5-11) first wires up a real one. Never
// touches WorkItem.Status — a caller that also needs the WorkItem genuinely
// BLOCKED does that separately.
func seedBlocker(t *testing.T, uow *fake.UnitOfWork, ids idsource.Source, workItemID string, blockerType workdomain.BlockerType) workdomain.WorkItemBlocker {
	t.Helper()
	item := workItemState(t, uow, workItemID)
	var created workdomain.WorkItemBlocker
	err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		blocker, err := workdomain.NewWorkItemBlocker(
			workdomain.BlockerID(ids.NewID()), item.ProjectID, item.ID, blockerType, "", "", "", "seeded for test", time.Now().UTC(),
		)
		if err != nil {
			return err
		}
		created, err = tx.Work().CreateWorkItemBlocker(context.Background(), blocker)
		return err
	})
	if err != nil {
		t.Fatalf("seed blocker %s: %v", blockerType, err)
	}
	return created
}

// quarantineExtraRepositoryWorkspace registers a second repository under the
// same project, and inserts a QUARANTINED RepositoryWorkspace row for it
// directly under familyWorkspaceSetID — test setup mirroring
// internal/app/workspacereconcile's own "construct the domain value with the
// target State directly, bypass the real reconcile flow" precedent for
// reaching a QUARANTINED fixture without driving the real detection path.
func quarantineExtraRepositoryWorkspace(t *testing.T, uow *fake.UnitOfWork, ids idsource.Source, workspaceSetID string) {
	t.Helper()
	mustCreateActiveRepository(t, uow, ids, "project-1", "repo-2")
	err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Work().CreateRepositoryWorkspace(context.Background(), workspace.RepositoryWorkspace{
			ID: workspace.RepositoryWorkspaceID(ids.NewID()), WorkspaceSetID: workspace.WorkspaceSetID(workspaceSetID),
			RepositoryID: project.RepositoryID("repo-2"), Generation: 1, Locator: "handle-repo-2",
			BaseRevision: "cafebabecafebabecafebabecafebabecafebabe", State: workspace.RepositoryWorkspaceQuarantined, Version: 1,
		})
		return err
	})
	if err != nil {
		t.Fatalf("seed quarantined repository workspace: %v", err)
	}
}

func resolveBlocker(t *testing.T, uow *fake.UnitOfWork, blockerID string, mode runtime.ResolutionMode, policyGrantRef string) (runtime.ResolveWorkItemBlockerResult, error) {
	t.Helper()
	return runtime.ResolveWorkItemBlocker(context.Background(), uow, runtime.ResolveWorkItemBlockerRequest{
		BlockerID: blockerID, Mode: mode, Actor: "operator-1", Reason: "resolved for test", PolicyGrantRef: policyGrantRef,
	})
}

// TestResolveWorkItemBlocker_OpenRunCancelledBlocker_ResolvesAndUnblocksToReady
// is the task's own baseline success case: an OPEN blocker with every
// precondition satisfied (no nonterminal Run, no QUARANTINED workspace)
// transitions to RESOLVED and — being the WorkItem's only OPEN blocker —
// unblocks it straight to READY (never ACTIVE: the Run that caused the
// block is gone, a NEW run is what comes next).
func TestResolveWorkItemBlocker_OpenRunCancelledBlocker_ResolvesAndUnblocksToReady(t *testing.T) {
	uow, _, workItemID, _, blocker := runCancelledBlockerFixture(t)

	result, err := resolveBlocker(t, uow, string(blocker.ID), runtime.ResolutionModeResolved, "")
	if err != nil {
		t.Fatalf("ResolveWorkItemBlocker: %v", err)
	}
	if result.AlreadyResolved || result.State != string(workdomain.BlockerResolved) || !result.WorkItemUnblocked || result.WorkItemStatus != string(workdomain.WorkItemReady) {
		t.Fatalf("result = %+v, want resolved, unblocked, READY", result)
	}
	if got := workItemState(t, uow, workItemID).Status; got != workdomain.WorkItemReady {
		t.Fatalf("work item status = %s, want READY", got)
	}
}

// TestResolveWorkItemBlocker_AlreadyResolved_Idempotent proves a second call
// against an already-RESOLVED blocker is a harmless no-op, never an error.
func TestResolveWorkItemBlocker_AlreadyResolved_Idempotent(t *testing.T) {
	uow, _, _, _, blocker := runCancelledBlockerFixture(t)
	if _, err := resolveBlocker(t, uow, string(blocker.ID), runtime.ResolutionModeResolved, ""); err != nil {
		t.Fatalf("first ResolveWorkItemBlocker: %v", err)
	}
	result, err := resolveBlocker(t, uow, string(blocker.ID), runtime.ResolutionModeResolved, "")
	if err != nil {
		t.Fatalf("second ResolveWorkItemBlocker: %v", err)
	}
	if !result.AlreadyResolved || result.State != string(workdomain.BlockerResolved) {
		t.Fatalf("second result = %+v, want AlreadyResolved=true State=RESOLVED", result)
	}
}

// TestResolveWorkItemBlocker_AlreadyWaived_Idempotent mirrors
// TestResolveWorkItemBlocker_AlreadyResolved_Idempotent for the WAIVED path.
func TestResolveWorkItemBlocker_AlreadyWaived_Idempotent(t *testing.T) {
	uow, _, _, _, blocker := runCancelledBlockerFixture(t)
	if _, err := resolveBlocker(t, uow, string(blocker.ID), runtime.ResolutionModeWaived, "grant-1"); err != nil {
		t.Fatalf("first ResolveWorkItemBlocker: %v", err)
	}
	result, err := resolveBlocker(t, uow, string(blocker.ID), runtime.ResolutionModeWaived, "grant-1")
	if err != nil {
		t.Fatalf("second ResolveWorkItemBlocker: %v", err)
	}
	if !result.AlreadyResolved || result.State != string(workdomain.BlockerWaived) {
		t.Fatalf("second result = %+v, want AlreadyResolved=true State=WAIVED", result)
	}
}

// TestResolveWorkItemBlocker_NonTerminalRun_Rejected proves ResolveWorkItemBlocker
// refuses to act while the WorkItem still has a non-terminal WorkflowRun —
// simulated here with a seeded (unrelated) blocker coexisting with a real,
// currently-RUNNING run, since a real producer for a non-RUN_CANCELLED
// blocker type could plausibly coexist with a run that has not yet reached
// its own terminal state.
func TestResolveWorkItemBlocker_NonTerminalRun_Rejected(t *testing.T) {
	uow, ids, workItemID, workflowVersionID := cancelWorkItemFixture(t)
	startRun(t, uow, ids, workItemID, workflowVersionID, "idem-start-1")
	blocker := seedBlocker(t, uow, ids, workItemID, workdomain.BlockerIsolationEnforcementUnavailable)

	_, err := resolveBlocker(t, uow, string(blocker.ID), runtime.ResolutionModeResolved, "")
	if !errors.Is(err, runtime.ErrWorkItemHasNonTerminalRun) {
		t.Fatalf("error = %v, want ErrWorkItemHasNonTerminalRun", err)
	}
}

// TestResolveWorkItemBlocker_QuarantinedWorkspace_Rejected proves
// ResolveWorkItemBlocker refuses to act while any repository workspace in
// the WorkItem's own family is QUARANTINED (V3-10).
func TestResolveWorkItemBlocker_QuarantinedWorkspace_Rejected(t *testing.T) {
	uow, ids, workItemID, _ := cancelWorkItemFixture(t)
	item := workItemState(t, uow, workItemID)
	set, err := uow.Snapshot.Work().GetWorkspaceSetByFamilyID(context.Background(), string(item.FamilyID))
	if err != nil {
		t.Fatalf("GetWorkspaceSetByFamilyID: %v", err)
	}
	quarantineExtraRepositoryWorkspace(t, uow, ids, string(set.ID))
	blocker := seedBlocker(t, uow, ids, workItemID, workdomain.BlockerRunCancelled)

	_, err = resolveBlocker(t, uow, string(blocker.ID), runtime.ResolutionModeResolved, "")
	if !errors.Is(err, runtime.ErrWorkspaceQuarantined) {
		t.Fatalf("error = %v, want ErrWorkspaceQuarantined", err)
	}
}

// TestResolveWorkItemBlocker_MissingMode_Rejected proves Mode has no default —
// ADR-020's own "Payload MUST chọn resolution mode tường minh, không có mặc định".
func TestResolveWorkItemBlocker_MissingMode_Rejected(t *testing.T) {
	uow, _, _, _, blocker := runCancelledBlockerFixture(t)
	_, err := runtime.ResolveWorkItemBlocker(context.Background(), uow, runtime.ResolveWorkItemBlockerRequest{
		BlockerID: string(blocker.ID), Actor: "operator-1", Reason: "no mode supplied",
	})
	if !errors.Is(err, runtime.ErrResolutionModeRequired) {
		t.Fatalf("error = %v, want ErrResolutionModeRequired", err)
	}
}

// TestResolveWorkItemBlocker_WaiveMissingPolicyGrant_Rejected proves WAIVED
// requires a non-blank PolicyGrantRef.
func TestResolveWorkItemBlocker_WaiveMissingPolicyGrant_Rejected(t *testing.T) {
	uow, _, _, _, blocker := runCancelledBlockerFixture(t)
	_, err := resolveBlocker(t, uow, string(blocker.ID), runtime.ResolutionModeWaived, "")
	if !errors.Is(err, runtime.ErrWaiveRequiresPolicyGrant) {
		t.Fatalf("error = %v, want ErrWaiveRequiresPolicyGrant", err)
	}
}

// TestResolveWorkItemBlocker_ResolutionModeTypeMatrix locks in ADR-020's own
// full resolution-mode x blocker-type authority table (this task's own
// Verify line): WAIVED is valid only for RUN_CANCELLED/COMPLETION_POLICY_FAILED;
// SCOPE_EXPANSION_REQUIRED rejects BOTH modes outright (only the real
// approval/reconcile flow may ever resolve it); every other type (including
// all four admission reasons, which have no real producer yet) accepts
// RESOLVED.
func TestResolveWorkItemBlocker_ResolutionModeTypeMatrix(t *testing.T) {
	tests := []struct {
		name    string
		typ     workdomain.BlockerType
		mode    runtime.ResolutionMode
		wantErr error
	}{
		{"RunCancelled_Resolved_OK", workdomain.BlockerRunCancelled, runtime.ResolutionModeResolved, nil},
		{"RunCancelled_Waived_OK", workdomain.BlockerRunCancelled, runtime.ResolutionModeWaived, nil},
		{"CompletionPolicyFailed_Waived_OK", workdomain.BlockerCompletionPolicyFailed, runtime.ResolutionModeWaived, nil},
		{"CompletionPolicyFailed_Resolved_OK", workdomain.BlockerCompletionPolicyFailed, runtime.ResolutionModeResolved, nil},
		{"ScopeExpansionRequired_Resolved_Rejected", workdomain.BlockerScopeExpansionRequired, runtime.ResolutionModeResolved, runtime.ErrBlockerNotResolvableViaCommand},
		{"ScopeExpansionRequired_Waived_Rejected", workdomain.BlockerScopeExpansionRequired, runtime.ResolutionModeWaived, runtime.ErrBlockerNotResolvableViaCommand},
		{"IsolationEnforcementUnavailable_Resolved_OK", workdomain.BlockerIsolationEnforcementUnavailable, runtime.ResolutionModeResolved, nil},
		{"IsolationEnforcementUnavailable_Waived_Rejected", workdomain.BlockerIsolationEnforcementUnavailable, runtime.ResolutionModeWaived, runtime.ErrBlockerNotWaivable},
		{"AdapterBuildDrift_Waived_Rejected", workdomain.BlockerAdapterBuildDrift, runtime.ResolutionModeWaived, runtime.ErrBlockerNotWaivable},
		{"CapabilityRequirementUnsatisfied_Waived_Rejected", workdomain.BlockerCapabilityRequirementUnsatisfied, runtime.ResolutionModeWaived, runtime.ErrBlockerNotWaivable},
		{"WriteCapabilityOrGrantMissing_Waived_Rejected", workdomain.BlockerWriteCapabilityOrGrantMissing, runtime.ResolutionModeWaived, runtime.ErrBlockerNotWaivable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uow, ids, workItemID, _ := cancelWorkItemFixture(t)
			blocker := seedBlocker(t, uow, ids, workItemID, tt.typ)

			result, err := resolveBlocker(t, uow, string(blocker.ID), tt.mode, "policy-grant-1")
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("(%s, %s) error = %v, want success", tt.typ, tt.mode, err)
				}
				wantState := workdomain.BlockerResolved
				if tt.mode == runtime.ResolutionModeWaived {
					wantState = workdomain.BlockerWaived
				}
				if result.State != string(wantState) {
					t.Fatalf("(%s, %s) result.State = %s, want %s", tt.typ, tt.mode, result.State, wantState)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("(%s, %s) error = %v, want %v", tt.typ, tt.mode, err, tt.wantErr)
			}
		})
	}
}

// TestResolveWorkItemBlocker_MultipleBlockers_StaysBlockedUntilLastCleared
// proves the two-separate-conditions rule directly: a WorkItem with two OPEN
// blockers stays BLOCKED after the first is resolved, and only unblocks to
// READY once the second (last) one clears too.
func TestResolveWorkItemBlocker_MultipleBlockers_StaysBlockedUntilLastCleared(t *testing.T) {
	uow, ids, workItemID, _, first := runCancelledBlockerFixture(t)
	second := seedBlocker(t, uow, ids, workItemID, workdomain.BlockerCompletionPolicyFailed)

	firstResult, err := resolveBlocker(t, uow, string(first.ID), runtime.ResolutionModeResolved, "")
	if err != nil {
		t.Fatalf("resolve first blocker: %v", err)
	}
	if firstResult.WorkItemUnblocked {
		t.Fatalf("first result = %+v, want WorkItemUnblocked=false (second blocker still OPEN)", firstResult)
	}
	if got := workItemState(t, uow, workItemID).Status; got != workdomain.WorkItemBlocked {
		t.Fatalf("work item status after resolving first blocker = %s, want still BLOCKED", got)
	}

	secondResult, err := resolveBlocker(t, uow, string(second.ID), runtime.ResolutionModeResolved, "")
	if err != nil {
		t.Fatalf("resolve second blocker: %v", err)
	}
	if !secondResult.WorkItemUnblocked || secondResult.WorkItemStatus != string(workdomain.WorkItemReady) {
		t.Fatalf("second result = %+v, want WorkItemUnblocked=true Status=READY", secondResult)
	}
	if got := workItemState(t, uow, workItemID).Status; got != workdomain.WorkItemReady {
		t.Fatalf("work item status after resolving second blocker = %s, want READY", got)
	}
}

// TestRace_ResolveWorkItemBlockerVsCancelWorkItem_IntentFirst_WorkItemStaysCancelled
// is the "intent trước" half of the required race: CancelWorkItem, called
// while the WorkItem is still BLOCKED with an OPEN blocker (its one Run
// already terminal), finds every Run already terminal and terminalizes the
// WorkItem straight to CANCELLED in that same call — never touching the
// still-OPEN blocker. A LATER ResolveWorkItemBlocker call can still validly
// resolve that now-orphaned blocker (nothing about a blocker's own CAS
// depends on its WorkItem's current status), but the WorkItem itself can
// never be revived to READY once CANCELLED — closeWorkItemBlockerTx's own
// "only unblock a WorkItem that is currently BLOCKED" check silently no-ops.
func TestRace_ResolveWorkItemBlockerVsCancelWorkItem_IntentFirst_WorkItemStaysCancelled(t *testing.T) {
	uow, ids, workItemID, _, blocker := runCancelledBlockerFixture(t)

	cancelResult, err := runtime.CancelWorkItem(context.Background(), uow, ids, runtime.CancelWorkItemRequest{
		WorkItemID: workItemID, Actor: "operator-1", Reason: "abandon entirely",
	})
	if err != nil {
		t.Fatalf("CancelWorkItem: %v", err)
	}
	if cancelResult.Status != string(workdomain.WorkItemCancelled) {
		t.Fatalf("CancelWorkItem result = %+v, want Status=CANCELLED (its only Run was already terminal)", cancelResult)
	}

	result, err := resolveBlocker(t, uow, string(blocker.ID), runtime.ResolutionModeResolved, "")
	if err != nil {
		t.Fatalf("ResolveWorkItemBlocker after WorkItem already CANCELLED: %v", err)
	}
	if result.WorkItemUnblocked {
		t.Fatalf("result = %+v, want WorkItemUnblocked=false — a CANCELLED WorkItem must never be revived", result)
	}
	if got := workItemState(t, uow, workItemID).Status; got != workdomain.WorkItemCancelled {
		t.Fatalf("work item status after stale resolve = %s, want still CANCELLED", got)
	}
}

// TestRace_ResolveWorkItemBlockerVsCancelWorkItem_ResolveFirst_LaterCancelStillQuiescesCorrectly
// is the "resolve trước" half: resolving the blocker first correctly cycles
// the WorkItem back to READY, a fresh run can then start normally, and a
// LATER CancelWorkItem call still quiesces that fresh run through the exact
// same protocol — proving the earlier blocker/resolve cycle left no stale
// state behind to confuse a subsequent, unrelated cancellation.
func TestRace_ResolveWorkItemBlockerVsCancelWorkItem_ResolveFirst_LaterCancelStillQuiescesCorrectly(t *testing.T) {
	uow, ids, workItemID, workflowVersionID, blocker := runCancelledBlockerFixture(t)

	if _, err := resolveBlocker(t, uow, string(blocker.ID), runtime.ResolutionModeResolved, ""); err != nil {
		t.Fatalf("ResolveWorkItemBlocker: %v", err)
	}
	if got := workItemState(t, uow, workItemID).Status; got != workdomain.WorkItemReady {
		t.Fatalf("work item status after resolve = %s, want READY", got)
	}

	secondRun := startRun(t, uow, ids, workItemID, workflowVersionID, "idem-start-2")
	if _, err := runtime.CancelWorkItem(context.Background(), uow, ids, runtime.CancelWorkItemRequest{
		WorkItemID: workItemID, Actor: "operator-1", Reason: "cancel the fresh run too",
	}); err != nil {
		t.Fatalf("CancelWorkItem: %v", err)
	}
	if got := runState(t, uow, secondRun.RunID).State; got != runtimedomain.WorkflowRunCancelling {
		t.Fatalf("second run state = %s, want CANCELLING", got)
	}

	driveCancelRunCoordinator(t, uow, ids, secondRun.RunID)
	if got := runState(t, uow, secondRun.RunID).State; got != runtimedomain.WorkflowRunCancelled {
		t.Fatalf("second run state after coordinator sweep = %s, want CANCELLED", got)
	}
	if got := workItemState(t, uow, workItemID).Status; got != workdomain.WorkItemCancelled {
		t.Fatalf("work item status after second run closed = %s, want CANCELLED", got)
	}
}
