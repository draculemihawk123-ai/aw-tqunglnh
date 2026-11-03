package runtime_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// V12-03 (UX-15, chochuyennha P1-01): Claude said "You've hit your 5-hour limit · resets …" in the middle of `fix`; the attempt
// and the run FAILED, and the rerun began again at `repro`. Now the node waits BLOCKED and the same node resumes.
func TestProviderQuotaExceeded_BlocksTheNodeAndResumesTheSameNode(t *testing.T) {
	uow, ids, runID, nodeRunID, attemptID, registry := admissionFixture(t, admissionFixtureOptions{})
	ctx := context.Background()
	job := claimableExecuteNodeJob(t, uow, attemptID)

	resetAt := time.Now().Add(3 * time.Hour).UTC()
	executor := &fake.NodeExecutor{Result: ports.NodeExecutionResult{
		State: runtimedomain.ExecutionAttemptBlocked, TerminationReason: runtimedomain.TerminationReasonProviderQuotaExceeded,
		BlockedDetail: "You've hit your 5-hour limit · resets 3pm", ResumeAt: &resetAt,
	}}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{}, fake.IsolationEnforcementChecker{}, registry, nil)
	if err := handler.Handle(ctx, job); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	assertBlockedReason(t, uow, attemptID, nodeRunID, runtimedomain.TerminationReasonProviderQuotaExceeded, workdomain.BlockerProviderQuotaExceeded)

	var run runtimedomain.WorkflowRun
	var blockers []workdomain.WorkItemBlocker
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		if run, err = tx.Runtime().GetWorkflowRun(ctx, runID); err != nil {
			return err
		}
		blockers, err = tx.Work().ListWorkItemBlockersForWorkItem(ctx, string(run.WorkItemID))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if run.State != runtimedomain.WorkflowRunRunning && run.State != runtimedomain.WorkflowRunWaiting {
		t.Fatalf("run = %s, want it alive — a usage limit must not fail the run", run.State)
	}
	if len(blockers) != 1 || !strings.Contains(blockers[0].Reason, "5-hour limit") || !strings.Contains(blockers[0].Reason, "resumes this node by itself") {
		t.Fatalf("blockers = %+v, want one with the provider's words and the resume time", blockers)
	}

	var resume *ports.EnqueueJobRequest
	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	for i := range jobs {
		if jobs[i].Kind == runtime.ProviderQuotaResumeJobKind {
			resume = &jobs[i]
		}
	}
	if resume == nil {
		t.Fatalf("no %s job enqueued", runtime.ProviderQuotaResumeJobKind)
	}
	if resume.AvailableAt.Before(resetAt) || resume.AvailableAt.After(resetAt.Add(5*time.Minute)) || resume.RunID != runID {
		t.Fatalf("resume job available at %v (run %q), want just after the reset %v", resume.AvailableAt, resume.RunID, resetAt)
	}

	// The reset time passes: the resume job retries the SAME node as a new activation and resolves the blocker.
	resumeHandler := runtime.NewProviderQuotaResumeHandler(runtime.NewRetryBlockedActivationHandler(uow, ids, fake.IsolationEnforcementChecker{}, registry))
	if err := resumeHandler.Handle(ctx, ports.DurableJob{ID: resume.ID, Kind: resume.Kind, Payload: resume.Payload}); err != nil {
		t.Fatalf("resume Handle: %v", err)
	}
	var nodeRuns []runtimedomain.NodeRun
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		if nodeRuns, err = tx.Runtime().ListNodeRunsForRun(ctx, runID); err != nil {
			return err
		}
		blockers, err = tx.Work().ListWorkItemBlockersForWorkItem(ctx, string(run.WorkItemID))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var blockedKey string
	for _, nr := range nodeRuns {
		if string(nr.ID) == nodeRunID {
			blockedKey = nr.NodeKey
		}
	}
	reactivated := 0
	for _, nr := range nodeRuns {
		if string(nr.ID) != nodeRunID && nr.NodeKey == blockedKey {
			reactivated++
		}
	}
	if reactivated != 1 {
		t.Fatalf("new activations of %s = %d, want 1 (the same node, not START)", blockedKey, reactivated)
	}
	if blockers[0].State != workdomain.BlockerResolved || blockers[0].ResolvedBy != runtime.QuotaResumeActor {
		t.Fatalf("blocker = %+v, want RESOLVED by %s", blockers[0], runtime.QuotaResumeActor)
	}

	// A second delivery of the same job (or the operator was faster) is a no-op.
	if err := resumeHandler.Handle(ctx, ports.DurableJob{ID: resume.ID, Kind: resume.Kind, Payload: resume.Payload}); err != nil {
		t.Fatalf("second resume Handle: %v", err)
	}
}

