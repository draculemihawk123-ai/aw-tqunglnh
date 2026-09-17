package kanban_test

import (
	"net/http"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/projection"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/kanban"
	"github.com/taQuangLing/agent-workflow/internal/domain/workspace"
)

func grant(repositoryID string) workapp.ScopeGrantRequest {
	return workapp.ScopeGrantRequest{RepositoryID: repositoryID, Access: "WRITE", PathScopes: []string{"services"}, Reason: "scope"}
}

// TestRegisterRoutes_ExposesExactlyTheDocumentedOperationSet proves this
// package's own closed route inventory (routes.go's own doc comment): no
// third route, and both routes are PROJECT-scoped (ADR-025: WorkItem is
// never installation-scoped).
func TestRegisterRoutes_ExposesExactlyTheDocumentedOperationSet(t *testing.T) {
	reg := httpapi.NewRouteRegistry()
	kanban.RegisterRoutes(reg, kanban.Dependencies{UnitOfWork: nil, Cursor: httpapi.NewCursorCodec([]byte("test-secret"))})

	want := map[string]bool{"listWorkItemKanban": true, "getWorkItemProjectedDetail": true}
	got := reg.Descriptors()
	if len(got) != len(want) {
		t.Fatalf("len(Descriptors()) = %d, want %d", len(got), len(want))
	}
	for _, d := range got {
		if !want[d.OperationID] {
			t.Errorf("unexpected operationId %q registered", d.OperationID)
		}
		if d.ScopeKind != httpapi.ScopeProject {
			t.Errorf("operationId %q has ScopeKind %q, want PROJECT", d.OperationID, d.ScopeKind)
		}
		delete(want, d.OperationID)
	}
	if len(want) != 0 {
		t.Errorf("missing operationIds: %v", want)
	}
}

// TestListKanban_MultiRepoCardAggregatesBadgesFromRootRow is this task's
// own explicit "multi-repo card" Verify bullet: a WorkItem family spanning
// two repositories must show both as RepositoryBadges on the ROOT card, and
// a CHILD card (whose own projected row never carries WorkspaceSetID at
// all, row.go's own documented scope boundary) must cross-reference the
// root row to resolve the SAME WorkspaceSetID, and therefore the SAME
// badges.
func TestListKanban_MultiRepoCardAggregatesBadgesFromRootRow(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	env.seedActiveRepository(t, "project-1", "repo-b")

	root := env.createRoot(t, "project-1", "Root task", grant("repo-a"), grant("repo-b"))
	child := env.createChild(t, "project-1", root.WorkItemID, "Child task", grant("repo-a"))

	env.seedRepositoryWorkspace(t, root.WorkspaceSetID, "repo-a", 1, workspace.RepositoryWorkspaceReady)
	env.seedRepositoryWorkspace(t, root.WorkspaceSetID, "repo-b", 1, workspace.RepositoryWorkspaceProvisioning)

	env.ensureGeneration(t, "project-1", 1)
	env.upsertCheckpoint(t, "project-1", 1, 10, ports.ProjectionLive)

	env.seedProjectionRow(t, "project-1", 1, projection.WorkItemCardRow{
		WorkItemID: root.WorkItemID, ProjectID: "project-1", FamilyID: root.FamilyID, Title: "Root task",
		IsRoot: true, WorkspaceSetID: root.WorkspaceSetID, Status: "BACKLOG",
	}, 5)
	env.seedProjectionRow(t, "project-1", 1, projection.WorkItemCardRow{
		WorkItemID: child.WorkItemID, ProjectID: "project-1", FamilyID: root.FamilyID, Title: "Child task",
		ParentWorkItemID: root.WorkItemID, IsRoot: false, Status: "BACKLOG",
	}, 6)

	var response struct {
		Items     []kanban.KanbanCardDTO `json:"items"`
		Freshness httpapi.Freshness      `json:"freshness"`
	}
	decodeInto(t, env.get(t, "/projects/project-1/work-items/kanban"), &response)

	if response.Freshness.Status != httpapi.FreshnessLive {
		t.Fatalf("Freshness.Status = %q, want LIVE", response.Freshness.Status)
	}
	if len(response.Items) != 2 {
		t.Fatalf("len(Items) = %d, want 2", len(response.Items))
	}

	cards := map[string]kanban.KanbanCardDTO{}
	for _, item := range response.Items {
		cards[item.WorkItemID] = item
	}

	rootCard, ok := cards[root.WorkItemID]
	if !ok {
		t.Fatalf("root card %s missing from response", root.WorkItemID)
	}
	if rootCard.WorkspaceSetID != root.WorkspaceSetID {
		t.Errorf("root card WorkspaceSetID = %q, want %q", rootCard.WorkspaceSetID, root.WorkspaceSetID)
	}
	assertBadges(t, "root", rootCard.RepositoryBadges)

	childCard, ok := cards[child.WorkItemID]
	if !ok {
		t.Fatalf("child card %s missing from response", child.WorkItemID)
	}
	if childCard.WorkspaceSetID != root.WorkspaceSetID {
		t.Errorf("child card WorkspaceSetID = %q, want cross-referenced root value %q", childCard.WorkspaceSetID, root.WorkspaceSetID)
	}
	assertBadges(t, "child (cross-referenced)", childCard.RepositoryBadges)
}

