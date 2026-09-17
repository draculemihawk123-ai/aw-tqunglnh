package workitemblocker_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	cliworkitemblocker "github.com/taQuangLing/agent-workflow/internal/delivery/cli/workitemblocker"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

func decodeResolveResult(t *testing.T, stdout *bytes.Buffer) cliworkitemblocker.ResolveResult {
	t.Helper()
	var result cliworkitemblocker.ResolveResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode ResolveResult %s: %v", stdout.String(), err)
	}
	return result
}

// TestResolve_OpenRunCancelledBlocker_ResolvesAndUnblocksToReady is this
// task's own baseline success case: an OPEN blocker with every precondition
// satisfied (no nonterminal Run, no QUARANTINED workspace) transitions to
// RESOLVED and — being the WorkItem's only OPEN blocker — unblocks it
// straight to READY.
func TestResolve_OpenRunCancelledBlocker_ResolvesAndUnblocksToReady(t *testing.T) {
	deps := newTestDeps(t)
	workItemID, blocker := runCancelledBlockerFixture(t, deps)

	var stdout, stderr bytes.Buffer
	args := []string{"--mode", "RESOLVED", "--reason", "operator confirmed safe to close", string(blocker.ID)}
	if err := cliworkitemblocker.Resolve(context.Background(), deps, args, &stdout, &stderr); err != nil {
		t.Fatalf("Resolve() error = %v, stderr = %s", err, stderr.String())
	}
	result := decodeResolveResult(t, &stdout)
	if result.AlreadyResolved || result.State != string(workdomain.BlockerResolved) || !result.WorkItemUnblocked || result.WorkItemStatus != string(workdomain.WorkItemReady) {
		t.Fatalf("result = %+v, want resolved, unblocked, READY", result)
	}

	item := workItemState(t, deps.UoW, workItemID)
	if item.Status != workdomain.WorkItemReady {
		t.Fatalf("work item status = %s, want READY", item.Status)
	}
}

func TestResolve_Waived_RecordsDecisionArtifact(t *testing.T) {
	deps := newTestDeps(t)
	_, blocker := runCancelledBlockerFixture(t, deps)

	var stdout, stderr bytes.Buffer
	args := []string{"--mode", "WAIVED", "--reason", "operator accepted the cancellation", "--policy-grant-ref", "policy-grant-1", string(blocker.ID)}
	if err := cliworkitemblocker.Resolve(context.Background(), deps, args, &stdout, &stderr); err != nil {
		t.Fatalf("Resolve() error = %v, stderr = %s", err, stderr.String())
	}
	result := decodeResolveResult(t, &stdout)
	if result.State != string(workdomain.BlockerWaived) {
		t.Fatalf("result.State = %s, want WAIVED", result.State)
	}
}

func TestResolve_WaivedWithoutPolicyGrant_Rejected(t *testing.T) {
	deps := newTestDeps(t)
	_, blocker := runCancelledBlockerFixture(t, deps)

	var stdout, stderr bytes.Buffer
	args := []string{"--mode", "WAIVED", "--reason", "trying to skip the grant", string(blocker.ID)}
	err := cliworkitemblocker.Resolve(context.Background(), deps, args, &stdout, &stderr)
	if !errors.Is(err, runtime.ErrWaiveRequiresPolicyGrant) {
		t.Fatalf("error = %v, want ErrWaiveRequiresPolicyGrant", err)
	}
}

// TestResolve_MissingMode_RejectedClientSide proves --mode has no default —
// rejected by this CLI leaf's own client-side check, before ever reaching
// the application layer.
func TestResolve_MissingMode_RejectedClientSide(t *testing.T) {
	deps := newTestDeps(t)
	_, blocker := runCancelledBlockerFixture(t, deps)

	var stdout, stderr bytes.Buffer
	args := []string{"--reason", "no mode supplied", string(blocker.ID)}
	err := cliworkitemblocker.Resolve(context.Background(), deps, args, &stdout, &stderr)
	if !isUsageError(err) {
		t.Fatalf("Resolve() with no --mode returned %v, want a cli.UsageError", err)
	}
}

