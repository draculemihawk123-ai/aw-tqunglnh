package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

func openCatalogTestStore(t *testing.T, name string) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), name))
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
