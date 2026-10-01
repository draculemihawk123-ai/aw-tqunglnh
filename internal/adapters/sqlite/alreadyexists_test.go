package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// V9-09 / B2: creating a Definition or registering a Repository with an id
// that already exists used to surface as MapSQLiteError's opaque
// "sqlite: unexpected error" (CodeInternal; HTTP 500 for LIM-06). Both are
// now the typed ports.ErrPersistenceAlreadyExists.

func requireAlreadyExists(t *testing.T, err error, wantInMessage string) {
	t.Helper()
	if err == nil {
		t.Fatalf("err = nil, want ports.ErrPersistenceAlreadyExists (%s)", wantInMessage)
	}
	if !errors.Is(err, ports.ErrPersistenceAlreadyExists) {
		t.Fatalf("err = %v, want ports.ErrPersistenceAlreadyExists", err)
	}
	var appErr *apperror.Error
	if errors.As(err, &appErr) {
		t.Fatalf("err = %v is still the opaque apperror %q; want the typed sentinel", err, appErr.Code)
	}
	if strings.Contains(err.Error(), "unexpected error") {
		t.Fatalf("err = %q still reads as an unexpected sqlite error", err)
	}
	if !strings.Contains(err.Error(), wantInMessage) {
		t.Fatalf("err = %q, want it to name %q", err, wantInMessage)
	}
}

