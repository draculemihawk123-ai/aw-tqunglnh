package runtime_test

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/errorcode"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// V9-06 / ADR-033 (gap G6): a Run that ends FAILED opens a RUN_FAILED blocker,
// the operator resolves it (RESOLVED only), and `run start` runs again on the
// SAME WorkItem with the SAME pinned workflow version. These tests drive that
// loop against a real *sqlite.Store through the real commands — no row is
// written directly except the two documented test-setup shortcuts
// (forcing the WorkItem READY the way readyFixtureSQLite always does, and the
// seeded second Run / quarantined workspace of the precondition tests).

// rerunSQLite is one WorkItem whose contract pins a published AGENT workflow
// version, on a real sqlite store.
type rerunSQLite struct {
	t          *testing.T
	ctx        context.Context
	dbPath     string
	uow        ports.UnitOfWork
	store      *sqlite.Store
	ids        idsource.Source
	workItemID string
	version    workflow.WorkflowVersion
}

func newRerunSQLite(t *testing.T) *rerunSQLite {
	t.Helper()
	return newRerunSQLiteWithCompletion(t, false)
}

// rerunRequiredEvidenceKind is the one evidence kind the completion policy of
// newRerunSQLiteWithCompletion requires.
const rerunRequiredEvidenceKind = "TEST_RESULT"

// newRerunSQLiteWithCompletion is newRerunSQLite whose workflow pins a
// COMPLETION policy requiring a passing rerunRequiredEvidenceKind evidence row,
// so EvaluateCompletionCandidate can decide a run (withCompletion).
func newRerunSQLiteWithCompletion(t *testing.T, withCompletion bool) *rerunSQLite {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agentkit-rerun-work-item.db")
	store, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")

	seedActiveRepositorySQLite(t, uow, ids, "project-1", "repo-1")
	document := agentExecutableDocument("agent-profile-v1", fullyResolvablePolicyRefs(), nil)
	dependencies := workflow.DependencyManifest{Pins: []workflow.DependencyPin{
		{Kind: "skill", Key: "implement", Version: "1", Hash: "sha256:dependency-1"},
	}}
	if withCompletion {
		publishPolicyVersion(t, uow, "completion-policy-def", "completion-policy-v1", policy.PolicyDocument{
			Category:   policy.CategoryCompletion,
			Completion: &policy.CompletionRules{RequiredEvidenceKinds: []string{rerunRequiredEvidenceKind}},
		})
		var compiledHash string
		if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
			fields, err := tx.Definitions().LoadVersion(ctx, "completion-policy-v1")
			compiledHash = fields.CompiledHash()
			return err
		}); err != nil {
			t.Fatalf("load the completion policy version: %v", err)
		}
		document.CompletionPolicyRef = &definition.DependencyPin{
			Kind: definition.KindPolicy, DefinitionID: "completion-policy-def", VersionID: "completion-policy-v1",
		}
		dependencies.Pins = append(dependencies.Pins, workflow.DependencyPin{
			Kind: string(definition.KindPolicy), Key: "completion-policy-def", Version: "completion-policy-v1", Hash: compiledHash,
		})
	}
	version := publishRerunWorkflowVersion(t, uow, document, dependencies)
	publishAgentProfileVersion(t, uow, "agent-profile-def", "agent-profile-v1", validAgentProfileDocument())
	publishPolicyVersion(t, uow, "attempt-policy-def", "attempt-policy-v1", attemptPolicyDocument(600))
	publishPolicyVersion(t, uow, "permission-policy-def", "permission-policy-v1", permissionPolicyDocument())

	// The WorkItem pins the workflow version in its contract, the way an
	// operator creating it with `"contract": {"workflowVersionId": ...}` does:
	// that pin is what every Run of the WorkItem must keep using (ADR-033
	// decision 4).
	root := readyRootWorkItemSQLite(t, uow, ids, "project-1", "repo-1", &work.WorkItemContractRequest{
		SchemaVersion: 1, WorkflowVersionID: string(version.ID()),
	})
	seedEffectiveScope(t, uow, root.WorkItemID, "repo-1")

	return &rerunSQLite{t: t, ctx: ctx, dbPath: dbPath, uow: uow, store: store, ids: ids, workItemID: root.WorkItemID, version: version}
}

func (f *rerunSQLite) startCommand(key string) ports.Command {
	return testCommand(key, "hash-"+key, ports.ProjectScope("project-1"), "StartWorkflowRun")
}

func (f *rerunSQLite) startWith(key, workflowVersionID string) (runtime.StartWorkflowRunResult, error) {
	return runtime.StartWorkflowRun(f.ctx, f.uow, f.ids, f.startCommand(key), runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: f.workItemID, WorkflowVersionID: workflowVersionID,
	})
}

func (f *rerunSQLite) start(key string) (runtime.StartWorkflowRunResult, error) {
	return f.startWith(key, string(f.version.ID()))
}

func (f *rerunSQLite) mustStart(key string) runtime.StartWorkflowRunResult {
	f.t.Helper()
	result, err := f.start(key)
	if err != nil {
		f.t.Fatalf("StartWorkflowRun(%s): %v", key, err)
	}
	return result
}

// rerunAttempt is the RUNNING ExecutionAttempt of a started run's AGENT node.
type rerunAttempt struct {
	runID, nodeRunID, attemptID string
	version                     uint64
}

