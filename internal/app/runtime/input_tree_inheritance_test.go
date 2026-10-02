package runtime_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// ADR-030 decision 2 (V9-01): the attempt that replaces another WITHIN THE
// SAME NodeRun copies the predecessor's InputTrees inside the creating
// transaction, so whatever a failed or crashed read-only attempt left behind
// never becomes its successor's input. These tests cover both creators —
// technical retry (finalize.go) and crash recovery (recovery_reaper.go) —
// and the first attempt of a NodeRun (schedule.go), which records nothing.

func inputTreesFixture() map[project.RepositoryID]string {
	return map[project.RepositoryID]string{
		"repo-1": strings.Repeat("ab", 20),
		"repo-2": strings.Repeat("cd", 20),
	}
}

func recordTrees(t *testing.T, uow ports.UnitOfWork, attemptID string, trees map[project.RepositoryID]string) {
	t.Helper()
	if err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		applied, err := tx.Runtime().RecordAttemptInputTrees(context.Background(), attemptID, trees)
		if err == nil && !applied {
			t.Fatal("RecordAttemptInputTrees was not applied to a fresh attempt")
		}
		return err
	}); err != nil {
		t.Fatalf("RecordAttemptInputTrees: %v", err)
	}
}

func equalTrees(a, b map[project.RepositoryID]string) bool {
	if len(a) != len(b) {
		return false
	}
	for repositoryID, treeID := range a {
		if b[repositoryID] != treeID {
			return false
		}
	}
	return true
}

func successorOf(t *testing.T, attempts map[string]runtimedomain.ExecutionAttempt, predecessorID string) runtimedomain.ExecutionAttempt {
	t.Helper()
	for id, attempt := range attempts {
		if id != predecessorID {
			return attempt
		}
	}
	t.Fatalf("no successor attempt among %+v", attempts)
	return runtimedomain.ExecutionAttempt{}
}

// Technical retry: the retry attempt carries the InputTrees the failed
// attempt had recorded.
func TestExecuteNodeHandler_RetryableFailure_RetryInheritsInputTrees(t *testing.T) {
	attemptDoc := retryableAttemptPolicyDocument(3, 30, 600, errorcode.CodeProviderUnavailable)
	uow, ids, _, _, attemptID := scheduledExecutionFixtureWithAttemptPolicy(t, attemptDoc)

	// Scheduling creates the FIRST attempt of a NodeRun with no InputTrees:
	// the snapshot happens at execution time (a new NodeRun always snapshots
	// afresh).
	if trees := uowAttempt(t, uow, attemptID).InputTrees; len(trees) != 0 {
		t.Fatalf("a first attempt starts with InputTrees %v, want none", trees)
	}
	recorded := inputTreesFixture()
	recordTrees(t, uow, attemptID, recorded)

	job := claimableExecuteNodeJob(t, uow, attemptID)
	executor := &fake.NodeExecutor{Result: ports.NodeExecutionResult{State: runtimedomain.ExecutionAttemptFailed, ErrorCode: errorcode.CodeProviderUnavailable}}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.NewFixed(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)), fake.IsolationEnforcementChecker{}, sharedTestAgentRegistry(t), nil)
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	attempts := uow.Snapshot.Runtime().(*fake.RuntimeRepository).Attempts()
	if len(attempts) != 2 {
		t.Fatalf("attempt count = %d, want original + retry", len(attempts))
	}
	retry := successorOf(t, attempts, attemptID)
	if retry.AttemptNumber != 2 || !equalTrees(retry.InputTrees, recorded) {
		t.Fatalf("retry attempt = number %d InputTrees %v, want number 2 inheriting %v", retry.AttemptNumber, retry.InputTrees, recorded)
	}
	// The copy is independent of the predecessor's row.
	if original := uowAttempt(t, uow, attemptID); !equalTrees(original.InputTrees, recorded) {
		t.Fatalf("original attempt InputTrees = %v, want unchanged %v", original.InputTrees, recorded)
	}
}

// A retry of an attempt that recorded nothing records nothing (no I/O in the
// creating transaction, no invented trees).
func TestExecuteNodeHandler_RetryableFailure_NoRecordedTrees_RetryHasNone(t *testing.T) {
	attemptDoc := retryableAttemptPolicyDocument(3, 30, 600, errorcode.CodeProviderUnavailable)
	uow, ids, _, _, attemptID := scheduledExecutionFixtureWithAttemptPolicy(t, attemptDoc)

	job := claimableExecuteNodeJob(t, uow, attemptID)
	executor := &fake.NodeExecutor{Result: ports.NodeExecutionResult{State: runtimedomain.ExecutionAttemptFailed, ErrorCode: errorcode.CodeProviderUnavailable}}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.NewFixed(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)), fake.IsolationEnforcementChecker{}, sharedTestAgentRegistry(t), nil)
	if err := handler.Handle(context.Background(), job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	retry := successorOf(t, uow.Snapshot.Runtime().(*fake.RuntimeRepository).Attempts(), attemptID)
	if len(retry.InputTrees) != 0 {
		t.Fatalf("retry attempt InputTrees = %v, want none", retry.InputTrees)
	}
}

