package rundetail_test

// Real HTTP round-trip coverage for GET /runs/{id} (V6-06B). Every test
// drives a REAL httptest.Server backed by a REAL *sqlite.Store, seeded
// through the REAL internal/app/runtime.StartWorkflowRun/AdvanceRun
// commands — never a hand-seeded row.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	runtimedomain "github.com/taQuangLing/agent-workflow/internal/domain/runtime"
)

func TestGetRunDetail_HTTP_NotFound(t *testing.T) {
	_, uow := openRunDetailTestStore(t, "detail-notfound.db")
	server := newTestServer(t, uow, redact.Matcher{})

	resp, err := http.Get(server.URL + "/runs/unknown-run")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestGetRunDetail_HTTP_ReturnsDetail(t *testing.T) {
	ctx := context.Background()
	_, uow := openRunDetailTestStore(t, "detail-basic.db")
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
	resp, err := http.Get(server.URL + "/runs/" + startResult.RunID)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body=%s", resp.StatusCode, body)
	}
	var detail runtime.RunDetail
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if detail.RunID != startResult.RunID || detail.ProjectID != "project-1" || detail.WorkItemID != root.WorkItemID {
		t.Fatalf("detail identities = %+v, want to match the started run", detail)
	}
	if detail.State != string(runtimedomain.WorkflowRunRunning) {
		t.Fatalf("detail.State = %s, want RUNNING", detail.State)
	}
	if detail.WorkflowVersionID != string(version.ID()) {
		t.Fatalf("detail.WorkflowVersionID = %s, want %s", detail.WorkflowVersionID, version.ID())
	}
	if detail.Manifest.WorkflowVersionID != string(version.ID()) || detail.Manifest.CompiledSnapshotHash == "" {
		t.Fatalf("detail.Manifest = %+v, want a real pinned WorkflowVersionID/CompiledSnapshotHash", detail.Manifest)
	}
	if detail.NodeRunCount != 1 {
		t.Fatalf("detail.NodeRunCount = %d, want 1 (only START activated so far)", detail.NodeRunCount)
	}
	if len(detail.Amendments) != 0 {
		t.Fatalf("detail.Amendments = %+v, want none", detail.Amendments)
	}
}
