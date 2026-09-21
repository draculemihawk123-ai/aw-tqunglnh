package runtime_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
)

// runOneReaperPassAndClaimSuccessor drives exactly one RECOVERY_REAPER pass
// through a real handler on an empty installation and reports what a worker
// polling right afterwards would see.
func runOneReaperPassAndClaimSuccessor(t *testing.T, options ...runtime.RecoveryReaperOption) (ports.DurableJob, error) {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "reaper-interval.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("reaper")

	handler := runtime.NewRecoveryReaperHandler(uow, ids, clock.System{}, store, store, store, options...)
	if err := runtime.StartupRecoveryScan(ctx, uow, ids); err != nil {
		t.Fatalf("StartupRecoveryScan: %v", err)
	}
	first, _, err := store.ClaimJob(ctx, "test-worker", time.Minute)
	if err != nil {
		t.Fatalf("claim first reaper job: %v", err)
	}
	if first.Kind != runtime.RecoveryReaperJobKind {
		t.Fatalf("first claimed job kind = %s, want %s", first.Kind, runtime.RecoveryReaperJobKind)
	}
	if err := handler.Handle(ctx, first); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	successor, _, err := store.ClaimJob(ctx, "test-worker", time.Minute)
	return successor, err
}

// A reaper pass whose successor is claimable the instant it finishes is a hot
// loop: the first real `aw worker` ran ~100 passes a second while idle.
func TestRecoveryReaperHandler_WithInterval_SuccessorIsNotClaimableUntilTheIntervalElapses(t *testing.T) {
	successor, err := runOneReaperPassAndClaimSuccessor(t, runtime.WithRecoveryReaperInterval(time.Hour))
	if !errors.Is(err, ports.ErrNoJobAvailable) {
		t.Fatalf("a worker polling right after a reaper pass with a 1h interval got job %+v (err=%v), want ErrNoJobAvailable", successor, err)
	}
}

// Documents the historical zero-interval behaviour hand-driven tests rely on.
func TestRecoveryReaperHandler_WithoutInterval_SuccessorIsClaimableImmediately(t *testing.T) {
	successor, err := runOneReaperPassAndClaimSuccessor(t)
	if err != nil {
		t.Fatalf("claim successor: %v", err)
	}
	if successor.Kind != runtime.RecoveryReaperJobKind {
		t.Fatalf("successor kind = %s, want %s", successor.Kind, runtime.RecoveryReaperJobKind)
	}
}

func TestDefaultRecoveryReaperInterval_IsNotAHotLoop(t *testing.T) {
	if runtime.DefaultRecoveryReaperInterval < time.Second {
		t.Fatalf("DefaultRecoveryReaperInterval = %s, want at least a second", runtime.DefaultRecoveryReaperInterval)
	}
}
