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
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
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

// --- CreateChildWorkItem (V3-05) ---

// seedRootFixtureSQLite mirrors commands_test.go's own createRootFixture,
// against a real sqlite store: seeds project-1/repositoryID ACTIVE and
// creates a root WorkItem granting access on paths.
func seedRootFixtureSQLite(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, repositoryID string, access workdomain.RepositoryAccess, paths []string) work.CreateRootWorkItemResult {
	t.Helper()
	ctx := context.Background()
	seedProjectSQLite(t, uow, "project-1")
	seedActiveRepositorySQLite(t, uow, ids, "project-1", repositoryID)

	cmd := ports.Command{
		ID: "cmd-root-" + repositoryID, IdempotencyKey: "idem-root-" + repositoryID, Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope("project-1"), RequestedAt: time.Now().UTC(),
		Type: "CreateRootWorkItem", RequestHash: "hash-root-" + repositoryID,
	}
	result, err := work.CreateRootWorkItem(ctx, uow, ids, cmd, work.CreateRootWorkItemRequest{
		ProjectID: "project-1", Title: "Root task", InitialScope: []work.ScopeGrantRequest{
			{RepositoryID: repositoryID, Access: string(access), PathScopes: paths, Reason: "root scope"},
		},
	})
	if err != nil {
		t.Fatalf("CreateRootWorkItem: %v", err)
	}
	return result
}

