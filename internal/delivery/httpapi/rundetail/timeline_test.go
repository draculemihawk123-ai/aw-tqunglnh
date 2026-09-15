package rundetail_test

// Real HTTP round-trip coverage for GET /runs/{id}/timeline (V6-06B).

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
)

type timelineHTTPResponse struct {
	RunID     string                      `json:"runId"`
	Entries   []runtime.TimelineEntryView `json:"entries"`
	Freshness struct {
		Generation          int    `json:"generation"`
		AsOfJournalPosition int64  `json:"asOfJournalPosition"`
		Status              string `json:"status"`
	} `json:"freshness"`
	NextCursor string `json:"nextCursor"`
}

func getTimeline(t *testing.T, baseURL, runID, query string) (int, timelineHTTPResponse) {
	t.Helper()
	u := baseURL + "/runs/" + runID + "/timeline"
	if query != "" {
		u += "?" + query
	}
	resp, err := http.Get(u)
	if err != nil {
		t.Fatalf("GET %s: %v", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s status = %d, body=%s", u, resp.StatusCode, body)
	}
	var out timelineHTTPResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp.StatusCode, out
}

func TestGetRunTimeline_HTTP_NotFound(t *testing.T) {
	_, uow := openRunDetailTestStore(t, "timeline-notfound.db")
	server := newTestServer(t, uow, redact.Matcher{})

	resp, err := http.Get(server.URL + "/runs/unknown-run/timeline")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestGetRunTimeline_HTTP_ReturnsOrderedEntries(t *testing.T) {
	ctx := context.Background()
	_, uow := openRunDetailTestStore(t, "timeline-basic.db")
	ids := idsource.NewSequential("id")
	root := readyWorkItemFixture(t, uow, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, uow, "project-1", "wf-def-1", "wf-v-1", routerChainDocument())
	startCmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	startResult, err := runtime.StartWorkflowRun(ctx, uow, ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	hop1, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: startResult.NodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun (start->router1): %v", err)
	}
	if _, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: hop1.NextNodeRunID}); err != nil {
		t.Fatalf("AdvanceRun (router1->router2): %v", err)
	}

	server := newTestServer(t, uow, redact.Matcher{})
	_, timeline := getTimeline(t, server.URL, startResult.RunID, "")

	if timeline.RunID != startResult.RunID {
		t.Fatalf("timeline.RunID = %s, want %s", timeline.RunID, startResult.RunID)
	}
	if len(timeline.Entries) != 3 {
		t.Fatalf("timeline.Entries = %+v, want exactly 3 NODE_RUN entries (start, router1, router2)", timeline.Entries)
	}
	wantKeys := []string{"start", "router1", "router2"}
	for i, e := range timeline.Entries {
		if e.Kind != runtime.TimelineEntryNodeRun || e.NodeKey != wantKeys[i] {
			t.Fatalf("entry[%d] = %+v, want Kind=NODE_RUN NodeKey=%s", i, e, wantKeys[i])
		}
	}
	for i := 1; i < len(timeline.Entries); i++ {
		if timeline.Entries[i-1].ActivationSequence > timeline.Entries[i].ActivationSequence {
			t.Fatalf("entries not non-decreasing by ActivationSequence at index %d: %+v", i, timeline.Entries)
		}
	}
	if timeline.Freshness.Status != "LIVE" {
		t.Fatalf("timeline.Freshness.Status = %s, want LIVE", timeline.Freshness.Status)
	}
}

// TestGetRunTimeline_HTTP_PagesAndStaysStableAcrossConcurrentWrite mirrors
// graph_test.go's own identical fork/concurrent-write proof, applied to the
// timeline's own 3-part (ActivationSequence, NodeRunID, subOrder) cursor
// key instead of the graph's own 2-part key.
func TestGetRunTimeline_HTTP_PagesAndStaysStableAcrossConcurrentWrite(t *testing.T) {
	ctx := context.Background()
	_, uow := openRunDetailTestStore(t, "timeline-paging.db")
	ids := idsource.NewSequential("id")
	root := readyWorkItemFixture(t, uow, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, uow, "project-1", "wf-def-1", "wf-v-1", routerChainDocument())
	startCmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	startResult, err := runtime.StartWorkflowRun(ctx, uow, ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}
	hop1, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: startResult.NodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun (start->router1): %v", err)
	}

	server := newTestServer(t, uow, redact.Matcher{})

	_, page1 := getTimeline(t, server.URL, startResult.RunID, "limit=1")
	if len(page1.Entries) != 1 || page1.Entries[0].NodeKey != "start" || page1.NextCursor == "" {
		t.Fatalf("page1 = %+v, want exactly [start] with a NextCursor", page1)
	}

	if _, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: hop1.NextNodeRunID}); err != nil {
		t.Fatalf("AdvanceRun (router1->router2): %v", err)
	}

	_, page2 := getTimeline(t, server.URL, startResult.RunID, "limit=1&cursor="+url.QueryEscape(page1.NextCursor))
	if len(page2.Entries) != 1 || page2.Entries[0].NodeKey != "router1" {
		t.Fatalf("page2 = %+v, want exactly [router1]", page2)
	}
	if page2.NextCursor != "" {
		t.Fatalf("page2.NextCursor = %q, want empty", page2.NextCursor)
	}

	_, fresh := getTimeline(t, server.URL, startResult.RunID, "")
	if len(fresh.Entries) != 3 {
		t.Fatalf("fresh.Entries len = %d, want 3", len(fresh.Entries))
	}
}

