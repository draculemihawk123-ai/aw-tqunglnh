package runtime_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

// The orchestrator is the only production caller of EvaluateCompletionCandidate;
// before it, nothing moved a VERIFYING run on, so no run ever completed.

func newOrchestrator(uow ports.UnitOfWork, ids idsource.Source) *runtime.CompletionOrchestrator {
	return runtime.NewCompletionOrchestrator(uow, ids, clock.System{})
}

func assertRunAndWorkItem(t *testing.T, uow *fake.UnitOfWork, run runtimedomain.WorkflowRun, wantRun runtimedomain.WorkflowRunState, wantItem workdomain.WorkItemStatus) {
	t.Helper()
	ctx := context.Background()
	gotRun, err := uow.Snapshot.Runtime().GetWorkflowRun(ctx, string(run.ID))
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	if gotRun.State != wantRun {
		t.Errorf("run.State = %s, want %s", gotRun.State, wantRun)
	}
	item, err := uow.Snapshot.Work().GetWorkItem(ctx, string(run.WorkItemID))
	if err != nil {
		t.Fatalf("GetWorkItem: %v", err)
	}
	if item.Status != wantItem {
		t.Errorf("work item status = %s, want %s", item.Status, wantItem)
	}
}

func TestCompletionOrchestrator_Sweep_DecidesAVerifyingRun(t *testing.T) {
	ctx := context.Background()
	doc := requiredEvidenceCompletionPolicy()
	uow, ids, run := completionCandidateFixture(t, workflowDocumentV1(), &doc)
	seedRunEvidence(t, uow, run, "implement", "TEST_RESULT", runtimedomain.EvidenceVerdictSucceeded)

	report, err := newOrchestrator(uow, ids).Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report.Evaluated != 1 || report.Outcomes[runtime.CompletionOutcomePass] != 1 {
		t.Fatalf("report = %+v, want exactly one PASS", report)
	}
	assertRunAndWorkItem(t, uow, run, runtimedomain.WorkflowRunSucceeded, workdomain.WorkItemDone)

	decided := false
	for _, e := range uow.Snapshot.Events().(*fake.EventsRepository).Items() {
		if e.EventType == runtime.CompletionDecidedEventType && e.AggregateID == string(run.ID) {
			decided = true
		}
	}
	if !decided {
		t.Error("no COMPLETION_DECIDED event recorded for the run")
	}
}

func TestCompletionOrchestrator_Sweep_DecidesEachCandidateOnce(t *testing.T) {
	ctx := context.Background()
	doc := requiredEvidenceCompletionPolicy()
	uow, ids, run := completionCandidateFixture(t, workflowDocumentV1(), &doc)
	seedRunEvidence(t, uow, run, "implement", "TEST_RESULT", runtimedomain.EvidenceVerdictSucceeded)
	orchestrator := newOrchestrator(uow, ids)

	if _, err := orchestrator.Sweep(ctx); err != nil {
		t.Fatalf("first Sweep: %v", err)
	}
	report, err := orchestrator.Sweep(ctx)
	if err != nil {
		t.Fatalf("second Sweep: %v", err)
	}
	if report.Evaluated != 0 {
		t.Fatalf("second sweep evaluated %d runs, want 0 — a decided run must not be decided again", report.Evaluated)
	}
	assertRunAndWorkItem(t, uow, run, runtimedomain.WorkflowRunSucceeded, workdomain.WorkItemDone)
}

func TestCompletionOrchestrator_Sweep_RecordsABlockWhenRequirementsAreUnmet(t *testing.T) {
	ctx := context.Background()
	doc := requiredEvidenceCompletionPolicy()
	uow, ids, run := completionCandidateFixture(t, workflowDocumentV1(), &doc)
	// No evidence at all: the policy's required TEST_RESULT is missing.

	report, err := newOrchestrator(uow, ids).Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report.Outcomes[runtime.CompletionOutcomeBlock] != 1 {
		t.Fatalf("report = %+v, want exactly one BLOCK", report)
	}
	assertRunAndWorkItem(t, uow, run, runtimedomain.WorkflowRunBlocked, workdomain.WorkItemBlocked)
}

