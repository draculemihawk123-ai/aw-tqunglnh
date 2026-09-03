package work_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

func openSQLiteStore(t *testing.T, name string) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func seedProjectSQLite(t *testing.T, uow ports.UnitOfWork, id string) {
	t.Helper()
	err := uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(context.Background(), ports.CreateProjectRequest{ID: id, Name: "project " + id})
		return err
	})
	if err != nil {
		t.Fatalf("seed project %s: %v", id, err)
	}
}

// seedActiveRepositorySQLite registers a repository then drives it
// REGISTERING->PROBING->ACTIVE directly via tx.Catalog(), standing in for
// what V3-02's own REPOSITORY_PROBE job handler would otherwise do — test
// setup, not the behavior under test.
func seedActiveRepositorySQLite(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID string) {
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
			NextStatus: project.RepositoryProbing, LastProbeErrorCode: nil,
		}); err != nil {
			return err
		}
		_, err := tx.Catalog().TransitionRepositoryStatus(ctx, ports.TransitionRepositoryStatusRequest{
			RepositoryID: repositoryID, ExpectedStatus: project.RepositoryProbing, ExpectedVersion: 2,
			NextStatus: project.RepositoryActive, LastProbeErrorCode: nil,
		})
		return err
	})
	if err != nil {
		t.Fatalf("activate repository %s: %v", repositoryID, err)
	}
}

func seedRegisteringRepositorySQLite(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, projectID, repositoryID string) {
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
}

