package recovery_test

// V11-11: the HTTP face of getNodeRunDetail and listAttemptEvents. It lives next to the recovery tests because it reuses their fixtures
// (a started run), not because the routes belong to recovery.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	runtimeapp "github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/rundetail"
)

func TestNodeRunAndAttemptEvents_HTTP(t *testing.T) {
	_, uow := openRecoveryTestStore(t, "node-detail.db")
	ids := idsource.NewSequential("id")
	started := startedRunFixture(t, uow, ids, "project-1", "repo-1")

	registry := httpapi.NewRouteRegistry()
	rundetail.RegisterRoutes(registry, rundetail.Dependencies{UnitOfWork: uow, Matcher: redact.NewMatcher()})
	mux := http.NewServeMux()
	for _, d := range registry.Descriptors() {
		mux.HandleFunc(d.Method+" "+d.Path, d.Handler)
	}
	server := httptest.NewServer(httpapi.Chain(mux, httpapi.BindPrincipal(testPrincipal())))
	defer server.Close()

	get := func(path string) (int, []byte) {
		resp, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		return resp.StatusCode, []byte(mustReadAll(t, resp.Body))
	}

	status, body := get("/node-runs/" + started.NodeRunID)
	if status != http.StatusOK {
		t.Fatalf("node run detail = %d; body %s", status, body)
	}
	var detail runtimeapp.NodeRunDetail
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if detail.NodeRunID != started.NodeRunID || detail.RunID != started.RunID || detail.Result.Kind != "STRUCTURAL" || detail.Result.Note == "" {
		t.Fatalf("detail = %+v, want the START node as a structural one-liner", detail)
	}

	if status, body = get("/node-runs/no-such-node-run"); status != http.StatusNotFound {
		t.Fatalf("unknown node run = %d, want 404; body %s", status, body)
	}
	if status, body = get("/execution-attempts/no-such-attempt/events"); status != http.StatusNotFound {
		t.Fatalf("unknown attempt = %d, want 404; body %s", status, body)
	}
	if status, body = get("/execution-attempts/no-such-attempt/events?cursor=abc"); status != http.StatusBadRequest {
		t.Fatalf("a malformed cursor = %d, want 400; body %s", status, body)
	}
	if status, body = get("/execution-attempts/no-such-attempt/events?limit=0"); status != http.StatusBadRequest {
		t.Fatalf("a non-positive limit = %d, want 400; body %s", status, body)
	}
}
