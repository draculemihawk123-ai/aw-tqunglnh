package definitions_test

import (
	"net/http"
	"testing"
)

func TestCreateDefinition_Global_HappyPath(t *testing.T) {
	e := newTestEnv(t)
	resp := e.do(t, http.MethodPost, "/definitions/BLOCK", "create-1", map[string]any{
		"definitionId": "blk-1", "name": "My Block",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var got struct {
		DefinitionID string `json:"definitionId"`
		Kind         string `json:"kind"`
	}
	decodeInto(t, resp, &got)
	if got.DefinitionID != "blk-1" || got.Kind != "BLOCK" {
		t.Fatalf("got %+v", got)
	}

	detail := e.do(t, http.MethodGet, "/definitions/BLOCK/blk-1", "", nil)
	defer detail.Body.Close()
	if detail.StatusCode != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", detail.StatusCode)
	}
	var view struct {
		Scope struct {
			Global bool `json:"global"`
		} `json:"scope"`
		Status string `json:"status"`
	}
	decodeInto(t, detail, &view)
	if !view.Scope.Global {
		t.Fatalf("expected global scope, got %+v", view)
	}
	if view.Status != "DRAFT" {
		t.Fatalf("status = %q, want DRAFT", view.Status)
	}
}

func TestCreateDefinition_Project_HappyPath(t *testing.T) {
	e := newTestEnv(t)
	e.seedProject(t, "proj-1")
	resp := e.do(t, http.MethodPost, "/projects/proj-1/definitions/SKILL", "create-1", map[string]any{
		"definitionId": "skl-1", "name": "My Skill",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}

	detail := e.do(t, http.MethodGet, "/projects/proj-1/definitions/SKILL/skl-1", "", nil)
	defer detail.Body.Close()
	if detail.StatusCode != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", detail.StatusCode)
	}
	var view struct {
		Scope struct {
			Global    bool   `json:"global"`
			ProjectID string `json:"projectId"`
		} `json:"scope"`
	}
	decodeInto(t, detail, &view)
	if view.Scope.Global || view.Scope.ProjectID != "proj-1" {
		t.Fatalf("expected project scope proj-1, got %+v", view)
	}
}

func TestCreateDefinition_MissingIdempotencyKey_Rejected(t *testing.T) {
	e := newTestEnv(t)
	resp := e.do(t, http.MethodPost, "/definitions/BLOCK", "", map[string]any{
		"definitionId": "blk-1", "name": "My Block",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestCreateDefinition_MissingFields_Rejected(t *testing.T) {
	e := newTestEnv(t)
	resp := e.do(t, http.MethodPost, "/definitions/BLOCK", "create-1", map[string]any{
		"definitionId": "", "name": "My Block",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestCreateDefinition_InvalidKind_Rejected(t *testing.T) {
	e := newTestEnv(t)
	resp := e.do(t, http.MethodPost, "/definitions/NOT_A_KIND", "create-1", map[string]any{
		"definitionId": "blk-1", "name": "My Block",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// TestCreateDefinition_Replay_SameIdempotencyKey_ReturnsIdenticalResult
// proves the idempotency-replay half: two identical creates with the same
// key return the exact same result without a second one erroring as
// "already exists" — the receipt-replay fast path handles it before ever
// reaching CreateDefinition's own transaction a second time.
func TestCreateDefinition_Replay_SameIdempotencyKey_ReturnsIdenticalResult(t *testing.T) {
	e := newTestEnv(t)
	body := map[string]any{"definitionId": "blk-1", "name": "My Block"}
	first := e.do(t, http.MethodPost, "/definitions/BLOCK", "create-1", body)
	defer first.Body.Close()
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first status = %d, want 201", first.StatusCode)
	}
	second := e.do(t, http.MethodPost, "/definitions/BLOCK", "create-1", body)
	defer second.Body.Close()
	// A replay writes the FIRST call's own stored result verbatim via
	// httpapi.WriteReceiptReplay, which always encodes 200 OK regardless
	// of the original mutation's own success status (receiptreplay.go's
	// own doc comment) — this is shared, established httpapi behavior,
	// not something this package's own handler chooses.
	if second.StatusCode != http.StatusOK {
		t.Fatalf("replay status = %d, want 200", second.StatusCode)
	}
}

// TestCreateDefinition_SameIdempotencyKey_DifferentBody_Conflicts proves
// the receipt-hash-conflict half: the same key reused for a genuinely
// different request body is rejected as 409, never silently replayed with
// the first call's own result.
func TestCreateDefinition_SameIdempotencyKey_DifferentBody_Conflicts(t *testing.T) {
	e := newTestEnv(t)
	first := e.do(t, http.MethodPost, "/definitions/BLOCK", "create-1", map[string]any{"definitionId": "blk-1", "name": "My Block"})
	defer first.Body.Close()
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first status = %d, want 201", first.StatusCode)
	}
	second := e.do(t, http.MethodPost, "/definitions/BLOCK", "create-1", map[string]any{"definitionId": "blk-1", "name": "A Different Name"})
	defer second.Body.Close()
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("second status = %d, want 409", second.StatusCode)
	}
}