// TestCreateChildWorkItem_NoNewWorkspaceSetOrProvisionJobSQLite is this
// task's own explicit "Hoàn thành khi: tạo child không enqueue provision
// workspace mới" bar, proven against real sqlite by direct table counts —
// not merely "the function didn't call X" — mirroring
// TestCreateRootWorkItem_MultiRepoFixtureSQLite's own rigor. A successful
// CreateChildWorkItem must leave workspace_sets and the WORKSPACE_PROVISION
// durable_jobs count exactly where the root's own creation left them.
func TestCreateChildWorkItem_NoNewWorkspaceSetOrProvisionJobSQLite(t *testing.T) {
	ctx := context.Background()
	store := openSQLiteStore(t, "agentkit-child-no-provision.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.Random{}

	root := seedRootFixtureSQLite(t, uow, ids, "repo-1", workdomain.RepositoryWrite, []string{"services/api"})

	assertCount(t, "work_items (before child)", 1, func() (int, error) { return store.CountWorkItems(ctx) })
	assertCount(t, "workspace_sets (before child)", 1, func() (int, error) { return store.CountWorkspaceSets(ctx) })
	assertCount(t, "task_families (before child)", 1, func() (int, error) { return store.CountTaskFamilies(ctx) })
	provisionJobsBefore, err := store.CountDurableJobsByKind(ctx, work.WorkspaceProvisionJobKind)
	if err != nil {
		t.Fatalf("CountDurableJobsByKind (before child): %v", err)
	}
	if provisionJobsBefore != 1 {
		t.Fatalf("provision jobs before child = %d, want 1 (from the root's own creation)", provisionJobsBefore)
	}

	cmd := ports.Command{
		ID: "cmd-child-1", IdempotencyKey: "idem-child-1", Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope("project-1"), RequestedAt: time.Now().UTC(),
		Type: "CreateChildWorkItem", RequestHash: "hash-child-1",
	}
	result, err := work.CreateChildWorkItem(ctx, uow, ids, cmd, work.CreateChildWorkItemRequest{
		ParentWorkItemID: root.WorkItemID, Title: "Implement the handler", ParentJoinPolicy: "PARENT_BLOCKS_ON_CHILD",
		EffectiveScope: []work.ScopeGrantRequest{
			{RepositoryID: "repo-1", Access: string(workdomain.RepositoryWrite), PathScopes: []string{"services/api/handler"}, Reason: "child scope"},
		},
	})
	if err != nil {
		t.Fatalf("CreateChildWorkItem: %v", err)
	}

	assertCount(t, "work_items (after child)", 2, func() (int, error) { return store.CountWorkItems(ctx) })
	// The load-bearing assertions: creating a child must NEVER create a
	// second TaskFamily/WorkspaceSet (AK-ARCH-012's own "Child cùng family
	// reuse WorkspaceSet") and must NEVER enqueue a new WORKSPACE_PROVISION
	// job (this task's own "Hoàn thành khi" bar) — both counts must stay
	// exactly where the root's own creation left them.
	assertCount(t, "task_families (after child)", 1, func() (int, error) { return store.CountTaskFamilies(ctx) })
	assertCount(t, "workspace_sets (after child)", 1, func() (int, error) { return store.CountWorkspaceSets(ctx) })
	assertCount(t, "work_item_effective_scopes (after child)", 1, func() (int, error) { return store.CountWorkItemEffectiveScopes(ctx) })

	provisionJobsAfter, err := store.CountDurableJobsByKind(ctx, work.WorkspaceProvisionJobKind)
	if err != nil {
		t.Fatalf("CountDurableJobsByKind (after child): %v", err)
	}
	if provisionJobsAfter != provisionJobsBefore {
		t.Fatalf("provision jobs after child = %d, want unchanged from %d (no new WORKSPACE_PROVISION job)", provisionJobsAfter, provisionJobsBefore)
	}

	eventCount, err := store.CountDomainEvents(ctx, "WorkItem", result.WorkItemID)
	if err != nil {
		t.Fatalf("CountDomainEvents: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("ChildWorkItemCreated event count = %d, want exactly 1", eventCount)
	}
}

// TestCreateChildWorkItem_RollbackOnEscalation_NoOrphanRowsSQLite mirrors
// TestCreateRootWorkItem_RollbackOnMidTransactionFailure_NoOrphanRows for
// CreateChildWorkItem: a REAL failure (READ→WRITE escalation, rejected by
// work.ValidateEffectiveScopes deep inside the transaction, after the child
// WorkItem row has already been written to the live *sql.Tx) must roll back
// EVERYTHING — zero new work_items/work_item_effective_scopes rows, no
// ChildWorkItemCreated event, no command receipt — proven against a real
// sqlite database, not the fake.
func TestCreateChildWorkItem_RollbackOnEscalation_NoOrphanRowsSQLite(t *testing.T) {
	ctx := context.Background()
	store := openSQLiteStore(t, "agentkit-child-rollback.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.Random{}

	root := seedRootFixtureSQLite(t, uow, ids, "repo-1", workdomain.RepositoryRead, []string{"services/api"})

	assertCount(t, "work_items (before child attempt)", 1, func() (int, error) { return store.CountWorkItems(ctx) })

	cmd := ports.Command{
		ID: "cmd-child-1", IdempotencyKey: "idem-child-1", Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope("project-1"), RequestedAt: time.Now().UTC(),
		Type: "CreateChildWorkItem", RequestHash: "hash-child-1",
	}
	_, err := work.CreateChildWorkItem(ctx, uow, ids, cmd, work.CreateChildWorkItemRequest{
		ParentWorkItemID: root.WorkItemID, Title: "Escalate to WRITE", ParentJoinPolicy: "PARENT_BLOCKS_ON_CHILD",
		EffectiveScope: []work.ScopeGrantRequest{
			{RepositoryID: "repo-1", Access: string(workdomain.RepositoryWrite), PathScopes: []string{"services/api"}, Reason: "escalate"},
		},
	})
	if !errors.Is(err, work.ErrEffectiveScopeExceedsFamilyScope) {
		t.Fatalf("CreateChildWorkItem err = %v, want work.ErrEffectiveScopeExceedsFamilyScope (family only granted READ)", err)
	}

	// The child WorkItem row itself was written to the live *sql.Tx before
	// ValidateEffectiveScopes ever ran (CreateWorkItem happens first) —
	// proving the whole transaction, not just the scope write, rolled back.
	assertCount(t, "work_items (after failed child attempt)", 1, func() (int, error) { return store.CountWorkItems(ctx) })
	assertCount(t, "work_item_effective_scopes (after failed child attempt)", 0, func() (int, error) { return store.CountWorkItemEffectiveScopes(ctx) })

	eventCount, err := store.CountDomainEventsByType(ctx, "ChildWorkItemCreated")
	if err != nil {
		t.Fatalf("CountDomainEventsByType: %v", err)
	}
	if eventCount != 0 {
		t.Fatalf("ChildWorkItemCreated event count = %d, want 0", eventCount)
	}

	receiptCount, err := store.CountCommandReceiptsByIdempotencyKey(ctx, "idem-child-1")
	if err != nil {
		t.Fatalf("CountCommandReceiptsByIdempotencyKey: %v", err)
	}
	if receiptCount != 0 {
		t.Fatalf("command receipt count = %d, want 0 (a failed command must remain retryable)", receiptCount)
	}
}

// TestCreateChildWorkItem_DuplicateCommandSQLite_Idempotent is this task's
// own "duplicate command" requirement against real sqlite: a retry with the
// same IdempotencyKey/RequestHash replays the first attempt's exact result
// without creating any duplicate row.
func TestCreateChildWorkItem_DuplicateCommandSQLite_Idempotent(t *testing.T) {
	ctx := context.Background()
	store := openSQLiteStore(t, "agentkit-child-duplicate.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.Random{}

	root := seedRootFixtureSQLite(t, uow, ids, "repo-1", workdomain.RepositoryWrite, []string{"services/api"})

	cmd := ports.Command{
		ID: "cmd-child-1", IdempotencyKey: "idem-child-1", Actor: "actor-1",
		CorrelationID: "corr-1", Scope: ports.ProjectScope("project-1"), RequestedAt: time.Now().UTC(),
		Type: "CreateChildWorkItem", RequestHash: "hash-child-1",
	}
	req := work.CreateChildWorkItemRequest{
		ParentWorkItemID: root.WorkItemID, Title: "Implement the handler", ParentJoinPolicy: "PARENT_BLOCKS_ON_CHILD",
		EffectiveScope: []work.ScopeGrantRequest{
			{RepositoryID: "repo-1", Access: string(workdomain.RepositoryWrite), PathScopes: []string{"services/api/handler"}, Reason: "child scope"},
		},
	}
	first, err := work.CreateChildWorkItem(ctx, uow, ids, cmd, req)
	if err != nil {
		t.Fatalf("first CreateChildWorkItem: %v", err)
	}
	second, err := work.CreateChildWorkItem(ctx, uow, ids, cmd, req)
	if err != nil {
		t.Fatalf("second (replayed) CreateChildWorkItem: %v", err)
	}
	if second.WorkItemID != first.WorkItemID {
		t.Fatalf("replayed result = %+v, want identical to first %+v", second, first)
	}

	assertCount(t, "work_items", 2, func() (int, error) { return store.CountWorkItems(ctx) })
	assertCount(t, "work_item_effective_scopes", 1, func() (int, error) { return store.CountWorkItemEffectiveScopes(ctx) })

	eventCount, err := store.CountDomainEvents(ctx, "WorkItem", first.WorkItemID)
	if err != nil {
		t.Fatalf("CountDomainEvents: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("domain event count after replay = %d, want exactly 1", eventCount)
	}
}
