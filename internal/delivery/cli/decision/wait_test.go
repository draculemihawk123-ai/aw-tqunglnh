package decision_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli/decision"
)

func TestSignalWait_HappyPath_ConsumesAndRoutes(t *testing.T) {
	_, uow := openDecisionCLITestStore(t, "wait-happy.db")
	deps := newTestDeps(uow)
	runID, hop := waitFixture(t, uow, deps.IDs, 0)

	var stdout, stderr bytes.Buffer
	args := []string{"--signal-key", "delivery-1", "--payload", `{"status":"green"}`, runID, hop.NextWaitRegistrationID}
	if err := decision.SignalWait(context.Background(), deps, args, &stdout, &stderr); err != nil {
		t.Fatalf("SignalWait() error = %v, stderr = %s", err, stderr.String())
	}

	var envelope struct {
		IdempotencyKey string                   `json:"idempotencyKey"`
		Result         runtime.SignalWaitResult `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode stdout %s: %v", stdout.String(), err)
	}
	if envelope.IdempotencyKey == "" {
		t.Fatal("no --idempotency-key given, so SignalWait must have generated and returned one")
	}
	if !envelope.Result.Won || !envelope.Result.Advanced || envelope.Result.NextNodeKey != "end_resumed" {
		t.Fatalf("result = %+v, want Won=true Advanced=true NextNodeKey=end_resumed", envelope.Result)
	}
}

func TestSignalWait_MissingSignalKey_IsUsageError(t *testing.T) {
	_, uow := openDecisionCLITestStore(t, "wait-nokey.db")
	deps := newTestDeps(uow)
	err := decision.SignalWait(context.Background(), deps, []string{"run-1", "reg-1"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !cli.IsUsageError(err) {
		t.Fatalf("SignalWait(no --signal-key) error = %v, want a cli.UsageError", err)
	}
}

func TestSignalWait_InvalidPayloadJSON_IsUsageError(t *testing.T) {
	_, uow := openDecisionCLITestStore(t, "wait-badpayload.db")
	deps := newTestDeps(uow)
	err := decision.SignalWait(context.Background(), deps, []string{"--signal-key", "k", "--payload", "{not json", "run-1", "reg-1"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !cli.IsUsageError(err) {
		t.Fatalf("SignalWait(bad --payload) error = %v, want a cli.UsageError", err)
	}
}

// TestSignalWait_Concurrent_SameSignalKey_ExactlyOneWinner races real
// concurrent `wait signal` invocations (each its own goroutine, its own
// --idempotency-key) reporting the SAME real external event (same
// --signal-key, same --payload) against one real sqlite.Store — this
// task's own "concurrent decision/signal" Verify bullet, mirroring
// internal/app/runtime/wait_sqlite_test.go's own
// TestSignalWait_SQLite_ConcurrentSameSignalKey_ExactlyOneWinner. Exactly
// one wait_signals row may ever exist for (WaitRegistrationID, SignalKey),
// and exactly one caller may ever observe Won=true.
func TestSignalWait_Concurrent_SameSignalKey_ExactlyOneWinner(t *testing.T) {
	_, uow := openDecisionCLITestStore(t, "wait-race.db")
	deps := newTestDeps(uow)
	runID, hop := waitFixture(t, uow, deps.IDs, 600)

	const attempts = 5
	var wg sync.WaitGroup
	stdouts := make([]bytes.Buffer, attempts)
	errs := make([]error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			args := []string{
				"--signal-key", "delivery-shared", "--payload", `{"status":"green"}`,
				"--idempotency-key", fmt.Sprintf("idem-signal-%d", i),
				runID, hop.NextWaitRegistrationID,
			}
			errs[i] = decision.SignalWait(context.Background(), deps, args, &stdouts[i], &bytes.Buffer{})
		}(i)
	}
	wg.Wait()

	winners := 0
	for i, err := range errs {
		if err != nil {
			t.Fatalf("attempt %d unexpected error = %v", i, err)
		}
		var envelope struct {
			Result runtime.SignalWaitResult `json:"result"`
		}
		if err := json.Unmarshal(stdouts[i].Bytes(), &envelope); err != nil {
			t.Fatalf("attempt %d decode stdout %s: %v", i, stdouts[i].String(), err)
		}
		if envelope.Result.Won {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d, want exactly 1", winners)
	}
}
