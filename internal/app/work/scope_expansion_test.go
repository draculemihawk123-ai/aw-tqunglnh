package work_test

import (
	"context"
	"errors"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

func requestGrant(repositoryID string, access workdomain.RepositoryAccess) work.ScopeGrantRequest {
	return work.ScopeGrantRequest{
		RepositoryID: repositoryID, Access: string(access), PathScopes: []string{"services/" + repositoryID}, Reason: "expand scope",
	}
}

// createRootFixtureNamed mirrors commands_test.go's own createRootFixture,
// parameterized over ProjectID too — needed here because this file's own
// cross-family reference test needs a SECOND, genuinely different project
// (createRootFixture always seeds "project-1").
func createRootFixtureNamed(t *testing.T, uow *fake.UnitOfWork, ids idsource.Source, projectID, repositoryID string, access workdomain.RepositoryAccess, paths []string) work.CreateRootWorkItemResult {
	t.Helper()
	ctx := context.Background()
	mustCreateProject(t, uow, projectID)
	mustCreateActiveRepository(t, uow, ids, projectID, repositoryID)

	cmd := testCommand("idem-root-"+repositoryID, "hash-root-"+repositoryID, ports.ProjectScope(projectID), "CreateRootWorkItem")
	result, err := work.CreateRootWorkItem(ctx, uow, ids, cmd, work.CreateRootWorkItemRequest{
		ProjectID: projectID, Title: "Root task", InitialScope: []work.ScopeGrantRequest{
			{RepositoryID: repositoryID, Access: string(access), PathScopes: paths, Reason: "root scope"},
		},
	})
	if err != nil {
		t.Fatalf("CreateRootWorkItem: %v", err)
	}
	return result
}

// --- RequestScopeExpansion ---

func TestRequestScopeExpansion_HappyPath(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	root := createRootFixture(t, uow, ids, "repo-1", workdomain.RepositoryWrite, []string{"services/api"})
	mustCreateActiveRepository(t, uow, ids, "project-1", "repo-2")

	cmd := testCommand("idem-request-1", "hash-request-1", ports.ProjectScope("project-1"), "RequestScopeExpansion")
	result, err := work.RequestScopeExpansion(ctx, uow, ids, cmd, work.RequestScopeExpansionRequest{
		FamilyID: root.FamilyID, Reason: "need repo-2", RequestedGrants: []work.ScopeGrantRequest{requestGrant("repo-2", workdomain.RepositoryWrite)},
	})
	if err != nil {
		t.Fatalf("RequestScopeExpansion: %v", err)
	}
	if result.RequestID == "" || result.FamilyID != root.FamilyID || result.ProjectID != "project-1" {
		t.Fatalf("result = %+v, want non-empty RequestID with FamilyID=%s ProjectID=project-1", result, root.FamilyID)
	}
	if result.Status != string(workdomain.ScopeExpansionPending) {
		t.Fatalf("result.Status = %q, want PENDING", result.Status)
	}

	persisted, err := uow.Snapshot.Work().GetScopeExpansionRequest(ctx, result.RequestID)
	if err != nil {
		t.Fatalf("GetScopeExpansionRequest: %v", err)
	}
	if persisted.Status != workdomain.ScopeExpansionPending || len(persisted.RequestedGrants) != 1 || persisted.RequestedGrants[0].RepositoryID != "repo-2" {
		t.Fatalf("persisted request = %+v, want PENDING with exactly one grant for repo-2", persisted)
	}

	// family_repository_scopes/task_families.scope_version must be
	// completely untouched — requesting is not granting.
	family, err := uow.Snapshot.Work().GetTaskFamily(ctx, root.FamilyID)
	if err != nil {
		t.Fatalf("GetTaskFamily: %v", err)
	}
	if family.ScopeVersion != 1 {
		t.Fatalf("family.ScopeVersion = %d, want unchanged 1 (a request never grants)", family.ScopeVersion)
	}
	scopes, err := uow.Snapshot.Work().ListFamilyRepositoryScopes(ctx, root.FamilyID)
	if err != nil {
		t.Fatalf("ListFamilyRepositoryScopes: %v", err)
	}
	if len(scopes) != 1 {
		t.Fatalf("family scopes = %+v, want unchanged (still just the root's own initial grant)", scopes)
	}
}

func TestRequestScopeExpansion_CrossProjectRepository_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	root := createRootFixture(t, uow, ids, "repo-1", workdomain.RepositoryWrite, []string{"services/api"})
	mustCreateProject(t, uow, "project-2")
	mustCreateActiveRepository(t, uow, ids, "project-2", "repo-other")

	cmd := testCommand("idem-request-1", "hash-request-1", ports.ProjectScope("project-1"), "RequestScopeExpansion")
	_, err := work.RequestScopeExpansion(ctx, uow, ids, cmd, work.RequestScopeExpansionRequest{
		FamilyID: root.FamilyID, Reason: "cross project", RequestedGrants: []work.ScopeGrantRequest{requestGrant("repo-other", workdomain.RepositoryRead)},
	})
	if !errors.Is(err, ports.ErrCrossProjectReference) {
		t.Fatalf("err = %v, want ports.ErrCrossProjectReference (repo-other belongs to project-2)", err)
	}

	requests, err := uow.Snapshot.Work().ListFamilyScopeExpansionRequests(ctx, root.FamilyID)
	if err != nil {
		t.Fatalf("ListFamilyScopeExpansionRequests: %v", err)
	}
	if len(requests) != 0 {
		t.Fatalf("requests = %+v, want none (a rejected request must never persist)", requests)
	}
}

