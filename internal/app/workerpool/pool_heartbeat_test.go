package workerpool_test

// V9-14a: a heartbeat that fails is not a lost lease.
//
// The heartbeat loop used to stop for good on the FIRST error from HeartbeatJob,
// whatever the error. A heartbeat is a SQLite commit, and a busy database
// ("database is locked (SQLITE_BUSY)") fails one commit without taking anything
// away from the job: the lease is still valid until its own lease_until. With the
// loop gone, the lease simply ran out one TTL later, the recovery reaper
// reclaimed a job whose handler was still running, and a long (and expensive —
// an AI attempt can run for many minutes) piece of work ran twice.
//
// Only ports.ErrJobLeaseLost says the lease is no longer ours. Any other error is
// retried on the next tick until the lease would have expired anyway.

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
)

// heartbeatQueue wraps a real queue and decides what HeartbeatJob returns; every
// other method is the real one.
type heartbeatQueue struct {
	ports.JobQueue
	calls atomic.Int32
	start time.Time
	mu    sync.Mutex
	at    []time.Duration // when each HeartbeatJob call happened, from start
	// decide is given the 1-based call number and returns the error to inject, or
	// nil to let the real heartbeat run.
	decide func(call int) error
}

func (q *heartbeatQueue) HeartbeatJob(ctx context.Context, lease ports.JobLease, ttl time.Duration) (ports.JobLease, error) {
	call := int(q.calls.Add(1))
	q.mu.Lock()
	q.at = append(q.at, time.Since(q.start))
	q.mu.Unlock()
	if err := q.decide(call); err != nil {
		return ports.JobLease{}, err
	}
	return q.JobQueue.HeartbeatJob(ctx, lease, ttl)
}

func runOneJob(t *testing.T, queue ports.JobQueue, config workerpool.Config, handlerRuns time.Duration, invocations *atomic.Int32) error {
	t.Helper()
	var finished atomic.Bool
	registry := workerpool.NewRegistry()
	registry.Register("slow", workerpool.HandlerFunc(func(ctx context.Context, _ ports.DurableJob) error {
		invocations.Add(1)
		select {
		case <-time.After(handlerRuns):
			finished.Store(true)
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}))
	pool, err := workerpool.New(queue, registry, config)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- pool.Run(ctx) }()
	waitForConditionWithin(t, 20*time.Second, func() bool { return finished.Load() })
	cancel()
	// Returned, not asserted here: a job that was wrongly reclaimed leaves a
	// second handler running at shutdown, and the tests must reach their own
	// invocation-count assertion (which names the real defect) before this.
	return <-runErr
}

func TestPool_TransientHeartbeatErrorsDoNotEndTheHeartbeat(t *testing.T) {
	store := openTestQueue(t, "agentkit-pool-heartbeat-transient.db")
	enqueueJob(t, store, "job-long", "slow")
	queue := &heartbeatQueue{JobQueue: store, decide: func(call int) error {
		if call <= 3 { // three busy commits in a row, well inside one lease
			return errors.New("heartbeat durable job: database is locked (5) (SQLITE_BUSY)")
		}
		return nil
	}}

	config := baseConfig("w")
	config.LeaseTTL = 2 * time.Second // 20 ticks per lease: three bad ones leave plenty of margin
	var invocations atomic.Int32
	runErr := runOneJob(t, queue, config, 4*time.Second, &invocations) // twice the lease: only a surviving heartbeat keeps the job

	if got := invocations.Load(); got != 1 {
		t.Fatalf("handler invocation count = %d, want exactly 1: a transient heartbeat error ended the heartbeat, the lease lapsed and the job was reclaimed while still running", got)
	}
	if got := queue.calls.Load(); got < 5 {
		t.Fatalf("HeartbeatJob called %d times, want it to keep going after the three failures", got)
	}
	if runErr != nil {
		t.Fatalf("Run: %v", runErr)
	}
}

func TestPool_LostLeaseEndsTheHeartbeatAtOnce(t *testing.T) {
	store := openTestQueue(t, "agentkit-pool-heartbeat-lost.db")
	enqueueJob(t, store, "job-long", "slow")
	queue := &heartbeatQueue{JobQueue: store, decide: func(int) error { return ports.ErrJobLeaseLost }}

	config := baseConfig("w")
	config.LeaseTTL = 2 * time.Second
	var invocations atomic.Int32
	if err := runOneJob(t, queue, config, 1*time.Second, &invocations); err != nil { // ~10 ticks if it did not stop
		t.Fatalf("Run: %v", err)
	}

	if got := queue.calls.Load(); got != 1 {
		t.Fatalf("HeartbeatJob called %d times after ErrJobLeaseLost, want exactly 1: a lost lease must stop the loop, not be retried", got)
	}
}

func TestPool_HeartbeatRetriesStopOnceTheLeaseHasExpired(t *testing.T) {
	store := openTestQueue(t, "agentkit-pool-heartbeat-bounded.db")
	enqueueJob(t, store, "job-long", "slow")
	queue := &heartbeatQueue{JobQueue: store, start: time.Now(), decide: func(int) error { return errors.New("database is locked (5) (SQLITE_BUSY)") }}

	config := baseConfig("w")
	config.LeaseTTL = 1 * time.Second // expires ~1s after the claim; 10 ticks of 100ms
	var invocations atomic.Int32
	// The handler runs 3s. Every renewal fails, so the lease really does lapse at
	// ~1s, the job is reclaimed and — once the first handler is done — claimed a
	// second time, whose own heartbeat starts around 3s. A second handler may
	// still be running at shutdown: the expected outcome of THIS scenario, not
	// what the test judges.
	if err := runOneJob(t, queue, config, 3*time.Second, &invocations); err != nil && !errors.Is(err, workerpool.ErrShutdownGraceExceeded) {
		t.Fatalf("Run: %v", err)
	}

	// While the first handler runs, the first claim's loop must stop retrying once
	// its lease has expired (~1s): a renewal after that revives nothing. Nothing
	// may call HeartbeatJob between expiry (plus a tick of slack) and the end of
	// that first handler (minus slack for the second claim to be picked up).
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if len(queue.at) == 0 {
		t.Fatal("HeartbeatJob was never called")
	}
	for _, at := range queue.at {
		if at > 1300*time.Millisecond && at < 2900*time.Millisecond {
			t.Fatalf("HeartbeatJob called at %v with the first lease (1s) long expired and its handler still running until ~3s; calls at %v: the loop must stop retrying once the lease has expired", at, queue.at)
		}
	}
}