// runToRunningAttempt drives the freshly started run to a RUNNING attempt of
// its AGENT node through the real commands: AdvanceRun (START -> implement),
// ScheduleExecutableNodeRun, then the claim the EXECUTE_NODE handler would do.
func (f *rerunSQLite) runToRunningAttempt(started runtime.StartWorkflowRunResult) rerunAttempt {
	f.t.Helper()
	ctx := f.ctx
	hop, err := runtime.AdvanceRun(ctx, f.uow, f.ids, runtime.AdvanceRunRequest{RunID: started.RunID, NodeRunID: started.NodeRunID})
	if err != nil {
		f.t.Fatalf("AdvanceRun (start->implement): %v", err)
	}
	scheduled, err := runtime.ScheduleExecutableNodeRun(ctx, f.uow, f.ids, fake.NewRuntimeExecutionConfigProvider(), runtime.ScheduleExecutableNodeRunRequest{
		RunID: started.RunID, NodeRunID: hop.NextNodeRunID, CorrelationID: "corr-1",
	})
	if err != nil {
		f.t.Fatalf("ScheduleExecutableNodeRun: %v", err)
	}

	attempt := rerunAttempt{runID: started.RunID, nodeRunID: hop.NextNodeRunID, attemptID: scheduled.AttemptID}
	if err := f.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		nodeRun, err := tx.Runtime().GetNodeRun(ctx, attempt.nodeRunID)
		if err != nil {
			return err
		}
		if _, err := tx.Runtime().TransitionNodeRun(ctx, ports.TransitionNodeRunRequest{
			NodeRunID: attempt.nodeRunID, ExpectedState: runtimedomain.NodeRunQueued, ExpectedVersion: nodeRun.Version,
			NextState: runtimedomain.NodeRunRunning,
		}); err != nil {
			return err
		}
		queued, err := tx.Runtime().GetExecutionAttempt(ctx, attempt.attemptID)
		if err != nil {
			return err
		}
		running, err := tx.Runtime().TransitionExecutionAttempt(ctx, ports.TransitionExecutionAttemptRequest{
			AttemptID: attempt.attemptID, ExpectedState: runtimedomain.ExecutionAttemptQueued, ExpectedVersion: queued.Version,
			NextState: runtimedomain.ExecutionAttemptRunning,
		})
		attempt.version = running.Version
		return err
	}); err != nil {
		f.t.Fatalf("drive attempt to RUNNING: %v", err)
	}
	return attempt
}

// failRun drives the freshly started run through its AGENT node to a real
// FAILED run: the attempt is finalized FAILED with a failure code the attempt
// policy does not retry, so the NodeRun fails and reconcileRunTerminalityTx
// fails the run — all inside the real transactions. It returns the finalize
// request it issued so a test can replay it.
func (f *rerunSQLite) failRun(started runtime.StartWorkflowRunResult) runtime.FinalizeExecutionAttemptRequest {
	f.t.Helper()
	attempt := f.runToRunningAttempt(started)
	_, lease := claimExecuteNodeJob(f.t, f.ctx, f.store, 5*time.Minute)
	finalize := runtime.FinalizeExecutionAttemptRequest{
		RunID: attempt.runID, NodeRunID: attempt.nodeRunID, AttemptID: attempt.attemptID, ExpectedVersion: attempt.version,
		NextState: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonExecutionFailed,
		FailureCode: errorcode.CodeExecutionFailed, JobLease: lease, CorrelationID: "corr-1",
	}
	if _, err := runtime.FinalizeExecutionAttempt(f.ctx, f.uow, f.ids, clock.System{}, nil, finalize); err != nil {
		f.t.Fatalf("FinalizeExecutionAttempt(FAILED): %v", err)
	}
	if got := f.run(started.RunID).State; got != runtimedomain.WorkflowRunFailed {
		f.t.Fatalf("run %s state = %s, want FAILED", started.RunID, got)
	}
	return finalize
}

// reachVerifying drives the freshly started run through its AGENT node to a
// SUCCEEDED attempt, which advances the run to END and so to VERIFYING (the
// completion candidate). The attempt it returns carries no evidence yet.
func (f *rerunSQLite) reachVerifying(started runtime.StartWorkflowRunResult) rerunAttempt {
	f.t.Helper()
	attempt := f.runToRunningAttempt(started)
	_, lease := claimExecuteNodeJob(f.t, f.ctx, f.store, 5*time.Minute)
	if _, err := runtime.FinalizeExecutionAttempt(f.ctx, f.uow, f.ids, clock.System{}, nil, runtime.FinalizeExecutionAttemptRequest{
		RunID: attempt.runID, NodeRunID: attempt.nodeRunID, AttemptID: attempt.attemptID, ExpectedVersion: attempt.version,
		NextState: runtimedomain.ExecutionAttemptSucceeded, TerminationReason: runtimedomain.TerminationReasonCompleted,
		SelectedOutcome: "done", JobLease: lease, CorrelationID: "corr-1",
	}); err != nil {
		f.t.Fatalf("FinalizeExecutionAttempt(SUCCEEDED): %v", err)
	}
	if got := f.run(started.RunID).State; got != runtimedomain.WorkflowRunVerifying {
		f.t.Fatalf("run %s state = %s, want VERIFYING", started.RunID, got)
	}
	return attempt
}

// addEvidence records one Evidence row of kind/verdict against attempt — the
// row a node executor would propose through FinalizeExecutionAttempt, written
// here directly because these tests are about which rows completion READS,
// not about producing them.
func (f *rerunSQLite) addEvidence(attempt rerunAttempt, kind, verdict string) {
	f.t.Helper()
	if err := f.uow.WithSerializedWrite(f.ctx, func(tx ports.Tx) error {
		evidence, err := runtimedomain.NewEvidence(
			runtimedomain.EvidenceID(attempt.attemptID+":"+kind), "project-1", workdomain.WorkItemID(f.workItemID),
			runtimedomain.WorkflowRunID(attempt.runID), runtimedomain.NodeRunID(attempt.nodeRunID),
			runtimedomain.ExecutionAttemptID(attempt.attemptID), kind, verdict, []string{"artifact-" + attempt.attemptID},
			workspace.RevisionSet{}, "policy-version-1", time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC),
		)
		if err != nil {
			return err
		}
		_, err = tx.Runtime().CreateEvidence(f.ctx, evidence)
		return err
	}); err != nil {
		f.t.Fatalf("seed %s/%s evidence for attempt %s: %v", kind, verdict, attempt.attemptID, err)
	}
}

// evaluate runs EvaluateCompletionCandidate for the run's current VERIFYING
// version.
func (f *rerunSQLite) evaluate(runID, key string) runtime.CompletionDecisionResult {
	f.t.Helper()
	cmd := testCommand(key, "hash-"+key, ports.ProjectScope("project-1"), "EvaluateCompletionCandidate")
	cmd.ExpectedVersion = f.run(runID).Version
	result, err := runtime.EvaluateCompletionCandidate(f.ctx, f.uow, f.ids, clock.System{}, cmd, runtime.EvaluateCompletionCandidateRequest{RunID: runID})
	if err != nil {
		f.t.Fatalf("EvaluateCompletionCandidate(%s): %v", runID, err)
	}
	return result
}

