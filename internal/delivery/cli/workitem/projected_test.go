package workitem_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	kanbanapp "github.com/taQuangLing/agent-workflow/internal/app/kanban"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/projection"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	cliworkitem "github.com/taQuangLing/agent-workflow/internal/delivery/cli/workitem"
)

// seedProjectedCard writes one projection row for a real WorkItem under an
// active, LIVE generation 1 of projectID, the way the live consumer would.
func seedProjectedCard(t *testing.T, uow ports.UnitOfWork, projectID string, card projection.WorkItemCardRow, journalPosition uint64) {
	t.Helper()
	ctx := context.Background()
	payload, err := card.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	err = uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		if err := tx.Projections().EnsureGeneration(ctx, projectID, projection.ProjectionName, 1, 1, fixedNow); err != nil {
			return err
		}
		var expected *uint64
		if existing, getErr := tx.Projections().GetProjectionCheckpoint(ctx, projectID, projection.ProjectionName, 1); getErr == nil {
			cursor := existing.Cursor
			expected = &cursor
		}
		if err := tx.Projections().UpsertProjectionCheckpoint(ctx, ports.UpsertProjectionCheckpointRequest{
			ProjectID: projectID, ProjectionName: projection.ProjectionName, Generation: 1,
			ExpectedCursor: expected, NewCursor: journalPosition, NewStatus: ports.ProjectionLive, UpdatedAt: fixedNow,
		}); err != nil {
			return err
		}
		return tx.Projections().UpsertProjectionRow(ctx, ports.ProjectionRow{
			ProjectID: projectID, ProjectionName: projection.ProjectionName, Generation: 1,
			EntityKey: card.WorkItemID, PayloadJSON: string(payload), LastAppliedJournalPosition: journalPosition, UpdatedAt: fixedNow,
		})
	})
	if err != nil {
		t.Fatalf("seed projected card %s: %v", card.WorkItemID, err)
	}
}

func TestRunWorkItemKanban_ReportsProjectedCardsAndFreshnessAndFilters(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	root := rootWorkItemFixture(t, u, deps.IDs, "project-1", "repo-1")
	child := createChildForTest(t, deps, root.WorkItemID, "Child", "key-kanban-child")

	seedProjectedCard(t, u, "project-1", projection.WorkItemCardRow{
		WorkItemID: root.WorkItemID, ProjectID: "project-1", FamilyID: root.FamilyID, Title: "Implement the thing",
		IsRoot: true, WorkspaceSetID: root.WorkspaceSetID, Status: "BACKLOG", BlockerCount: 1, TopBlockerType: "APPROVAL",
	}, 3)
	seedProjectedCard(t, u, "project-1", projection.WorkItemCardRow{
		WorkItemID: child.WorkItemID, ProjectID: "project-1", FamilyID: root.FamilyID, Title: "Child",
		ParentWorkItemID: root.WorkItemID, Status: "READY",
	}, 4)

	run := func(args ...string) kanbanapp.Page {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if err := cliworkitem.RunWorkItemKanban(context.Background(), deps, args, &stdout, &stderr); err != nil {
			t.Fatalf("RunWorkItemKanban(%v): %v, stderr=%s", args, err, stderr.String())
		}
		var page kanbanapp.Page
		if err := json.Unmarshal(stdout.Bytes(), &page); err != nil {
			t.Fatalf("decode %s: %v", stdout.String(), err)
		}
		return page
	}

	all := run("--project-id", "project-1")
	if len(all.Items) != 2 {
		t.Fatalf("board = %+v, want both cards", all.Items)
	}
	if all.Freshness != (kanbanapp.Freshness{Generation: 1, AsOfJournalPosition: 4, Status: "LIVE"}) {
		t.Fatalf("freshness = %+v, want generation 1 at position 4, LIVE", all.Freshness)
	}
	byID := map[string]kanbanapp.Card{}
	for _, c := range all.Items {
		byID[c.WorkItemID] = c
	}
	if c := byID[root.WorkItemID]; !c.IsRoot || c.BlockerCount != 1 || c.TopBlockerType != "APPROVAL" {
		t.Errorf("root card = %+v, want the projected blocker data verbatim", c)
	}
	if c := byID[child.WorkItemID]; c.IsRoot || c.ParentWorkItemID != root.WorkItemID || c.WorkspaceSetID != root.WorkspaceSetID {
		t.Errorf("child card = %+v, want a non-root whose WorkspaceSetID comes from the root row", c)
	}

	if got := run("--project-id", "project-1", "--status", "ready"); len(got.Items) != 1 || got.Items[0].WorkItemID != child.WorkItemID {
		t.Errorf("--status ready = %+v, want only the READY child", got.Items)
	}
	if got := run("--project-id", "project-1", "--status", "READY", "--status", "backlog"); len(got.Items) != 2 {
		t.Errorf("repeated --status = %d cards, want 2", len(got.Items))
	}
	if got := run("--project-id", "project-1", "--family-id", "another-family"); len(got.Items) != 0 || got.Items == nil {
		t.Errorf("--family-id of no card = %#v, want an empty (non-nil) board", got.Items)
	}
}

