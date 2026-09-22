package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

func openCatalogTestStore(t *testing.T, name string) *Store {
	t.Helper()
	store, err := Open(context.Background(), migratedDatabasePath(t, name))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func withCatalogTx(t *testing.T, store *Store, fn func(tx *sql.Tx) error) {
	t.Helper()
	if err := store.RunSerializedWrite(context.Background(), fn); err != nil {
		t.Fatalf("RunSerializedWrite: %v", err)
	}
}

// --- Project CRUD ---

func TestCatalogRepository_CreateProject_GetProject(t *testing.T) {
	store := openCatalogTestStore(t, "catalog-project-crud.db")
	ctx := context.Background()

	var created project.Project
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		created, err = catalogRepository{tx: tx}.CreateProject(ctx, ports.CreateProjectRequest{ID: "project-1", Name: "demo"})
		return err
	})
	if created.Status != project.ProjectActive || created.Version != 1 {
		t.Fatalf("created = %+v, want ACTIVE/version 1", created)
	}

	var loaded project.Project
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		loaded, err = catalogRepository{tx: tx}.GetProject(ctx, "project-1")
		return err
	})
	if loaded != created {
		t.Fatalf("loaded = %+v, want %+v", loaded, created)
	}
}

func TestCatalogRepository_GetProject_NotFound(t *testing.T) {
	store := openCatalogTestStore(t, "catalog-project-notfound.db")
	ctx := context.Background()
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := catalogRepository{tx: tx}.GetProject(ctx, "missing")
		if !errors.Is(err, ports.ErrPersistenceNotFound) {
			t.Fatalf("err = %v, want ports.ErrPersistenceNotFound", err)
		}
		return nil
	})
}

// --- Repository CRUD/CAS/idempotency/cross-project ---

func seedProject(t *testing.T, store *Store, id string) {
	t.Helper()
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := catalogRepository{tx: tx}.CreateProject(context.Background(), ports.CreateProjectRequest{ID: id, Name: "project " + id})
		return err
	})
}

func TestCatalogRepository_RegisterRepository_StartsRegistering(t *testing.T) {
	store := openCatalogTestStore(t, "catalog-repo-registering.db")
	ctx := context.Background()
	seedProject(t, store, "project-1")

	var repo project.Repository
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		repo, err = catalogRepository{tx: tx}.RegisterRepository(ctx, ports.RegisterRepositoryRequest{
			ID: "repo-1", ProjectID: "project-1", Name: "svc",
			RemoteLocator: "https://example.invalid/repo.git", DefaultRef: "main",
		})
		return err
	})
	if repo.Status != project.RepositoryRegistering {
		t.Fatalf("Status = %q, want REGISTERING", repo.Status)
	}
	if repo.LastProbeErrorCode != nil {
		t.Fatalf("LastProbeErrorCode = %v, want nil", repo.LastProbeErrorCode)
	}

	var loaded project.Repository
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		loaded, err = catalogRepository{tx: tx}.GetRepository(ctx, "repo-1")
		return err
	})
	if loaded.Status != project.RepositoryRegistering || loaded.ProjectID != "project-1" {
		t.Fatalf("loaded = %+v, want ProjectID=project-1 Status=REGISTERING", loaded)
	}
}

func TestCatalogRepository_RegisterRepository_UnknownProject_Rejected(t *testing.T) {
	store := openCatalogTestStore(t, "catalog-repo-unknown-project.db")
	ctx := context.Background()
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := catalogRepository{tx: tx}.RegisterRepository(ctx, ports.RegisterRepositoryRequest{
			ID: "repo-1", ProjectID: "project-missing", Name: "svc",
			RemoteLocator: "https://example.invalid/repo.git", DefaultRef: "main",
		})
		if !errors.Is(err, ports.ErrPersistenceNotFound) {
			t.Fatalf("err = %v, want ports.ErrPersistenceNotFound", err)
		}
		return nil
	})
}

