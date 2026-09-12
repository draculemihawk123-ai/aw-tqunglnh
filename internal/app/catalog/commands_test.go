package catalog_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
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

// --- CreateProject / ListProjects / GetProject ---

func TestCreateProject_CreatesProjectAndEmitsEvent(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("project")

	cmd := testCommand("idem-1", "hash-a", ports.InstallationScope(), "CreateProject")
	result, err := catalog.CreateProject(ctx, uow, ids, cmd, catalog.CreateProjectRequest{Name: "demo"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if result.ProjectID != "project-1" || result.Name != "demo" || result.Status != string(project.ProjectActive) {
		t.Fatalf("result = %+v, want ProjectID=project-1 Name=demo Status=ACTIVE", result)
	}

	loaded, err := uow.Snapshot.Catalog().GetProject(ctx, "project-1")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if loaded.Name != "demo" || loaded.Status != project.ProjectActive || loaded.Version != 1 {
		t.Fatalf("persisted project = %+v, want Name=demo Status=ACTIVE Version=1", loaded)
	}

	events := uow.Snapshot.Events().(*fake.EventsRepository).Items()
	if len(events) != 1 || events[0].EventType != catalog.ProjectCreatedEventType {
		t.Fatalf("events = %+v, want exactly one %s", events, catalog.ProjectCreatedEventType)
	}
	if events[0].ProjectID != "project-1" || events[0].AggregateType != "Project" || events[0].AggregateID != "project-1" {
		t.Fatalf("event = %+v, want ProjectID=AggregateID=project-1 AggregateType=Project", events[0])
	}
}

func TestCreateProject_DuplicateSameRequest_ReplaysExactSameGeneratedID(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	// Sequential is stateful: if a replay ever (incorrectly) called
	// ids.NewID() again, the second call would observably mint
	// "project-2" instead of replaying "project-1" — this is what makes
	// this test a real proof of "replay returns exact ID" (V6-03's own
	// Thực hiện line), not merely "replay returns *a* consistent result".
	ids := idsource.NewSequential("project")

	cmd := testCommand("idem-1", "hash-a", ports.InstallationScope(), "CreateProject")
	req := catalog.CreateProjectRequest{Name: "demo"}
	first, err := catalog.CreateProject(ctx, uow, ids, cmd, req)
	if err != nil {
		t.Fatalf("first CreateProject: %v", err)
	}
	if first.ProjectID != "project-1" {
		t.Fatalf("first.ProjectID = %q, want project-1", first.ProjectID)
	}

	second, err := catalog.CreateProject(ctx, uow, ids, cmd, req)
	if err != nil {
		t.Fatalf("second (replayed) CreateProject: %v", err)
	}
	if second != first {
		t.Fatalf("replayed result = %+v, want identical to first %+v", second, first)
	}
	if second.ProjectID != "project-1" {
		t.Fatalf("replayed ProjectID = %q, want the exact first-execution id project-1 (not a freshly minted one)", second.ProjectID)
	}

	projects, err := catalog.ListProjects(ctx, uow, ports.InstallationScope())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("projects after replay = %d, want 1 (a replay must never create a second Project)", len(projects))
	}
	events := uow.Snapshot.Events().(*fake.EventsRepository).Items()
	if len(events) != 1 {
		t.Fatalf("events after replay = %d, want 1", len(events))
	}
}

func TestCreateProject_DuplicateDifferentPayload_ReturnsConflict(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("project")

	first := testCommand("idem-1", "hash-a", ports.InstallationScope(), "CreateProject")
	if _, err := catalog.CreateProject(ctx, uow, ids, first, catalog.CreateProjectRequest{Name: "demo"}); err != nil {
		t.Fatalf("first CreateProject: %v", err)
	}

	second := testCommand("idem-1", "hash-b", ports.InstallationScope(), "CreateProject")
	_, err := catalog.CreateProject(ctx, uow, ids, second, catalog.CreateProjectRequest{Name: "different-name"})
	if !errors.Is(err, ports.ErrReceiptConflict) {
		t.Fatalf("second CreateProject err = %v, want ports.ErrReceiptConflict", err)
	}

	projects, err := catalog.ListProjects(ctx, uow, ports.InstallationScope())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 1 || projects[0].Name != "demo" {
		t.Fatalf("projects = %+v, want exactly [demo] (the conflicting retry must never mutate anything)", projects)
	}
}

func TestCreateProject_ProjectScopedCommand_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("project")

	cmd := testCommand("idem-1", "hash-a", ports.ProjectScope("project-1"), "CreateProject")
	_, err := catalog.CreateProject(ctx, uow, ids, cmd, catalog.CreateProjectRequest{Name: "demo"})
	if !errors.Is(err, ports.ErrScopeMismatch) {
		t.Fatalf("err = %v, want ports.ErrScopeMismatch (CreateProject is installation-scoped per ADR-025)", err)
	}
	projects, err := catalog.ListProjects(ctx, uow, ports.InstallationScope())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 0 {
		t.Fatalf("projects = %+v, want none created by a rejected command", projects)
	}
}

