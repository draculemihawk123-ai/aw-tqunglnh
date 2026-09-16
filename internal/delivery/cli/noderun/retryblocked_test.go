package noderun_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/agentregistry"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	clinoderun "github.com/taQuangLing/agent-workflow/internal/delivery/cli/noderun"
)

func newNodeRunDeps(t *testing.T, isolation fake.IsolationEnforcementChecker) (clinoderun.Dependencies, idsource.Source) {
	t.Helper()
	_, uow := openNodeRunCLITestStore(t, "noderun-cli.db")
	ids := idsource.NewSequential("cli-noderun")
	return clinoderun.Dependencies{UOW: uow, IDs: ids, Isolation: isolation, Agents: agentregistry.Empty()}, ids
}

// TestNodeRunRetryBlocked_RevalidationPasses_ReactivatesNodeRun is this
// task's own "recovery" Verify bullet: retry-blocked actually unblocks a
// blocked node-run in a test scenario. fake.IsolationEnforcementChecker{}
// (zero value, Err=nil) reports every tier enforceable, so the SAME
// isolation tier the blocked fixture pinned now passes on revalidation —
// the exact "still-live Run, admission now clean" case
// RetryBlockedActivationHandler.Retry exists to handle.
func TestNodeRunRetryBlocked_RevalidationPasses_ReactivatesNodeRun(t *testing.T) {
	deps, ids := newNodeRunDeps(t, fake.IsolationEnforcementChecker{})
	root := readyWorkItemFixture(t, deps.UOW, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, deps.UOW, "project-1", "wf-def-1", "wf-v-1", workflowDocumentV1())
	run := startTestRun(t, deps.UOW, ids, "project-1", root.WorkItemID, string(version.ID()))
	nodeRunID := blockedAdmissionNodeRunFixture(t, deps.UOW, "project-1", run, "OPERATOR_TRUSTED_LOCAL")

	var stdout, stderr bytes.Buffer
	if err := clinoderun.RetryBlocked(context.Background(), deps, []string{"--reason", "retry after fix", nodeRunID}, &stdout, &stderr); err != nil {
		t.Fatalf("RetryBlocked: %v, stderr=%s", err, stderr.String())
	}
	var result clinoderun.RetryBlockedResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode RetryBlockedResult: %v\nstdout=%s", err, stdout.String())
	}
	if result.NodeRunID != nodeRunID {
		t.Fatalf("NodeRunID = %q, want %q", result.NodeRunID, nodeRunID)
	}
	if result.AlreadyRetried {
		t.Fatal("AlreadyRetried = true on the first genuine retry, want false")
	}
	if !result.Retried {
		t.Fatalf("Retried = false, want true (isolation now enforceable, nothing else pinned) — result: %+v", result)
	}
	if result.ReactivatedNodeRunID == "" {
		t.Fatal("ReactivatedNodeRunID is empty even though Retried=true")
	}
	if result.FailureReason != "" {
		t.Fatalf("FailureReason = %q, want empty on a successful retry", result.FailureReason)
	}

	// A second call for the same blocked NodeRun is now a safe, idempotent
	// no-op — the admission blocker was RESOLVED by the first call, so
	// this must report AlreadyRetried=true, never a second reactivation.
	var stdout2, stderr2 bytes.Buffer
	if err := clinoderun.RetryBlocked(context.Background(), deps, []string{"--reason", "retry again", nodeRunID}, &stdout2, &stderr2); err != nil {
		t.Fatalf("second RetryBlocked: %v, stderr=%s", err, stderr2.String())
	}
	var result2 clinoderun.RetryBlockedResult
	if err := json.Unmarshal(stdout2.Bytes(), &result2); err != nil {
		t.Fatalf("decode second RetryBlockedResult: %v", err)
	}
	if !result2.AlreadyRetried {
		t.Fatal("second RetryBlocked reported AlreadyRetried=false, want true")
	}
	if result2.Retried {
		t.Fatal("second RetryBlocked reported Retried=true, want false (must not reactivate twice)")
	}
	if result2.ReactivatedNodeRunID != "" {
		t.Fatalf("second RetryBlocked ReactivatedNodeRunID = %q, want empty", result2.ReactivatedNodeRunID)
	}
}