// TestCatalogRepository_ListProjectRepositories_IdentityIsIDBased proves
// V3-01's own "Hoàn thành khi": list/filter never infers identity from
// slug/cwd/remote. Two projects each register a repository sharing the
// exact same Name and RemoteLocator (a colliding "slug"-like value) —
// ListProjectRepositories must still return exactly the one repository
// belonging to each project, distinguished purely by the stored
// project_id/id columns.
func TestCatalogRepository_ListProjectRepositories_IdentityIsIDBased(t *testing.T) {
	store := openCatalogTestStore(t, "catalog-repo-list-identity.db")
	ctx := context.Background()
	seedProject(t, store, "project-1")
	seedProject(t, store, "project-2")

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		repo := catalogRepository{tx: tx}
		if _, err := repo.RegisterRepository(ctx, ports.RegisterRepositoryRequest{
			ID: "repo-a", ProjectID: "project-1", Name: "svc",
			RemoteLocator: "https://example.invalid/same-remote.git", DefaultRef: "main",
		}); err != nil {
			return err
		}
		_, err := repo.RegisterRepository(ctx, ports.RegisterRepositoryRequest{
			ID: "repo-b", ProjectID: "project-2", Name: "svc", // identical Name and RemoteLocator as repo-a
			RemoteLocator: "https://example.invalid/same-remote.git", DefaultRef: "main",
		})
		return err
	})

	var project1Repos, project2Repos []project.Repository
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		project1Repos, err = catalogRepository{tx: tx}.ListProjectRepositories(ctx, "project-1")
		return err
	})
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		project2Repos, err = catalogRepository{tx: tx}.ListProjectRepositories(ctx, "project-2")
		return err
	})
	if len(project1Repos) != 1 || project1Repos[0].ID != "repo-a" {
		t.Fatalf("project-1 repositories = %+v, want exactly [repo-a] despite the colliding name/remote", project1Repos)
	}
	if len(project2Repos) != 1 || project2Repos[0].ID != "repo-b" {
		t.Fatalf("project-2 repositories = %+v, want exactly [repo-b] despite the colliding name/remote", project2Repos)
	}
}

// --- Component CRUD/cross-project ---

func seedRepository(t *testing.T, store *Store, projectID, repositoryID string) {
	t.Helper()
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := catalogRepository{tx: tx}.RegisterRepository(context.Background(), ports.RegisterRepositoryRequest{
			ID: repositoryID, ProjectID: projectID, Name: "svc-" + repositoryID,
			RemoteLocator: "https://example.invalid/" + repositoryID + ".git", DefaultRef: "main",
		})
		return err
	})
}

func TestCatalogRepository_CreateComponent_GetComponent(t *testing.T) {
	store := openCatalogTestStore(t, "catalog-component-crud.db")
	ctx := context.Background()
	seedProject(t, store, "project-1")
	seedRepository(t, store, "project-1", "repo-1")

	var created project.Component
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		created, err = catalogRepository{tx: tx}.CreateComponent(ctx, ports.CreateComponentRequest{
			ID: "comp-1", ProjectID: "project-1", RepositoryID: "repo-1",
			Name: "api", Path: "services/api", Kind: "SERVICE",
		})
		return err
	})
	if created.Path != "services/api" || created.Version != 1 {
		t.Fatalf("created = %+v, want Path=services/api Version=1", created)
	}

	var loaded project.Component
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		loaded, err = catalogRepository{tx: tx}.GetComponent(ctx, "comp-1")
		return err
	})
	if loaded != created {
		t.Fatalf("loaded = %+v, want %+v", loaded, created)
	}
}

func TestCatalogRepository_CreateComponent_CrossProject_Rejected(t *testing.T) {
	store := openCatalogTestStore(t, "catalog-component-cross-project.db")
	ctx := context.Background()
	seedProject(t, store, "project-1")
	seedProject(t, store, "project-2")
	seedRepository(t, store, "project-1", "repo-1")

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := catalogRepository{tx: tx}.CreateComponent(ctx, ports.CreateComponentRequest{
			ID: "comp-1", ProjectID: "project-2", RepositoryID: "repo-1", // repo-1 actually belongs to project-1
			Name: "api", Path: "services/api", Kind: "SERVICE",
		})
		if !errors.Is(err, ports.ErrCrossProjectReference) {
			t.Fatalf("err = %v, want ports.ErrCrossProjectReference", err)
		}
		return nil
	})
}

func TestCatalogRepository_CreateComponent_UnknownRepository_Rejected(t *testing.T) {
	store := openCatalogTestStore(t, "catalog-component-unknown-repo.db")
	ctx := context.Background()
	seedProject(t, store, "project-1")
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := catalogRepository{tx: tx}.CreateComponent(ctx, ports.CreateComponentRequest{
			ID: "comp-1", ProjectID: "project-1", RepositoryID: "repo-missing",
			Name: "api", Path: "services/api", Kind: "SERVICE",
		})
		if !errors.Is(err, ports.ErrPersistenceNotFound) {
			t.Fatalf("err = %v, want ports.ErrPersistenceNotFound", err)
		}
		return nil
	})
}

