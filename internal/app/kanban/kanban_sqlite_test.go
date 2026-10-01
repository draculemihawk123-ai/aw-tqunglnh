package kanban_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/kanban"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/projection"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

type env struct {
	uow ports.UnitOfWork
}

func newEnv(t *testing.T) *env {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "kanban-app.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &env{uow: sqlite.NewUnitOfWork(store)}
}

func (e *env) seedProject(t *testing.T, id string) {
	t.Helper()
	err := e.uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(context.Background(), ports.CreateProjectRequest{ID: id, Name: "project " + id})
		return err
	})
	if err != nil {
		t.Fatalf("seed project %s: %v", id, err)
	}
}

func (e *env) seedActiveRepository(t *testing.T, projectID, repositoryID string) {
	t.Helper()
	ctx := context.Background()
	regCmd := ports.Command{
		ID: "cmd-reg-" + repositoryID, IdempotencyKey: "idem-reg-" + repositoryID, Actor: "seed",
		CorrelationID: "seed", Scope: ports.ProjectScope(projectID), RequestedAt: time.Now().UTC(),
		Type: "RegisterRepository", RequestHash: "hash-reg-" + repositoryID,
	}
	if _, err := catalog.RegisterRepository(ctx, e.uow, idsource.Random{}, regCmd, catalog.RegisterRepositoryRequest{
		RepositoryID: repositoryID, ProjectID: projectID, Name: repositoryID,
		RemoteLocator: "https://example.invalid/" + repositoryID + ".git", DefaultRef: "main",
	}); err != nil {
		t.Fatalf("RegisterRepository(%s): %v", repositoryID, err)
	}
	err := e.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
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

func grant(repositoryID string) workapp.ScopeGrantRequest {
	return workapp.ScopeGrantRequest{RepositoryID: repositoryID, Access: "WRITE", PathScopes: []string{"services"}, Reason: "scope"}
}

func (e *env) createRoot(t *testing.T, projectID, title string, grants ...workapp.ScopeGrantRequest) workapp.CreateRootWorkItemResult {
	t.Helper()
	cmd := ports.Command{
		ID: "cmd-root-" + title, IdempotencyKey: "idem-root-" + title, Actor: "seed",
		CorrelationID: "seed", Scope: ports.ProjectScope(projectID), RequestedAt: time.Now().UTC(),
		Type: "CreateRootWorkItem", RequestHash: "hash-root-" + title,
	}
	result, err := workapp.CreateRootWorkItem(context.Background(), e.uow, idsource.Random{}, cmd, workapp.CreateRootWorkItemRequest{
		ProjectID: projectID, Title: title, InitialScope: grants,
	})
	if err != nil {
		t.Fatalf("CreateRootWorkItem(%s): %v", title, err)
	}
	return result
}

func (e *env) createChild(t *testing.T, projectID, parentWorkItemID, title string, scope ...workapp.ScopeGrantRequest) workapp.CreateChildWorkItemResult {
	t.Helper()
	cmd := ports.Command{
		ID: "cmd-child-" + title, IdempotencyKey: "idem-child-" + title, Actor: "seed",
		CorrelationID: "seed", Scope: ports.ProjectScope(projectID), RequestedAt: time.Now().UTC(),
		Type: "CreateChildWorkItem", RequestHash: "hash-child-" + title,
	}
	result, err := workapp.CreateChildWorkItem(context.Background(), e.uow, idsource.Random{}, cmd, workapp.CreateChildWorkItemRequest{
		ParentWorkItemID: parentWorkItemID, Title: title, ParentJoinPolicy: "ALL_CHILDREN_DONE", EffectiveScope: scope,
	})
	if err != nil {
		t.Fatalf("CreateChildWorkItem(%s): %v", title, err)
	}
	return result
}

func (e *env) seedRepositoryWorkspace(t *testing.T, workspaceSetID, repositoryID string, generation uint64, state workspace.RepositoryWorkspaceState) {
	t.Helper()
	err := e.uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Work().CreateRepositoryWorkspace(context.Background(), workspace.RepositoryWorkspace{
			ID: workspace.RepositoryWorkspaceID(idsource.Random{}.NewID()), WorkspaceSetID: workspace.WorkspaceSetID(workspaceSetID),
			RepositoryID: project.RepositoryID(repositoryID), Generation: generation,
			Locator:      "https://example.invalid/" + repositoryID + ".git",
			BaseRevision: "0000000000000000000000000000000000000000", State: state, Version: 1,
		})
		return err
	})
	if err != nil {
		t.Fatalf("seedRepositoryWorkspace(%s/%s): %v", workspaceSetID, repositoryID, err)
	}
}

