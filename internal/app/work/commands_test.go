package work_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

func testCommand(idempotencyKey, requestHash string, scope ports.CommandScope, commandType string) ports.Command {
	return ports.Command{
		ID: "cmd-" + idempotencyKey, IdempotencyKey: idempotencyKey, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: scope, RequestedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Type: commandType, RequestHash: requestHash,
	}
}

func mustCreateProject(t *testing.T, uow *fake.UnitOfWork, id string) {
	t.Helper()
	err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(context.Background(), ports.CreateProjectRequest{ID: id, Name: "project " + id})
		return err
	})
	if err != nil {
		t.Fatalf("seed project %s: %v", id, err)
	}
}

// mustCreateActiveRepository registers a repository (RegisterRepository
// always starts REGISTERING) then drives it REGISTERING->PROBING->ACTIVE
// directly via tx.Catalog(), standing in for what V3-02's own
// REPOSITORY_PROBE job handler would otherwise do — test setup, not the
// behavior under test, mirroring internal/app/catalog/commands_test.go's
// own moveRepositoryToBlocked helper for the opposite (BLOCKED) outcome.
func mustCreateActiveRepository(t *testing.T, uow *fake.UnitOfWork, ids idsource.Source, projectID, repositoryID string) {
	t.Helper()
	ctx := context.Background()
	regCmd := testCommand("idem-repo-"+repositoryID, "hash-repo-"+repositoryID, ports.ProjectScope(projectID), "RegisterRepository")
	if _, err := catalog.RegisterRepository(ctx, uow, ids, regCmd, catalog.RegisterRepositoryRequest{
		RepositoryID: repositoryID, ProjectID: projectID, Name: repositoryID,
		RemoteLocator: "https://example.invalid/" + repositoryID + ".git", DefaultRef: "main",
	}); err != nil {
		t.Fatalf("RegisterRepository(%s): %v", repositoryID, err)
	}
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if _, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: repositoryID, ExpectedStatus: project.RepositoryRegistering, ExpectedVersion: 1,
			NextStatus: project.RepositoryProbing, LastProbeErrorCode: nil,
		}); err != nil {
			return err
		}
		_, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: repositoryID, ExpectedStatus: project.RepositoryProbing, ExpectedVersion: 2,
			NextStatus: project.RepositoryActive, LastProbeErrorCode: nil,
		})
		return err
	})
	if err != nil {
		t.Fatalf("activate repository %s: %v", repositoryID, err)
	}
}

func baseRequest(projectID string, grants ...work.ScopeGrantRequest) work.CreateRootWorkItemRequest {
	return work.CreateRootWorkItemRequest{
		ProjectID: projectID, Title: "Implement the thing", InitialScope: grants,
	}
}

func grant(repositoryID string) work.ScopeGrantRequest {
	return work.ScopeGrantRequest{
		RepositoryID: repositoryID, Access: string(workdomain.RepositoryWrite),
		PathScopes: []string{"services/api"}, Reason: "implement the thing",
	}
}

// --- CreateRootWorkItem: happy path ---

