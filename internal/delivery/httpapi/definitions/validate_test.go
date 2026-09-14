package definitions_test

import (
	"net/http"
	"testing"
)

func TestValidateDefinitionDraft_Block_HappyPath(t *testing.T) {
	e := newTestEnv(t)
	createResp := e.do(t, http.MethodPost, "/definitions/BLOCK", "create-1", map[string]any{"definitionId": "blk-1", "name": "My Block"})
	createResp.Body.Close()
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", createResp.StatusCode)
	}

	resp := e.do(t, http.MethodPost, "/definitions/BLOCK/blk-1/validate", "", map[string]any{"content": validBlockDocumentJSON})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var view struct {
		DefinitionID string `json:"definitionId"`
		Kind         string `json:"kind"`
		SourceHash   string `json:"sourceHash"`
		CompiledHash string `json:"compiledHash"`
	}
	decodeInto(t, resp, &view)
	if view.DefinitionID != "blk-1" || view.Kind != "BLOCK" || view.SourceHash == "" || view.CompiledHash == "" {
		t.Fatalf("got %+v", view)
	}

	// A dry run never persists anything: no version exists for blk-1 yet.
	list := e.do(t, http.MethodGet, "/definitions/BLOCK/blk-1/versions", "", nil)
	defer list.Body.Close()
	var items struct {
		Items []any `json:"items"`
	}
	decodeInto(t, list, &items)
	if len(items.Items) != 0 {
		t.Fatalf("validate must not persist a version, got %d", len(items.Items))
	}
}

// TestValidateDefinitionDraft_Block_LocationDiagnostics proves V6-05's own
// "Verify: location diagnostics" bullet: an invalid document's 400
// response carries real per-field ErrorDetail entries, not one generic
// message.
func TestValidateDefinitionDraft_Block_LocationDiagnostics(t *testing.T) {
	e := newTestEnv(t)
	e.do(t, http.MethodPost, "/definitions/BLOCK", "create-1", map[string]any{"definitionId": "blk-1", "name": "My Block"}).Body.Close()

	resp := e.do(t, http.MethodPost, "/definitions/BLOCK/blk-1/validate", "", map[string]any{"content": invalidBlockDocumentJSON})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Details []struct {
				Field   string `json:"field"`
				Message string `json:"message"`
			} `json:"details"`
		} `json:"error"`
	}
	decodeInto(t, resp, &body)
	if body.Error.Code != "INVALID_REQUEST" {
		t.Fatalf("code = %q, want INVALID_REQUEST", body.Error.Code)
	}
	if len(body.Error.Details) == 0 {
		t.Fatal("expected at least one location-diagnostic ErrorDetail")
	}
	for _, d := range body.Error.Details {
		if d.Field == "" {
			t.Errorf("diagnostic detail missing Field (source location path): %+v", d)
		}
	}
}

func TestValidateDefinitionDraft_UnknownDefinition_404(t *testing.T) {
	e := newTestEnv(t)
	resp := e.do(t, http.MethodPost, "/definitions/BLOCK/does-not-exist/validate", "", map[string]any{"content": validBlockDocumentJSON})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// TestValidateDefinitionDraft_IsolatedSchemas proves the "isolated
// schemas" Verify bullet: a Definition created as BLOCK is invisible under
// the SKILL path for the exact same id — Kind is part of the identity,
// never just a label.
func TestValidateDefinitionDraft_IsolatedSchemas(t *testing.T) {
	e := newTestEnv(t)
	e.do(t, http.MethodPost, "/definitions/BLOCK", "create-1", map[string]any{"definitionId": "shared-id", "name": "A Block"}).Body.Close()

	resp := e.do(t, http.MethodPost, "/definitions/SKILL/shared-id/validate", "", map[string]any{"content": validBlockDocumentJSON})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (BLOCK id must not be visible as SKILL)", resp.StatusCode)
	}
}

func TestValidateDefinitionDraft_Workflow_HappyPath(t *testing.T) {
	e := newTestEnv(t)
	e.do(t, http.MethodPost, "/definitions/WORKFLOW", "create-1", map[string]any{"definitionId": "wf-1", "name": "My Workflow"}).Body.Close()

	resp := e.do(t, http.MethodPost, "/definitions/WORKFLOW/wf-1/validate", "", map[string]any{"content": minimalWorkflowDocumentJSON})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var view struct {
		DefinitionID string `json:"definitionId"`
		Kind         string `json:"kind"`
	}
	decodeInto(t, resp, &view)
	if view.DefinitionID != "wf-1" || view.Kind != "WORKFLOW" {
		t.Fatalf("got %+v", view)
	}
}