func TestRequestScopeExpansion_RepositoryNotActive_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	root := createRootFixture(t, uow, ids, "repo-1", workdomain.RepositoryWrite, []string{"services/api"})

	// RegisterRepository always starts REGISTERING — never activated here.
	regCmd := testCommand("idem-repo-2", "hash-repo-2", ports.ProjectScope("project-1"), "RegisterRepository")
	if _, err := catalog.RegisterRepository(ctx, uow, ids, regCmd, catalog.RegisterRepositoryRequest{
		RepositoryID: "repo-2", ProjectID: "project-1", Name: "repo-2",
		RemoteLocator: "https://example.invalid/repo-2.git", DefaultRef: "main",
	}); err != nil {
		t.Fatalf("RegisterRepository: %v", err)
	}

	cmd := testCommand("idem-request-1", "hash-request-1", ports.ProjectScope("project-1"), "RequestScopeExpansion")
	_, err := work.RequestScopeExpansion(ctx, uow, ids, cmd, work.RequestScopeExpansionRequest{
		FamilyID: root.FamilyID, Reason: "not active yet", RequestedGrants: []work.ScopeGrantRequest{requestGrant("repo-2", workdomain.RepositoryRead)},
	})
	if !errors.Is(err, work.ErrRepositoryNotActive) {
		t.Fatalf("err = %v, want work.ErrRepositoryNotActive (repo-2 is still REGISTERING)", err)
	}
}

