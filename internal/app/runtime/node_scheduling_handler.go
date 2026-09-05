package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
)

// NodeSchedulingHandler is V4-04's own workerpool.Handler for
// ScheduleNodeRunJobKind, mirroring Scheduler's identical role for
// AdvanceRunJobKind (scheduler.go): a thin adapter from one durable job to
// one ScheduleExecutableNodeRun call, holding the
// ports.RuntimeExecutionConfigProvider dependency ADR-027 requires.
type NodeSchedulingHandler struct {
	uow      ports.UnitOfWork
	ids      idsource.Source
	provider ports.RuntimeExecutionConfigProvider
}

// NewNodeSchedulingHandler returns a ready-to-register NodeSchedulingHandler.
func NewNodeSchedulingHandler(uow ports.UnitOfWork, ids idsource.Source, provider ports.RuntimeExecutionConfigProvider) *NodeSchedulingHandler {
	return &NodeSchedulingHandler{uow: uow, ids: ids, provider: provider}
}

var _ workerpool.Handler = (*NodeSchedulingHandler)(nil)

// Handle implements workerpool.Handler for ScheduleNodeRunJobKind: unmarshal
// the job's ScheduleNodeRunJobPayload and run exactly one
// ScheduleExecutableNodeRun call. Any error propagates unchanged, leaving
// the job for workerpool's own lease-expiry/retry mechanism — the same
// "never swallow a routing failure as a false job success" discipline
// Scheduler.Handle's own doc comment already establishes for
// AdvanceRunJobKind.
func (h *NodeSchedulingHandler) Handle(ctx context.Context, job ports.DurableJob) error {
	var payload ScheduleNodeRunJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("runtime: unmarshal %s job %s payload: %w", ScheduleNodeRunJobKind, job.ID, err)
	}
	if payload.RunID == "" || payload.NodeRunID == "" {
		return fmt.Errorf("runtime: %s job %s payload missing runId/nodeRunId", ScheduleNodeRunJobKind, job.ID)
	}
	_, err := ScheduleExecutableNodeRun(ctx, h.uow, h.ids, h.provider, ScheduleExecutableNodeRunRequest{
		RunID: payload.RunID, NodeRunID: payload.NodeRunID,
		CorrelationID: payload.CorrelationID, JobID: string(job.ID),
	})
	return err
}
