package definitions_test

import (
	"net/http"
	"strings"
	"testing"
)

// V9-09 / B2: POST /definitions/{kind} with a definitionId that already
// exists is the typed 409 CONFLICT, never the 500 INTERNAL "sqlite:
// unexpected error" it used to be — for every duplicate shape (same kind,
// another kind, Workflow, project scope over a global id) — while the same
// Idempotency-Key still replays the first result.
func TestCreateDefinition_ExistingID_Returns409Conflict(t *testing.T) {
	e := newTestEnv(t)
	e.seedProject(t, "proj-1")

	first := e.do(t, http.MethodPost, "/definitions/BLOCK", "create-1", map[string]any{"definitionId": "dup-1", "name": "first"})
	first.Body.Close()
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first create status = %d, want 201", first.StatusCode)
	}
	wf := e.do(t, http.MethodPost, "/definitions/WORKFLOW", "create-wf", map[string]any{"definitionId": "dup-wf", "name": "workflow"})
	wf.Body.Close()
	if wf.StatusCode != http.StatusCreated {
		t.Fatalf("workflow create status = %d, want 201", wf.StatusCode)
	}

	cases := []struct {
		name, path, id string
	}{
		{"same kind", "/definitions/BLOCK", "dup-1"},
		{"another kind", "/definitions/SKILL", "dup-1"},
		{"workflow over a shared id", "/definitions/WORKFLOW", "dup-1"},
		{"shared over a workflow id", "/definitions/BLOCK", "dup-wf"},
		{"workflow twice", "/definitions/WORKFLOW", "dup-wf"},
		{"project scope over a global id", "/projects/proj-1/definitions/BLOCK", "dup-1"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := e.do(t, http.MethodPost, tc.path, "dup-key-"+string(rune('a'+i)), map[string]any{"definitionId": tc.id, "name": "again"})
			var body struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			status := resp.StatusCode
			decodeInto(t, resp, &body)
			if status != http.StatusConflict {
				t.Fatalf("status = %d, want 409 (body %+v)", status, body)
			}
			if body.Error.Code != "CONFLICT" {
				t.Fatalf("error.code = %q, want CONFLICT", body.Error.Code)
			}
			if !strings.Contains(body.Error.Message, tc.id) || strings.Contains(body.Error.Message, "unexpected error") {
				t.Fatalf("error.message = %q, want it to name %q and not be the opaque sqlite error", body.Error.Message, tc.id)
			}
		})
	}

	// Idempotent re-create with the first command's own key keeps working.
	replay := e.do(t, http.MethodPost, "/definitions/BLOCK", "create-1", map[string]any{"definitionId": "dup-1", "name": "first"})
	replay.Body.Close()
	if replay.StatusCode != http.StatusOK {
		t.Fatalf("replay status = %d, want 200 (the stored first result)", replay.StatusCode)
	}
}