func TestRequestScopeExpansion_ReferencedWorkItem_MustExistAndBeSameFamily(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	root := createRootFixture(t, uow, ids, "repo-1", workdomain.RepositoryWrite, []string{"services/api"})
	mustCreateActiveRepository(t, uow, ids, "project-1", "repo-2")

	// Missing entirely.
	missingCmd := testCommand("idem-request-missing", "hash-request-missing", ports.ProjectScope("project-1"), "RequestScopeExpansion")
	_, err := work.RequestScopeExpansion(ctx, uow, ids, missingCmd, work.RequestScopeExpansionRequest{
		FamilyID: root.FamilyID, Reason: "reference missing item", ReferencedWorkItemID: "work-item-missing",
		RequestedGrants: []work.ScopeGrantRequest{requestGrant("repo-2", workdomain.RepositoryRead)},
	})
	if !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("err = %v, want ports.ErrPersistenceNotFound (referenced work item does not exist)", err)
	}

	// Exists, but in a different family.
	otherRoot := createRootFixtureNamed(t, uow, ids, "project-2", "repo-3", workdomain.RepositoryWrite, []string{"services/api"})
	crossCmd := testCommand("idem-request-cross", "hash-request-cross", ports.ProjectScope("project-1"), "RequestScopeExpansion")
	_, err = work.RequestScopeExpansion(ctx, uow, ids, crossCmd, work.RequestScopeExpansionRequest{
		FamilyID: root.FamilyID, Reason: "reference cross-family item", ReferencedWorkItemID: otherRoot.WorkItemID,
		RequestedGrants: []work.ScopeGrantRequest{requestGrant("repo-2", workdomain.RepositoryRead)},
	})
	if !errors.Is(err, work.ErrCrossFamilyReference) {
		t.Fatalf("err = %v, want work.ErrCrossFamilyReference (work item belongs to a different family)", err)
	}

	// A reference to a real WorkItem in the SAME family succeeds.
	sameFamilyCmd := testCommand("idem-request-same", "hash-request-same", ports.ProjectScope("project-1"), "RequestScopeExpansion")
	result, err := work.RequestScopeExpansion(ctx, uow, ids, sameFamilyCmd, work.RequestScopeExpansionRequest{
		FamilyID: root.FamilyID, Reason: "reference same-family item", ReferencedWorkItemID: root.WorkItemID,
		RequestedGrants: []work.ScopeGrantRequest{requestGrant("repo-2", workdomain.RepositoryRead)},
	})
	if err != nil {
		t.Fatalf("RequestScopeExpansion (same family reference): %v", err)
	}
	persisted, err := uow.Snapshot.Work().GetScopeExpansionRequest(ctx, result.RequestID)
	if err != nil {
		t.Fatalf("GetScopeExpansionRequest: %v", err)
	}
	if persisted.ReferencedWorkItemID == nil || string(*persisted.ReferencedWorkItemID) != root.WorkItemID {
		t.Fatalf("persisted.ReferencedWorkItemID = %v, want %s", persisted.ReferencedWorkItemID, root.WorkItemID)
	}
}

