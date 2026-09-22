package runtime_test

// V6-06E's own test suite for GetRunDetail's ApprovalRequests/
// WaitRegistrations (run_detail_queries.go) — the rework of V6-06A found
// empirically by the V6-14 black-box journey: resolveApproval /
// submitWaitSignal need an approvalRequestId / waitRegistrationId that no
// public read used to return. Every fixture below drives a REAL
// StartWorkflowRun/AdvanceRun/ResolveApproval/SignalWait sequence against a
// real *sqlite.Store (never a hand-seeded approval_requests/
// wait_registrations row), reusing this package's own readyFixtureSQLite/
// publishWorkflowVersionDocument/testCommand helpers.

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// approvalThenSignalWaitDocument is start -> approval(APPROVAL) ->
// wait_signal(WAIT, SIGNAL, no timeout ceiling) -> end, the exact shape the
// V6-14 journey parks a Run on: the approval node declares one authorized
// role, one requested evidence kind and "denied" as its timer-side
// EscalationOutcome; the WAIT node declares a single Outcome so its
// CompletionOutcome is inferred (WaitNodeConfig's own documented rule) and,
// with TimeoutSeconds == 0, has no deadline at all.
func approvalThenSignalWaitDocument() workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "approval", Type: workflow.NodeApproval, Outcomes: []string{"approved", "denied"}, Approval: &workflow.ApprovalNodeConfig{
				AuthorizedRoles: []string{"operator"}, TimeoutSeconds: 3600, EscalationOutcome: "denied",
				RequestedEvidenceKinds: []string{"test-plan"},
			}},
			{Key: "wait_signal", Type: workflow.NodeWait, Outcomes: []string{"released"}, Wait: &workflow.WaitNodeConfig{
				Mode: workflow.WaitModeSignal, SignalName: "release-approved-externally",
			}},
			{Key: "end", Type: workflow.NodeEnd},
			{Key: "end_denied", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-to-approval", From: "start", Outcome: "next", To: "approval"},
			{Key: "approval-to-wait", From: "approval", Outcome: "approved", To: "wait_signal"},
			{Key: "approval-to-denied", From: "approval", Outcome: "denied", To: "end_denied"},
			{Key: "wait-to-end", From: "wait_signal", Outcome: "released", To: "end"},
		},
	}
}

func operatorCommand(idempotencyKey, requestHash, commandType string) ports.Command {
	cmd := testCommand(idempotencyKey, requestHash, ports.ProjectScope("project-1"), commandType)
	cmd.Actor = "operator-1"
	cmd.ActorRoles = []string{"operator"}
	return cmd
}

func mustGetRunDetail(t *testing.T, uow ports.UnitOfWork, runID string) runtime.RunDetail {
	t.Helper()
	detail, err := runtime.GetRunDetail(context.Background(), uow, runID)
	if err != nil {
		t.Fatalf("GetRunDetail(%s): %v", runID, err)
	}
	return detail
}