// TestCreateRootWorkItem_RollbackOnMidTransactionFailure_NoOrphanRows is
// this task's single most important test (V3-04's own "Hoàn thành khi:
// không có root task orphan family/workspace hoặc job intent thiếu"): a
// REAL failure partway through the transaction — repo-a is ACTIVE and its
// own WorkItem/TaskFamily/WorkspaceSet/scope/provision-job all get written
// to the live *sql.Tx first, then repo-b (still REGISTERING) fails the
// ACTIVE check — must roll back EVERYTHING, leaving zero rows in
// work_items/task_families/workspace_sets/family_repository_scopes/
// durable_jobs. Asserted against a real sqlite database, not the fake,
// and not merely "the command returned an error".
func TestCreateRootWorkItem_RollbackOnMidTransactionFailure_NoOrphanRows(t *testing.T) {
	ctx := context.Background()
	store := openSQLiteStore(t, "agentkit-work-rollback.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.Random{}

	seedProjectSQLite(t, uow, "project-1")
	seedActiveRepositorySQLite(t, uow, ids, "project-1", "repo-a")
	seedRegisteringRepositorySQLite(t, uow, ids, "project-1", "repo-b") // deliberately never activated

	cmd := ports.Command{
		ID: "cmd-root-1", IdempotencyKey: "idem-root-1", Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope("project-1"), RequestedAt: time.Now().UTC(),
		Type: "CreateRootWorkItem", RequestHash: "hash-root-1",
	}
	_, err := work.CreateRootWorkItem(ctx, uow, ids, cmd, baseRequest("project-1", grant("repo-a"), grant("repo-b")))
	if !errors.Is(err, work.ErrRepositoryNotActive) {
		t.Fatalf("CreateRootWorkItem err = %v, want work.ErrRepositoryNotActive (repo-b is still REGISTERING)", err)
	}

	// Prove the rollback by direct table counts, not by re-deriving IDs the
	// failed attempt minted (idsource.Random makes those unrecoverable by
	// design) — count every row in each of the five tables this command
	// touches and require zero, the strongest possible "no orphan" proof.
	assertCount(t, "work_items", 0, func() (int, error) { return store.CountWorkItems(ctx) })
	assertCount(t, "task_families", 0, func() (int, error) { return store.CountTaskFamilies(ctx) })
	assertCount(t, "workspace_sets", 0, func() (int, error) { return store.CountWorkspaceSets(ctx) })
	assertCount(t, "family_repository_scopes", 0, func() (int, error) { return store.CountFamilyRepositoryScopes(ctx) })

	// durable_jobs must contain only the two REPOSITORY_PROBE jobs test
	// setup itself enqueued (repo-a's real probe, repo-b's registration
	// probe) — never a WORKSPACE_PROVISION row for repo-a, even though
	// repo-a's own scope grant was valid and would have succeeded in
	// isolation.
	count, err := store.CountDurableJobsByIdempotencyKey(ctx, "idem-root-1-provision-repo-a")
	if err != nil {
		t.Fatalf("CountDurableJobsByIdempotencyKey(repo-a provision): %v", err)
	}
	if count != 0 {
		t.Fatalf("repo-a provision job count = %d, want 0 (rollback must remove it)", count)
	}
	count, err = store.CountDurableJobsByIdempotencyKey(ctx, "idem-root-1-provision-repo-b")
	if err != nil {
		t.Fatalf("CountDurableJobsByIdempotencyKey(repo-b provision): %v", err)
	}
	if count != 0 {
		t.Fatalf("repo-b provision job count = %d, want 0", count)
	}

	// No RootWorkItemCreated event may have committed either.
	eventCount, err := store.CountDomainEventsByType(ctx, "RootWorkItemCreated")
	if err != nil {
		t.Fatalf("CountDomainEventsByType: %v", err)
	}
	if eventCount != 0 {
		t.Fatalf("RootWorkItemCreated event count = %d, want 0", eventCount)
	}

	// No command receipt for the failed command either — a failed attempt
	// must remain retryable, never poisoned by a partially recorded
	// receipt.
	receiptCount, err := store.CountCommandReceiptsByIdempotencyKey(ctx, "idem-root-1")
	if err != nil {
		t.Fatalf("CountCommandReceiptsByIdempotencyKey: %v", err)
	}
	if receiptCount != 0 {
		t.Fatalf("command receipt count = %d, want 0 (a failed command must remain retryable)", receiptCount)
	}
}

// assertCount calls fn (a Store.Count* method call, deferred so this helper
// can name which one failed) and fails the test if it either errored or
// returned something other than want.
func assertCount(t *testing.T, label string, want int, fn func() (int, error)) {
	t.Helper()
	got, err := fn()
	if err != nil {
		t.Fatalf("count %s: %v", label, err)
	}
	if got != want {
		t.Fatalf("%s row count = %d, want %d", label, got, want)
	}
}

// TestCreateRootWorkItem_MultiRepoFixtureSQLite is AK-ARCH-011 made
// concrete against a real sqlite database: "Project hai repository tạo
// được TaskFamily/WorkspaceSet với hai worktree độc lập" — two
// repositories in the same project, both ACTIVE, both granted scope on one
// root WorkItem, each getting its own independent RepositoryScope row and
// WORKSPACE_PROVISION job.
func TestCreateRootWorkItem_MultiRepoFixtureSQLite(t *testing.T) {
	ctx := context.Background()
	store := openSQLiteStore(t, "agentkit-work-multirepo.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.Random{}

	seedProjectSQLite(t, uow, "project-1")
	seedActiveRepositorySQLite(t, uow, ids, "project-1", "repo-a")
	seedActiveRepositorySQLite(t, uow, ids, "project-1", "repo-b")

	cmd := ports.Command{
		ID: "cmd-root-1", IdempotencyKey: "idem-root-1", Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope("project-1"), RequestedAt: time.Now().UTC(),
		Type: "CreateRootWorkItem", RequestHash: "hash-root-1",
	}
	result, err := work.CreateRootWorkItem(ctx, uow, ids, cmd, baseRequest("project-1", grant("repo-a"), grant("repo-b")))
	if err != nil {
		t.Fatalf("CreateRootWorkItem: %v", err)
	}
	if len(result.ProvisionedRepositories) != 2 {
		t.Fatalf("ProvisionedRepositories = %+v, want exactly 2", result.ProvisionedRepositories)
	}

	assertCount(t, "work_items", 1, func() (int, error) { return store.CountWorkItems(ctx) })
	assertCount(t, "task_families", 1, func() (int, error) { return store.CountTaskFamilies(ctx) })
	assertCount(t, "workspace_sets", 1, func() (int, error) { return store.CountWorkspaceSets(ctx) })
	assertCount(t, "family_repository_scopes", 2, func() (int, error) { return store.CountFamilyRepositoryScopes(ctx) })

	var scopeSet workspace.WorkspaceSet
	err = uow.WithReadOnly(ctx, func(tx ports.Tx) error {
		var err error
		scopeSet, err = tx.Work().GetWorkspaceSetByFamilyID(ctx, result.FamilyID)
		return err
	})
	if err != nil {
		t.Fatalf("GetWorkspaceSetByFamilyID: %v", err)
	}
	if scopeSet.State != workspace.WorkspaceSetRequested {
		t.Fatalf("workspace set state = %q, want REQUESTED (this command only records intent, V3-06 provisions)", scopeSet.State)
	}

	// Each repository's own WORKSPACE_PROVISION job was durably persisted
	// exactly once — the concrete "two independent worktrees" proof
	// AK-ARCH-011 names.
	for _, repositoryID := range []string{"repo-a", "repo-b"} {
		key := fmt.Sprintf("idem-root-1-provision-%s", repositoryID)
		count, err := store.CountDurableJobsByIdempotencyKey(ctx, key)
		if err != nil {
			t.Fatalf("CountDurableJobsByIdempotencyKey(%s): %v", repositoryID, err)
		}
		if count != 1 {
			t.Fatalf("%s provision job count = %d, want exactly 1", repositoryID, count)
		}
	}

	count, err := store.CountDomainEvents(ctx, "WorkItem", result.WorkItemID)
	if err != nil {
		t.Fatalf("CountDomainEvents: %v", err)
	}
	if count != 1 {
		t.Fatalf("WorkItem domain event count = %d, want exactly 1", count)
	}
}

// TestCreateRootWorkItem_DuplicateCommandSQLite_Idempotent is this task's
// own "duplicate command" Verify-line requirement against real sqlite: a
// retry with the same IdempotencyKey/RequestHash replays the first
// attempt's exact result without creating any duplicate row or job.
func TestCreateRootWorkItem_DuplicateCommandSQLite_Idempotent(t *testing.T) {
	ctx := context.Background()
	store := openSQLiteStore(t, "agentkit-work-duplicate.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.Random{}

	seedProjectSQLite(t, uow, "project-1")
	seedActiveRepositorySQLite(t, uow, ids, "project-1", "repo-a")

	cmd := ports.Command{
		ID: "cmd-root-1", IdempotencyKey: "idem-root-1", Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope("project-1"), RequestedAt: time.Now().UTC(),
		Type: "CreateRootWorkItem", RequestHash: "hash-root-1",
	}
	req := baseRequest("project-1", grant("repo-a"))
	first, err := work.CreateRootWorkItem(ctx, uow, ids, cmd, req)
	if err != nil {
		t.Fatalf("first CreateRootWorkItem: %v", err)
	}
	second, err := work.CreateRootWorkItem(ctx, uow, ids, cmd, req)
	if err != nil {
		t.Fatalf("second (replayed) CreateRootWorkItem: %v", err)
	}
	if second.WorkItemID != first.WorkItemID || second.FamilyID != first.FamilyID || second.WorkspaceSetID != first.WorkspaceSetID {
		t.Fatalf("replayed result = %+v, want identical to first %+v", second, first)
	}

	assertCount(t, "work_items", 1, func() (int, error) { return store.CountWorkItems(ctx) })
	assertCount(t, "task_families", 1, func() (int, error) { return store.CountTaskFamilies(ctx) })
	assertCount(t, "workspace_sets", 1, func() (int, error) { return store.CountWorkspaceSets(ctx) })
	assertCount(t, "family_repository_scopes", 1, func() (int, error) { return store.CountFamilyRepositoryScopes(ctx) })

	count, err := store.CountDurableJobsByIdempotencyKey(ctx, "idem-root-1-provision-repo-a")
	if err != nil {
		t.Fatalf("CountDurableJobsByIdempotencyKey: %v", err)
	}
	if count != 1 {
		t.Fatalf("provision job count after replay = %d, want exactly 1 (never redo the mutation)", count)
	}

	eventCount, err := store.CountDomainEvents(ctx, "WorkItem", first.WorkItemID)
	if err != nil {
		t.Fatalf("CountDomainEvents: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("domain event count after replay = %d, want exactly 1", eventCount)
	}
}