func TestCompletionOrchestrator_Sweep_IgnoresRunsThatAreNotVerifying(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, _ := startWorkflowRunFixture(t, workflowDocumentV1())

	report, err := newOrchestrator(uow, ids).Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report.Evaluated != 0 {
		t.Fatalf("report = %+v, want nothing evaluated for a run that has not reached END", report)
	}
	run, err := uow.Snapshot.Runtime().GetWorkflowRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	if run.State == runtimedomain.WorkflowRunSucceeded || run.State == runtimedomain.WorkflowRunBlocked || run.State == runtimedomain.WorkflowRunFailed {
		t.Fatalf("run.State = %s, want the run left alone", run.State)
	}
}

func TestCompletionOrchestrator_Sweep_ReportsTheNodeRunAReworkDecisionCreated(t *testing.T) {
	ctx := context.Background()
	doc := requiredEvidenceCompletionPolicy()
	uow, ids, run := completionCandidateFixture(t, documentWithReworkEdge(3), &doc)
	// No evidence: the ladder is unsatisfied but an under-budget rework edge
	// exists, so the decision is REWORK.

	report, err := newOrchestrator(uow, ids).Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report.Outcomes[runtime.CompletionOutcomeRework] != 1 || len(report.ReworkNodeRunIDs) != 1 {
		t.Fatalf("report = %+v, want one REWORK naming the node run it created", report)
	}
	nodeRun, err := uow.Snapshot.Runtime().GetNodeRun(ctx, report.ReworkNodeRunIDs[0])
	if err != nil {
		t.Fatalf("GetNodeRun: %v", err)
	}
	if nodeRun.State != runtimedomain.NodeRunPending {
		t.Errorf("reworked node run state = %s, want PENDING", nodeRun.State)
	}
	assertRunAndWorkItem(t, uow, run, runtimedomain.WorkflowRunRunning, workdomain.WorkItemActive)
}

// interferingUOW lets a test change the world between the sweep's read of
// the candidates and its decision — the window a cancel or a second sweeper
// can land in. The hook fires once, after the first read-only transaction.
type interferingUOW struct {
	ports.UnitOfWork
	afterRead func()
}

func (u *interferingUOW) WithReadOnly(ctx context.Context, fn func(ports.Tx) error) error {
	err := u.UnitOfWork.WithReadOnly(ctx, fn)
	if hook := u.afterRead; hook != nil {
		u.afterRead = nil
		hook()
	}
	return err
}

func countCompletionDecidedEvents(uow *fake.UnitOfWork, runID string) int {
	count := 0
	for _, e := range uow.Snapshot.Events().(*fake.EventsRepository).Items() {
		if e.EventType == runtime.CompletionDecidedEventType && e.AggregateID == runID {
			count++
		}
	}
	return count
}

// EvaluateCompletionCandidate allows exactly one immutable decision per
// candidate and replays it for any later caller, so two sweepers racing on the
// same run can never disagree or decide twice.
func TestCompletionOrchestrator_Sweep_TwoSweepersDecideACandidateOnce(t *testing.T) {
	ctx := context.Background()
	doc := requiredEvidenceCompletionPolicy()
	uow, ids, run := completionCandidateFixture(t, workflowDocumentV1(), &doc)
	seedRunEvidence(t, uow, run, "implement", "TEST_RESULT", runtimedomain.EvidenceVerdictSucceeded)

	interfered := false
	wrapped := &interferingUOW{UnitOfWork: uow}
	wrapped.afterRead = func() {
		interfered = true
		cmd := evaluateCompletionCandidateCmd("idem-other-sweeper")
		cmd.ExpectedVersion = run.Version
		if _, err := runtime.EvaluateCompletionCandidate(ctx, uow, ids, clock.System{}, cmd, runtime.EvaluateCompletionCandidateRequest{RunID: string(run.ID)}); err != nil {
			t.Errorf("the other sweeper's EvaluateCompletionCandidate: %v", err)
		}
	}

	report, err := newOrchestrator(wrapped, ids).Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if !interfered {
		t.Fatal("the interference hook never ran, so this test proved nothing")
	}
	if report.Outcomes[runtime.CompletionOutcomePass] != 1 {
		t.Errorf("report = %+v, want the other sweeper's PASS replayed", report)
	}
	if got := countCompletionDecidedEvents(uow, string(run.ID)); got != 1 {
		t.Errorf("COMPLETION_DECIDED events = %d, want exactly 1 — a candidate must be decided once", got)
	}
	assertRunAndWorkItem(t, uow, run, runtimedomain.WorkflowRunSucceeded, workdomain.WorkItemDone)
}

