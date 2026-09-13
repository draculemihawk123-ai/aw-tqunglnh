package catalog_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	appcatalog "github.com/taQuangLing/agent-workflow/internal/app/catalog"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
)

func TestCreateProject_ReturnsCreatedResourceWithGeneratedID(t *testing.T) {
	handler, _ := newTestHandler(t)
	rec := doRequest(handler, http.MethodPost, "/projects", "key-1", "", `{"name":"widget"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s, want 201", rec.Code, rec.Body.String())
	}
	var result appcatalog.CreateProjectResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode body %s: %v", rec.Body.String(), err)
	}
	if result.ProjectID == "" {
		t.Fatal("result.ProjectID is empty, want a generated ID")
	}
	if result.Name != "widget" || result.Status != "ACTIVE" {
		t.Fatalf("result = %+v, want Name=widget Status=ACTIVE", result)
	}
	if etag := rec.Header().Get(http.CanonicalHeaderKey("ETag")); etag != "" {
		t.Fatalf("ETag header = %q, want none (CreateProjectResult carries no Version to encode)", etag)
	}
}

func TestCreateProject_MissingIdempotencyKey_Returns400(t *testing.T) {
	handler, _ := newTestHandler(t)
	rec := doRequest(handler, http.MethodPost, "/projects", "", "", `{"name":"widget"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body %s, want 400", rec.Code, rec.Body.String())
	}
}

// TestCreateProject_ReplaySameKey_ReturnsExactOriginalProjectID proves a
// real HTTP retry (same Idempotency-Key, same body) is a true replay end
// to end through this package's own routes — not just at the
// internal/app/catalog layer receiptreplay_test.go already proves.
func TestCreateProject_ReplaySameKey_ReturnsExactOriginalProjectID(t *testing.T) {
	handler, uow := newTestHandler(t)
	first := doRequest(handler, http.MethodPost, "/projects", "key-1", "", `{"name":"widget"}`)
	if first.Code != http.StatusCreated {
		t.Fatalf("first status = %d, body %s, want 201", first.Code, first.Body.String())
	}
	firstID := jsonField(t, first.Body.String(), "projectId")

	second := doRequest(handler, http.MethodPost, "/projects", "key-1", "", `{"name":"widget"}`)
	if second.Code != http.StatusOK {
		t.Fatalf("replay status = %d, body %s, want 200 (WriteReceiptReplay always writes 200)", second.Code, second.Body.String())
	}
	secondID := jsonField(t, second.Body.String(), "projectId")
	if secondID != firstID {
		t.Fatalf("replay projectId = %q, want the exact original %q", secondID, firstID)
	}

	projects, err := appcatalog.ListProjects(context.Background(), uow, ports.InstallationScope())
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("project rows = %d, want exactly 1 (replay must never create a second row)", len(projects))
	}
}

func TestCreateProject_SameKeyDifferentBody_Returns409(t *testing.T) {
	handler, _ := newTestHandler(t)
	first := doRequest(handler, http.MethodPost, "/projects", "key-1", "", `{"name":"widget"}`)
	if first.Code != http.StatusCreated {
		t.Fatalf("first status = %d, body %s, want 201", first.Code, first.Body.String())
	}
	second := doRequest(handler, http.MethodPost, "/projects", "key-1", "", `{"name":"gadget"}`)
	if second.Code != http.StatusConflict {
		t.Fatalf("second status = %d, body %s, want 409", second.Code, second.Body.String())
	}
}

func TestListProjects_ReturnsEveryCreatedProject(t *testing.T) {
	handler, _ := newTestHandler(t)
	idA := createTestProject(t, handler, "key-a", "widget")
	idB := createTestProject(t, handler, "key-b", "gadget")

	rec := doRequest(handler, http.MethodGet, "/projects", "", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s, want 200", rec.Code, rec.Body.String())
	}
	var body struct {
		Projects []struct {
			ID string `json:"id"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %s: %v", rec.Body.String(), err)
	}
	if len(body.Projects) != 2 {
		t.Fatalf("projects = %+v, want exactly 2", body.Projects)
	}
	seen := map[string]bool{}
	for _, p := range body.Projects {
		seen[p.ID] = true
	}
	if !seen[idA] || !seen[idB] {
		t.Fatalf("projects = %+v, want both %q and %q", body.Projects, idA, idB)
	}
}

func TestGetProject_ReturnsAuthoritativeDetail(t *testing.T) {
	handler, _ := newTestHandler(t)
	id := createTestProject(t, handler, "key-1", "widget")

	rec := doRequest(handler, http.MethodGet, "/projects/"+id, "", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s, want 200", rec.Code, rec.Body.String())
	}
	var body struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Status  string `json:"status"`
		Version uint64 `json:"version"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %s: %v", rec.Body.String(), err)
	}
	if body.ID != id || body.Name != "widget" || body.Status != "ACTIVE" || body.Version != 1 {
		t.Fatalf("body = %+v, want ID=%s Name=widget Status=ACTIVE Version=1", body, id)
	}
}

// TestGetProject_UnknownID_Returns404Hidden proves the leakage-
// normalization policy (errors.go's own WriteResourceHidden) end to end
// over a real route: a nonexistent ID reports the exact same generic 404
// body a caller would see for an ID that exists but is outside their
// scope.
func TestGetProject_UnknownID_Returns404Hidden(t *testing.T) {
	handler, _ := newTestHandler(t)
	rec := doRequest(handler, http.MethodGet, "/projects/does-not-exist", "", "", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body %s, want 404", rec.Code, rec.Body.String())
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %s: %v", rec.Body.String(), err)
	}
	if body.Error.Code != "NOT_FOUND" {
		t.Fatalf("error.code = %q, want NOT_FOUND", body.Error.Code)
	}
	if body.Error.Message != "the requested resource was not found" {
		t.Fatalf("error.message = %q, want the generic leakage-normalized message, never a resource-specific one", body.Error.Message)
	}
}
