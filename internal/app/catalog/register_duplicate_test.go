package catalog_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
)

// V9-09 / B2 (LIM-06): registering a repository whose id already exists
// returned an opaque persistence error (HTTP 500). It is now the typed
// ports.ErrPersistenceAlreadyExists (wire code CONFLICT, HTTP 409), the
// rejected attempts create no row, and an idempotent
// re-register with the SAME idempotency key keeps replaying the first
// result. Run against the in-memory fake and the real sqlite adapter, which
// must agree.
func TestRegisterRepository_ExistingID_IsTypedAlreadyExists_ReplayStillWorks(t *testing.T) {
	ctx := context.Background()
	backends := map[string]func(t *testing.T) ports.UnitOfWork{
		"fake": func(t *testing.T) ports.UnitOfWork { return fake.New() },
		"sqlite": func(t *testing.T) ports.UnitOfWork {
			store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "register-duplicate.db"))
			if err != nil {
				t.Fatalf("sqlite.Open: %v", err)
			}
			t.Cleanup(func() { store.Close() })
			return sqlite.NewUnitOfWork(store)
		},
	}
	for name, newUoW := range backends {
		t.Run(name, func(t *testing.T) {
			uow := newUoW(t)
			ids := idsource.NewSequential("id")
			created, err := catalog.CreateProject(ctx, uow, ids,
				testCommand("idem-project", "hash-project", ports.InstallationScope(), "CreateProject"),
				catalog.CreateProjectRequest{Name: "demo"})
			if err != nil {
				t.Fatalf("CreateProject: %v", err)
			}
			scope := ports.ProjectScope(created.ProjectID)
			register := func(key, hash, repositoryID, repositoryName string) (catalog.RegisterRepositoryResult, error) {
				return catalog.RegisterRepository(ctx, uow, ids, testCommand(key, hash, scope, "RegisterRepository"), catalog.RegisterRepositoryRequest{
					RepositoryID: repositoryID, ProjectID: created.ProjectID, Name: repositoryName,
					RemoteLocator: "https://example.invalid/repo.git", DefaultRef: "main",
				})
			}

			first, err := register("idem-1", "hash-a", "repo-1", "svc")
			if err != nil {
				t.Fatalf("first RegisterRepository: %v", err)
			}

			// A DIFFERENT command (new idempotency key) for the same id.
			if _, err := register("idem-2", "hash-b", "repo-1", "other-name"); !errors.Is(err, ports.ErrPersistenceAlreadyExists) {
				t.Fatalf("duplicate id err = %v, want ports.ErrPersistenceAlreadyExists", err)
			}
			// A different id but a name already used inside this project.
			if _, err := register("idem-3", "hash-c", "repo-2", "svc"); !errors.Is(err, ports.ErrPersistenceAlreadyExists) {
				t.Fatalf("duplicate name err = %v, want ports.ErrPersistenceAlreadyExists", err)
			}

			// Replaying the first command's own key is untouched.
			replayed, err := register("idem-1", "hash-a", "repo-1", "svc")
			if err != nil {
				t.Fatalf("replay of the first command: %v", err)
			}
			if replayed != first {
				t.Fatalf("replayed result = %+v, want identical to first %+v", replayed, first)
			}

			// The rejected attempts created nothing.
			err = uow.WithReadOnly(ctx, func(tx ports.Tx) error {
				if _, err := tx.Catalog().GetRepository(ctx, "repo-2"); !errors.Is(err, ports.ErrPersistenceNotFound) {
					t.Errorf("repo-2 lookup err = %v, want not found (the rejected register must not create it)", err)
				}
				return nil
			})
			if err != nil {
				t.Fatalf("WithReadOnly: %v", err)
			}
		})
	}
}