func (f *rerunSQLite) run(runID string) runtimedomain.WorkflowRun {
	f.t.Helper()
	var run runtimedomain.WorkflowRun
	if err := f.uow.WithReadOnly(f.ctx, func(tx ports.Tx) error {
		var err error
		run, err = tx.Runtime().GetWorkflowRun(f.ctx, runID)
		return err
	}); err != nil {
		f.t.Fatalf("GetWorkflowRun(%s): %v", runID, err)
	}
	return run
}

func (f *rerunSQLite) runs() []runtimedomain.WorkflowRun {
	f.t.Helper()
	var runs []runtimedomain.WorkflowRun
	if err := f.uow.WithReadOnly(f.ctx, func(tx ports.Tx) error {
		var err error
		runs, err = tx.Runtime().ListWorkflowRunsForWorkItem(f.ctx, f.workItemID)
		return err
	}); err != nil {
		f.t.Fatalf("ListWorkflowRunsForWorkItem: %v", err)
	}
	return runs
}

func (f *rerunSQLite) item() workdomain.WorkItem {
	f.t.Helper()
	var item workdomain.WorkItem
	if err := f.uow.WithReadOnly(f.ctx, func(tx ports.Tx) error {
		var err error
		item, err = tx.Work().GetWorkItem(f.ctx, f.workItemID)
		return err
	}); err != nil {
		f.t.Fatalf("GetWorkItem: %v", err)
	}
	return item
}

func (f *rerunSQLite) blockers() []workdomain.WorkItemBlocker {
	f.t.Helper()
	var blockers []workdomain.WorkItemBlocker
	if err := f.uow.WithReadOnly(f.ctx, func(tx ports.Tx) error {
		var err error
		blockers, err = tx.Work().ListWorkItemBlockersForWorkItem(f.ctx, f.workItemID)
		return err
	}); err != nil {
		f.t.Fatalf("ListWorkItemBlockersForWorkItem: %v", err)
	}
	return blockers
}

// onlyOpenRunFailedBlocker returns the one OPEN RUN_FAILED blocker, failing if
// there is not exactly one.
func (f *rerunSQLite) onlyOpenRunFailedBlocker() workdomain.WorkItemBlocker {
	f.t.Helper()
	var open []workdomain.WorkItemBlocker
	for _, b := range f.blockers() {
		if b.Type == workdomain.BlockerRunFailed && b.State == workdomain.BlockerOpen {
			open = append(open, b)
		}
	}
	if len(open) != 1 {
		f.t.Fatalf("OPEN RUN_FAILED blockers = %+v, want exactly one", open)
	}
	return open[0]
}

func (f *rerunSQLite) eventCount(eventType string) int {
	f.t.Helper()
	events, err := f.store.ListDomainEventsForProject(f.ctx, "project-1")
	if err != nil {
		f.t.Fatalf("ListDomainEventsForProject: %v", err)
	}
	count := 0
	for _, e := range events {
		if e.EventType == eventType {
			count++
		}
	}
	return count
}

func (f *rerunSQLite) resolve(blockerID string, mode runtime.ResolutionMode, policyGrantRef string) (runtime.ResolveWorkItemBlockerResult, error) {
	f.t.Helper()
	return runtime.ResolveWorkItemBlocker(f.ctx, f.uow, runtime.ResolveWorkItemBlockerRequest{
		BlockerID: blockerID, Mode: mode, Actor: "operator-1", Reason: "looked at the failed run", PolicyGrantRef: policyGrantRef,
	})
}