func TestRequestScopeExpansion_DuplicateSameRequest_ReplaysWithoutNewRow(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	root := createRootFixture(t, uow, ids, "repo-1", workdomain.RepositoryWrite, []string{"services/api"})
	mustCreateActiveRepository(t, uow, ids, "project-1", "repo-2")

	cmd := testCommand("idem-request-1", "hash-request-1", ports.ProjectScope("project-1"), "RequestScopeExpansion")
	req := work.RequestScopeExpansionRequest{
		FamilyID: root.FamilyID, Reason: "need repo-2", RequestedGrants: []work.ScopeGrantRequest{requestGrant("repo-2", workdomain.RepositoryWrite)},
	}
	first, err := work.RequestScopeExpansion(ctx, uow, ids, cmd, req)
	if err != nil {
		t.Fatalf("first RequestScopeExpansion: %v", err)
	}
	second, err := work.RequestScopeExpansion(ctx, uow, ids, cmd, req)
	if err != nil {
		t.Fatalf("second (replayed) RequestScopeExpansion: %v", err)
	}
	if second.RequestID != first.RequestID {
		t.Fatalf("replayed result = %+v, want identical to first %+v", second, first)
	}
	requests, err := uow.Snapshot.Work().ListFamilyScopeExpansionRequests(ctx, root.FamilyID)
	if err != nil {
		t.Fatalf("ListFamilyScopeExpansionRequests: %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("requests after replay = %d, want 1 (a replay must never redo the mutation)", len(requests))
	}
}

// --- ApproveScopeExpansion ---

func TestApproveScopeExpansion_HappyPath_BumpsScopeVersionAndProvisionsNewRepository(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	root := createRootFixture(t, uow, ids, "repo-1", workdomain.RepositoryWrite, []string{"services/api"})
	mustCreateActiveRepository(t, uow, ids, "project-1", "repo-2")

	reqCmd := testCommand("idem-request-1", "hash-request-1", ports.ProjectScope("project-1"), "RequestScopeExpansion")
	reqResult, err := work.RequestScopeExpansion(ctx, uow, ids, reqCmd, work.RequestScopeExpansionRequest{
		FamilyID: root.FamilyID, Reason: "need repo-2", RequestedGrants: []work.ScopeGrantRequest{requestGrant("repo-2", workdomain.RepositoryWrite)},
	})
	if err != nil {
		t.Fatalf("RequestScopeExpansion: %v", err)
	}

	approveCmd := testCommand("idem-approve-1", "hash-approve-1", ports.ProjectScope("project-1"), "ApproveScopeExpansion")
	result, err := work.ApproveScopeExpansion(ctx, uow, ids, approveCmd, work.ApproveScopeExpansionRequest{RequestID: reqResult.RequestID})
	if err != nil {
		t.Fatalf("ApproveScopeExpansion: %v", err)
	}
	if result.NewScopeVersion != 2 {
		t.Fatalf("NewScopeVersion = %d, want 2", result.NewScopeVersion)
	}
	if len(result.ApprovedGrants) != 1 || result.ApprovedGrants[0].RepositoryID != "repo-2" {
		t.Fatalf("ApprovedGrants = %+v, want exactly one entry for repo-2", result.ApprovedGrants)
	}
	if len(result.ProvisionedRepositories) != 1 || result.ProvisionedRepositories[0].RepositoryID != "repo-2" {
		t.Fatalf("ProvisionedRepositories = %+v, want exactly one entry for repo-2", result.ProvisionedRepositories)
	}

	family, err := uow.Snapshot.Work().GetTaskFamily(ctx, root.FamilyID)
	if err != nil {
		t.Fatalf("GetTaskFamily: %v", err)
	}
	if family.ScopeVersion != 2 {
		t.Fatalf("family.ScopeVersion = %d, want 2", family.ScopeVersion)
	}

	scopes, err := uow.Snapshot.Work().ListFamilyRepositoryScopes(ctx, root.FamilyID)
	if err != nil {
		t.Fatalf("ListFamilyRepositoryScopes: %v", err)
	}
	if len(scopes) != 2 {
		t.Fatalf("family scopes = %+v, want 2 (original repo-1 grant untouched, new repo-2 grant added)", scopes)
	}
	var newScope *workdomain.RepositoryScope
	for i := range scopes {
		if scopes[i].RepositoryID() == "repo-2" {
			newScope = &scopes[i]
		}
	}
	if newScope == nil || newScope.AddedInScopeVersion() != 2 {
		t.Fatalf("repo-2 scope = %+v, want AddedInScopeVersion=2", newScope)
	}

	request, err := uow.Snapshot.Work().GetScopeExpansionRequest(ctx, reqResult.RequestID)
	if err != nil {
		t.Fatalf("GetScopeExpansionRequest: %v", err)
	}
	if request.Status != workdomain.ScopeExpansionApproved {
		t.Fatalf("request.Status = %q, want APPROVED", request.Status)
	}
	if request.ApprovedScopeVersion == nil || *request.ApprovedScopeVersion != 2 {
		t.Fatalf("request.ApprovedScopeVersion = %v, want 2", request.ApprovedScopeVersion)
	}
}

// TestApproveScopeExpansion_UpgradeOnly_NoNewProvisionJob is the positive
// half of "newly-added vs. upgraded": a grant that only widens access/paths
// on a repository the family ALREADY has SOME grant for must still mint a
// new RepositoryScope row at the new ScopeVersion, but must NEVER enqueue a
// second WORKSPACE_PROVISION job for it (no new worktree is needed).
func TestApproveScopeExpansion_UpgradeOnly_NoNewProvisionJob(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	root := createRootFixture(t, uow, ids, "repo-1", workdomain.RepositoryRead, []string{"services/api"})

	provisionJobsBefore := len(provisionJobsOnly(uow.Snapshot.Jobs().(*fake.JobsRepository).Items()))

	reqCmd := testCommand("idem-request-1", "hash-request-1", ports.ProjectScope("project-1"), "RequestScopeExpansion")
	reqResult, err := work.RequestScopeExpansion(ctx, uow, ids, reqCmd, work.RequestScopeExpansionRequest{
		FamilyID: root.FamilyID, Reason: "upgrade to WRITE",
		RequestedGrants: []work.ScopeGrantRequest{{RepositoryID: "repo-1", Access: string(workdomain.RepositoryWrite), PathScopes: []string{"services/api"}, Reason: "upgrade"}},
	})
	if err != nil {
		t.Fatalf("RequestScopeExpansion: %v", err)
	}
	approveCmd := testCommand("idem-approve-1", "hash-approve-1", ports.ProjectScope("project-1"), "ApproveScopeExpansion")
	result, err := work.ApproveScopeExpansion(ctx, uow, ids, approveCmd, work.ApproveScopeExpansionRequest{RequestID: reqResult.RequestID})
	if err != nil {
		t.Fatalf("ApproveScopeExpansion: %v", err)
	}
	if len(result.ProvisionedRepositories) != 0 {
		t.Fatalf("ProvisionedRepositories = %+v, want none (repo-1 already had a grant, no new worktree needed)", result.ProvisionedRepositories)
	}

	scopes, err := uow.Snapshot.Work().ListFamilyRepositoryScopes(ctx, root.FamilyID)
	if err != nil {
		t.Fatalf("ListFamilyRepositoryScopes: %v", err)
	}
	if len(scopes) != 2 {
		t.Fatalf("family scopes = %+v, want 2 (original READ grant untouched at v1, new WRITE grant added at v2)", scopes)
	}

	provisionJobsAfter := len(provisionJobsOnly(uow.Snapshot.Jobs().(*fake.JobsRepository).Items()))
	if provisionJobsAfter != provisionJobsBefore {
		t.Fatalf("provision jobs after upgrade-only approval = %d, want unchanged from %d", provisionJobsAfter, provisionJobsBefore)
	}
}

// TestApproveScopeExpansion_DuplicateApproval_Rejected is this task's own
// explicit "duplicate approval" Verify-line requirement: a second
// ApproveScopeExpansion against an already-APPROVED request must fail
// cleanly, never silently re-provision or double-increment ScopeVersion.
func TestApproveScopeExpansion_DuplicateApproval_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	root := createRootFixture(t, uow, ids, "repo-1", workdomain.RepositoryWrite, []string{"services/api"})
	mustCreateActiveRepository(t, uow, ids, "project-1", "repo-2")

	reqCmd := testCommand("idem-request-1", "hash-request-1", ports.ProjectScope("project-1"), "RequestScopeExpansion")
	reqResult, err := work.RequestScopeExpansion(ctx, uow, ids, reqCmd, work.RequestScopeExpansionRequest{
		FamilyID: root.FamilyID, Reason: "need repo-2", RequestedGrants: []work.ScopeGrantRequest{requestGrant("repo-2", workdomain.RepositoryWrite)},
	})
	if err != nil {
		t.Fatalf("RequestScopeExpansion: %v", err)
	}

	firstApproveCmd := testCommand("idem-approve-1", "hash-approve-1", ports.ProjectScope("project-1"), "ApproveScopeExpansion")
	if _, err := work.ApproveScopeExpansion(ctx, uow, ids, firstApproveCmd, work.ApproveScopeExpansionRequest{RequestID: reqResult.RequestID}); err != nil {
		t.Fatalf("first ApproveScopeExpansion: %v", err)
	}

	// A second approval attempt with a DIFFERENT idempotency key (never a
	// mere envelope replay) must be rejected outright.
	secondApproveCmd := testCommand("idem-approve-2", "hash-approve-2", ports.ProjectScope("project-1"), "ApproveScopeExpansion")
	_, err = work.ApproveScopeExpansion(ctx, uow, ids, secondApproveCmd, work.ApproveScopeExpansionRequest{RequestID: reqResult.RequestID})
	if !errors.Is(err, work.ErrScopeExpansionNotPending) {
		t.Fatalf("second ApproveScopeExpansion err = %v, want work.ErrScopeExpansionNotPending", err)
	}

	family, err := uow.Snapshot.Work().GetTaskFamily(ctx, root.FamilyID)
	if err != nil {
		t.Fatalf("GetTaskFamily: %v", err)
	}
	if family.ScopeVersion != 2 {
		t.Fatalf("family.ScopeVersion after duplicate approval attempt = %d, want unchanged 2 (never double-incremented)", family.ScopeVersion)
	}
	scopes, err := uow.Snapshot.Work().ListFamilyRepositoryScopes(ctx, root.FamilyID)
	if err != nil {
		t.Fatalf("ListFamilyRepositoryScopes: %v", err)
	}
	if len(scopes) != 2 {
		t.Fatalf("family scopes after duplicate approval attempt = %+v, want unchanged 2 (never double-granted)", scopes)
	}
}

