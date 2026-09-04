package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/workerpool"
)

// Scheduler is V4-03's own workerpool.Handler for AdvanceRunJobKind — the
// consumer StartWorkflowRun's own doc comment (V4-02, commands.go) already
// named as "sits AVAILABLE until V4-03 builds one". Unlike
// workspaceprovision.Handler/repositoryprobe's handlers, it never touches a
// concrete provider/adapter: routing is pure application logic over
// ports.UnitOfWork, so Scheduler is a thin adapter from one durable job to
// one AdvanceRun call.
type Scheduler struct {
	uow ports.UnitOfWork
	ids idsource.Source
}

// NewScheduler returns a ready-to-register Scheduler.
func NewScheduler(uow ports.UnitOfWork, ids idsource.Source) *Scheduler {
	return &Scheduler{uow: uow, ids: ids}
}

var _ workerpool.Handler = (*Scheduler)(nil)

// Handle implements workerpool.Handler for AdvanceRunJobKind: unmarshal the
// job's AdvanceRunJobPayload and run exactly one AdvanceRun hop. Any error
// AdvanceRun returns (including ErrOutcomeRequired/ErrOutcomeNotAllowed/
// ErrRouteNotFound) propagates unchanged, leaving the job for workerpool's
// own lease-expiry/retry mechanism — this handler never swallows a routing
// failure as a false job success the way workspaceprovision.Handler's own
// "job success vs business outcome" split does for real
// provider/filesystem I/O; nothing here is external environment state, so
// every error is a genuine defect worth retrying/surfacing, not data.
func (s *Scheduler) Handle(ctx context.Context, job ports.DurableJob) error {
	var payload AdvanceRunJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return fmt.Errorf("runtime: unmarshal %s job %s payload: %w", AdvanceRunJobKind, job.ID, err)
	}
	if payload.RunID == "" || payload.NodeRunID == "" {
		return fmt.Errorf("runtime: %s job %s payload missing runId/nodeRunId", AdvanceRunJobKind, job.ID)
	}
	_, err := AdvanceRun(ctx, s.uow, s.ids, AdvanceRunRequest{
		RunID: payload.RunID, NodeRunID: payload.NodeRunID,
		CorrelationID: payload.CorrelationID, JobID: string(job.ID),
	})
	return err
}
