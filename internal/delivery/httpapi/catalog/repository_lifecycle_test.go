package catalog_test

// V11-12 (ADR-043): correct, retire and remove a repository over HTTP, and check a path before registering it.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/apperror"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

type stubProber struct {
	evidence ports.RepositoryProbeEvidence
	err      error
}

func (p stubProber) Probe(_ context.Context, _ string, _ string) (ports.RepositoryProbeEvidence, error) {
	return p.evidence, p.err
}

func errorDetails(t *testing.T, body string) string {
	t.Helper()
	var envelope struct {
		Error struct {
			Details []struct {
				Field   string `json:"field"`
				Message string `json:"message"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("decode error body %s: %v", body, err)
	}
	var out []string
	for _, d := range envelope.Error.Details {
		out = append(out, d.Field+"="+d.Message)
	}
	return strings.Join(out, "|")
}

func TestUpdateRepository_CorrectsABlockedRegistrationOverHTTP(t *testing.T) {
	handler, uow := newTestHandler(t)
	projectID := createTestProject(t, handler, "key-project", "widget")
	registerTestRepository(t, handler, projectID, "key-repo", "repo-1")

	// not BLOCKED yet: a typed refusal, nothing changed
	rec := doRequest(handler, http.MethodPatch, "/repositories/repo-1", "key-upd-early", `"1"`, `{"remoteLocator":"D:/right"}`)
	if rec.Code != http.StatusConflict || !strings.Contains(errorDetails(t, rec.Body.String()), "repository=REPOSITORY_NOT_BLOCKED") {
		t.Fatalf("update of a REGISTERING repository = %d %s; want 409 REPOSITORY_NOT_BLOCKED", rec.Code, rec.Body.String())
	}

	moveRepositoryToBlocked(t, uow, "repo-1")
	if rec := doRequest(handler, http.MethodPatch, "/repositories/repo-1", "key-upd-nomatch", "", `{"remoteLocator":"D:/right"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("update without If-Match = %d, want 400", rec.Code)
	}
	rec = doRequest(handler, http.MethodPatch, "/repositories/repo-1", "key-upd", `"3"`, `{"remoteLocator":"D:/right","defaultRef":"develop"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("PATCH = %d %s; want 202", rec.Code, rec.Body.String())
	}
	if status := jsonField(t, rec.Body.String(), "status"); status != "PROBING" {
		t.Fatalf("status = %q, want PROBING (%s)", status, rec.Body.String())
	}
	if jsonField(t, rec.Body.String(), "probeJobId") == "" || rec.Header().Get("ETag") != `"4"` {
		t.Fatalf("response %s etag %s; want a probe job and ETag 4", rec.Body.String(), rec.Header().Get("ETag"))
	}
	get := doRequest(handler, http.MethodGet, "/repositories/repo-1", "", "", "")
	if jsonField(t, get.Body.String(), "remoteLocator") != "D:/right" || jsonField(t, get.Body.String(), "defaultRef") != "develop" || jsonField(t, get.Body.String(), "name") != "repo-1" {
		t.Fatalf("repository after update = %s", get.Body.String())
	}
	// the same key replays the same answer
	again := doRequest(handler, http.MethodPatch, "/repositories/repo-1", "key-upd", `"3"`, `{"remoteLocator":"D:/right","defaultRef":"develop"}`)
	if again.Body.String() != rec.Body.String() {
		t.Fatalf("replay = %d %s; want the original result %s", again.Code, again.Body.String(), rec.Body.String())
	}
}

func TestDisableRepository_HidesItFromTheDefaultListAndFreesTheName(t *testing.T) {
	handler, uow := newTestHandler(t)
	projectID := createTestProject(t, handler, "key-project", "widget")
	registerTestRepository(t, handler, projectID, "key-repo", "repo-1")
	moveRepositoryToBlocked(t, uow, "repo-1")

	rec := doRequest(handler, http.MethodPost, "/repositories/repo-1/disable", "key-dis", `"3"`, `{"reason":"wrong folder"}`)
	if rec.Code != http.StatusOK || jsonField(t, rec.Body.String(), "status") != "DISABLED" {
		t.Fatalf("disable = %d %s; want 200 DISABLED", rec.Code, rec.Body.String())
	}
	if rec := doRequest(handler, http.MethodPost, "/repositories/repo-1/disable", "key-dis-2", `"4"`, `{}`); rec.Code != http.StatusConflict ||
		!strings.Contains(errorDetails(t, rec.Body.String()), "REPOSITORY_ALREADY_DISABLED") {
		t.Fatalf("second disable = %d %s; want 409 REPOSITORY_ALREADY_DISABLED", rec.Code, rec.Body.String())
	}

	list := func(query string) int {
		rec := doRequest(handler, http.MethodGet, "/projects/"+projectID+"/repositories"+query, "", "", "")
		var body struct {
			Repositories []struct {
				ID string `json:"id"`
			} `json:"repositories"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %s: %v", rec.Body.String(), err)
		}
		return len(body.Repositories)
	}
	if n := list(""); n != 0 {
		t.Fatalf("default list shows %d repositories, want the disabled one hidden", n)
	}
	if n := list("?includeDisabled=true"); n != 1 {
		t.Fatalf("includeDisabled list shows %d repositories, want 1", n)
	}
	// the name is free again
	body := `{"repositoryId":"repo-2","name":"repo-1","remoteLocator":"D:/x","defaultRef":"main"}`
	if rec := doRequest(handler, http.MethodPost, "/projects/"+projectID+"/repositories", "key-repo-2", "", body); rec.Code != http.StatusCreated {
		t.Fatalf("re-register under the freed name = %d %s; want 201", rec.Code, rec.Body.String())
	}
}

func TestDeleteRepository_RemovesItForGoodAndRetiresTheId(t *testing.T) {
	handler, uow := newTestHandler(t)
	projectID := createTestProject(t, handler, "key-project", "widget")
	registerTestRepository(t, handler, projectID, "key-repo", "repo-1")
	moveRepositoryToBlocked(t, uow, "repo-1")

	if rec := doRequest(handler, http.MethodDelete, "/repositories/repo-1", "key-del-noreason", `"3"`, `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("delete without a reason = %d %s; want 400", rec.Code, rec.Body.String())
	}
	rec := doRequest(handler, http.MethodDelete, "/repositories/repo-1", "key-del", `"3"`, `{"reason":"typo in the path"}`)
	if rec.Code != http.StatusOK || jsonField(t, rec.Body.String(), "repositoryId") != "repo-1" {
		t.Fatalf("delete = %d %s; want 200", rec.Code, rec.Body.String())
	}
	if rec := doRequest(handler, http.MethodGet, "/repositories/repo-1", "", "", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("GET after delete = %d, want 404", rec.Code)
	}
	body := `{"repositoryId":"repo-1","name":"fresh","remoteLocator":"D:/x","defaultRef":"main"}`
	rec = doRequest(handler, http.MethodPost, "/projects/"+projectID+"/repositories", "key-reuse", "", body)
	if rec.Code != http.StatusConflict || !strings.Contains(errorDetails(t, rec.Body.String()), "REPOSITORY_DELETED") {
		t.Fatalf("registering a deleted id = %d %s; want 409 REPOSITORY_DELETED", rec.Code, rec.Body.String())
	}
}

func TestRepositoryView_BlockedOffersRetryUpdateDisableAndDelete(t *testing.T) {
	handler, uow := newTestHandler(t)
	projectID := createTestProject(t, handler, "key-project", "widget")
	registerTestRepository(t, handler, projectID, "key-repo", "repo-1")
	moveRepositoryToBlocked(t, uow, "repo-1")
	get := doRequest(handler, http.MethodGet, "/repositories/repo-1", "", "", "")
	var view struct {
		ValidActions []struct {
			OperationID string `json:"operationId"`
		} `json:"validActions"`
	}
	if err := json.Unmarshal(get.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	var ops []string
	for _, a := range view.ValidActions {
		ops = append(ops, a.OperationID)
	}
	if got := strings.Join(ops, ","); got != "repositoriesRetryProbe,repositoriesUpdate,repositoriesDisable,repositoriesDelete" {
		t.Fatalf("validActions = %s; want retry, update, disable and delete for a BLOCKED repository that never was ACTIVE", got)
	}
}

func TestCheckRepository_ReportsTheProblemWithoutRegisteringAnything(t *testing.T) {
	failure := apperror.New(apperror.CodeInvalidArgument, "The folder D:/bpm-gl is not a Git repository yet.", false).WithDetails(map[string]string{
		ports.ProbeDetailFailureKind: "NOT_A_GIT_REPOSITORY", ports.ProbeDetailPath: "D:/bpm-gl", ports.ProbeDetailDetail: "fatal: not a git repository",
		ports.ProbeDetailHint: "Run: git -C D:/bpm-gl init -b main",
	})
	handler, _ := newTestHandlerWithProber(t, stubProber{err: failure})
	projectID := createTestProject(t, handler, "key-project", "widget")

	rec := doRequest(handler, http.MethodGet, "/projects/"+projectID+"/repositories/check?path=D:/bpm-gl", "", "", "")
	if rec.Code != http.StatusOK || jsonField(t, rec.Body.String(), "failureKind") != "NOT_A_GIT_REPOSITORY" ||
		!strings.Contains(jsonField(t, rec.Body.String(), "hint"), "init -b main") || !strings.Contains(rec.Body.String(), `"ok":false`) {
		t.Fatalf("check = %d %s", rec.Code, rec.Body.String())
	}
	list := doRequest(handler, http.MethodGet, "/projects/"+projectID+"/repositories?includeDisabled=true", "", "", "")
	if !strings.Contains(list.Body.String(), `"repositories":[]`) {
		t.Fatalf("a check registered something: %s", list.Body.String())
	}
	if rec := doRequest(handler, http.MethodGet, "/projects/"+projectID+"/repositories/check", "", "", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("check without a path = %d, want 400", rec.Code)
	}
	if rec := doRequest(handler, http.MethodGet, "/projects/nope/repositories/check?path=x", "", "", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("check in an unknown project = %d, want 404", rec.Code)
	}
}

func TestCheckRepository_OKCarriesWhatTheProbeFound(t *testing.T) {
	handler, _ := newTestHandlerWithProber(t, stubProber{evidence: ports.RepositoryProbeEvidence{
		CanonicalPath: "D:/app", BaseCommit: "abc123", Dirty: true, Components: []ports.ProbedComponent{{Name: "web", Path: "web", Kind: "DIRECTORY"}},
	}})
	projectID := createTestProject(t, handler, "key-project", "widget")
	rec := doRequest(handler, http.MethodGet, "/projects/"+projectID+"/repositories/check?path=D:/app&ref=main", "", "", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":true`) || jsonField(t, rec.Body.String(), "baseCommit") != "abc123" || !strings.Contains(rec.Body.String(), `"web"`) {
		t.Fatalf("check = %d %s", rec.Code, rec.Body.String())
	}
}

func TestCheckRepository_WithoutAProberIsUnavailable(t *testing.T) {
	handler, _ := newTestHandler(t)
	projectID := createTestProject(t, handler, "key-project", "widget")
	if rec := doRequest(handler, http.MethodGet, "/projects/"+projectID+"/repositories/check?path=D:/app", "", "", ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("check without a prober = %d, want 503", rec.Code)
	}
}