func TestParseQuotaReset(t *testing.T) {
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Skip("no tz database")
	}
	now := time.Date(2026, 11, 3, 14, 10, 0, 0, time.UTC) // 15:10 in Paris
	cases := []struct {
		message string
		want    time.Time
		ok      bool
	}{
		{"You've hit your 5-hour limit · resets 5pm (Europe/Paris)", time.Date(2026, 11, 3, 17, 0, 0, 0, paris), true},
		{"You've hit your limit · resets 3pm (Europe/Paris)", time.Date(2026, 11, 4, 15, 0, 0, 0, paris), true}, // 15:00 has passed
		{"Session limit reached ∙ resets 2:30am", time.Date(2026, 11, 4, 2, 30, 0, 0, time.UTC), true},
		{"usage limit, reset at 21:05", time.Date(2026, 11, 3, 21, 5, 0, 0, time.UTC), true},
		{"resets 12am", time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC), true},
		{"Credit balance is too low", time.Time{}, false},
		{"resets 5", time.Time{}, false},
		{"resets 13pm", time.Time{}, false},
	}
	for _, c := range cases {
		got, ok := runtime.ParseQuotaReset(c.message, now)
		if ok != c.ok || (ok && !got.Equal(c.want)) {
			t.Errorf("ParseQuotaReset(%q) = %v, %v; want %v, %v", c.message, got, ok, c.want, c.ok)
		}
	}
}

// V12-04 (UX-07): the Claude session expired; the attempt used to fail the run. Now the node waits BLOCKED behind
// PROVIDER_LOGIN_REQUIRED, with no automatic resume, and a retry after signing in runs the same node.
func TestProviderLoginRequired_BlocksTheNodeWithoutAutomaticResume(t *testing.T) {
	uow, ids, runID, nodeRunID, attemptID, registry := admissionFixture(t, admissionFixtureOptions{})
	ctx := context.Background()
	job := claimableExecuteNodeJob(t, uow, attemptID)
	executor := &fake.NodeExecutor{Result: ports.NodeExecutionResult{
		State: runtimedomain.ExecutionAttemptBlocked, TerminationReason: runtimedomain.TerminationReasonProviderLoginRequired,
		BlockedDetail: "OAuth token has expired · Please run /login",
	}}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{}, fake.IsolationEnforcementChecker{}, registry, nil)
	if err := handler.Handle(ctx, job); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	assertBlockedReason(t, uow, attemptID, nodeRunID, runtimedomain.TerminationReasonProviderLoginRequired, workdomain.BlockerProviderLoginRequired)
	for _, j := range uow.Snapshot.Jobs().(*fake.JobsRepository).Items() {
		if j.Kind == runtime.ProviderQuotaResumeJobKind {
			t.Fatal("a lost sign-in must not schedule an automatic resume")
		}
	}
	var run runtimedomain.WorkflowRun
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		run, err = tx.Runtime().GetWorkflowRun(ctx, runID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if run.State != runtimedomain.WorkflowRunRunning && run.State != runtimedomain.WorkflowRunWaiting {
		t.Fatalf("run = %s, want alive", run.State)
	}
	result, err := runtime.NewRetryBlockedActivationHandler(uow, ids, fake.IsolationEnforcementChecker{}, registry).Retry(ctx,
		runtime.RetryBlockedActivationRequest{NodeRunID: nodeRunID, Actor: "operator-1", Reason: "signed in again"})
	if err != nil || !result.Retried {
		t.Fatalf("Retry = %+v, %v; want Retried", result, err)
	}
}