// TestRunFailed_SQLite_OpensExactlyOneBlocker_AndRerunOnTheSameWorkItem is
// ADR-033's whole loop on a real backend: fail -> RUN_FAILED blocker (the
// WorkItem BLOCKED) -> `run start` refused -> resolve -> READY -> `run start`
// again runs the SAME pinned workflow version -> it can fail and be resolved
// again, each failed Run owning one distinct blocker.
func TestRunFailed_SQLite_OpensExactlyOneBlocker_AndRerunOnTheSameWorkItem(t *testing.T) {
	f := newRerunSQLite(t)

	first := f.mustStart("idem-start-1")
	if got := f.item().Status; got != workdomain.WorkItemActive {
		t.Fatalf("work item status while the first run is live = %s, want ACTIVE", got)
	}
	f.failRun(first)

	// Failure: ONE RUN_FAILED blocker, deterministic id, sourced from the run,
	// and the WorkItem BLOCKED instead of stuck ACTIVE.
	blocker := f.onlyOpenRunFailedBlocker()
	if want := first.RunID + "-run-failed-blocker"; string(blocker.ID) != want {
		t.Fatalf("blocker id = %q, want the deterministic %q", blocker.ID, want)
	}
	if blocker.SourceRunID != first.RunID || blocker.Version != 1 {
		t.Fatalf("blocker = %+v, want SourceRunID %s at version 1", blocker, first.RunID)
	}
	if got := len(f.blockers()); got != 1 {
		t.Fatalf("blockers after the first failure = %d, want exactly 1", got)
	}
	if got := f.item().Status; got != workdomain.WorkItemBlocked {
		t.Fatalf("work item status after a failed run = %s, want BLOCKED", got)
	}
	if got := f.eventCount(runtime.RunFailedEventType); got != 1 {
		t.Fatalf("%s events = %d, want 1", runtime.RunFailedEventType, got)
	}
	if got := f.eventCount(runtime.WorkItemBlockedEventType); got != 1 {
		t.Fatalf("%s events = %d, want 1", runtime.WorkItemBlockedEventType, got)
	}

	// While the blocker is open `run start` returns the existing typed error.
	if _, err := f.start("idem-start-while-blocked"); !errors.Is(err, runtime.ErrWorkItemNotReady) {
		t.Fatalf("start while the RUN_FAILED blocker is open = %v, want ErrWorkItemNotReady", err)
	}

	// Waive is a typed validation error, with or without a policy grant, and
	// leaves everything as it was (the WAIVED path's DecisionArtifact included).
	if _, err := f.resolve(string(blocker.ID), runtime.ResolutionModeWaived, "grant-1"); !errors.Is(err, runtime.ErrBlockerNotWaivable) {
		t.Fatalf("waive with a grant = %v, want ErrBlockerNotWaivable", err)
	}
	if _, err := f.resolve(string(blocker.ID), runtime.ResolutionModeWaived, ""); !errors.Is(err, runtime.ErrWaiveRequiresPolicyGrant) {
		t.Fatalf("waive without a grant = %v, want ErrWaiveRequiresPolicyGrant", err)
	}
	if got := f.onlyOpenRunFailedBlocker(); got.Version != blocker.Version {
		t.Fatalf("blocker after rejected waives = %+v, want untouched", got)
	}
	if got := f.item().Status; got != workdomain.WorkItemBlocked {
		t.Fatalf("work item status after rejected waives = %s, want BLOCKED", got)
	}
	err := f.uow.WithReadOnly(f.ctx, func(tx ports.Tx) error {
		_, err := tx.Runtime().GetDecisionArtifact(f.ctx, string(blocker.ID)+"-waiver")
		return err
	})
	if !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("waiver DecisionArtifact lookup = %v, want not found (the rejected waive must leave no artifact)", err)
	}

	// Resolve -> READY.
	resolved, err := f.resolve(string(blocker.ID), runtime.ResolutionModeResolved, "")
	if err != nil {
		t.Fatalf("resolve RUN_FAILED: %v", err)
	}
	if resolved.AlreadyResolved || resolved.State != string(workdomain.BlockerResolved) || !resolved.WorkItemUnblocked ||
		resolved.WorkItemStatus != string(workdomain.WorkItemReady) {
		t.Fatalf("resolve result = %+v, want resolved, unblocked, READY", resolved)
	}
	if got := f.item().Status; got != workdomain.WorkItemReady {
		t.Fatalf("work item status after resolve = %s, want READY", got)
	}

	// No repin: another workflow version is refused even though the WorkItem is
	// READY again — changing the version still means a new WorkItem.
	other := publishWorkflowVersionDocument(t, f.uow, "project-1", "wf-def-other", "wf-v-other",
		agentExecutableDocument("agent-profile-v1", fullyResolvablePolicyRefs(), nil))
	if _, err := f.startWith("idem-start-other-version", string(other.ID())); !errors.Is(err, runtime.ErrWorkflowVersionMismatch) {
		t.Fatalf("start with a different workflow version = %v, want ErrWorkflowVersionMismatch", err)
	}
	if got := f.item().Status; got != workdomain.WorkItemReady {
		t.Fatalf("work item status after the refused repin = %s, want still READY", got)
	}

	// Rerun: a NEW run on the SAME WorkItem, the SAME pinned version.
	second := f.mustStart("idem-start-2")
	if second.RunID == first.RunID {
		t.Fatalf("second run id = first run id %s", first.RunID)
	}
	runs := f.runs()
	if len(runs) != 2 || runs[0].ID != runtimedomain.WorkflowRunID(first.RunID) || runs[1].ID != runtimedomain.WorkflowRunID(second.RunID) {
		t.Fatalf("runs = %+v, want [first, second]", runs)
	}
	for _, r := range runs {
		if r.WorkItemID != workdomain.WorkItemID(f.workItemID) || r.WorkflowVersionID != f.version.ID() {
			t.Fatalf("run %s = work item %s version %s, want %s / %s", r.ID, r.WorkItemID, r.WorkflowVersionID, f.workItemID, f.version.ID())
		}
	}
	if got := f.run(first.RunID).State; got != runtimedomain.WorkflowRunFailed {
		t.Fatalf("first run state after the rerun = %s, want it left FAILED", got)
	}
	if got := f.run(second.RunID).State; got != runtimedomain.WorkflowRunRunning {
		t.Fatalf("second run state = %s, want RUNNING", got)
	}
	if got := f.item().Status; got != workdomain.WorkItemActive {
		t.Fatalf("work item status during the second run = %s, want ACTIVE", got)
	}
	err = f.uow.WithReadOnly(f.ctx, func(tx ports.Tx) error {
		for _, runID := range []string{first.RunID, second.RunID} {
			manifest, err := tx.Runtime().GetExecutionManifest(f.ctx, runID)
			if err != nil {
				return err
			}
			if manifest.WorkflowVersionID != f.version.ID() || manifest.CompiledSnapshotHash != f.version.ContentHash() {
				t.Errorf("manifest of %s pins %s/%s, want %s/%s", runID, manifest.WorkflowVersionID, manifest.CompiledSnapshotHash, f.version.ID(), f.version.ContentHash())
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read manifests: %v", err)
	}

	// The loop repeats: the second run fails too and opens ITS OWN blocker;
	// the first one stays RESOLVED.
	f.failRun(second)
	again := f.onlyOpenRunFailedBlocker()
	if want := second.RunID + "-run-failed-blocker"; string(again.ID) != want || again.ID == blocker.ID {
		t.Fatalf("second blocker id = %q, want %q (distinct from the first)", again.ID, want)
	}
	byID := map[workdomain.BlockerID]workdomain.WorkItemBlocker{}
	for _, b := range f.blockers() {
		byID[b.ID] = b
	}
	if len(byID) != 2 || byID[blocker.ID].State != workdomain.BlockerResolved {
		t.Fatalf("blockers = %+v, want the first RESOLVED and the second OPEN", byID)
	}
	if got := f.item().Status; got != workdomain.WorkItemBlocked {
		t.Fatalf("work item status after the second failure = %s, want BLOCKED", got)
	}
	if _, err := f.resolve(string(again.ID), runtime.ResolutionModeResolved, ""); err != nil {
		t.Fatalf("resolve the second RUN_FAILED: %v", err)
	}
	third := f.mustStart("idem-start-3")
	if got := len(f.runs()); got != 3 || f.run(third.RunID).State != runtimedomain.WorkflowRunRunning {
		t.Fatalf("after the third start: %d runs, third state %s, want 3 runs and the third RUNNING", got, f.run(third.RunID).State)
	}
}

// TestRunFailed_SQLite_ReplayOfTheFailureOpensNoSecondBlocker: the failure
// handling is replayed on an already-FAILED run (what a redelivered job or a
// restart does) and opens nothing more — the blocker id is deterministic per
// run, and a second delivery of the same underlying transition can only be the
// identical row.
func TestRunFailed_SQLite_ReplayOfTheFailureOpensNoSecondBlocker(t *testing.T) {
	f := newRerunSQLite(t)
	first := f.mustStart("idem-start-1")
	finalize := f.failRun(first)
	blocker := f.onlyOpenRunFailedBlocker()

	// The very same terminal transition delivered a second time (a redelivered
	// job after the first delivery committed) is refused by the Attempt's own
	// CAS before anything else is written — in particular no second blocker,
	// no second RUN_FAILED / WORK_ITEM_BLOCKED event.
	if _, err := runtime.FinalizeExecutionAttempt(f.ctx, f.uow, f.ids, clock.System{}, nil, finalize); err == nil {
		t.Fatal("a second delivery of the same FAILED finalize succeeded; it must be refused")
	}
	if got := len(f.blockers()); got != 1 {
		t.Fatalf("blockers after the redelivered finalize = %d, want 1", got)
	}
	if got := f.eventCount(runtime.RunFailedEventType); got != 1 {
		t.Fatalf("%s events after the redelivered finalize = %d, want 1", runtime.RunFailedEventType, got)
	}

	// CreateWorkItemBlocker with the same deterministic id (what a replayed
	// open would issue) returns the stored row instead of inserting a second.
	if err := f.uow.WithSerializedWrite(f.ctx, func(tx ports.Tx) error {
		replay, err := workdomain.NewWorkItemBlocker(
			blocker.ID, blocker.ProjectID, blocker.WorkItemID, workdomain.BlockerRunFailed, first.RunID, "", "", "replayed open", time.Now().UTC(),
		)
		if err != nil {
			return err
		}
		stored, err := tx.Work().CreateWorkItemBlocker(f.ctx, replay)
		if err != nil {
			return err
		}
		if stored.Reason != blocker.Reason {
			t.Errorf("replayed CreateWorkItemBlocker returned reason %q, want the stored %q", stored.Reason, blocker.Reason)
		}
		return nil
	}); err != nil {
		t.Fatalf("replayed CreateWorkItemBlocker: %v", err)
	}
	if got := len(f.blockers()); got != 1 {
		t.Fatalf("blockers after the replayed open = %d, want 1", got)
	}

	// And nothing is lost or duplicated across a restart.
	restarted := f.restart()
	if got := len(restarted.blockers()); got != 1 {
		t.Fatalf("blockers after a restart = %d, want 1", got)
	}
	if got := restarted.eventCount(runtime.WorkItemBlockedEventType); got != 1 {
		t.Fatalf("%s events after a restart = %d, want 1", runtime.WorkItemBlockedEventType, got)
	}
}

// restart closes and reopens the SAME database file, the crash/restart proof
// every sqlite test in this package uses.
func (f *rerunSQLite) restart() *rerunSQLite {
	f.t.Helper()
	if err := f.store.Close(); err != nil {
		f.t.Fatalf("close store: %v", err)
	}
	store, err := sqlite.Open(f.ctx, f.dbPath)
	if err != nil {
		f.t.Fatalf("reopen store: %v", err)
	}
	f.t.Cleanup(func() { _ = store.Close() })
	next := *f
	next.store = store
	next.uow = sqlite.NewUnitOfWork(store)
	return &next
}

// TestResolveRunFailed_SQLite_PreconditionsAreUnchanged: resolving a RUN_FAILED
// blocker keeps ResolveWorkItemBlocker's two preconditions — no non-terminal
// run, no QUARANTINED workspace — and a rejection leaves the blocker OPEN and
// the WorkItem BLOCKED.
func TestResolveRunFailed_SQLite_PreconditionsAreUnchanged(t *testing.T) {
	f := newRerunSQLite(t)
	first := f.mustStart("idem-start-1")
	f.failRun(first)
	blocker := f.onlyOpenRunFailedBlocker()

	assertStillBlocked := func(label string) {
		t.Helper()
		if got := f.onlyOpenRunFailedBlocker(); got.Version != blocker.Version {
			t.Fatalf("%s: blocker = %+v, want untouched", label, got)
		}
		if got := f.item().Status; got != workdomain.WorkItemBlocked {
			t.Fatalf("%s: work item status = %s, want BLOCKED", label, got)
		}
	}

	// A QUARANTINED workspace in the family: refuse.
	item := f.item()
	var setID string
	if err := f.uow.WithReadOnly(f.ctx, func(tx ports.Tx) error {
		set, err := tx.Work().GetWorkspaceSetByFamilyID(f.ctx, string(item.FamilyID))
		setID = string(set.ID)
		return err
	}); err != nil {
		t.Fatalf("GetWorkspaceSetByFamilyID: %v", err)
	}
	registerSecondRepositorySQLite(t, f.uow, f.ids)
	if err := f.uow.WithSerializedWrite(f.ctx, func(tx ports.Tx) error {
		_, err := tx.Work().CreateRepositoryWorkspace(f.ctx, workspace.RepositoryWorkspace{
			ID: workspace.RepositoryWorkspaceID(f.ids.NewID()), WorkspaceSetID: workspace.WorkspaceSetID(setID),
			RepositoryID: project.RepositoryID("repo-2"), Generation: 1, Locator: "handle-repo-2",
			BaseRevision: "cafebabecafebabecafebabecafebabecafebabe", State: workspace.RepositoryWorkspaceQuarantined, Version: 1,
		})
		return err
	}); err != nil {
		t.Fatalf("seed quarantined repository workspace: %v", err)
	}
	if _, err := f.resolve(string(blocker.ID), runtime.ResolutionModeResolved, ""); !errors.Is(err, runtime.ErrWorkspaceQuarantined) {
		t.Fatalf("resolve with a QUARANTINED workspace = %v, want ErrWorkspaceQuarantined", err)
	}
	assertStillBlocked("quarantined workspace")

	// A non-terminal run of the WorkItem: refuse. (The WorkItem is BLOCKED, so no
	// command can start one; the second run is seeded directly, like
	// TestResolveWorkItemBlocker_NonTerminalRun_Rejected seeds its blocker.)
	if err := f.uow.WithSerializedWrite(f.ctx, func(tx ports.Tx) error {
		live, err := runtimedomain.NewWorkflowRun(
			runtimedomain.WorkflowRunID("run-live"), item.ProjectID, item.ID, f.version, item.FamilyID, 1, nil,
		)
		if err != nil {
			return err
		}
		live.State = runtimedomain.WorkflowRunRunning
		_, err = tx.Runtime().CreateWorkflowRun(f.ctx, live)
		return err
	}); err != nil {
		t.Fatalf("seed a live run: %v", err)
	}
	if _, err := f.resolve(string(blocker.ID), runtime.ResolutionModeResolved, ""); !errors.Is(err, runtime.ErrWorkItemHasNonTerminalRun) {
		t.Fatalf("resolve with a live run = %v, want ErrWorkItemHasNonTerminalRun", err)
	}
	assertStillBlocked("live run")
}

// registerSecondRepositorySQLite registers repo-2 under project-1 (ACTIVE),
// the repository a QUARANTINED RepositoryWorkspace row needs a foreign key to.
func registerSecondRepositorySQLite(t *testing.T, uow ports.UnitOfWork, ids idsource.Source) {
	t.Helper()
	ctx := context.Background()
	regCmd := ports.Command{
		ID: "cmd-reg-repo-2", IdempotencyKey: "idem-reg-repo-2", Actor: "actor-1", CorrelationID: "corr-1",
		Scope: ports.ProjectScope("project-1"), RequestedAt: time.Now().UTC(), Type: "RegisterRepository", RequestHash: "hash-reg-repo-2",
	}
	if _, err := catalog.RegisterRepository(ctx, uow, ids, regCmd, catalog.RegisterRepositoryRequest{
		RepositoryID: "repo-2", ProjectID: "project-1", Name: "repo-2",
		RemoteLocator: "https://example.invalid/repo-2.git", DefaultRef: "main",
	}); err != nil {
		t.Fatalf("RegisterRepository(repo-2): %v", err)
	}
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if _, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: "repo-2", ExpectedStatus: project.RepositoryRegistering, ExpectedVersion: 1, NextStatus: project.RepositoryProbing,
		}); err != nil {
			return err
		}
		_, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: "repo-2", ExpectedStatus: project.RepositoryProbing, ExpectedVersion: 2, NextStatus: project.RepositoryActive,
		})
		return err
	}); err != nil {
		t.Fatalf("activate repo-2: %v", err)
	}
}