func TestCreateProject_RequiresName(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("project")

	cmd := testCommand("idem-1", "hash-a", ports.InstallationScope(), "CreateProject")
	_, err := catalog.CreateProject(ctx, uow, ids, cmd, catalog.CreateProjectRequest{Name: "   "})
	if err == nil {
		t.Fatal("CreateProject with a blank Name: want an error, got nil")
	}
}

func TestListProjects_ProjectScopedCaller_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	mustCreateProject(t, uow, "project-1")

	_, err := catalog.ListProjects(ctx, uow, ports.ProjectScope("project-1"))
	if !errors.Is(err, ports.ErrScopeMismatch) {
		t.Fatalf("err = %v, want ports.ErrScopeMismatch (ListProjects is installation-scoped per ADR-025)", err)
	}
}

func TestListProjects_ReturnsEveryProjectInIDOrder(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	mustCreateProject(t, uow, "project-b")
	mustCreateProject(t, uow, "project-a")
	mustCreateProject(t, uow, "project-c")

	projects, err := catalog.ListProjects(ctx, uow, ports.InstallationScope())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 3 || projects[0].ID != "project-a" || projects[1].ID != "project-b" || projects[2].ID != "project-c" {
		t.Fatalf("projects = %+v, want [project-a, project-b, project-c] in ID order", projects)
	}
}

func TestGetProject_MatchingProjectScope_ReturnsAuthoritativeDetail(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	mustCreateProject(t, uow, "project-1")

	got, err := catalog.GetProject(ctx, uow, ports.ProjectScope("project-1"), "project-1")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.ID != "project-1" || got.Name != "project project-1" || got.Status != project.ProjectActive {
		t.Fatalf("got = %+v, want the persisted project-1 row", got)
	}
}

func TestGetProject_AnotherProjectScope_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	mustCreateProject(t, uow, "project-1")
	mustCreateProject(t, uow, "project-2")

	_, err := catalog.GetProject(ctx, uow, ports.ProjectScope("project-2"), "project-1")
	if !errors.Is(err, ports.ErrScopeMismatch) {
		t.Fatalf("err = %v, want ports.ErrScopeMismatch (a caller scoped to project-2 cannot read project-1)", err)
	}
}

func TestGetProject_InstallationScopedCaller_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	mustCreateProject(t, uow, "project-1")

	_, err := catalog.GetProject(ctx, uow, ports.InstallationScope(), "project-1")
	if !errors.Is(err, ports.ErrScopeMismatch) {
		t.Fatalf("err = %v, want ports.ErrScopeMismatch (GetProject is project-scoped, not one of ADR-025's installation set)", err)
	}
}

// --- RegisterRepository ---