func TestCreateRootWorkItem_CreatesWorkItemFamilyWorkspaceSetAndScopeAtomically(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	mustCreateProject(t, uow, "project-1")
	mustCreateActiveRepository(t, uow, ids, "project-1", "repo-1")

	cmd := testCommand("idem-root-1", "hash-a", ports.ProjectScope("project-1"), "CreateRootWorkItem")
	result, err := work.CreateRootWorkItem(ctx, uow, ids, cmd, baseRequest("project-1", grant("repo-1")))
	if err != nil {
		t.Fatalf("CreateRootWorkItem: %v", err)
	}
	if result.WorkItemID == "" || result.FamilyID == "" || result.WorkspaceSetID == "" {
		t.Fatalf("result = %+v, want non-empty WorkItemID/FamilyID/WorkspaceSetID", result)
	}
	if result.Status != string(workdomain.WorkItemBacklog) {
		t.Fatalf("result.Status = %q, want BACKLOG", result.Status)
	}
	if len(result.ProvisionedRepositories) != 1 || result.ProvisionedRepositories[0].RepositoryID != "repo-1" {
		t.Fatalf("result.ProvisionedRepositories = %+v, want exactly one entry for repo-1", result.ProvisionedRepositories)
	}
	if result.ProvisionedRepositories[0].ProvisionJobID == "" {
		t.Fatal("ProvisionJobID is empty, want a minted provision job id")
	}

	item, err := uow.Snapshot.Work().GetWorkItem(ctx, result.WorkItemID)
	if err != nil {
		t.Fatalf("GetWorkItem: %v", err)
	}
	if item.Kind != workdomain.WorkItemRoot || item.Status != workdomain.WorkItemBacklog || item.FamilyID != workdomain.TaskFamilyID(result.FamilyID) {
		t.Fatalf("persisted work item = %+v, want ROOT/BACKLOG with FamilyID=%s", item, result.FamilyID)
	}

	family, err := uow.Snapshot.Work().GetTaskFamily(ctx, result.FamilyID)
	if err != nil {
		t.Fatalf("GetTaskFamily: %v", err)
	}
	if family.Status != workdomain.TaskFamilyActive || family.ScopeVersion != 1 || family.RootWorkItemID != workdomain.WorkItemID(result.WorkItemID) {
		t.Fatalf("persisted family = %+v, want ACTIVE/ScopeVersion=1 rooted at %s", family, result.WorkItemID)
	}

	set, err := uow.Snapshot.Work().GetWorkspaceSetByFamilyID(ctx, result.FamilyID)
	if err != nil {
		t.Fatalf("GetWorkspaceSetByFamilyID: %v", err)
	}
	if set.State != workspace.WorkspaceSetRequested {
		t.Fatalf("persisted workspace set state = %q, want REQUESTED (this command only records intent)", set.State)
	}

	scopes, err := uow.Snapshot.Work().ListFamilyRepositoryScopes(ctx, result.FamilyID)
	if err != nil {
		t.Fatalf("ListFamilyRepositoryScopes: %v", err)
	}
	if len(scopes) != 1 || scopes[0].RepositoryID() != "repo-1" || scopes[0].AddedInScopeVersion() != 1 {
		t.Fatalf("persisted scopes = %+v, want exactly one repo-1 grant at scope version 1", scopes)
	}
	if scopes[0].Access() != workdomain.RepositoryWrite {
		t.Fatalf("persisted scope access = %q, want WRITE", scopes[0].Access())
	}

	provisionJobs := provisionJobsOnly(uow.Snapshot.Jobs().(*fake.JobsRepository).Items())
	if len(provisionJobs) != 1 || provisionJobs[0].AggregateType != "WorkspaceSet" || provisionJobs[0].AggregateID != result.WorkspaceSetID {
		t.Fatalf("provision jobs = %+v, want exactly one WORKSPACE_PROVISION job for the new workspace set", provisionJobs)
	}

	rootCreatedEvents := rootWorkItemCreatedEventsOnly(uow.Snapshot.Events().(*fake.EventsRepository).Items())
	if len(rootCreatedEvents) != 1 || rootCreatedEvents[0].AggregateID != result.WorkItemID {
		t.Fatalf("RootWorkItemCreated events = %+v, want exactly one for the new work item", rootCreatedEvents)
	}
}

// rootWorkItemCreatedEventsOnly filters an event list down to just this
// command's own RootWorkItemCreated events, excluding whatever
// RepositoryRegistered events test setup (mustCreateActiveRepository/
// RegisterRepository) already appended.
func rootWorkItemCreatedEventsOnly(events []ports.DomainEvent) []ports.DomainEvent {
	var result []ports.DomainEvent
	for _, e := range events {
		if e.EventType == "RootWorkItemCreated" {
			result = append(result, e)
		}
	}
	return result
}

// TestCreateRootWorkItem_MultiRepoFixture is AK-ARCH-011 made concrete: "Project
// hai repository tạo được TaskFamily/WorkspaceSet với hai worktree độc lập" —
// two repositories in the same project, both ACTIVE, both granted scope on
// one root WorkItem, each getting its own independent provision job.
func TestCreateRootWorkItem_MultiRepoFixture(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	mustCreateProject(t, uow, "project-1")
	mustCreateActiveRepository(t, uow, ids, "project-1", "repo-a")
	mustCreateActiveRepository(t, uow, ids, "project-1", "repo-b")

	cmd := testCommand("idem-root-1", "hash-a", ports.ProjectScope("project-1"), "CreateRootWorkItem")
	result, err := work.CreateRootWorkItem(ctx, uow, ids, cmd, baseRequest("project-1", grant("repo-a"), grant("repo-b")))
	if err != nil {
		t.Fatalf("CreateRootWorkItem: %v", err)
	}
	if len(result.ProvisionedRepositories) != 2 {
		t.Fatalf("ProvisionedRepositories = %+v, want exactly 2 (one per repository)", result.ProvisionedRepositories)
	}
	seenRepos := map[string]bool{}
	seenJobs := map[string]bool{}
	for _, p := range result.ProvisionedRepositories {
		if seenRepos[p.RepositoryID] {
			t.Fatalf("duplicate repository %q in provisioned list", p.RepositoryID)
		}
		seenRepos[p.RepositoryID] = true
		if p.ProvisionJobID == "" || seenJobs[p.ProvisionJobID] {
			t.Fatalf("empty or duplicate provision job id %q for repository %q", p.ProvisionJobID, p.RepositoryID)
		}
		seenJobs[p.ProvisionJobID] = true
	}
	if !seenRepos["repo-a"] || !seenRepos["repo-b"] {
		t.Fatalf("provisioned repositories = %+v, want both repo-a and repo-b", result.ProvisionedRepositories)
	}

	scopes, err := uow.Snapshot.Work().ListFamilyRepositoryScopes(ctx, result.FamilyID)
	if err != nil {
		t.Fatalf("ListFamilyRepositoryScopes: %v", err)
	}
	if len(scopes) != 2 {
		t.Fatalf("persisted scopes = %+v, want exactly 2 (independent grants for each repository)", scopes)
	}

	provisionJobs := provisionJobsOnly(uow.Snapshot.Jobs().(*fake.JobsRepository).Items())
	if len(provisionJobs) != 2 {
		t.Fatalf("provision jobs = %+v, want exactly 2 (one WORKSPACE_PROVISION per repository — independent worktrees)", provisionJobs)
	}
}