// TestCatalogRepository_Component_DistinctRepositoriesSamePath proves the
// same list/filter identity independence bar as
// TestCatalogRepository_ListProjectRepositories_IdentityIsIDBased, at the
// Component level: two Components sharing the exact same relative Path
// under two different Repositories must never collide, distinguished
// only by repository_id.
func TestCatalogRepository_Component_DistinctRepositoriesSamePath(t *testing.T) {
	store := openCatalogTestStore(t, "catalog-component-same-path.db")
	ctx := context.Background()
	seedProject(t, store, "project-1")
	seedRepository(t, store, "project-1", "repo-a")
	seedRepository(t, store, "project-1", "repo-b")

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		repo := catalogRepository{tx: tx}
		if _, err := repo.CreateComponent(ctx, ports.CreateComponentRequest{
			ID: "comp-a", ProjectID: "project-1", RepositoryID: "repo-a", Name: "api", Path: "services/api", Kind: "SERVICE",
		}); err != nil {
			return err
		}
		_, err := repo.CreateComponent(ctx, ports.CreateComponentRequest{
			ID: "comp-b", ProjectID: "project-1", RepositoryID: "repo-b", Name: "api", Path: "services/api", Kind: "SERVICE",
		})
		return err
	})

	var compA, compB project.Component
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		compA, err = catalogRepository{tx: tx}.GetComponent(ctx, "comp-a")
		return err
	})
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		compB, err = catalogRepository{tx: tx}.GetComponent(ctx, "comp-b")
		return err
	})
	if compA.RepositoryID != "repo-a" || compB.RepositoryID != "repo-b" {
		t.Fatalf("compA=%+v compB=%+v, want distinct RepositoryID despite identical Path", compA, compB)
	}
}

// --- ComponentPackAssignment CRUD/cross-project/provenance ---

func seedComponent(t *testing.T, store *Store, projectID, repositoryID, componentID string) project.Component {
	t.Helper()
	var created project.Component
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		created, err = catalogRepository{tx: tx}.CreateComponent(context.Background(), ports.CreateComponentRequest{
			ID: componentID, ProjectID: projectID, RepositoryID: repositoryID,
			Name: "api", Path: "services/" + componentID, Kind: "SERVICE",
		})
		return err
	})
	return created
}

func TestCatalogRepository_AssignComponentPack_CrossProject_Rejected(t *testing.T) {
	store := openCatalogTestStore(t, "catalog-assignment-cross-project.db")
	ctx := context.Background()
	seedProject(t, store, "project-1")
	seedProject(t, store, "project-2")
	seedRepository(t, store, "project-1", "repo-1")
	seedComponent(t, store, "project-1", "repo-1", "comp-1")

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := catalogRepository{tx: tx}.AssignComponentPack(ctx, ports.AssignComponentPackRequest{
			ID: "assign-1", ProjectID: "project-2", ComponentID: "comp-1", // comp-1 actually belongs to project-1
			PackVersionID: "pack-version-1", EffectiveAt: time.Now().UTC(), Actor: "operator-1",
		})
		if !errors.Is(err, ports.ErrCrossProjectReference) {
			t.Fatalf("err = %v, want ports.ErrCrossProjectReference", err)
		}
		return nil
	})
}