func assertBadges(t *testing.T, label string, badges []kanban.RepositoryBadgeDTO) {
	t.Helper()
	if len(badges) != 2 {
		t.Fatalf("%s RepositoryBadges = %+v, want exactly 2", label, badges)
	}
	byRepo := map[string]string{}
	for _, b := range badges {
		byRepo[b.RepositoryID] = b.State
	}
	if byRepo["repo-a"] != string(workspace.RepositoryWorkspaceReady) {
		t.Errorf("%s repo-a state = %q, want READY", label, byRepo["repo-a"])
	}
	if byRepo["repo-b"] != string(workspace.RepositoryWorkspaceProvisioning) {
		t.Errorf("%s repo-b state = %q, want PROVISIONING", label, byRepo["repo-b"])
	}
}

// seedFlatCard seeds a bare-bones projection row identified only by
// workItemID/status/familyID — used by the filter/paging/resync/freshness
// tests below, none of which care about badges/authoritative WorkItem
// existence (list.go never reloads the authoritative WorkItem at all, only
// detail.go does) — a real WorkItem is deliberately NOT created for these,
// exactly the "projection rows are decoupled key-value storage" property
// ports.ProjectionRepository's own doc comment establishes.
func (e *testEnv) seedFlatCard(t *testing.T, projectID, workItemID, familyID, status string, generation, journalPosition uint64) {
	t.Helper()
	e.seedProjectionRow(t, projectID, generation, projection.WorkItemCardRow{
		WorkItemID: workItemID, ProjectID: projectID, FamilyID: familyID, Title: "card " + workItemID,
		IsRoot: true, Status: status,
	}, journalPosition)
}