// TestNodeRunRetryBlocked_RevalidationStillFails_BlockerStaysOpen proves
// the OTHER real outcome retry_blocked_activation.go's own doc comment
// names: a revalidation that still fails is a normal, structured business
// result (FailureReason/FailureDetail populated), never an error — and the
// existing blocker is left OPEN, unresolved, untouched (this task's own
// "Không làm" line: "resolve open blocker hợp lệ... revalidation thất bại
// giữ nguyên blocker hiện tại").
func TestNodeRunRetryBlocked_RevalidationStillFails_BlockerStaysOpen(t *testing.T) {
	deps, ids := newNodeRunDeps(t, fake.IsolationEnforcementChecker{Err: errors.New("still no real OS enforcement")})
	root := readyWorkItemFixture(t, deps.UOW, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, deps.UOW, "project-1", "wf-def-1", "wf-v-1", workflowDocumentV1())
	run := startTestRun(t, deps.UOW, ids, "project-1", root.WorkItemID, string(version.ID()))
	nodeRunID := blockedAdmissionNodeRunFixture(t, deps.UOW, "project-1", run, "OPERATOR_TRUSTED_LOCAL")

	var stdout, stderr bytes.Buffer
	if err := clinoderun.RetryBlocked(context.Background(), deps, []string{"--reason", "retry, still broken", nodeRunID}, &stdout, &stderr); err != nil {
		t.Fatalf("RetryBlocked: %v, stderr=%s", err, stderr.String())
	}
	var result clinoderun.RetryBlockedResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode RetryBlockedResult: %v\nstdout=%s", err, stdout.String())
	}
	if result.Retried {
		t.Fatal("Retried = true, want false (isolation is still not enforceable)")
	}
	if result.AlreadyRetried {
		t.Fatal("AlreadyRetried = true, want false (this is a genuine still-failing revalidation, not a duplicate call)")
	}
	if result.FailureReason == "" {
		t.Fatal("FailureReason is empty, want ISOLATION_ENFORCEMENT_UNAVAILABLE (or similar) — a still-failing retry must report a structured reason")
	}
	if result.ReactivatedNodeRunID != "" {
		t.Fatalf("ReactivatedNodeRunID = %q, want empty (nothing was reactivated)", result.ReactivatedNodeRunID)
	}

	// A follow-up call against the SAME still-broken environment reaches
	// the identical still-failing outcome again (the blocker is still
	// OPEN, so this is a fresh evaluation each time, not a replay) —
	// proving the blocker was never silently resolved by the first call.
	var stdout2, stderr2 bytes.Buffer
	if err := clinoderun.RetryBlocked(context.Background(), deps, []string{"--reason", "retry once more", nodeRunID}, &stdout2, &stderr2); err != nil {
		t.Fatalf("second RetryBlocked: %v, stderr=%s", err, stderr2.String())
	}
	var result2 clinoderun.RetryBlockedResult
	if err := json.Unmarshal(stdout2.Bytes(), &result2); err != nil {
		t.Fatalf("decode second RetryBlockedResult: %v", err)
	}
	if result2.Retried || result2.AlreadyRetried {
		t.Fatalf("second RetryBlocked = %+v, want a fresh still-failing evaluation (Retried=false, AlreadyRetried=false)", result2)
	}
	if result2.FailureReason == "" {
		t.Fatal("second RetryBlocked FailureReason is empty, want the blocker to still be OPEN and re-evaluated")
	}
}

func TestNodeRunRetryBlocked_MissingReason(t *testing.T) {
	deps, ids := newNodeRunDeps(t, fake.IsolationEnforcementChecker{})
	root := readyWorkItemFixture(t, deps.UOW, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, deps.UOW, "project-1", "wf-def-1", "wf-v-1", workflowDocumentV1())
	run := startTestRun(t, deps.UOW, ids, "project-1", root.WorkItemID, string(version.ID()))
	nodeRunID := blockedAdmissionNodeRunFixture(t, deps.UOW, "project-1", run, "OPERATOR_TRUSTED_LOCAL")

	var stdout, stderr bytes.Buffer
	err := clinoderun.RetryBlocked(context.Background(), deps, []string{nodeRunID}, &stdout, &stderr)
	if err == nil {
		t.Fatal("RetryBlocked with no --reason succeeded, want a usage error")
	}
}

func TestNodeRunRetryBlocked_UnknownNodeRunID(t *testing.T) {
	deps, _ := newNodeRunDeps(t, fake.IsolationEnforcementChecker{})
	var stdout, stderr bytes.Buffer
	err := clinoderun.RetryBlocked(context.Background(), deps, []string{"--reason", "x", "no-such-node-run"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("RetryBlocked with an unknown nodeRunId succeeded, want an error")
	}
}