// TestGetRunDetail_SQLite_ApprovalAndWait_DiscoverableThroughLifecycle is
// V6-06E's own headline proof against real SQLite: a Run parked on its
// APPROVAL node lists exactly that one PENDING request (and no WAIT yet);
// after ResolveApproval the request is decided with its ResolvedOutcome and
// the WAIT registration appears with its signal name and no deadline; after
// SignalWait the registration is decided too, and BOTH stay listed as
// history. The ids the detail returns are the ids the real commands accept.
func TestGetRunDetail_SQLite_ApprovalAndWait_DiscoverableThroughLifecycle(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "agentkit-rundetail-decisions.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")

	root := readyFixtureSQLite(t, uow, ids, "project-1", "repo-1")
	version := publishWorkflowVersionDocument(t, uow, "project-1", "wf-def-1", "wf-v-1", approvalThenSignalWaitDocument())
	startCmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	startResult, err := runtime.StartWorkflowRun(ctx, uow, ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	before := time.Now().UTC()
	hop, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: startResult.NodeRunID})
	after := time.Now().UTC()
	if err != nil {
		t.Fatalf("AdvanceRun (start->approval): %v", err)
	}
	if hop.NextNodeKey != "approval" || hop.NextApprovalRequestID == "" {
		t.Fatalf("hop = %+v, want NextNodeKey=approval with a minted NextApprovalRequestID", hop)
	}

	// --- 1. Parked on the APPROVAL node ---
	detail := mustGetRunDetail(t, uow, startResult.RunID)
	if len(detail.ApprovalRequests) != 1 {
		t.Fatalf("ApprovalRequests = %+v, want exactly one (the pending request)", detail.ApprovalRequests)
	}
	if len(detail.WaitRegistrations) != 0 {
		t.Fatalf("WaitRegistrations = %+v, want none yet (the Run has not reached the WAIT node)", detail.WaitRegistrations)
	}
	pending := detail.ApprovalRequests[0]
	if pending.ApprovalRequestID != hop.NextApprovalRequestID || pending.NodeRunID != hop.NextNodeRunID || pending.NodeKey != "approval" {
		t.Fatalf("pending = %+v, want approvalRequestId=%s nodeRunId=%s nodeKey=approval", pending, hop.NextApprovalRequestID, hop.NextNodeRunID)
	}
	if pending.State != string(runtimedomain.ApprovalRequestPending) || pending.Version != 1 || pending.ResolvedOutcome != "" {
		t.Fatalf("pending = %+v, want State=PENDING Version=1 and no ResolvedOutcome", pending)
	}
	if len(pending.AuthorizedRoles) != 1 || pending.AuthorizedRoles[0] != "operator" {
		t.Fatalf("pending.AuthorizedRoles = %v, want [operator]", pending.AuthorizedRoles)
	}
	if len(pending.RequestedEvidenceKinds) != 1 || pending.RequestedEvidenceKinds[0] != "test-plan" {
		t.Fatalf("pending.RequestedEvidenceKinds = %v, want [test-plan]", pending.RequestedEvidenceKinds)
	}
	if pending.EscalationOutcome != "denied" {
		t.Fatalf("pending.EscalationOutcome = %q, want denied", pending.EscalationOutcome)
	}
	if pending.DueAt.Before(before.Add(3600*time.Second)) || pending.DueAt.After(after.Add(3600*time.Second)) {
		t.Fatalf("pending.DueAt = %v, want within [%v, %v] (TimeoutSeconds=3600)", pending.DueAt, before.Add(3600*time.Second), after.Add(3600*time.Second))
	}

	// --- 2. The id the detail returned is the id ResolveApproval accepts ---
	resolveCmd := operatorCommand("idem-resolve-1", "hash-resolve-1", "ResolveApproval")
	resolved, err := runtime.ResolveApproval(ctx, uow, ids, resolveCmd, runtime.ResolveApprovalRequest{
		RunID: startResult.RunID, ApprovalRequestID: pending.ApprovalRequestID, Outcome: "approved", Reason: "ship it",
	})
	if err != nil {
		t.Fatalf("ResolveApproval(%s): %v", pending.ApprovalRequestID, err)
	}
	if !resolved.Won || resolved.NextNodeKey != "wait_signal" {
		t.Fatalf("resolved = %+v, want Won=true routed to wait_signal", resolved)
	}

	detail = mustGetRunDetail(t, uow, startResult.RunID)
	if len(detail.ApprovalRequests) != 1 {
		t.Fatalf("ApprovalRequests after resolve = %+v, want the decided request still listed as history", detail.ApprovalRequests)
	}
	decided := detail.ApprovalRequests[0]
	if decided.ApprovalRequestID != pending.ApprovalRequestID || decided.State != string(runtimedomain.ApprovalRequestDecided) ||
		decided.ResolvedOutcome != "approved" || decided.Version != 2 {
		t.Fatalf("decided = %+v, want same id, State=DECIDED ResolvedOutcome=approved Version=2", decided)
	}
	if len(detail.WaitRegistrations) != 1 {
		t.Fatalf("WaitRegistrations after resolve = %+v, want exactly one (the WAIT node is now active)", detail.WaitRegistrations)
	}
	active := detail.WaitRegistrations[0]
	if active.NodeRunID != resolved.NextNodeRunID || active.NodeKey != "wait_signal" {
		t.Fatalf("active = %+v, want nodeRunId=%s nodeKey=wait_signal", active, resolved.NextNodeRunID)
	}
	if active.State != string(runtimedomain.WaitRegistrationActive) || active.Mode != string(workflow.WaitModeSignal) ||
		active.SignalName != "release-approved-externally" || active.DueAt != nil {
		t.Fatalf("active = %+v, want State=ACTIVE Mode=SIGNAL SignalName=release-approved-externally and no DueAt (no timeout ceiling)", active)
	}
	// The listed id is a real registration for this very NodeRun.
	var registration runtimedomain.WaitRegistration
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		registration, err = tx.Wait().GetWaitRegistration(ctx, active.WaitRegistrationID)
		return err
	}); err != nil {
		t.Fatalf("GetWaitRegistration(%s): %v", active.WaitRegistrationID, err)
	}
	if string(registration.NodeRunID) != active.NodeRunID || string(registration.RunID) != startResult.RunID {
		t.Fatalf("registration = %+v, want it to belong to node run %s of run %s", registration, active.NodeRunID, startResult.RunID)
	}

	// --- 3. The id the detail returned is the id SignalWait accepts ---
	signalCmd := operatorCommand("idem-signal-1", "hash-signal-1", "SignalWait")
	signaled, err := runtime.SignalWait(ctx, uow, ids, signalCmd, runtime.SignalWaitRequest{
		RunID: startResult.RunID, WaitRegistrationID: active.WaitRegistrationID, SignalKey: "release-1", Payload: json.RawMessage(`{"ok":true}`),
	})
	if err != nil {
		t.Fatalf("SignalWait(%s): %v", active.WaitRegistrationID, err)
	}
	if !signaled.Won || signaled.NextNodeKey != "end" {
		t.Fatalf("signaled = %+v, want Won=true routed to end", signaled)
	}

	detail = mustGetRunDetail(t, uow, startResult.RunID)
	if len(detail.WaitRegistrations) != 1 || detail.WaitRegistrations[0].State != string(runtimedomain.WaitRegistrationConsumed) {
		t.Fatalf("WaitRegistrations after signal = %+v, want the one registration CONSUMED", detail.WaitRegistrations)
	}
	if len(detail.ApprovalRequests) != 1 || detail.ApprovalRequests[0].State != string(runtimedomain.ApprovalRequestDecided) {
		t.Fatalf("ApprovalRequests after signal = %+v, want the decided request still listed", detail.ApprovalRequests)
	}
	if detail.State != string(runtimedomain.WorkflowRunVerifying) {
		t.Fatalf("detail.State = %s, want VERIFYING (END reached — the Run can finish over the public surface)", detail.State)
	}

	// --- 4. Nothing free-text or actor-identifying leaks through ---
	// The decision's own audit trail (DecidedBy/DecidedRole/Reason) is
	// deliberately NOT part of the view (see ApprovalRequestView's doc
	// comment): Reason is free text that GetRunDetail has no redact.Matcher
	// for.
	wire, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("marshal detail: %v", err)
	}
	for _, leaked := range []string{"ship it", "operator-1", `"payload"`, "release-1"} {
		if strings.Contains(string(wire), leaked) {
			t.Fatalf("run detail JSON leaks %q: %s", leaked, wire)
		}
	}
}

