package work_test

// Real-sqlite coverage for queries.go (V6-04): every query in that file is
// exercised against a real *sqlite.Store/ports.UnitOfWork, never a mock or a
// direct row fabrication (this repo's own hard rule — see
// internal/delivery/httpapi/receiptreplay_test.go and this package's own
// commands_sqlite_test.go for the established pattern this file mirrors).
// commands_sqlite_test.go's own openSQLiteStore/seedProjectSQLite/
// seedActiveRepositorySQLite/seedRootFixtureSQLite helpers are reused
// directly (same package, work_test) rather than duplicated.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/work"
	workdomain "github.com/taQuangLing/agent-workflow/internal/domain/work"
)

func TestGetWorkItem_MatchingProjectScope_ReturnsAuthoritativeDetail(t *testing.T) {
	store := openSQLiteStore(t, "get-work-item.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	root := seedRootFixtureSQLite(t, uow, ids, "repo-a", workdomain.RepositoryWrite, nil)

	detail, err := work.GetWorkItem(context.Background(), uow, ports.ProjectScope("project-1"), root.WorkItemID)
	if err != nil {
		t.Fatalf("GetWorkItem: %v", err)
	}
	if detail.WorkItemID != root.WorkItemID || detail.ProjectID != "project-1" || detail.Kind != string(workdomain.WorkItemRoot) {
		t.Fatalf("detail = %+v, want a root detail for %s in project-1", detail, root.WorkItemID)
	}
	if detail.Status != string(workdomain.WorkItemBacklog) || detail.Version != 1 {
		t.Fatalf("detail.Status/Version = %s/%d, want BACKLOG/1", detail.Status, detail.Version)
	}
}

func TestGetWorkItem_AnotherProjectScope_RejectedAsScopeMismatch(t *testing.T) {
	store := openSQLiteStore(t, "get-work-item-cross-project.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	root := seedRootFixtureSQLite(t, uow, ids, "repo-a", workdomain.RepositoryWrite, nil)

	_, err := work.GetWorkItem(context.Background(), uow, ports.ProjectScope("project-2"), root.WorkItemID)
	if !errors.Is(err, ports.ErrScopeMismatch) {
		t.Fatalf("err = %v, want ports.ErrScopeMismatch", err)
	}
}

func TestGetWorkItem_InstallationScope_Rejected(t *testing.T) {
	store := openSQLiteStore(t, "get-work-item-installation.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	root := seedRootFixtureSQLite(t, uow, ids, "repo-a", workdomain.RepositoryWrite, nil)

	_, err := work.GetWorkItem(context.Background(), uow, ports.InstallationScope(), root.WorkItemID)
	if !errors.Is(err, ports.ErrScopeMismatch) {
		t.Fatalf("err = %v, want ports.ErrScopeMismatch", err)
	}
}

func TestGetWorkItem_UnknownID_ReturnsPersistenceNotFound(t *testing.T) {
	store := openSQLiteStore(t, "get-work-item-unknown.db")
	uow := sqlite.NewUnitOfWork(store)
	seedProjectSQLite(t, uow, "project-1")

	_, err := work.GetWorkItem(context.Background(), uow, ports.ProjectScope("project-1"), "no-such-work-item")
	if !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("err = %v, want ports.ErrPersistenceNotFound", err)
	}
}

func TestListWorkItems_ReturnsRootAndChildInCreationOrder(t *testing.T) {
	store := openSQLiteStore(t, "list-work-items.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	root := seedRootFixtureSQLite(t, uow, ids, "repo-a", workdomain.RepositoryWrite, nil)

	ctx := context.Background()
	childCmd := ports.Command{
		ID: "cmd-child-1", IdempotencyKey: "idem-child-1", Actor: "actor-1", CorrelationID: "corr-1",
		Scope: ports.ProjectScope("project-1"), RequestedAt: time.Now().UTC(),
		Type: "CreateChildWorkItem", RequestHash: "hash-child-1",
	}
	child, err := work.CreateChildWorkItem(ctx, uow, ids, childCmd, work.CreateChildWorkItemRequest{
		ParentWorkItemID: root.WorkItemID, Title: "Child task", ParentJoinPolicy: "ALL",
		EffectiveScope: []work.ScopeGrantRequest{{RepositoryID: "repo-a", Access: string(workdomain.RepositoryWrite), Reason: "child scope"}},
	})
	if err != nil {
		t.Fatalf("CreateChildWorkItem: %v", err)
	}

	items, err := work.ListWorkItems(ctx, uow, ports.ProjectScope("project-1"))
	if err != nil {
		t.Fatalf("ListWorkItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2 (root + child)", len(items))
	}
	if items[0].WorkItemID != root.WorkItemID || items[1].WorkItemID != child.WorkItemID {
		t.Fatalf("items = %+v, want [root(%s), child(%s)] in creation order", items, root.WorkItemID, child.WorkItemID)
	}
}

func TestListWorkItems_InstallationScope_Rejected(t *testing.T) {
	store := openSQLiteStore(t, "list-work-items-installation.db")
	uow := sqlite.NewUnitOfWork(store)
	seedProjectSQLite(t, uow, "project-1")

	_, err := work.ListWorkItems(context.Background(), uow, ports.InstallationScope())
	if !errors.Is(err, ports.ErrScopeMismatch) {
		t.Fatalf("err = %v, want ports.ErrScopeMismatch", err)
	}
}

func TestListChildWorkItems_ReturnsOnlyDirectChildren(t *testing.T) {
	store := openSQLiteStore(t, "list-child-work-items.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	root := seedRootFixtureSQLite(t, uow, ids, "repo-a", workdomain.RepositoryWrite, nil)

	ctx := context.Background()
	child, err := work.CreateChildWorkItem(ctx, uow, ids, ports.Command{
		ID: "cmd-child-1", IdempotencyKey: "idem-child-1", Actor: "actor-1", CorrelationID: "corr-1",
		Scope: ports.ProjectScope("project-1"), RequestedAt: time.Now().UTC(),
		Type: "CreateChildWorkItem", RequestHash: "hash-child-1",
	}, work.CreateChildWorkItemRequest{
		ParentWorkItemID: root.WorkItemID, Title: "Child task", ParentJoinPolicy: "ALL",
		EffectiveScope: []work.ScopeGrantRequest{{RepositoryID: "repo-a", Access: string(workdomain.RepositoryWrite), Reason: "child scope"}},
	})
	if err != nil {
		t.Fatalf("CreateChildWorkItem: %v", err)
	}
	// A grandchild under the child must not show up in the ROOT's own
	// children list — direct children only (ListChildWorkItems' own doc
	// comment).
	if _, err := work.CreateChildWorkItem(ctx, uow, ids, ports.Command{
		ID: "cmd-grandchild-1", IdempotencyKey: "idem-grandchild-1", Actor: "actor-1", CorrelationID: "corr-1",
		Scope: ports.ProjectScope("project-1"), RequestedAt: time.Now().UTC(),
		Type: "CreateChildWorkItem", RequestHash: "hash-grandchild-1",
	}, work.CreateChildWorkItemRequest{
		ParentWorkItemID: child.WorkItemID, Title: "Grandchild task", ParentJoinPolicy: "ALL",
		EffectiveScope: []work.ScopeGrantRequest{{RepositoryID: "repo-a", Access: string(workdomain.RepositoryWrite), Reason: "grandchild scope"}},
	}); err != nil {
		t.Fatalf("CreateChildWorkItem (grandchild): %v", err)
	}

	children, err := work.ListChildWorkItems(ctx, uow, ports.ProjectScope("project-1"), root.WorkItemID)
	if err != nil {
		t.Fatalf("ListChildWorkItems: %v", err)
	}
	if len(children) != 1 || children[0].WorkItemID != child.WorkItemID {
		t.Fatalf("children = %+v, want exactly [child(%s)], grandchild must not appear", children, child.WorkItemID)
	}
}

func TestListChildWorkItems_ParentInAnotherProject_RejectedAsScopeMismatch(t *testing.T) {
	store := openSQLiteStore(t, "list-child-work-items-cross-project.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	root := seedRootFixtureSQLite(t, uow, ids, "repo-a", workdomain.RepositoryWrite, nil)

	_, err := work.ListChildWorkItems(context.Background(), uow, ports.ProjectScope("project-2"), root.WorkItemID)
	if !errors.Is(err, ports.ErrScopeMismatch) {
		t.Fatalf("err = %v, want ports.ErrScopeMismatch", err)
	}
}

func TestGetTaskFamily_MatchingProjectScope_ReturnsAuthoritativeDetail(t *testing.T) {
	store := openSQLiteStore(t, "get-task-family.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	root := seedRootFixtureSQLite(t, uow, ids, "repo-a", workdomain.RepositoryWrite, nil)

	detail, err := work.GetTaskFamily(context.Background(), uow, ports.ProjectScope("project-1"), root.FamilyID)
	if err != nil {
		t.Fatalf("GetTaskFamily: %v", err)
	}
	if detail.FamilyID != root.FamilyID || detail.ProjectID != "project-1" || detail.RootWorkItemID != root.WorkItemID {
		t.Fatalf("detail = %+v, want family %s owning root %s in project-1", detail, root.FamilyID, root.WorkItemID)
	}
	if detail.ScopeVersion != 1 || detail.Status != string(workdomain.TaskFamilyActive) {
		t.Fatalf("detail.ScopeVersion/Status = %d/%s, want 1/ACTIVE", detail.ScopeVersion, detail.Status)
	}
}

func TestGetTaskFamily_AnotherProjectScope_RejectedAsScopeMismatch(t *testing.T) {
	store := openSQLiteStore(t, "get-task-family-cross-project.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	root := seedRootFixtureSQLite(t, uow, ids, "repo-a", workdomain.RepositoryWrite, nil)

	_, err := work.GetTaskFamily(context.Background(), uow, ports.ProjectScope("project-2"), root.FamilyID)
	if !errors.Is(err, ports.ErrScopeMismatch) {
		t.Fatalf("err = %v, want ports.ErrScopeMismatch", err)
	}
}

func TestGetScopeExpansionRequest_MatchingProjectScope_ReturnsAuthoritativeDetail(t *testing.T) {
	store := openSQLiteStore(t, "get-scope-expansion-request.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	root := seedRootFixtureSQLite(t, uow, ids, "repo-a", workdomain.RepositoryRead, nil)
	seedActiveRepositorySQLite(t, uow, ids, "project-1", "repo-b")

	ctx := context.Background()
	reqResult, err := work.RequestScopeExpansion(ctx, uow, ids, ports.Command{
		ID: "cmd-expand-1", IdempotencyKey: "idem-expand-1", Actor: "actor-1", CorrelationID: "corr-1",
		Scope: ports.ProjectScope("project-1"), RequestedAt: time.Now().UTC(),
		Type: "RequestScopeExpansion", RequestHash: "hash-expand-1",
	}, work.RequestScopeExpansionRequest{
		FamilyID: root.FamilyID, Reason: "need repo-b too",
		RequestedGrants: []work.ScopeGrantRequest{{RepositoryID: "repo-b", Access: string(workdomain.RepositoryRead), Reason: "expand"}},
	})
	if err != nil {
		t.Fatalf("RequestScopeExpansion: %v", err)
	}

	detail, err := work.GetScopeExpansionRequest(ctx, uow, ports.ProjectScope("project-1"), reqResult.RequestID)
	if err != nil {
		t.Fatalf("GetScopeExpansionRequest: %v", err)
	}
	if detail.RequestID != reqResult.RequestID || detail.FamilyID != root.FamilyID || detail.Status != string(workdomain.ScopeExpansionPending) {
		t.Fatalf("detail = %+v, want PENDING request %s for family %s", detail, reqResult.RequestID, root.FamilyID)
	}
	if len(detail.RequestedGrants) != 1 || detail.RequestedGrants[0].RepositoryID != "repo-b" {
		t.Fatalf("detail.RequestedGrants = %+v, want exactly one grant for repo-b", detail.RequestedGrants)
	}
	if detail.Version != 1 {
		t.Fatalf("detail.Version = %d, want 1 (fresh PENDING request)", detail.Version)
	}
}

func TestGetScopeExpansionRequest_AnotherProjectScope_RejectedAsScopeMismatch(t *testing.T) {
	store := openSQLiteStore(t, "get-scope-expansion-request-cross-project.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	root := seedRootFixtureSQLite(t, uow, ids, "repo-a", workdomain.RepositoryRead, nil)

	ctx := context.Background()
	reqResult, err := work.RequestScopeExpansion(ctx, uow, ids, ports.Command{
		ID: "cmd-expand-1", IdempotencyKey: "idem-expand-1", Actor: "actor-1", CorrelationID: "corr-1",
		Scope: ports.ProjectScope("project-1"), RequestedAt: time.Now().UTC(),
		Type: "RequestScopeExpansion", RequestHash: "hash-expand-1",
	}, work.RequestScopeExpansionRequest{
		FamilyID: root.FamilyID, Reason: "need more",
		RequestedGrants: []work.ScopeGrantRequest{{RepositoryID: "repo-a", Access: string(workdomain.RepositoryWrite), Reason: "expand"}},
	})
	if err != nil {
		t.Fatalf("RequestScopeExpansion: %v", err)
	}

	_, err = work.GetScopeExpansionRequest(ctx, uow, ports.ProjectScope("project-2"), reqResult.RequestID)
	if !errors.Is(err, ports.ErrScopeMismatch) {
		t.Fatalf("err = %v, want ports.ErrScopeMismatch", err)
	}
}

// TestExplainWorkItemReadiness_FreshWorkItem_ReportsRealCompletenessGaps
// proves ExplainWorkItemReadiness calls the REAL workdomain.
// ValidateReadinessGate against a REAL loaded WorkItem — see queries.go's
// own top-of-file doc comment for why a freshly root-created WorkItem
// (CreateRootWorkItem never populates the V3-03 contract fields) always
// reports the same real, honest set of problems today.
func TestExplainWorkItemReadiness_FreshWorkItem_ReportsRealCompletenessGaps(t *testing.T) {
	store := openSQLiteStore(t, "explain-readiness.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	root := seedRootFixtureSQLite(t, uow, ids, "repo-a", workdomain.RepositoryWrite, nil)

	readiness, err := work.ExplainWorkItemReadiness(context.Background(), uow, ports.ProjectScope("project-1"), root.WorkItemID)
	if err != nil {
		t.Fatalf("ExplainWorkItemReadiness: %v", err)
	}
	if readiness.Ready {
		t.Fatal("a freshly root-created WorkItem has no contract yet and must not be Ready")
	}
	if len(readiness.Problems) == 0 {
		t.Fatal("expected at least one concrete readiness problem")
	}
	if readiness.WorkItemID != root.WorkItemID || readiness.Status != string(workdomain.WorkItemBacklog) {
		t.Fatalf("readiness = %+v, want WorkItemID=%s Status=BACKLOG", readiness, root.WorkItemID)
	}
	// Cross-check against the real domain validator directly, so this test
	// fails loudly if ExplainWorkItemReadiness ever stops actually calling
	// it (e.g. a future edit that hardcodes a canned problem list instead).
	direct := workdomain.ValidateReadinessGate(workdomain.WorkItem{
		ID: workdomain.WorkItemID(root.WorkItemID), ProjectID: "project-1", FamilyID: workdomain.TaskFamilyID(root.FamilyID),
		Kind: workdomain.WorkItemRoot, Title: "Root task", Status: workdomain.WorkItemBacklog,
	})
	var directErr *workdomain.ReadinessError
	if !errors.As(direct, &directErr) {
		t.Fatalf("test setup bug: direct ValidateReadinessGate did not return *ReadinessError: %v", direct)
	}
	if len(readiness.Problems) != len(directErr.Problems) {
		t.Fatalf("readiness.Problems = %v, want exactly the same problems the real validator reports: %v", readiness.Problems, directErr.Problems)
	}
}

func TestExplainWorkItemReadiness_AnotherProjectScope_RejectedAsScopeMismatch(t *testing.T) {
	store := openSQLiteStore(t, "explain-readiness-cross-project.db")
	uow := sqlite.NewUnitOfWork(store)
	ids := idsource.NewSequential("id")
	root := seedRootFixtureSQLite(t, uow, ids, "repo-a", workdomain.RepositoryWrite, nil)

	_, err := work.ExplainWorkItemReadiness(context.Background(), uow, ports.ProjectScope("project-2"), root.WorkItemID)
	if !errors.Is(err, ports.ErrScopeMismatch) {
		t.Fatalf("err = %v, want ports.ErrScopeMismatch", err)
	}
}