func TestRegisterRepository_CreatesRepositoryAndProbeJobAtomically(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	mustCreateProject(t, uow, "project-1")
	ids := idsource.NewSequential("job")

	cmd := testCommand("idem-1", "hash-a", ports.ProjectScope("project-1"), "RegisterRepository")
	result, err := catalog.RegisterRepository(ctx, uow, ids, cmd, catalog.RegisterRepositoryRequest{
		RepositoryID: "repo-1", ProjectID: "project-1", Name: "svc",
		RemoteLocator: "https://example.invalid/repo.git", DefaultRef: "main",
	})
	if err != nil {
		t.Fatalf("RegisterRepository: %v", err)
	}
	if result.RepositoryID != "repo-1" || result.Status != string(project.RepositoryRegistering) {
		t.Fatalf("result = %+v, want RepositoryID=repo-1 Status=REGISTERING", result)
	}
	if result.ProbeJobID == "" {
		t.Fatal("result.ProbeJobID is empty, want a minted probe job id")
	}

	repo, err := uow.Snapshot.Catalog().GetRepository(ctx, "repo-1")
	if err != nil {
		t.Fatalf("GetRepository: %v", err)
	}
	if repo.Status != project.RepositoryRegistering {
		t.Fatalf("persisted Status = %q, want REGISTERING", repo.Status)
	}

	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	if len(jobs) != 1 {
		t.Fatalf("enqueued jobs = %d, want exactly 1", len(jobs))
	}
	if jobs[0].Kind != "REPOSITORY_PROBE" || jobs[0].AggregateType != "Repository" || jobs[0].AggregateID != "repo-1" {
		t.Fatalf("job = %+v, want Kind=REPOSITORY_PROBE AggregateType=Repository AggregateID=repo-1", jobs[0])
	}

	events := uow.Snapshot.Events().(*fake.EventsRepository).Items()
	if len(events) != 1 || events[0].EventType != "RepositoryRegistered" {
		t.Fatalf("events = %+v, want exactly one RepositoryRegistered", events)
	}
}

func TestRegisterRepository_DuplicateSameRequest_ReplaysWithoutNewJobOrEvent(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	mustCreateProject(t, uow, "project-1")
	ids := idsource.NewSequential("job")

	cmd := testCommand("idem-1", "hash-a", ports.ProjectScope("project-1"), "RegisterRepository")
	req := catalog.RegisterRepositoryRequest{
		RepositoryID: "repo-1", ProjectID: "project-1", Name: "svc",
		RemoteLocator: "https://example.invalid/repo.git", DefaultRef: "main",
	}
	first, err := catalog.RegisterRepository(ctx, uow, ids, cmd, req)
	if err != nil {
		t.Fatalf("first RegisterRepository: %v", err)
	}
	second, err := catalog.RegisterRepository(ctx, uow, ids, cmd, req)
	if err != nil {
		t.Fatalf("second (replayed) RegisterRepository: %v", err)
	}
	if second != first {
		t.Fatalf("replayed result = %+v, want identical to first %+v", second, first)
	}

	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	if len(jobs) != 1 {
		t.Fatalf("enqueued jobs after replay = %d, want 1 (a replay must never redo the mutation)", len(jobs))
	}
	events := uow.Snapshot.Events().(*fake.EventsRepository).Items()
	if len(events) != 1 {
		t.Fatalf("events after replay = %d, want 1", len(events))
	}
}

func TestRegisterRepository_DuplicateDifferentPayload_ReturnsConflict(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	mustCreateProject(t, uow, "project-1")
	ids := idsource.NewSequential("job")

	first := testCommand("idem-1", "hash-a", ports.ProjectScope("project-1"), "RegisterRepository")
	if _, err := catalog.RegisterRepository(ctx, uow, ids, first, catalog.RegisterRepositoryRequest{
		RepositoryID: "repo-1", ProjectID: "project-1", Name: "svc",
		RemoteLocator: "https://example.invalid/repo.git", DefaultRef: "main",
	}); err != nil {
		t.Fatalf("first RegisterRepository: %v", err)
	}

	second := testCommand("idem-1", "hash-b", ports.ProjectScope("project-1"), "RegisterRepository")
	_, err := catalog.RegisterRepository(ctx, uow, ids, second, catalog.RegisterRepositoryRequest{
		RepositoryID: "repo-1", ProjectID: "project-1", Name: "different-name",
		RemoteLocator: "https://example.invalid/repo.git", DefaultRef: "main",
	})
	if !errors.Is(err, ports.ErrReceiptConflict) {
		t.Fatalf("second RegisterRepository err = %v, want ports.ErrReceiptConflict", err)
	}
}

