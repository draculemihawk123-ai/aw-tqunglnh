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
