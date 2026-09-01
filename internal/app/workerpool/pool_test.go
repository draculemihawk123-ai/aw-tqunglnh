package workerpool_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
)

func openTestQueue(t *testing.T, name string) *sqlite.Store {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if err := sqlite.SeedFixtureOwners(ctx, store, "proj-1", "fam-1", "wi-1"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	return store
}

func enqueueJob(t *testing.T, store *sqlite.Store, id, kind string) {
	t.Helper()
	_, err := store.EnqueueJob(context.Background(), ports.EnqueueJobRequest{
		ID: ports.JobID(id), ProjectID: "proj-1", Kind: kind,
		AggregateType: "Test", AggregateID: id, MaxClaims: 5, IdempotencyKey: id + "-key",
	})
	if err != nil {
		t.Fatalf("EnqueueJob(%s): %v", id, err)
	}
}

func baseConfig(owner string) workerpool.Config {
	return workerpool.Config{
		Concurrency:      1,
		Owner:            owner,
		LeaseTTL:         500 * time.Millisecond,
		HeartbeatEvery:   100 * time.Millisecond,
		PollInterval:     20 * time.Millisecond,
		ShutdownGrace:    2 * time.Second,
		RecoveryInterval: 200 * time.Millisecond,
	}
}

func TestPool_ClaimsProcessesAndCompletesJob(t *testing.T) {
	store := openTestQueue(t, "agentkit-pool-basic.db")
	enqueueJob(t, store, "job-1", "greet")

	var handled atomic.Int32
	registry := workerpool.NewRegistry()
	registry.Register("greet", workerpool.HandlerFunc(func(_ context.Context, job ports.DurableJob) error {
		handled.Add(1)
		if job.ID != "job-1" {
			return fmt.Errorf("unexpected job id %q", job.ID)
		}
		return nil
	}))

	pool, err := workerpool.New(store, registry, baseConfig("w"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- pool.Run(ctx) }()

	waitForCondition(t, func() bool { return handled.Load() == 1 })
	cancel()
	if err := <-runErr; err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The job must now be SUCCEEDED, not still (or again) AVAILABLE.
	_, _, err = store.ClaimJob(context.Background(), "prober", time.Second)
	if !errors.Is(err, ports.ErrNoJobAvailable) {
		t.Fatalf("ClaimJob after completion err = %v, want ports.ErrNoJobAvailable", err)
	}
}

// TestPool_HandlerPanic_ContainedAndWorkerKeepsClaiming is V1-10's own
// "handler panic containment" Verify requirement: a Handler panicking on
// one job must not crash the pool or that worker's own goroutine — the
// same worker must go on to claim and process a later job.
func TestPool_HandlerPanic_ContainedAndWorkerKeepsClaiming(t *testing.T) {
	store := openTestQueue(t, "agentkit-pool-panic.db")
	enqueueJob(t, store, "job-panic", "flaky")
	enqueueJob(t, store, "job-ok", "flaky")

	var calls atomic.Int32
	var okHandled atomic.Bool
	registry := workerpool.NewRegistry()
	registry.Register("flaky", workerpool.HandlerFunc(func(_ context.Context, job ports.DurableJob) error {
		calls.Add(1)
		if job.ID == "job-panic" {
			panic("simulated handler panic")
		}
		okHandled.Store(true)
		return nil
	}))

	pool, err := workerpool.New(store, registry, baseConfig("w"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- pool.Run(ctx) }()

	waitForCondition(t, func() bool { return okHandled.Load() })
	cancel()
	if err := <-runErr; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls.Load() < 2 {
		t.Fatalf("handler was called %d times, want at least 2 (panic on job-panic, then job-ok)", calls.Load())
	}
}

// TestPool_HeartbeatKeepsLongRunningJobAlive proves the heartbeat loop
// actually extends a job's lease: a handler that runs well past LeaseTTL
// must still complete successfully exactly once, never get reclaimed and
// reprocessed mid-flight. An earlier version of this test also polled
// store.ClaimJob directly in its wait loop as a "prober" — that raced the
// pool's own worker for the SAME job (whichever call happened to run
// first could win it), which was flaky under load rather than a real
// signal of anything; waiting on the handler's own completion signal
// instead removes that self-inflicted race entirely.
func TestPool_HeartbeatKeepsLongRunningJobAlive(t *testing.T) {
	store := openTestQueue(t, "agentkit-pool-heartbeat.db")
	enqueueJob(t, store, "job-long", "slow")

	var invocations atomic.Int32
	var completed atomic.Bool
	registry := workerpool.NewRegistry()
	registry.Register("slow", workerpool.HandlerFunc(func(ctx context.Context, _ ports.DurableJob) error {
		invocations.Add(1)
		select {
		case <-time.After(1200 * time.Millisecond): // well past the 500ms LeaseTTL
			completed.Store(true)
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}))

	config := baseConfig("w")
	pool, err := workerpool.New(store, registry, config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- pool.Run(ctx) }()

	waitForCondition(t, func() bool { return completed.Load() })
	cancel()
	if err := <-runErr; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := invocations.Load(); got != 1 {
		t.Fatalf("handler invocation count = %d, want exactly 1 (heartbeat must prevent premature reclaim/reprocessing)", got)
	}
}

// TestPool_StartupScan_ReclaimsExpiredLeaseFromPreviousProcess is V1-10's
// own "lease hết hạn sau startup" Verify requirement: a job left LEASED
// by a process that crashed before this Pool ever started must still get
// picked up, via Run's own startup recovery scan.
func TestPool_StartupScan_ReclaimsExpiredLeaseFromPreviousProcess(t *testing.T) {
	store := openTestQueue(t, "agentkit-pool-startup-scan.db")
	enqueueJob(t, store, "job-orphaned", "cleanup")

	// Simulate a previous process claiming the job and then crashing
	// before it ever completed or heartbeated again.
	if _, _, err := store.ClaimJob(context.Background(), "crashed-worker", 100*time.Millisecond); err != nil {
		t.Fatalf("simulate prior claim: %v", err)
	}
	time.Sleep(150 * time.Millisecond) // let that lease expire

	var handled atomic.Bool
	registry := workerpool.NewRegistry()
	registry.Register("cleanup", workerpool.HandlerFunc(func(_ context.Context, job ports.DurableJob) error {
		if job.ID == "job-orphaned" {
			handled.Store(true)
		}
		return nil
	}))

	pool, err := workerpool.New(store, registry, baseConfig("w"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- pool.Run(ctx) }()

	waitForCondition(t, func() bool { return handled.Load() })
	cancel()
	if err := <-runErr; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// TestPool_TwoPoolsRaceRecovery_NoDuplicateProcessing is V1-10's own
// "hai worker tranh recovery, không tạo recovery job trùng" Verify
// requirement: two independent Pool instances (standing in for two
// processes) racing to reclaim and process the same orphaned job must
// process it exactly once between them, never twice.
func TestPool_TwoPoolsRaceRecovery_NoDuplicateProcessing(t *testing.T) {
	store := openTestQueue(t, "agentkit-pool-race-recovery.db")
	enqueueJob(t, store, "job-contested", "contested")

	if _, _, err := store.ClaimJob(context.Background(), "crashed-worker", 100*time.Millisecond); err != nil {
		t.Fatalf("simulate prior claim: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	var processed atomic.Int32
	registry := workerpool.NewRegistry()
	registry.Register("contested", workerpool.HandlerFunc(func(_ context.Context, _ ports.DurableJob) error {
		processed.Add(1)
		return nil
	}))

	poolA, err := workerpool.New(store, registry, baseConfig("pool-a"))
	if err != nil {
		t.Fatalf("New (A): %v", err)
	}
	poolB, err := workerpool.New(store, registry, baseConfig("pool-b"))
	if err != nil {
		t.Fatalf("New (B): %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	errA := make(chan error, 1)
	errB := make(chan error, 1)
	go func() { errA <- poolA.Run(ctx) }()
	go func() { errB <- poolB.Run(ctx) }()

	waitForCondition(t, func() bool { return processed.Load() >= 1 })
	time.Sleep(150 * time.Millisecond) // give a would-be duplicate a real chance to also fire
	cancel()
	if err := <-errA; err != nil {
		t.Fatalf("Run (A): %v", err)
	}
	if err := <-errB; err != nil {
		t.Fatalf("Run (B): %v", err)
	}
	if got := processed.Load(); got != 1 {
		t.Fatalf("processed count = %d, want exactly 1 (two pools racing must never both process the same job)", got)
	}
}

// TestPool_CancelDuringShutdown_WaitsForInFlightThenReturnsNil is V1-10's
// own "cancel shutdown" Verify requirement (the clean path): an in-flight
// job that finishes well within ShutdownGrace lets Run return nil.
func TestPool_CancelDuringShutdown_WaitsForInFlightThenReturnsNil(t *testing.T) {
	store := openTestQueue(t, "agentkit-pool-shutdown-clean.db")
	enqueueJob(t, store, "job-1", "slow-ok")

	var completed atomic.Bool
	registry := workerpool.NewRegistry()
	registry.Register("slow-ok", workerpool.HandlerFunc(func(_ context.Context, _ ports.DurableJob) error {
		time.Sleep(150 * time.Millisecond)
		completed.Store(true)
		return nil
	}))

	config := baseConfig("w")
	config.ShutdownGrace = 2 * time.Second // comfortably more than the 150ms handler
	pool, err := workerpool.New(store, registry, config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- pool.Run(ctx) }()

	time.Sleep(30 * time.Millisecond) // let the job actually get claimed and start running
	cancel()

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run = %v, want nil (in-flight job should finish within grace)", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return in time")
	}
	if !completed.Load() {
		t.Fatal("the in-flight handler never completed before Run returned")
	}
}

// TestPool_ShutdownGraceExceeded_ReturnsErrAndEscalatesCancellation is
// V1-10's own "cancel shutdown" Verify requirement (the forceful path):
// a handler that outlives ShutdownGrace makes Run return
// ErrShutdownGraceExceeded promptly rather than block forever, and its
// escalated cancellation reaches the handler's own ctx.
func TestPool_ShutdownGraceExceeded_ReturnsErrAndEscalatesCancellation(t *testing.T) {
	store := openTestQueue(t, "agentkit-pool-shutdown-forced.db")
	enqueueJob(t, store, "job-1", "too-slow")

	var sawEscalatedCancel atomic.Bool
	registry := workerpool.NewRegistry()
	registry.Register("too-slow", workerpool.HandlerFunc(func(ctx context.Context, _ ports.DurableJob) error {
		select {
		case <-time.After(5 * time.Second):
			return nil
		case <-ctx.Done():
			sawEscalatedCancel.Store(true)
			return ctx.Err()
		}
	}))

	config := baseConfig("w")
	config.ShutdownGrace = 150 * time.Millisecond
	pool, err := workerpool.New(store, registry, config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- pool.Run(ctx) }()

	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case err := <-runErr:
		if !errors.Is(err, workerpool.ErrShutdownGraceExceeded) {
			t.Fatalf("Run = %v, want ErrShutdownGraceExceeded", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return promptly after the grace period elapsed")
	}
	if !sawEscalatedCancel.Load() {
		t.Fatal("the handler's own ctx was never cancelled after the grace period elapsed")
	}
}

func waitForCondition(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was never met in time")
}