// TestCatalogRepository_AssignComponentPack_VersionProvenance proves the
// append-only version/provenance requirement directly at the persistence
// layer: assigning a second PackVersion to the same Component inserts a
// new row (never mutates the first), ListComponentPackAssignments
// returns both oldest-first with their own distinct Actor/EffectiveAt,
// and GetEffectiveComponentPackAssignment resolves the correct
// historical row for a query time between, before and after both.
func TestCatalogRepository_AssignComponentPack_VersionProvenance(t *testing.T) {
	store := openCatalogTestStore(t, "catalog-assignment-provenance.db")
	ctx := context.Background()
	seedProject(t, store, "project-1")
	seedRepository(t, store, "project-1", "repo-1")
	component := seedComponent(t, store, "project-1", "repo-1", "comp-1")

	firstEffective := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	secondEffective := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		repo := catalogRepository{tx: tx}
		if _, err := repo.AssignComponentPack(ctx, ports.AssignComponentPackRequest{
			ID: "assign-1", ProjectID: "project-1", ComponentID: string(component.ID),
			PackVersionID: "pack-version-1", EffectiveAt: firstEffective, Actor: "operator-1",
		}); err != nil {
			return err
		}
		_, err := repo.AssignComponentPack(ctx, ports.AssignComponentPackRequest{
			ID: "assign-2", ProjectID: "project-1", ComponentID: string(component.ID),
			PackVersionID: "pack-version-2", EffectiveAt: secondEffective, Actor: "operator-2",
		})
		return err
	})

	var all []project.ComponentPackAssignment
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		all, err = catalogRepository{tx: tx}.ListComponentPackAssignments(ctx, string(component.ID))
		return err
	})
	if len(all) != 2 {
		t.Fatalf("assignments = %+v, want exactly 2 rows (append-only)", all)
	}
	if all[0].PackVersionID != "pack-version-1" || all[0].Actor != "operator-1" {
		t.Fatalf("all[0] = %+v, want pack-version-1/operator-1", all[0])
	}
	if all[1].PackVersionID != "pack-version-2" || all[1].Actor != "operator-2" {
		t.Fatalf("all[1] = %+v, want pack-version-2/operator-2", all[1])
	}

	cases := []struct {
		name        string
		at          time.Time
		wantVersion string
		wantErr     error
	}{
		{"before either", time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC), "", ports.ErrPersistenceNotFound},
		{"exactly first", firstEffective, "pack-version-1", nil},
		{"between both", time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC), "pack-version-1", nil},
		{"exactly second", secondEffective, "pack-version-2", nil},
		{"after both", time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), "pack-version-2", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var resolved project.ComponentPackAssignment
			var resolveErr error
			withCatalogTx(t, store, func(tx *sql.Tx) error {
				resolved, resolveErr = catalogRepository{tx: tx}.GetEffectiveComponentPackAssignment(ctx, string(component.ID), tc.at)
				return nil
			})
			if tc.wantErr != nil {
				if !errors.Is(resolveErr, tc.wantErr) {
					t.Fatalf("GetEffectiveComponentPackAssignment(%s) err = %v, want %v", tc.name, resolveErr, tc.wantErr)
				}
				return
			}
			if resolveErr != nil {
				t.Fatalf("GetEffectiveComponentPackAssignment(%s): %v", tc.name, resolveErr)
			}
			if string(resolved.PackVersionID) != tc.wantVersion {
				t.Fatalf("GetEffectiveComponentPackAssignment(%s) = %+v, want PackVersionID=%s", tc.name, resolved, tc.wantVersion)
			}
		})
	}
}

func TestCatalogRepository_AssignComponentPack_UnknownComponent_Rejected(t *testing.T) {
	store := openCatalogTestStore(t, "catalog-assignment-unknown-component.db")
	ctx := context.Background()
	seedProject(t, store, "project-1")
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := catalogRepository{tx: tx}.AssignComponentPack(ctx, ports.AssignComponentPackRequest{
			ID: "assign-1", ProjectID: "project-1", ComponentID: "comp-missing",
			PackVersionID: "pack-version-1", EffectiveAt: time.Now().UTC(), Actor: "operator-1",
		})
		if !errors.Is(err, ports.ErrPersistenceNotFound) {
			t.Fatalf("err = %v, want ports.ErrPersistenceNotFound", err)
		}
		return nil
	})
}