// TestGetRunDetail_SQLite_NoDecisionNodes_OmitsBothArrays proves a Run with
// no APPROVAL/WAIT node yields nil slices — the omitempty tags then drop
// both keys, so every pre-V6-06E response stays byte-identical.
func TestGetRunDetail_SQLite_NoDecisionNodes_OmitsBothArrays(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "agentkit-rundetail-nodecisions.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")

	root := readyFixtureSQLite(t, uow, ids, "project-1", "repo-1")
	version := publishWorkflowVersionDocument(t, uow, "project-1", "wf-def-1", "wf-v-1", workflowDocumentV1())
	startCmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	startResult, err := runtime.StartWorkflowRun(ctx, uow, ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	if _, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: startResult.NodeRunID}); err != nil {
		t.Fatalf("AdvanceRun (start->end): %v", err)
	}

	detail := mustGetRunDetail(t, uow, startResult.RunID)
	if detail.ApprovalRequests != nil || detail.WaitRegistrations != nil {
		t.Fatalf("ApprovalRequests=%+v WaitRegistrations=%+v, want both nil for a Run with neither node type", detail.ApprovalRequests, detail.WaitRegistrations)
	}
	wire, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("marshal detail: %v", err)
	}
	if strings.Contains(string(wire), "approvalRequests") || strings.Contains(string(wire), "waitRegistrations") {
		t.Fatalf("run detail JSON = %s, want neither approvalRequests nor waitRegistrations key", wire)
	}
}