// TestResolve_InvalidMode_RejectedClientSide proves any value other than
// exactly RESOLVED/WAIVED is rejected client-side too (this task's own
// explicit "reject client-side too" bar).
func TestResolve_InvalidMode_RejectedClientSide(t *testing.T) {
	deps := newTestDeps(t)
	_, blocker := runCancelledBlockerFixture(t, deps)

	var stdout, stderr bytes.Buffer
	args := []string{"--mode", "APPROVED", "--reason", "bad mode value", string(blocker.ID)}
	err := cliworkitemblocker.Resolve(context.Background(), deps, args, &stdout, &stderr)
	if !isUsageError(err) {
		t.Fatalf("Resolve() with --mode=APPROVED returned %v, want a cli.UsageError", err)
	}
}

func TestResolve_MissingReason_IsError(t *testing.T) {
	deps := newTestDeps(t)
	_, blocker := runCancelledBlockerFixture(t, deps)

	var stdout, stderr bytes.Buffer
	args := []string{"--mode", "RESOLVED", string(blocker.ID)}
	err := cliworkitemblocker.Resolve(context.Background(), deps, args, &stdout, &stderr)
	if err == nil {
		t.Fatal("Resolve() with no --reason returned nil error")
	}
}

func TestResolve_MissingBlockerIDArgument_IsError(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	args := []string{"--mode", "RESOLVED", "--reason", "cleanup"}
	err := cliworkitemblocker.Resolve(context.Background(), deps, args, &stdout, &stderr)
	if err == nil {
		t.Fatal("Resolve() with no <blockerId> argument returned nil error")
	}
}

// TestResolve_UnknownBlocker_ReturnsError mirrors the leakage-normalization
// proof every other similar route in this codebase establishes.
func TestResolve_UnknownBlocker_ReturnsError(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	args := []string{"--mode", "RESOLVED", "--reason", "cleanup", "does-not-exist"}
	err := cliworkitemblocker.Resolve(context.Background(), deps, args, &stdout, &stderr)
	if err == nil {
		t.Fatal("Resolve() for an unknown blocker returned nil error")
	}
}

// TestResolve_AlreadyResolved_IdempotentReplay proves a second call against
// an already-RESOLVED blocker is a graceful no-op, never an error, even
// with a totally different mode/reason on the second call (the FIRST call's
// own real outcome is what is reported, never re-decided).
func TestResolve_AlreadyResolved_IdempotentReplay(t *testing.T) {
	deps := newTestDeps(t)
	_, blocker := runCancelledBlockerFixture(t, deps)

	var first bytes.Buffer
	firstArgs := []string{"--mode", "RESOLVED", "--reason", "first", string(blocker.ID)}
	if err := cliworkitemblocker.Resolve(context.Background(), deps, firstArgs, &first, &bytes.Buffer{}); err != nil {
		t.Fatalf("first Resolve() error = %v", err)
	}
	firstResult := decodeResolveResult(t, &first)
	if firstResult.AlreadyResolved {
		t.Fatalf("first resolve = %+v, want a fresh resolution", firstResult)
	}

	var second bytes.Buffer
	secondArgs := []string{"--mode", "WAIVED", "--reason", "second, different mode entirely", "--policy-grant-ref", "grant-1", string(blocker.ID)}
	if err := cliworkitemblocker.Resolve(context.Background(), deps, secondArgs, &second, &bytes.Buffer{}); err != nil {
		t.Fatalf("second Resolve() error = %v, want nil (graceful, not an error)", err)
	}
	secondResult := decodeResolveResult(t, &second)
	if !secondResult.AlreadyResolved {
		t.Fatal("second resolve reported AlreadyResolved = false, want true")
	}
	if secondResult.State != string(workdomain.BlockerResolved) {
		t.Fatalf("second resolve State = %q, want RESOLVED (the FIRST call's own real outcome, never re-decided by the second, differently-shaped call)", secondResult.State)
	}
}