// TestCatalogRepository_AssignComponentPack_OrdersByActualTimeNotLexicalText
// is a regression test for a real correctness hazard: time.Format(time.RFC3339Nano)
// trims trailing zero fractional-second digits, so two timestamps that
// share the same whole second — one with no fractional part at all, one
// with a genuine sub-second component — do NOT compare correctly as
// plain TEXT ("2026-01-01T00:00:00Z" sorts AFTER
// "2026-01-01T00:00:00.5Z" lexically, even though the first instant is
// chronologically earlier, since 'Z' > '.' in ASCII). This is exactly
// why every other timestamp comparison in this adapter
// (internal/adapters/sqlite/scheduling.go's own
// "julianday(lease_until) > julianday('now')") wraps the column in
// julianday() rather than comparing the TEXT column directly — this test
// proves ListComponentPackAssignments/GetEffectiveComponentPackAssignment
// do the same and get the true chronological order right even in this
// adversarial case.
func TestCatalogRepository_AssignComponentPack_OrdersByActualTimeNotLexicalText(t *testing.T) {
	store := openCatalogTestStore(t, "catalog-assignment-lexical-order.db")
	ctx := context.Background()
	seedProject(t, store, "project-1")
	seedRepository(t, store, "project-1", "repo-1")
	component := seedComponent(t, store, "project-1", "repo-1", "comp-1")

	// Same whole second: earlier formats with NO fractional part at all
	// ("...:00Z", time.Format trims an exact-zero fraction entirely),
	// later formats WITH one ("...:00.5Z") — the exact adversarial case
	// plain TEXT comparison gets backwards, since '.' (0x2E) sorts before
	// 'Z' (0x5A), making the fractional (truly later) string compare as
	// lexically SMALLER than the whole-second (truly earlier) one.
	earlier := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)         // "2026-01-01T00:00:00Z"
	later := time.Date(2026, 1, 1, 0, 0, 0, 500_000_000, time.UTC) // "2026-01-01T00:00:00.5Z"
	if !later.After(earlier) {
		t.Fatalf("test setup bug: later (%v) must be strictly after earlier (%v)", later, earlier)
	}

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		repo := catalogRepository{tx: tx}
		if _, err := repo.AssignComponentPack(ctx, ports.AssignComponentPackRequest{
			ID: "assign-earlier", ProjectID: "project-1", ComponentID: string(component.ID),
			PackVersionID: "pack-version-earlier", EffectiveAt: earlier, Actor: "operator-1",
		}); err != nil {
			return err
		}
		_, err := repo.AssignComponentPack(ctx, ports.AssignComponentPackRequest{
			ID: "assign-later", ProjectID: "project-1", ComponentID: string(component.ID),
			PackVersionID: "pack-version-later", EffectiveAt: later, Actor: "operator-2",
		})
		return err
	})

	var all []project.ComponentPackAssignment
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		all, err = catalogRepository{tx: tx}.ListComponentPackAssignments(ctx, string(component.ID))
		return err
	})
	if len(all) != 2 || all[0].PackVersionID != "pack-version-earlier" || all[1].PackVersionID != "pack-version-later" {
		t.Fatalf("ListComponentPackAssignments = %+v, want [pack-version-earlier, pack-version-later] in true chronological order", all)
	}

	var effective project.ComponentPackAssignment
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		effective, err = catalogRepository{tx: tx}.GetEffectiveComponentPackAssignment(ctx, string(component.ID), later)
		return err
	})
	if effective.PackVersionID != "pack-version-later" {
		t.Fatalf("GetEffectiveComponentPackAssignment(later) = %+v, want pack-version-later (the true most-recent assignment)", effective)
	}
}

// --- TransitionRepositoryStatus (V3-02 CAS) ---

func seedProbeJob(t *testing.T, store *Store, projectID, repositoryID, jobID string) ports.DurableJob {
	t.Helper()
	job, err := store.EnqueueJob(context.Background(), ports.EnqueueJobRequest{
		ID: ports.JobID(jobID), ProjectID: project.ProjectID(projectID), Kind: "REPOSITORY_PROBE",
		AggregateType: "Repository", AggregateID: repositoryID, MaxClaims: 3, IdempotencyKey: jobID + "-key",
	})
	if err != nil {
		t.Fatalf("seedProbeJob: %v", err)
	}
	return job
}

func TestCatalogRepository_TransitionRepositoryStatus_Success(t *testing.T) {
	store := openCatalogTestStore(t, "catalog-transition-success.db")
	ctx := context.Background()
	seedProject(t, store, "project-1")
	seedRepository(t, store, "project-1", "repo-1")

	var updated project.Repository
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		updated, err = catalogRepository{tx: tx}.TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: "repo-1", ExpectedStatus: project.RepositoryRegistering, ExpectedVersion: 1,
			NextStatus: project.RepositoryProbing, LastProbeErrorCode: nil,
		})
		return err
	})
	if updated.Status != project.RepositoryProbing || updated.Version != 2 {
		t.Fatalf("updated = %+v, want Status=PROBING Version=2", updated)
	}

	var loaded project.Repository
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		loaded, err = catalogRepository{tx: tx}.GetRepository(ctx, "repo-1")
		return err
	})
	if loaded != updated {
		t.Fatalf("loaded = %+v, want %+v", loaded, updated)
	}
}