// TestListKanban_FiltersAndPagingStable is this task's own "filters/paging"
// Verify bullet: a fixed filter+sort produces a stable, non-duplicating,
// non-omitting walk across pages even when a NEW row is written between
// page 1 and page 2 (cursor.go's own UpperWatermark contract), and a cursor
// minted under one filter is rejected (QUERY_CHANGED) when replayed against
// a different one.
func TestListKanban_FiltersAndPagingStable(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.ensureGeneration(t, "project-1", 1)
	env.upsertCheckpoint(t, "project-1", 1, 100, ports.ProjectionLive)

	env.seedFlatCard(t, "project-1", "wi-a", "fam-1", "BACKLOG", 1, 10)
	env.seedFlatCard(t, "project-1", "wi-b", "fam-1", "BACKLOG", 1, 11)
	env.seedFlatCard(t, "project-1", "wi-c", "fam-1", "BACKLOG", 1, 12)
	// A row with a DIFFERENT status — must never appear under the
	// status=BACKLOG filter below.
	env.seedFlatCard(t, "project-1", "wi-x", "fam-1", "DONE", 1, 13)

	type page struct {
		Items      []kanban.KanbanCardDTO `json:"items"`
		NextCursor string                 `json:"nextCursor"`
	}

	var first page
	decodeInto(t, env.get(t, "/projects/project-1/work-items/kanban?status=BACKLOG&limit=2"), &first)
	if len(first.Items) != 2 {
		t.Fatalf("page 1 len(Items) = %d, want 2", len(first.Items))
	}
	if first.NextCursor == "" {
		t.Fatalf("page 1 NextCursor is empty, want a cursor (3 BACKLOG rows exist, limit=2)")
	}

	// A row written AFTER page 1's own cursor was minted, whose WorkItemID
	// sorts BEFORE "wi-c" (the row page 2 is about to return) — proving
	// page 2 stays bounded by page 1's own pinned UpperWatermark rather
	// than picking this up mid-walk.
	env.seedFlatCard(t, "project-1", "wi-aa", "fam-1", "BACKLOG", 1, 14)

	var second page
	decodeInto(t, env.get(t, "/projects/project-1/work-items/kanban?status=BACKLOG&limit=2&cursor="+first.NextCursor), &second)
	if len(second.Items) != 1 {
		t.Fatalf("page 2 len(Items) = %d, want exactly 1 (the concurrently-written row must NOT appear in this walk)", len(second.Items))
	}
	if second.Items[0].WorkItemID != "wi-c" {
		t.Errorf("page 2 item = %q, want wi-c", second.Items[0].WorkItemID)
	}
	if second.NextCursor != "" {
		t.Errorf("page 2 NextCursor = %q, want empty (walk exhausted)", second.NextCursor)
	}

	seen := map[string]bool{}
	for _, item := range append(first.Items, second.Items...) {
		if seen[item.WorkItemID] {
			t.Errorf("duplicate item %q across pages", item.WorkItemID)
		}
		seen[item.WorkItemID] = true
		if item.Status != "BACKLOG" {
			t.Errorf("item %q has Status %q, want BACKLOG (filter leaked a non-matching row)", item.WorkItemID, item.Status)
		}
	}
	for _, want := range []string{"wi-a", "wi-b", "wi-c"} {
		if !seen[want] {
			t.Errorf("expected item %q never appeared across either page", want)
		}
	}
	if seen["wi-x"] || seen["wi-aa"] {
		t.Errorf("filter/watermark leaked an excluded row: seen=%v", seen)
	}

	// A cursor minted under status=BACKLOG must be rejected — as a typed
	// resync, never a silently-wrong result — when replayed against a
	// DIFFERENT filter (status=DONE).
	resp := env.get(t, "/projects/project-1/work-items/kanban?status=DONE&limit=2&cursor="+first.NextCursor)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("reused cursor under a different filter: status = %d, want 409", resp.StatusCode)
	}
	var errBody httpapi.ErrorResponse
	decodeInto(t, resp, &errBody)
	if errBody.Error.Code != httpapi.ErrorCodeResyncRequired {
		t.Errorf("error.code = %q, want RESYNC_REQUIRED", errBody.Error.Code)
	}
	if len(errBody.Error.Details) != 1 || errBody.Error.Details[0].Message != string(httpapi.ResyncReasonQueryChanged) {
		t.Errorf("error.details = %+v, want a single QUERY_CHANGED reason", errBody.Error.Details)
	}
}