// TestIsUniqueConstraintViolation_ClassifiesByDriverCode proves the
// classification the backstop rests on is made from the driver's typed
// result code: a PRIMARY KEY and a UNIQUE failure are recognized, a
// FOREIGN KEY and a CHECK failure are not.
func TestIsUniqueConstraintViolation_ClassifiesByDriverCode(t *testing.T) {
	store := openCatalogTestStore(t, "alreadyexists-classify.db")
	ctx := context.Background()
	seedProject(t, store, "project-1")
	seedRepository(t, store, "project-1", "repo-1")

	exec := func(statement string, args ...any) error {
		return store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, statement, args...)
			return err
		})
	}
	insertRepository := func(id, projectID, name string) error {
		return exec(`INSERT INTO repositories (id, project_id, name, local_path, default_ref, status, version, created_at, updated_at)
VALUES (?, ?, ?, 'p', 'main', 'REGISTERING', 1, 't', 't')`, id, projectID, name)
	}

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"primary key", insertRepository("repo-1", "project-1", "other-name"), true},
		{"unique (project_id, name)", insertRepository("repo-2", "project-1", "svc-repo-1"), true},
		{"foreign key", insertRepository("repo-3", "project-missing", "n"), false},
		{"check constraint", exec(`INSERT INTO projects (id, name, status, version, created_at, updated_at) VALUES ('p2', 'n', 'ACTIVE', 0, 't', 't')`), false},
		{"nil", nil, false},
		{"not a driver error", errors.New("boom"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name != "nil" && tc.name != "not a driver error" && tc.err == nil {
				t.Fatal("the fixture statement unexpectedly succeeded")
			}
			if got := isUniqueConstraintViolation(tc.err); got != tc.want {
				t.Fatalf("isUniqueConstraintViolation(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestCreateSharedDefinition_DuplicateID_IsTypedAlreadyExists reaches the
// INSERT-level backstop directly (Store.CreateSharedDefinition has no
// pre-insert existence check): the PRIMARY KEY failure is mapped, not
// wrapped as an unexpected error.
func TestCreateSharedDefinition_DuplicateID_IsTypedAlreadyExists(t *testing.T) {
	store := openDefinitionsTestStore(t, "alreadyexists-shared-backstop.db")
	createTestSharedDefinition(t, store, "def-dup", definition.KindSkill, definition.GlobalScope(), "first")

	err := store.CreateSharedDefinition(context.Background(), "def-dup", definition.KindSkill, definition.GlobalScope(), "second", time.Now().UTC())
	requireAlreadyExists(t, err, "definition def-dup")
}

// TestDefinitionsRepository_CreateDefinition_DuplicateID_IsTypedAlreadyExists
// is the repository-port level proof, over every pairing that used to fail
// as "sqlite: unexpected error": the same shared kind, a different shared
// kind, a global definition re-created as project-scoped, and — through the
// DefinitionCreated event stream, which is keyed by id alone — both Workflow
// pairings.
func TestDefinitionsRepository_CreateDefinition_DuplicateID_IsTypedAlreadyExists(t *testing.T) {
	store := openDefinitionsTestStore(t, "alreadyexists-definitions-port.db")
	ctx := context.Background()
	seedProject(t, store, "project-1")
	project1 := project.ProjectID("project-1")

	create := func(id string, kind definition.Kind, scope definition.Scope) error {
		return store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
			return definitionsRepository{tx: tx}.CreateDefinition(ctx, id, kind, scope, "name "+id, time.Now().UTC())
		})
	}

	if err := create("shared-1", definition.KindSkill, definition.GlobalScope()); err != nil {
		t.Fatalf("first create shared-1: %v", err)
	}
	if err := create("workflow-1", definition.KindWorkflow, definition.GlobalScope()); err != nil {
		t.Fatalf("first create workflow-1: %v", err)
	}

	cases := []struct {
		name  string
		id    string
		kind  definition.Kind
		scope definition.Scope
	}{
		{"same shared kind", "shared-1", definition.KindSkill, definition.GlobalScope()},
		{"different shared kind", "shared-1", definition.KindBlock, definition.GlobalScope()},
		{"project scope over a global id", "shared-1", definition.KindSkill, definition.ProjectScope(project1)},
		{"workflow over a shared id", "shared-1", definition.KindWorkflow, definition.GlobalScope()},
		{"workflow twice", "workflow-1", definition.KindWorkflow, definition.GlobalScope()},
		{"shared over a workflow id", "workflow-1", definition.KindSkill, definition.GlobalScope()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := create(tc.id, tc.kind, tc.scope)
			requireAlreadyExists(t, err, "definition "+tc.id)
		})
	}

	// A genuinely new id is still created: the check is not a blanket refusal.
	if err := create("shared-2", definition.KindSkill, definition.GlobalScope()); err != nil {
		t.Fatalf("create of a new id: %v", err)
	}
}

// TestCatalogRepository_RegisterRepository_DuplicateIDOrName_IsTypedAlreadyExists
// is LIM-06 at the persistence boundary: a taken repository id, and a name
// already used inside the same project (UNIQUE (project_id, name)), are the
// typed sentinel; the same name in ANOTHER project stays allowed.
func TestCatalogRepository_RegisterRepository_DuplicateIDOrName_IsTypedAlreadyExists(t *testing.T) {
	store := openCatalogTestStore(t, "alreadyexists-repository.db")
	ctx := context.Background()
	seedProject(t, store, "project-1")
	seedProject(t, store, "project-2")

	register := func(id, projectID, name string) error {
		return store.RunSerializedWrite(ctx, func(tx *sql.Tx) error {
			_, err := catalogRepository{tx: tx}.RegisterRepository(ctx, ports.RegisterRepositoryRequest{
				ID: id, ProjectID: projectID, Name: name, RemoteLocator: "https://example.invalid/" + id + ".git", DefaultRef: "main",
			})
			return err
		})
	}

	if err := register("repo-1", "project-1", "svc"); err != nil {
		t.Fatalf("first register: %v", err)
	}
	requireAlreadyExists(t, register("repo-1", "project-1", "other"), "repository repo-1")
	requireAlreadyExists(t, register("repo-1", "project-2", "svc"), "repository repo-1")
	requireAlreadyExists(t, register("repo-2", "project-1", "svc"), `repository name "svc"`)
	if err := register("repo-2", "project-2", "svc"); err != nil {
		t.Fatalf("same name in another project must stay allowed: %v", err)
	}
}
