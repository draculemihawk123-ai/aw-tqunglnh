package run_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/run"
)

// driftAuditor reports that every worktree HEAD is `head`, with the commits
// between the pinned revision and it.
type driftAuditor struct {
	head        string
	fastForward bool
	commits     []ports.HeadAuditCommit
}

func (d driftAuditor) AuditHead(_ context.Context, req ports.AuditHeadRequest) (ports.HeadAudit, error) {
	return ports.HeadAudit{Head: d.head, Pinned: req.PinnedRevision, FastForward: d.fastForward, TotalCommits: len(d.commits), Commits: d.commits}, nil
}

func newTestServerWithAuditor(t *testing.T, uow ports.UnitOfWork, ids idsource.Source, auditor ports.WorkspaceHeadAuditor) *httptest.Server {
	t.Helper()
	registry := httpapi.NewRouteRegistry()
	run.RegisterRoutes(registry, run.Dependencies{UOW: uow, IDs: ids, HeadAuditor: auditor})
	mux := http.NewServeMux()
	for _, d := range registry.Descriptors() {
		mux.HandleFunc(d.Method+" "+d.Path, d.Handler)
	}
	server := httptest.NewServer(httpapi.Chain(mux, httpapi.BindPrincipal(testPrincipal())))
	t.Cleanup(server.Close)
	return server
}

// V11-01: a worktree HEAD that is not the revision aw recorded is refused at
// start, with the cause and the command that resolves it, instead of failing at
// the run's first machine gate.
func TestStartWorkflowRun_HTTP_RefusesWhenAWorktreeHeadDrifted(t *testing.T) {
	_, uow := openRunTestStore(t, "start-head-drift.db")
	ids := idsource.NewSequential("id")
	root := readyWorkItemFixture(t, uow, ids, "project-1", "repo-1")
	version := publishTestWorkflowVersion(t, uow, "project-1", "wf-def-1", "wf-v-1")
	server := newTestServerWithAuditor(t, uow, ids, driftAuditor{
		head: strings.Repeat("b", 40), fastForward: true,
		commits: []ports.HeadAuditCommit{{CommitID: strings.Repeat("b", 40), AuthorName: "Ide User", Subject: "by hand"}},
	})

	resp := postStartRun(t, server.URL, root.WorkItemID, "idem-drift", `{"workflowVersionId":"`+string(version.ID())+`"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body: %s", resp.StatusCode, mustReadAll(t, resp.Body))
	}
	message := decodeErrorResponse(t, resp).Error.Message
	for _, want := range []string{"WORKSPACE_HEAD_DRIFT", "repo-1", "1 commit(s) made outside aw", "aw repository-workspace adopt-head", "--project-id project-1"} {
		if !strings.Contains(message, want) {
			t.Fatalf("message %q does not contain %q", message, want)
		}
	}

	// Nothing was started: the work item is still READY and a run can start once HEAD is as recorded.
	clean := newTestServerWithAuditor(t, uow, ids, driftAuditor{head: "cafebabecafebabecafebabecafebabecafebabe"})
	resp = postStartRun(t, clean.URL, root.WorkItemID, "idem-clean", `{"workflowVersionId":"`+string(version.ID())+`"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("after HEAD matches: status = %d, want 201; body: %s", resp.StatusCode, mustReadAll(t, resp.Body))
	}
}
