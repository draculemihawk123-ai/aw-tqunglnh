package fake_test

import (
	"context"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
)

// TestFakeJobsRepository_EnqueueJob_EnforcesJobScopeLikeSQLite proves the
// fake backend rejects/accepts the exact same EnqueueJobRequest shapes the
// real sqlite adapter does (V4-13, confirmed with the user: "Fake
// repository enforce giống SQLite") — both call the identical
// ports.ValidateJobScope, so neither backend silently accepts what the
// other rejects.
func TestFakeJobsRepository_EnqueueJob_EnforcesJobScopeLikeSQLite(t *testing.T) {
	ctx := context.Background()

	t.Run("RECOVERY_REAPER with blank ProjectID and RunID is accepted", func(t *testing.T) {
		uow := fake.New()
		err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
			_, err := tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
				ID: "job-reaper", Kind: "RECOVERY_REAPER",
				AggregateType: "RecoveryReaper", AggregateID: "singleton",
				MaxClaims: 1, IdempotencyKey: "recovery-reaper:0",
			})
			return err
		})
		if err != nil {
			t.Fatalf("EnqueueJob(RECOVERY_REAPER, blank ProjectID/RunID) error = %v", err)
		}
	})

	t.Run("RECOVERY_REAPER with a ProjectID is rejected", func(t *testing.T) {
		uow := fake.New()
		err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
			_, err := tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
				ID: "job-reaper", ProjectID: "project-1", Kind: "RECOVERY_REAPER",
				AggregateType: "RecoveryReaper", AggregateID: "singleton",
				MaxClaims: 1, IdempotencyKey: "recovery-reaper:0",
			})
			return err
		})
		if err == nil {
			t.Fatal("EnqueueJob(RECOVERY_REAPER, non-blank ProjectID) = nil error, want rejection")
		}
	})

	t.Run("RECOVERY_REAPER with a RunID is rejected", func(t *testing.T) {
		uow := fake.New()
		err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
			_, err := tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
				ID: "job-reaper", Kind: "RECOVERY_REAPER", RunID: "run-1",
				AggregateType: "RecoveryReaper", AggregateID: "singleton",
				MaxClaims: 1, IdempotencyKey: "recovery-reaper:0",
			})
			return err
		})
		if err == nil {
			t.Fatal("EnqueueJob(RECOVERY_REAPER, non-blank RunID) = nil error, want rejection")
		}
	})

	t.Run("a non-RECOVERY_REAPER kind with blank ProjectID is rejected", func(t *testing.T) {
		uow := fake.New()
		err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
			_, err := tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
				ID: "job-no-project", Kind: "EXECUTE_NODE",
				AggregateType: "ExecutionAttempt", AggregateID: "attempt-1",
				MaxClaims: 1, IdempotencyKey: "idem-no-project",
			})
			return err
		})
		if err == nil {
			t.Fatal("EnqueueJob(EXECUTE_NODE, blank ProjectID) = nil error, want rejection")
		}
	})

	t.Run("a CONTROL kind other than RECOVERY_REAPER still requires a ProjectID", func(t *testing.T) {
		uow := fake.New()
		err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
			_, err := tx.Jobs().EnqueueJob(ctx, ports.EnqueueJobRequest{
				ID: "job-cancel-coordinator", Kind: "CANCEL_RUN_COORDINATOR",
				AggregateType: "WorkflowRun", AggregateID: "run-1",
				MaxClaims: 1, IdempotencyKey: "idem-cancel-coordinator",
			})
			return err
		})
		if err == nil {
			t.Fatal("EnqueueJob(CANCEL_RUN_COORDINATOR, blank ProjectID) = nil error, want rejection")
		}
	})
}