// provisionJobsOnly filters a job list down to just this command's own
// WORKSPACE_PROVISION jobs, excluding whatever REPOSITORY_PROBE jobs test
// setup (mustCreateActiveRepository/RegisterRepository) already enqueued.
func provisionJobsOnly(jobs []ports.EnqueueJobRequest) []ports.EnqueueJobRequest {
	var result []ports.EnqueueJobRequest
	for _, j := range jobs {
		if j.Kind == work.WorkspaceProvisionJobKind {
			result = append(result, j)
		}
	}
	return result
}

func TestCreateRootWorkItem_DuplicateSameRequest_ReplaysWithoutNewRowsOrJobs(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	mustCreateProject(t, uow, "project-1")
	mustCreateActiveRepository(t, uow, ids, "project-1", "repo-1")

	cmd := testCommand("idem-root-1", "hash-a", ports.ProjectScope("project-1"), "CreateRootWorkItem")
	req := baseRequest("project-1", grant("repo-1"))
	first, err := work.CreateRootWorkItem(ctx, uow, ids, cmd, req)
	if err != nil {
		t.Fatalf("first CreateRootWorkItem: %v", err)
	}
	second, err := work.CreateRootWorkItem(ctx, uow, ids, cmd, req)
	if err != nil {
		t.Fatalf("second (replayed) CreateRootWorkItem: %v", err)
	}
	if second.WorkItemID != first.WorkItemID || second.FamilyID != first.FamilyID || second.WorkspaceSetID != first.WorkspaceSetID {
		t.Fatalf("replayed result = %+v, want identical to first %+v", second, first)
	}

	provisionJobs := provisionJobsOnly(uow.Snapshot.Jobs().(*fake.JobsRepository).Items())
	if len(provisionJobs) != 1 {
		t.Fatalf("provision jobs after replay = %d, want 1 (a replay must never redo the mutation)", len(provisionJobs))
	}
	rootCreatedEvents := rootWorkItemCreatedEventsOnly(uow.Snapshot.Events().(*fake.EventsRepository).Items())
	if len(rootCreatedEvents) != 1 {
		t.Fatalf("RootWorkItemCreated events after replay = %d, want 1", len(rootCreatedEvents))
	}
}

func TestCreateRootWorkItem_DuplicateDifferentPayload_ReturnsConflict(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	mustCreateProject(t, uow, "project-1")
	mustCreateActiveRepository(t, uow, ids, "project-1", "repo-1")

	first := testCommand("idem-root-1", "hash-a", ports.ProjectScope("project-1"), "CreateRootWorkItem")
	if _, err := work.CreateRootWorkItem(ctx, uow, ids, first, baseRequest("project-1", grant("repo-1"))); err != nil {
		t.Fatalf("first CreateRootWorkItem: %v", err)
	}

	second := testCommand("idem-root-1", "hash-b", ports.ProjectScope("project-1"), "CreateRootWorkItem")
	_, err := work.CreateRootWorkItem(ctx, uow, ids, second, work.CreateRootWorkItemRequest{
		ProjectID: "project-1", Title: "A different title", InitialScope: []work.ScopeGrantRequest{grant("repo-1")},
	})
	if !errors.Is(err, ports.ErrReceiptConflict) {
		t.Fatalf("second CreateRootWorkItem err = %v, want ports.ErrReceiptConflict", err)
	}
}

// --- Validation ---

func TestCreateRootWorkItem_RequiresProjectIDTitleAndScope(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	mustCreateProject(t, uow, "project-1")

	cases := []struct {
		name string
		req  work.CreateRootWorkItemRequest
	}{
		{"missing ProjectID", work.CreateRootWorkItemRequest{Title: "T", InitialScope: []work.ScopeGrantRequest{grant("repo-1")}}},
		{"missing Title", work.CreateRootWorkItemRequest{ProjectID: "project-1", InitialScope: []work.ScopeGrantRequest{grant("repo-1")}}},
		{"empty InitialScope", work.CreateRootWorkItemRequest{ProjectID: "project-1", Title: "T"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := testCommand("idem-"+tc.name, "hash-"+tc.name, ports.ProjectScope("project-1"), "CreateRootWorkItem")
			_, err := work.CreateRootWorkItem(ctx, uow, ids, cmd, tc.req)
			if err == nil {
				t.Fatalf("%s: want a validation error, got nil", tc.name)
			}
		})
	}
}