func TestRegisterRepository_UnknownProject_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("job")

	cmd := testCommand("idem-1", "hash-a", ports.ProjectScope("project-missing"), "RegisterRepository")
	_, err := catalog.RegisterRepository(ctx, uow, ids, cmd, catalog.RegisterRepositoryRequest{
		RepositoryID: "repo-1", ProjectID: "project-missing", Name: "svc",
		RemoteLocator: "https://example.invalid/repo.git", DefaultRef: "main",
	})
	if !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("err = %v, want ports.ErrPersistenceNotFound", err)
	}
}

func TestRegisterRepository_CrossProject_TwoProjectsSameRepositoryName_BothSucceedDistinctly(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	mustCreateProject(t, uow, "project-1")
	mustCreateProject(t, uow, "project-2")
	ids := idsource.NewSequential("job")

	cmdA := testCommand("idem-a", "hash-a", ports.ProjectScope("project-1"), "RegisterRepository")
	_, err := catalog.RegisterRepository(ctx, uow, ids, cmdA, catalog.RegisterRepositoryRequest{
		RepositoryID: "repo-a", ProjectID: "project-1", Name: "svc",
		RemoteLocator: "https://example.invalid/repo.git", DefaultRef: "main",
	})
	if err != nil {
		t.Fatalf("RegisterRepository project-1: %v", err)
	}

	cmdB := testCommand("idem-b", "hash-b", ports.ProjectScope("project-2"), "RegisterRepository")
	_, err = catalog.RegisterRepository(ctx, uow, ids, cmdB, catalog.RegisterRepositoryRequest{
		RepositoryID: "repo-b", ProjectID: "project-2", Name: "svc", // identical Name as project-1's repo
		RemoteLocator: "https://example.invalid/repo.git", DefaultRef: "main",
	})
	if err != nil {
		t.Fatalf("RegisterRepository project-2: %v", err)
	}

	reposProject1, err := catalog.ListProjectRepositories(ctx, uow, "project-1")
	if err != nil {
		t.Fatalf("ListProjectRepositories project-1: %v", err)
	}
	reposProject2, err := catalog.ListProjectRepositories(ctx, uow, "project-2")
	if err != nil {
		t.Fatalf("ListProjectRepositories project-2: %v", err)
	}
	if len(reposProject1) != 1 || reposProject1[0].ID != "repo-a" {
		t.Fatalf("project-1 repositories = %+v, want exactly [repo-a]", reposProject1)
	}
	if len(reposProject2) != 1 || reposProject2[0].ID != "repo-b" {
		t.Fatalf("project-2 repositories = %+v, want exactly [repo-b]", reposProject2)
	}
}

// --- CreateComponent / AssignComponentPack ---

func setupComponent(t *testing.T, uow *fake.UnitOfWork, ids idsource.Source, projectID, repositoryID string) project.Component {
	t.Helper()
	ctx := context.Background()
	mustCreateProject(t, uow, projectID)
	regCmd := testCommand("idem-repo-"+repositoryID, "hash-repo-"+repositoryID, ports.ProjectScope(projectID), "RegisterRepository")
	if _, err := catalog.RegisterRepository(ctx, uow, ids, regCmd, catalog.RegisterRepositoryRequest{
		RepositoryID: repositoryID, ProjectID: projectID, Name: "svc",
		RemoteLocator: "https://example.invalid/repo.git", DefaultRef: "main",
	}); err != nil {
		t.Fatalf("RegisterRepository: %v", err)
	}
	component, err := catalog.CreateComponent(ctx, uow, ids, catalog.CreateComponentRequest{
		ProjectID: projectID, RepositoryID: repositoryID, Name: "api", Path: "services/api", Kind: "SERVICE",
	})
	if err != nil {
		t.Fatalf("CreateComponent: %v", err)
	}
	return component
}