func (e *env) seedProjectionRow(t *testing.T, projectID string, generation uint64, card projection.WorkItemCardRow, journalPosition uint64) {
	t.Helper()
	payload, err := card.CanonicalJSON()
	if err != nil {
		t.Fatalf("card.CanonicalJSON(%s): %v", card.WorkItemID, err)
	}
	err = e.uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		return tx.Projections().UpsertProjectionRow(context.Background(), ports.ProjectionRow{
			ProjectID: projectID, ProjectionName: projection.ProjectionName, Generation: generation,
			EntityKey: card.WorkItemID, PayloadJSON: string(payload), LastAppliedJournalPosition: journalPosition,
			UpdatedAt: time.Now().UTC(),
		})
	})
	if err != nil {
		t.Fatalf("seedProjectionRow(%s): %v", card.WorkItemID, err)
	}
}

func (e *env) ensureGeneration(t *testing.T, projectID string, generation uint64) {
	t.Helper()
	err := e.uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		return tx.Projections().EnsureGeneration(context.Background(), projectID, projection.ProjectionName, generation, 1, time.Now().UTC())
	})
	if err != nil {
		t.Fatalf("ensureGeneration(%s/%d): %v", projectID, generation, err)
	}
}

func (e *env) upsertCheckpoint(t *testing.T, projectID string, generation, cursorPos uint64, status ports.ProjectionStatus) {
	t.Helper()
	err := e.uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		var expected *uint64
		existing, getErr := tx.Projections().GetProjectionCheckpoint(context.Background(), projectID, projection.ProjectionName, generation)
		switch {
		case getErr == nil:
			cursorCopy := existing.Cursor
			expected = &cursorCopy
		case errors.Is(getErr, ports.ErrPersistenceNotFound):
			expected = nil
		default:
			return getErr
		}
		return tx.Projections().UpsertProjectionCheckpoint(context.Background(), ports.UpsertProjectionCheckpointRequest{
			ProjectID: projectID, ProjectionName: projection.ProjectionName, Generation: generation,
			ExpectedCursor: expected, NewCursor: cursorPos, NewStatus: status, UpdatedAt: time.Now().UTC(),
		})
	})
	if err != nil {
		t.Fatalf("upsertCheckpoint(%s/%d): %v", projectID, generation, err)
	}
}