func TestCreateRootWorkItem_UnknownProject_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")

	cmd := testCommand("idem-root-1", "hash-a", ports.ProjectScope("project-missing"), "CreateRootWorkItem")
	_, err := work.CreateRootWorkItem(ctx, uow, ids, cmd, baseRequest("project-missing", grant("repo-1")))
	if !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("err = %v, want ports.ErrPersistenceNotFound", err)
	}
}

func TestCreateRootWorkItem_UnknownRepository_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	mustCreateProject(t, uow, "project-1")

	cmd := testCommand("idem-root-1", "hash-a", ports.ProjectScope("project-1"), "CreateRootWorkItem")
	_, err := work.CreateRootWorkItem(ctx, uow, ids, cmd, baseRequest("project-1", grant("repo-missing")))
	if !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("err = %v, want ports.ErrPersistenceNotFound", err)
	}
}

func TestCreateRootWorkItem_RepositoryCrossProject_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	mustCreateProject(t, uow, "project-1")
	mustCreateProject(t, uow, "project-2")
	mustCreateActiveRepository(t, uow, ids, "project-2", "repo-other")

	cmd := testCommand("idem-root-1", "hash-a", ports.ProjectScope("project-1"), "CreateRootWorkItem")
	_, err := work.CreateRootWorkItem(ctx, uow, ids, cmd, baseRequest("project-1", grant("repo-other")))
	if !errors.Is(err, ports.ErrCrossProjectReference) {
		t.Fatalf("err = %v, want ports.ErrCrossProjectReference (repo-other belongs to project-2)", err)
	}
}

func TestCreateRootWorkItem_RepositoryNotActive_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	mustCreateProject(t, uow, "project-1")
	// RegisterRepository always starts REGISTERING — never activated here.
	regCmd := testCommand("idem-repo-1", "hash-repo-1", ports.ProjectScope("project-1"), "RegisterRepository")
	if _, err := catalog.RegisterRepository(ctx, uow, ids, regCmd, catalog.RegisterRepositoryRequest{
		RepositoryID: "repo-1", ProjectID: "project-1", Name: "svc",
		RemoteLocator: "https://example.invalid/repo.git", DefaultRef: "main",
	}); err != nil {
		t.Fatalf("RegisterRepository: %v", err)
	}

	cmd := testCommand("idem-root-1", "hash-a", ports.ProjectScope("project-1"), "CreateRootWorkItem")
	_, err := work.CreateRootWorkItem(ctx, uow, ids, cmd, baseRequest("project-1", grant("repo-1")))
	if !errors.Is(err, work.ErrRepositoryNotActive) {
		t.Fatalf("err = %v, want work.ErrRepositoryNotActive (repo-1 is still REGISTERING)", err)
	}
}

func TestCreateRootWorkItem_DuplicateRepositoryInInitialScope_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	mustCreateProject(t, uow, "project-1")
	mustCreateActiveRepository(t, uow, ids, "project-1", "repo-1")

	cmd := testCommand("idem-root-1", "hash-a", ports.ProjectScope("project-1"), "CreateRootWorkItem")
	_, err := work.CreateRootWorkItem(ctx, uow, ids, cmd, baseRequest("project-1", grant("repo-1"), grant("repo-1")))
	if err == nil {
		t.Fatal("CreateRootWorkItem with a duplicate repository in InitialScope succeeded, want an error")
	}
}