func TestCreateComponent_CrossProject_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	mustCreateProject(t, uow, "project-1")
	mustCreateProject(t, uow, "project-2")
	regCmd := testCommand("idem-repo", "hash-repo", ports.ProjectScope("project-1"), "RegisterRepository")
	if _, err := catalog.RegisterRepository(ctx, uow, ids, regCmd, catalog.RegisterRepositoryRequest{
		RepositoryID: "repo-1", ProjectID: "project-1", Name: "svc",
		RemoteLocator: "https://example.invalid/repo.git", DefaultRef: "main",
	}); err != nil {
		t.Fatalf("RegisterRepository: %v", err)
	}

	_, err := catalog.CreateComponent(ctx, uow, ids, catalog.CreateComponentRequest{
		ProjectID: "project-2", RepositoryID: "repo-1", Name: "api", Path: "services/api", Kind: "SERVICE",
	})
	if !errors.Is(err, ports.ErrCrossProjectReference) {
		t.Fatalf("err = %v, want ports.ErrCrossProjectReference (component declared project-2 but repo-1 belongs to project-1)", err)
	}
}

func TestAssignComponentPack_CreatesAssignmentAndEmitsEvent(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	component := setupComponent(t, uow, ids, "project-1", "repo-1")

	cmd := testCommand("idem-assign-1", "hash-assign-1", ports.ProjectScope("project-1"), "AssignComponentPack")
	effectiveAt := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	result, err := catalog.AssignComponentPack(ctx, uow, ids, cmd, catalog.AssignComponentPackRequest{
		ProjectID: "project-1", ComponentID: string(component.ID), PackVersionID: "pack-version-1",
		EffectiveAt: effectiveAt, Actor: "operator-1",
	})
	if err != nil {
		t.Fatalf("AssignComponentPack: %v", err)
	}
	if result.PackVersionID != "pack-version-1" || !result.EffectiveAt.Equal(effectiveAt) {
		t.Fatalf("result = %+v, want PackVersionID=pack-version-1 EffectiveAt=%v", result, effectiveAt)
	}

	events := uow.Snapshot.Events().(*fake.EventsRepository).Items()
	found := false
	for _, event := range events {
		if event.EventType == "ComponentPackAssigned" {
			found = true
		}
	}
	if !found {
		t.Fatalf("events = %+v, want a ComponentPackAssigned event", events)
	}
}

func TestAssignComponentPack_SecondPackVersion_CreatesNewRowNotUpdate(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	component := setupComponent(t, uow, ids, "project-1", "repo-1")

	firstEffective := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cmd1 := testCommand("idem-assign-1", "hash-assign-1", ports.ProjectScope("project-1"), "AssignComponentPack")
	if _, err := catalog.AssignComponentPack(ctx, uow, ids, cmd1, catalog.AssignComponentPackRequest{
		ProjectID: "project-1", ComponentID: string(component.ID), PackVersionID: "pack-version-1",
		EffectiveAt: firstEffective, Actor: "operator-1",
	}); err != nil {
		t.Fatalf("first AssignComponentPack: %v", err)
	}

	secondEffective := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	cmd2 := testCommand("idem-assign-2", "hash-assign-2", ports.ProjectScope("project-1"), "AssignComponentPack")
	if _, err := catalog.AssignComponentPack(ctx, uow, ids, cmd2, catalog.AssignComponentPackRequest{
		ProjectID: "project-1", ComponentID: string(component.ID), PackVersionID: "pack-version-2",
		EffectiveAt: secondEffective, Actor: "operator-2",
	}); err != nil {
		t.Fatalf("second AssignComponentPack: %v", err)
	}

	all, err := catalog.ListComponentPackAssignments(ctx, uow, string(component.ID))
	if err != nil {
		t.Fatalf("ListComponentPackAssignments: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("assignments = %+v, want exactly 2 rows (append-only, never an update)", all)
	}
	if all[0].PackVersionID != "pack-version-1" || all[1].PackVersionID != "pack-version-2" {
		t.Fatalf("assignments = %+v, want oldest-first pack-version-1 then pack-version-2", all)
	}

	// Effective-time resolution: "what was in effect at time T".
	beforeAny, err := catalog.GetEffectiveComponentPackAssignment(ctx, uow, string(component.ID), time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC))
	if !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("before any assignment: err = %v, want ports.ErrPersistenceNotFound", err)
	}
	betweenBoth, err := catalog.GetEffectiveComponentPackAssignment(ctx, uow, string(component.ID), time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("between both: %v", err)
	}
	if betweenBoth.PackVersionID != "pack-version-1" {
		t.Fatalf("between both = %+v, want pack-version-1 still effective", betweenBoth)
	}
	afterBoth, err := catalog.GetEffectiveComponentPackAssignment(ctx, uow, string(component.ID), time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("after both: %v", err)
	}
	if afterBoth.PackVersionID != "pack-version-2" {
		t.Fatalf("after both = %+v, want pack-version-2 now effective", afterBoth)
	}
	_ = beforeAny
}

