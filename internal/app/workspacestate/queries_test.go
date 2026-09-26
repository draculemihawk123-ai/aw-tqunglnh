package workspacestate_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/workspacestate"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

// repoSpec describes one RepositoryWorkspace mustSeedWorkspaceSet seeds
// alongside the WorkspaceSet — mirrors
// internal/app/workspacerelease/commands_test.go's own identical helper
// exactly (same fake, same domain constructors), since this package reads
// the exact same rows that eligibility check reasons about.
type repoSpec struct {
	RepositoryID string
	State        workspace.RepositoryWorkspaceState
}

type seededSet struct {
	ProjectID              string
	FamilyID               string
	WorkspaceSetID         string
	Version                uint64
	RepositoryWorkspaceIDs []string
}

func mustSeedWorkspaceSet(t *testing.T, uow ports.UnitOfWork, projectID, familyID string, repos []repoSpec) seededSet {
	t.Helper()
	ctx := context.Background()
	var result seededSet
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if _, err := tx.Catalog().CreateProject(ctx, ports.CreateProjectRequest{ID: projectID, Name: "project " + projectID}); err != nil {
			return err
		}
		root, err := workdomain.NewRootWorkItem(workdomain.WorkItemID("root-"+familyID), project.ProjectID(projectID), workdomain.TaskFamilyID(familyID), "Root task")
		if err != nil {
			return err
		}
		family, err := workdomain.NewTaskFamily(workdomain.TaskFamilyID(familyID), root)
		if err != nil {
			return err
		}
		if _, err := tx.Work().CreateTaskFamily(ctx, family); err != nil {
			return err
		}
		if _, err := tx.Work().CreateWorkItem(ctx, root); err != nil {
			return err
		}

		set, err := workspace.NewWorkspaceSet(workspace.WorkspaceSetID("set-"+familyID), family)
		if err != nil {
			return err
		}
		created, err := tx.Work().CreateWorkspaceSet(ctx, set)
		if err != nil {
			return err
		}

		repositoryWorkspaceIDs := make([]string, 0, len(repos))
		for i, spec := range repos {
			if _, err := tx.Catalog().RegisterRepository(ctx, ports.RegisterRepositoryRequest{
				ID: spec.RepositoryID, ProjectID: projectID, Name: "svc-" + spec.RepositoryID,
				RemoteLocator: "/fixture/" + spec.RepositoryID, DefaultRef: "main",
			}); err != nil {
				return err
			}
			repo, err := tx.Catalog().GetRepository(ctx, spec.RepositoryID)
			if err != nil {
				return err
			}
			rw, err := workspace.NewRepositoryWorkspace(
				workspace.RepositoryWorkspaceID(fmt.Sprintf("rw-%s-%d", familyID, i)), created, repo, 1,
				"handle-"+spec.RepositoryID, "", "base-sha",
			)
			if err != nil {
				return err
			}
			rw.State = spec.State
			rw.CurrentRevision = "current-sha"
			persisted, err := tx.Work().CreateRepositoryWorkspace(ctx, rw)
			if err != nil {
				return err
			}
			repositoryWorkspaceIDs = append(repositoryWorkspaceIDs, string(persisted.ID))
		}

		result = seededSet{
			ProjectID: projectID, FamilyID: familyID, WorkspaceSetID: string(created.ID),
			Version: created.Version, RepositoryWorkspaceIDs: repositoryWorkspaceIDs,
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed workspace set: %v", err)
	}
	return result
}

