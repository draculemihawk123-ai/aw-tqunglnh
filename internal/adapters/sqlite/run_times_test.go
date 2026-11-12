package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// V12: CompletionPolicy's PASS moves a run VERIFYING -> SUCCEEDED; that transition must stamp finished_at like FAILED and
// CANCELLED do, or the UI times a long-finished run until now.
func TestTransitionWorkflowRunState_SucceededSetsFinishedAt(t *testing.T) {
	ctx := context.Background()
	store := openRunTimesStore(t, "agentkit-run-times-run.db")
	seedCancelRunTestRun(t, ctx, store, "run-1", "VERIFYING")
	tx, err := beginTestJobsTx(ctx, store)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	run, err := runtimeRepository{tx: tx}.TransitionWorkflowRunState(ctx, ports.TransitionWorkflowRunStateRequest{
		RunID: "run-1", ExpectedState: runtime.WorkflowRunVerifying, ExpectedVersion: 1, NextState: runtime.WorkflowRunSucceeded,
	})
	if err != nil {
		t.Fatalf("transition to SUCCEEDED: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if run.FinishedAt == nil {
		loaded, err := loadWorkflowRun(ctx, store.db, runtime.WorkflowRunID("run-1"))
		if err != nil {
			t.Fatalf("load run: %v", err)
		}
		run = loaded
	}
	if run.FinishedAt == nil {
		t.Fatal("a SUCCEEDED run has no finished_at")
	}
}

// V12: the run timeline reads attempt times through loadExecutionAttemptByID: finished_at once the attempt ended, created_at
// as the display start. StartedAt stays nil while started_at is NULL (nil means no executor was spawned).
func TestLoadExecutionAttempt_ReturnsStartAndFinishTimes(t *testing.T) {
	ctx := context.Background()
	store := openRunTimesStore(t, "agentkit-run-times-attempt.db")
	if err := SeedFixtureOwners(ctx, store, "project-1", "family-1", "work-item-1"); err != nil {
		t.Fatalf("seed owners: %v", err)
	}
	if err := SeedFixtureExecutionAttempt(ctx, store, "project-1", "family-1", "work-item-1", "attempt-1"); err != nil {
		t.Fatalf("seed attempt: %v", err)
	}
	load := func() runtime.ExecutionAttempt {
		t.Helper()
		tx, err := store.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback() }()
		attempt, err := loadExecutionAttemptByID(ctx, tx, runtime.ExecutionAttemptID("attempt-1"))
		if err != nil {
			t.Fatalf("load attempt: %v", err)
		}
		return attempt
	}
	running := load()
	if running.StartedAt != nil {
		t.Fatalf("StartedAt = %v, want nil while started_at is NULL", running.StartedAt)
	}
	if !running.CreatedAt.Equal(time.Date(2026, 8, 28, 16, 0, 0, 0, time.UTC)) {
		t.Fatalf("CreatedAt = %v, want the row's created_at", running.CreatedAt)
	}
	if running.FinishedAt != nil {
		t.Fatalf("a running attempt has FinishedAt %v", running.FinishedAt)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE execution_attempts SET finished_at = '2026-08-28T16:04:05Z' WHERE id = 'attempt-1'`); err != nil {
		t.Fatalf("finish attempt: %v", err)
	}
	finished := load()
	if finished.FinishedAt == nil || finished.FinishedAt.Sub(finished.CreatedAt) != 4*time.Minute+5*time.Second {
		t.Fatalf("FinishedAt = %v, want 4m05s after the start", finished.FinishedAt)
	}
}

func openRunTimesStore(t *testing.T, name string) *Store {
	t.Helper()
	store, err := Open(context.Background(), migratedDatabasePath(t, name))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
