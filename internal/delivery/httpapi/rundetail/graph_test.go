package rundetail_test

// Real HTTP round-trip coverage for GET /runs/{id}/graph (V6-06B).

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
)

type graphHTTPResponse struct {
	RunID            string                       `json:"runId"`
	ManifestRevision uint64                       `json:"manifestRevision"`
	Nodes            []runtime.GraphNodeView      `json:"nodes"`
	PossibleEdges    []runtime.GraphEdgeView      `json:"possibleEdges"`
	Activations      []runtime.NodeActivationView `json:"activations"`
	BranchTokens     []runtime.BranchTokenView    `json:"branchTokens"`
	Freshness        struct {
		Generation          int    `json:"generation"`
		AsOfJournalPosition int64  `json:"asOfJournalPosition"`
		Status              string `json:"status"`
	} `json:"freshness"`
	NextCursor string `json:"nextCursor"`
}

func getGraph(t *testing.T, baseURL, runID, query string) (int, graphHTTPResponse) {
	t.Helper()
	u := baseURL + "/runs/" + runID + "/graph"
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
	var out graphHTTPResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp.StatusCode, out
}

func TestGetRunGraph_HTTP_NotFound(t *testing.T) {
	_, uow := openRunDetailTestStore(t, "graph-notfound.db")
	server := newTestServer(t, uow, redact.Matcher{})

	resp, err := http.Get(server.URL + "/runs/unknown-run/graph")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestGetRunGraph_HTTP_ReturnsStructureAndFreshness(t *testing.T) {
	ctx := context.Background()
	_, uow := openRunDetailTestStore(t, "graph-basic.db")
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
	if _, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: startResult.NodeRunID}); err != nil {
		t.Fatalf("AdvanceRun: %v", err)
	}

	server := newTestServer(t, uow, redact.Matcher{})
	_, graph := getGraph(t, server.URL, startResult.RunID, "")

	if graph.RunID != startResult.RunID {
		t.Fatalf("graph.RunID = %s, want %s", graph.RunID, startResult.RunID)
	}
	nodeKeys := map[string]bool{}
	for _, n := range graph.Nodes {
		nodeKeys[n.Key] = true
	}
	if !nodeKeys["start"] || !nodeKeys["end"] {
		t.Fatalf("graph.Nodes = %+v, missing start/end", graph.Nodes)
	}
	foundEdge := false
	for _, e := range graph.PossibleEdges {
		if e.Key == "start-to-end" {
			foundEdge = true
		}
	}
	if !foundEdge {
		t.Fatalf("graph.PossibleEdges = %+v, missing start-to-end", graph.PossibleEdges)
	}
	if len(graph.Activations) != 2 {
		t.Fatalf("graph.Activations = %+v, want exactly 2 (start, end)", graph.Activations)
	}
	if graph.Freshness.Status != "LIVE" {
		t.Fatalf("graph.Freshness.Status = %s, want LIVE", graph.Freshness.Status)
	}
	if graph.Freshness.AsOfJournalPosition != 2 {
		t.Fatalf("graph.Freshness.AsOfJournalPosition = %d, want 2 (highest ActivationSequence)", graph.Freshness.AsOfJournalPosition)
	}
	if graph.NextCursor != "" {
		t.Fatalf("graph.NextCursor = %q, want empty (default limit covers both activations)", graph.NextCursor)
	}
}

