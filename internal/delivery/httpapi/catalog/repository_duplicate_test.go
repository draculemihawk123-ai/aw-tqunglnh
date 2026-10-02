package catalog_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// V9-09 / B2 (LIM-06): POST /projects/{id}/repositories with a repositoryId
// that already exists — or a name already used inside the project — is the
// typed 409 CONFLICT, never the 500 INTERNAL the raw constraint failure used
// to become; the same Idempotency-Key still replays the first result.
func TestRegisterRepository_ExistingID_Returns409Conflict(t *testing.T) {
	handler, _ := newTestHandler(t)
	projectID := createTestProject(t, handler, "key-project", "widget")
	registerTestRepository(t, handler, projectID, "key-repo-1", "repo-1")

	cases := []struct {
		name, key, repositoryID, repositoryName, wantInMessage string
	}{
		{"same id, another name", "key-dup-id", "repo-1", "another-name", "repo-1"},
		{"another id, same name", "key-dup-name", "repo-2", "repo-1", "repo-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"repositoryId":"` + tc.repositoryID + `","name":"` + tc.repositoryName + `","remoteLocator":"https://example.invalid/repo.git","defaultRef":"main"}`
			rec := doRequest(handler, http.MethodPost, "/projects/"+projectID+"/repositories", tc.key, "", body)
			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, body %s, want 409", rec.Code, rec.Body.String())
			}
			var envelope struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decode %s: %v", rec.Body.String(), err)
			}
			if envelope.Error.Code != "CONFLICT" {
				t.Fatalf("error.code = %q, want CONFLICT", envelope.Error.Code)
			}
			if !strings.Contains(envelope.Error.Message, tc.wantInMessage) || strings.Contains(envelope.Error.Message, "internal") {
				t.Fatalf("error.message = %q, want it to name %q and not be an internal error", envelope.Error.Message, tc.wantInMessage)
			}
		})
	}

	// Nothing was created by the rejected attempts.
	if rec := doRequest(handler, http.MethodGet, "/repositories/repo-2", "", "", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("GET /repositories/repo-2 = %d, want 404: a rejected register must create nothing", rec.Code)
	}

	// Replaying the first command's own key is untouched (the stored 201
	// result is replayed as 200).
	body := `{"repositoryId":"repo-1","name":"repo-1","remoteLocator":"https://example.invalid/repo.git","defaultRef":"main"}`
	if rec := doRequest(handler, http.MethodPost, "/projects/"+projectID+"/repositories", "key-repo-1", "", body); rec.Code != http.StatusOK {
		t.Fatalf("replay status = %d, body %s, want 200", rec.Code, rec.Body.String())
	}
}
