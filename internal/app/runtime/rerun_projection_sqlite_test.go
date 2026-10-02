package runtime_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/kanban"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/projection"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
)

// TestRerun_SQLite_KanbanProjectionFollowsTheLoopAndRebuildReproducesIt is the
// V9-06 projection proof (ADR-033, gap G6), run on the REAL journal the
// commands above produce, not on hand-written events: the live consumer puts
// the WorkItem in the BLOCKED column after a failed run, back in READY after
// the resolve, follows the new run when it starts, counts the runs, and a
// from-scratch replay of the same journal (what `aw projection rebuild` does)
// yields byte-identical rows. The authoritative detail lists every run.
func TestRerun_SQLite_KanbanProjectionFollowsTheLoopAndRebuildReproducesIt(t *testing.T) {
	f := newRerunSQLite(t)
	catalog := projection.NewCatalog()
	now := time.Now().UTC()
	tick := 0

	apply := func() {
		t.Helper()
		tick++
		outcome, err := projection.ApplyBatch(f.ctx, f.uow, catalog, projection.ApplyBatchRequest{
			ProjectID: "project-1", ProjectionName: projection.ProjectionName, Owner: "consumer-a",
			TTL: 30 * time.Second, BatchSize: 10000, Now: now.Add(time.Duration(tick) * time.Second), IDs: idsource.Random{},
		})
		if err != nil {
			t.Fatalf("ApplyBatch: %v", err)
		}
		if outcome.Poisoned {
			t.Fatalf("ApplyBatch poisoned: %s", outcome.PoisonReason)
		}
	}
	card := func() kanban.Card {
		t.Helper()
		page, err := kanban.ListWorkItemKanban(f.ctx, f.uow, kanban.ListRequest{ProjectID: "project-1"})
		if err != nil {
			t.Fatalf("ListWorkItemKanban: %v", err)
		}
		for _, c := range page.Items {
			if c.WorkItemID == f.workItemID {
				return c
			}
		}
		t.Fatalf("board %+v has no card for %s", page.Items, f.workItemID)
		return kanban.Card{}
	}

	// Before any run: no count.
	apply()
	if got := card(); got.RunCount != 0 {
		t.Fatalf("card before any run = %+v, want runCount 0", got)
	}

	// First run starts: ACTIVE, following run 1, one run.
	first := f.mustStart("idem-start-1")
	apply()
	got := card()
	if got.Status != "ACTIVE" || got.ActiveRunID != first.RunID || got.ActiveRunStatus != "ACTIVE" || got.RunCount != 1 {
		t.Fatalf("card with run 1 live = %+v, want ACTIVE following %s, runCount 1", got, first.RunID)
	}

	// Run 1 fails: the BLOCKED column, carrying the RUN_FAILED blocker badge,
	// still pointing at the failed run so its diagnostics stay reachable.
	f.failRun(first)
	apply()
	got = card()
	if got.Status != "BLOCKED" || got.BlockerCount != 1 || got.TopBlockerType != "RUN_FAILED" ||
		got.ActiveRunID != first.RunID || got.ActiveRunStatus != "FAILED" || got.RunCount != 1 {
		t.Fatalf("card after the failure = %+v, want BLOCKED, 1 blocker (RUN_FAILED), failed run %s, runCount 1", got, first.RunID)
	}

	// Resolve: READY, no blocker, no live run.
	if _, err := f.resolve(string(f.onlyOpenRunFailedBlocker().ID), runtime.ResolutionModeResolved, ""); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	apply()
	got = card()
	if got.Status != "READY" || got.BlockerCount != 0 || got.TopBlockerType != "" || got.ActiveRunID != "" || got.RunCount != 1 {
		t.Fatalf("card after the resolve = %+v, want READY with no blocker and no live run, runCount 1", got)
	}

	// Rerun: the card follows the NEW run, and counts two.
	second := f.mustStart("idem-start-2")
	apply()
	got = card()
	if got.Status != "ACTIVE" || got.ActiveRunID != second.RunID || got.ActiveRunStatus != "ACTIVE" || got.RunCount != 2 {
		t.Fatalf("card with run 2 live = %+v, want ACTIVE following %s, runCount 2", got, second.RunID)
	}

	// Run 2 fails as well: BLOCKED again, still counting two runs.
	f.failRun(second)
	apply()
	got = card()
	if got.Status != "BLOCKED" || got.BlockerCount != 1 || got.TopBlockerType != "RUN_FAILED" || got.ActiveRunID != second.RunID || got.RunCount != 2 {
		t.Fatalf("card after the second failure = %+v, want BLOCKED, 1 blocker (RUN_FAILED), failed run %s, runCount 2", got, second.RunID)
	}

	// The task detail: the projected card's count and the authoritative list
	// of every run, oldest first, all on the one pinned workflow version.
	detail, err := kanban.GetWorkItemProjectedDetail(f.ctx, f.uow, kanban.DetailRequest{WorkItemID: f.workItemID})
	if err != nil {
		t.Fatalf("GetWorkItemProjectedDetail: %v", err)
	}
	if detail.Card.RunCount != 2 {
		t.Fatalf("detail card runCount = %d, want 2", detail.Card.RunCount)
	}
	if len(detail.Runs) != 2 {
		t.Fatalf("detail runs = %+v, want 2", detail.Runs)
	}
	for i, want := range []string{first.RunID, second.RunID} {
		r := detail.Runs[i]
		if r.RunID != want || r.RunNumber != i+1 || r.State != "FAILED" || r.WorkflowVersionID != string(f.version.ID()) || r.StartedAt == nil {
			t.Fatalf("detail run %d = %+v, want %s #%d FAILED on %s", i, r, want, i+1, f.version.ID())
		}
	}

	// A from-scratch replay of the same journal into a fresh generation (the
	// rebuild path) reproduces every projected row exactly, runCount included.
	const rebuilt = 2
	for {
		tick++
		outcome, err := projection.ReplayGenerationBatch(f.ctx, f.uow, catalog, projection.ReplayGenerationBatchRequest{
			ProjectID: "project-1", ProjectionName: projection.ProjectionName, Generation: rebuilt, Owner: "rebuild",
			TTL: 30 * time.Second, BatchSize: 10000, Now: now.Add(time.Duration(tick) * time.Second), IDs: idsource.Random{},
		})
		if err != nil {
			t.Fatalf("ReplayGenerationBatch: %v", err)
		}
		if outcome.Poisoned {
			t.Fatalf("replay poisoned: %s", outcome.PoisonReason)
		}
		if outcome.EventsScanned == 0 {
			break
		}
	}
	rows := func(generation uint64) map[string]string {
		t.Helper()
		out := map[string]string{}
		if err := f.uow.WithReadOnly(f.ctx, func(tx ports.Tx) error {
			list, err := tx.Projections().ListProjectionRows(f.ctx, "project-1", projection.ProjectionName, generation)
			for _, row := range list {
				out[row.EntityKey] = row.PayloadJSON
			}
			return err
		}); err != nil {
			t.Fatalf("ListProjectionRows(%d): %v", generation, err)
		}
		return out
	}
	live, replayed := rows(1), rows(rebuilt)
	if len(live) == 0 || !reflect.DeepEqual(live, replayed) {
		t.Fatalf("rebuilt generation differs from the live one:\nlive     = %v\nreplayed = %v", live, replayed)
	}
}
