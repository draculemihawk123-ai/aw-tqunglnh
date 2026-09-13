package catalog_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	appcatalog "github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/domain/project"
)

// registerTestRepository dispatches a real POST /projects/{id}/repositories
// and returns the caller-chosen RepositoryID — shared setup for every
// other repository test in this file. The repository's own "name" reuses
// repositoryID: repositories carries UNIQUE(project_id, name)
// (migrations/0001_initial_schema.sql), so two repositories registered
// under the same project in one test must never share a literal name.
func registerTestRepository(t *testing.T, handler http.Handler, projectID, idempotencyKey, repositoryID string) {
	t.Helper()
	body := `{"repositoryId":"` + repositoryID + `","name":"` + repositoryID + `","remoteLocator":"https://example.invalid/repo.git","defaultRef":"main"}`
	rec := doRequest(handler, http.MethodPost, "/projects/"+projectID+"/repositories", idempotencyKey, "", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /projects/%s/repositories = %d, body %s, want 201", projectID, rec.Code, rec.Body.String())
	}
}

// TestRegisterRepository_ReturnsRegisteringStatusNeverFakedActive is
// V6-03A's own "Thực hiện: register trả REGISTERING" line proven two
// ways at once: the POST response body itself, AND an immediate follow-up
// GET — neither may ever report anything but REGISTERING, since no real
// probe has run in this test (V6-03A's own "Không làm: ... giả sync
// success trước probe").
func TestRegisterRepository_ReturnsRegisteringStatusNeverFakedActive(t *testing.T) {
	handler, _ := newTestHandler(t)
	projectID := createTestProject(t, handler, "key-project", "widget")

	body := `{"repositoryId":"repo-1","name":"svc","remoteLocator":"https://example.invalid/repo.git","defaultRef":"main"}`
	rec := doRequest(handler, http.MethodPost, "/projects/"+projectID+"/repositories", "key-repo", "", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s, want 201", rec.Code, rec.Body.String())
	}
	var result appcatalog.RegisterRepositoryResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode body %s: %v", rec.Body.String(), err)
	}
	if result.Status != string(project.RepositoryRegistering) {
		t.Fatalf("POST response Status = %q, want REGISTERING", result.Status)
	}
	if result.ProbeJobID == "" {
		t.Fatal("result.ProbeJobID is empty, want a freshly enqueued probe job id")
	}

	getRec := doRequest(handler, http.MethodGet, "/repositories/repo-1", "", "", "")
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, body %s, want 200", getRec.Code, getRec.Body.String())
	}
	var view struct {
		Status  string `json:"status"`
		Version uint64 `json:"version"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode GET body %s: %v", getRec.Body.String(), err)
	}
	if view.Status != string(project.RepositoryRegistering) {
		t.Fatalf("GET /repositories/repo-1 Status = %q, want REGISTERING", view.Status)
	}
	if view.Version != 1 {
		t.Fatalf("GET /repositories/repo-1 Version = %d, want 1", view.Version)
	}
	if etag := getRec.Header().Get("ETag"); etag != `"1"` {
		t.Fatalf("ETag = %q, want %q", etag, `"1"`)
	}
}

func TestRegisterRepository_UnknownProject_Returns404(t *testing.T) {
	handler, _ := newTestHandler(t)
	body := `{"repositoryId":"repo-1","name":"svc","remoteLocator":"https://example.invalid/repo.git","defaultRef":"main"}`
	rec := doRequest(handler, http.MethodPost, "/projects/does-not-exist/repositories", "key-repo", "", body)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body %s, want 404 (RegisterRepository's own persistence layer must reject a nonexistent project)", rec.Code, rec.Body.String())
	}
}

func TestListProjectRepositories_ReturnsRegistered(t *testing.T) {
	handler, _ := newTestHandler(t)
	projectID := createTestProject(t, handler, "key-project", "widget")
	registerTestRepository(t, handler, projectID, "key-repo-a", "repo-a")
	registerTestRepository(t, handler, projectID, "key-repo-b", "repo-b")

	rec := doRequest(handler, http.MethodGet, "/projects/"+projectID+"/repositories", "", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s, want 200", rec.Code, rec.Body.String())
	}
	var body struct {
		Repositories []struct {
			ID string `json:"id"`
		} `json:"repositories"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %s: %v", rec.Body.String(), err)
	}
	if len(body.Repositories) != 2 {
		t.Fatalf("repositories = %+v, want exactly 2", body.Repositories)
	}
}

