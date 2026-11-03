package workerpool_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
)

// V12-01: a handler error used to be dropped silently; the pool now reports it (with the job and
// its claim count) before leaving the job for its lease to expire.
func TestPool_ReportsHandlerErrorsAndMissingHandlers(t *testing.T) {
	store := openTestQueue(t, "agentkit-pool-handler-error.db")
	enqueueJob(t, store, "job-fail", "fails")
	enqueueJob(t, store, "job-orphan", "nobody-handles-this")

	registry := workerpool.NewRegistry()
	registry.Register("fails", workerpool.HandlerFunc(func(context.Context, ports.DurableJob) error {
		return errors.New("simulated handler failure")
	}))

	var mu sync.Mutex
	reported := map[ports.JobID]string{}
	config := baseConfig("w")
	config.OnHandlerError = func(job ports.DurableJob, err error) {
		mu.Lock()
		defer mu.Unlock()
		if job.ClaimCount == 0 {
			t.Errorf("job %s reported with claim count 0", job.ID)
		}
		reported[job.ID] = err.Error()
	}
	pool, err := workerpool.New(store, registry, config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- pool.Run(ctx) }()
	waitForCondition(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(reported) == 2
	})
	cancel()
	<-runErr

	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(reported["job-fail"], "simulated handler failure") {
		t.Fatalf("job-fail reported %q", reported["job-fail"])
	}
	if !strings.Contains(reported["job-orphan"], "no handler registered") {
		t.Fatalf("job-orphan reported %q", reported["job-orphan"])
	}
}
