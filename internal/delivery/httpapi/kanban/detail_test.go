package kanban_test

import (
	"net/http"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/projection"
	workapp "github.com/taQuangLing/agent-workflow/internal/app/work"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/kanban"
)

// detailResponse mirrors kanban's own unexported workItemDetailResponse
// wire shape (dto.go) — this external test package (kanban_test) asserts
// against the real HTTP JSON contract, not the internal type, exactly like
// every other *_test.go file in this codebase's own httpapi subpackages.
type detailResponse struct {
	Card         kanban.KanbanCardDTO      `json:"card"`
	Readiness    workapp.WorkItemReadiness `json:"readiness"`
	Freshness    httpapi.Freshness         `json:"freshness"`
	ValidActions []httpapi.ValidAction     `json:"validActions"`
}

// TestGetWorkItemDetail_ActionRace_UsesFreshReadinessNotStaleProjection is
// this task's own single most important test — the "action race" Verify
// bullet, and the concrete meaning of "Hoàn thành khi: ... cannot create
// false action authority". The seeded projection row deliberately CLAIMS
// (falsely, as any real row could after enough projection lag) that this
// WorkItem is READY with zero blockers. The real, authoritative WorkItem
// is still BACKLOG and — per internal/app/work/queries.go's own doc
// comment, "every real WorkItem today reports the identical set of
// [readiness] problems" (no public command populates a WorkItem's own
// contract fields yet) — genuinely fails ValidateReadinessGate. The
// response must display the stale Card.Status literally (it's a projected
// display value, never itself authority) while Readiness/ValidActions are
// computed FRESH and must NOT be fooled by it.
func TestGetWorkItemDetail_ActionRace_UsesFreshReadinessNotStaleProjection(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.createRoot(t, "project-1", "Root task", grant("repo-a"))

	env.ensureGeneration(t, "project-1", 1)
	env.upsertCheckpoint(t, "project-1", 1, 20, ports.ProjectionLive)
	env.seedProjectionRow(t, "project-1", 1, projection.WorkItemCardRow{
		WorkItemID: root.WorkItemID, ProjectID: "project-1", FamilyID: root.FamilyID, Title: "Root task",
		IsRoot: true, WorkspaceSetID: root.WorkspaceSetID, Status: "READY", BlockerCount: 0,
	}, 15)

	var response detailResponse
	resp := env.get(t, "/work-items/"+root.WorkItemID+"/detail")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET detail: status = %d", resp.StatusCode)
	}
	decodeInto(t, resp, &response)

	if response.Card.Status != "READY" {
		t.Errorf("Card.Status = %q, want the stale PROJECTED value READY (display-only, never authority)", response.Card.Status)
	}
	if response.Readiness.Status != "BACKLOG" {
		t.Errorf("Readiness.Status = %q, want the FRESH authoritative value BACKLOG — the projection's stale claim must never leak into Readiness", response.Readiness.Status)
	}
	if response.Readiness.Ready {
		t.Errorf("Readiness.Ready = true, want false — the real WorkItem has an empty contract and genuinely fails ValidateReadinessGate regardless of what the stale projection claims")
	}
	if len(response.Readiness.Problems) == 0 {
		t.Errorf("Readiness.Problems is empty, want the real ValidateReadinessGate failure reasons")
	}
	if len(response.ValidActions) != 0 {
		t.Errorf("ValidActions = %+v, want empty — markWorkItemReady must never be advertised off a stale projected READY status", response.ValidActions)
	}
	if response.Readiness.Version != 1 {
		t.Errorf("Readiness.Version = %d, want 1 (the real WorkItem's own current version)", response.Readiness.Version)
	}
}

// TestGetWorkItemDetail_MissingProjectionRow_FallsBackToAuthoritative
// proves the honest-fallback path: a real WorkItem that the projection has
// not caught up to yet still returns a usable (if display-sparse) card,
// built straight from the authoritative row — never a 404, since the
// WorkItem itself genuinely exists.
func TestGetWorkItemDetail_MissingProjectionRow_FallsBackToAuthoritative(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.createRoot(t, "project-1", "Root task", grant("repo-a"))
	// No projection row, no generation ever created for this project.

	var response detailResponse
	resp := env.get(t, "/work-items/"+root.WorkItemID+"/detail")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET detail: status = %d", resp.StatusCode)
	}
	decodeInto(t, resp, &response)

	if response.Card.WorkItemID != root.WorkItemID || response.Card.Title != "Root task" || response.Card.Status != "BACKLOG" {
		t.Errorf("fallback Card = %+v, want authoritative WorkItemID/Title/Status", response.Card)
	}
	if response.Freshness.Status != httpapi.FreshnessStale {
		t.Errorf("Freshness.Status = %q, want STALE (no generation exists yet)", response.Freshness.Status)
	}
	if response.Readiness.WorkItemID != root.WorkItemID {
		t.Errorf("Readiness.WorkItemID = %q, want %q", response.Readiness.WorkItemID, root.WorkItemID)
	}
}

// TestGetWorkItemDetail_UnknownWorkItem_IsResourceHidden proves the
// leakage-normalized 404 for a WorkItemID that names no real row at all.
func TestGetWorkItemDetail_UnknownWorkItem_IsResourceHidden(t *testing.T) {
	env := newTestEnv(t)
	resp := env.get(t, "/work-items/does-not-exist/detail")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	var errBody httpapi.ErrorResponse
	decodeInto(t, resp, &errBody)
	if errBody.Error.Code != httpapi.ErrorCodeNotFound {
		t.Errorf("error.code = %q, want NOT_FOUND", errBody.Error.Code)
	}
}
