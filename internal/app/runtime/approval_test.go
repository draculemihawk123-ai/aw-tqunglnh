package runtime_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// V4-09's own APPROVAL fixtures and tests.

// approvalDocument is start -> gate(APPROVAL) -> end_approved|end_rejected|
// end_escalated: three declared outcomes, so the operator's own decision
// always supplies which one applies (GC-INV-11's own allow-list check,
// advanceRunTx, is what validates it) — EscalationOutcome is the only one
// pre-pinned, for the timer's own use.
func approvalDocument(timeoutSeconds uint32) workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "gate", Type: workflow.NodeApproval, Outcomes: []string{"approved", "rejected", "escalated"}, Approval: &workflow.ApprovalNodeConfig{
				AuthorizedRoles: []string{"reviewer"}, TimeoutSeconds: timeoutSeconds, EscalationOutcome: "escalated",
				RequestedEvidenceKinds: []string{"test-plan"},
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

// approvalFixture starts a run over document and advances it exactly one
// hop (start -> gate), returning the hop's own AdvanceRunResult —
// NextNodeRunID is the APPROVAL node's own NodeRunID, NextApprovalRequestID
// its freshly created request.
func approvalFixture(t *testing.T, document workflow.WorkflowDocument) (uow *fake.UnitOfWork, ids idsource.Source, runID string, hop runtime.AdvanceRunResult) {
	t.Helper()
	ctx := context.Background()
	u, seq, rid, startNodeRunID := startWorkflowRunFixture(t, document)
	result, err := runtime.AdvanceRun(ctx, u, seq, runtime.AdvanceRunRequest{RunID: rid, NodeRunID: startNodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun (start->gate): %v", err)
	}
	if result.NextNodeKey != "gate" || result.NextApprovalRequestID == "" || result.NextApprovalTimerJobID == "" {
		t.Fatalf("hop = %+v, want NextNodeKey=gate with a minted NextApprovalRequestID and NextApprovalTimerJobID", result)
	}
	return u, seq, rid, result
}

func reviewerCommand(idempotencyKey, requestHash string) ports.Command {
	cmd := testCommand(idempotencyKey, requestHash, ports.ProjectScope("project-1"), "ResolveApproval")
	cmd.Actor = "operator-1"
	cmd.ActorRoles = []string{"reviewer"}
	return cmd
}

// --- advanceRunTx: APPROVAL node activation itself ---

func TestAdvanceRun_ApprovalNode_CreatesRequestAndTimerJob(t *testing.T) {
	ctx := context.Background()
	before := time.Now().UTC()
	uow, _, _, hop := approvalFixture(t, approvalDocument(600))
	after := time.Now().UTC()

	gateNodeRun, err := uow.Snapshot.Runtime().GetNodeRun(ctx, hop.NextNodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun(gate): %v", err)
	}
	if gateNodeRun.State != runtimedomain.NodeRunWaiting {
		t.Fatalf("gate node run = %+v, want WAITING", gateNodeRun)
	}

	request, err := uow.Snapshot.Approvals().GetApprovalRequest(ctx, hop.NextApprovalRequestID)
	if err != nil {
		t.Fatalf("GetApprovalRequest: %v", err)
	}
	if request.State != runtimedomain.ApprovalRequestPending || request.EscalationOutcome != "escalated" {
		t.Fatalf("request = %+v, want PENDING/escalated", request)
	}
	if len(request.AuthorizedRoles) != 1 || request.AuthorizedRoles[0] != "reviewer" {
		t.Fatalf("request.AuthorizedRoles = %v, want [reviewer]", request.AuthorizedRoles)
	}
	if len(request.RequestedEvidenceKinds) != 1 || request.RequestedEvidenceKinds[0] != "test-plan" {
		t.Fatalf("request.RequestedEvidenceKinds = %v, want [test-plan]", request.RequestedEvidenceKinds)
	}
	wantMin := before.Add(600 * time.Second)
	wantMax := after.Add(600 * time.Second)
	if request.DueAt.Before(wantMin) || request.DueAt.After(wantMax) {
		t.Fatalf("request.DueAt = %v, want within [%v, %v]", request.DueAt, wantMin, wantMax)
	}

	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	found := false
	for _, j := range jobs {
		if j.Kind == runtime.ApprovalTimerJobKind && j.AggregateID == hop.NextApprovalRequestID {
			found = true
		}
	}
	if !found {
		t.Fatalf("no %s job found for request %s among %+v", runtime.ApprovalTimerJobKind, hop.NextApprovalRequestID, jobs)
	}
}

// --- ResolveApproval: happy path ---

func TestResolveApproval_AuthorizedActor_ApprovesAndRoutes(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, hop := approvalFixture(t, approvalDocument(600))

	result, err := runtime.ResolveApproval(ctx, uow, ids, reviewerCommand("idem-decide-1", "hash-1"), runtime.ResolveApprovalRequest{
		RunID: runID, ApprovalRequestID: hop.NextApprovalRequestID, Outcome: "approved", Reason: "looks good",
	})
	if err != nil {
		t.Fatalf("ResolveApproval: %v", err)
	}
	if !result.Won || result.MatchedRole != "reviewer" || !result.Advanced || result.NextNodeKey != "end_approved" {
		t.Fatalf("result = %+v, want Won=true MatchedRole=reviewer Advanced=true NextNodeKey=end_approved", result)
	}

	request, err := uow.Snapshot.Approvals().GetApprovalRequest(ctx, hop.NextApprovalRequestID)
	if err != nil {
		t.Fatalf("GetApprovalRequest: %v", err)
	}
	if request.State != runtimedomain.ApprovalRequestDecided || request.DecidedBy != "operator-1" ||
		request.DecidedRole != "reviewer" || request.DecidedOutcome != "approved" || request.Reason != "looks good" || request.DecidedAt == nil {
		t.Fatalf("request = %+v, want DECIDED by operator-1/reviewer/approved/looks good with DecidedAt set", request)
	}

	gateNodeRun, err := uow.Snapshot.Runtime().GetNodeRun(ctx, hop.NextNodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun(gate): %v", err)
	}
	if gateNodeRun.State != runtimedomain.NodeRunSucceeded || gateNodeRun.SelectedOutcome != "approved" {
		t.Fatalf("gate node run = %+v, want SUCCEEDED/approved", gateNodeRun)
	}
}

// --- ResolveApproval: unauthorized-shaped input ---

func TestResolveApproval_UnauthorizedActor_Rejected(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, hop := approvalFixture(t, approvalDocument(600))

	cmd := testCommand("idem-decide-1", "hash-1", ports.ProjectScope("project-1"), "ResolveApproval")
	cmd.Actor = "intruder-1"
	cmd.ActorRoles = []string{"guest"} // not "reviewer"

	_, err := runtime.ResolveApproval(ctx, uow, ids, cmd, runtime.ResolveApprovalRequest{
		RunID: runID, ApprovalRequestID: hop.NextApprovalRequestID, Outcome: "approved",
	})
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != errorcode.CodePolicyDenied {
		t.Fatalf("err = %v, want an *apperror.Error with CodePolicyDenied", err)
	}

	request, getErr := uow.Snapshot.Approvals().GetApprovalRequest(ctx, hop.NextApprovalRequestID)
	if getErr != nil {
		t.Fatalf("GetApprovalRequest: %v", getErr)
	}
	if request.State != runtimedomain.ApprovalRequestPending {
		t.Fatalf("request = %+v, want unchanged PENDING (unauthorized actor must never resolve it)", request)
	}

	gateNodeRun, getErr := uow.Snapshot.Runtime().GetNodeRun(ctx, hop.NextNodeRunID)
	if getErr != nil {
		t.Fatalf("GetNodeRun(gate): %v", getErr)
	}
	if gateNodeRun.State != runtimedomain.NodeRunWaiting {
		t.Fatalf("gate node run = %+v, want unchanged WAITING", gateNodeRun)
	}
}

func TestResolveApproval_NoRolesAtAll_Rejected(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, hop := approvalFixture(t, approvalDocument(600))

	cmd := testCommand("idem-decide-1", "hash-1", ports.ProjectScope("project-1"), "ResolveApproval")
	cmd.Actor = "no-roles-1"
	// ActorRoles deliberately left nil/empty.

	_, err := runtime.ResolveApproval(ctx, uow, ids, cmd, runtime.ResolveApprovalRequest{
		RunID: runID, ApprovalRequestID: hop.NextApprovalRequestID, Outcome: "approved",
	})
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != errorcode.CodePolicyDenied {
		t.Fatalf("err = %v, want an *apperror.Error with CodePolicyDenied", err)
	}
}

// --- ResolveApproval: duplicate decision ---

func TestResolveApproval_DuplicateDecision_SecondAttemptWonFalse(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, hop := approvalFixture(t, approvalDocument(600))

	first, err := runtime.ResolveApproval(ctx, uow, ids, reviewerCommand("idem-decide-1", "hash-1"), runtime.ResolveApprovalRequest{
		RunID: runID, ApprovalRequestID: hop.NextApprovalRequestID, Outcome: "approved",
	})
	if err != nil {
		t.Fatalf("first ResolveApproval: %v", err)
	}
	if !first.Won {
		t.Fatalf("first result = %+v, want Won=true", first)
	}

	// A second, genuinely different command invocation (different
	// IdempotencyKey — e.g. a different reviewer, or the same reviewer
	// double-clicking) attempting to decide the SAME already-DECIDED
	// request.
	second, err := runtime.ResolveApproval(ctx, uow, ids, reviewerCommand("idem-decide-2", "hash-2"), runtime.ResolveApprovalRequest{
		RunID: runID, ApprovalRequestID: hop.NextApprovalRequestID, Outcome: "rejected",
	})
	if err != nil {
		t.Fatalf("second ResolveApproval: %v", err)
	}
	if second.Won || second.State != string(runtimedomain.ApprovalRequestDecided) {
		t.Fatalf("second result = %+v, want Won=false State=DECIDED", second)
	}

	events := uow.Snapshot.Events().(*fake.EventsRepository).Items()
	routedCount := 0
	for _, e := range events {
		if e.EventType == runtime.NodeRoutedEventType && e.AggregateID == hop.NextNodeRunID {
			routedCount++
		}
	}
	if routedCount != 1 {
		t.Fatalf("NODE_ROUTED count = %d, want exactly 1 (the duplicate decision must never re-route)", routedCount)
	}

	// The request's own real decision (the winner's "approved") must not
	// have been overwritten by the loser's "rejected".
	request, err := uow.Snapshot.Approvals().GetApprovalRequest(ctx, hop.NextApprovalRequestID)
	if err != nil {
		t.Fatalf("GetApprovalRequest: %v", err)
	}
	if request.DecidedOutcome != "approved" {
		t.Fatalf("request.DecidedOutcome = %s, want unchanged approved", request.DecidedOutcome)
	}
}

// --- ResolveApproval: receipt replay re-authorizes (V6-13) ---

// TestResolveApproval_RevokedRoleCannotReplayItsOwnEarlierDecision pins
// design doc §1 contract point 3 at the application layer, the path `aw`
// takes directly (the HTTP handler has its own receipt fast path, proven
// separately in internal/delivery/httpapi/securitymatrix): a stored receipt
// must never answer for an actor whose authorizing role has since been
// revoked, even with the identical Idempotency-Key and request hash.
func TestResolveApproval_RevokedRoleCannotReplayItsOwnEarlierDecision(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, hop := approvalFixture(t, approvalDocument(600))
	request := runtime.ResolveApprovalRequest{
		RunID: runID, ApprovalRequestID: hop.NextApprovalRequestID, Outcome: "approved", Reason: "looks good",
	}

	first, err := runtime.ResolveApproval(ctx, uow, ids, reviewerCommand("idem-replay-1", "hash-replay-1"), request)
	if err != nil || !first.Won {
		t.Fatalf("first ResolveApproval = %+v, %v; want Won=true, nil", first, err)
	}

	// Same actor, same role: a genuine replay still returns the stored
	// result — the reordering must not break idempotency.
	replay, err := runtime.ResolveApproval(ctx, uow, ids, reviewerCommand("idem-replay-1", "hash-replay-1"), request)
	if err != nil {
		t.Fatalf("replay under the SAME role: %v", err)
	}
	if replay.Won != first.Won || replay.MatchedRole != first.MatchedRole || replay.NextNodeKey != first.NextNodeKey {
		t.Fatalf("replay = %+v, want the stored result %+v", replay, first)
	}

	for name, roles := range map[string][]string{"downgraded to a role the request does not authorize": {"guest"}, "all roles revoked": nil} {
		revoked := reviewerCommand("idem-replay-1", "hash-replay-1")
		revoked.ActorRoles = roles
		got, err := runtime.ResolveApproval(ctx, uow, ids, revoked, request)
		var appErr *apperror.Error
		if !errors.As(err, &appErr) || appErr.Code != errorcode.CodePolicyDenied {
			t.Errorf("%s: err = %v (result %+v), want an *apperror.Error with CodePolicyDenied — a stored receipt must not answer for a revoked role", name, err, got)
		}
		if got.Won || got.MatchedRole != "" {
			t.Errorf("%s: result = %+v, want the zero result (no stored decision may leak)", name, got)
		}
	}
}

// --- ApprovalTimeoutHandler ---

func TestApprovalTimeoutHandler_RoutesViaEscalationOutcome(t *testing.T) {
	ctx := context.Background()
	uow, ids, _, hop := approvalFixture(t, approvalDocument(600))

	handler := runtime.NewApprovalTimeoutHandler(uow, ids)
	job := claimableApprovalTimerJob(t, uow, hop.NextApprovalRequestID)
	if err := handler.Handle(ctx, job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	request, err := uow.Snapshot.Approvals().GetApprovalRequest(ctx, hop.NextApprovalRequestID)
	if err != nil {
		t.Fatalf("GetApprovalRequest: %v", err)
	}
	if request.State != runtimedomain.ApprovalRequestEscalated || request.DecidedBy != "" {
		t.Fatalf("request = %+v, want ESCALATED with no DecidedBy", request)
	}

	gateNodeRun, err := uow.Snapshot.Runtime().GetNodeRun(ctx, hop.NextNodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun(gate): %v", err)
	}
	if gateNodeRun.State != runtimedomain.NodeRunSucceeded || gateNodeRun.SelectedOutcome != "escalated" {
		t.Fatalf("gate node run = %+v, want SUCCEEDED/escalated", gateNodeRun)
	}
}

func TestApprovalTimeoutHandler_ReplayAfterAlreadyDecided_IsNoOp(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, hop := approvalFixture(t, approvalDocument(600))

	if _, err := runtime.ResolveApproval(ctx, uow, ids, reviewerCommand("idem-decide-1", "hash-1"), runtime.ResolveApprovalRequest{
		RunID: runID, ApprovalRequestID: hop.NextApprovalRequestID, Outcome: "approved",
	}); err != nil {
		t.Fatalf("ResolveApproval: %v", err)
	}

	handler := runtime.NewApprovalTimeoutHandler(uow, ids)
	job := claimableApprovalTimerJob(t, uow, hop.NextApprovalRequestID)
	if err := handler.Handle(ctx, job); err != nil {
		t.Fatalf("Handle (replayed timer job): %v", err)
	}

	request, err := uow.Snapshot.Approvals().GetApprovalRequest(ctx, hop.NextApprovalRequestID)
	if err != nil {
		t.Fatalf("GetApprovalRequest: %v", err)
	}
	if request.State != runtimedomain.ApprovalRequestDecided {
		t.Fatalf("request = %+v, want unchanged DECIDED (replayed timer must never overwrite it)", request)
	}

	events := uow.Snapshot.Events().(*fake.EventsRepository).Items()
	routedCount := 0
	for _, e := range events {
		if e.EventType == runtime.NodeRoutedEventType && e.AggregateID == hop.NextNodeRunID {
			routedCount++
		}
	}
	if routedCount != 1 {
		t.Fatalf("NODE_ROUTED count = %d, want exactly 1 (replayed timer must never re-route)", routedCount)
	}
}

// claimableApprovalTimerJob builds a ports.DurableJob for the real
// APPROVAL_TIMER job the APPROVAL dispatch already enqueued for
// approvalRequestID, mirroring wait_test.go's own claimableWaitTimerJob.
func claimableApprovalTimerJob(t *testing.T, uow *fake.UnitOfWork, approvalRequestID string) ports.DurableJob {
	t.Helper()
	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	var found *ports.EnqueueJobRequest
	for i := range jobs {
		if jobs[i].Kind == runtime.ApprovalTimerJobKind && jobs[i].AggregateID == approvalRequestID {
			found = &jobs[i]
		}
	}
	if found == nil {
		t.Fatalf("no %s job found for request %s among %+v", runtime.ApprovalTimerJobKind, approvalRequestID, jobs)
	}
	leaseUntil := time.Now().Add(time.Minute)
	lease := ports.JobLease{JobID: found.ID, Owner: "worker-1", Token: 1, LeaseUntil: leaseUntil}
	uow.Snapshot.Jobs().(*fake.JobsRepository).SetActiveLease(string(found.ID), lease)
	return ports.DurableJob{
		ID: found.ID, AggregateType: found.AggregateType, AggregateID: found.AggregateID,
		Payload: found.Payload, LeaseOwner: lease.Owner, LeaseToken: lease.Token, LeaseUntil: &leaseUntil,
	}
}