func TestCatalogRepository_TransitionRepositoryStatus_WrongExpectedVersion_Conflict(t *testing.T) {
	store := openCatalogTestStore(t, "catalog-transition-wrong-version.db")
	ctx := context.Background()
	seedProject(t, store, "project-1")
	seedRepository(t, store, "project-1", "repo-1")

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := catalogRepository{tx: tx}.TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: "repo-1", ExpectedStatus: project.RepositoryRegistering, ExpectedVersion: 99,
			NextStatus: project.RepositoryProbing, LastProbeErrorCode: nil,
		})
		if !errors.Is(err, ports.ErrOptimisticConflict) {
			t.Fatalf("err = %v, want ports.ErrOptimisticConflict", err)
		}
		return nil
	})

	// The row must be completely untouched by the rejected attempt.
	var loaded project.Repository
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		loaded, err = catalogRepository{tx: tx}.GetRepository(ctx, "repo-1")
		return err
	})
	if loaded.Status != project.RepositoryRegistering || loaded.Version != 1 {
		t.Fatalf("loaded = %+v, want unchanged Status=REGISTERING Version=1", loaded)
	}
}

func TestCatalogRepository_TransitionRepositoryStatus_WrongExpectedStatus_Conflict(t *testing.T) {
	store := openCatalogTestStore(t, "catalog-transition-wrong-status.db")
	ctx := context.Background()
	seedProject(t, store, "project-1")
	seedRepository(t, store, "project-1", "repo-1")

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		// repo-1 is actually REGISTERING, not PROBING -- BLOCKED->PROBING
		// is a legal edge in the abstract, but the row's own current
		// status does not match ExpectedStatus.
		_, err := catalogRepository{tx: tx}.TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: "repo-1", ExpectedStatus: project.RepositoryBlocked, ExpectedVersion: 1,
			NextStatus: project.RepositoryProbing, LastProbeErrorCode: nil,
		})
		if !errors.Is(err, ports.ErrOptimisticConflict) {
			t.Fatalf("err = %v, want ports.ErrOptimisticConflict", err)
		}
		return nil
	})
}

func TestCatalogRepository_TransitionRepositoryStatus_NotFound(t *testing.T) {
	store := openCatalogTestStore(t, "catalog-transition-notfound.db")
	ctx := context.Background()
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := catalogRepository{tx: tx}.TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: "repo-missing", ExpectedStatus: project.RepositoryRegistering, ExpectedVersion: 1,
			NextStatus: project.RepositoryProbing, LastProbeErrorCode: nil,
		})
		if !errors.Is(err, ports.ErrPersistenceNotFound) {
			t.Fatalf("err = %v, want ports.ErrPersistenceNotFound", err)
		}
		return nil
	})
}

// TestCatalogRepository_TransitionRepositoryStatus_IllegalTransition_Rejected
// proves TransitionRepositoryStatus validates the edge itself
// (project.CanTransitionRepositoryStatus) before ever touching storage —
// REGISTERING can never jump straight to ACTIVE, skipping PROBING
// entirely, no matter what ExpectedVersion a caller supplies.
func TestCatalogRepository_TransitionRepositoryStatus_IllegalTransition_Rejected(t *testing.T) {
	store := openCatalogTestStore(t, "catalog-transition-illegal.db")
	ctx := context.Background()
	seedProject(t, store, "project-1")
	seedRepository(t, store, "project-1", "repo-1")

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := catalogRepository{tx: tx}.TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: "repo-1", ExpectedStatus: project.RepositoryRegistering, ExpectedVersion: 1,
			NextStatus: project.RepositoryActive, LastProbeErrorCode: nil,
		})
		if !errors.Is(err, project.ErrIllegalRepositoryTransition) {
			t.Fatalf("err = %v, want project.ErrIllegalRepositoryTransition", err)
		}
		return nil
	})

	// The illegal request must never have touched the row at all.
	var loaded project.Repository
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		loaded, err = catalogRepository{tx: tx}.GetRepository(ctx, "repo-1")
		return err
	})
	if loaded.Status != project.RepositoryRegistering || loaded.Version != 1 {
		t.Fatalf("loaded = %+v, want unchanged Status=REGISTERING Version=1", loaded)
	}
}