func TestApproveScopeExpansion_RejectedRequest_CannotBeApproved(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	root := createRootFixture(t, uow, ids, "repo-1", workdomain.RepositoryWrite, []string{"services/api"})
	mustCreateActiveRepository(t, uow, ids, "project-1", "repo-2")

	reqCmd := testCommand("idem-request-1", "hash-request-1", ports.ProjectScope("project-1"), "RequestScopeExpansion")
	reqResult, err := work.RequestScopeExpansion(ctx, uow, ids, reqCmd, work.RequestScopeExpansionRequest{
		FamilyID: root.FamilyID, Reason: "need repo-2", RequestedGrants: []work.ScopeGrantRequest{requestGrant("repo-2", workdomain.RepositoryWrite)},
	})
	if err != nil {
		t.Fatalf("RequestScopeExpansion: %v", err)
	}
	rejectCmd := testCommand("idem-reject-1", "hash-reject-1", ports.ProjectScope("project-1"), "RejectScopeExpansion")
	if _, err := work.RejectScopeExpansion(ctx, uow, rejectCmd, work.RejectScopeExpansionRequest{RequestID: reqResult.RequestID, DecisionNote: "not needed"}); err != nil {
		t.Fatalf("RejectScopeExpansion: %v", err)
	}

	approveCmd := testCommand("idem-approve-1", "hash-approve-1", ports.ProjectScope("project-1"), "ApproveScopeExpansion")
	_, err = work.ApproveScopeExpansion(ctx, uow, ids, approveCmd, work.ApproveScopeExpansionRequest{RequestID: reqResult.RequestID})
	if !errors.Is(err, work.ErrScopeExpansionNotPending) {
		t.Fatalf("ApproveScopeExpansion (after reject) err = %v, want work.ErrScopeExpansionNotPending", err)
	}
}