func TestGetRepository_UnknownID_Returns404Hidden(t *testing.T) {
	handler, _ := newTestHandler(t)
	rec := doRequest(handler, http.MethodGet, "/repositories/does-not-exist", "", "", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body %s, want 404", rec.Code, rec.Body.String())
	}
}

func TestGetRepositoryOnboarding_NoProbeYet_ReturnsEmptyAttempts(t *testing.T) {
	handler, _ := newTestHandler(t)
	projectID := createTestProject(t, handler, "key-project", "widget")
	registerTestRepository(t, handler, projectID, "key-repo", "repo-1")

	rec := doRequest(handler, http.MethodGet, "/repositories/repo-1/onboarding", "", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s, want 200", rec.Code, rec.Body.String())
	}
	var body struct {
		RepositoryID string        `json:"repositoryId"`
		Status       string        `json:"status"`
		Attempts     []interface{} `json:"attempts"`
		ValidActions []interface{} `json:"validActions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %s: %v", rec.Body.String(), err)
	}
	if body.RepositoryID != "repo-1" || body.Status != string(project.RepositoryRegistering) {
		t.Fatalf("body = %+v, want RepositoryID=repo-1 Status=REGISTERING", body)
	}
	if len(body.Attempts) != 0 {
		t.Fatalf("attempts = %+v, want empty (no probe has run yet)", body.Attempts)
	}
	if len(body.ValidActions) != 0 {
		t.Fatalf("validActions = %+v, want empty (retry-probe is only advertised for a BLOCKED repository)", body.ValidActions)
	}
}

// TestRetryRepositoryProbe_NotBlocked_Returns409AndNeverDispatches is this
// task's own "handler dispatch spy" Verify bullet, proven against real
// side effects rather than a mock: a REGISTERING repository's own
// Version/Status must be byte-for-byte unchanged after a rejected
// retry-probe call, which is only true if the real
// internal/app/catalog.RetryRepositoryProbe command was never actually
// invoked (it would have CAS'd the row and incremented Version had it
// run).
func TestRetryRepositoryProbe_NotBlocked_Returns409AndNeverDispatches(t *testing.T) {
	handler, uow := newTestHandler(t)
	projectID := createTestProject(t, handler, "key-project", "widget")
	registerTestRepository(t, handler, projectID, "key-repo", "repo-1")

	rec := doRequest(handler, http.MethodPost, "/repositories/repo-1/retry-probe", "key-retry", `"1"`, "{}")
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body %s, want 409 (repository is REGISTERING, not BLOCKED)", rec.Code, rec.Body.String())
	}

	repo, err := appcatalog.GetRepository(context.Background(), uow, "repo-1")
	if err != nil {
		t.Fatalf("GetRepository: %v", err)
	}
	if repo.Status != project.RepositoryRegistering || repo.Version != 1 {
		t.Fatalf("repository = %+v, want unchanged Status=REGISTERING Version=1 (the rejected call must never have dispatched the real command)", repo)
	}
}

func TestRetryRepositoryProbe_MissingIfMatch_Returns400(t *testing.T) {
	handler, _ := newTestHandler(t)
	projectID := createTestProject(t, handler, "key-project", "widget")
	registerTestRepository(t, handler, projectID, "key-repo", "repo-1")

	rec := doRequest(handler, http.MethodPost, "/repositories/repo-1/retry-probe", "key-retry", "", "{}")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body %s, want 400", rec.Code, rec.Body.String())
	}
}

// TestRetryRepositoryProbe_Blocked_TransitionsToProbingAcceptedWithNewETag
// proves the accepted, real-dispatch path: a genuinely BLOCKED repository
// transitions to PROBING, a fresh probe job is enqueued, and the response
// carries the deterministically-computed new ETag (expectedVersion+1).
func TestRetryRepositoryProbe_Blocked_TransitionsToProbingAcceptedWithNewETag(t *testing.T) {
	handler, uow := newTestHandler(t)
	projectID := createTestProject(t, handler, "key-project", "widget")
	registerTestRepository(t, handler, projectID, "key-repo", "repo-1")
	version := moveRepositoryToBlocked(t, uow, "repo-1")

	rec := doRequest(handler, http.MethodPost, "/repositories/repo-1/retry-probe", "key-retry", `"3"`, "{}")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body %s, want 202", rec.Code, rec.Body.String())
	}
	var result appcatalog.RetryRepositoryProbeResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode body %s: %v", rec.Body.String(), err)
	}
	if result.Status != string(project.RepositoryProbing) {
		t.Fatalf("result.Status = %q, want PROBING", result.Status)
	}
	if etag := rec.Header().Get("ETag"); etag != `"4"` {
		t.Fatalf("ETag = %q, want %q (version %d + 1)", etag, `"4"`, version)
	}

	repo, err := appcatalog.GetRepository(context.Background(), uow, "repo-1")
	if err != nil {
		t.Fatalf("GetRepository: %v", err)
	}
	if repo.Status != project.RepositoryProbing || repo.Version != 4 {
		t.Fatalf("repository = %+v, want Status=PROBING Version=4", repo)
	}
}

// TestRetryRepositoryProbe_ReplaySameKey_WinsOverStateDrift is V6-02's own
// "Same-key committed replay thắng ETag/state drift nhưng vẫn phải qua
// current authorization" line, proven against this exact route: the FIRST
// retry-probe call (BLOCKED->PROBING) succeeds; a SECOND call with the
// identical Idempotency-Key — now that the repository has already moved
// past BLOCKED — must still replay the original accepted result, never
// reject with "not BLOCKED". This is only true because
// repository.go's own retryRepositoryProbe checks the receipt (via
// beginMutation) strictly BEFORE checking the BLOCKED precondition — see
// that function's own doc comment for exactly why the ordering is
// deliberate, not incidental.
func TestRetryRepositoryProbe_ReplaySameKey_WinsOverStateDrift(t *testing.T) {
	handler, uow := newTestHandler(t)
	projectID := createTestProject(t, handler, "key-project", "widget")
	registerTestRepository(t, handler, projectID, "key-repo", "repo-1")
	moveRepositoryToBlocked(t, uow, "repo-1")

	first := doRequest(handler, http.MethodPost, "/repositories/repo-1/retry-probe", "key-retry", `"3"`, "{}")
	if first.Code != http.StatusAccepted {
		t.Fatalf("first status = %d, body %s, want 202", first.Code, first.Body.String())
	}
	firstJobID := jsonField(t, first.Body.String(), "probeJobId")

	// The repository is now PROBING (not BLOCKED); a genuinely new
	// retry-probe attempt against it would 409, per
	// TestRetryRepositoryProbe_NotBlocked_Returns409AndNeverDispatches
	// above. The SAME idempotency key must not hit that path.
	second := doRequest(handler, http.MethodPost, "/repositories/repo-1/retry-probe", "key-retry", `"3"`, "{}")
	if second.Code != http.StatusOK {
		t.Fatalf("replay status = %d, body %s, want 200 (WriteReceiptReplay always writes 200, never the 409 a fresh attempt would now get)", second.Code, second.Body.String())
	}
	secondJobID := jsonField(t, second.Body.String(), "probeJobId")
	if secondJobID != firstJobID {
		t.Fatalf("replay probeJobId = %q, want the exact original %q", secondJobID, firstJobID)
	}

	repo, err := appcatalog.GetRepository(context.Background(), uow, "repo-1")
	if err != nil {
		t.Fatalf("GetRepository: %v", err)
	}
	if repo.Version != 4 {
		t.Fatalf("repository Version = %d, want 4 (the replay must never re-run the CAS a second time)", repo.Version)
	}
}
