package runtime_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/app/workspaceprovision"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// V4-12A's own "concurrent approval, duplicate delivery, restart và
// negative sibling/effective-scope" tests (docs/design/06-v4-runtime-engine.md).

// scopeExpansionProvider mirrors internal/app/work's own scriptedProvider
// (scope_expansion_workspaceprovision_test.go), duplicated locally rather
// than imported — that file's own doc comment already establishes this
// precedent: proving two packages compose correctly across a boundary is
// this file's own job, not re-testing either package's own already-covered
// internals. Only repo-2's revision is ever scripted here: repo-1's own
// RepositoryWorkspace row is already durably captured by readyFixture's own
// real workspaceprovision.Handler run, before this file's own fixture ever
// starts.
type scopeExpansionProvider struct {
	revision workspace.Revision
}

func (s *scopeExpansionProvider) Provision(_ context.Context, spec ports.ProvisionSpec) (ports.WorkspaceHandle, error) {
	return ports.NewWorkspaceHandle("handle-" + string(spec.RepositoryID))
}

func (s *scopeExpansionProvider) Inspect(context.Context, ports.WorkspaceHandle) (ports.WorkspaceInspection, error) {
	return ports.WorkspaceInspection{}, errors.New("scopeExpansionProvider: Inspect must not be called")
}

func (s *scopeExpansionProvider) CaptureRevision(context.Context, ports.WorkspaceHandle) (workspace.Revision, error) {
	return s.revision, nil
}

func (s *scopeExpansionProvider) Diff(context.Context, ports.WorkspaceHandle, workspace.Revision) (ports.WorkspaceDiff, error) {
	return ports.WorkspaceDiff{}, errors.New("scopeExpansionProvider: Diff must not be called")
}

func (s *scopeExpansionProvider) Release(context.Context, ports.WorkspaceHandle) error {
	return errors.New("scopeExpansionProvider: Release must not be called")
}

func (s *scopeExpansionProvider) WorkingDirectory(context.Context, ports.WorkspaceHandle) (string, error) {
	return "", errors.New("scopeExpansionProvider: WorkingDirectory must not be called")
}

