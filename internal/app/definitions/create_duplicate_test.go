package definitions_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/definitions"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// V9-09 / B2: `definition create` with a definitionId that already exists
// returned the opaque "sqlite: unexpected error". It is now the typed
// ports.ErrPersistenceAlreadyExists (wire code CONFLICT) while an
// idempotent re-create with the SAME idempotency key keeps replaying the
// first result exactly as before. Run against both the in-memory fake and
// the real sqlite adapter, which must agree.
func TestCreateDefinition_ExistingID_IsTypedAlreadyExists_ReplayStillWorks(t *testing.T) {
	ctx := context.Background()
	backends := map[string]func(t *testing.T) ports.UnitOfWork{
		"fake": func(t *testing.T) ports.UnitOfWork { return fake.New() },
		"sqlite": func(t *testing.T) ports.UnitOfWork {
			store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "create-duplicate.db"))
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
			first := testCommand("idem-1", "hash-a", ports.InstallationScope(), "CreateDefinition")
			req := definitions.CreateDefinitionRequest{DefinitionID: "def-1", Kind: definition.KindSkill, Scope: definition.GlobalScope(), Name: "first"}
			if _, err := definitions.CreateDefinition(ctx, uow, first, req); err != nil {
				t.Fatalf("first CreateDefinition: %v", err)
			}

			// A DIFFERENT command (new idempotency key) for the same id.
			second := testCommand("idem-2", "hash-b", ports.InstallationScope(), "CreateDefinition")
			_, err := definitions.CreateDefinition(ctx, uow, second, definitions.CreateDefinitionRequest{
				DefinitionID: "def-1", Kind: definition.KindSkill, Scope: definition.GlobalScope(), Name: "second",
			})
			if !errors.Is(err, ports.ErrPersistenceAlreadyExists) {
				t.Fatalf("duplicate CreateDefinition err = %v, want ports.ErrPersistenceAlreadyExists", err)
			}

			// The same id under another kind is the same conflict: ids are
			// one namespace across all nine kinds.
			_, err = definitions.CreateDefinition(ctx, uow, testCommand("idem-3", "hash-c", ports.InstallationScope(), "CreateDefinition"),
				definitions.CreateDefinitionRequest{DefinitionID: "def-1", Kind: definition.KindWorkflow, Scope: definition.GlobalScope(), Name: "wf"})
			if !errors.Is(err, ports.ErrPersistenceAlreadyExists) {
				t.Fatalf("cross-kind duplicate err = %v, want ports.ErrPersistenceAlreadyExists", err)
			}

			// The idempotent re-create with the first command's own key is
			// untouched: it replays the stored result and never re-checks.
			replayed, err := definitions.CreateDefinition(ctx, uow, first, req)
			if err != nil {
				t.Fatalf("replay of the first command: %v", err)
			}
			if replayed.DefinitionID != "def-1" || replayed.Kind != definition.KindSkill {
				t.Fatalf("replayed result = %+v, want def-1/SKILL", replayed)
			}

			// A failed duplicate leaves nothing behind: exactly one
			// DefinitionCreated event exists for the id.
			if name == "fake" {
				events := uow.(*fake.UnitOfWork).Snapshot.Events().(*fake.EventsRepository).Items()
				if len(events) != 1 {
					t.Fatalf("events = %d, want exactly 1 DefinitionCreated after the rejected duplicates", len(events))
				}
			}
		})
	}
}

// TestCreateDefinition_ExistingWorkflowID_IsTypedAlreadyExists covers the
// Workflow path, whose duplicate used to fail one step later (the
// DefinitionCreated event stream's UNIQUE key) rather than at the
// definition row.
func TestCreateDefinition_ExistingWorkflowID_IsTypedAlreadyExists(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "create-duplicate-workflow.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer store.Close()
	uow := sqlite.NewUnitOfWork(store)

	req := definitions.CreateDefinitionRequest{DefinitionID: "wf-1", Kind: definition.KindWorkflow, Scope: definition.GlobalScope(), Name: "wf"}
	if _, err := definitions.CreateDefinition(ctx, uow, testCommand("idem-1", "hash-a", ports.InstallationScope(), "CreateDefinition"), req); err != nil {
		t.Fatalf("first CreateDefinition: %v", err)
	}
	_, err = definitions.CreateDefinition(ctx, uow, testCommand("idem-2", "hash-b", ports.InstallationScope(), "CreateDefinition"), req)
	if !errors.Is(err, ports.ErrPersistenceAlreadyExists) {
		t.Fatalf("duplicate workflow CreateDefinition err = %v, want ports.ErrPersistenceAlreadyExists", err)
	}
}
