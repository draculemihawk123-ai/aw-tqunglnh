package definitions_test

import (
	"net/http"
	"testing"
)

func TestPublishDefinitionVersion_Block_HappyPath(t *testing.T) {
	e := newTestEnv(t)
	e.do(t, http.MethodPost, "/definitions/BLOCK", "create-1", map[string]any{"definitionId": "blk-1", "name": "My Block"}).Body.Close()
	// A dependency pin is cross-project-resolved for real
	// (publishSharedDefinitionVersionTx's own "each pin's actual project
	// is resolved from the repository itself" check) — the referenced
	// Definition must actually exist in this same scope.
	e.do(t, http.MethodPost, "/definitions/POLICY", "create-policy", map[string]any{"definitionId": "policy-1", "name": "My Policy"}).Body.Close()

	resp := e.do(t, http.MethodPost, "/definitions/BLOCK/blk-1/publish", "publish-1", map[string]any{
		"content": validBlockDocumentJSON,
		"dependencies": []map[string]string{
			{"kind": "POLICY", "definitionId": "policy-1", "versionId": "policy-1-v1"},
		},
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var view struct {
		ID               string `json:"id"`
		DefinitionID     string `json:"definitionId"`
		Kind             string `json:"kind"`
		VersionNumber    uint64 `json:"versionNumber"`
		SourceHash       string `json:"sourceHash"`
		CompiledHash     string `json:"compiledHash"`
		CanonicalSource  string `json:"canonicalSource"`
		CompiledSnapshot string `json:"compiledSnapshot"`
		Dependencies     struct {
			Pins []map[string]string `json:"pins"`
		} `json:"dependencies"`
	}
	decodeInto(t, resp, &view)
	// V6-05's own "Thực hiện: publish trả source/compiled hash và exact
	// pins" — every one of these must be populated, never blank.
	if view.ID == "" || view.DefinitionID != "blk-1" || view.Kind != "BLOCK" || view.VersionNumber != 1 {
		t.Fatalf("got %+v", view)
	}
	if view.SourceHash == "" || view.CompiledHash == "" || view.CanonicalSource == "" || view.CompiledSnapshot == "" {
		t.Fatalf("expected non-empty hashes/snapshots, got %+v", view)
	}
	if len(view.Dependencies.Pins) != 1 || view.Dependencies.Pins[0]["definitionId"] != "policy-1" {
		t.Fatalf("expected exact declared pin echoed back, got %+v", view.Dependencies)
	}

	// The published version is now visible via list/detail.
	list := e.do(t, http.MethodGet, "/definitions/BLOCK/blk-1/versions", "", nil)
	defer list.Body.Close()
	var items struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	decodeInto(t, list, &items)
	if len(items.Items) != 1 || items.Items[0].ID != view.ID {
		t.Fatalf("list versions = %+v, want exactly [%s]", items, view.ID)
	}

	one := e.do(t, http.MethodGet, "/definitions/versions/"+view.ID, "", nil)
	defer one.Body.Close()
	if one.StatusCode != http.StatusOK {
		t.Fatalf("get version status = %d, want 200", one.StatusCode)
	}
}

// TestPublishDefinitionVersion_Replay_SameIdempotencyKey_NoNewVersion
// proves a literal retry (same Idempotency-Key, same body) replays the
// first call's own stored result — never appends a second version row.
func TestPublishDefinitionVersion_Replay_SameIdempotencyKey_NoNewVersion(t *testing.T) {
	e := newTestEnv(t)
	e.do(t, http.MethodPost, "/definitions/BLOCK", "create-1", map[string]any{"definitionId": "blk-1", "name": "My Block"}).Body.Close()

	body := map[string]any{"content": validBlockDocumentJSON}
	first := e.do(t, http.MethodPost, "/definitions/BLOCK/blk-1/publish", "publish-1", body)
	var firstView struct {
		ID string `json:"id"`
	}
	decodeInto(t, first, &firstView)

	second := e.do(t, http.MethodPost, "/definitions/BLOCK/blk-1/publish", "publish-1", body)
	defer second.Body.Close()
	// See TestCreateDefinition_Replay_SameIdempotencyKey_ReturnsIdenticalResult's
	// own comment: a receipt replay always encodes 200, never the original
	// mutation's own status.
	if second.StatusCode != http.StatusOK {
		t.Fatalf("replay status = %d, want 200", second.StatusCode)
	}
	var secondView struct {
		ID string `json:"id"`
	}
	decodeInto(t, second, &secondView)
	if secondView.ID != firstView.ID {
		t.Fatalf("replay minted a different version id: %q vs %q", secondView.ID, firstView.ID)
	}

	list := e.do(t, http.MethodGet, "/definitions/BLOCK/blk-1/versions", "", nil)
	defer list.Body.Close()
	var items struct {
		Items []any `json:"items"`
	}
	decodeInto(t, list, &items)
	if len(items.Items) != 1 {
		t.Fatalf("expected exactly one persisted version after replay, got %d", len(items.Items))
	}
}

// TestPublishDefinitionVersion_DifferentIdempotencyKey_SameContent_DedupesVersion
// proves the second, subtler dedup: a DIFFERENT command (a fresh
// Idempotency-Key) publishing byte-identical content still gets its own
// 201 response, but never a second version row — AK-ARCH-005B's own
// CompiledHash dedup, exercised through this package's own HTTP layer.
func TestPublishDefinitionVersion_DifferentIdempotencyKey_SameContent_DedupesVersion(t *testing.T) {
	e := newTestEnv(t)
	e.do(t, http.MethodPost, "/definitions/BLOCK", "create-1", map[string]any{"definitionId": "blk-1", "name": "My Block"}).Body.Close()

	body := map[string]any{"content": validBlockDocumentJSON}
	first := e.do(t, http.MethodPost, "/definitions/BLOCK/blk-1/publish", "publish-1", body)
	var firstView struct {
		ID           string `json:"id"`
		CompiledHash string `json:"compiledHash"`
	}
	decodeInto(t, first, &firstView)

	second := e.do(t, http.MethodPost, "/definitions/BLOCK/blk-1/publish", "publish-2", body)
	defer second.Body.Close()
	if second.StatusCode != http.StatusCreated {
		t.Fatalf("second publish status = %d, want 201", second.StatusCode)
	}
	var secondView struct {
		ID           string `json:"id"`
		CompiledHash string `json:"compiledHash"`
	}
	decodeInto(t, second, &secondView)
	if secondView.ID != firstView.ID {
		t.Fatalf("expected the same deduped version id, got %q vs %q", secondView.ID, firstView.ID)
	}
	if secondView.CompiledHash != firstView.CompiledHash {
		t.Fatalf("compiled hash changed across dedup: %q vs %q", secondView.CompiledHash, firstView.CompiledHash)
	}

	list := e.do(t, http.MethodGet, "/definitions/BLOCK/blk-1/versions", "", nil)
	defer list.Body.Close()
	var items struct {
		Items []any `json:"items"`
	}
	decodeInto(t, list, &items)
	if len(items.Items) != 1 {
		t.Fatalf("expected exactly one persisted version after dedup, got %d", len(items.Items))
	}
}

func TestPublishDefinitionVersion_SameIdempotencyKey_DifferentBody_Conflicts(t *testing.T) {
	e := newTestEnv(t)
	e.do(t, http.MethodPost, "/definitions/BLOCK", "create-1", map[string]any{"definitionId": "blk-1", "name": "My Block"}).Body.Close()

	first := e.do(t, http.MethodPost, "/definitions/BLOCK/blk-1/publish", "publish-1", map[string]any{"content": validBlockDocumentJSON})
	first.Body.Close()

	second := e.do(t, http.MethodPost, "/definitions/BLOCK/blk-1/publish", "publish-1", map[string]any{"content": validBlockDocumentJSON, "schemaVersion": 2})
	defer second.Body.Close()
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", second.StatusCode)
	}
}

func TestPublishDefinitionVersion_UnknownDefinition_404(t *testing.T) {
	e := newTestEnv(t)
	resp := e.do(t, http.MethodPost, "/definitions/BLOCK/does-not-exist/publish", "publish-1", map[string]any{"content": validBlockDocumentJSON})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestPublishDefinitionVersion_WrongScope_404(t *testing.T) {
	e := newTestEnv(t)
	e.seedProject(t, "proj-1")
	e.do(t, http.MethodPost, "/projects/proj-1/definitions/BLOCK", "create-1", map[string]any{"definitionId": "blk-1", "name": "My Block"}).Body.Close()

	resp := e.do(t, http.MethodPost, "/definitions/BLOCK/blk-1/publish", "publish-1", map[string]any{"content": validBlockDocumentJSON})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (must not publish a project-scoped definition through the global route)", resp.StatusCode)
	}

	// The project-scoped route itself still works.
	ok := e.do(t, http.MethodPost, "/projects/proj-1/definitions/BLOCK/blk-1/publish", "publish-1", map[string]any{"content": validBlockDocumentJSON})
	defer ok.Body.Close()
	if ok.StatusCode != http.StatusCreated {
		t.Fatalf("project-scoped publish status = %d, want 201", ok.StatusCode)
	}
}

// TestPublishDefinitionVersion_Workflow_HappyPath proves the Workflow
// dispatch branch end to end via real HTTP: publish pins the real next
// VersionNumber (1) and the version is retrievable by its own kind-
// agnostic get-version route.
func TestPublishDefinitionVersion_Workflow_HappyPath(t *testing.T) {
	e := newTestEnv(t)
	e.do(t, http.MethodPost, "/definitions/WORKFLOW", "create-1", map[string]any{"definitionId": "wf-1", "name": "My Workflow"}).Body.Close()

	resp := e.do(t, http.MethodPost, "/definitions/WORKFLOW/wf-1/publish", "publish-1", map[string]any{"content": minimalWorkflowDocumentJSON})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var view struct {
		ID            string `json:"id"`
		Kind          string `json:"kind"`
		VersionNumber uint64 `json:"versionNumber"`
	}
	decodeInto(t, resp, &view)
	if view.Kind != "WORKFLOW" || view.VersionNumber != 1 {
		t.Fatalf("got %+v", view)
	}

	one := e.do(t, http.MethodGet, "/definitions/versions/"+view.ID, "", nil)
	defer one.Body.Close()
	if one.StatusCode != http.StatusOK {
		t.Fatalf("get version status = %d, want 200", one.StatusCode)
	}
}
