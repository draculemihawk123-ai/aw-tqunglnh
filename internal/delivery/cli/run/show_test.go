package run_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	clirun "github.com/taQuangLing/agent-workflow/internal/delivery/cli/run"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

func TestRunShow_ReturnsRunDetail(t *testing.T) {
	deps, ids := newRunDeps(t)
	root := readyWorkItemFixture(t, deps.UOW, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, deps.UOW, "project-1", "wf-def-1", "wf-v-1", workflowDocumentV1())

	var startOut, startErr bytes.Buffer
	if err := clirun.Start(context.Background(), deps, []string{
		"--workflow-version-id", string(version.ID()), "--idempotency-key", "idem-1", root.WorkItemID,
	}, &startOut, &startErr); err != nil {
		t.Fatalf("Start: %v, stderr=%s", err, startErr.String())
	}
	runID := decodeStartRunID(t, startOut.Bytes())

	var stdout, stderr bytes.Buffer
	if err := clirun.Show(context.Background(), deps, []string{runID}, &stdout, &stderr); err != nil {
		t.Fatalf("Show: %v, stderr=%s", err, stderr.String())
	}
	var detail runtime.RunDetail
	if err := json.Unmarshal(stdout.Bytes(), &detail); err != nil {
		t.Fatalf("decode RunDetail: %v\nstdout=%s", err, stdout.String())
	}
	if detail.RunID != runID {
		t.Fatalf("RunDetail.RunID = %q, want %q", detail.RunID, runID)
	}
	if detail.State != "RUNNING" {
		t.Fatalf("RunDetail.State = %q, want RUNNING", detail.State)
	}
}

// approvalDocumentForCLI mirrors internal/app/runtime's own approvalDocument
// fixture (approval_test.go) — kept local per this package's own
// established "duplicate rather than import _test.go helpers" discipline.
func approvalDocumentForCLI(timeoutSeconds uint32) workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "gate", Type: workflow.NodeApproval, Outcomes: []string{"approved", "rejected", "escalated"}, Approval: &workflow.ApprovalNodeConfig{
				AuthorizedRoles: []string{"reviewer"}, TimeoutSeconds: timeoutSeconds, EscalationOutcome: "escalated",
			}},
			{Key: "end_approved", Type: workflow.NodeEnd},
			{Key: "end_rejected", Type: workflow.NodeEnd},
			{Key: "end_escalated", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-to-gate", From: "start", Outcome: "next", To: "gate"},
			{Key: "gate-to-approved", From: "gate", Outcome: "approved", To: "end_approved"},
			{Key: "gate-to-rejected", From: "gate", Outcome: "rejected", To: "end_rejected"},
			{Key: "gate-to-escalated", From: "gate", Outcome: "escalated", To: "end_escalated"},
		},
	}
}

// TestRunShow_ApprovalRequests_ReachCLIOutput is V6-06E's own "make sure the
// new arrays reach its JSON/human output through the same app query (no
// second code path)" proof: `aw run show` dispatches
// runtime.GetRunDetail directly (show.go) and JSON-encodes the returned
// struct verbatim — never a second, CLI-only DTO — so a Run parked on an
// APPROVAL node must show its approvalRequestId in `run show`'s own stdout,
// with no waitRegistrations key at all (the WAIT node has not activated).
func TestRunShow_ApprovalRequests_ReachCLIOutput(t *testing.T) {
	deps, ids := newRunDeps(t)
	root := readyWorkItemFixture(t, deps.UOW, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, deps.UOW, "project-1", "wf-def-1", "wf-v-1", approvalDocumentForCLI(600))

	ctx := context.Background()
	startCmd := ports.Command{
		ID: "cmd-start-1", IdempotencyKey: "idem-start-1", Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope("project-1"), RequestedAt: time.Now().UTC(),
		Type: "StartWorkflowRun", RequestHash: "hash-start-1",
	}
	startResult, err := runtime.StartWorkflowRun(ctx, deps.UOW, deps.IDs, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	hop, err := runtime.AdvanceRun(ctx, deps.UOW, deps.IDs, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: startResult.NodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun (start->gate): %v", err)
	}
	if hop.NextApprovalRequestID == "" {
		t.Fatalf("hop = %+v, want a minted NextApprovalRequestID", hop)
	}

	var stdout, stderr bytes.Buffer
	if err := clirun.Show(ctx, deps, []string{startResult.RunID}, &stdout, &stderr); err != nil {
		t.Fatalf("Show: %v, stderr=%s", err, stderr.String())
	}
	var detail runtime.RunDetail
	if err := json.Unmarshal(stdout.Bytes(), &detail); err != nil {
		t.Fatalf("decode RunDetail: %v\nstdout=%s", err, stdout.String())
	}
	if len(detail.ApprovalRequests) != 1 || detail.ApprovalRequests[0].ApprovalRequestID != hop.NextApprovalRequestID {
		t.Fatalf("ApprovalRequests = %+v, want exactly one matching %s", detail.ApprovalRequests, hop.NextApprovalRequestID)
	}
	if detail.ApprovalRequests[0].State != string(runtimedomain.ApprovalRequestPending) {
		t.Fatalf("ApprovalRequests[0].State = %s, want PENDING", detail.ApprovalRequests[0].State)
	}
	if detail.WaitRegistrations != nil {
		t.Fatalf("WaitRegistrations = %+v, want absent (nil) — no WAIT node in this document", detail.WaitRegistrations)
	}
	if strings.Contains(stdout.String(), "waitRegistrations") {
		t.Fatalf("stdout = %s, want no waitRegistrations key at all", stdout.String())
	}
}

func TestRunShow_UnknownRunID(t *testing.T) {
	deps, _ := newRunDeps(t)
	var stdout, stderr bytes.Buffer
	err := clirun.Show(context.Background(), deps, []string{"no-such-run"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("Show with an unknown runId succeeded, want an error")
	}
}

func TestRunShow_MissingRunIDArgument(t *testing.T) {
	deps, _ := newRunDeps(t)
	var stdout, stderr bytes.Buffer
	err := clirun.Show(context.Background(), deps, nil, &stdout, &stderr)
	if err == nil {
		t.Fatal("Show with no <runId> argument succeeded, want a usage error")
	}
}