// TestListKanban_GenerationResync is this task's own "generation resync"
// Verify bullet: a cursor bound to a generation that no longer matches the
// currently active one must resync, never silently mix rows across
// generations. V6-09A (the real fenced-cutover worker that would ever move
// a project's own active generation to a NEW number in production) is not
// built yet — ports.ProjectionRepository.EnsureGeneration itself refuses to
// swap an already-active generation to a different number (that is
// deliberately CutoverProjectionGeneration's own future job, its own doc
// comment says so explicitly), so this test cannot make a real cutover
// happen. Instead it tampers a REAL, server-issued cursor's own Generation
// field directly — decode with the SAME secret this test's own composition
// root uses (testCursorSecret), mutate only Generation, re-encode — which
// exercises the exact same server-side Bind check
// (internal/delivery/httpapi/cursor.go) a genuine V6-09A cutover would
// eventually trigger for real, without needing that worker to exist yet.
func TestListKanban_GenerationResync(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.ensureGeneration(t, "project-1", 1)
	env.upsertCheckpoint(t, "project-1", 1, 50, ports.ProjectionLive)
	env.seedFlatCard(t, "project-1", "wi-a", "fam-1", "BACKLOG", 1, 10)
	env.seedFlatCard(t, "project-1", "wi-b", "fam-1", "BACKLOG", 1, 11)

	type page struct {
		Items      []kanban.KanbanCardDTO `json:"items"`
		NextCursor string                 `json:"nextCursor"`
	}
	var first page
	decodeInto(t, env.get(t, "/projects/project-1/work-items/kanban?limit=1"), &first)
	if first.NextCursor == "" {
		t.Fatalf("expected a NextCursor from page 1")
	}

	codec := httpapi.NewCursorCodec([]byte(testCursorSecret))
	state, err := codec.Decode(first.NextCursor)
	if err != nil {
		t.Fatalf("Decode(first.NextCursor): %v", err)
	}
	if state.Generation != 1 {
		t.Fatalf("state.Generation = %d, want 1 (the real active generation at the time page 1 was issued)", state.Generation)
	}
	state.Generation = 2 // pretend a fenced cutover moved the active generation to 2.
	staleCursor, err := codec.Encode(state)
	if err != nil {
		t.Fatalf("Encode(tampered state): %v", err)
	}

	resp := env.get(t, "/projects/project-1/work-items/kanban?limit=1&cursor="+staleCursor)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("cursor bound to a stale generation: status = %d, want 409", resp.StatusCode)
	}
	var errBody httpapi.ErrorResponse
	decodeInto(t, resp, &errBody)
	if errBody.Error.Code != httpapi.ErrorCodeResyncRequired {
		t.Errorf("error.code = %q, want RESYNC_REQUIRED", errBody.Error.Code)
	}
	if len(errBody.Error.Details) != 1 || errBody.Error.Details[0].Message != string(httpapi.ResyncReasonGenerationChanged) {
		t.Errorf("error.details = %+v, want a single GENERATION_CHANGED reason", errBody.Error.Details)
	}
}

// TestListKanban_FreshnessReflectsStaleAndDegradedCheckpoint is this task's
// own "stale/degraded" Verify bullet.
func TestListKanban_FreshnessReflectsStaleAndDegradedCheckpoint(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")

	// No generation has ever been created yet — honestly reported STALE,
	// never fabricated as LIVE.
	var neverBootstrapped struct {
		Freshness httpapi.Freshness `json:"freshness"`
	}
	decodeInto(t, env.get(t, "/projects/project-1/work-items/kanban"), &neverBootstrapped)
	if neverBootstrapped.Freshness.Status != httpapi.FreshnessStale {
		t.Errorf("Freshness.Status (no generation yet) = %q, want STALE", neverBootstrapped.Freshness.Status)
	}

	env.ensureGeneration(t, "project-1", 1)
	env.upsertCheckpoint(t, "project-1", 1, 42, ports.ProjectionDegraded)
	var degraded struct {
		Freshness httpapi.Freshness `json:"freshness"`
	}
	decodeInto(t, env.get(t, "/projects/project-1/work-items/kanban"), &degraded)
	if degraded.Freshness.Status != httpapi.FreshnessDegraded {
		t.Errorf("Freshness.Status = %q, want DEGRADED", degraded.Freshness.Status)
	}
	if degraded.Freshness.AsOfJournalPosition != 42 {
		t.Errorf("Freshness.AsOfJournalPosition = %d, want 42", degraded.Freshness.AsOfJournalPosition)
	}

	env.upsertCheckpoint(t, "project-1", 1, 42, ports.ProjectionStale)
	var stale struct {
		Freshness httpapi.Freshness `json:"freshness"`
	}
	decodeInto(t, env.get(t, "/projects/project-1/work-items/kanban"), &stale)
	if stale.Freshness.Status != httpapi.FreshnessStale {
		t.Errorf("Freshness.Status = %q, want STALE", stale.Freshness.Status)
	}
}