// --- RejectScopeExpansion ---

func TestRejectScopeExpansion_HappyPath(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	root := createRootFixture(t, uow, ids, "repo-1", workdomain.RepositoryWrite, []string{"services/api"})
	mustCreateActiveRepository(t, uow, ids, "project-1", "repo-2")

	reqCmd := testCommand("idem-request-1", "hash-request-1", ports.ProjectScope("project-1"), "RequestScopeExpansion")
	reqResult, err := work.RequestScopeExpansion(ctx, uow, ids, reqCmd, work.RequestScopeExpansionRequest{
		FamilyID: root.FamilyID, Reason: "need repo-2", RequestedGrants: []work.ScopeGrantRequest{requestGrant("repo-2", workdomain.RepositoryWrite)},
	})
	if err != nil {
		t.Fatalf("RequestScopeExpansion: %v", err)
	}

	rejectCmd := testCommand("idem-reject-1", "hash-reject-1", ports.ProjectScope("project-1"), "RejectScopeExpansion")
	result, err := work.RejectScopeExpansion(ctx, uow, rejectCmd, work.RejectScopeExpansionRequest{RequestID: reqResult.RequestID, DecisionNote: "budget too tight"})
	if err != nil {
		t.Fatalf("RejectScopeExpansion: %v", err)
	}
	if result.Status != string(workdomain.ScopeExpansionRejected) {
		t.Fatalf("result.Status = %q, want REJECTED", result.Status)
	}

	request, err := uow.Snapshot.Work().GetScopeExpansionRequest(ctx, reqResult.RequestID)
	if err != nil {
		t.Fatalf("GetScopeExpansionRequest: %v", err)
	}
	if request.Status != workdomain.ScopeExpansionRejected || request.DecisionNote != "budget too tight" {
		t.Fatalf("persisted request = %+v, want REJECTED with the decision note", request)
	}

	family, err := uow.Snapshot.Work().GetTaskFamily(ctx, root.FamilyID)
	if err != nil {
		t.Fatalf("GetTaskFamily: %v", err)
	}
	if family.ScopeVersion != 1 {
		t.Fatalf("family.ScopeVersion after rejection = %d, want unchanged 1 (no scope change)", family.ScopeVersion)
	}
}