// TestCatalogRepository_TransitionRepositoryStatus_SetsThenClearsLastProbeErrorCode
// proves LastProbeErrorCode is always written exactly as given: PROBING->BLOCKED
// sets it, and the following BLOCKED->PROBING retry clears it back to nil —
// there is no third "leave unchanged" mode.
func TestCatalogRepository_TransitionRepositoryStatus_SetsThenClearsLastProbeErrorCode(t *testing.T) {
	store := openCatalogTestStore(t, "catalog-transition-error-code.db")
	ctx := context.Background()
	seedProject(t, store, "project-1")
	seedRepository(t, store, "project-1", "repo-1")

	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := catalogRepository{tx: tx}.TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: "repo-1", ExpectedStatus: project.RepositoryRegistering, ExpectedVersion: 1,
			NextStatus: project.RepositoryProbing, LastProbeErrorCode: nil,
		})
		return err
	})
	code := "UNAVAILABLE"
	var blocked project.Repository
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		blocked, err = catalogRepository{tx: tx}.TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: "repo-1", ExpectedStatus: project.RepositoryProbing, ExpectedVersion: 2,
			NextStatus: project.RepositoryBlocked, LastProbeErrorCode: &code,
		})
		return err
	})
	if blocked.LastProbeErrorCode == nil || *blocked.LastProbeErrorCode != "UNAVAILABLE" {
		t.Fatalf("blocked.LastProbeErrorCode = %v, want \"UNAVAILABLE\"", blocked.LastProbeErrorCode)
	}

	var retried project.Repository
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		retried, err = catalogRepository{tx: tx}.TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: "repo-1", ExpectedStatus: project.RepositoryBlocked, ExpectedVersion: 3,
			NextStatus: project.RepositoryProbing, LastProbeErrorCode: nil,
		})
		return err
	})
	if retried.LastProbeErrorCode != nil {
		t.Fatalf("retried.LastProbeErrorCode = %v, want nil (cleared by the retry)", retried.LastProbeErrorCode)
	}
}

// TestCatalogRepository_TransitionRepositoryStatus_ConcurrentCAS_OnlyOneWins
// is a real concurrency proof (real sqlite, real goroutines, no fake) that
// the CAS is genuinely exclusive: N goroutines race to CAS the exact same
// (RepositoryID, ExpectedStatus, ExpectedVersion) REGISTERING->PROBING at
// once — exactly one must succeed, every other must lose with
// ports.ErrOptimisticConflict, and the row's final version must reflect
// exactly one applied transition, never more.
func TestCatalogRepository_TransitionRepositoryStatus_ConcurrentCAS_OnlyOneWins(t *testing.T) {
	store := openCatalogTestStore(t, "catalog-transition-concurrent-cas.db")
	ctx := context.Background()
	seedProject(t, store, "project-1")
	seedRepository(t, store, "project-1", "repo-1")

	const racers = 8
	var succeeded atomic.Int32
	var conflicted atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
				_, err := catalogRepository{tx: tx}.TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
					RepositoryID: "repo-1", ExpectedStatus: project.RepositoryRegistering, ExpectedVersion: 1,
					NextStatus: project.RepositoryProbing, LastProbeErrorCode: nil,
				})
				return err
			})
			switch {
			case err == nil:
				succeeded.Add(1)
			case errors.Is(err, ports.ErrOptimisticConflict):
				conflicted.Add(1)
			default:
				t.Errorf("unexpected error racing TransitionRepositoryStatus: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if succeeded.Load() != 1 {
		t.Fatalf("succeeded = %d, want exactly 1 (two+ racing CAS callers must never both win)", succeeded.Load())
	}
	if conflicted.Load() != racers-1 {
		t.Fatalf("conflicted = %d, want %d", conflicted.Load(), racers-1)
	}

	var loaded project.Repository
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		loaded, err = catalogRepository{tx: tx}.GetRepository(ctx, "repo-1")
		return err
	})
	if loaded.Status != project.RepositoryProbing || loaded.Version != 2 {
		t.Fatalf("loaded = %+v, want exactly one applied transition (Status=PROBING Version=2)", loaded)
	}
}

// --- RepositoryProbeAttempt (V3-02, append-only, active probe idempotent) ---

