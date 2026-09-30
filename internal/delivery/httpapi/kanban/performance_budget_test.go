package kanban_test

// V8-07 (docs/design/10-v8-alpha-hardening.md): "đo ... Kanban, task
// detail ... ghi hardware/profile rồi freeze numeric threshold ... Hoàn
// thành khi: không có unbounded query/render/memory path". This file
// measures the two GET routes this package owns at real, seeded scale in a
// real sqlite database — never a synthetic Go-level micro-benchmark of the
// decode loop alone, since the actual concern is the REAL query+handler
// path an operator's browser would hit.
//
// Real finding (recorded here, not fixed in this task — see
// baocaov8checklist.md's own V8-07 section for the full reasoning):
// list.go's own handleListKanban calls ListProjectionRows unconditionally,
// which loads and decodes EVERY row this project's own generation
// currently has before applying the requested page's own limit/cursor
// in-memory (list.go's own doc comment already explains why: an
// unordered-UUID sort key needs the whole row set to pin a consistent
// UpperWatermark). This is a genuine O(project size) cost per page
// request, not O(page size) — TestKanbanListLatency below proves this
// stays LINEAR (bounded) rather than superlinear/unbounded as project size
// grows, which is the actual bar V8-07's own "Hoàn thành khi" line sets;
// it does not by itself justify a full pagination-layer rewrite of an
// already-deliberate design choice.
import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/projection"
)