// sameBranchToken compares two *runtimedomain.BranchTokenID by VALUE, never
// by pointer identity — the fake repository round-trips every NodeRun
// through its own clone() on every save/load, so two logically-equal
// BranchTokenID values read back from storage are never the same pointer.
func sameBranchToken(a, b *runtimedomain.BranchTokenID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// blockScopeExpansionFixture schedules and executes agentExecutableDocument's
// own "implement" AGENT node to a real, fenced BLOCKED finalize — a
// well-formed ScopeExpansionProposal asking for a brand-new repository
// ("repo-2") this family has never been granted at all. Returns everything
// each of this file's own tests continues differently from that point.
func blockScopeExpansionFixture(t *testing.T) (
	uow *fake.UnitOfWork, ids idsource.Source, runID, nodeRunID, attemptID string, requestJobID string,
) {
	t.Helper()
	ctx := context.Background()
	uow, ids, runID, nodeRunID, attemptID = scheduledExecutionFixture(t, 600)
	job := claimableExecuteNodeJob(t, uow, attemptID)

	executor := &fake.NodeExecutor{Result: ports.NodeExecutionResult{
		State: runtimedomain.ExecutionAttemptBlocked,
		RequestedScopeExpansion: &runtimedomain.ScopeExpansionProposal{
			RequestedGrants: []runtimedomain.ScopeGrantProposal{
				{RepositoryID: "repo-2", Access: "WRITE", PathScopes: []string{"services/repo-2"}, Reason: "need repo-2 too"},
			},
			Reason: "need repo-2 too",
		},
	}}
	handler := runtime.NewExecuteNodeHandler(uow, ids, executor, clock.System{}, fake.IsolationEnforcementChecker{}, sharedTestAgentRegistry(t))
	if err := handler.Handle(ctx, job); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if _, err := uow.Snapshot.Runtime().GetScopeExpansionOriginByAttemptID(ctx, attemptID); err != nil {
		t.Fatalf("GetScopeExpansionOriginByAttemptID: %v", err)
	}

	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	var requestJob *ports.EnqueueJobRequest
	for i := range jobs {
		if jobs[i].Kind == runtime.RequestScopeExpansionJobKind && jobs[i].AggregateID == attemptID {
			requestJob = &jobs[i]
		}
	}
	if requestJob == nil {
		t.Fatalf("no %s job found for attempt %s among %+v", runtime.RequestScopeExpansionJobKind, attemptID, jobs)
	}
	return uow, ids, runID, nodeRunID, attemptID, string(requestJob.ID)
}

// TestFinalizeExecutionAttempt_Blocked_CreatesOriginAndRequestsScopeExpansion
// proves the base BLOCKED transition itself: the Attempt/NodeRun both end
// BLOCKED (never FAILED, never consuming AttemptPolicy retry budget — no
// second Attempt is ever created), and a ScopeExpansionOrigin +
// REQUEST_SCOPE_EXPANSION job exist, both keyed on the same AttemptID.
func TestFinalizeExecutionAttempt_Blocked_CreatesOriginAndRequestsScopeExpansion(t *testing.T) {
	uow, _, _, nodeRunID, attemptID, _ := blockScopeExpansionFixture(t)
	ctx := context.Background()

	attempt, err := uow.Snapshot.Runtime().GetExecutionAttempt(ctx, attemptID)
	if err != nil {
		t.Fatalf("GetExecutionAttempt: %v", err)
	}
	if attempt.State != runtimedomain.ExecutionAttemptBlocked || attempt.TerminationReason != runtimedomain.TerminationReasonScopeExpansionRequired {
		t.Fatalf("attempt = %+v, want BLOCKED/SCOPE_EXPANSION_REQUIRED", attempt)
	}
	if attempts := uow.Snapshot.Runtime().(*fake.RuntimeRepository).Attempts(); len(attempts) != 1 {
		t.Fatalf("attempt count = %d, want exactly 1 (BLOCKED never spawns a retry attempt)", len(attempts))
	}
	nodeRun, err := uow.Snapshot.Runtime().GetNodeRun(ctx, nodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun: %v", err)
	}
	if nodeRun.State != runtimedomain.NodeRunBlocked {
		t.Fatalf("node run = %+v, want BLOCKED", nodeRun)
	}

	origin, err := uow.Snapshot.Runtime().GetScopeExpansionOriginByAttemptID(ctx, attemptID)
	if err != nil {
		t.Fatalf("GetScopeExpansionOriginByAttemptID: %v", err)
	}
	if origin.ReconcileStatus != runtimedomain.ScopeExpansionReconcilePending || origin.RequestID == "" {
		t.Fatalf("origin = %+v, want PENDING with a non-empty reserved RequestID", origin)
	}
}

// driveScopeExpansionRequestAndApproval runs the REAL REQUEST_SCOPE_EXPANSION
// job (raising a real work.ScopeExpansionRequest under the reserved
// RequestID) then the REAL work.ApproveScopeExpansion command for repo-2 —
// the shared middle section every "past BLOCKED" test in this file needs,
// regardless of what each one does with the approval afterwards.
func driveScopeExpansionRequestAndApproval(t *testing.T, uow *fake.UnitOfWork, ids idsource.Source, runID, nodeRunID, attemptID, requestJobID string) runtimedomain.ScopeExpansionOrigin {
	t.Helper()
	ctx := context.Background()

	// repo-2 must already be ACTIVE before RequestScopeExpansion itself ever
	// runs — that command's own same-project/ACTIVE-repository validation
	// (mirroring CreateRootWorkItem's own) rejects a proposal naming a
	// repository nobody has registered yet.
	mustCreateActiveRepository(t, uow, ids, "project-1", "repo-2")

	requestHandler := runtime.NewRequestScopeExpansionHandler(uow, ids)
	requestPayload, _ := json.Marshal(runtime.RequestScopeExpansionJobPayload{RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID})
	if err := requestHandler.Handle(ctx, ports.DurableJob{ID: ports.JobID(requestJobID), Kind: runtime.RequestScopeExpansionJobKind, Payload: requestPayload}); err != nil {
		t.Fatalf("RequestScopeExpansionHandler.Handle: %v", err)
	}

	origin, err := uow.Snapshot.Runtime().GetScopeExpansionOriginByAttemptID(ctx, attemptID)
	if err != nil {
		t.Fatalf("GetScopeExpansionOriginByAttemptID: %v", err)
	}

	var request workdomain.ScopeExpansionRequest
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		request, err = tx.Work().GetScopeExpansionRequest(ctx, origin.RequestID)
		return err
	}); err != nil {
		t.Fatalf("GetScopeExpansionRequest: %v", err)
	}
	if request.ID != workdomain.ScopeExpansionRequestID(origin.RequestID) || request.Status != workdomain.ScopeExpansionPending {
		t.Fatalf("request = %+v, want id=%s status=PENDING", request, origin.RequestID)
	}

	approveCmd := testCommand("idem-approve-"+attemptID, "hash-approve-"+attemptID, ports.ProjectScope("project-1"), "ApproveScopeExpansion")
	approveResult, err := work.ApproveScopeExpansion(ctx, uow, ids, approveCmd, work.ApproveScopeExpansionRequest{RequestID: origin.RequestID})
	if err != nil {
		t.Fatalf("ApproveScopeExpansion: %v", err)
	}
	if len(approveResult.ProvisionedRepositories) != 1 || approveResult.ProvisionedRepositories[0].RepositoryID != "repo-2" {
		t.Fatalf("ProvisionedRepositories = %+v, want exactly one entry for repo-2", approveResult.ProvisionedRepositories)
	}

	refreshed, err := uow.Snapshot.Runtime().GetScopeExpansionOriginByAttemptID(ctx, attemptID)
	if err != nil {
		t.Fatalf("GetScopeExpansionOriginByAttemptID (after approve): %v", err)
	}
	return refreshed
}