// seedBoard seeds count BACKLOG root cards (distinct families, so each is its
// own root) into projectID at generation 1 with a LIVE checkpoint, journal
// position i+1 for card i. It returns the cards' WorkItemIDs in the order the
// board must return them (ascending WorkItemID).
func (e *env) seedBoard(t *testing.T, projectID string, count int) []string {
	t.Helper()
	e.seedProject(t, projectID)
	e.seedActiveRepository(t, projectID, "repo-"+projectID)
	e.ensureGeneration(t, projectID, 1)
	e.upsertCheckpoint(t, projectID, 1, uint64(count), ports.ProjectionLive)
	var ids []string
	for i := 0; i < count; i++ {
		root := e.createRoot(t, projectID, fmt.Sprintf("card-%d", i), grant("repo-"+projectID))
		e.seedProjectionRow(t, projectID, 1, projection.WorkItemCardRow{
			WorkItemID: root.WorkItemID, ProjectID: projectID, FamilyID: root.FamilyID, Title: fmt.Sprintf("card-%d", i),
			IsRoot: true, WorkspaceSetID: root.WorkspaceSetID, Status: "BACKLOG",
		}, uint64(i+1))
		ids = append(ids, root.WorkItemID)
	}
	sortStrings(ids)
	return ids
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func cardIDs(cards []kanban.Card) []string {
	out := make([]string, 0, len(cards))
	for _, c := range cards {
		out = append(out, c.WorkItemID)
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestListWorkItemKanban_NeverProjectedBoardIsEmptyAndStaleNotAnError(t *testing.T) {
	e := newEnv(t)
	e.seedProject(t, "project-1")

	page, err := kanban.ListWorkItemKanban(context.Background(), e.uow, kanban.ListRequest{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("ListWorkItemKanban: %v", err)
	}
	if page.Items == nil || len(page.Items) != 0 {
		t.Fatalf("items = %#v, want a non-nil empty slice (it serializes as [])", page.Items)
	}
	if page.Freshness != (kanban.Freshness{Generation: 0, AsOfJournalPosition: 0, Status: "STALE"}) {
		t.Fatalf("freshness = %+v, want generation 0 STALE (no consumer ever ran)", page.Freshness)
	}
	if page.Next != nil {
		t.Fatalf("Next = %+v, want nil", page.Next)
	}
}

func TestListWorkItemKanban_GenerationWithoutCheckpointIsStaleAtThatGeneration(t *testing.T) {
	e := newEnv(t)
	e.seedProject(t, "project-1")
	e.ensureGeneration(t, "project-1", 3)

	page, err := kanban.ListWorkItemKanban(context.Background(), e.uow, kanban.ListRequest{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("ListWorkItemKanban: %v", err)
	}
	if page.Freshness != (kanban.Freshness{Generation: 3, AsOfJournalPosition: 0, Status: "STALE"}) {
		t.Fatalf("freshness = %+v, want generation 3 STALE (a generation exists but its consumer never checkpointed)", page.Freshness)
	}
}

func TestListWorkItemKanban_ReportsTheCheckpointsOwnFreshness(t *testing.T) {
	e := newEnv(t)
	e.seedBoard(t, "project-1", 1)
	e.upsertCheckpoint(t, "project-1", 1, 42, ports.ProjectionDegraded)

	page, err := kanban.ListWorkItemKanban(context.Background(), e.uow, kanban.ListRequest{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("ListWorkItemKanban: %v", err)
	}
	if page.Freshness != (kanban.Freshness{Generation: 1, AsOfJournalPosition: 42, Status: "DEGRADED"}) {
		t.Fatalf("freshness = %+v, want generation 1 at position 42, DEGRADED", page.Freshness)
	}
}

func TestListWorkItemKanban_BlankProjectIsAnError(t *testing.T) {
	e := newEnv(t)
	if _, err := kanban.ListWorkItemKanban(context.Background(), e.uow, kanban.ListRequest{ProjectID: "  "}); err == nil {
		t.Fatal("a blank ProjectID must be an error, not an empty board")
	}
}

func TestListWorkItemKanban_UnboundedReturnsEveryCardAscendingByWorkItemID(t *testing.T) {
	e := newEnv(t)
	want := e.seedBoard(t, "project-1", 5)

	page, err := kanban.ListWorkItemKanban(context.Background(), e.uow, kanban.ListRequest{ProjectID: "project-1"})
	if err != nil {
		t.Fatalf("ListWorkItemKanban: %v", err)
	}
	if got := cardIDs(page.Items); !equalStrings(got, want) {
		t.Fatalf("cards = %v, want %v", got, want)
	}
	if page.Next != nil {
		t.Fatalf("an unbounded read left Next = %+v", page.Next)
	}
	for _, c := range page.Items {
		if c.ProjectID != "project-1" || !c.IsRoot || c.Status != "BACKLOG" {
			t.Errorf("card = %+v, want a BACKLOG root of project-1", c)
		}
	}
}

func TestListWorkItemKanban_PagesPartitionTheBoardWithNoGapOrRepeat(t *testing.T) {
	e := newEnv(t)
	want := e.seedBoard(t, "project-1", 5)

	var got []string
	var resume *kanban.ResumePoint
	pages := 0
	for {
		page, err := kanban.ListWorkItemKanban(context.Background(), e.uow, kanban.ListRequest{ProjectID: "project-1", Limit: 2, Resume: resume})
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		pages++
		if len(page.Items) > 2 {
			t.Fatalf("page %d holds %d cards, want at most 2", pages, len(page.Items))
		}
		got = append(got, cardIDs(page.Items)...)
		if page.Next == nil {
			break
		}
		if page.Next.LastKey != page.Items[len(page.Items)-1].WorkItemID || page.Next.Generation != 1 {
			t.Fatalf("Next = %+v, want it to resume after %s at generation 1", page.Next, page.Items[len(page.Items)-1].WorkItemID)
		}
		resume = page.Next
		if pages > 10 {
			t.Fatal("pagination did not terminate")
		}
	}
	if pages != 3 || !equalStrings(got, want) {
		t.Fatalf("walk = %v in %d pages, want %v in 3", got, pages, want)
	}
}

func TestListWorkItemKanban_ACardChangedAfterTheWalkBeganIsExcludedFromIt(t *testing.T) {
	e := newEnv(t)
	ids := e.seedBoard(t, "project-1", 4)

	first, err := kanban.ListWorkItemKanban(context.Background(), e.uow, kanban.ListRequest{ProjectID: "project-1", Limit: 2})
	if err != nil || first.Next == nil {
		t.Fatalf("first page: %+v, %v", first, err)
	}

	// A card on a page not yet reached is re-projected (its journal position
	// moves past the watermark the walk pinned): it must not appear in THIS
	// walk's remaining pages, nor shift any other card.
	late := ids[3]
	e.seedProjectionRow(t, "project-1", 1, projection.WorkItemCardRow{
		WorkItemID: late, ProjectID: "project-1", Title: "changed", IsRoot: true, Status: "READY",
	}, 999)

	second, err := kanban.ListWorkItemKanban(context.Background(), e.uow, kanban.ListRequest{ProjectID: "project-1", Limit: 10, Resume: first.Next})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if got, want := cardIDs(second.Items), ids[2:3]; !equalStrings(got, want) {
		t.Fatalf("remaining cards = %v, want %v (the re-projected %s belongs to a later walk)", got, want, late)
	}

	// A fresh walk sees it, with its new status.
	fresh, err := kanban.ListWorkItemKanban(context.Background(), e.uow, kanban.ListRequest{ProjectID: "project-1"})
	if err != nil || len(fresh.Items) != 4 {
		t.Fatalf("fresh walk = %d items, err %v, want all 4", len(fresh.Items), err)
	}
}

func TestListWorkItemKanban_ResumeAcrossAGenerationSwapIsTypedNotSilent(t *testing.T) {
	e := newEnv(t)
	e.seedBoard(t, "project-1", 3)
	first, err := kanban.ListWorkItemKanban(context.Background(), e.uow, kanban.ListRequest{ProjectID: "project-1", Limit: 1})
	if err != nil || first.Next == nil {
		t.Fatalf("first page: %+v, %v", first, err)
	}

	// A walk resumed with a cursor from another generation is refused.
	stale := *first.Next
	stale.Generation = 7
	_, err = kanban.ListWorkItemKanban(context.Background(), e.uow, kanban.ListRequest{ProjectID: "project-1", Limit: 1, Resume: &stale})
	if !errors.Is(err, kanban.ErrGenerationChanged) {
		t.Fatalf("err = %v, want kanban.ErrGenerationChanged", err)
	}

	// The real swap: the active generation moves on under a live walk.
	oldGeneration := uint64(1)
	err = e.uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		return tx.Projections().CutoverProjectionGeneration(context.Background(), ports.CutoverProjectionGenerationRequest{
			ProjectID: "project-1", ProjectionName: projection.ProjectionName, ExpectedGeneration: &oldGeneration,
			NewGeneration: 2, SchemaVersion: 1, UpdatedAt: time.Now().UTC(),
		})
	})
	if err != nil {
		t.Fatalf("cut over to generation 2: %v", err)
	}
	_, err = kanban.ListWorkItemKanban(context.Background(), e.uow, kanban.ListRequest{ProjectID: "project-1", Limit: 1, Resume: first.Next})
	if !errors.Is(err, kanban.ErrGenerationChanged) {
		t.Fatalf("after the swap err = %v, want kanban.ErrGenerationChanged", err)
	}
}

func TestListWorkItemKanban_FiltersByStatusAndFamily(t *testing.T) {
	e := newEnv(t)
	e.seedProject(t, "project-1")
	e.seedActiveRepository(t, "project-1", "repo-a")
	e.ensureGeneration(t, "project-1", 1)
	e.upsertCheckpoint(t, "project-1", 1, 9, ports.ProjectionLive)

	statuses := map[string]string{}
	families := map[string]string{}
	for i, status := range []string{"BACKLOG", "READY", "RUNNING", "READY"} {
		root := e.createRoot(t, "project-1", fmt.Sprintf("card-%d", i), grant("repo-a"))
		statuses[root.WorkItemID] = status
		families[root.WorkItemID] = root.FamilyID
		e.seedProjectionRow(t, "project-1", 1, projection.WorkItemCardRow{
			WorkItemID: root.WorkItemID, ProjectID: "project-1", FamilyID: root.FamilyID, Title: fmt.Sprintf("card-%d", i),
			IsRoot: true, Status: status,
		}, uint64(i+1))
	}

	list := func(filter kanban.Filter) []kanban.Card {
		t.Helper()
		page, err := kanban.ListWorkItemKanban(context.Background(), e.uow, kanban.ListRequest{ProjectID: "project-1", Filter: filter})
		if err != nil {
			t.Fatalf("ListWorkItemKanban(%+v): %v", filter, err)
		}
		return page.Items
	}

	for _, c := range list(kanban.NormalizeFilter([]string{"ready"}, "")) {
		if c.Status != "READY" {
			t.Errorf("status filter READY returned a %s card", c.Status)
		}
	}
	if got := len(list(kanban.NormalizeFilter([]string{" ready ", "READY"}, ""))); got != 2 {
		t.Errorf("READY (any case, duplicated) = %d cards, want 2", got)
	}
	if got := len(list(kanban.NormalizeFilter([]string{"BACKLOG", "running"}, ""))); got != 2 {
		t.Errorf("BACKLOG|RUNNING = %d cards, want 2", got)
	}
	if got := len(list(kanban.NormalizeFilter([]string{"DONE"}, ""))); got != 0 {
		t.Errorf("a status no card has = %d cards, want 0", got)
	}
	for id, family := range families {
		cards := list(kanban.NormalizeFilter(nil, " "+family+" "))
		if len(cards) != 1 || cards[0].WorkItemID != id {
			t.Errorf("family %s filter = %v, want exactly %s", family, cardIDs(cards), id)
		}
	}
	if got := len(list(kanban.NormalizeFilter([]string{"READY"}, families[firstKeyWithStatus(statuses, "BACKLOG")]))); got != 0 {
		t.Errorf("READY within a BACKLOG-only family = %d cards, want 0 (the filters AND together)", got)
	}
}

func firstKeyWithStatus(m map[string]string, status string) string {
	for k, v := range m {
		if v == status {
			return k
		}
	}
	return ""
}

func TestListWorkItemKanban_MultiRepoBadgesComeFromTheRootsWorkspaceSetForChildrenToo(t *testing.T) {
	e := newEnv(t)
	e.seedProject(t, "project-1")
	e.seedActiveRepository(t, "project-1", "repo-a")
	e.seedActiveRepository(t, "project-1", "repo-b")
	root := e.createRoot(t, "project-1", "Root task", grant("repo-a"), grant("repo-b"))
	child := e.createChild(t, "project-1", root.WorkItemID, "Child task", grant("repo-a"))

	// repo-a was provisioned twice (generation 1 quarantined, generation 2
	// ready): the badge is the CURRENT state only.
	e.seedRepositoryWorkspace(t, root.WorkspaceSetID, "repo-a", 1, workspace.RepositoryWorkspaceQuarantined)
	e.seedRepositoryWorkspace(t, root.WorkspaceSetID, "repo-a", 2, workspace.RepositoryWorkspaceReady)
	e.seedRepositoryWorkspace(t, root.WorkspaceSetID, "repo-b", 1, workspace.RepositoryWorkspaceProvisioning)

	e.ensureGeneration(t, "project-1", 1)
	e.upsertCheckpoint(t, "project-1", 1, 10, ports.ProjectionLive)
	e.seedProjectionRow(t, "project-1", 1, projection.WorkItemCardRow{
		WorkItemID: root.WorkItemID, ProjectID: "project-1", FamilyID: root.FamilyID, Title: "Root task",
		IsRoot: true, WorkspaceSetID: root.WorkspaceSetID, Status: "BACKLOG",
	}, 5)
	e.seedProjectionRow(t, "project-1", 1, projection.WorkItemCardRow{
		WorkItemID: child.WorkItemID, ProjectID: "project-1", FamilyID: root.FamilyID, Title: "Child task",
		ParentWorkItemID: root.WorkItemID, IsRoot: false, Status: "BACKLOG",
	}, 6)

	page, err := kanban.ListWorkItemKanban(context.Background(), e.uow, kanban.ListRequest{ProjectID: "project-1"})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("board = %+v, err %v, want 2 cards", page.Items, err)
	}
	for _, c := range page.Items {
		if c.WorkspaceSetID != root.WorkspaceSetID {
			t.Errorf("card %s WorkspaceSetID = %q, want the root's %q (a child row never carries it itself)", c.Title, c.WorkspaceSetID, root.WorkspaceSetID)
		}
		states := map[string]string{}
		for _, b := range c.RepositoryBadges {
			states[b.RepositoryID] = b.State
		}
		if len(states) != 2 || states["repo-a"] != string(workspace.RepositoryWorkspaceReady) || states["repo-b"] != string(workspace.RepositoryWorkspaceProvisioning) {
			t.Errorf("card %s badges = %v, want repo-a READY (the latest generation) and repo-b PROVISIONING", c.Title, states)
		}
	}
}

func TestFilter_WireShapeIsFrozenBecauseCursorsFingerprintIt(t *testing.T) {
	raw, err := json.Marshal(kanban.NormalizeFilter(nil, ""))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"statuses":[],"familyId":""}` {
		t.Fatalf("filter JSON = %s; changing it invalidates every pagination cursor an older process minted", raw)
	}
	raw, _ = json.Marshal(kanban.NormalizeFilter([]string{"b", "A", " a ", ""}, " fam "))
	if string(raw) != `{"statuses":["A","B"],"familyId":"fam"}` {
		t.Fatalf("normalized filter JSON = %s", raw)
	}
}

func TestGetWorkItemProjectedDetail_ProjectedCardNextToFreshReadiness(t *testing.T) {
	e := newEnv(t)
	e.seedProject(t, "project-1")
	e.seedActiveRepository(t, "project-1", "repo-a")
	root := e.createRoot(t, "project-1", "Root task", grant("repo-a"))
	e.ensureGeneration(t, "project-1", 1)
	e.upsertCheckpoint(t, "project-1", 1, 7, ports.ProjectionLive)
	// The projection CLAIMS the card is RUNNING with two blockers; the real row
	// is a BACKLOG root. The card is shown as projected; readiness is real.
	e.seedProjectionRow(t, "project-1", 1, projection.WorkItemCardRow{
		WorkItemID: root.WorkItemID, ProjectID: "project-1", FamilyID: root.FamilyID, Title: "Root task",
		IsRoot: true, WorkspaceSetID: root.WorkspaceSetID, Status: "RUNNING", BlockerCount: 2, TopBlockerType: "APPROVAL",
	}, 4)

	detail, err := kanban.GetWorkItemProjectedDetail(context.Background(), e.uow, kanban.DetailRequest{WorkItemID: root.WorkItemID})
	if err != nil {
		t.Fatalf("GetWorkItemProjectedDetail: %v", err)
	}
	if detail.Card.Status != "RUNNING" || detail.Card.BlockerCount != 2 || detail.Card.TopBlockerType != "APPROVAL" {
		t.Fatalf("card = %+v, want the projection's own (possibly stale) values verbatim", detail.Card)
	}
	if detail.Readiness.Status != "BACKLOG" || detail.Readiness.WorkItemID != root.WorkItemID {
		t.Fatalf("readiness = %+v, want the REAL BACKLOG status — never derived from the stale card", detail.Readiness)
	}
	if detail.Freshness != (kanban.Freshness{Generation: 1, AsOfJournalPosition: 7, Status: "LIVE"}) {
		t.Fatalf("freshness = %+v", detail.Freshness)
	}
}

func TestGetWorkItemProjectedDetail_NoProjectionRowYetFallsBackToTheRealRow(t *testing.T) {
	e := newEnv(t)
	e.seedProject(t, "project-1")
	e.seedActiveRepository(t, "project-1", "repo-a")
	root := e.createRoot(t, "project-1", "Root task", grant("repo-a"))
	child := e.createChild(t, "project-1", root.WorkItemID, "Child task", grant("repo-a"))

	detail, err := kanban.GetWorkItemProjectedDetail(context.Background(), e.uow, kanban.DetailRequest{WorkItemID: child.WorkItemID})
	if err != nil {
		t.Fatalf("GetWorkItemProjectedDetail: %v", err)
	}
	card := detail.Card
	if card.WorkItemID != child.WorkItemID || card.ProjectID != "project-1" || card.FamilyID != root.FamilyID ||
		card.Title != "Child task" || card.IsRoot || card.ParentWorkItemID != root.WorkItemID || card.Status != "BACKLOG" {
		t.Fatalf("fallback card = %+v, want the real child row's identity", card)
	}
	if card.BlockerCount != 0 || card.ActiveRunID != "" || card.WorkspaceSetID != "" || card.RepositoryBadges != nil {
		t.Fatalf("fallback card = %+v: fields the real row cannot know must stay at their honest zero value", card)
	}
	if detail.Freshness.Status != "STALE" {
		t.Fatalf("freshness = %+v, want STALE (no consumer ran)", detail.Freshness)
	}
}

func TestGetWorkItemProjectedDetail_ScopeAndLookupErrors(t *testing.T) {
	e := newEnv(t)
	e.seedProject(t, "project-1")
	e.seedActiveRepository(t, "project-1", "repo-a")
	root := e.createRoot(t, "project-1", "Root task", grant("repo-a"))

	if _, err := kanban.GetWorkItemProjectedDetail(context.Background(), e.uow, kanban.DetailRequest{WorkItemID: root.WorkItemID, ProjectID: "project-1"}); err != nil {
		t.Fatalf("matching ProjectID: %v", err)
	}
	if _, err := kanban.GetWorkItemProjectedDetail(context.Background(), e.uow, kanban.DetailRequest{WorkItemID: root.WorkItemID, ProjectID: "another-project"}); !errors.Is(err, ports.ErrScopeMismatch) {
		t.Fatalf("another project's id: err = %v, want ports.ErrScopeMismatch", err)
	}
	if _, err := kanban.GetWorkItemProjectedDetail(context.Background(), e.uow, kanban.DetailRequest{WorkItemID: "no-such-item"}); !errors.Is(err, ports.ErrPersistenceNotFound) {
		t.Fatalf("unknown item: err = %v, want ports.ErrPersistenceNotFound", err)
	}
	if _, err := kanban.GetWorkItemProjectedDetail(context.Background(), e.uow, kanban.DetailRequest{WorkItemID: " "}); err == nil {
		t.Fatal("a blank WorkItemID must be an error")
	}
}