// TestGetRunGraph_HTTP_PagesAndStaysStableAcrossConcurrentWrite is this
// task's own "paging qua write" Verify bullet: a NodeRun created AFTER a
// walk's first page was issued must never surface partway through that
// SAME walk, but IS visible to a fresh, cursor-less request issued
// afterward — mirrors internal/delivery/httpapi/message's own
// TestListMessages_PagesAndStaysStableAcrossConcurrentWrite exactly, using
// routerChainDocument's own multi-hop shape so each "page" boundary lines
// up with a real, separately-created NodeRun.
func TestGetRunGraph_HTTP_PagesAndStaysStableAcrossConcurrentWrite(t *testing.T) {
	ctx := context.Background()
	_, uow := openRunDetailTestStore(t, "graph-paging.db")
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
	// start -> router1 (2 activations so far: start, router1).
	hop1, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: startResult.NodeRunID})
	if err != nil {
		t.Fatalf("AdvanceRun (start->router1): %v", err)
	}

	server := newTestServer(t, uow, redact.Matcher{})

	_, page1 := getGraph(t, server.URL, startResult.RunID, "limit=1")
	if len(page1.Activations) != 1 || page1.Activations[0].NodeKey != "start" || page1.NextCursor == "" {
		t.Fatalf("page1 = %+v, want exactly [start] with a NextCursor", page1)
	}

	// A concurrent write lands BETWEEN page 1 and page 2 of the SAME walk:
	// router1 -> router2 (a genuinely new NodeRun, created after page1's
	// own UpperWatermark was pinned).
	if _, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: startResult.RunID, NodeRunID: hop1.NextNodeRunID}); err != nil {
		t.Fatalf("AdvanceRun (router1->router2): %v", err)
	}

	_, page2 := getGraph(t, server.URL, startResult.RunID, "limit=1&cursor="+url.QueryEscape(page1.NextCursor))
	if len(page2.Activations) != 1 || page2.Activations[0].NodeKey != "router1" {
		t.Fatalf("page2 = %+v, want exactly [router1]", page2)
	}
	if page2.NextCursor != "" {
		t.Fatalf("page2.NextCursor = %q, want empty — the walk begun before router2 was created must never surface it", page2.NextCursor)
	}

	_, fresh := getGraph(t, server.URL, startResult.RunID, "")
	if len(fresh.Activations) != 3 {
		t.Fatalf("fresh.Activations len = %d, want 3 (start, router1, router2 — a fresh cursor-less request DOES see the concurrent write)", len(fresh.Activations))
	}
}

func TestGetRunGraph_HTTP_InvalidCursor_Returns400(t *testing.T) {
	ctx := context.Background()
	_, uow := openRunDetailTestStore(t, "graph-badcursor.db")
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
	resp, err := http.Get(server.URL + "/runs/" + startResult.RunID + "/graph?cursor=not-a-real-token")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// TestGetRunGraph_HTTP_CursorFromAnotherRun_ResyncRequired proves
// cursor.go's own Bind/QUERY_CHANGED resync path is reachable through this
// route: a valid, correctly-signed cursor minted for one Run's own walk
// must never silently resume against a different Run's own graph.
func TestGetRunGraph_HTTP_CursorFromAnotherRun_ResyncRequired(t *testing.T) {
	ctx := context.Background()
	_, uow := openRunDetailTestStore(t, "graph-crossrun.db")
	ids := idsource.NewSequential("id")
	root := readyWorkItemFixture(t, uow, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, uow, "project-1", "wf-def-1", "wf-v-1", routerChainDocument())

	startA := testCommand("idem-start-a", "hash-a", ports.ProjectScope("project-1"), "StartWorkflowRun")
	runA, err := runtime.StartWorkflowRun(ctx, uow, ids, startA, runtime.StartWorkflowRunRequest{
		ProjectID: "project-1", WorkItemID: root.WorkItemID, WorkflowVersionID: string(version.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun A: %v", err)
	}
	if _, err := runtime.AdvanceRun(ctx, uow, ids, runtime.AdvanceRunRequest{RunID: runA.RunID, NodeRunID: runA.NodeRunID}); err != nil {
		t.Fatalf("AdvanceRun A: %v", err)
	}

	server := newTestServer(t, uow, redact.Matcher{})
	_, page1 := getGraph(t, server.URL, runA.RunID, "limit=1")
	if page1.NextCursor == "" {
		t.Fatalf("page1 = %+v, want a NextCursor", page1)
	}

	// Run B lives in a SEPARATE project — its own WorkflowVersion, since
	// CreateWorkflowRun requires a Run's pinned WorkflowVersion to belong to
	// that Run's own Project (or be installation-shared); reusing project-1's
	// own version would itself be rejected as a cross-project reference.
	root2 := readyWorkItemFixture(t, uow, ids, "project-2", "repo-2")
	version2 := publishTestWorkflowVersion(t, uow, "project-2", "wf-def-2", "wf-v-2", routerChainDocument())
	startB := testCommand("idem-start-b", "hash-b", ports.ProjectScope("project-2"), "StartWorkflowRun")
	runB, err := runtime.StartWorkflowRun(ctx, uow, ids, startB, runtime.StartWorkflowRunRequest{
		ProjectID: "project-2", WorkItemID: root2.WorkItemID, WorkflowVersionID: string(version2.ID()),
	})
	if err != nil {
		t.Fatalf("StartWorkflowRun B: %v", err)
	}

	resp, err := http.Get(server.URL + "/runs/" + runB.RunID + "/graph?limit=1&cursor=" + url.QueryEscape(page1.NextCursor))
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 409 RESYNC_REQUIRED, body=%s", resp.StatusCode, body)
	}
}