// findReconcileJob locates the SCOPE_EXPANSION_RECONCILE job for attemptID
// at the given generation among every job currently enqueued.
func findReconcileJob(t *testing.T, uow *fake.UnitOfWork, attemptID string, generation uint64) ports.DurableJob {
	t.Helper()
	wantKey := fmt.Sprintf("scope-expansion-reconcile:%s:%d", attemptID, generation)
	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	for i := range jobs {
		if jobs[i].Kind == work.ScopeExpansionReconcileJobKind && jobs[i].IdempotencyKey == wantKey {
			return ports.DurableJob{ID: jobs[i].ID, AggregateType: jobs[i].AggregateType, AggregateID: jobs[i].AggregateID, Payload: jobs[i].Payload}
		}
	}
	t.Fatalf("no %s job with IdempotencyKey %s among %+v", work.ScopeExpansionReconcileJobKind, wantKey, jobs)
	return ports.DurableJob{}
}

// driveRepo2Provisioning finds the exact WORKSPACE_PROVISION job
// ApproveScopeExpansion itself enqueued for repo-2 (never reconstructed by
// hand — its payload already carries the real WorkspaceSetID/FamilyID this
// test has no other easy way to name) and runs it through the REAL,
// unmodified workspaceprovision.Handler.
func driveRepo2Provisioning(t *testing.T, uow *fake.UnitOfWork, ids idsource.Source, provisionJobID string) {
	t.Helper()
	ctx := context.Background()
	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	var job *ports.EnqueueJobRequest
	for i := range jobs {
		if string(jobs[i].ID) == provisionJobID {
			job = &jobs[i]
		}
	}
	if job == nil {
		t.Fatalf("no job with id %s among %+v", provisionJobID, jobs)
	}
	provider := &scopeExpansionProvider{revision: workspace.Revision{
		RepositoryID: "repo-2", VCSObjectID: "2222222222222222222222222222222222222222", WorkspaceGeneration: 1,
	}}
	handler := workspaceprovision.New(uow, ids, provider)
	if err := handler.Handle(ctx, ports.DurableJob{ID: job.ID, AggregateType: job.AggregateType, AggregateID: job.AggregateID, Payload: job.Payload}); err != nil {
		t.Fatalf("workspaceprovision.Handle(repo-2): %v", err)
	}
}