// TestRerun_SQLite_CrashBetweenTheTwoRuns_ExactlyOneNewRun covers the crash
// windows between the failed run and its rerun: after the resolve (a restart
// before `run start`), the same start command re-issued with the same
// idempotency key after it already committed (replay, no second run), and a
// burst of concurrent starts with distinct keys against the one READY
// WorkItem — in every case exactly one new run exists.
func TestRerun_SQLite_CrashBetweenTheTwoRuns_ExactlyOneNewRun(t *testing.T) {
	f := newRerunSQLite(t)
	first := f.mustStart("idem-start-1")
	f.failRun(first)
	blocker := f.onlyOpenRunFailedBlocker()
	if _, err := f.resolve(string(blocker.ID), runtime.ResolutionModeResolved, ""); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	// Crash right after the resolve: everything it committed survives, the
	// WorkItem is READY and a second resolve is the harmless idempotent no-op.
	f = f.restart()
	if got := f.item().Status; got != workdomain.WorkItemReady {
		t.Fatalf("work item status after the restart = %s, want READY", got)
	}
	again, err := f.resolve(string(blocker.ID), runtime.ResolutionModeResolved, "")
	if err != nil || !again.AlreadyResolved {
		t.Fatalf("second resolve = %+v, %v, want the AlreadyResolved no-op", again, err)
	}

	// Concurrent starts, each with its own key: exactly one wins the
	// READY -> ACTIVE CAS, every other fails closed with ErrWorkItemNotReady.
	const racers = 6
	var wg sync.WaitGroup
	var mu sync.Mutex
	winnerKeyByRun := map[string]string{}
	for i := 0; i < racers; i++ {
		suffix := strconv.Itoa(i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := runtime.StartWorkflowRun(f.ctx, f.uow, idsource.NewSequential("race"+suffix), f.startCommand("idem-race-"+suffix), runtime.StartWorkflowRunRequest{
				ProjectID: "project-1", WorkItemID: f.workItemID, WorkflowVersionID: string(f.version.ID()),
			})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if !errors.Is(err, runtime.ErrWorkItemNotReady) {
					t.Errorf("racer %s: unexpected error %v", suffix, err)
				}
				return
			}
			winnerKeyByRun[result.RunID] = "idem-race-" + suffix
		}()
	}
	wg.Wait()
	if len(winnerKeyByRun) != 1 {
		t.Fatalf("winning starts = %v, want exactly one", winnerKeyByRun)
	}
	var winnerRunID, winnerKey string
	for runID, key := range winnerKeyByRun {
		winnerRunID, winnerKey = runID, key
	}
	if got := len(f.runs()); got != 2 {
		t.Fatalf("runs after the race = %d, want 2 (the failed one and exactly one new)", got)
	}

	// The winner's command re-issued after a crash (same key, same request):
	// the receipt replays the first result, creating nothing.
	f = f.restart()
	replayed, err := f.start(winnerKey)
	if err != nil {
		t.Fatalf("replayed start: %v", err)
	}
	if replayed.RunID != winnerRunID {
		t.Fatalf("replayed start run id = %s, want the original %s", replayed.RunID, winnerRunID)
	}
	if got := len(f.runs()); got != 2 {
		t.Fatalf("runs after the replayed start = %d, want still 2", got)
	}
	count, err := f.store.CountWorkflowRuns(f.ctx)
	if err != nil || count != 2 {
		t.Fatalf("workflow_runs row count = %d, %v, want 2", count, err)
	}
	if got := f.item().Status; got != workdomain.WorkItemActive {
		t.Fatalf("work item status = %s, want ACTIVE", got)
	}
}

