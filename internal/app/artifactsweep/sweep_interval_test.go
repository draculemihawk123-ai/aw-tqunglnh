package artifactsweep_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/artifactsweep"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// runOneSweepAndClaimSuccessor drives exactly one ARTIFACT_SWEEP pass through a
// real handler and reports what a worker polling right afterwards would see.
func runOneSweepAndClaimSuccessor(t *testing.T, f *sweepFixture, options ...artifactsweep.HandlerOption) (successor ports.DurableJob, err error) {
	t.Helper()
	ctx := context.Background()
	handler := artifactsweep.NewHandler(f.uow, f.ids, clock.System{}, f.artStore, options...)
	if err := artifactsweep.StartupArtifactSweep(ctx, f.uow, f.ids); err != nil {
		t.Fatalf("StartupArtifactSweep: %v", err)
	}
	first, _, claimErr := f.store.ClaimJob(ctx, "test-worker", time.Minute)
	if claimErr != nil {
		t.Fatalf("claim first sweep job: %v", claimErr)
	}
	if first.Kind != artifactsweep.ArtifactSweepJobKind {
		t.Fatalf("first claimed job kind = %s, want %s", first.Kind, artifactsweep.ArtifactSweepJobKind)
	}
	if err := handler.Handle(ctx, first); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	successor, _, err = f.store.ClaimJob(ctx, "test-worker", time.Minute)
	return successor, err
}

// A self-rescheduling CONTROL job whose successor is claimable the instant it
// finishes is a hot loop: the first real `aw worker` ran ~200 sweeps a second
// while idle, appending one ARTIFACT_SWEEP_COMPLETED event each time. With an
// Interval, the successor must not be claimable until it has elapsed.
func TestHandler_WithInterval_SuccessorIsNotClaimableUntilTheIntervalElapses(t *testing.T) {
	f := newSweepFixture(t, "sweep-interval.db")
	successor, err := runOneSweepAndClaimSuccessor(t, f, artifactsweep.WithInterval(time.Hour))
	if !errors.Is(err, ports.ErrNoJobAvailable) {
		t.Fatalf("a worker polling right after a sweep with a 1h interval got job %+v (err=%v), want ErrNoJobAvailable", successor, err)
	}
}

// Documents the historical zero-interval behaviour that hand-driven tests rely
// on: no delay, so the successor is claimable at once. A production worker
// must never rely on it (see DefaultInterval).
func TestHandler_WithoutInterval_SuccessorIsClaimableImmediately(t *testing.T) {
	f := newSweepFixture(t, "sweep-no-interval.db")
	successor, err := runOneSweepAndClaimSuccessor(t, f)
	if err != nil {
		t.Fatalf("claim successor: %v", err)
	}
	if successor.Kind != artifactsweep.ArtifactSweepJobKind {
		t.Fatalf("successor kind = %s, want %s", successor.Kind, artifactsweep.ArtifactSweepJobKind)
	}
}

func TestDefaultInterval_IsNotAHotLoop(t *testing.T) {
	if artifactsweep.DefaultInterval < time.Minute {
		t.Fatalf("DefaultInterval = %s, want at least a minute", artifactsweep.DefaultInterval)
	}
}
