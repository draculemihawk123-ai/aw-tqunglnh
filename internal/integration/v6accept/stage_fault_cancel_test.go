package v6accept

import (
	"net/http"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// waitSignalOnlyWorkflowDocument is START -> WAIT(SIGNAL, never fired) ->
// END — a graph whose one NodeRun sits durably WAITING (a real
// WaitRegistration row) until either the signal arrives, the timeout
// fires, or (this scenario's own subject) the Run is cancelled out from
// under it. timeoutSeconds is set generously long so it never fires
// before this scenario's own cancel does.
func waitSignalOnlyWorkflowDocument(timeoutSeconds uint32) workflow.WorkflowDocument {
	return workflow.WorkflowDocument{
		SchemaVersion: "1",
		Nodes: []workflow.Node{
			{Key: "start", Type: workflow.NodeStart, Outcomes: []string{"next"}},
			{Key: "pause", Type: workflow.NodeWait, Outcomes: []string{"resumed", "expired"}, Wait: &workflow.WaitNodeConfig{
				Mode: workflow.WaitModeSignal, SignalName: "never-sent", TimeoutSeconds: timeoutSeconds,
				CompletionOutcome: "resumed", TimeoutOutcome: "expired",
			}},
			{Key: "end_resumed", Type: workflow.NodeEnd},
			{Key: "end_expired", Type: workflow.NodeEnd},
		},
		Edges: []workflow.Edge{
			{Key: "start-to-pause", From: "start", Outcome: "next", To: "pause"},
			{Key: "pause-to-end-resumed", From: "pause", Outcome: "resumed", To: "end_resumed"},
			{Key: "pause-to-end-expired", From: "pause", Outcome: "expired", To: "end_expired"},
		},
	}
}

// TestV6HTTPAcceptance_Fault_CancelQuiesceSurvivesWorkerCrash is V6-14A
// scenario 9: "cancel CANCELLING->CANCELLED". ADR-020's own quiesce
// protocol (internal/app/runtime/cancel_run.go's own package doc comment)
// is two parts: CancelRun's own transaction CASes the Run straight to
// CANCELLING and enqueues exactly one CANCEL_RUN_COORDINATOR job, and that
// SEPARATE job (idempotent, safely redeliverable — it re-checks
// run.State==CANCELLING and CASes every row on its own ExpectedState) does
// the actual quiesce sweep asynchronously. This scenario hard-kills the
// worker IMMEDIATELY after observing CANCELLING (the real signal:
// GET /runs/{runId}'s own `state` field, read right after the cancel
// POST itself already returned it) — before the worker's own poll loop
// has had a realistic chance to even claim the coordinator job, let alone
// finish it — so the crash genuinely interrupts quiesce at its earliest
// possible point, the strongest version of "recovery must not get stuck
// mid-quiesce" this black-box suite can force.
//
// Verify: after the worker restarts, the SAME Run converges to CANCELLED
// (never stuck in CANCELLING forever, never silently resurrected as if
// nothing happened). The Run's own NodeRunCount (GET /runs/{runId}) is
// IDENTICAL before and after — an exact-count proof that no NEW NodeRun
// was ever activated once cancellation was requested — and the WAIT
// node's own WaitRegistration (also exposed on run detail) ends CANCELLED,
// not left ACTIVE or silently resumed.
func TestV6HTTPAcceptance_Fault_CancelQuiesceSurvivesWorkerCrash(t *testing.T) {
	requireAcceptance(t)
	j := newFaultStack(t)
	j.createRootWorkItem(t, "fault-cancel-root")

	wf := j.publishDefinition(t, "/projects/"+j.projectID, definition.KindWorkflow, "wait-only-workflow", "wait-only workflow",
		waitSignalOnlyWorkflowDocument(3600))
	childID := j.createChild(t, "fault-cancel-child", "v6-14a cancel-quiesce fixture", wf)
	j.waitWorkspaceReady(t)
	j.markReady(t, childID)
	runID := j.startRun(t, childID, wf.versionID)

	type waitRegistrationView struct {
		WaitRegistrationID string `json:"waitRegistrationId"`
		State              string `json:"state"`
	}
	type runDetailView struct {
		State             string                 `json:"state"`
		NodeRunCount      int                    `json:"nodeRunCount"`
		WaitRegistrations []waitRegistrationView `json:"waitRegistrations"`
	}
	var before runDetailView
	waitFor(t, "the run's WAIT node to reach a real, active WaitRegistration", 30*time.Second, 200*time.Millisecond, func() bool {
		j.s.api.get(t, "/runs/"+runID).requireStatus(t, http.StatusOK).decode(t, &before)
		for _, w := range before.WaitRegistrations {
			if w.State == "ACTIVE" {
				return true
			}
		}
		return false
	})
	if before.State != "RUNNING" {
		t.Fatalf("run state before cancel = %s, want RUNNING", before.State)
	}
	nodeRunCountBefore := before.NodeRunCount

	cancelled := j.s.api.post(t, "/runs/"+runID+"/cancel", map[string]string{"reason": "v6-14a fault: cancel-quiesce crash"}).requireStatus(t, http.StatusOK, http.StatusAccepted)
	var cancelResult struct {
		State string `json:"state"`
	}
	cancelled.decode(t, &cancelResult)
	if cancelResult.State != "CANCELLING" {
		t.Fatalf("cancel response state = %s, want CANCELLING", cancelResult.State)
	}

	// The real crash: hard-kill the worker AS SOON AS this test's own
	// client observed CANCELLING (already true — cancelResult IS that
	// observation), before the worker's own poll loop can realistically
	// have claimed the CANCEL_RUN_COORDINATOR job yet.
	j.s.hardKillWorker(t)
	j.s.startWorker(t)

	var after runDetailView
	waitFor(t, "the restarted worker to converge the crashed cancellation to CANCELLED", 30*time.Second, 200*time.Millisecond, func() bool {
		j.s.api.get(t, "/runs/"+runID).requireStatus(t, http.StatusOK).decode(t, &after)
		return after.State == "CANCELLED"
	})

	if after.NodeRunCount != nodeRunCountBefore {
		t.Fatalf("NodeRunCount changed across cancellation: before=%d after=%d — cancellation must never activate a NEW NodeRun",
			nodeRunCountBefore, after.NodeRunCount)
	}
	if len(after.WaitRegistrations) != 1 {
		t.Fatalf("run has %d WaitRegistrations after cancellation, want exactly 1", len(after.WaitRegistrations))
	}
	if after.WaitRegistrations[0].State != "CANCELLED" {
		t.Fatalf("WaitRegistration state after cancellation = %s, want CANCELLED (never left ACTIVE, never silently RESUMED)", after.WaitRegistrations[0].State)
	}
}
