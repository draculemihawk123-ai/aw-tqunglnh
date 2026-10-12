package catalog_test

// V11-13 (ADR-044): rename, archive and restore a project, against a real sqlite store.

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

func (f *lifecycleFixture) project(id string) project.Project {
	f.t.Helper()
	p, err := catalog.GetProject(f.ctx, f.uow, ports.ProjectScope(id), id)
	if err != nil {
		f.t.Fatalf("GetProject(%s): %v", id, err)
	}
	return p
}

func TestRenameProject_ChangesTheNameKeepsTheIdAndRecordsIt(t *testing.T) {
	f := newLifecycleFixture(t)
	before := f.project("p1")
	result, err := catalog.RenameProject(f.ctx, f.uow, f.cmd("RenameProject", "ren-1", before.Version), catalog.RenameProjectRequest{ProjectID: "p1", Name: "  Better name  "})
	if err != nil {
		t.Fatalf("RenameProject: %v", err)
	}
	after := f.project("p1")
	if after.Name != "Better name" || after.Version != before.Version+1 || after.ID != "p1" || result.Version != after.Version {
		t.Fatalf("project after rename = %+v (result %+v)", after, result)
	}
	events, err := f.store.ListDomainEventsForProject(f.ctx, "p1")
	if err != nil {
		t.Fatalf("ListDomainEventsForProject: %v", err)
	}
	var found bool
	for _, e := range events {
		if e.EventType == catalog.ProjectRenamedEventType && strings.Contains(e.PayloadJSON, `"previousName":"project p1"`) && strings.Contains(e.PayloadJSON, `"name":"Better name"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no ProjectRenamed event with the old and new name among %d events", len(events))
	}
	// replay: same answer, no second bump
	again, err := catalog.RenameProject(f.ctx, f.uow, f.cmd("RenameProject", "ren-1", before.Version), catalog.RenameProjectRequest{ProjectID: "p1", Name: "  Better name  "})
	if err != nil || again != result || f.project("p1").Version != after.Version {
		t.Fatalf("replay = %+v, %v; want %+v and no second version bump", again, err, result)
	}
}

func TestRenameProject_RefusesBadNamesAndStaleVersions(t *testing.T) {
	f := newLifecycleFixture(t)
	v := f.project("p1").Version
	for _, name := range []string{"", "   ", strings.Repeat("x", 201)} {
		if _, err := catalog.RenameProject(f.ctx, f.uow, f.cmd("RenameProject", "ren-bad", v), catalog.RenameProjectRequest{ProjectID: "p1", Name: name}); err == nil {
			t.Fatalf("rename to %d-character name %q succeeded, want an error", len(name), name)
		}
	}
	if _, err := catalog.RenameProject(f.ctx, f.uow, f.cmd("RenameProject", "ren-stale", v+5), catalog.RenameProjectRequest{ProjectID: "p1", Name: "x"}); !errors.Is(err, ports.ErrOptimisticConflict) {
		t.Fatalf("stale version = %v; want ErrOptimisticConflict", err)
	}
	if got := f.project("p1"); got.Name != "project p1" || got.Version != v {
		t.Fatalf("a refused rename changed the project: %+v", got)
	}
}

func TestArchiveProject_BlocksWhileSomethingIsRunningAndListsIt(t *testing.T) {
	f := newLifecycleFixture(t)
	if err := sqlite.SeedFixtureOwners(f.ctx, f.store, "p2", "f2", "wi-2"); err != nil {
		t.Fatalf("SeedFixtureOwners: %v", err)
	}
	cmd := f.cmd("ArchiveProject", "arc-busy", 1)
	cmd.Scope = ports.ProjectScope("p2")
	_, err := catalog.ArchiveProject(f.ctx, f.uow, cmd, catalog.ArchiveProjectRequest{ProjectID: "p2", Reason: "done"})
	if catalog.RefusalCode(err) != catalog.RefusalProjectBusy {
		t.Fatalf("archive of a project with an ACTIVE work item = %v; want %s", err, catalog.RefusalProjectBusy)
	}
	if items := catalog.RefusalItems(err); len(items) != 1 || !strings.HasPrefix(items[0], "WORK_ITEM wi-2") {
		t.Fatalf("items = %v; want the work item named", items)
	}
	if got, _ := catalog.GetProject(f.ctx, f.uow, ports.ProjectScope("p2"), "p2"); got.Status != project.ProjectActive {
		t.Fatalf("a refused archive changed the project: %+v", got)
	}
}

func TestArchiveProject_MakesTheProjectReadOnlyAndRestoreBringsItBack(t *testing.T) {
	f := newLifecycleFixture(t)
	f.register("r1", "svc") // leaves an AVAILABLE probe job behind
	v := f.project("p1").Version

	archived, err := catalog.ArchiveProject(f.ctx, f.uow, f.cmd("ArchiveProject", "arc-1", v), catalog.ArchiveProjectRequest{ProjectID: "p1", Reason: "finished"})
	if err != nil || archived.Status != "ARCHIVED" || archived.PreviousStatus != "ACTIVE" {
		t.Fatalf("ArchiveProject = %+v, %v", archived, err)
	}

	// every write into the project is refused with the stable code and the way out
	_, err = catalog.RegisterRepository(f.ctx, f.uow, f.ids, f.cmd("RegisterRepository", "reg-late", 0), catalog.RegisterRepositoryRequest{
		RepositoryID: "late", ProjectID: "p1", Name: "late", RemoteLocator: "D:/x", DefaultRef: "main",
	})
	if err == nil || !strings.Contains(err.Error(), "PROJECT_ARCHIVED") || !strings.Contains(err.Error(), "restore") {
		t.Fatalf("RegisterRepository into an archived project = %v; want PROJECT_ARCHIVED with how to restore", err)
	}
	if _, err := catalog.GetRepository(f.ctx, f.uow, "late"); !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("a refused command left a repository behind: %v", err)
	}
	if _, err := catalog.RenameProject(f.ctx, f.uow, f.cmd("RenameProject", "ren-late", archived.Version), catalog.RenameProjectRequest{ProjectID: "p1", Name: "x"}); err == nil || !strings.Contains(err.Error(), "PROJECT_ARCHIVED") {
		t.Fatalf("rename of an archived project = %v; want PROJECT_ARCHIVED", err)
	}
	if _, err := catalog.DisableRepository(f.ctx, f.uow, f.cmd("DisableRepository", "dis-late", 1), catalog.DisableRepositoryRequest{RepositoryID: "r1", ProjectID: "p1"}); err == nil || !strings.Contains(err.Error(), "PROJECT_ARCHIVED") {
		t.Fatalf("repository command in an archived project = %v; want PROJECT_ARCHIVED", err)
	}

	// reads still work
	if got := f.project("p1"); got.Status != project.ProjectArchived {
		t.Fatalf("project = %+v", got)
	}
	if repos, err := catalog.ListProjectRepositories(f.ctx, f.uow, "p1"); err != nil || len(repos) != 1 {
		t.Fatalf("repositories of an archived project = %v, %v; want the one that was there", repos, err)
	}

	// the worker does not claim its jobs
	if _, _, err := f.store.ClaimJob(f.ctx, "worker-1", time.Minute); !errors.Is(err, ports.ErrNoJobAvailable) {
		t.Fatalf("ClaimJob while the project is archived = %v; want ErrNoJobAvailable", err)
	}

	// archiving again is a typed refusal
	if _, err := catalog.ArchiveProject(f.ctx, f.uow, f.cmd("ArchiveProject", "arc-2", archived.Version), catalog.ArchiveProjectRequest{ProjectID: "p1"}); catalog.RefusalCode(err) != catalog.RefusalProjectAlreadyArchived {
		t.Fatalf("second archive = %v; want %s", err, catalog.RefusalProjectAlreadyArchived)
	}

	// restore: writable again, the job is claimable again
	restored, err := catalog.RestoreProject(f.ctx, f.uow, f.cmd("RestoreProject", "res-1", archived.Version), catalog.RestoreProjectRequest{ProjectID: "p1"})
	if err != nil || restored.Status != "ACTIVE" {
		t.Fatalf("RestoreProject = %+v, %v", restored, err)
	}
	if _, _, err := f.store.ClaimJob(f.ctx, "worker-1", time.Minute); err != nil {
		t.Fatalf("ClaimJob after restore = %v; want the probe job", err)
	}
	f.register("r2", "svc2")
	if _, err := catalog.RestoreProject(f.ctx, f.uow, f.cmd("RestoreProject", "res-2", restored.Version), catalog.RestoreProjectRequest{ProjectID: "p1"}); catalog.RefusalCode(err) != catalog.RefusalProjectNotArchived {
		t.Fatalf("restore of an active project = %v; want %s", err, catalog.RefusalProjectNotArchived)
	}
}

func TestArchiveProject_ReplayAnswersEvenOnceArchived(t *testing.T) {
	f := newLifecycleFixture(t)
	v := f.project("p1").Version
	first, err := catalog.ArchiveProject(f.ctx, f.uow, f.cmd("ArchiveProject", "arc-replay", v), catalog.ArchiveProjectRequest{ProjectID: "p1"})
	if err != nil {
		t.Fatalf("ArchiveProject: %v", err)
	}
	again, err := catalog.ArchiveProject(f.ctx, f.uow, f.cmd("ArchiveProject", "arc-replay", v), catalog.ArchiveProjectRequest{ProjectID: "p1"})
	if err != nil || again != first {
		t.Fatalf("replay = %+v, %v; want %+v", again, err, first)
	}
}