// TestCreateRootWorkItem_PartialFailure_NoRowsPersisted proves the fake's
// own rollback semantics (a failed WithSerializedWrite attempt never
// commits to Snapshot — internal/app/ports/fake.UnitOfWork.run's own
// "clone only replaces Snapshot if fn returns nil"): repo-a is ACTIVE and
// would succeed on its own, but repo-b is still REGISTERING, so the whole
// command must fail and leave NO trace of repo-a's own work either. The
// authoritative rollback proof against real sqlite lives in
// commands_sqlite_test.go; this is the fake-backed sanity check for the
// identical scenario.
func TestCreateRootWorkItem_PartialFailure_NoRowsPersisted(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	mustCreateProject(t, uow, "project-1")
	mustCreateActiveRepository(t, uow, ids, "project-1", "repo-a")
	regCmd := testCommand("idem-repo-b", "hash-repo-b", ports.ProjectScope("project-1"), "RegisterRepository")
	if _, err := catalog.RegisterRepository(ctx, uow, ids, regCmd, catalog.RegisterRepositoryRequest{
		RepositoryID: "repo-b", ProjectID: "project-1", Name: "repo-b",
		RemoteLocator: "https://example.invalid/repo-b.git", DefaultRef: "main",
	}); err != nil {
		t.Fatalf("RegisterRepository(repo-b): %v", err)
	}

	cmd := testCommand("idem-root-1", "hash-a", ports.ProjectScope("project-1"), "CreateRootWorkItem")
	_, err := work.CreateRootWorkItem(ctx, uow, ids, cmd, baseRequest("project-1", grant("repo-a"), grant("repo-b")))
	if !errors.Is(err, work.ErrRepositoryNotActive) {
		t.Fatalf("err = %v, want work.ErrRepositoryNotActive (repo-b is still REGISTERING)", err)
	}

	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	// Only repo-a's and repo-b's own RegisterRepository probe jobs (seeded
	// by test setup, committed in their own earlier transactions) may
	// exist — CreateRootWorkItem's own WORKSPACE_PROVISION job for repo-a
	// must never have survived, since its whole attempt rolled back.
	for _, j := range jobs {
		if j.Kind == work.WorkspaceProvisionJobKind {
			t.Fatalf("found a WORKSPACE_PROVISION job %+v after a failed CreateRootWorkItem, want none (rollback must remove it)", j)
		}
	}
	events := uow.Snapshot.Events().(*fake.EventsRepository).Items()
	for _, e := range events {
		if e.EventType == "RootWorkItemCreated" {
			t.Fatalf("found a RootWorkItemCreated event %+v after a failed CreateRootWorkItem, want none", e)
		}
	}
}

// --- CreateChildWorkItem ---

// createRootFixture seeds project-1/repo-1 ACTIVE and creates a root
// WorkItem granting repositoryAccess on paths, returning the root's own
// CreateRootWorkItemResult so a child test can build a request against its
// real WorkItemID/FamilyID.
func createRootFixture(t *testing.T, uow *fake.UnitOfWork, ids idsource.Source, repositoryID string, access workdomain.RepositoryAccess, paths []string) work.CreateRootWorkItemResult {
	t.Helper()
	ctx := context.Background()
	mustCreateProject(t, uow, "project-1")
	mustCreateActiveRepository(t, uow, ids, "project-1", repositoryID)

	cmd := testCommand("idem-root-"+repositoryID, "hash-root-"+repositoryID, ports.ProjectScope("project-1"), "CreateRootWorkItem")
	result, err := work.CreateRootWorkItem(ctx, uow, ids, cmd, work.CreateRootWorkItemRequest{
		ProjectID: "project-1", Title: "Root task", InitialScope: []work.ScopeGrantRequest{
			{RepositoryID: repositoryID, Access: string(access), PathScopes: paths, Reason: "root scope"},
		},
	})
	if err != nil {
		t.Fatalf("CreateRootWorkItem: %v", err)
	}
	return result
}

func childRequest(parentWorkItemID, title string, grants ...work.ScopeGrantRequest) work.CreateChildWorkItemRequest {
	return work.CreateChildWorkItemRequest{
		ParentWorkItemID: parentWorkItemID, Title: title, ParentJoinPolicy: "PARENT_BLOCKS_ON_CHILD",
		EffectiveScope: grants,
	}
}

func childGrant(repositoryID string, access workdomain.RepositoryAccess, paths []string) work.ScopeGrantRequest {
	return work.ScopeGrantRequest{
		RepositoryID: repositoryID, Access: string(access), PathScopes: paths, Reason: "child scope",
	}
}