// Crash recovery on real SQLite: an attempt whose worker died after the
// InputTree was recorded gets a recovery attempt that uses the very same
// InputTree (the "crash between maker and checker" scenario of V9-01).
func TestRecoveryReaperHandler_OrphanedReadOnlyAttempt_RecoveryAttemptInheritsInputTrees(t *testing.T) {
	ctx := context.Background()
	uow, store, ids, runID, _, attemptID := sqliteExecutionFixture(t)

	recorded := inputTreesFixture()
	recordTrees(t, uow, attemptID, recorded)

	// A long lease expired explicitly: this test is about what recovery
	// copies, not about lease timing (see sqlite.ExpireJobLeaseForTest).
	claimedJob, _ := claimExecuteNodeJob(t, ctx, store, 30*time.Second)
	if err := sqlite.ExpireJobLeaseForTest(ctx, store, string(claimedJob.ID)); err != nil {
		t.Fatalf("expire the driving job lease: %v", err)
	}

	handler := runtime.NewRecoveryReaperHandler(uow, ids, clock.System{}, store, store, store)
	if err := runtime.StartupRecoveryScan(ctx, uow, ids); err != nil {
		t.Fatalf("StartupRecoveryScan: %v", err)
	}
	job := firstSQLiteJobOfKind(t, ctx, store, runtime.RecoveryReaperJobKind)
	if err := handler.Handle(ctx, job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	var attempts []runtimedomain.ExecutionAttempt
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		attempts, err = tx.Runtime().ListExecutionAttemptsForRun(ctx, runID)
		return err
	}); err != nil {
		t.Fatalf("ListExecutionAttemptsForRun: %v", err)
	}
	var original, recovery *runtimedomain.ExecutionAttempt
	for i := range attempts {
		switch attempts[i].AttemptNumber {
		case 1:
			original = &attempts[i]
		case 2:
			recovery = &attempts[i]
		}
	}
	if original == nil || recovery == nil {
		t.Fatalf("want attempts 1 and 2, got %+v", attempts)
	}
	if original.State != runtimedomain.ExecutionAttemptLost {
		t.Fatalf("original attempt state = %s, want LOST", original.State)
	}
	if !equalTrees(recovery.InputTrees, recorded) {
		t.Fatalf("recovery attempt InputTrees = %v, want the crashed attempt's recorded %v (persisted through real SQLite)", recovery.InputTrees, recorded)
	}
	if !equalTrees(original.InputTrees, recorded) {
		t.Fatalf("original attempt InputTrees = %v, want unchanged %v", original.InputTrees, recorded)
	}
}

// The same recovery path for an attempt that never recorded anything: the
// recovery attempt has none either, and will snapshot at execution time.
func TestRecoveryReaperHandler_OrphanedAttemptWithoutInputTrees_RecoveryAttemptHasNone(t *testing.T) {
	ctx := context.Background()
	uow, store, ids, runID, _, _ := sqliteExecutionFixture(t)

	claimedJob, _ := claimExecuteNodeJob(t, ctx, store, 30*time.Second)
	if err := sqlite.ExpireJobLeaseForTest(ctx, store, string(claimedJob.ID)); err != nil {
		t.Fatalf("expire the driving job lease: %v", err)
	}
	handler := runtime.NewRecoveryReaperHandler(uow, ids, clock.System{}, store, store, store)
	if err := runtime.StartupRecoveryScan(ctx, uow, ids); err != nil {
		t.Fatalf("StartupRecoveryScan: %v", err)
	}
	if err := handler.Handle(ctx, firstSQLiteJobOfKind(t, ctx, store, runtime.RecoveryReaperJobKind)); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	var attempts []runtimedomain.ExecutionAttempt
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		attempts, err = tx.Runtime().ListExecutionAttemptsForRun(ctx, runID)
		return err
	}); err != nil {
		t.Fatalf("ListExecutionAttemptsForRun: %v", err)
	}
	for _, attempt := range attempts {
		if len(attempt.InputTrees) != 0 {
			t.Fatalf("attempt %d InputTrees = %v, want none", attempt.AttemptNumber, attempt.InputTrees)
		}
	}
}
