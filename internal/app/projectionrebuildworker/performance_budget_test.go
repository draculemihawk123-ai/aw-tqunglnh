package projectionrebuildworker_test

// V8-07 (docs/design/10-v8-alpha-hardening.md): "đo ... projection rebuild
// ... ghi hardware/profile rồi freeze numeric threshold ... Hoàn thành
// khi: không có unbounded query/render/memory path".
//
// This file measures a REAL end-to-end rebuild (REQUESTED -> SNAPSHOTTING
// -> BUILDING -> CUTTING_OVER -> SUCCEEDED, driven by the one real
// ExecuteProjectionRebuild call this package's own execute_sqlite_test.go
// already proves reaches SUCCEEDED for a small event count) against two
// much larger event-journal sizes, to confirm the real cost scales
// proportionally with how many events a rebuild has to replay rather than
// blowing up worse than linear.
//
// Bootstrap path deliberately used (no live consumer ever applied a batch
// first, exactly like TestExecuteProjectionRebuild_BootstrapNoActiveGenerationYet
// already establishes elsewhere in this package): W0=0, so BUILDING
// replays the WHOLE seeded event journal — the real worst case a rebuild
// can face for a given event count, and the simplest way to make "how many
// events get replayed" and "how many events exist" the same number.
import (
	"context"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/projectionrebuildworker"
)

// appendManyEvents appends count real domain events, all in ONE
// transaction (unlike appendEvent's own one-transaction-per-call
// convention) purely so seeding thousands of events stays fast enough for
// a latency benchmark to run in CI. Every event names the SAME WorkItem
// (rootWorkItemCreatedPayload's own fixed "work-item-1") deliberately: the
// property under test is replay/scan cost scaling with EVENT COUNT, not
// final row count, and reusing one payload avoids needing count distinct,
// individually-fixture-seeded WorkItems just to generate load.
func (fx *workerFixture) appendManyEvents(t *testing.T, count int) {
	t.Helper()
	err := fx.uow.WithSerializedWrite(fx.ctx, func(tx ports.Tx) error {
		for i := 0; i < count; i++ {
			if err := tx.Events().Append(fx.ctx, ports.DomainEvent{
				ID: fx.ids.NewID(), ProjectID: "project-1", AggregateType: "WorkItem",
				AggregateID: fx.ids.NewID(), Sequence: 1, EventType: "RootWorkItemCreated", SchemaVersion: 1,
				PayloadJSON: rootWorkItemCreatedPayload, CorrelationID: fx.ids.NewID(), CreatedAt: time.Now().UTC(),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("appendManyEvents(%d): %v", count, err)
	}
}

// measureFullRebuildLatency requests a fresh rebuild, claims its job, and
// returns the real wall-clock time for the ONE ExecuteProjectionRebuild
// call that drives it all the way to SUCCEEDED (see this file's own
// package doc comment: a single call internally loops through every
// BUILDING round needed, regardless of event count).
func measureFullRebuildLatency(t *testing.T, fx *workerFixture, idempotencyKey string) time.Duration {
	t.Helper()
	created := fx.requestRebuild(t, idempotencyKey)
	job, _ := fx.claimJob(t, "rebuild-worker-"+idempotencyKey, 5*time.Minute)

	start := time.Now()
	if err := projectionrebuildworker.ExecuteProjectionRebuild(context.Background(), fx.deps(), job); err != nil {
		t.Fatalf("ExecuteProjectionRebuild: %v", err)
	}
	elapsed := time.Since(start)

	op := fx.getOperation(t, created.OperationID)
	if op.Phase != ports.ProjectionRebuildSucceeded {
		t.Fatalf("op.Phase = %s, want SUCCEEDED", op.Phase)
	}
	return elapsed
}

// TestV8PerformanceBudget_FullRebuildLatencyScalesBoundedWithEventCount is
// V8-07's own frozen scaling-shape assertion for projection rebuild.
//
// Frozen threshold: ratio < 30x for a 10x event-count increase. Owner:
// V8-07 task (this session, 2026-09-29). Reason: BUILDING replays events in
// bounded BatchSize rounds (a real O(n) cost in event count, by this
// package's own documented design — see execute.go's own "repeated,
// bounded ReplayGenerationBatch rounds" description), predicting a ratio
// near 10x for a 10x event-count increase; 30x leaves the same generous
// headroom this session's other V8-07 scaling-ratio tests use (see
// internal/delivery/httpapi/kanban's own identical reasoning) for
// per-round fixed-cost noise on a contended CI runner, while still failing
// well below the ~100x a real O(n^2) regression in the replay loop would
// produce.
func TestV8PerformanceBudget_FullRebuildLatencyScalesBoundedWithEventCount(t *testing.T) {
	const small = 500
	const large = 5000

	fxSmall := newWorkerFixture(t, "perf-rebuild-small.db")
	fxSmall.appendManyEvents(t, small)
	smallLatency := measureFullRebuildLatency(t, fxSmall, "req-small")

	fxLarge := newWorkerFixture(t, "perf-rebuild-large.db")
	fxLarge.appendManyEvents(t, large)
	largeLatency := measureFullRebuildLatency(t, fxLarge, "req-large")

	t.Logf("V8-07 benchmark report: full projection rebuild latency — %d events: %v, %d events: %v (ratio %.2fx for a %dx event-count increase)",
		small, smallLatency, large, largeLatency, float64(largeLatency)/float64(smallLatency), large/small)

	const maxRatio = 30.0
	if smallLatency <= 0 {
		t.Fatalf("smallLatency = %v, want > 0", smallLatency)
	}
	if ratio := float64(largeLatency) / float64(smallLatency); ratio > maxRatio {
		t.Errorf("rebuild latency ratio = %.2fx for a %dx event-count increase, want < %.0fx (frozen V8-07 threshold — suggests a worse-than-linear replay path)",
			ratio, large/small, maxRatio)
	}
}
