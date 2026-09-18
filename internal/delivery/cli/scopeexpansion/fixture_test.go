package scopeexpansion_test

// This file duplicates internal/delivery/cli/run's own fixture_test.go
// idiom (itself a duplicate of internal/app/work's own commands_test.go
// helpers) rather than importing them — Go test helpers in a _test.go file
// are not exported across packages. Every fixture helper here drives a
// REAL *sqlite.Store through the REAL application commands
// (catalog.RegisterRepository, work.CreateRootWorkItem) — never a
// hand-seeded row and never a mock — and uses real sqlite rather than
// fake.UnitOfWork specifically because this package's own concurrent/race
// tests need real cross-goroutine transaction serialization
// (internal/adapters/sqlite's own _txlock=immediate connections, exactly
// the reasoning work/scope_expansion.go's own ApproveScopeExpansion doc
// comment gives) — fake.UnitOfWork's own WithSerializedWrite rejects a
// genuinely concurrent second caller outright (ErrNestedTransaction)
// rather than blocking and serializing it, so it cannot stand in for that
// here.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli/scopeexpansion"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

func openScopeExpansionCLITestStore(t *testing.T, name string) (*sqlite.Store, ports.UnitOfWork) {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, sqlite.NewUnitOfWork(store)
}

func newTestDeps(uow ports.UnitOfWork) scopeexpansion.Dependencies {
	fixedNow := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	return scopeexpansion.Dependencies{
		UOW: uow, IDs: idsource.NewSequential("id"),
		Now: func() time.Time { return fixedNow },
	}
}

func seedProject(t *testing.T, uow ports.UnitOfWork, projectID string) {
	t.Helper()
	ctx := context.Background()
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(ctx, ports.CreateProjectRequest{ID: projectID, Name: "project " + projectID})
		return err
	})
	if err != nil {
		t.Fatalf("seed project %s: %v", projectID, err)
	}
}

func seedActiveRepository(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID string) {
	t.Helper()
	ctx := context.Background()
	regCmd := ports.Command{
		ID: "cmd-reg-" + repositoryID, IdempotencyKey: "idem-reg-" + repositoryID, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope(projectID), RequestedAt: time.Now().UTC(),
		Type: "RegisterRepository", RequestHash: "hash-reg-" + repositoryID,
	}
	if _, err := catalog.RegisterRepository(ctx, uow, ids, regCmd, catalog.RegisterRepositoryRequest{
		RepositoryID: repositoryID, ProjectID: projectID, Name: repositoryID,
		RemoteLocator: "https://example.invalid/" + repositoryID + ".git", DefaultRef: "main",
	}); err != nil {
		t.Fatalf("RegisterRepository(%s): %v", repositoryID, err)
	}
	err := uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if _, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: repositoryID, ExpectedStatus: project.RepositoryRegistering, ExpectedVersion: 1,
			NextStatus: project.RepositoryProbing,
		}); err != nil {
			return err
		}
		_, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: repositoryID, ExpectedStatus: project.RepositoryProbing, ExpectedVersion: 2,
			NextStatus: project.RepositoryActive,
		})
		return err
	})
	if err != nil {
		t.Fatalf("activate repository %s: %v", repositoryID, err)
	}
}

// familyFixture seeds a project and one ACTIVE repository, then creates a
// root WorkItem/TaskFamily over it (never driven to READY — scope
// expansion has no such precondition) — this package's own shared
// "already have a family to expand" starting point.
func familyFixture(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID string) work.CreateRootWorkItemResult {
	t.Helper()
	ctx := context.Background()
	seedProject(t, uow, projectID)
	seedActiveRepository(t, uow, ids, projectID, repositoryID)

	cmd := ports.Command{
		ID: "cmd-root-" + repositoryID, IdempotencyKey: "idem-root-" + repositoryID, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope(projectID), RequestedAt: time.Now().UTC(),
		Type: "CreateRootWorkItem", RequestHash: "hash-root-" + repositoryID,
	}
	root, err := work.CreateRootWorkItem(ctx, uow, ids, cmd, work.CreateRootWorkItemRequest{
		ProjectID: projectID, Title: "Implement the thing",
		InitialScope: []work.ScopeGrantRequest{{
			RepositoryID: repositoryID, Access: string(workdomain.RepositoryWrite),
			PathScopes: []string{"**"}, Reason: "root task",
		}},
	})
	if err != nil {
		t.Fatalf("CreateRootWorkItem: %v", err)
	}
	return root
}

// mustRequestScopeExpansion runs `scope-expansion request` for real (never
// a direct work.RequestScopeExpansion call) — shared setup for every
// approve/reject/withdraw test in this package. Returns the freshly
// created RequestID (always at Version 1 — work.NewScopeExpansionRequest
// hardcodes it, confirmed by internal/delivery/httpapi/workitem's own
// scope_expansion_commands.go doc comment).
func mustRequestScopeExpansion(t *testing.T, deps scopeexpansion.Dependencies, familyID, projectID, repositoryID, idempotencyKey string) string {
	t.Helper()
	var stdout bytes.Buffer
	body := `{"requestedGrants":[{"repositoryId":"` + repositoryID + `","access":"WRITE","reason":"need it"}],"reason":"expand scope"}`
	args := []string{"--project-id", projectID, "--idempotency-key", idempotencyKey, familyID}
	if err := scopeexpansion.Request(context.Background(), deps, args, strings.NewReader(body), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("Request(%s): %v", familyID, err)
	}
	var envelope struct {
		Result struct {
			RequestID string `json:"requestId"`
		} `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode Request result: %v", err)
	}
	return envelope.Result.RequestID
}

// writePrincipalFile writes a minimal trusted local-principal config file
// (config.LoadLocalPrincipalFile's own wire shape) to a temp file and
// returns its path — this package's own shared --principal-config fixture.
func writePrincipalFile(t *testing.T, actor string, roles []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "principal.json")
	rolesJSON := `[]`
	if len(roles) > 0 {
		rolesJSON = `["` + roles[0] + `"`
		for _, r := range roles[1:] {
			rolesJSON += `,"` + r + `"`
		}
		rolesJSON += `]`
	}
	content := `{"localPrincipal": {"actor": "` + actor + `", "roles": ` + rolesJSON + `}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write principal file: %v", err)
	}
	return path
}