// --- RetryRepositoryProbe ---

// moveRepositoryToBlocked drives a freshly REGISTERING repository straight
// through REGISTERING->PROBING->BLOCKED via direct Catalog() calls,
// standing in for what V3-02's own REPOSITORY_PROBE job handler would
// otherwise do — test setup, not the behavior under test. Returns the
// Version the repository ends up at (2, after two transitions from
// version 1) so a test can supply the correct cmd.ExpectedVersion.
func moveRepositoryToBlocked(t *testing.T, uow *fake.UnitOfWork, repositoryID string) uint64 {
	t.Helper()
	ctx := context.Background()
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if _, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: repositoryID, ExpectedStatus: project.RepositoryRegistering, ExpectedVersion: 1,
			NextStatus: project.RepositoryProbing, LastProbeErrorCode: nil,
		}); err != nil {
			return err
		}
		code := "INVALID_ARGUMENT"
		_, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: repositoryID, ExpectedStatus: project.RepositoryProbing, ExpectedVersion: 2,
			NextStatus: project.RepositoryBlocked, LastProbeErrorCode: &code,
		})
		return err
	})
	if err != nil {
		t.Fatalf("moveRepositoryToBlocked(%s): %v", repositoryID, err)
	}
	return 3
}

func setupBlockedRepository(t *testing.T, uow *fake.UnitOfWork, ids idsource.Source, projectID, repositoryID string) uint64 {
	t.Helper()
	ctx := context.Background()
	mustCreateProject(t, uow, projectID)
	regCmd := testCommand("idem-repo-"+repositoryID, "hash-repo-"+repositoryID, ports.ProjectScope(projectID), "RegisterRepository")
	if _, err := catalog.RegisterRepository(ctx, uow, ids, regCmd, catalog.RegisterRepositoryRequest{
		RepositoryID: repositoryID, ProjectID: projectID, Name: "svc",
		RemoteLocator: "https://example.invalid/repo.git", DefaultRef: "main",
	}); err != nil {
		t.Fatalf("RegisterRepository: %v", err)
	}
	return moveRepositoryToBlocked(t, uow, repositoryID)
}

