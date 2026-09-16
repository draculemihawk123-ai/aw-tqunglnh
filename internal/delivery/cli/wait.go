package cli

import (
	"context"
	"errors"
	"time"
)

// ObserveFunc is a single, read-only poll of current state — Wait's own
// caller-supplied contract for V6-15B's own "`--wait` only observes and
// never executes/cancels job" line: an ObserveFunc must never itself
// start, retry, or cancel anything, only report what is already true.
// terminal reports whether state represents a final outcome Wait should
// stop polling for.
type ObserveFunc func(ctx context.Context) (state any, terminal bool, err error)

// Sleeper waits for d (or returns immediately for d <= 0) or ctx
// cancellation, whichever comes first, returning ctx.Err() in the latter
// case — Wait's own injectable time source, the seam that keeps its
// poll/timeout/interrupt behavior table-driven-testable without a real
// wall-clock sleep in every test case (there is no existing precedent for
// this kind of seam elsewhere in the codebase; this is this task's own
// design decision for exactly that reason).
type Sleeper func(ctx context.Context, d time.Duration) error

// DefaultSleeper is the real, wall-clock Sleeper production code uses.
func DefaultSleeper(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// WaitOptions configures Wait. Interval must be > 0 (defaults to 1s when
// not set). Timeout of 0 means "no timeout" — poll forever until ctx is
// cancelled or observe reports a terminal state. Sleep defaults to
// DefaultSleeper when nil.
type WaitOptions struct {
	Interval time.Duration
	Timeout  time.Duration
	Sleep    Sleeper
}

// ErrWaitTimeout is returned when Options.Timeout elapses before observe
// ever reports a terminal state.
var ErrWaitTimeout = errors.New("cli: --wait timed out before a terminal state was observed")

// Wait polls observe at Interval until it reports a terminal state, ctx
// is cancelled (its own error, e.g. context.Canceled for an operator's own
// Ctrl-C, is returned immediately — the interrupt path: Wait returns
// promptly with no further observe call, and critically calls nothing but
// observe throughout its whole lifetime, so an interrupted `--wait` can
// never execute or cancel the job it was only ever watching), or
// Options.Timeout elapses first (ErrWaitTimeout). It always calls observe
// at least once before its first sleep, so a job that is already terminal
// when `--wait` starts returns immediately with zero delay.
func Wait(ctx context.Context, observe ObserveFunc, opts WaitOptions) (any, error) {
	interval := opts.Interval
	if interval <= 0 {
		interval = time.Second
	}
	sleep := opts.Sleep
	if sleep == nil {
		sleep = DefaultSleeper
	}

	var deadline time.Time
	hasDeadline := opts.Timeout > 0
	if hasDeadline {
		deadline = time.Now().Add(opts.Timeout)
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		state, terminal, err := observe(ctx)
		if err != nil {
			return nil, err
		}
		if terminal {
			return state, nil
		}
		if hasDeadline && !time.Now().Before(deadline) {
			return state, ErrWaitTimeout
		}

		remaining := interval
		if hasDeadline {
			if untilDeadline := time.Until(deadline); untilDeadline < remaining {
				remaining = untilDeadline
			}
		}
		if err := sleep(ctx, remaining); err != nil {
			return nil, err
		}
	}
}