// findRepo2ProvisionJobID decodes every enqueued WORKSPACE_PROVISION job's
// own payload to find the one naming repo-2 — never assumed by position or
// recency, since a test may have other provisioning jobs enqueued earlier
// (readyFixture's own repo-1 job) still present in the fake's own job list.
func findRepo2ProvisionJobID(t *testing.T, uow *fake.UnitOfWork) string {
	t.Helper()
	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	for _, j := range jobs {
		if j.Kind != work.WorkspaceProvisionJobKind {
			continue
		}
		var payload struct {
			RepositoryID string `json:"repositoryId"`
		}
		if json.Unmarshal(j.Payload, &payload) == nil && payload.RepositoryID == "repo-2" {
			return string(j.ID)
		}
	}
	t.Fatalf("no repo-2 %s job found among %+v", work.WorkspaceProvisionJobKind, jobs)
	return ""
}

// TestScopeExpansion_EndToEnd_NewRepositoryProvisionedReactivatesNodeRun is
// this task's own central proof, driving the REAL, completely unmodified
// pipeline every step of the way: BLOCKED -> REQUEST_SCOPE_EXPANSION job
// raises a real work.ScopeExpansionRequest under the RESERVED RequestID ->
// ApproveScopeExpansion (V3-08, unmodified except for this task's own
// small reconcile-job-enqueue addition) bumps ScopeVersion and enqueues a
// REAL WORKSPACE_PROVISION job for repo-2 -> workspaceprovision.Handler
// (V3-06, completely unmodified) provisions it for real and the
// WorkspaceSet reaches READY with an EXPANDED BaseRevisionSet ->
// SCOPE_EXPANSION_RECONCILE reactivates: a NEW NodeRun exists with
// ReactivationReason=SCOPE_EXPANDED, the SAME NodeKey/Iteration as the
// original, and an EffectiveScope that now includes repo-2 — while the
// ORIGINAL Attempt/NodeRun stay BLOCKED forever (AK-ARCH-015A's own "not
// revived").
func TestScopeExpansion_EndToEnd_NewRepositoryProvisionedReactivatesNodeRun(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, nodeRunID, attemptID, requestJobID := blockScopeExpansionFixture(t)

	origin := driveScopeExpansionRequestAndApproval(t, uow, ids, runID, nodeRunID, attemptID, requestJobID)

	firstReconcileJob := findReconcileJob(t, uow, attemptID, 0)
	reconcileHandler := runtime.NewScopeExpansionReconcileHandler(uow, ids)

	// First delivery: WorkspaceSet is still PROVISIONING (repo-2's own job
	// has not run yet) — must self-reschedule, never reactivate early.
	if err := reconcileHandler.Handle(ctx, firstReconcileJob); err != nil {
		t.Fatalf("ScopeExpansionReconcileHandler.Handle (still provisioning): %v", err)
	}
	originAfterFirstPoll, err := uow.Snapshot.Runtime().GetScopeExpansionOriginByAttemptID(ctx, attemptID)
	if err != nil {
		t.Fatalf("GetScopeExpansionOriginByAttemptID (after first poll): %v", err)
	}
	if originAfterFirstPoll.ReconcileStatus != runtimedomain.ScopeExpansionReconcilePending || originAfterFirstPoll.PollGeneration != 1 {
		t.Fatalf("origin after first poll = %+v, want PENDING/pollGeneration=1", originAfterFirstPoll)
	}
	if originAfterFirstPoll.ReactivatedNodeRunID != nil {
		t.Fatalf("origin after first poll = %+v, want no reactivation yet (still PROVISIONING)", originAfterFirstPoll)
	}

	// Now provision repo-2 for real — WorkspaceSet reaches READY with an
	// expanded BaseRevisionSet.
	driveRepo2Provisioning(t, uow, ids, findRepo2ProvisionJobID(t, uow))
	var set workspace.WorkspaceSet
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		set, err = tx.Work().GetWorkspaceSetByFamilyID(ctx, origin.FamilyID)
		return err
	}); err != nil {
		t.Fatalf("GetWorkspaceSetByFamilyID: %v", err)
	}
	if set.State != workspace.WorkspaceSetReady {
		t.Fatalf("workspace set state = %s, want READY", set.State)
	}

	// Second delivery (generation 1, the successor the first poll enqueued):
	// must now reactivate.
	secondReconcileJob := findReconcileJob(t, uow, attemptID, 1)
	if err := reconcileHandler.Handle(ctx, secondReconcileJob); err != nil {
		t.Fatalf("ScopeExpansionReconcileHandler.Handle (ready): %v", err)
	}

	finalOrigin, err := uow.Snapshot.Runtime().GetScopeExpansionOriginByAttemptID(ctx, attemptID)
	if err != nil {
		t.Fatalf("GetScopeExpansionOriginByAttemptID (final): %v", err)
	}
	if finalOrigin.ReconcileStatus != runtimedomain.ScopeExpansionReconcileReactivated || finalOrigin.ReactivatedNodeRunID == nil {
		t.Fatalf("final origin = %+v, want REACTIVATED with a non-nil ReactivatedNodeRunID", finalOrigin)
	}
	reactivatedID := string(*finalOrigin.ReactivatedNodeRunID)

	reactivatedNodeRun, err := uow.Snapshot.Runtime().GetNodeRun(ctx, reactivatedID)
	if err != nil {
		t.Fatalf("GetNodeRun(reactivated): %v", err)
	}
	originalNodeRun, err := uow.Snapshot.Runtime().GetNodeRun(ctx, nodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun(original): %v", err)
	}
	if reactivatedNodeRun.NodeKey != originalNodeRun.NodeKey || reactivatedNodeRun.Iteration != originalNodeRun.Iteration {
		t.Fatalf("reactivated node run = %+v, want same NodeKey/Iteration as original %+v", reactivatedNodeRun, originalNodeRun)
	}
	if reactivatedNodeRun.ReactivationReason != "SCOPE_EXPANDED" {
		t.Fatalf("reactivated node run ReactivationReason = %q, want SCOPE_EXPANDED", reactivatedNodeRun.ReactivationReason)
	}
	if reactivatedNodeRun.State != runtimedomain.NodeRunPending {
		t.Fatalf("reactivated node run state = %s, want PENDING (hands off to ScheduleExecutableNodeRun)", reactivatedNodeRun.State)
	}

	// The ORIGINAL Attempt/NodeRun are never revived.
	if originalNodeRun.State != runtimedomain.NodeRunBlocked {
		t.Fatalf("original node run = %+v, want unchanged BLOCKED", originalNodeRun)
	}
	originalAttempt, err := uow.Snapshot.Runtime().GetExecutionAttempt(ctx, attemptID)
	if err != nil {
		t.Fatalf("GetExecutionAttempt(original): %v", err)
	}
	if originalAttempt.State != runtimedomain.ExecutionAttemptBlocked {
		t.Fatalf("original attempt = %+v, want unchanged BLOCKED", originalAttempt)
	}

	// A ScheduleNodeRunJobKind job now exists for the reactivated NodeRun —
	// scope/manifest resolution is the EXISTING ScheduleExecutableNodeRun
	// pipeline's own job, not re-derived here.
	scheduleJobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	foundSchedule := false
	for _, j := range scheduleJobs {
		if j.Kind == runtime.ScheduleNodeRunJobKind && j.AggregateID == reactivatedID {
			foundSchedule = true
		}
	}
	if !foundSchedule {
		t.Fatalf("no %s job found for reactivated node run %s among %+v", runtime.ScheduleNodeRunJobKind, reactivatedID, scheduleJobs)
	}

	amendments, err := uow.Snapshot.Runtime().ListRunManifestAmendments(ctx, runID)
	if err != nil {
		t.Fatalf("ListRunManifestAmendments: %v", err)
	}
	if len(amendments) != 1 || amendments[0].Revision != 1 || amendments[0].ApprovedScopeVersion != 2 {
		t.Fatalf("amendments = %+v, want exactly 1 with Revision=1 ApprovedScopeVersion=2", amendments)
	}

	var scopes []workdomain.RepositoryScope
	if err := uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		scopes, err = tx.Work().ListFamilyRepositoryScopes(ctx, origin.FamilyID)
		return err
	}); err != nil {
		t.Fatalf("ListFamilyRepositoryScopes: %v", err)
	}
	foundRepo2 := false
	for _, s := range scopes {
		if string(s.RepositoryID()) == "repo-2" {
			foundRepo2 = true
		}
	}
	if !foundRepo2 {
		t.Fatalf("family scopes = %+v, want a repo-2 grant", scopes)
	}

	// V4-12C: this reactivation is also the ONLY path with authority to
	// resolve the SCOPE_EXPANSION_REQUIRED blocker requestScopeExpansionTx's
	// own BLOCKED branch opened — and it unblocks the WorkItem to ACTIVE
	// (never READY: the SAME Run resumes, it never stopped).
	run, err := uow.Snapshot.Runtime().GetWorkflowRun(ctx, runID)
	if err != nil {
		t.Fatalf("GetWorkflowRun: %v", err)
	}
	blockerID := attemptID + "-scope-expansion-blocker"
	blocker, err := uow.Snapshot.Work().GetWorkItemBlocker(ctx, blockerID)
	if err != nil {
		t.Fatalf("GetWorkItemBlocker: %v", err)
	}
	if blocker.State != workdomain.BlockerResolved || blocker.Type != workdomain.BlockerScopeExpansionRequired {
		t.Fatalf("blocker = %+v, want RESOLVED SCOPE_EXPANSION_REQUIRED", blocker)
	}
	item, err := uow.Snapshot.Work().GetWorkItem(ctx, string(run.WorkItemID))
	if err != nil {
		t.Fatalf("GetWorkItem: %v", err)
	}
	if item.Status != workdomain.WorkItemActive {
		t.Fatalf("work item status after reactivation = %s, want ACTIVE (the same Run resumes)", item.Status)
	}
}