// TestCreateChildWorkItem_SameRepositoryPathSubset_Succeeds is this task's
// own "child same repo, path subset" Verify-line case: the child requests
// the SAME repository/access as the family's own grant, on a path that is a
// strict subset of the granted path — must succeed, reuse the family
// (never creating a second TaskFamily/WorkspaceSet), and record exactly one
// work_item_effective_scopes-equivalent row.
func TestCreateChildWorkItem_SameRepositoryPathSubset_Succeeds(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	root := createRootFixture(t, uow, ids, "repo-1", workdomain.RepositoryWrite, []string{"services/api"})

	provisionJobsBefore := len(provisionJobsOnly(uow.Snapshot.Jobs().(*fake.JobsRepository).Items()))

	cmd := testCommand("idem-child-1", "hash-child-1", ports.ProjectScope("project-1"), "CreateChildWorkItem")
	result, err := work.CreateChildWorkItem(ctx, uow, ids, cmd, childRequest(
		root.WorkItemID, "Implement the handler",
		childGrant("repo-1", workdomain.RepositoryWrite, []string{"services/api/handler"}),
	))
	if err != nil {
		t.Fatalf("CreateChildWorkItem: %v", err)
	}
	if result.WorkItemID == "" || result.FamilyID != root.FamilyID || result.ParentWorkItemID != root.WorkItemID {
		t.Fatalf("result = %+v, want non-empty WorkItemID with FamilyID=%s and ParentWorkItemID=%s", result, root.FamilyID, root.WorkItemID)
	}
	if len(result.EffectiveScope) != 1 || result.EffectiveScope[0].RepositoryID != "repo-1" || result.EffectiveScope[0].Access != string(workdomain.RepositoryWrite) {
		t.Fatalf("result.EffectiveScope = %+v, want exactly one WRITE entry for repo-1", result.EffectiveScope)
	}

	item, err := uow.Snapshot.Work().GetWorkItem(ctx, result.WorkItemID)
	if err != nil {
		t.Fatalf("GetWorkItem: %v", err)
	}
	if item.Kind != workdomain.WorkItemChild || item.ParentID == nil || *item.ParentID != workdomain.WorkItemID(root.WorkItemID) {
		t.Fatalf("persisted child = %+v, want CHILD with ParentID=%s", item, root.WorkItemID)
	}
	if item.FamilyID != workdomain.TaskFamilyID(root.FamilyID) || item.ProjectID != "project-1" {
		t.Fatalf("persisted child = %+v, want FamilyID=%s inherited from parent (GC-INV-01)", item, root.FamilyID)
	}
	if item.ParentJoinPolicy != workdomain.JoinPolicy("PARENT_BLOCKS_ON_CHILD") {
		t.Fatalf("persisted child ParentJoinPolicy = %q, want the requested policy", item.ParentJoinPolicy)
	}
	if item.SourceNodeRunID != nil {
		t.Fatalf("persisted child SourceNodeRunID = %v, want nil (no SourceNodeRunID was requested)", item.SourceNodeRunID)
	}

	scopes, err := uow.Snapshot.Work().ListWorkItemEffectiveScopes(ctx, result.WorkItemID)
	if err != nil {
		t.Fatalf("ListWorkItemEffectiveScopes: %v", err)
	}
	if len(scopes) != 1 || scopes[0].RepositoryID() != "repo-1" || scopes[0].Access() != workdomain.RepositoryWrite {
		t.Fatalf("persisted effective scopes = %+v, want exactly one WRITE entry for repo-1", scopes)
	}
	if paths := scopes[0].PathScopes(); len(paths) != 1 || paths[0] != "services/api/handler" {
		t.Fatalf("persisted effective scope paths = %v, want [services/api/handler]", paths)
	}

	// The task's own "Hoàn thành khi" bar: creating a child must never
	// enqueue a new WORKSPACE_PROVISION job or create a second
	// TaskFamily/WorkspaceSet — the family already has both from its own
	// root creation.
	set, err := uow.Snapshot.Work().GetWorkspaceSetByFamilyID(ctx, root.FamilyID)
	if err != nil {
		t.Fatalf("GetWorkspaceSetByFamilyID: %v", err)
	}
	if string(set.ID) != root.WorkspaceSetID {
		t.Fatalf("workspace set id = %s, want the family's original %s (no second WorkspaceSet created)", set.ID, root.WorkspaceSetID)
	}

	provisionJobsAfter := len(provisionJobsOnly(uow.Snapshot.Jobs().(*fake.JobsRepository).Items()))
	if provisionJobsAfter != provisionJobsBefore {
		t.Fatalf("provision job count after CreateChildWorkItem = %d, want unchanged from %d (no new WORKSPACE_PROVISION job)", provisionJobsAfter, provisionJobsBefore)
	}

	childCreatedEvents := 0
	for _, e := range uow.Snapshot.Events().(*fake.EventsRepository).Items() {
		if e.EventType == "ChildWorkItemCreated" && e.AggregateID == result.WorkItemID {
			childCreatedEvents++
		}
	}
	if childCreatedEvents != 1 {
		t.Fatalf("ChildWorkItemCreated events = %d, want exactly 1", childCreatedEvents)
	}
}

// TestCreateChildWorkItem_DifferentRepositoryNeverGranted_Rejected is this
// task's own "child ... different repo" Verify-line case: the family only
// ever granted repo-1, so a child requesting repo-2 (never granted at all,
// not merely a path/access mismatch) must be rejected.
func TestCreateChildWorkItem_DifferentRepositoryNeverGranted_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	root := createRootFixture(t, uow, ids, "repo-1", workdomain.RepositoryWrite, []string{"services/api"})

	cmd := testCommand("idem-child-1", "hash-child-1", ports.ProjectScope("project-1"), "CreateChildWorkItem")
	_, err := work.CreateChildWorkItem(ctx, uow, ids, cmd, childRequest(
		root.WorkItemID, "Touch a different repo",
		childGrant("repo-2", workdomain.RepositoryWrite, nil),
	))
	if !errors.Is(err, work.ErrEffectiveScopeExceedsFamilyScope) {
		t.Fatalf("err = %v, want work.ErrEffectiveScopeExceedsFamilyScope (repo-2 was never granted to the family)", err)
	}
}