// publishRerunWorkflowVersion is publishWorkflowVersionDocument with a caller
// supplied dependency manifest (the completion policy pin).
func publishRerunWorkflowVersion(t *testing.T, uow ports.UnitOfWork, document workflow.WorkflowDocument, dependencies workflow.DependencyManifest) workflow.WorkflowVersion {
	t.Helper()
	pid := project.ProjectID("project-1")
	def := workflow.WorkflowDefinition{
		ID: "wf-def-1", ProjectID: &pid, Name: "workflow wf-def-1", Status: workflow.DefinitionActive, Version: 1,
	}
	candidate, err := workflow.Compile(def, workflow.PublishRequest{
		VersionID: "wf-v-1", VersionNumber: 1, Document: document, Dependencies: dependencies,
		PublishedBy: "operator-1", PublishedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("compile workflow: %v", err)
	}
	ctx := context.Background()
	var published workflow.WorkflowVersion
	if err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		p, err := tx.Definitions().PublishWorkflowVersion(ctx, def, candidate)
		published = p
		return err
	}); err != nil {
		t.Fatalf("publish workflow version: %v", err)
	}
	return published
}

// TestRerun_SQLite_CompletionOfTheSecondRunIgnoresTheFirstRunsEvidence is
// ADR-033 decision 5: messages and evidence of the earlier run stay on the
// WorkItem, but completion considers ONLY the current run's evidence. Both
// directions: a FAILED evidence row of the first run does not block the
// second run's completion, and a PASSING row of the first run does not satisfy
// a second run that produced none.
func TestRerun_SQLite_CompletionOfTheSecondRunIgnoresTheFirstRunsEvidence(t *testing.T) {
	t.Run("the first run's FAILED evidence does not block the second run", func(t *testing.T) {
		f := newRerunSQLiteWithCompletion(t, true)
		first := f.mustStart("idem-start-1")
		firstAttempt := f.runToRunningAttempt(first)
		f.addEvidence(firstAttempt, rerunRequiredEvidenceKind, runtimedomain.EvidenceVerdictFailed)
		_, lease := claimExecuteNodeJob(t, f.ctx, f.store, 5*time.Minute)
		if _, err := runtime.FinalizeExecutionAttempt(f.ctx, f.uow, f.ids, clock.System{}, nil, runtime.FinalizeExecutionAttemptRequest{
			RunID: firstAttempt.runID, NodeRunID: firstAttempt.nodeRunID, AttemptID: firstAttempt.attemptID, ExpectedVersion: firstAttempt.version,
			NextState: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonExecutionFailed,
			FailureCode: errorcode.CodeExecutionFailed, JobLease: lease, CorrelationID: "corr-1",
		}); err != nil {
			t.Fatalf("fail the first run: %v", err)
		}
		if _, err := f.resolve(string(f.onlyOpenRunFailedBlocker().ID), runtime.ResolutionModeResolved, ""); err != nil {
			t.Fatalf("resolve: %v", err)
		}

		second := f.mustStart("idem-start-2")
		secondAttempt := f.reachVerifying(second)
		f.addEvidence(secondAttempt, rerunRequiredEvidenceKind, runtimedomain.EvidenceVerdictSucceeded)

		decision := f.evaluate(second.RunID, "idem-eval-2")
		if decision.Outcome != runtime.CompletionOutcomePass {
			t.Fatalf("completion of the second run = %+v, want PASS (the first run's FAILED evidence must not count)", decision)
		}
		if got := f.item().Status; got != workdomain.WorkItemDone {
			t.Fatalf("work item status = %s, want DONE on the same WorkItem", got)
		}
		if got := f.run(second.RunID).State; got != runtimedomain.WorkflowRunSucceeded {
			t.Fatalf("second run state = %s, want SUCCEEDED", got)
		}
		if got := f.run(first.RunID).State; got != runtimedomain.WorkflowRunFailed {
			t.Fatalf("first run state = %s, want it left FAILED", got)
		}
	})

	t.Run("the first run's PASSING evidence does not satisfy the second run", func(t *testing.T) {
		f := newRerunSQLiteWithCompletion(t, true)
		first := f.mustStart("idem-start-1")
		firstAttempt := f.runToRunningAttempt(first)
		f.addEvidence(firstAttempt, rerunRequiredEvidenceKind, runtimedomain.EvidenceVerdictSucceeded)
		_, lease := claimExecuteNodeJob(t, f.ctx, f.store, 5*time.Minute)
		if _, err := runtime.FinalizeExecutionAttempt(f.ctx, f.uow, f.ids, clock.System{}, nil, runtime.FinalizeExecutionAttemptRequest{
			RunID: firstAttempt.runID, NodeRunID: firstAttempt.nodeRunID, AttemptID: firstAttempt.attemptID, ExpectedVersion: firstAttempt.version,
			NextState: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonExecutionFailed,
			FailureCode: errorcode.CodeExecutionFailed, JobLease: lease, CorrelationID: "corr-1",
		}); err != nil {
			t.Fatalf("fail the first run: %v", err)
		}
		if _, err := f.resolve(string(f.onlyOpenRunFailedBlocker().ID), runtime.ResolutionModeResolved, ""); err != nil {
			t.Fatalf("resolve: %v", err)
		}

		second := f.mustStart("idem-start-2")
		f.reachVerifying(second) // no evidence for the second run at all

		decision := f.evaluate(second.RunID, "idem-eval-2")
		if decision.Outcome != runtime.CompletionOutcomeBlock || !strings.Contains(decision.Reason, runtime.ReasonCompletionRequirementsUnmet) {
			t.Fatalf("completion of the second run = %+v, want BLOCK (%s): the first run's evidence must not satisfy it", decision, runtime.ReasonCompletionRequirementsUnmet)
		}
		if got := f.item().Status; got != workdomain.WorkItemBlocked {
			t.Fatalf("work item status = %s, want BLOCKED", got)
		}
	})
}