func TestValidateDefinitionDraft_Workflow_StructuralProblemsHaveDetails(t *testing.T) {
	e := newTestEnv(t)
	e.do(t, http.MethodPost, "/definitions/WORKFLOW", "create-1", map[string]any{"definitionId": "wf-1", "name": "My Workflow"}).Body.Close()

	resp := e.do(t, http.MethodPost, "/definitions/WORKFLOW/wf-1/validate", "", map[string]any{"content": invalidWorkflowDocumentJSON})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var body struct {
		Error struct {
			Details []struct {
				Message string `json:"message"`
			} `json:"details"`
		} `json:"error"`
	}
	decodeInto(t, resp, &body)
	if len(body.Error.Details) == 0 {
		t.Fatal("expected at least one workflow validation problem detail")
	}
}

func TestValidateDefinitionDraft_Workflow_YAMLRejected(t *testing.T) {
	e := newTestEnv(t)
	e.do(t, http.MethodPost, "/definitions/WORKFLOW", "create-1", map[string]any{"definitionId": "wf-1", "name": "My Workflow"}).Body.Close()

	resp := e.do(t, http.MethodPost, "/definitions/WORKFLOW/wf-1/validate", "", map[string]any{"content": minimalWorkflowDocumentJSON, "format": "yaml"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (WORKFLOW has no YAML decode path)", resp.StatusCode)
	}
}

// TestValidateDefinitionDraft_WrongScope_404 proves the global/project
// negative matrix: a project-scoped Definition is invisible via the
// global route.
func TestValidateDefinitionDraft_WrongScope_404(t *testing.T) {
	e := newTestEnv(t)
	e.seedProject(t, "proj-1")
	e.do(t, http.MethodPost, "/projects/proj-1/definitions/BLOCK", "create-1", map[string]any{"definitionId": "blk-1", "name": "My Block"}).Body.Close()

	resp := e.do(t, http.MethodPost, "/definitions/BLOCK/blk-1/validate", "", map[string]any{"content": validBlockDocumentJSON})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (project-scoped definition must not be visible via the global route)", resp.StatusCode)
	}
}

// TestValidateDefinitionDraft_OtherProjectScope_404 proves the reverse:
// created under proj-1, invisible via proj-2's own route.
func TestValidateDefinitionDraft_OtherProjectScope_404(t *testing.T) {
	e := newTestEnv(t)
	e.seedProject(t, "proj-1")
	e.seedProject(t, "proj-2")
	e.do(t, http.MethodPost, "/projects/proj-1/definitions/BLOCK", "create-1", map[string]any{"definitionId": "blk-1", "name": "My Block"}).Body.Close()

	resp := e.do(t, http.MethodPost, "/projects/proj-2/definitions/BLOCK/blk-1/validate", "", map[string]any{"content": validBlockDocumentJSON})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (a different project's route must not see proj-1's own definition)", resp.StatusCode)
	}
}

// TestValidateDefinitionDraft_GlobalOnlyVisibleGlobally proves the last
// leg of the matrix: a GLOBAL definition is invisible via ANY project
// route.
func TestValidateDefinitionDraft_GlobalOnlyVisibleGlobally(t *testing.T) {
	e := newTestEnv(t)
	e.seedProject(t, "proj-1")
	e.do(t, http.MethodPost, "/definitions/BLOCK", "create-1", map[string]any{"definitionId": "blk-1", "name": "My Block"}).Body.Close()

	resp := e.do(t, http.MethodPost, "/projects/proj-1/definitions/BLOCK/blk-1/validate", "", map[string]any{"content": validBlockDocumentJSON})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (a global definition must not be visible via a project route)", resp.StatusCode)
	}
}

func TestValidateDefinitionDraft_MissingContent_Rejected(t *testing.T) {
	e := newTestEnv(t)
	e.do(t, http.MethodPost, "/definitions/BLOCK", "create-1", map[string]any{"definitionId": "blk-1", "name": "My Block"}).Body.Close()

	resp := e.do(t, http.MethodPost, "/definitions/BLOCK/blk-1/validate", "", map[string]any{"content": ""})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}