func TestRejectScopeExpansion_DuplicateRejection_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	root := createRootFixture(t, uow, ids, "repo-1", workdomain.RepositoryWrite, []string{"services/api"})
	mustCreateActiveRepository(t, uow, ids, "project-1", "repo-2")

	reqCmd := testCommand("idem-request-1", "hash-request-1", ports.ProjectScope("project-1"), "RequestScopeExpansion")
	reqResult, err := work.RequestScopeExpansion(ctx, uow, ids, reqCmd, work.RequestScopeExpansionRequest{
		FamilyID: root.FamilyID, Reason: "need repo-2", RequestedGrants: []work.ScopeGrantRequest{requestGrant("repo-2", workdomain.RepositoryWrite)},
	})
	if err != nil {
		t.Fatalf("RequestScopeExpansion: %v", err)
	}
	firstCmd := testCommand("idem-reject-1", "hash-reject-1", ports.ProjectScope("project-1"), "RejectScopeExpansion")
	if _, err := work.RejectScopeExpansion(ctx, uow, firstCmd, work.RejectScopeExpansionRequest{RequestID: reqResult.RequestID, DecisionNote: "first"}); err != nil {
		t.Fatalf("first RejectScopeExpansion: %v", err)
	}
	secondCmd := testCommand("idem-reject-2", "hash-reject-2", ports.ProjectScope("project-1"), "RejectScopeExpansion")
	_, err = work.RejectScopeExpansion(ctx, uow, secondCmd, work.RejectScopeExpansionRequest{RequestID: reqResult.RequestID, DecisionNote: "second"})
	if !errors.Is(err, work.ErrScopeExpansionNotPending) {
		t.Fatalf("second RejectScopeExpansion err = %v, want work.ErrScopeExpansionNotPending", err)
	}
}

// --- WithdrawScopeExpansion ---

func TestWithdrawScopeExpansion_HappyPath(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	root := createRootFixture(t, uow, ids, "repo-1", workdomain.RepositoryWrite, []string{"services/api"})
	mustCreateActiveRepository(t, uow, ids, "project-1", "repo-2")

	reqCmd := testCommand("idem-request-1", "hash-request-1", ports.ProjectScope("project-1"), "RequestScopeExpansion")
	reqResult, err := work.RequestScopeExpansion(ctx, uow, ids, reqCmd, work.RequestScopeExpansionRequest{
		FamilyID: root.FamilyID, Reason: "need repo-2", RequestedGrants: []work.ScopeGrantRequest{requestGrant("repo-2", workdomain.RepositoryWrite)},
	})
	if err != nil {
		t.Fatalf("RequestScopeExpansion: %v", err)
	}

	withdrawCmd := testCommand("idem-withdraw-1", "hash-withdraw-1", ports.ProjectScope("project-1"), "WithdrawScopeExpansion")
	result, err := work.WithdrawScopeExpansion(ctx, uow, withdrawCmd, work.WithdrawScopeExpansionRequest{RequestID: reqResult.RequestID})
	if err != nil {
		t.Fatalf("WithdrawScopeExpansion: %v", err)
	}
	if result.Status != string(workdomain.ScopeExpansionWithdrawn) {
		t.Fatalf("result.Status = %q, want WITHDRAWN", result.Status)
	}

	// Once withdrawn, it can never be approved.
	approveCmd := testCommand("idem-approve-1", "hash-approve-1", ports.ProjectScope("project-1"), "ApproveScopeExpansion")
	_, err = work.ApproveScopeExpansion(ctx, uow, ids, approveCmd, work.ApproveScopeExpansionRequest{RequestID: reqResult.RequestID})
	if !errors.Is(err, work.ErrScopeExpansionNotPending) {
		t.Fatalf("ApproveScopeExpansion (after withdraw) err = %v, want work.ErrScopeExpansionNotPending", err)
	}
}

