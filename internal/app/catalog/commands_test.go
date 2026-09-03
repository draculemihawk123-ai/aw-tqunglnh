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