func TestGetWorkspaceSetState_HappyPath_ReturnsSetAndEveryRepositoryWorkspace(t *testing.T) {
	uow := fake.New()
	seeded := mustSeedWorkspaceSet(t, uow, "project-1", "family-1", []repoSpec{
		{RepositoryID: "repo-1", State: workspace.RepositoryWorkspaceReady},
		{RepositoryID: "repo-2", State: workspace.RepositoryWorkspaceQuarantined},
	})

	state, err := workspacestate.GetWorkspaceSetState(context.Background(), uow, workspacestate.GetWorkspaceSetStateRequest{
		ProjectID: "project-1", FamilyID: "family-1",
	})
	if err != nil {
		t.Fatalf("GetWorkspaceSetState: %v", err)
	}
	if state.WorkspaceSetID != seeded.WorkspaceSetID {
		t.Fatalf("WorkspaceSetID = %q, want %q", state.WorkspaceSetID, seeded.WorkspaceSetID)
	}
	if state.State != workspace.WorkspaceSetRequested {
		t.Fatalf("State = %q, want REQUESTED (mustSeedWorkspaceSet never advances it)", state.State)
	}
	if state.HasBaseRevisionSet {
		t.Fatal("HasBaseRevisionSet = true, want false: a freshly seeded WorkspaceSet never has one")
	}
	if len(state.RepositoryWorkspaces) != 2 {
		t.Fatalf("len(RepositoryWorkspaces) = %d, want 2", len(state.RepositoryWorkspaces))
	}
	var sawReady, sawQuarantined bool
	for _, rw := range state.RepositoryWorkspaces {
		if rw.HasActiveWriteLease {
			t.Fatalf("repository workspace %s: HasActiveWriteLease = true, want false (fake.WorkRepository never models write_leases — see its own doc comment)", rw.RepositoryWorkspaceID)
		}
		if rw.BaseRevision != "base-sha" {
			t.Fatalf("repository workspace %s: BaseRevision = %q, want %q (the workspace's own immutable provisioning revision, distinct from CurrentRevision)", rw.RepositoryWorkspaceID, rw.BaseRevision, "base-sha")
		}
		if rw.CurrentRevision != "current-sha" {
			t.Fatalf("repository workspace %s: CurrentRevision = %q, want %q", rw.RepositoryWorkspaceID, rw.CurrentRevision, "current-sha")
		}
		switch rw.State {
		case workspace.RepositoryWorkspaceReady:
			sawReady = true
		case workspace.RepositoryWorkspaceQuarantined:
			sawQuarantined = true
		}
	}
	if !sawReady || !sawQuarantined {
		t.Fatalf("expected one READY and one QUARANTINED repository workspace, got %+v", state.RepositoryWorkspaces)
	}
}

func TestGetWorkspaceSetState_UnknownFamily_ReturnsPersistenceNotFound(t *testing.T) {
	uow := fake.New()
	_, err := workspacestate.GetWorkspaceSetState(context.Background(), uow, workspacestate.GetWorkspaceSetStateRequest{
		ProjectID: "project-1", FamilyID: "does-not-exist",
	})
	if !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("err = %v, want ErrPersistenceNotFound", err)
	}
}

func TestGetWorkspaceSetState_CrossProject_ReturnsScopeMismatch(t *testing.T) {
	uow := fake.New()
	mustSeedWorkspaceSet(t, uow, "project-1", "family-1", []repoSpec{{RepositoryID: "repo-1", State: workspace.RepositoryWorkspaceReady}})

	_, err := workspacestate.GetWorkspaceSetState(context.Background(), uow, workspacestate.GetWorkspaceSetStateRequest{
		ProjectID: "project-2", FamilyID: "family-1", // family-1's real WorkspaceSet belongs to project-1
	})
	if !errors.Is(err, workspacestate.ErrScopeMismatch) {
		t.Fatalf("err = %v, want ErrScopeMismatch", err)
	}
}

func TestGetWorkspaceSetState_RequiresFamilyIDAndProjectID(t *testing.T) {
	uow := fake.New()
	if _, err := workspacestate.GetWorkspaceSetState(context.Background(), uow, workspacestate.GetWorkspaceSetStateRequest{ProjectID: "project-1"}); err == nil {
		t.Fatal("expected an error for a missing FamilyID")
	}
	if _, err := workspacestate.GetWorkspaceSetState(context.Background(), uow, workspacestate.GetWorkspaceSetStateRequest{FamilyID: "family-1"}); err == nil {
		t.Fatal("expected an error for a missing ProjectID")
	}
}