// TestWithdrawScopeExpansion_SecondWithdraw_IdempotentNoOp is go-core-spec's
// own explicit "idempotent" bar for this exact command: a SECOND withdraw
// call (a genuinely different command invocation, not a byte-identical
// envelope replay) against an already-WITHDRAWN request succeeds harmlessly
// with the same result, never an error, and never a second event.
func TestWithdrawScopeExpansion_SecondWithdraw_IdempotentNoOp(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	root := createRootFixture(t, uow, ids, "repo-1", workdomain.RepositoryWrite, []string{"services/api"})
	mustCreateActiveRepository(t, uow, ids, "project-1", "repo-2")

	reqCmd := testCommand("idem-request-1", "hash-request-1", ports.ProjectScope("project-1"), "RequestScopeExpansion")
	reqResult, err := work.RequestScopeExpansion(ctx, uow, ids, reqCmd, work.RequestScopeExpansionRequest{
		FamilyID: root.FamilyID, Reason: "need repo-2", RequestedGrants: []work.ScopeGrantRequest{requestGrant("repo-2", workdomain.RepositoryWrite)},
	})
	if err != nil {
		t.Fatalf("RequestScopeExpansion: %v", err)
	}

	firstCmd := testCommand("idem-withdraw-1", "hash-withdraw-1", ports.ProjectScope("project-1"), "WithdrawScopeExpansion")
	first, err := work.WithdrawScopeExpansion(ctx, uow, firstCmd, work.WithdrawScopeExpansionRequest{RequestID: reqResult.RequestID})
	if err != nil {
		t.Fatalf("first WithdrawScopeExpansion: %v", err)
	}

	// A DIFFERENT idempotency key — this is not the generic envelope replay,
	// it is a second, independent withdraw attempt.
	secondCmd := testCommand("idem-withdraw-2", "hash-withdraw-2", ports.ProjectScope("project-1"), "WithdrawScopeExpansion")
	second, err := work.WithdrawScopeExpansion(ctx, uow, secondCmd, work.WithdrawScopeExpansionRequest{RequestID: reqResult.RequestID})
	if err != nil {
		t.Fatalf("second WithdrawScopeExpansion (must be a no-op, not an error): %v", err)
	}
	if second.Status != first.Status {
		t.Fatalf("second withdraw result = %+v, want identical status to first %+v", second, first)
	}

	withdrawnEvents := 0
	for _, e := range uow.Snapshot.Events().(*fake.EventsRepository).Items() {
		if e.EventType == "ScopeExpansionWithdrawn" {
			withdrawnEvents++
		}
	}
	if withdrawnEvents != 1 {
		t.Fatalf("ScopeExpansionWithdrawn events = %d, want exactly 1 (the second call must never re-emit)", withdrawnEvents)
	}
}

func TestWithdrawScopeExpansion_AlreadyApproved_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	root := createRootFixture(t, uow, ids, "repo-1", workdomain.RepositoryWrite, []string{"services/api"})
	mustCreateActiveRepository(t, uow, ids, "project-1", "repo-2")

	reqCmd := testCommand("idem-request-1", "hash-request-1", ports.ProjectScope("project-1"), "RequestScopeExpansion")
	reqResult, err := work.RequestScopeExpansion(ctx, uow, ids, reqCmd, work.RequestScopeExpansionRequest{
		FamilyID: root.FamilyID, Reason: "need repo-2", RequestedGrants: []work.ScopeGrantRequest{requestGrant("repo-2", workdomain.RepositoryWrite)},
	})
	if err != nil {
		t.Fatalf("RequestScopeExpansion: %v", err)
	}
	approveCmd := testCommand("idem-approve-1", "hash-approve-1", ports.ProjectScope("project-1"), "ApproveScopeExpansion")
	if _, err := work.ApproveScopeExpansion(ctx, uow, ids, approveCmd, work.ApproveScopeExpansionRequest{RequestID: reqResult.RequestID}); err != nil {
		t.Fatalf("ApproveScopeExpansion: %v", err)
	}

	withdrawCmd := testCommand("idem-withdraw-1", "hash-withdraw-1", ports.ProjectScope("project-1"), "WithdrawScopeExpansion")
	_, err = work.WithdrawScopeExpansion(ctx, uow, withdrawCmd, work.WithdrawScopeExpansionRequest{RequestID: reqResult.RequestID})
	if !errors.Is(err, work.ErrScopeExpansionNotPending) {
		t.Fatalf("WithdrawScopeExpansion (after approve) err = %v, want work.ErrScopeExpansionNotPending", err)
	}
}