// descendingIDs is a deterministic idsource.Source whose ids sort STRICTLY
// DESCENDING in call order, so the sqlite adapters'
// ListApprovalRequestsForRun/ListWaitRegistrationsForRun (which are
// "ORDER BY id") return the LAST-created row FIRST. It exists to make the
// activation-order guard below genuinely discriminating — with uuid or
// sequential ids "ORDER BY id" would agree with activation order by chance
// (or, for "id-10" < "id-9", disagree only sometimes).
type descendingIDs struct{ next int }

func (d *descendingIDs) NewID() string {
	d.next++
	return fmt.Sprintf("desc-%06d", 999999-d.next)
}

// approvalsAndWaitsChainDocument is start -> first_gate(APPROVAL) ->
// second_gate(APPROVAL) -> wait_signal(WAIT, SIGNAL, 1800s ceiling) ->
// wait_duration(WAIT, DURATION 3600s) -> end: two of each decision node type
// in a fixed activation order, covering both WAIT modes and a deadline on
// each. second_gate deliberately declares no RequestedEvidenceKinds so the
// omit-when-empty tag is exercised.
func approvalsAndWaitsChainDocument() workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "first_gate", Type: workflow.NodeApproval, Outcomes: []string{"approved", "denied"}, Approval: &workflow.ApprovalNodeConfig{
				AuthorizedRoles: []string{"operator"}, TimeoutSeconds: 3600, EscalationOutcome: "denied",
				RequestedEvidenceKinds: []string{"test-plan"},
			}},
			{Key: "second_gate", Type: workflow.NodeApproval, Outcomes: []string{"approved", "denied"}, Approval: &workflow.ApprovalNodeConfig{
				AuthorizedRoles: []string{"operator", "reviewer"}, TimeoutSeconds: 7200, EscalationOutcome: "denied",
			}},
			{Key: "wait_signal", Type: workflow.NodeWait, Outcomes: []string{"released", "expired"}, Wait: &workflow.WaitNodeConfig{
				Mode: workflow.WaitModeSignal, SignalName: "release-approved-externally", TimeoutSeconds: 1800,
				CompletionOutcome: "released", TimeoutOutcome: "expired",
			}},
			{Key: "wait_duration", Type: workflow.NodeWait, Outcomes: []string{"elapsed"}, Wait: &workflow.WaitNodeConfig{
				Mode: workflow.WaitModeDuration, DurationSeconds: 3600,
			}},
			{Key: "end", Type: workflow.NodeEnd},
			{Key: "end_denied_1", Type: workflow.NodeEnd},
			{Key: "end_denied_2", Type: workflow.NodeEnd},
			{Key: "end_expired", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-to-first", From: "start", Outcome: "next", To: "first_gate"},
			{Key: "first-to-second", From: "first_gate", Outcome: "approved", To: "second_gate"},
			{Key: "first-to-denied", From: "first_gate", Outcome: "denied", To: "end_denied_1"},
			{Key: "second-to-signal", From: "second_gate", Outcome: "approved", To: "wait_signal"},
			{Key: "second-to-denied", From: "second_gate", Outcome: "denied", To: "end_denied_2"},
			{Key: "signal-to-duration", From: "wait_signal", Outcome: "released", To: "wait_duration"},
			{Key: "signal-to-expired", From: "wait_signal", Outcome: "expired", To: "end_expired"},
			{Key: "duration-to-end", From: "wait_duration", Outcome: "elapsed", To: "end"},
		},
	}
}