// TestScopeExpansionReconcile_DuplicateDelivery_OnlyOneReactivation proves
// the fenced ReactivatedNodeRunID CAS: redelivering the EXACT SAME
// already-consumed reconcile job after reactivation already happened must
// never create a second NodeRun activation.
func TestScopeExpansionReconcile_DuplicateDelivery_OnlyOneReactivation(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, nodeRunID, attemptID, requestJobID := blockScopeExpansionFixture(t)
	driveScopeExpansionRequestAndApproval(t, uow, ids, runID, nodeRunID, attemptID, requestJobID)

	firstReconcileJob := findReconcileJob(t, uow, attemptID, 0)
	reconcileHandler := runtime.NewScopeExpansionReconcileHandler(uow, ids)
	if err := reconcileHandler.Handle(ctx, firstReconcileJob); err != nil {
		t.Fatalf("ScopeExpansionReconcileHandler.Handle (poll 1, still provisioning): %v", err)
	}

	driveRepo2Provisioning(t, uow, ids, findRepo2ProvisionJobID(t, uow))

	secondReconcileJob := findReconcileJob(t, uow, attemptID, 1)
	if err := reconcileHandler.Handle(ctx, secondReconcileJob); err != nil {
		t.Fatalf("ScopeExpansionReconcileHandler.Handle (poll 2, ready): %v", err)
	}

	origin, err := uow.Snapshot.Runtime().GetScopeExpansionOriginByAttemptID(ctx, attemptID)
	if err != nil {
		t.Fatalf("GetScopeExpansionOriginByAttemptID: %v", err)
	}
	if origin.ReconcileStatus != runtimedomain.ScopeExpansionReconcileReactivated || origin.ReactivatedNodeRunID == nil {
		t.Fatalf("origin = %+v, want REACTIVATED", origin)
	}
	firstReactivatedID := string(*origin.ReactivatedNodeRunID)

	nodeRunsBefore, err := uow.Snapshot.Runtime().ListNodeRunsForRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListNodeRunsForRun: %v", err)
	}
	countBefore := len(nodeRunsBefore)

	// Redeliver the SAME already-superseded generation-1 job a second time —
	// origin.ReconcileStatus is already REACTIVATED, not PENDING, so the
	// handler's own very first check must no-op.
	if err := reconcileHandler.Handle(ctx, secondReconcileJob); err != nil {
		t.Fatalf("ScopeExpansionReconcileHandler.Handle (duplicate delivery): %v", err)
	}

	originAfterDuplicate, err := uow.Snapshot.Runtime().GetScopeExpansionOriginByAttemptID(ctx, attemptID)
	if err != nil {
		t.Fatalf("GetScopeExpansionOriginByAttemptID (after duplicate): %v", err)
	}
	if string(*originAfterDuplicate.ReactivatedNodeRunID) != firstReactivatedID {
		t.Fatalf("ReactivatedNodeRunID changed after duplicate delivery: got %s, want unchanged %s",
			*originAfterDuplicate.ReactivatedNodeRunID, firstReactivatedID)
	}

	nodeRunsAfter, err := uow.Snapshot.Runtime().ListNodeRunsForRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListNodeRunsForRun (after duplicate): %v", err)
	}
	if len(nodeRunsAfter) != countBefore {
		t.Fatalf("node run count = %d after duplicate delivery, want unchanged %d (exactly one reactivation ever created)", len(nodeRunsAfter), countBefore)
	}
}