// TestRunFailed_SQLite_WorkItemBeingCancelled_OpensNoBlocker is the "unless
// the run belongs to a WorkItem being cancelled" rule of ADR-033 decision 1
// (the RUN_CANCELLED twin's rule): a WorkItem already on its way to CANCELLED
// must never be pushed back to BLOCKED by the Run that closes it out.
func TestRunFailed_SQLite_WorkItemBeingCancelled_OpensNoBlocker(t *testing.T) {
	t.Run("CancelWorkItem while the run is live", func(t *testing.T) {
		f := newRerunSQLite(t)
		started := f.mustStart("idem-start-1")
		attempt := f.runToRunningAttempt(started)
		if _, err := runtime.CancelWorkItem(f.ctx, f.uow, f.ids, runtime.CancelWorkItemRequest{
			WorkItemID: f.workItemID, Actor: "operator-1", Reason: "not needed any more",
		}); err != nil {
			t.Fatalf("CancelWorkItem: %v", err)
		}
		_, lease := claimExecuteNodeJob(t, f.ctx, f.store, 5*time.Minute)
		if _, err := runtime.FinalizeExecutionAttempt(f.ctx, f.uow, f.ids, clock.System{}, nil, runtime.FinalizeExecutionAttemptRequest{
			RunID: attempt.runID, NodeRunID: attempt.nodeRunID, AttemptID: attempt.attemptID, ExpectedVersion: attempt.version,
			NextState: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonExecutionFailed,
			FailureCode: errorcode.CodeExecutionFailed, JobLease: lease, CorrelationID: "corr-1",
		}); err != nil {
			t.Fatalf("FinalizeExecutionAttempt(FAILED): %v", err)
		}
		if got := f.item().Status; got != workdomain.WorkItemCancelled {
			t.Fatalf("work item status = %s, want CANCELLED", got)
		}
		for _, b := range f.blockers() {
			if b.Type == workdomain.BlockerRunFailed {
				t.Fatalf("a RUN_FAILED blocker %+v was opened for a WorkItem being cancelled", b)
			}
		}
	})

	t.Run("a run failing under a recorded cancellation intent", func(t *testing.T) {
		f := newRerunSQLite(t)
		started := f.mustStart("idem-start-1")
		attempt := f.runToRunningAttempt(started)

		// The intent alone is recorded (CancelWorkItem would also have moved the
		// live run to CANCELLING; no public command leaves a RUNNING run behind
		// an intent, so the defensive FAILED branch is reached by writing the
		// intent directly, the way seedBlocker writes a blocker).
		if err := f.uow.WithSerializedWrite(f.ctx, func(tx ports.Tx) error {
			intent, err := runtimedomain.NewWorkItemCancellationIntent(
				"intent-1", "project-1", workdomain.WorkItemID(f.workItemID), "operator-1", "not needed any more", time.Now().UTC(),
			)
			if err != nil {
				return err
			}
			_, err = tx.Runtime().RecordWorkItemCancellationIntent(f.ctx, intent)
			return err
		}); err != nil {
			t.Fatalf("record cancellation intent: %v", err)
		}
		_, lease := claimExecuteNodeJob(t, f.ctx, f.store, 5*time.Minute)
		if _, err := runtime.FinalizeExecutionAttempt(f.ctx, f.uow, f.ids, clock.System{}, nil, runtime.FinalizeExecutionAttemptRequest{
			RunID: attempt.runID, NodeRunID: attempt.nodeRunID, AttemptID: attempt.attemptID, ExpectedVersion: attempt.version,
			NextState: runtimedomain.ExecutionAttemptFailed, TerminationReason: runtimedomain.TerminationReasonExecutionFailed,
			FailureCode: errorcode.CodeExecutionFailed, JobLease: lease, CorrelationID: "corr-1",
		}); err != nil {
			t.Fatalf("FinalizeExecutionAttempt(FAILED): %v", err)
		}
		if got := f.run(started.RunID).State; got != runtimedomain.WorkflowRunFailed {
			t.Fatalf("run state = %s, want FAILED", got)
		}
		if got := len(f.blockers()); got != 0 {
			t.Fatalf("blockers = %+v, want none for a WorkItem being cancelled", f.blockers())
		}
		if got := f.item().Status; got != workdomain.WorkItemCancelled {
			t.Fatalf("work item status = %s, want CANCELLED (the failed run closes the pending cancellation out)", got)
		}
		if got := f.eventCount(runtime.WorkItemBlockedEventType); got != 0 {
			t.Fatalf("%s events = %d, want 0", runtime.WorkItemBlockedEventType, got)
		}
	})
}