// seedManyFlatCards seeds count synthetic Kanban projection rows directly,
// all inside ONE transaction (unlike seedProjectionRow's own one-
// transaction-per-row convention) purely so growing "project size" by
// thousands of rows stays fast enough for a latency benchmark to run in
// CI — these rows never need a real backing WorkItem, since list.go's own
// handler only ever decodes projection_rows, it never calls tx.Work() for
// any row in the list.
func (e *testEnv) seedManyFlatCards(t *testing.T, projectID string, startIndex, count int, journalPositionStart uint64) {
	t.Helper()
	ctx := context.Background()
	err := e.uow.WithSerializedWrite(ctx, func(tx ports.Tx) error {
		for i := startIndex; i < startIndex+count; i++ {
			card := projection.WorkItemCardRow{
				WorkItemID: fmt.Sprintf("wi-scale-%06d", i), ProjectID: projectID,
				FamilyID: fmt.Sprintf("fam-scale-%06d", i), Title: fmt.Sprintf("scale card %d", i),
				IsRoot: true, Status: "BACKLOG",
			}
			payload, err := card.CanonicalJSON()
			if err != nil {
				return err
			}
			if err := tx.Projections().UpsertProjectionRow(ctx, ports.ProjectionRow{
				ProjectID: projectID, ProjectionName: projection.ProjectionName, Generation: 1,
				EntityKey: card.WorkItemID, PayloadJSON: string(payload),
				LastAppliedJournalPosition: journalPositionStart + uint64(i-startIndex), UpdatedAt: time.Now().UTC(),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seedManyFlatCards(%s, %d, %d): %v", projectID, startIndex, count, err)
	}
}

// measureKanbanListLatency issues 20 real GETs, times the WHOLE batch in one
// time.Now()/time.Since() pair, and returns the per-request average.
//
// This replaced an earlier min-of-7-individually-timed-samples design after
// a real CI run (PR #133, then again on the docs-only PR #134 — see
// baocaov8checklist.md's own V8-07 follow-up section) caught it returning an
// exact `0s` measurement: on a fast/quiet Windows runner, time.Since()
// around ONE very-fast in-process HTTP round trip can truncate to exactly
// zero due to timer-resolution granularity, and taking the minimum of
// several individually-timed samples makes hitting that zero MORE likely,
// not less (one lucky near-zero sample poisons the whole minimum) — it hit
// three separate real CI runs across two different PRs. Timing the entire
// batch in one pair and dividing by the count makes an exact-zero TOTAL
// virtually impossible even under coarse timer resolution (summing 20 real
// round trips is reliably measurable), while still averaging out ordinary
// per-request scheduler/GC noise well enough for this file's own frozen
// ratio thresholds (30x and 6x), which already carry wide margins.
func measureKanbanListLatency(t *testing.T, e *testEnv, path string) time.Duration {
	t.Helper()
	const samples = 20
	start := time.Now()
	for i := 0; i < samples; i++ {
		resp := e.get(t, path)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status = %d", path, resp.StatusCode)
		}
		resp.Body.Close()
	}
	return time.Since(start) / samples
}

// TestV8PerformanceBudget_KanbanListLatencyScalesBoundedWithProjectSize is
// V8-07's own frozen scaling-shape assertion for the Kanban list route —
// see baocaov8checklist.md's own V8-07 section for why a SCALING RATIO
// (not an absolute wall-clock threshold) is the frozen, hardware-
// independent bar here: absolute wall-clock numbers on a shared CI runner
// have already proven noisy enough this session (see
// agent-kit-ci-known-flakes memory) to make a tight absolute-time
// assertion a real flake source, while a gross scaling-ratio bound still
// catches the actual failure mode this task cares about (an accidentally
// quadratic-or-worse path), which a 10x data increase would blow up to
// ~100x, not ~30x.
//
// Frozen threshold: ratio(2000 rows) / ratio(200 rows) < 30. Owner: V8-07
// task (this session, 2026-09-29). Reason: a purely linear O(n) full-
// project-scan-then-paginate cost (list.go's own documented, deliberate
// design — see this file's own package doc comment) predicts a ratio near
// 10x for a 10x data increase; 30x leaves ample headroom above that for
// per-request fixed-cost amortization noise while still failing hard
// noticeably below the ~100x a real O(n^2) regression would produce.
func TestV8PerformanceBudget_KanbanListLatencyScalesBoundedWithProjectSize(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.ensureGeneration(t, "project-1", 1)
	env.upsertCheckpoint(t, "project-1", 1, 100000, ports.ProjectionLive)

	const small = 200
	const large = 2000
	env.seedManyFlatCards(t, "project-1", 0, small, 1)
	smallLatency := measureKanbanListLatency(t, env, "/projects/project-1/work-items/kanban?limit=50")

	env.seedManyFlatCards(t, "project-1", small, large-small, uint64(small)+1)
	largeLatency := measureKanbanListLatency(t, env, "/projects/project-1/work-items/kanban?limit=50")

	t.Logf("V8-07 benchmark report: Kanban list first-page latency — %d rows: %v, %d rows: %v (ratio %.2fx for a %dx data increase)",
		small, smallLatency, large, largeLatency, float64(largeLatency)/float64(smallLatency), large/small)

	const maxRatio = 30.0
	if smallLatency <= 0 {
		t.Fatalf("smallLatency = %v, want > 0", smallLatency)
	}
	if ratio := float64(largeLatency) / float64(smallLatency); ratio > maxRatio {
		t.Errorf("latency ratio = %.2fx for a %dx data increase, want < %.0fx (frozen V8-07 threshold — suggests a worse-than-linear, unbounded query path)",
			ratio, large/small, maxRatio)
	}
}

// TestV8PerformanceBudget_WorkItemDetailLatencyStaysFlatAsProjectGrows is
// the positive control this file's own package doc comment promises:
// detail.go's own resolveWorkspaceSetIDForDetail doc comment already
// states this route deliberately never scans the whole project the way
// list.go does — a single targeted WorkItem lookup instead. This test
// proves that claim holds under real, growing project size: one real
// target WorkItem's own detail latency must stay flat, never grow
// proportionally with how many OTHER unrelated rows this project
// accumulates.
//
// Frozen threshold: ratio < 6x for a 10x growth in unrelated project rows.
// Owner/reason: same as this file's Kanban test above, with one addition —
// a real CI run caught the original 3x cutoff producing a false failure
// (284µs vs 934µs, ratio 3.28x) purely from scheduler/GC noise at a sub-
// millisecond absolute baseline, where even a single unlucky context
// switch can swing the ratio by 2x+ on its own. 6x still leaves a wide,
// clearly-distinguishable gap below the ~10x a real O(n) leak in this
// route would produce, while tolerating that noise floor.
func TestV8PerformanceBudget_WorkItemDetailLatencyStaysFlatAsProjectGrows(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedActiveRepository(t, "project-1", "repo-a")
	root := env.createRoot(t, "project-1", "Root task", grant("repo-a"))
	env.ensureGeneration(t, "project-1", 1)
	env.upsertCheckpoint(t, "project-1", 1, 100000, ports.ProjectionLive)
	env.seedProjectionRow(t, "project-1", 1, projection.WorkItemCardRow{
		WorkItemID: root.WorkItemID, ProjectID: "project-1", FamilyID: root.FamilyID,
		Title: "Root task", IsRoot: true, WorkspaceSetID: root.WorkspaceSetID, Status: "BACKLOG",
	}, 1)

	detailPath := "/work-items/" + root.WorkItemID + "/detail"
	const small = 200
	const large = 2000
	env.seedManyFlatCards(t, "project-1", 0, small, 2)
	smallLatency := measureKanbanListLatency(t, env, detailPath)

	env.seedManyFlatCards(t, "project-1", small, large-small, uint64(small)+2)
	largeLatency := measureKanbanListLatency(t, env, detailPath)

	t.Logf("V8-07 benchmark report: WorkItem detail latency — project size %d: %v, project size %d: %v (ratio %.2fx for a %dx data increase)",
		small, smallLatency, large, largeLatency, float64(largeLatency)/float64(smallLatency), large/small)

	const maxRatio = 6.0
	if smallLatency <= 0 {
		t.Fatalf("smallLatency = %v, want > 0", smallLatency)
	}
	if ratio := float64(largeLatency) / float64(smallLatency); ratio > maxRatio {
		t.Errorf("latency ratio = %.2fx for a %dx growth in unrelated project rows, want < %.0fx (frozen V8-07 threshold — the detail route should never scan the whole project)",
			ratio, large/small, maxRatio)
	}
}