// TestScopeExpansionReconcile_Rejected_NeverReactivates proves the REJECTED
// path: an operator rejecting the scope expansion request leaves the
// Attempt/NodeRun BLOCKED forever — the origin transitions to REJECTED and
// no NodeRun is ever created (Alpha has no automatic resume path; only
// CancelRun gets the Run itself out of this state).
func TestScopeExpansionReconcile_Rejected_NeverReactivates(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, nodeRunID, attemptID, requestJobID := blockScopeExpansionFixture(t)
	mustCreateActiveRepository(t, uow, ids, "project-1", "repo-2")

	requestHandler := runtime.NewRequestScopeExpansionHandler(uow, ids)
	requestPayload, _ := json.Marshal(runtime.RequestScopeExpansionJobPayload{RunID: runID, NodeRunID: nodeRunID, AttemptID: attemptID})
	if err := requestHandler.Handle(ctx, ports.DurableJob{ID: ports.JobID(requestJobID), Kind: runtime.RequestScopeExpansionJobKind, Payload: requestPayload}); err != nil {
		t.Fatalf("RequestScopeExpansionHandler.Handle: %v", err)
	}
	origin, err := uow.Snapshot.Runtime().GetScopeExpansionOriginByAttemptID(ctx, attemptID)
	if err != nil {
		t.Fatalf("GetScopeExpansionOriginByAttemptID: %v", err)
	}

	rejectCmd := testCommand("idem-reject-"+attemptID, "hash-reject-"+attemptID, ports.ProjectScope("project-1"), "RejectScopeExpansion")
	if _, err := work.RejectScopeExpansion(ctx, uow, rejectCmd, work.RejectScopeExpansionRequest{
		RequestID: origin.RequestID, DecisionNote: "not now",
	}); err != nil {
		t.Fatalf("RejectScopeExpansion: %v", err)
	}

	// RejectScopeExpansion never enqueues a reconcile job itself (only
	// ApproveScopeExpansion does) — this test drives the reconcile job by
	// hand, standing in for an operator's earlier, already-in-flight poll
	// that only now observes the rejection, exactly as
	// ScopeExpansionReconcileHandler's own REJECTED/WITHDRAWN branch
	// documents.
	reconcilePayload, _ := json.Marshal(work.ScopeExpansionReconcileJobPayload{AttemptID: attemptID, PollGeneration: 0})
	reconcileHandler := runtime.NewScopeExpansionReconcileHandler(uow, ids)
	if err := reconcileHandler.Handle(ctx, ports.DurableJob{Payload: reconcilePayload}); err != nil {
		t.Fatalf("ScopeExpansionReconcileHandler.Handle: %v", err)
	}

	finalOrigin, err := uow.Snapshot.Runtime().GetScopeExpansionOriginByAttemptID(ctx, attemptID)
	if err != nil {
		t.Fatalf("GetScopeExpansionOriginByAttemptID (final): %v", err)
	}
	if finalOrigin.ReconcileStatus != runtimedomain.ScopeExpansionReconcileRejected || finalOrigin.ReactivatedNodeRunID != nil {
		t.Fatalf("origin = %+v, want REJECTED with no reactivation", finalOrigin)
	}

	nodeRun, err := uow.Snapshot.Runtime().GetNodeRun(ctx, nodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun: %v", err)
	}
	if nodeRun.State != runtimedomain.NodeRunBlocked {
		t.Fatalf("node run = %+v, want unchanged BLOCKED", nodeRun)
	}
	attempt, err := uow.Snapshot.Runtime().GetExecutionAttempt(ctx, attemptID)
	if err != nil {
		t.Fatalf("GetExecutionAttempt: %v", err)
	}
	if attempt.State != runtimedomain.ExecutionAttemptBlocked {
		t.Fatalf("attempt = %+v, want unchanged BLOCKED", attempt)
	}
}