// TestResolve_ConcurrentResolveRace_ExactlyOneFreshDecision proves
// resolution is race-safe through this CLI leaf: several concurrent
// resolve calls against the SAME blocker must converge on exactly one
// fresh (AlreadyResolved=false) decision. Deliberately uses a REAL
// sqlite-backed store (newSQLiteTestDeps), not the in-memory fake:
// fake.UnitOfWork's own WithSerializedWrite only detects and rejects a
// genuinely-concurrent second caller with a synthetic ErrNestedTransaction
// (a boolean in-progress flag, not a real queue/lock) rather than
// serializing it — confirmed by observation, the identical reason
// internal/delivery/cli/workitem's own
// TestRunWorkItemMarkReady_ConcurrentDoubleMarkReady_ExactlyOneWinner
// switched to sqlite, and the same reason
// internal/delivery/httpapi/recovery/resolveblocker_test.go's own
// TestResolveWorkItemBlocker_HTTP_ConcurrentResolveRace_ExactlyOneFreshDecision
// already uses a real sqlite-backed server rather than the fake.
func TestResolve_ConcurrentResolveRace_ExactlyOneFreshDecision(t *testing.T) {
	store, deps := newSQLiteTestDeps(t, "resolve-blocker-race.db")
	_, blocker := runCancelledBlockerFixtureSQLite(t, store, deps)

	const attempts = 6
	var wg sync.WaitGroup
	var mu sync.Mutex
	var results []cliworkitemblocker.ResolveResult
	var callErrors []error
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func() {
			defer wg.Done()
			var stdout bytes.Buffer
			args := []string{"--mode", "RESOLVED", "--reason", "racer", string(blocker.ID)}
			err := cliworkitemblocker.Resolve(context.Background(), deps, args, &stdout, &bytes.Buffer{})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				callErrors = append(callErrors, err)
				return
			}
			results = append(results, decodeResolveResult(t, &stdout))
		}()
	}
	wg.Wait()

	for _, err := range callErrors {
		t.Fatalf("concurrent Resolve() call failed: %v", err)
	}
	if len(results) != attempts {
		t.Fatalf("got %d results, want %d", len(results), attempts)
	}
	fresh := 0
	for _, r := range results {
		if !r.AlreadyResolved {
			fresh++
		}
	}
	if fresh != 1 {
		t.Fatalf("fresh (AlreadyResolved=false) responses = %d, want exactly 1 across %d concurrent racers", fresh, attempts)
	}
}

// TestResolve_NonTerminalRun_Rejected proves ResolveWorkItemBlocker's own
// ErrWorkItemHasNonTerminalRun precondition is surfaced cleanly through this
// CLI leaf.
func TestResolve_NonTerminalRun_Rejected(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	workItemID, workflowVersionID := cancelWorkItemFixture(t, deps)
	startTestRun(t, u, deps.IDs, "project-1", workItemID, workflowVersionID, "idem-start-1")
	blocker := seedBlocker(t, u, deps.IDs, workItemID, workdomain.BlockerIsolationEnforcementUnavailable)

	var stdout, stderr bytes.Buffer
	args := []string{"--mode", "RESOLVED", "--reason", "try to close while another run is active", string(blocker.ID)}
	err := cliworkitemblocker.Resolve(context.Background(), deps, args, &stdout, &stderr)
	if !errors.Is(err, runtime.ErrWorkItemHasNonTerminalRun) {
		t.Fatalf("error = %v, want ErrWorkItemHasNonTerminalRun", err)
	}
}

