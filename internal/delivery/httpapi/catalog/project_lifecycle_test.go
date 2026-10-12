package catalog_test

// V11-13 (ADR-044): rename, archive and restore a project over HTTP.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func projectVersionOf(t *testing.T, handler http.Handler, projectID string) string {
	t.Helper()
	rec := doRequest(handler, http.MethodGet, "/projects/"+projectID, "", "", "")
	var view struct {
		Version uint64 `json:"version"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return `"` + jsonNumber(view.Version) + `"`
}

func jsonNumber(n uint64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func projectIDs(t *testing.T, handler http.Handler, query string) []string {
	t.Helper()
	rec := doRequest(handler, http.MethodGet, "/projects"+query, "", "", "")
	var body struct {
		Projects []struct {
			ID string `json:"id"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	var ids []string
	for _, p := range body.Projects {
		ids = append(ids, p.ID)
	}
	return ids
}

func TestRenameProject_OverHTTP(t *testing.T) {
	handler, _ := newTestHandler(t)
	id := createTestProject(t, handler, "key-project", "widget")

	if rec := doRequest(handler, http.MethodPatch, "/projects/"+id, "key-ren-nomatch", "", `{"name":"x"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("rename without If-Match = %d, want 400", rec.Code)
	}
	if rec := doRequest(handler, http.MethodPatch, "/projects/"+id, "key-ren-empty", projectVersionOf(t, handler, id), `{"name":"   "}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("rename to blank = %d %s, want 400", rec.Code, rec.Body.String())
	}
	rec := doRequest(handler, http.MethodPatch, "/projects/"+id, "key-ren", projectVersionOf(t, handler, id), `{"name":"Gadget"}`)
	if rec.Code != http.StatusOK || jsonField(t, rec.Body.String(), "name") != "Gadget" {
		t.Fatalf("rename = %d %s; want 200 with the new name", rec.Code, rec.Body.String())
	}
	if rec := doRequest(handler, http.MethodPatch, "/projects/"+id, "key-ren-stale", `"1"`, `{"name":"Stale"}`); rec.Code != http.StatusConflict {
		t.Fatalf("rename at a stale version = %d, want 409", rec.Code)
	}
	get := doRequest(handler, http.MethodGet, "/projects/"+id, "", "", "")
	if jsonField(t, get.Body.String(), "name") != "Gadget" || jsonField(t, get.Body.String(), "id") != id {
		t.Fatalf("project = %s; want the new name and the same id", get.Body.String())
	}
}

func TestArchiveProject_HidesItMakesItReadOnlyAndRestoreUndoesIt(t *testing.T) {
	handler, _ := newTestHandler(t)
	id := createTestProject(t, handler, "key-project", "widget")
	other := createTestProject(t, handler, "key-project-2", "other")

	rec := doRequest(handler, http.MethodPost, "/projects/"+id+"/archive", "key-arc", projectVersionOf(t, handler, id), `{"reason":"finished"}`)
	if rec.Code != http.StatusOK || jsonField(t, rec.Body.String(), "status") != "ARCHIVED" {
		t.Fatalf("archive = %d %s; want 200 ARCHIVED", rec.Code, rec.Body.String())
	}
	if ids := projectIDs(t, handler, ""); len(ids) != 1 || ids[0] != other {
		t.Fatalf("default list = %v; want only the other project", ids)
	}
	if ids := projectIDs(t, handler, "?includeArchived=true"); len(ids) != 2 {
		t.Fatalf("includeArchived list = %v; want both", ids)
	}

	// read-only: a write is refused with the code and the way back; reads work
	reg := doRequest(handler, http.MethodPost, "/projects/"+id+"/repositories", "key-late", "", `{"repositoryId":"r","name":"r","remoteLocator":"D:/x","defaultRef":"main"}`)
	if reg.Code != http.StatusConflict || !strings.Contains(reg.Body.String(), "PROJECT_ARCHIVED") || !strings.Contains(reg.Body.String(), "restore") {
		t.Fatalf("register into an archived project = %d %s; want 409 PROJECT_ARCHIVED with how to restore", reg.Code, reg.Body.String())
	}
	if get := doRequest(handler, http.MethodGet, "/projects/"+id, "", "", ""); get.Code != http.StatusOK || jsonField(t, get.Body.String(), "status") != "ARCHIVED" {
		t.Fatalf("GET of an archived project = %d %s", get.Code, get.Body.String())
	}
	if list := doRequest(handler, http.MethodGet, "/projects/"+id+"/repositories", "", "", ""); list.Code != http.StatusOK {
		t.Fatalf("listing an archived project's repositories = %d, want 200", list.Code)
	}
	if ren := doRequest(handler, http.MethodPatch, "/projects/"+id, "key-ren-late", projectVersionOf(t, handler, id), `{"name":"x"}`); ren.Code != http.StatusConflict {
		t.Fatalf("rename of an archived project = %d, want 409", ren.Code)
	}

	// the actions it offers follow its state
	var view struct {
		ValidActions []struct {
			OperationID string `json:"operationId"`
		} `json:"validActions"`
	}
	_ = json.Unmarshal(doRequest(handler, http.MethodGet, "/projects/"+id, "", "", "").Body.Bytes(), &view)
	if len(view.ValidActions) != 1 || view.ValidActions[0].OperationID != "projectsRestore" {
		t.Fatalf("validActions of an archived project = %+v; want only projectsRestore", view.ValidActions)
	}

	rec = doRequest(handler, http.MethodPost, "/projects/"+id+"/restore", "key-res", projectVersionOf(t, handler, id), `{}`)
	if rec.Code != http.StatusOK || jsonField(t, rec.Body.String(), "status") != "ACTIVE" {
		t.Fatalf("restore = %d %s; want 200 ACTIVE", rec.Code, rec.Body.String())
	}
	body := `{"repositoryId":"r","name":"r","remoteLocator":"D:/x","defaultRef":"main"}`
	if reg := doRequest(handler, http.MethodPost, "/projects/"+id+"/repositories", "key-after", "", body); reg.Code != http.StatusCreated {
		t.Fatalf("register after restore = %d %s; want 201", reg.Code, reg.Body.String())
	}
}
