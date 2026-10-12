package recovery_test

// V11-10: the HTTP face of RetryWorkItem, RecreateWorkItem and the redo preview. It lives next to the recovery tests because it reuses
// their fixtures (a work item whose run was cancelled and that sits BLOCKED), not because the routes belong to recovery.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/artifactstore"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/app/runtime"
	"github.com/taQuangLing/agent-workflow/internal/app/workitemredo"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	redohttp "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/redo"
)

func newRedoServer(t *testing.T, uow ports.UnitOfWork, ids idsource.Source) *httptest.Server {
	t.Helper()
	store, err := artifactstore.New(t.TempDir())
	if err != nil {
		t.Fatalf("artifactstore.New: %v", err)
	}
	registry := httpapi.NewRouteRegistry()
	redohttp.RegisterRoutes(registry, redohttp.Dependencies{
		UOW: uow,
		Service: workitemredo.Service{RedoDeps: runtime.RedoDeps{
			UnitOfWork: uow, ArtifactStore: store, IDs: ids, Clock: clock.System{}, Matcher: redact.NewMatcher(),
		}},
	})
	mux := http.NewServeMux()
	for _, d := range registry.Descriptors() {
		mux.HandleFunc(d.Method+" "+d.Path, d.Handler)
	}
	server := httptest.NewServer(httpapi.Chain(mux, httpapi.BindPrincipal(testPrincipal())))
	t.Cleanup(server.Close)
	return server
}

func redoRequest(t *testing.T, method, url, key, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func TestRetryWorkItem_HTTP_OneRequestResolvesTheBlockerAndStartsARun(t *testing.T) {
	store, uow := openRecoveryTestStore(t, "redo-retry.db")
	ids := idsource.NewSequential("id")
	workItemID, blockerID := runCancelledBlockerFixture(t, store, uow, ids, "project-1", "repo-1")
	server := newRedoServer(t, uow, ids)
	url := server.URL + "/work-items/" + workItemID + "/retry"

	status, body := redoRequest(t, http.MethodPost, url, "", `{"note":"go again"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("retry without an Idempotency-Key = %d, want 400; body %s", status, body)
	}

	status, body = redoRequest(t, http.MethodPost, url, "key-1", `{"note":"go again"}`)
	if status != http.StatusCreated {
		t.Fatalf("retry = %d, want 201; body %s", status, body)
	}
	var first runtime.RetryWorkItemResult
	if err := json.Unmarshal(body, &first); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if first.WorkItemID != workItemID || first.RunID == "" || first.RunID == first.PreviousRunID || len(first.ResolvedBlockerIDs) != 1 || first.ResolvedBlockerIDs[0] != blockerID || first.NoteMessageID == "" {
		t.Fatalf("result = %+v, want a new run, blocker %s resolved and the note stored", first, blockerID)
	}

	// the same key replays the same answer; the same key with another body is a conflict
	status, again := redoRequest(t, http.MethodPost, url, "key-1", `{"note":"go again"}`)
	if status != http.StatusCreated {
		t.Fatalf("replay = %d; body %s", status, again)
	}
	var replayed runtime.RetryWorkItemResult
	if err := json.Unmarshal(again, &replayed); err != nil || replayed.RunID != first.RunID {
		t.Fatalf("replay = %+v, %v; want run %s", replayed, err, first.RunID)
	}
	if status, body = redoRequest(t, http.MethodPost, url, "key-1", `{"note":"something else"}`); status != http.StatusConflict {
		t.Fatalf("same key, different body = %d, want 409; body %s", status, body)
	}

	// the work item is running again, so a second retry (new key) is a typed refusal
	status, body = redoRequest(t, http.MethodPost, url, "key-2", `{}`)
	if status != http.StatusConflict || !strings.Contains(string(body), "NOT_RETRYABLE") {
		t.Fatalf("retry while running = %d %s, want 409 NOT_RETRYABLE", status, body)
	}
}

func TestRedoRoutes_HTTP_RefusalsAndPreview(t *testing.T) {
	store, uow := openRecoveryTestStore(t, "redo-refusals.db")
	ids := idsource.NewSequential("id")
	workItemID, _ := runCancelledBlockerFixture(t, store, uow, ids, "project-1", "repo-1")
	server := newRedoServer(t, uow, ids)

	status, body := redoRequest(t, http.MethodGet, server.URL+"/work-items/"+workItemID+"/redo-preview", "", "")
	if status != http.StatusOK {
		t.Fatalf("preview = %d; body %s", status, body)
	}
	var preview workitemredo.Preview
	if err := json.Unmarshal(body, &preview); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if !preview.CanRetry || preview.CanRecreate || preview.RecreateRefusal == nil || preview.RecreateRefusal.Code != runtime.RedoRefusalRootUnsupported ||
		len(preview.BlockersToResolve) != 1 || preview.PinnedWorkflowVersion.WorkflowVersionID == "" {
		t.Fatalf("preview = %+v", preview)
	}

	recreate := server.URL + "/work-items/" + workItemID + "/recreate"
	if status, body = redoRequest(t, http.MethodPost, recreate, "key-1", `{}`); status != http.StatusBadRequest {
		t.Fatalf("recreate without a reason = %d, want 400; body %s", status, body)
	}
	status, body = redoRequest(t, http.MethodPost, recreate, "key-2", `{"reason":"kit fixed"}`)
	if status != http.StatusConflict || !strings.Contains(string(body), "ROOT_UNSUPPORTED") {
		t.Fatalf("recreate a root = %d %s, want 409 ROOT_UNSUPPORTED", status, body)
	}
	if status, body = redoRequest(t, http.MethodPost, server.URL+"/work-items/"+workItemID+"/retry", "key-3", `{"outOfScope":"DELETE"}`); status != http.StatusUnprocessableEntity {
		t.Fatalf("retry with an unknown outOfScope = %d, want 422; body %s", status, body)
	}
	if status, body = redoRequest(t, http.MethodPost, server.URL+"/work-items/no-such-item/retry", "key-4", `{}`); status != http.StatusNotFound {
		t.Fatalf("retry of an unknown work item = %d, want 404; body %s", status, body)
	}
	if status, body = redoRequest(t, http.MethodGet, server.URL+"/work-items/no-such-item/redo-preview", "", ""); status != http.StatusNotFound {
		t.Fatalf("preview of an unknown work item = %d, want 404; body %s", status, body)
	}
}