// TestCreateChildWorkItem_PathOutsideFamilyGrant_Rejected is the "path
// subset" Verify-line case's negative half: a path outside every grant's
// own PathScopes must be rejected even though the repository/access match.
func TestCreateChildWorkItem_PathOutsideFamilyGrant_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	root := createRootFixture(t, uow, ids, "repo-1", workdomain.RepositoryWrite, []string{"services/api"})

	cmd := testCommand("idem-child-1", "hash-child-1", ports.ProjectScope("project-1"), "CreateChildWorkItem")
	_, err := work.CreateChildWorkItem(ctx, uow, ids, cmd, childRequest(
		root.WorkItemID, "Touch an unrelated path",
		childGrant("repo-1", workdomain.RepositoryWrite, []string{"services/other"}),
	))
	if !errors.Is(err, work.ErrEffectiveScopeExceedsFamilyScope) {
		t.Fatalf("err = %v, want work.ErrEffectiveScopeExceedsFamilyScope (services/other is outside services/api)", err)
	}
}

// TestCreateChildWorkItem_ReadToWriteEscalation_Rejected is this task's own
// explicit "READ→WRITE escalation rejection" Verify-line requirement: the
// family granted READ only, the child requests WRITE on the identical
// repository/path — must be rejected.
func TestCreateChildWorkItem_ReadToWriteEscalation_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	root := createRootFixture(t, uow, ids, "repo-1", workdomain.RepositoryRead, []string{"services/api"})

	cmd := testCommand("idem-child-1", "hash-child-1", ports.ProjectScope("project-1"), "CreateChildWorkItem")
	_, err := work.CreateChildWorkItem(ctx, uow, ids, cmd, childRequest(
		root.WorkItemID, "Escalate to WRITE",
		childGrant("repo-1", workdomain.RepositoryWrite, []string{"services/api"}),
	))
	if !errors.Is(err, work.ErrEffectiveScopeExceedsFamilyScope) {
		t.Fatalf("err = %v, want work.ErrEffectiveScopeExceedsFamilyScope (family only granted READ)", err)
	}
}

func TestCreateChildWorkItem_DuplicateSameRequest_ReplaysWithoutNewRowsOrJobs(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	root := createRootFixture(t, uow, ids, "repo-1", workdomain.RepositoryWrite, []string{"services/api"})

	cmd := testCommand("idem-child-1", "hash-child-1", ports.ProjectScope("project-1"), "CreateChildWorkItem")
	req := childRequest(root.WorkItemID, "Implement the handler", childGrant("repo-1", workdomain.RepositoryWrite, []string{"services/api/handler"}))
	first, err := work.CreateChildWorkItem(ctx, uow, ids, cmd, req)
	if err != nil {
		t.Fatalf("first CreateChildWorkItem: %v", err)
	}
	second, err := work.CreateChildWorkItem(ctx, uow, ids, cmd, req)
	if err != nil {
		t.Fatalf("second (replayed) CreateChildWorkItem: %v", err)
	}
	if second.WorkItemID != first.WorkItemID {
		t.Fatalf("replayed result = %+v, want identical to first %+v", second, first)
	}

	scopes, err := uow.Snapshot.Work().ListWorkItemEffectiveScopes(ctx, first.WorkItemID)
	if err != nil {
		t.Fatalf("ListWorkItemEffectiveScopes: %v", err)
	}
	if len(scopes) != 1 {
		t.Fatalf("effective scopes after replay = %d, want 1 (a replay must never redo the mutation)", len(scopes))
	}
}

func TestCreateChildWorkItem_DuplicateDifferentPayload_ReturnsConflict(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	root := createRootFixture(t, uow, ids, "repo-1", workdomain.RepositoryWrite, []string{"services/api"})

	first := testCommand("idem-child-1", "hash-child-1", ports.ProjectScope("project-1"), "CreateChildWorkItem")
	if _, err := work.CreateChildWorkItem(ctx, uow, ids, first, childRequest(
		root.WorkItemID, "Implement the handler", childGrant("repo-1", workdomain.RepositoryWrite, []string{"services/api/handler"}),
	)); err != nil {
		t.Fatalf("first CreateChildWorkItem: %v", err)
	}

	second := testCommand("idem-child-1", "hash-child-2", ports.ProjectScope("project-1"), "CreateChildWorkItem")
	_, err := work.CreateChildWorkItem(ctx, uow, ids, second, childRequest(
		root.WorkItemID, "A different title", childGrant("repo-1", workdomain.RepositoryWrite, []string{"services/api/handler"}),
	))
	if !errors.Is(err, ports.ErrReceiptConflict) {
		t.Fatalf("second CreateChildWorkItem err = %v, want ports.ErrReceiptConflict", err)
	}
}