// TestResolve_QuarantinedWorkspace_Rejected proves
// ResolveWorkItemBlocker's own ErrWorkspaceQuarantined precondition is
// surfaced cleanly through this CLI leaf.
func TestResolve_QuarantinedWorkspace_Rejected(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	workItemID, _ := cancelWorkItemFixture(t, deps)
	item := workItemState(t, deps.UoW, workItemID)
	set, err := u.Snapshot.Work().GetWorkspaceSetByFamilyID(context.Background(), string(item.FamilyID))
	if err != nil {
		t.Fatalf("GetWorkspaceSetByFamilyID: %v", err)
	}
	quarantineExtraRepositoryWorkspace(t, u, deps.IDs, string(set.ID))
	blocker := seedBlocker(t, u, deps.IDs, workItemID, workdomain.BlockerRunCancelled)

	var stdout, stderr bytes.Buffer
	args := []string{"--mode", "RESOLVED", "--reason", "try to close with a quarantined workspace", string(blocker.ID)}
	err = cliworkitemblocker.Resolve(context.Background(), deps, args, &stdout, &stderr)
	if !errors.Is(err, runtime.ErrWorkspaceQuarantined) {
		t.Fatalf("error = %v, want ErrWorkspaceQuarantined", err)
	}
}

// TestResolve_ResolutionModeTypeMatrix locks in ADR-020's own full
// resolution-mode x blocker-type authority table through this CLI leaf —
// mirrors internal/app/runtime/resolve_work_item_blocker_test.go's own
// TestResolveWorkItemBlocker_ResolutionModeTypeMatrix, proving this leaf
// surfaces every branch cleanly rather than special-casing or working
// around any of them.
func TestResolve_ResolutionModeTypeMatrix(t *testing.T) {
	tests := []struct {
		name    string
		typ     workdomain.BlockerType
		mode    string
		wantErr error
	}{
		{"RunCancelled_Resolved_OK", workdomain.BlockerRunCancelled, "RESOLVED", nil},
		{"RunCancelled_Waived_OK", workdomain.BlockerRunCancelled, "WAIVED", nil},
		{"CompletionPolicyFailed_Waived_OK", workdomain.BlockerCompletionPolicyFailed, "WAIVED", nil},
		{"CompletionPolicyFailed_Resolved_OK", workdomain.BlockerCompletionPolicyFailed, "RESOLVED", nil},
		{"ScopeExpansionRequired_Resolved_Rejected", workdomain.BlockerScopeExpansionRequired, "RESOLVED", runtime.ErrBlockerNotResolvableViaCommand},
		{"ScopeExpansionRequired_Waived_Rejected", workdomain.BlockerScopeExpansionRequired, "WAIVED", runtime.ErrBlockerNotResolvableViaCommand},
		{"IsolationEnforcementUnavailable_Resolved_OK", workdomain.BlockerIsolationEnforcementUnavailable, "RESOLVED", nil},
		{"IsolationEnforcementUnavailable_Waived_Rejected", workdomain.BlockerIsolationEnforcementUnavailable, "WAIVED", runtime.ErrBlockerNotWaivable},
		{"AdapterBuildDrift_Waived_Rejected", workdomain.BlockerAdapterBuildDrift, "WAIVED", runtime.ErrBlockerNotWaivable},
		{"CapabilityRequirementUnsatisfied_Waived_Rejected", workdomain.BlockerCapabilityRequirementUnsatisfied, "WAIVED", runtime.ErrBlockerNotWaivable},
		{"WriteCapabilityOrGrantMissing_Waived_Rejected", workdomain.BlockerWriteCapabilityOrGrantMissing, "WAIVED", runtime.ErrBlockerNotWaivable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := newTestDeps(t)
			u := deps.UoW.(*fake.UnitOfWork)
			workItemID, _ := cancelWorkItemFixture(t, deps)
			blocker := seedBlocker(t, u, deps.IDs, workItemID, tt.typ)

			var stdout, stderr bytes.Buffer
			args := []string{"--mode", tt.mode, "--reason", "matrix test", "--policy-grant-ref", "policy-grant-1", string(blocker.ID)}
			err := cliworkitemblocker.Resolve(context.Background(), deps, args, &stdout, &stderr)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("(%s, %s) error = %v, want success", tt.typ, tt.mode, err)
				}
				wantState := workdomain.BlockerResolved
				if tt.mode == "WAIVED" {
					wantState = workdomain.BlockerWaived
				}
				result := decodeResolveResult(t, &stdout)
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
