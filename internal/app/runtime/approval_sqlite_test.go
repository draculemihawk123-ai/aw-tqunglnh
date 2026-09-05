package runtime_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

// claimApprovalTimerJob claims jobs off store's own queue until it gets
// the APPROVAL_TIMER one, mirroring claimWaitTimerJob's own "skip earlier
// ADVANCE_RUN jobs still sitting AVAILABLE" discipline.
func claimApprovalTimerJob(t *testing.T, ctx context.Context, store *sqlite.Store, ttl time.Duration) (ports.DurableJob, ports.JobLease) {
	t.Helper()
	for i := 0; i < 5; i++ {
		job, lease, err := store.ClaimJob(ctx, "worker-1", ttl)
		if err != nil {
			t.Fatalf("ClaimJob: %v", err)
		}
		if job.Kind == runtime.ApprovalTimerJobKind {
			return job, lease
		}
	}
	t.Fatal("did not find an APPROVAL_TIMER job to claim within 5 attempts")
	return ports.DurableJob{}, ports.JobLease{}
}

// TestApprovalTimeoutHandler_SQLite_PersistsAcrossRestart proves an
// ApprovalRequest and its own APPROVAL_TIMER job survive a genuine process
// restart (close/reopen the same database file) and still fire correctly
// afterward — this task's own explicit "restart" Verify requirement,
// mirroring V4-08's own TestWaitTimeoutHandler_SQLite_PersistsAcrossRestart.
func TestApprovalTimeoutHandler_SQLite_PersistsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agentkit-approval-restart.db")

	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	u := sqlite.NewUnitOfWork(store)
	seq := idsource.NewSequential("id")

	root := readyFixtureSQLite(t, u, seq, "project-1", "repo-1")
	version := publishWorkflowVersionDocument(t, u, "project-1", "wf-def-1", "wf-v-1", approvalDocument(1)) // due in 1 second
	startCmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	startResult, err := runtime.StartWorkflowRun(ctx, u, seq, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	hop, err := runtime.AdvanceRun(ctx, u, seq, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: startResult.NodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun (start->gate): %v", err)
	}
	if hop.NextApprovalRequestID == "" {
		t.Fatalf("hop = %+v, want a minted NextApprovalRequestID", hop)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	time.Sleep(1200 * time.Millisecond) // past the 1s due time

	reopened, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen Open: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	reopenedUow := sqlite.NewUnitOfWork(reopened)

	job, lease := claimApprovalTimerJob(t, ctx, reopened, 30*time.Second)
	var payload runtime.ApprovalTimerJobPayload
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		t.Fatalf("decode timer job payload: %v", err)
	}
	if payload.ApprovalRequestID != hop.NextApprovalRequestID || payload.RunID != startResult.RunID {
		t.Fatalf("timer job payload = %+v, want request %s run %s", payload, hop.NextApprovalRequestID, startResult.RunID)
	}

	handler := runtime.NewApprovalTimeoutHandler(reopenedUow, seq)
	leaseUntil := lease.LeaseUntil
	if err := handler.Handle(ctx, ports.DurableJob{
		ID: job.ID, AggregateType: job.AggregateType, AggregateID: job.AggregateID,
		Payload: job.Payload, LeaseOwner: lease.Owner, LeaseToken: lease.Token, LeaseUntil: &leaseUntil,
	}); err != nil {
		t.Fatalf("Handle after restart: %v", err)
	}

	var request runtimedomain.ApprovalRequest
	var gateNodeRun runtimedomain.NodeRun
	if err := reopenedUow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		request, err = tx.Approvals().GetApprovalRequest(ctx, hop.NextApprovalRequestID)
		if err != nil {
			return err
		}
		gateNodeRun, err = tx.Runtime().GetNodeRun(ctx, hop.NextNodeRunID)
		return err
	}); err != nil {
		t.Fatalf("read after restart: %v", err)
	}
	if request.State != runtimedomain.ApprovalRequestEscalated {
		t.Fatalf("request after restart = %+v, want ESCALATED", request)
	}
	if gateNodeRun.State != runtimedomain.NodeRunSucceeded || gateNodeRun.SelectedOutcome != "escalated" {
		t.Fatalf("gate node run after restart = %+v, want SUCCEEDED/escalated", gateNodeRun)
	}
}
