package runtime_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/releasesetcommit"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workflow"
)

// V11-07: a decision that also asks for a commit. Against a real sqlite store; the commit itself
// (Git) is the background job's, so these tests prove what the decision's transaction writes — or,
// when it refuses, that it wrote nothing.

type releaseFixture struct {
	ctx               context.Context
	uow               ports.UnitOfWork
	store             *sqlite.Store
	ids               idsource.Source
	runID             string
	approvalRequestID string
	workspaceID       string
	workspaceVersion  uint64
	familyID          string
	workItemID        string
}

// releaseDocument is start -> gate (APPROVAL with the given release policy) -> end; "approved" is the
// outcome that may carry a commit.
func releaseDocument(release *workflow.ApprovalReleaseConfig) workflow.WorkflowDocument {
	document := approvalDocument(600)
	for index := range document.Nodes {
		if document.Nodes[index].Key == "gate" {
			document.Nodes[index].Approval.Release = release
		}
	}
	return document
}

func newReleaseFixture(t *testing.T, release *workflow.ApprovalReleaseConfig) *releaseFixture {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "release.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")

	root := readyFixtureSQLite(t, uow, ids, "project-1", "repo-1")
	version := publishWorkflowVersionDocument(t, uow, "project-1", "wf-def-1", "wf-v-1", releaseDocument(release))
	start, err := runtime.StartWorkflowRun(ctx, uow, ids,
		testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun"),
		runtime.StartWorkflowRunRequest{ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID())})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	hop, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: start.RunID, NodeRunID: start.NodeRunID})
	if err != nil || hop.NextApprovalRequestID == "" {
		t.Fatalf("AdvanceRun (start->gate) = %+v, %v", hop, err)
	}
	fx := &releaseFixture{
		ctx: ctx, uow: uow, store: store, ids: ids, runID: start.RunID, approvalRequestID: hop.NextApprovalRequestID,
		familyID: root.FamilyID, workItemID: root.WorkItemID,
	}
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		repositories, err := tx.Work().ListWorkspaceSetRepositoryWorkspaces(ctx, root.WorkspaceSetID)
		if err != nil || len(repositories) != 1 {
			t.Fatalf("workspace set repositories = %d, %v", len(repositories), err)
		}
		fx.workspaceID, fx.workspaceVersion = string(repositories[0].ID), repositories[0].Version
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return fx
}

func (fx *releaseFixture) resolve(key, outcome string, commit *runtime.ApprovalCommit) (runtime.ResolveApprovalResult, error) {
	return runtime.ResolveApproval(fx.ctx, fx.uow, fx.ids, reviewerCommand(key, "hash-"+key), runtime.ResolveApprovalRequest{
		RunID: fx.runID, ApprovalRequestID: fx.approvalRequestID, Outcome: outcome, Reason: "ok", Commit: commit,
	})
}

func (fx *releaseFixture) commit(mutate func(*runtime.ApprovalCommitRepository)) *runtime.ApprovalCommit {
	repository := runtime.ApprovalCommitRepository{
		RepositoryWorkspaceID: fx.workspaceID, ExpectedWorkspaceVersion: fx.workspaceVersion,
		AuthorName: "Reviewer", AuthorEmail: "reviewer@example.invalid",
		Files:      []workdomain.LocalCommitFile{{Path: "spec/openapi.json", Status: "ADDED", ContentHash: "abc"}},
		Unselected: workdomain.UnselectedKeep, Patch: []byte("diff --git a/spec/openapi.json b/spec/openapi.json\n"),
	}
	if mutate != nil {
		mutate(&repository)
	}
	return &runtime.ApprovalCommit{Message: "contract: add the issue API", Repositories: []runtime.ApprovalCommitRepository{repository}}
}

