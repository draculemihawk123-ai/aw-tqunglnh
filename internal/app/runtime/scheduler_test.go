package runtime_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

func TestScheduler_Handle_AdvancesTheTargetNodeRun(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, startNodeRunID := startWorkflowRunFixture(t, workflowDocumentV1())
	scheduler := runtime.NewScheduler(uow, ids)

	payload, err := json.Marshal(runtime.AdvanceRunJobPayload{RunID: runID, NodeRunID: startNodeRunID})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := scheduler.Handle(ctx, ports.DurableJob{ID: "job-1", Kind: runtime.AdvanceRunJobKind, Payload: payload}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	startNodeRun, err := uow.Snapshot.Runtime().GetNodeRun(ctx, startNodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun: %v", err)
	}
	if startNodeRun.State != runtimedomain.NodeRunSucceeded || startNodeRun.SelectedOutcome != "next" {
		t.Fatalf("start node run = %+v, want SUCCEEDED with outcome next", startNodeRun)
	}
}

func TestScheduler_Handle_MalformedPayload_ReturnsError(t *testing.T) {
	ctx := context.Background()
	uow, ids, _, _ := startWorkflowRunFixture(t, workflowDocumentV1())
	scheduler := runtime.NewScheduler(uow, ids)

	if err := scheduler.Handle(ctx, ports.DurableJob{ID: "job-1", Kind: runtime.AdvanceRunJobKind, Payload: []byte("not json")}); err == nil {
		t.Fatal("Handle with malformed payload = nil error, want an error")
	}
}

func TestScheduler_Handle_MissingFields_ReturnsError(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, _ := startWorkflowRunFixture(t, workflowDocumentV1())
	scheduler := runtime.NewScheduler(uow, ids)

	payload, err := json.Marshal(runtime.AdvanceRunJobPayload{RunID: runID, NodeRunID: ""})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if err := scheduler.Handle(ctx, ports.DurableJob{ID: "job-1", Kind: runtime.AdvanceRunJobKind, Payload: payload}); err == nil {
		t.Fatal("Handle with missing nodeRunId = nil error, want an error")
	}
}