// TestRunFailed_SQLite_GivingUpIsCancelWorkItem: ADR-033 decision 2 — "Muốn bỏ
// việc thì dùng CancelWorkItem". A WorkItem BLOCKED by a RUN_FAILED blocker can
// be cancelled (all its runs are terminal, so it closes out at once), and
// nothing can then be started on it.
func TestRunFailed_SQLite_GivingUpIsCancelWorkItem(t *testing.T) {
	f := newRerunSQLite(t)
	first := f.mustStart("idem-start-1")
	f.failRun(first)
	blocker := f.onlyOpenRunFailedBlocker()

	result, err := runtime.CancelWorkItem(f.ctx, f.uow, f.ids, runtime.CancelWorkItemRequest{
		WorkItemID: f.workItemID, Actor: "operator-1", Reason: "not worth another try",
	})
	if err != nil {
		t.Fatalf("CancelWorkItem: %v", err)
	}
	if result.Status != string(workdomain.WorkItemCancelled) || f.item().Status != workdomain.WorkItemCancelled {
		t.Fatalf("cancel result = %+v, work item status %s, want CANCELLED", result, f.item().Status)
	}
	if _, err := f.start("idem-start-after-cancel"); !errors.Is(err, runtime.ErrWorkItemNotReady) {
		t.Fatalf("start on a cancelled work item = %v, want ErrWorkItemNotReady", err)
	}
	// Resolving the leftover blocker neither errors nor revives the WorkItem.
	if _, err := f.resolve(string(blocker.ID), runtime.ResolutionModeResolved, ""); err != nil {
		t.Fatalf("resolve after the cancel: %v", err)
	}
	if got := f.item().Status; got != workdomain.WorkItemCancelled {
		t.Fatalf("work item status after resolving the leftover blocker = %s, want it left CANCELLED", got)
	}
}