func (fx *releaseFixture) request(t *testing.T) runtimedomain.ApprovalRequest {
	t.Helper()
	var request runtimedomain.ApprovalRequest
	if err := fx.uow.WithReadOnly(fx.ctx, func(tx ports.Tx) error {
		var err error
		request, err = tx.Approvals().GetApprovalRequest(fx.ctx, fx.approvalRequestID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return request
}

// operations counts the local-commit jobs the decision enqueued by draining the queue.
func (fx *releaseFixture) operations(t *testing.T) int {
	t.Helper()
	jobs := 0
	for {
		job, _, err := fx.store.ClaimJob(fx.ctx, "counter", time.Minute)
		if err != nil || job.ID == "" {
			return jobs
		}
		if job.Kind == releasesetcommit.ReleaseSetLocalCommitJobKind {
			jobs++
		}
	}
}

var offeredOnApproved = &workflow.ApprovalReleaseConfig{Commit: workflow.ReleaseCommitOffered, Outcomes: []string{"approved"}}

func TestResolveApproval_WithCommit_RecordsTheDecisionAndRequestsTheCommitInOneTransaction(t *testing.T) {
	fx := newReleaseFixture(t, offeredOnApproved)
	result, err := fx.resolve("resolve-1", "approved", fx.commit(nil))
	if err != nil {
		t.Fatalf("ResolveApproval: %v", err)
	}
	if !result.Won || result.State != string(runtimedomain.ApprovalRequestDecided) || !result.Advanced {
		t.Fatalf("result = %+v, want a won, decided, advanced decision", result)
	}
	if result.Commit == nil || len(result.Commit.Operations) != 1 || result.Commit.ReleaseSetID == "" || result.CommitSkipped != "" {
		t.Fatalf("commit result = %+v / %q, want one operation of a release set", result.Commit, result.CommitSkipped)
	}
	if request := fx.request(t); request.State != runtimedomain.ApprovalRequestDecided || request.DecidedOutcome != "approved" {
		t.Fatalf("request = %+v, want DECIDED/approved", request)
	}
	operation := result.Commit.Operations[0]
	if err := fx.uow.WithReadOnly(fx.ctx, func(tx ports.Tx) error {
		intent, err := tx.Work().GetReleaseSetLocalCommit(fx.ctx, operation.ReleaseSetLocalCommitID)
		if err != nil {
			return err
		}
		if intent.State != workdomain.ReleaseSetLocalCommitRequested || intent.AuthorName != "Reviewer" || intent.Message != "contract: add the issue API" {
			t.Errorf("operation = %+v", intent)
		}
		selection, found, err := tx.Work().GetReleaseSetLocalCommitSelection(fx.ctx, operation.ReleaseSetLocalCommitID)
		if err != nil || !found {
			t.Fatalf("selection found=%v err=%v", found, err)
		}
		if selection.ApprovalRequestID != fx.approvalRequestID || selection.WorkItemID != fx.workItemID || selection.RunID != fx.runID ||
			len(selection.Files) != 1 || selection.Files[0].Path != "spec/openapi.json" || len(selection.Patch) == 0 {
			t.Errorf("selection = %+v, want it tied to the approval, the work item and the run, with the reviewed file and patch", selection)
		}
		releaseSet, err := tx.Work().GetReleaseSet(fx.ctx, result.Commit.ReleaseSetID)
		if err != nil {
			return err
		}
		if releaseSet.State != workdomain.ReleaseSetSealed {
			t.Errorf("release set = %s, want SEALED", releaseSet.State)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestResolveApproval_WithCommit_ReplayReturnsTheSameOperationsAndCreatesNoSecondSet(t *testing.T) {
	fx := newReleaseFixture(t, offeredOnApproved)
	first, err := fx.resolve("resolve-replay", "approved", fx.commit(nil))
	if err != nil {
		t.Fatal(err)
	}
	second, err := fx.resolve("resolve-replay", "approved", fx.commit(nil))
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if first.Commit == nil || second.Commit == nil || first.Commit.ReleaseSetID != second.Commit.ReleaseSetID ||
		first.Commit.Operations[0].ReleaseSetLocalCommitID != second.Commit.Operations[0].ReleaseSetLocalCommitID {
		t.Fatalf("replay = %+v, want the first call's operations", second.Commit)
	}
	if got := fx.operations(t); got != 1 {
		t.Fatalf("commit jobs after a replay = %d, want exactly 1", got)
	}
}

func TestResolveApproval_CommitThePolicyDoesNotOfferLeavesTheGatePending(t *testing.T) {
	for name, release := range map[string]*workflow.ApprovalReleaseConfig{
		"no policy":                  nil,
		"disabled":                   {Commit: workflow.ReleaseCommitDisabled},
		"another outcome is offered": {Commit: workflow.ReleaseCommitOffered, Outcomes: []string{"rejected"}},
	} {
		t.Run(name, func(t *testing.T) {
			fx := newReleaseFixture(t, release)
			_, err := fx.resolve("resolve-refused", "approved", fx.commit(nil))
			if !errors.Is(err, runtime.ErrCommitNotOffered) {
				t.Fatalf("err = %v, want ErrCommitNotOffered", err)
			}
			if request := fx.request(t); request.State != runtimedomain.ApprovalRequestPending {
				t.Fatalf("request = %s, want still PENDING: a refused commit must not record the decision", request.State)
			}
			if got := fx.operations(t); got != 0 {
				t.Fatalf("commit jobs = %d, want 0", got)
			}
			// the same decision without a commit still works
			if result, err := fx.resolve("resolve-plain", "approved", nil); err != nil || !result.Won {
				t.Fatalf("plain decision = %+v, %v", result, err)
			}
		})
	}
}

func TestResolveApproval_RequiredPolicyDemandsACommitDecision(t *testing.T) {
	required := &workflow.ApprovalReleaseConfig{Commit: workflow.ReleaseCommitRequired, Outcomes: []string{"approved"}}
	fx := newReleaseFixture(t, required)
	if _, err := fx.resolve("resolve-required", "approved", nil); !errors.Is(err, runtime.ErrCommitRequired) {
		t.Fatalf("err = %v, want ErrCommitRequired", err)
	}
	if request := fx.request(t); request.State != runtimedomain.ApprovalRequestPending {
		t.Fatalf("request = %s, want PENDING", request.State)
	}
	// an outcome the policy does not list needs no commit
	if result, err := fx.resolve("resolve-rejected", "rejected", nil); err != nil || !result.Won {
		t.Fatalf("rejected without a commit = %+v, %v; want accepted", result, err)
	}
}

func TestResolveApproval_RequiredPolicyWithNothingToCommitIsRecordedNotFailed(t *testing.T) {
	required := &workflow.ApprovalReleaseConfig{Commit: workflow.ReleaseCommitRequired, Outcomes: []string{"approved"}}
	fx := newReleaseFixture(t, required)
	result, err := fx.resolve("resolve-empty", "approved", &runtime.ApprovalCommit{Message: "nothing changed"})
	if err != nil {
		t.Fatalf("ResolveApproval: %v", err)
	}
	if !result.Won || result.CommitSkipped != runtime.CommitSkippedNothingToCommit || result.Commit != nil {
		t.Fatalf("result = %+v, want a won decision with NOTHING_TO_COMMIT and no operation", result)
	}
	if got := fx.operations(t); got != 0 {
		t.Fatalf("commit jobs = %d, want 0", got)
	}
}

func TestResolveApproval_MissingAuthorIdentityRefusesTheWholeCommandBeforeWritingAnything(t *testing.T) {
	fx := newReleaseFixture(t, offeredOnApproved)
	for name, mutate := range map[string]func(*runtime.ApprovalCommitRepository){
		"no name":  func(r *runtime.ApprovalCommitRepository) { r.AuthorName = "" },
		"no email": func(r *runtime.ApprovalCommitRepository) { r.AuthorEmail = "" },
	} {
		_, err := fx.resolve("resolve-identity-"+name, "approved", fx.commit(mutate))
		if !errors.Is(err, releasesetcommit.ErrCommitIdentityMissing) {
			t.Fatalf("%s: err = %v, want ErrCommitIdentityMissing", name, err)
		}
		if request := fx.request(t); request.State != runtimedomain.ApprovalRequestPending {
			t.Fatalf("%s: request = %s, want PENDING (the gate stays open)", name, request.State)
		}
	}
	if got := fx.operations(t); got != 0 {
		t.Fatalf("commit jobs = %d, want 0", got)
	}
}

// The decision and the commit request are atomic: if the commit cannot be requested (here, the
// workspace changed under the approver), the decision is not recorded either.
func TestResolveApproval_AFailedCommitRequestRollsTheDecisionBack(t *testing.T) {
	fx := newReleaseFixture(t, offeredOnApproved)
	_, err := fx.resolve("resolve-stale", "approved", fx.commit(func(r *runtime.ApprovalCommitRepository) { r.ExpectedWorkspaceVersion = 99 }))
	if !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("err = %v, want ErrOptimisticConflict", err)
	}
	if request := fx.request(t); request.State != runtimedomain.ApprovalRequestPending {
		t.Fatalf("request = %s, want PENDING: the decision must roll back with the failed commit request", request.State)
	}
	if got := fx.operations(t); got != 0 {
		t.Fatalf("commit jobs = %d, want 0", got)
	}
}

func TestResolveApproval_ALostRaceCreatesNoCommit(t *testing.T) {
	fx := newReleaseFixture(t, offeredOnApproved)
	if result, err := fx.resolve("resolve-first", "rejected", nil); err != nil || !result.Won {
		t.Fatalf("first decision = %+v, %v", result, err)
	}
	// the gate is already decided; a second decision with a commit loses and must not commit
	second, err := fx.resolve("resolve-second", "approved", fx.commit(nil))
	if err != nil {
		t.Fatalf("second decision: %v", err)
	}
	if second.Won || second.Commit != nil {
		t.Fatalf("second = %+v, want Won=false and no commit", second)
	}
	if got := fx.operations(t); got != 0 {
		t.Fatalf("commit jobs = %d, want 0", got)
	}
}

func TestOpenReleaseFailedBlockerTx_OpensOneBlockerOnTheWorkItemTheApprovalBelongsTo(t *testing.T) {
	fx := newReleaseFixture(t, offeredOnApproved)
	result, err := fx.resolve("resolve-blocker", "approved", fx.commit(nil))
	if err != nil {
		t.Fatal(err)
	}
	operationID := result.Commit.Operations[0].ReleaseSetLocalCommitID

	open := func() error {
		return fx.uow.WithSerializedWrite(fx.ctx, func(tx ports.Tx) error {
			intent, err := tx.Work().GetReleaseSetLocalCommit(fx.ctx, operationID)
			if err != nil {
				return err
			}
			return runtime.OpenReleaseFailedBlockerTx(fx.ctx, tx, intent, workdomain.FailureChangeSetDrift)
		})
	}
	if err := open(); err != nil {
		t.Fatalf("OpenReleaseFailedBlockerTx: %v", err)
	}
	if err := open(); err != nil {
		t.Fatalf("a duplicate delivery must be idempotent: %v", err)
	}
	var blockers []workdomain.WorkItemBlocker
	if err := fx.uow.WithReadOnly(fx.ctx, func(tx ports.Tx) error {
		var err error
		blockers, err = tx.Work().ListWorkItemBlockersForWorkItem(fx.ctx, fx.workItemID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	opened := 0
	for _, blocker := range blockers {
		if blocker.Type == workdomain.BlockerReleaseFailed {
			opened++
			if blocker.State != workdomain.BlockerOpen || blocker.SourceRunID != fx.runID || blocker.Type.Waivable() {
				t.Errorf("blocker = %+v, want OPEN, sourced at the run, never waivable", blocker)
			}
		}
	}
	if opened != 1 {
		t.Fatalf("RELEASE_FAILED blockers = %d, want exactly 1", opened)
	}
}

func TestOpenReleaseFailedBlockerTx_OpensNothingForAManualCommit(t *testing.T) {
	fx := newReleaseFixture(t, offeredOnApproved)
	intent := workdomain.ReleaseSetLocalCommit{ID: "manual-op", ProjectID: "project-1"}
	if err := fx.uow.WithSerializedWrite(fx.ctx, func(tx ports.Tx) error {
		return runtime.OpenReleaseFailedBlockerTx(fx.ctx, tx, intent, workdomain.FailureNoChanges)
	}); err != nil {
		t.Fatalf("a manual commit has no selection record, so the hook has nothing to do: %v", err)
	}
}
