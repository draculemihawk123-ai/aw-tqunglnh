// Package workerpool is the embedded worker lifecycle V1-10 requires
// (docs/design/03-v1-alpha-foundation.md): "serve" and "worker" are
// meant to share this exact same claim/heartbeat/shutdown protocol, so
// this package owns none of the CLI wiring — only the pool mechanics a
// later task composes into either command.
package workerpool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

// ErrShutdownGraceExceeded is returned by Run when at least one in-flight
// job was still running once ShutdownGrace elapsed after the caller's ctx
// was cancelled. Run still returns (it never blocks past the grace
// period), but the caller learns its shutdown was forceful, not clean.
var ErrShutdownGraceExceeded = errors.New("workerpool: shutdown grace period exceeded with jobs still in flight")

// Config controls Pool's behavior. Validate fills in sane defaults for
// any zero-value field it can, and rejects a combination that could
// never behave correctly (a HeartbeatEvery that can't outrun LeaseTTL,
// for instance).
type Config struct {
	// Concurrency bounds how many jobs Pool ever runs at once — one
	// worker goroutine per unit, each processing at most one job at a
	// time, so N goroutines is already the bound with no separate
	// semaphore needed.
	Concurrency int
	// Owner identifies this pool's claims (JobLease.Owner) — must be
	// unique per running process, e.g. hostname+pid.
	Owner string
	// LeaseTTL is how long a claimed job's lease is valid without a
	// heartbeat.
	LeaseTTL time.Duration
	// HeartbeatEvery is how often an in-flight job's lease is renewed.
	// Must be well under LeaseTTL so a heartbeat always lands before the
	// previous one expires.
	HeartbeatEvery time.Duration
	// PollInterval is how long an idle worker waits before checking for
	// a new job again after finding none available.
	PollInterval time.Duration
	// ShutdownGrace bounds how long Run waits for in-flight jobs to
	// finish after ctx is cancelled before giving up and returning
	// ErrShutdownGraceExceeded.
	ShutdownGrace time.Duration
	// RecoveryInterval is how often the periodic recovery reaper calls
	// RecoverExpiredJobs, independent of the startup scan Run always
	// does once immediately.
	RecoveryInterval time.Duration
}

// Validate fills in sane defaults for any zero-value field it can, and
// rejects a combination that could never behave correctly (e.g. a
// HeartbeatEvery that can't outrun LeaseTTL). New calls this internally;
// it is also exported so a caller — V1-11's doctor package, for one —
// can check a Config's lease/reaper settings are sane without needing a
// real ports.JobQueue/Registry to construct a Pool just to find out.
func (c Config) Validate() (Config, error) {
	if c.Concurrency <= 0 {
		c.Concurrency = 1
	}
	if c.Owner == "" {
		return c, errors.New("workerpool: Config.Owner is required")
	}
	if c.LeaseTTL <= 0 {
		return c, errors.New("workerpool: Config.LeaseTTL must be positive")
	}
	if c.HeartbeatEvery <= 0 {
		c.HeartbeatEvery = c.LeaseTTL / 3
	}
	if c.HeartbeatEvery >= c.LeaseTTL {
		return c, fmt.Errorf("workerpool: Config.HeartbeatEvery (%s) must be less than LeaseTTL (%s)", c.HeartbeatEvery, c.LeaseTTL)
	}
	if c.PollInterval <= 0 {
		c.PollInterval = 200 * time.Millisecond
	}
	if c.ShutdownGrace < 0 {
		return c, errors.New("workerpool: Config.ShutdownGrace must not be negative")
	}
	if c.RecoveryInterval <= 0 {
		c.RecoveryInterval = c.LeaseTTL
	}
	return c, nil
}

// Pool claims and dispatches durable jobs to registered handlers, one
// worker goroutine per unit of Concurrency: each loops
// claim -> heartbeat-while-running -> complete, with a Handler panic
// contained so one bad handler can never take down the pool's control
// loop or another worker's loop.
//
// A job whose Handler returns an error, or for whose Kind no Handler is
// registered, is never explicitly failed — ports.JobQueue has no such
// method; V0's existing durable_jobs model only ever moves a stuck job
// forward via lease expiry (RecoverExpiredJobs decides AVAILABLE-for-retry
// vs. DEAD once ClaimCount reaches MaxClaims). Pool follows that same
// path rather than inventing a second one: it simply does not call
// CompleteJob, and lets the lease expire naturally.
type Pool struct {
	queue    ports.JobQueue
	registry *Registry
	config   Config
}

// New returns a Pool claiming jobs from queue and dispatching them
// through registry, or an error if config is invalid.
func New(queue ports.JobQueue, registry *Registry, config Config) (*Pool, error) {
	if queue == nil {
		return nil, errors.New("workerpool: queue is required")
	}
	if registry == nil {
		return nil, errors.New("workerpool: registry is required")
	}
	validated, err := config.Validate()
	if err != nil {
		return nil, err
	}
	return &Pool{queue: queue, registry: registry, config: validated}, nil
}

