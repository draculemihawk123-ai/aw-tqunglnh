package cli_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
)

// instantSleeper is a cli.Sleeper that never sleeps real wall-clock time —
// it still respects ctx cancellation — so Wait's own poll-count logic can
// be tested in microseconds instead of real seconds.
func instantSleeper(ctx context.Context, _ time.Duration) error {
	return ctx.Err()
}

func TestWaitTableDriven(t *testing.T) {
	tests := []struct {
		name           string
		terminalAt     int // observe call count (1-based) that first reports terminal; 0 = never
		timeout        time.Duration
		useRealSleeper bool
		wantErr        error
		wantCalls      int
	}{
		{name: "already terminal on first observe", terminalAt: 1, wantCalls: 1},
		{name: "terminal after a few polls", terminalAt: 3, wantCalls: 3},
		{name: "never terminal times out", terminalAt: 0, timeout: 5 * time.Millisecond, useRealSleeper: true, wantErr: cli.ErrWaitTimeout},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var calls int32
			observe := func(ctx context.Context) (any, bool, error) {
				n := atomic.AddInt32(&calls, 1)
				terminal := tc.terminalAt > 0 && int(n) >= tc.terminalAt
				return int(n), terminal, nil
			}
			sleep := instantSleeper
			if tc.useRealSleeper {
				// The timeout case needs a real, tiny wall-clock elapse so
				// Wait's own deadline check actually fires — an instant
				// sleeper would spin forever never advancing time.Now().
				sleep = cli.DefaultSleeper
			}

			_, err := cli.Wait(context.Background(), observe, cli.WaitOptions{Interval: time.Millisecond, Timeout: tc.timeout, Sleep: sleep})

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Wait() error = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Wait() error = %v, want nil", err)
			}
			if int(atomic.LoadInt32(&calls)) != tc.wantCalls {
				t.Fatalf("observe called %d times, want %d", calls, tc.wantCalls)
			}
		})
	}
}

// TestWaitInterruptReturnsPromptlyWithoutFurtherObserve is the "interrupt"
// verify bullet: cancelling ctx (an operator's own Ctrl-C, wired by a
// future composition root) makes Wait return promptly with exactly the
// observe call already in flight — never a second poll, and critically,
// Wait itself never calls anything but observe, so an interrupted --wait
// can never execute or cancel the job it was only ever watching.
func TestWaitInterruptReturnsPromptlyWithoutFurtherObserve(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls int32
	started := make(chan struct{})
	unblockObserve := make(chan struct{})
	observe := func(ctx context.Context) (any, bool, error) {
		atomic.AddInt32(&calls, 1)
		close(started)
		<-unblockObserve // blocks until the test cancels ctx, mimicking a real in-flight poll interrupted mid-request
		return nil, false, ctx.Err()
	}

	done := make(chan error, 1)
	go func() {
		_, err := cli.Wait(ctx, observe, cli.WaitOptions{Interval: time.Millisecond, Sleep: instantSleeper})
		done <- err
	}()

	<-started // deterministically wait for the in-flight observe call before interrupting, so this test never races Wait's own ctx.Err() pre-check against goroutine scheduling
	cancel()
	close(unblockObserve)

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Wait() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait() did not return promptly after context cancellation")
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("observe called %d times after interrupt, want exactly 1 (Wait must never poll again after cancellation)", calls)
	}
}

func TestWaitReturnsImmediatelyWhenAlreadyTerminalNoSleepNeeded(t *testing.T) {
	sleepCalls := 0
	sleep := func(ctx context.Context, d time.Duration) error {
		sleepCalls++
		return nil
	}
	state, err := cli.Wait(context.Background(), func(ctx context.Context) (any, bool, error) {
		return "READY", true, nil
	}, cli.WaitOptions{Interval: time.Hour, Sleep: sleep})
	if err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if state != "READY" {
		t.Fatalf("Wait() state = %v, want READY", state)
	}
	if sleepCalls != 0 {
		t.Fatalf("Sleep called %d times for an already-terminal state, want 0", sleepCalls)
	}
}

func TestWaitPropagatesObserveError(t *testing.T) {
	wantErr := errors.New("boom")
	_, err := cli.Wait(context.Background(), func(ctx context.Context) (any, bool, error) {
		return nil, false, wantErr
	}, cli.WaitOptions{Interval: time.Millisecond, Sleep: instantSleeper})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Wait() error = %v, want %v", err, wantErr)
	}
}