func TestCatalogRepository_RecordRepositoryProbeAttempt_And_List(t *testing.T) {
	store := openCatalogTestStore(t, "catalog-probe-attempt-record-list.db")
	ctx := context.Background()
	seedProject(t, store, "project-1")
	seedRepository(t, store, "project-1", "repo-1")
	seedProbeJob(t, store, "project-1", "repo-1", "job-1")
	seedProbeJob(t, store, "project-1", "repo-1", "job-2")

	blockedResult := project.RepositoryBlocked
	errorCode := "INVALID_ARGUMENT"
	errorMessage := "repository local path is not a Git working tree"
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := catalogRepository{tx: tx}.RecordRepositoryProbeAttempt(ctx, ports.RecordRepositoryProbeAttemptRequest{
			ID: "attempt-1", ProjectID: "project-1", RepositoryID: "repo-1", JobID: "job-1",
			State: ports.RepositoryProbeAttemptSucceeded, Result: &blockedResult,
			ErrorCode: &errorCode, ErrorMessage: &errorMessage,
		})
		return err
	})

	activeResult := project.RepositoryActive
	baseCommit := "abc123abc123abc123abc123abc123abc123ab"
	dirty := true
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := catalogRepository{tx: tx}.RecordRepositoryProbeAttempt(ctx, ports.RecordRepositoryProbeAttemptRequest{
			ID: "attempt-2", ProjectID: "project-1", RepositoryID: "repo-1", JobID: "job-2",
			State: ports.RepositoryProbeAttemptSucceeded, Result: &activeResult,
			BaseCommit: &baseCommit, Dirty: &dirty,
		})
		return err
	})

	var attempts []ports.RepositoryProbeAttempt
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		attempts, err = catalogRepository{tx: tx}.ListRepositoryProbeAttempts(ctx, "repo-1")
		return err
	})
	if len(attempts) != 2 {
		t.Fatalf("attempts = %+v, want exactly 2 rows", attempts)
	}
	first, second := attempts[0], attempts[1]
	if first.JobID != "job-1" || first.Result == nil || *first.Result != project.RepositoryBlocked {
		t.Fatalf("attempts[0] = %+v, want JobID=job-1 Result=BLOCKED", first)
	}
	if first.ErrorCode == nil || *first.ErrorCode != "INVALID_ARGUMENT" || first.BaseCommit != nil || first.Dirty != nil {
		t.Fatalf("attempts[0] = %+v, want ErrorCode set and BaseCommit/Dirty nil", first)
	}
	if second.JobID != "job-2" || second.Result == nil || *second.Result != project.RepositoryActive {
		t.Fatalf("attempts[1] = %+v, want JobID=job-2 Result=ACTIVE", second)
	}
	if second.BaseCommit == nil || *second.BaseCommit != baseCommit || second.Dirty == nil || !*second.Dirty || second.ErrorCode != nil {
		t.Fatalf("attempts[1] = %+v, want BaseCommit/Dirty set and ErrorCode nil", second)
	}
}

// TestCatalogRepository_RecordRepositoryProbeAttempt_DuplicateJobID_Rejected
// is "active probe idempotent" (docs/design/01-system-design.md §6.1) made
// concrete at the storage layer: the exact same durable job can never
// produce two attempt rows.
func TestCatalogRepository_RecordRepositoryProbeAttempt_DuplicateJobID_Rejected(t *testing.T) {
	store := openCatalogTestStore(t, "catalog-probe-attempt-duplicate-job.db")
	ctx := context.Background()
	seedProject(t, store, "project-1")
	seedRepository(t, store, "project-1", "repo-1")
	seedProbeJob(t, store, "project-1", "repo-1", "job-1")

	activeResult := project.RepositoryActive
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		_, err := catalogRepository{tx: tx}.RecordRepositoryProbeAttempt(ctx, ports.RecordRepositoryProbeAttemptRequest{
			ID: "attempt-1", ProjectID: "project-1", RepositoryID: "repo-1", JobID: "job-1",
			State: ports.RepositoryProbeAttemptSucceeded, Result: &activeResult,
		})
		return err
	})

	err := store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
		_, err := catalogRepository{tx: tx}.RecordRepositoryProbeAttempt(ctx, ports.RecordRepositoryProbeAttemptRequest{
			ID: "attempt-2", ProjectID: "project-1", RepositoryID: "repo-1", JobID: "job-1", // same job id
			State: ports.RepositoryProbeAttemptSucceeded, Result: &activeResult,
		})
		return err
	})
	if err == nil {
		t.Fatal("second RecordRepositoryProbeAttempt with a duplicate job_id succeeded, want a UNIQUE(job_id) violation")
	}

	var attempts []ports.RepositoryProbeAttempt
	withCatalogTx(t, store, func(tx *sql.Tx) error {
		var err error
		attempts, err = catalogRepository{tx: tx}.ListRepositoryProbeAttempts(ctx, "repo-1")
		return err
	})
	if len(attempts) != 1 {
		t.Fatalf("attempts = %+v, want exactly 1 row (the duplicate must never have been written)", attempts)
	}
}