// TestGetRunTimeline_HTTP_LimitBounds proves ResolveLimit's own bounded-
// defaults/max policy (httpapi.DefaultPageLimit/MaxPageLimit) is actually
// wired through this route: an out-of-range limit is a typed 400, never a
// silently-ignored value, and an over-large limit silently clamps rather
// than ever returning an unbounded response — this task's own "bounds"
// Verify bullet.
func TestGetRunTimeline_HTTP_LimitBounds(t *testing.T) {
	ctx := context.Background()
	_, uow := openRunDetailTestStore(t, "timeline-bounds.db")
	ids := idsource.NewSequential("id")
	root := readyWorkItemFixture(t, uow, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, uow, "project-1", "wf-def-1", "wf-v-1", workflowDocumentV1())
	startCmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	startResult, err := runtime.StartWorkflowRun(ctx, uow, ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}

	server := newTestServer(t, uow, redact.Matcher{})

	resp, err := http.Get(server.URL + "/runs/" + startResult.RunID + "/timeline?limit=0")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("limit=0 status = %d, want 400", resp.StatusCode)
	}

	resp2, err := http.Get(server.URL + "/runs/" + startResult.RunID + "/timeline?limit=-1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("limit=-1 status = %d, want 400", resp2.StatusCode)
	}

	// A very large limit clamps to httpapi.MaxPageLimit rather than erroring
	// or returning an unbounded page — proven here indirectly: this Run
	// only has 1 NodeRun (START, never advanced), so a request for 100000
	// entries just returns that one real entry, with no error and no
	// NextCursor, rather than the request itself being rejected.
	_, page := getTimeline(t, server.URL, startResult.RunID, "limit=100000")
	if len(page.Entries) != 1 || page.NextCursor != "" {
		t.Fatalf("page = %+v, want exactly 1 entry, no NextCursor", page)
	}
}

func TestGetRunTimeline_HTTP_InvalidCursor_Returns400(t *testing.T) {
	ctx := context.Background()
	_, uow := openRunDetailTestStore(t, "timeline-badcursor.db")
	ids := idsource.NewSequential("id")
	root := readyWorkItemFixture(t, uow, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, uow, "project-1", "wf-def-1", "wf-v-1", workflowDocumentV1())
	startCmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	startResult, err := runtime.StartWorkflowRun(ctx, uow, ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}

	server := newTestServer(t, uow, redact.Matcher{})
	resp, err := http.Get(server.URL + "/runs/" + startResult.RunID + "/timeline?cursor=not-a-real-token")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// TestGetRunGraphAndTimeline_HTTP_RedactBlockReason proves AK-ARCH-024's
// own redaction policy actually reaches both routes through the real
// process-lifetime redact.Matcher — a real BLOCKED NodeRun (hand-seeded
// here, mirroring internal/app/runtime's own identical, narrowly-scoped
// redaction unit test — see that package's own
// TestGetRunGraph_RedactsBlockReason doc comment for why hand-seeding ONE
// field is acceptable for this one targeted assertion) whose own
// BlockReason exactly matches a known secret must never appear verbatim in
// either response.
//
// Uses fake.New() rather than real sqlite: run_detail_queries_sqlite_test.go's
// own TestGetRunGraph_RedactsBlockReason made the identical choice for the
// identical reason — internal/adapters/sqlite's own CreateNodeRun/NodeRun
// row scan has no block_reason column at all yet (confirmed by grep: no
// occurrence of "block_reason" anywhere under internal/adapters/sqlite), a
// pre-existing gap in the persistence layer this task does not fix (no new
// migration is this task's own expected scope) — every value this package's
// own redaction path is asked to redact must therefore be exercised against
// an in-memory fake.UnitOfWork, which round-trips the domain struct in
// full, rather than real sqlite, which would silently observe BlockReason
// as always "" regardless of what a caller sets. See this task's own
// baocaov6checklist.md section for the full note on this finding.
func TestGetRunGraphAndTimeline_HTTP_RedactBlockReason(t *testing.T) {
	ctx := context.Background()
	uow := fake.New()
	ids := idsource.NewSequential("id")
	root := readyWorkItemFixture(t, uow, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, uow, "project-1", "wf-def-1", "wf-v-1", workflowDocumentV1())
	startCmd := testCommand("idem-start-1", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	startResult, err := runtime.StartWorkflowRun(ctx, uow, ids, startCmd, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun: %v", err)
	}

	const secret = "super-secret-block-reason-token"
	if err := seedBlockedNodeRun(ctx, uow, startResult.RunID, "blocked-node", secret); err != nil {
		t.Fatalf("seed blocked node run: %v", err)
	}

	matcher := redact.NewMatcher(secret)
	server := newTestServer(t, uow, matcher)

	_, graph := getGraph(t, server.URL, startResult.RunID, "")
	foundInGraph := false
	for _, a := range graph.Activations {
		if a.NodeRunID == "blocked-node-run" {
			foundInGraph = true
			if a.BlockReason != "[REDACTED]" {
				t.Fatalf("graph BlockReason = %q, want the redaction placeholder", a.BlockReason)
			}
		}
	}
	if !foundInGraph {
		t.Fatalf("graph.Activations = %+v, missing the hand-seeded blocked node run", graph.Activations)
	}

	_, timeline := getTimeline(t, server.URL, startResult.RunID, "")
	foundInTimeline := false
	for _, e := range timeline.Entries {
		if e.NodeRunID == "blocked-node-run" {
			foundInTimeline = true
			if e.BlockReason != "[REDACTED]" {
				t.Fatalf("timeline BlockReason = %q, want the redaction placeholder", e.BlockReason)
			}
		}
	}
	if !foundInTimeline {
		t.Fatalf("timeline.Entries missing the hand-seeded blocked node run")
	}
}