func TestRetryRepositoryProbe_TransitionsBlockedToProbingAndEnqueuesNewJob(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	version := setupBlockedRepository(t, uow, ids, "project-1", "repo-1")

	cmd := testCommand("idem-retry-1", "hash-retry-a", ports.ProjectScope("project-1"), "RetryRepositoryProbe")
	cmd.ExpectedVersion = version
	result, err := catalog.RetryRepositoryProbe(ctx, uow, ids, cmd, catalog.RetryRepositoryProbeRequest{
		RepositoryID: "repo-1", ProjectID: "project-1",
	})
	if err != nil {
		t.Fatalf("RetryRepositoryProbe: %v", err)
	}
	if result.RepositoryID != "repo-1" || result.Status != string(project.RepositoryProbing) {
		t.Fatalf("result = %+v, want RepositoryID=repo-1 Status=PROBING", result)
	}
	if result.ProbeJobID == "" {
		t.Fatal("result.ProbeJobID is empty, want a freshly minted probe job id")
	}

	repo, err := uow.Snapshot.Catalog().GetRepository(ctx, "repo-1")
	if err != nil {
		t.Fatalf("GetRepository: %v", err)
	}
	if repo.Status != project.RepositoryProbing {
		t.Fatalf("persisted Status = %q, want PROBING", repo.Status)
	}
	if repo.LastProbeErrorCode != nil {
		t.Fatalf("persisted LastProbeErrorCode = %v, want nil (cleared for the fresh probe)", repo.LastProbeErrorCode)
	}

	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	if len(jobs) != 2 { // RegisterRepository's original job, plus this retry's fresh job
		t.Fatalf("enqueued jobs = %d, want exactly 2 (original + retry, never resurrecting the dead one)", len(jobs))
	}
	retryJob := jobs[len(jobs)-1]
	if retryJob.Kind != "REPOSITORY_PROBE" || retryJob.AggregateType != "Repository" || retryJob.AggregateID != "repo-1" {
		t.Fatalf("retry job = %+v, want Kind=REPOSITORY_PROBE AggregateType=Repository AggregateID=repo-1", retryJob)
	}
	if retryJob.ID == jobs[0].ID {
		t.Fatalf("retry job id %q reused the original job's id %q, want a distinct freshly minted job", retryJob.ID, jobs[0].ID)
	}

	events := uow.Snapshot.Events().(*fake.EventsRepository).Items()
	found := false
	for _, event := range events {
		if event.EventType == "RepositoryProbeRetried" {
			found = true
		}
	}
	if !found {
		t.Fatalf("events = %+v, want a RepositoryProbeRetried event", events)
	}
}

func TestRetryRepositoryProbe_DuplicateSameRequest_ReplaysWithoutNewJobOrEvent(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	version := setupBlockedRepository(t, uow, ids, "project-1", "repo-1")

	cmd := testCommand("idem-retry-1", "hash-retry-a", ports.ProjectScope("project-1"), "RetryRepositoryProbe")
	cmd.ExpectedVersion = version
	req := catalog.RetryRepositoryProbeRequest{RepositoryID: "repo-1", ProjectID: "project-1"}
	first, err := catalog.RetryRepositoryProbe(ctx, uow, ids, cmd, req)
	if err != nil {
		t.Fatalf("first RetryRepositoryProbe: %v", err)
	}
	second, err := catalog.RetryRepositoryProbe(ctx, uow, ids, cmd, req)
	if err != nil {
		t.Fatalf("second (replayed) RetryRepositoryProbe: %v", err)
	}
	if second != first {
		t.Fatalf("replayed result = %+v, want identical to first %+v", second, first)
	}

	jobs := uow.Snapshot.Jobs().(*fake.JobsRepository).Items()
	if len(jobs) != 2 { // original RegisterRepository job + exactly one retry job, never a second
		t.Fatalf("enqueued jobs after replay = %d, want 2 (a replay must never redo the mutation)", len(jobs))
	}
}

func TestRetryRepositoryProbe_DuplicateDifferentPayload_ReturnsConflict(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	version := setupBlockedRepository(t, uow, ids, "project-1", "repo-1")

	first := testCommand("idem-retry-1", "hash-retry-a", ports.ProjectScope("project-1"), "RetryRepositoryProbe")
	first.ExpectedVersion = version
	if _, err := catalog.RetryRepositoryProbe(ctx, uow, ids, first, catalog.RetryRepositoryProbeRequest{
		RepositoryID: "repo-1", ProjectID: "project-1",
	}); err != nil {
		t.Fatalf("first RetryRepositoryProbe: %v", err)
	}

	second := testCommand("idem-retry-1", "hash-retry-b", ports.ProjectScope("project-1"), "RetryRepositoryProbe")
	second.ExpectedVersion = version
	_, err := catalog.RetryRepositoryProbe(ctx, uow, ids, second, catalog.RetryRepositoryProbeRequest{
		RepositoryID: "repo-1", ProjectID: "project-1",
	})
	if !errors.Is(err, ports.ErrReceiptConflict) {
		t.Fatalf("second RetryRepositoryProbe err = %v, want ports.ErrReceiptConflict", err)
	}
}