// Run performs one startup recovery scan, then starts Concurrency worker
// goroutines and a periodic recovery reaper, and blocks until ctx is
// cancelled. Once cancelled, workers stop claiming new jobs immediately
// (graceful stop); Run then waits up to ShutdownGrace for every
// already-claimed job to finish naturally before returning. Only if that
// grace period elapses with jobs still in flight does Run escalate to
// cancelling their handlers' own ctx — an in-flight handler is otherwise
// never cancelled just because ctx was.
//
// This is why the pool tracks two independent signals, not one: ctx
// itself only ever means "stop claiming new work" (runWorker's claim
// loop and ClaimJob/sleepOrDone below use ctx directly, so it stops
// instantly); jobsCtx is a separate root Run owns, threaded down as the
// ctx a Handler and its heartbeat loop actually see, cancelled only by
// Run's own escalation decision. Deriving jobsCtx from ctx instead would
// make it inherit ctx's cancellation immediately — indistinguishable
// from having no grace period at all.
func (p *Pool) Run(ctx context.Context) error {
	if _, err := p.queue.RecoverExpiredJobs(ctx); err != nil {
		return fmt.Errorf("workerpool: startup recovery scan: %w", err)
	}

	jobsCtx, cancelJobs := context.WithCancel(context.Background())
	defer cancelJobs()

	var workers sync.WaitGroup
	for i := 0; i < p.config.Concurrency; i++ {
		workers.Add(1)
		go func(workerIndex int) {
			defer workers.Done()
			p.runWorker(ctx, jobsCtx, workerIndex)
		}(i)
	}

	reaperStopped := make(chan struct{})
	go func() {
		defer close(reaperStopped)
		p.runRecoveryReaper(ctx)
	}()

	<-ctx.Done()
	<-reaperStopped // the reaper already selects on ctx.Done(), so this returns promptly

	workersFinished := make(chan struct{})
	go func() {
		workers.Wait()
		close(workersFinished)
	}()

	select {
	case <-workersFinished:
		return nil
	case <-time.After(p.config.ShutdownGrace):
		cancelJobs() // escalate: ask every still-running handler's ctx to stop
		<-workersFinished
		return ErrShutdownGraceExceeded
	}
}

func (p *Pool) runWorker(runCtx, jobsCtx context.Context, workerIndex int) {
	owner := fmt.Sprintf("%s-%d", p.config.Owner, workerIndex)
	for {
		if runCtx.Err() != nil {
			return
		}
		job, lease, err := p.queue.ClaimJob(runCtx, owner, p.config.LeaseTTL)
		if err != nil {
			// ports.ErrNoJobAvailable ("nothing to claim right now") and
			// any other transient claim error (e.g. lock contention) get
			// the same treatment: back off and retry rather than exit
			// the worker's loop — a real, permanent failure would keep
			// recurring and simply keep this worker idling, never crash
			// it.
			if !p.sleepOrDone(runCtx, p.config.PollInterval) {
				return
			}
			continue
		}
		p.runJob(jobsCtx, job, lease)
	}
}

func (p *Pool) runJob(jobsCtx context.Context, job ports.DurableJob, lease ports.JobLease) {
	jobCtx, cancelJob := context.WithCancel(jobsCtx)
	defer cancelJob()

	box := &leaseBox{lease: lease}
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		p.heartbeatLoop(jobCtx, box)
	}()

	handler, ok := p.registry.Lookup(job.Kind)
	var handleErr error
	if !ok {
		handleErr = fmt.Errorf("workerpool: no handler registered for kind %q", job.Kind)
	} else {
		handleErr = p.invokeContained(jobCtx, handler, job)
	}

	cancelJob()
	<-heartbeatDone

	if handleErr != nil {
		return // leave it un-completed; lease expiry + recovery reaper decide retry vs DEAD
	}
	// Best-effort: if the lease was lost between the last successful
	// heartbeat and here, CompleteJob returns ports.ErrJobLeaseLost and
	// there is nothing more this worker can do — another owner already
	// (or will) reclaim the job.
	_ = p.queue.CompleteJob(context.WithoutCancel(jobsCtx), box.get())
}

// invokeContained calls handler.Handle, recovering a panic into an error
// so one broken Handler can never crash this worker's goroutine (V1-10's
// own "handler panic containment").
func (p *Pool) invokeContained(ctx context.Context, handler Handler, job ports.DurableJob) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("workerpool: handler for kind %q panicked: %v", job.Kind, r)
		}
	}()
	return handler.Handle(ctx, job)
}

func (p *Pool) heartbeatLoop(ctx context.Context, box *leaseBox) {
	ticker := time.NewTicker(p.config.HeartbeatEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			updated, err := p.queue.HeartbeatJob(context.WithoutCancel(ctx), box.get(), p.config.LeaseTTL)
			if err != nil {
				return // lease lost; nothing more this loop can do
			}
			box.set(updated)
		}
	}
}

func (p *Pool) runRecoveryReaper(ctx context.Context) {
	ticker := time.NewTicker(p.config.RecoveryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = p.queue.RecoverExpiredJobs(ctx) // best-effort; a transient error just waits for the next tick
		}
	}
}

// sleepOrDone waits for d, returning false early (without waiting out the
// full duration) if ctx is cancelled first.
func (p *Pool) sleepOrDone(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// leaseBox holds the one JobLease a running job's heartbeat loop keeps
// current and its completion path reads back, guarded against the data
// race those two different goroutines would otherwise have over the same
// lease value.
type leaseBox struct {
	mu    sync.Mutex
	lease ports.JobLease
}

func (b *leaseBox) get() ports.JobLease {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lease
}

func (b *leaseBox) set(lease ports.JobLease) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lease = lease
}
