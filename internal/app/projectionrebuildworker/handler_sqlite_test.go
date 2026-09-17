package projectionrebuildworker_test

import (
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/projectionrebuildworker"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
)

func TestHandler_ImplementsWorkerpoolHandler(t *testing.T) {
	var _ workerpool.Handler = (*projectionrebuildworker.Handler)(nil)
}

// TestHandler_Handle_RunsRealRebuildToSucceeded proves Handler is a real,
// callable workerpool.Handler wired against the real stack — a caller
// could register it on a workerpool.Registry today (see Handler's own doc
// comment for the deliberate scope boundary: this task does not itself
// wire a continuous "aw worker" composition-root call around it).
func TestHandler_Handle_RunsRealRebuildToSucceeded(t *testing.T) {
	fx := newWorkerFixture(t, "handler-real-rebuild.db")
	fx.appendEvent(t, "RootWorkItemCreated", 1, rootWorkItemCreatedPayload)
	fx.applyLive(t, "live-consumer")

	created := fx.requestRebuild(t, "req-1")
	job, _ := fx.claimJob(t, "rebuild-worker", time.Hour)

	handler := projectionrebuildworker.NewHandler(fx.deps())
	if err := handler.Handle(fx.ctx, job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	op := fx.getOperation(t, created.OperationID)
	if op.Phase != ports.ProjectionRebuildSucceeded {
		t.Fatalf("op.Phase = %s, want SUCCEEDED", op.Phase)
	}
}