func TestCreateChildWorkItem_RequiresParentTitleJoinPolicyAndScope(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	root := createRootFixture(t, uow, ids, "repo-1", workdomain.RepositoryWrite, []string{"services/api"})
	grant := childGrant("repo-1", workdomain.RepositoryWrite, []string{"services/api"})

	cases := []struct {
		name string
		req  work.CreateChildWorkItemRequest
	}{
		{"missing ParentWorkItemID", work.CreateChildWorkItemRequest{Title: "T", ParentJoinPolicy: "P", EffectiveScope: []work.ScopeGrantRequest{grant}}},
		{"missing Title", work.CreateChildWorkItemRequest{ParentWorkItemID: root.WorkItemID, ParentJoinPolicy: "P", EffectiveScope: []work.ScopeGrantRequest{grant}}},
		{"missing ParentJoinPolicy", work.CreateChildWorkItemRequest{ParentWorkItemID: root.WorkItemID, Title: "T", EffectiveScope: []work.ScopeGrantRequest{grant}}},
		{"empty EffectiveScope", work.CreateChildWorkItemRequest{ParentWorkItemID: root.WorkItemID, Title: "T", ParentJoinPolicy: "P"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := testCommand("idem-"+tc.name, "hash-"+tc.name, ports.ProjectScope("project-1"), "CreateChildWorkItem")
			_, err := work.CreateChildWorkItem(ctx, uow, ids, cmd, tc.req)
			if err == nil {
				t.Fatalf("%s: want a validation error, got nil", tc.name)
			}
		})
	}
}

func TestCreateChildWorkItem_UnknownParent_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	mustCreateProject(t, uow, "project-1")

	cmd := testCommand("idem-child-1", "hash-child-1", ports.ProjectScope("project-1"), "CreateChildWorkItem")
	_, err := work.CreateChildWorkItem(ctx, uow, ids, cmd, childRequest(
		"work-item-missing", "Orphan child", childGrant("repo-1", workdomain.RepositoryWrite, nil),
	))
	if !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("err = %v, want ports.ErrPersistenceNotFound (parent does not exist)", err)
	}
}

func TestCreateChildWorkItem_DuplicateRepositoryInEffectiveScope_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	root := createRootFixture(t, uow, ids, "repo-1", workdomain.RepositoryWrite, []string{"services/api"})

	cmd := testCommand("idem-child-1", "hash-child-1", ports.ProjectScope("project-1"), "CreateChildWorkItem")
	_, err := work.CreateChildWorkItem(ctx, uow, ids, cmd, childRequest(
		root.WorkItemID, "Duplicate repo",
		childGrant("repo-1", workdomain.RepositoryWrite, []string{"services/api/a"}),
		childGrant("repo-1", workdomain.RepositoryWrite, []string{"services/api/b"}),
	))
	if err == nil {
		t.Fatal("CreateChildWorkItem with a duplicate repository in EffectiveScope succeeded, want an error")
	}
}

// TestCreateChildWorkItem_DifferentRepository_MultipleFamilyGrants_Succeeds
// is the positive half of "child same/different repo": a family granted
// scope on TWO repositories, and a child may request either one — proving
// "different repo" alone is not what gets rejected, only a repo the family
// never granted at all.
func TestCreateChildWorkItem_DifferentRepository_MultipleFamilyGrants_Succeeds(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	mustCreateProject(t, uow, "project-1")
	mustCreateActiveRepository(t, uow, ids, "project-1", "repo-a")
	mustCreateActiveRepository(t, uow, ids, "project-1", "repo-b")

	rootCmd := testCommand("idem-root-1", "hash-root-1", ports.ProjectScope("project-1"), "CreateRootWorkItem")
	root, err := work.CreateRootWorkItem(ctx, uow, ids, rootCmd, work.CreateRootWorkItemRequest{
		ProjectID: "project-1", Title: "Root task", InitialScope: []work.ScopeGrantRequest{
			grant("repo-a"), grant("repo-b"),
		},
	})
	if err != nil {
		t.Fatalf("CreateRootWorkItem: %v", err)
	}

	cmd := testCommand("idem-child-1", "hash-child-1", ports.ProjectScope("project-1"), "CreateChildWorkItem")
	result, err := work.CreateChildWorkItem(ctx, uow, ids, cmd, childRequest(
		root.WorkItemID, "Work on repo-b only",
		childGrant("repo-b", workdomain.RepositoryWrite, []string{"services/api"}),
	))
	if err != nil {
		t.Fatalf("CreateChildWorkItem: %v", err)
	}
	if len(result.EffectiveScope) != 1 || result.EffectiveScope[0].RepositoryID != "repo-b" {
		t.Fatalf("result.EffectiveScope = %+v, want exactly one entry for repo-b", result.EffectiveScope)
	}
}