func TestGetRepositoryWorkspaceState_HappyPath_ReturnsExactRow(t *testing.T) {
	uow := fake.New()
	seeded := mustSeedWorkspaceSet(t, uow, "project-1", "family-1", []repoSpec{{RepositoryID: "repo-1", State: workspace.RepositoryWorkspaceReady}})

	state, err := workspacestate.GetRepositoryWorkspaceState(context.Background(), uow, workspacestate.GetRepositoryWorkspaceStateRequest{
		ProjectID: "project-1", RepositoryWorkspaceID: seeded.RepositoryWorkspaceIDs[0],
	})
	if err != nil {
		t.Fatalf("GetRepositoryWorkspaceState: %v", err)
	}
	if state.RepositoryWorkspaceID != seeded.RepositoryWorkspaceIDs[0] {
		t.Fatalf("RepositoryWorkspaceID = %q, want %q", state.RepositoryWorkspaceID, seeded.RepositoryWorkspaceIDs[0])
	}
	if state.RepositoryID != "repo-1" {
		t.Fatalf("RepositoryID = %q, want repo-1", state.RepositoryID)
	}
	if state.Generation != 1 {
		t.Fatalf("Generation = %d, want 1 (first provision)", state.Generation)
	}
	if state.State != workspace.RepositoryWorkspaceReady {
		t.Fatalf("State = %q, want READY", state.State)
	}
	if state.BaseRevision != "base-sha" {
		t.Fatalf("BaseRevision = %q, want base-sha", state.BaseRevision)
	}
	if state.CurrentRevision != "current-sha" {
		t.Fatalf("CurrentRevision = %q, want current-sha", state.CurrentRevision)
	}
	if state.HasActiveWriteLease {
		t.Fatal("HasActiveWriteLease = true, want false")
	}
}

func TestGetRepositoryWorkspaceState_UnknownID_ReturnsPersistenceNotFound(t *testing.T) {
	uow := fake.New()
	_, err := workspacestate.GetRepositoryWorkspaceState(context.Background(), uow, workspacestate.GetRepositoryWorkspaceStateRequest{
		ProjectID: "project-1", RepositoryWorkspaceID: "does-not-exist",
	})
	if !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("err = %v, want ErrPersistenceNotFound", err)
	}
}

func TestGetRepositoryWorkspaceState_CrossProject_ReturnsScopeMismatch(t *testing.T) {
	uow := fake.New()
	seeded := mustSeedWorkspaceSet(t, uow, "project-1", "family-1", []repoSpec{{RepositoryID: "repo-1", State: workspace.RepositoryWorkspaceReady}})

	_, err := workspacestate.GetRepositoryWorkspaceState(context.Background(), uow, workspacestate.GetRepositoryWorkspaceStateRequest{
		ProjectID: "project-2", RepositoryWorkspaceID: seeded.RepositoryWorkspaceIDs[0],
	})
	if !errors.Is(err, workspacestate.ErrScopeMismatch) {
		t.Fatalf("err = %v, want ErrScopeMismatch", err)
	}
}

func TestGetRepositoryWorkspaceState_RequiresIDAndProjectID(t *testing.T) {
	uow := fake.New()
	if _, err := workspacestate.GetRepositoryWorkspaceState(context.Background(), uow, workspacestate.GetRepositoryWorkspaceStateRequest{ProjectID: "project-1"}); err == nil {
		t.Fatal("expected an error for a missing RepositoryWorkspaceID")
	}
	if _, err := workspacestate.GetRepositoryWorkspaceState(context.Background(), uow, workspacestate.GetRepositoryWorkspaceStateRequest{RepositoryWorkspaceID: "rw-1"}); err == nil {
		t.Fatal("expected an error for a missing ProjectID")
	}
}