func TestRetryRepositoryProbe_WrongExpectedVersion_Conflict(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	setupBlockedRepository(t, uow, ids, "project-1", "repo-1")

	cmd := testCommand("idem-retry-1", "hash-retry-a", ports.ProjectScope("project-1"), "RetryRepositoryProbe")
	cmd.ExpectedVersion = 99 // stale/wrong
	_, err := catalog.RetryRepositoryProbe(ctx, uow, ids, cmd, catalog.RetryRepositoryProbeRequest{
		RepositoryID: "repo-1", ProjectID: "project-1",
	})
	if !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("err = %v, want ports.ErrOptimisticConflict", err)
	}
}

func TestRetryRepositoryProbe_NotBlocked_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	mustCreateProject(t, uow, "project-1")
	regCmd := testCommand("idem-repo", "hash-repo", ports.ProjectScope("project-1"), "RegisterRepository")
	if _, err := catalog.RegisterRepository(ctx, uow, ids, regCmd, catalog.RegisterRepositoryRequest{
		RepositoryID: "repo-1", ProjectID: "project-1", Name: "svc",
		RemoteLocator: "https://example.invalid/repo.git", DefaultRef: "main",
	}); err != nil {
		t.Fatalf("RegisterRepository: %v", err)
	}

	// repo-1 is still REGISTERING, never BLOCKED -- BLOCKED->PROBING's own
	// ExpectedStatus check must reject this, not silently transition it.
	cmd := testCommand("idem-retry-1", "hash-retry-a", ports.ProjectScope("project-1"), "RetryRepositoryProbe")
	cmd.ExpectedVersion = 1
	_, err := catalog.RetryRepositoryProbe(ctx, uow, ids, cmd, catalog.RetryRepositoryProbeRequest{
		RepositoryID: "repo-1", ProjectID: "project-1",
	})
	if !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("err = %v, want ports.ErrOptimisticConflict", err)
	}
}

func TestRetryRepositoryProbe_CrossProject_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	version := setupBlockedRepository(t, uow, ids, "project-1", "repo-1")
	mustCreateProject(t, uow, "project-2")

	cmd := testCommand("idem-retry-1", "hash-retry-a", ports.ProjectScope("project-2"), "RetryRepositoryProbe")
	cmd.ExpectedVersion = version
	_, err := catalog.RetryRepositoryProbe(ctx, uow, ids, cmd, catalog.RetryRepositoryProbeRequest{
		RepositoryID: "repo-1", ProjectID: "project-2", // repo-1 actually belongs to project-1
	})
	if !errors.Is(err, ports.ErrCrossProjectReference) {
		t.Fatalf("err = %v, want ports.ErrCrossProjectReference", err)
	}
}

func TestRetryRepositoryProbe_RequiresExpectedVersion(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	setupBlockedRepository(t, uow, ids, "project-1", "repo-1")

	cmd := testCommand("idem-retry-1", "hash-retry-a", ports.ProjectScope("project-1"), "RetryRepositoryProbe")
	// cmd.ExpectedVersion left at its zero value deliberately.
	_, err := catalog.RetryRepositoryProbe(ctx, uow, ids, cmd, catalog.RetryRepositoryProbeRequest{
		RepositoryID: "repo-1", ProjectID: "project-1",
	})
	if err == nil {
		t.Fatal("RetryRepositoryProbe with ExpectedVersion=0 succeeded, want a validation error")
	}
}

func TestAssignComponentPack_UnknownComponent_Rejected(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	mustCreateProject(t, uow, "project-1")

	cmd := testCommand("idem-assign-1", "hash-assign-1", ports.ProjectScope("project-1"), "AssignComponentPack")
	_, err := catalog.AssignComponentPack(ctx, uow, ids, cmd, catalog.AssignComponentPackRequest{
		ProjectID: "project-1", ComponentID: "component-missing", PackVersionID: "pack-version-1",
		EffectiveAt: time.Now().UTC(), Actor: "operator-1",
	})
	if !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("err = %v, want ports.ErrPersistenceNotFound", err)
	}
}