func TestCompletionOrchestrator_Sweep_IgnoresARunCancelledMidSweep(t *testing.T) {
	ctx := context.Background()
	doc := requiredEvidenceCompletionPolicy()
	uow, ids, run := completionCandidateFixture(t, workflowDocumentV1(), &doc)
	seedRunEvidence(t, uow, run, "implement", "TEST_RESULT", runtimedomain.EvidenceVerdictSucceeded)

	interfered := false
	wrapped := &interferingUOW{UnitOfWork: uow}
	wrapped.afterRead = func() {
		interfered = true
		if _, err := runtime.CancelRun(ctx, uow, ids, runtime.CancelRunRequest{RunID: string(run.ID), Actor: "actor-1", Reason: "cancelled mid-sweep"}); err != nil {
			t.Errorf("CancelRun: %v", err)
		}
	}

	report, err := newOrchestrator(wrapped, ids).Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep returned an error for a run that was cancelled after the scan: %v", err)
	}
	if !interfered {
		t.Fatal("the interference hook never ran, so this test proved nothing")
	}
	if report.Evaluated != 0 {
		t.Errorf("report = %+v, want nothing decided for a cancelled run", report)
	}
	if got := countCompletionDecidedEvents(uow, string(run.ID)); got != 0 {
		t.Errorf("COMPLETION_DECIDED events = %d, want none for a cancelled run", got)
	}
	item, err := uow.Snapshot.Work().GetWorkItem(ctx, string(run.WorkItemID))
	if err != nil {
		t.Fatalf("GetWorkItem: %v", err)
	}
	if item.Status == workdomain.WorkItemDone {
		t.Errorf("work item status = %s, a cancelled run must never complete its work item", item.Status)
	}
}

// The fake repositories and the SQLite ones must agree on the queries Sweep
// scans with (ListProjects, ListWorkItemsByProject, ListWorkflowRunsForWorkItem),
// and a decision must survive a real close and reopen. With no completion
// policy pinned the decision is FAIL (ADR-021), which exercises the same path
// as any other outcome.
func TestCompletionOrchestrator_SQLite_DecidesAVerifyingRunAndItSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agentkit-completion-orchestrator.db")
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")

	root := readyFixtureSQLite(t, uow, ids, "project-1", "repo-1")
	version := publishWorkflowVersionDocument(t, uow, "project-1", "wf-def-1", "wf-v-1", workflowDocumentV1())
	started, err := runtime.StartWorkflowRun(ctx, uow, ids, testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun"), runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	if _, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: started.RunID, NodeRunID: started.NodeRunID}); err != nil {
		t.Fatalf("AdvanceRun (start->end): %v", err)
	}

	report, err := runtime.NewCompletionOrchestrator(uow, ids, clock.System{}).Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report.Evaluated != 1 || report.Outcomes[runtime.CompletionOutcomeFail] != 1 {
		t.Fatalf("report = %+v, want exactly one FAIL (no completion policy pinned)", report)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("close before restart: %v", err)
	}
	reopened, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen after restart: %v", err)
	}
	defer reopened.Close()
	reopenedUow := sqlite.NewUnitOfWork(reopened)

	if err := reopenedUow.WithReadOnly(ctx, func(tx ports.Tx) error {
		run, err := tx.Runtime().GetWorkflowRun(ctx, started.RunID)
		if err != nil {
			return err
		}
		if run.State != runtimedomain.WorkflowRunFailed {
			t.Errorf("run state after restart = %s, want FAILED", run.State)
		}
		item, err := tx.Work().GetWorkItem(ctx, root.WorkItemID)
		if err != nil {
			return err
		}
		if item.Status != workdomain.WorkItemBlocked {
			t.Errorf("work item status after restart = %s, want BLOCKED", item.Status)
		}
		return nil
	}); err != nil {
		t.Fatalf("read after restart: %v", err)
	}

	again, err := runtime.NewCompletionOrchestrator(reopenedUow, ids, clock.System{}).Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep after restart: %v", err)
	}
	if again.Evaluated != 0 {
		t.Errorf("sweep after restart evaluated %d runs, want 0 — the decision must not repeat", again.Evaluated)
	}
}