// TestGetRunDetail_SQLite_ApprovalsAndWaits_ListedInActivationOrder proves
// the order contract — "by creation/activation order (stable,
// deterministic)" — against real SQLite, where the repositories' own
// ORDER BY is a random-UUID `id`. With descendingIDs the raw repository
// order is the exact REVERSE of activation order (asserted first, so this
// test can never silently stop discriminating); GetRunDetail must still list
// first_gate before second_gate and wait_signal before wait_duration. It also
// covers the second WAIT mode (DURATION: no signalName, a deadline) and the
// omit-when-empty requestedEvidenceKinds.
func TestGetRunDetail_SQLite_ApprovalsAndWaits_ListedInActivationOrder(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "agentkit-rundetail-decisions-order.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	uow := sqlite.NewUnitOfWork(store)
	fixtureIDs := idsource.NewSequential("id")
	ids := &descendingIDs{}

	root := readyFixtureSQLite(t, uow, fixtureIDs, "project-1", "repo-1")
	version := publishWorkflowVersionDocument(t, uow, "project-1", "wf-def-1", "wf-v-1", approvalsAndWaitsChainDocument())
	startCmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	startResult, err := runtime.StartWorkflowRun(ctx, uow, fixtureIDs, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}

	if _, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: startResult.NodeRunID}); err != nil {
		t.Fatalf("AdvanceRun (start->first_gate): %v", err)
	}
	approvalID := func(nodeKey string) string {
		t.Helper()
		for _, a := range mustGetRunDetail(t, uow, startResult.RunID).ApprovalRequests {
			if a.NodeKey == nodeKey {
				return a.ApprovalRequestID
			}
		}
		t.Fatalf("no approval request for node %q", nodeKey)
		return ""
	}
	waitID := func(nodeKey string) string {
		t.Helper()
		for _, w := range mustGetRunDetail(t, uow, startResult.RunID).WaitRegistrations {
			if w.NodeKey == nodeKey {
				return w.WaitRegistrationID
			}
		}
		t.Fatalf("no wait registration for node %q", nodeKey)
		return ""
	}

	if _, err := runtime.ResolveApproval(ctx, uow, ids, operatorCommand("idem-r1", "hash-r1", "ResolveApproval"), runtime.ResolveApprovalRequest{
		RunID: startResult.RunID, ApprovalRequestID: approvalID("first_gate"), Outcome: "approved",
	}); err != nil {
		t.Fatalf("ResolveApproval(first_gate): %v", err)
	}
	if _, err := runtime.ResolveApproval(ctx, uow, ids, operatorCommand("idem-r2", "hash-r2", "ResolveApproval"), runtime.ResolveApprovalRequest{
		RunID: startResult.RunID, ApprovalRequestID: approvalID("second_gate"), Outcome: "approved",
	}); err != nil {
		t.Fatalf("ResolveApproval(second_gate): %v", err)
	}
	if _, err := runtime.SignalWait(ctx, uow, ids, operatorCommand("idem-s1", "hash-s1", "SignalWait"), runtime.SignalWaitRequest{
		RunID: startResult.RunID, WaitRegistrationID: waitID("wait_signal"), SignalKey: "release-1",
	}); err != nil {
		t.Fatalf("SignalWait(wait_signal): %v", err)
	}

	// Precondition: the repositories' own order is the REVERSE of activation
	// order, so the assertions below can only pass via GetRunDetail's sort.
	var rawApprovals []runtimedomain.ApprovalRequest
	var rawWaits []runtimedomain.WaitRegistration
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		if rawApprovals, err = tx.Approvals().ListApprovalRequestsForRun(ctx, startResult.RunID); err != nil {
			return err
		}
		rawWaits, err = tx.Wait().ListWaitRegistrationsForRun(ctx, startResult.RunID)
		return err
	}); err != nil {
		t.Fatalf("list raw rows: %v", err)
	}
	if len(rawApprovals) != 2 || rawApprovals[0].NodeKey != "second_gate" {
		t.Fatalf("raw approval order = %+v, want second_gate FIRST (test precondition: repository order is reverse of activation order)", rawApprovals)
	}
	if len(rawWaits) != 2 || rawWaits[0].NodeKey != "wait_duration" {
		t.Fatalf("raw wait order = %+v, want wait_duration FIRST (test precondition: repository order is reverse of activation order)", rawWaits)
	}

	detail := mustGetRunDetail(t, uow, startResult.RunID)
	if len(detail.ApprovalRequests) != 2 || detail.ApprovalRequests[0].NodeKey != "first_gate" || detail.ApprovalRequests[1].NodeKey != "second_gate" {
		t.Fatalf("ApprovalRequests = %+v, want [first_gate, second_gate] in activation order", detail.ApprovalRequests)
	}
	if len(detail.WaitRegistrations) != 2 || detail.WaitRegistrations[0].NodeKey != "wait_signal" || detail.WaitRegistrations[1].NodeKey != "wait_duration" {
		t.Fatalf("WaitRegistrations = %+v, want [wait_signal, wait_duration] in activation order", detail.WaitRegistrations)
	}

	first, second := detail.ApprovalRequests[0], detail.ApprovalRequests[1]
	if first.State != string(runtimedomain.ApprovalRequestDecided) || second.State != string(runtimedomain.ApprovalRequestDecided) {
		t.Fatalf("approvals = %+v, want both DECIDED", detail.ApprovalRequests)
	}
	if len(second.AuthorizedRoles) != 2 || second.AuthorizedRoles[0] != "operator" || second.AuthorizedRoles[1] != "reviewer" {
		t.Fatalf("second.AuthorizedRoles = %v, want [operator reviewer]", second.AuthorizedRoles)
	}
	if second.RequestedEvidenceKinds != nil {
		t.Fatalf("second.RequestedEvidenceKinds = %v, want nil (omitted) — the node declares none", second.RequestedEvidenceKinds)
	}

	signalWait, durationWait := detail.WaitRegistrations[0], detail.WaitRegistrations[1]
	if signalWait.Mode != "SIGNAL" || signalWait.SignalName != "release-approved-externally" || signalWait.DueAt == nil ||
		signalWait.State != string(runtimedomain.WaitRegistrationConsumed) {
		t.Fatalf("signalWait = %+v, want Mode=SIGNAL SignalName=release-approved-externally with a DueAt (1800s ceiling), CONSUMED", signalWait)
	}
	if durationWait.Mode != "DURATION" || durationWait.SignalName != "" || durationWait.DueAt == nil ||
		durationWait.State != string(runtimedomain.WaitRegistrationActive) {
		t.Fatalf("durationWait = %+v, want Mode=DURATION, no SignalName, a DueAt, ACTIVE", durationWait)
	}
}