// TestScopeExpansion_SiblingNodeRunsNeverTouchedByReactivation proves the
// other half of AK-ARCH-015A's own "không mở quyền cho sibling" bar:
// reactivating the one Attempt that actually raised the scope request must
// never touch any OTHER pre-existing NodeRun belonging to the same Run
// (this fixture's own START node and the original BLOCKED "implement"
// activation itself) — only ONE brand-new reactivated activation is ever
// created, with its own BranchTokenID copied unchanged from the original.
func TestScopeExpansion_SiblingNodeRunsNeverTouchedByReactivation(t *testing.T) {
	ctx := context.Background()
	uow, ids, runID, nodeRunID, attemptID, requestJobID := blockScopeExpansionFixture(t)

	blockedNodeRun, err := uow.Snapshot.Runtime().GetNodeRun(ctx, nodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun(blocked): %v", err)
	}

	driveScopeExpansionRequestAndApproval(t, uow, ids, runID, nodeRunID, attemptID, requestJobID)

	before, err := uow.Snapshot.Runtime().ListNodeRunsForRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListNodeRunsForRun (before): %v", err)
	}
	beforeByID := make(map[string]runtimedomain.NodeRun, len(before))
	for _, nr := range before {
		beforeByID[string(nr.ID)] = nr
	}

	firstReconcileJob := findReconcileJob(t, uow, attemptID, 0)
	reconcileHandler := runtime.NewScopeExpansionReconcileHandler(uow, ids)
	if err := reconcileHandler.Handle(ctx, firstReconcileJob); err != nil {
		t.Fatalf("ScopeExpansionReconcileHandler.Handle (poll 1): %v", err)
	}

	driveRepo2Provisioning(t, uow, ids, findRepo2ProvisionJobID(t, uow))

	secondReconcileJob := findReconcileJob(t, uow, attemptID, 1)
	if err := reconcileHandler.Handle(ctx, secondReconcileJob); err != nil {
		t.Fatalf("ScopeExpansionReconcileHandler.Handle (poll 2): %v", err)
	}

	after, err := uow.Snapshot.Runtime().ListNodeRunsForRun(ctx, runID)
	if err != nil {
		t.Fatalf("ListNodeRunsForRun (after): %v", err)
	}
	newCount := 0
	for _, nr := range after {
		prior, existed := beforeByID[string(nr.ID)]
		if !existed {
			newCount++
			if nr.ReactivationReason != "SCOPE_EXPANDED" {
				t.Fatalf("unexpected new NodeRun %+v, want the reactivated activation", nr)
			}
			if !sameBranchToken(nr.BranchTokenID, blockedNodeRun.BranchTokenID) {
				t.Fatalf("reactivated NodeRun BranchTokenID = %v, want copied from original %v", nr.BranchTokenID, blockedNodeRun.BranchTokenID)
			}
			continue
		}
		if nr.State != prior.State || nr.ReactivationReason != prior.ReactivationReason {
			t.Fatalf("pre-existing NodeRun %s changed: before=%+v after=%+v (a sibling must never be touched)", nr.ID, prior, nr)
		}
	}
	if newCount != 1 {
		t.Fatalf("new node run count = %d, want exactly 1 reactivated activation", newCount)
	}

	// The originally-BLOCKED NodeRun itself is unchanged — reactivation
	// always creates a NEW activation, never revives the old one.
	stillBlocked, err := uow.Snapshot.Runtime().GetNodeRun(ctx, nodeRunID)
	if err != nil {
		t.Fatalf("GetNodeRun(still blocked): %v", err)
	}
	if stillBlocked.State != runtimedomain.NodeRunBlocked || !sameBranchToken(stillBlocked.BranchTokenID, blockedNodeRun.BranchTokenID) {
		t.Fatalf("original node run = %+v, want unchanged BLOCKED with the same BranchTokenID %v", stillBlocked, blockedNodeRun.BranchTokenID)
	}
}