func TestRunWorkItemKanban_UnprojectedProjectIsAnEmptyStaleBoardAndOtherProjectsAreInvisible(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	root := rootWorkItemFixture(t, u, deps.IDs, "project-1", "repo-1")

	var stdout, stderr bytes.Buffer
	if err := cliworkitem.RunWorkItemKanban(context.Background(), deps, []string{"--project-id", "project-1"}, &stdout, &stderr); err != nil {
		t.Fatalf("a never-projected board is not an error: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		t.Fatalf("decode %s: %v", stdout.String(), err)
	}
	if len(raw) != 2 {
		t.Fatalf("result keys = %v, want exactly items and freshness (the internal resume point must not leak): %s", raw, stdout.String())
	}
	if string(bytes.TrimSpace(raw["items"])) != "[]" {
		t.Fatalf("items = %s, want an empty array, never null", raw["items"])
	}
	var freshness kanbanapp.Freshness
	if err := json.Unmarshal(raw["freshness"], &freshness); err != nil || freshness != (kanbanapp.Freshness{Status: "STALE"}) {
		t.Fatalf("freshness = %+v (err %v), want generation 0 STALE", freshness, err)
	}

	// A projected card of project-1 is never on another project's board.
	seedProjectedCard(t, u, "project-1", projection.WorkItemCardRow{
		WorkItemID: root.WorkItemID, ProjectID: "project-1", FamilyID: root.FamilyID, Title: "x", IsRoot: true, Status: "BACKLOG",
	}, 1)
	stdout.Reset()
	if err := cliworkitem.RunWorkItemKanban(context.Background(), deps, []string{"--project-id", "project-2"}, &stdout, &stderr); err != nil {
		t.Fatalf("RunWorkItemKanban(project-2): %v", err)
	}
	var other kanbanapp.Page
	if err := json.Unmarshal(stdout.Bytes(), &other); err != nil || len(other.Items) != 0 {
		t.Fatalf("project-2 board = %s (err %v), want empty", stdout.String(), err)
	}
}

func TestRunWorkItemDetail_ProjectedCardNextToFreshReadinessAndScopeCheck(t *testing.T) {
	deps := newTestDeps(t)
	u := deps.UoW.(*fake.UnitOfWork)
	root := rootWorkItemFixture(t, u, deps.IDs, "project-1", "repo-1")
	// The projection claims RUNNING; the real row is a BACKLOG root.
	seedProjectedCard(t, u, "project-1", projection.WorkItemCardRow{
		WorkItemID: root.WorkItemID, ProjectID: "project-1", FamilyID: root.FamilyID, Title: "Implement the thing",
		IsRoot: true, Status: "RUNNING",
	}, 2)

	var stdout, stderr bytes.Buffer
	args := []string{"--project-id", "project-1", root.WorkItemID}
	if err := cliworkitem.RunWorkItemDetail(context.Background(), deps, args, &stdout, &stderr); err != nil {
		t.Fatalf("RunWorkItemDetail: %v, stderr=%s", err, stderr.String())
	}
	var detail kanbanapp.Detail
	if err := json.Unmarshal(stdout.Bytes(), &detail); err != nil {
		t.Fatalf("decode %s: %v", stdout.String(), err)
	}
	if detail.Card.Status != "RUNNING" {
		t.Errorf("card.status = %s, want the projected RUNNING verbatim", detail.Card.Status)
	}
	if detail.Readiness.Status != "BACKLOG" || detail.Readiness.WorkItemID != root.WorkItemID || detail.Readiness.Version == 0 {
		t.Errorf("readiness = %+v, want the REAL BACKLOG status with its version", detail.Readiness)
	}
	if detail.Freshness.Status != "LIVE" {
		t.Errorf("freshness = %+v", detail.Freshness)
	}

	// Another project's id reports the same failure an unknown id does, and
	// writes nothing.
	stdout.Reset()
	if err := cliworkitem.RunWorkItemDetail(context.Background(), deps, []string{"--project-id", "project-2", root.WorkItemID}, &stdout, &stderr); err == nil {
		t.Fatal("a work item must not be readable through another project")
	}
	if err := cliworkitem.RunWorkItemDetail(context.Background(), deps, []string{"--project-id", "project-1", "no-such-item"}, &stdout, &stderr); err == nil {
		t.Fatal("an unknown work item must be an error")
	}
	if stdout.Len() != 0 {
		t.Fatalf("a rejected read wrote %q to stdout", stdout.String())
	}
}

func TestProjectedReads_UsageErrors(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	for name, args := range map[string][]string{
		"kanban: no project id":     {},
		"kanban: stray positional":  {"--project-id", "p1", "extra"},
		"kanban: unknown flag":      {"--project-id", "p1", "--limit", "5"},
		"detail: no project id":     {"wi-1"},
		"detail: no argument":       {"--project-id", "p1"},
		"detail: blank id":          {"--project-id", "p1", " "},
		"detail: two arguments":     {"--project-id", "p1", "a", "b"},
		"kanban: blank project id":  {"--project-id", " "},
		"detail: blank project id":  {"--project-id", " ", "wi-1"},
		"kanban: status without it": {"--project-id", "p1", "--status"},
	} {
		var err error
		if name[:6] == "kanban" {
			err = cliworkitem.RunWorkItemKanban(context.Background(), deps, args, &stdout, &stderr)
		} else {
			err = cliworkitem.RunWorkItemDetail(context.Background(), deps, args, &stdout, &stderr)
		}
		if err == nil || !cli.IsUsageError(err) {
			t.Errorf("%s: err = %v, want a usage error", name, err)
		}
	}
}
