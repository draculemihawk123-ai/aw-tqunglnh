package definitions_test

import (
	"net/http"
	"testing"
)

// TestListDefinitions_Global_ReturnsOnlyMatchingKindAndScope proves
// GET /definitions/{kind} (V7-07A's own new route, closing the parity
// ledger gap internal/delivery/parity/ledger.go's own "definition list: a
// CLI_LOCAL leaf with no route" entries named) returns every Definition of
// that Kind in the global scope — never a different Kind, never a
// project-scoped one, even though both exist in the same store.
func TestListDefinitions_Global_ReturnsOnlyMatchingKindAndScope(t *testing.T) {
	e := newTestEnv(t)
	e.seedProject(t, "proj-1")

	create := func(path, idempotencyKey, definitionID, name string) {
		resp := e.do(t, http.MethodPost, path, idempotencyKey, map[string]any{"definitionId": definitionID, "name": name})
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %s: status = %d, want 201", path, resp.StatusCode)
		}
	}
	create("/definitions/BLOCK", "create-1", "blk-1", "Global Block One")
	create("/definitions/BLOCK", "create-2", "blk-2", "Global Block Two")
	create("/definitions/SKILL", "create-3", "skl-1", "Global Skill, different kind")
	create("/projects/proj-1/definitions/BLOCK", "create-4", "blk-3", "Project Block, different scope")

	resp := e.do(t, http.MethodGet, "/definitions/BLOCK", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got struct {
		Definitions []struct {
			ID    string `json:"id"`
			Kind  string `json:"kind"`
			Scope struct {
				Global bool `json:"global"`
			} `json:"scope"`
		} `json:"definitions"`
	}
	decodeInto(t, resp, &got)
	if len(got.Definitions) != 2 {
		t.Fatalf("got %d definitions, want 2 (blk-1, blk-2 only): %+v", len(got.Definitions), got.Definitions)
	}
	for _, d := range got.Definitions {
		if d.Kind != "BLOCK" {
			t.Errorf("definition %s has kind %s, want BLOCK", d.ID, d.Kind)
		}
		if !d.Scope.Global {
			t.Errorf("definition %s is not global-scoped, want global", d.ID)
		}
	}
}

// TestListDefinitions_Project_ReturnsOnlyThatProjectsDefinitions proves
// GET /projects/{projectId}/definitions/{kind} (listProjectDefinitions)
// never leaks a global Definition, nor a different project's Definition,
// into a project-scoped list. Each fixture below gets its own distinct
// DefinitionID (the definitions table's own real PRIMARY KEY is `id` alone,
// with no scope column in that key — a definitionId is globally unique
// regardless of scope, confirmed against
// internal/adapters/sqlite/migrations/0004_shared_definitions.sql — so the
// real leakage risk this test guards is a WHERE-clause bug that forgets to
// filter by project_id, not an ID collision across scopes).
func TestListDefinitions_Project_ReturnsOnlyThatProjectsDefinitions(t *testing.T) {
	e := newTestEnv(t)
	e.seedProject(t, "proj-1")
	e.seedProject(t, "proj-2")

	create := func(path, idempotencyKey, definitionID, name string) {
		resp := e.do(t, http.MethodPost, path, idempotencyKey, map[string]any{"definitionId": definitionID, "name": name})
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %s: status = %d, want 201", path, resp.StatusCode)
		}
	}
	create("/definitions/BLOCK", "create-1", "global-block", "Global Block")
	create("/projects/proj-1/definitions/BLOCK", "create-2", "proj1-block", "proj-1's own Block")
	create("/projects/proj-2/definitions/BLOCK", "create-3", "proj2-block", "proj-2's own Block")

	resp := e.do(t, http.MethodGet, "/projects/proj-1/definitions/BLOCK", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got struct {
		Definitions []struct {
			ID    string `json:"id"`
			Scope struct {
				Global    bool   `json:"global"`
				ProjectID string `json:"projectId"`
			} `json:"scope"`
		} `json:"definitions"`
	}
	decodeInto(t, resp, &got)
	if len(got.Definitions) != 1 {
		t.Fatalf("got %d definitions, want exactly 1 (proj-1's own): %+v", len(got.Definitions), got.Definitions)
	}
	if got.Definitions[0].Scope.Global || got.Definitions[0].Scope.ProjectID != "proj-1" {
		t.Fatalf("expected proj-1 scope, got %+v", got.Definitions[0])
	}
}

// TestListDefinitions_EmptyScope_ReturnsEmptyNotError proves an unknown or
// simply-empty scope/kind combination returns a 200 with an empty
// (non-null) array — matching internal/app/definitions.ListDefinitions'
// own documented contract ("an empty scope with no matching Definitions
// returns an empty, non-nil slice, never an error"), including for a
// projectId this store has never seen at all.
func TestListDefinitions_EmptyScope_ReturnsEmptyNotError(t *testing.T) {
	e := newTestEnv(t)

	resp := e.do(t, http.MethodGet, "/definitions/WORKFLOW", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got struct {
		Definitions []any `json:"definitions"`
	}
	decodeInto(t, resp, &got)
	if got.Definitions == nil || len(got.Definitions) != 0 {
		t.Fatalf("got %+v, want a present, empty array", got.Definitions)
	}

	respUnknownProject := e.do(t, http.MethodGet, "/projects/does-not-exist/definitions/WORKFLOW", "", nil)
	defer respUnknownProject.Body.Close()
	if respUnknownProject.StatusCode != http.StatusOK {
		t.Fatalf("unknown-project status = %d, want 200 (never a 404 — ListDefinitions never errors on scope)", respUnknownProject.StatusCode)
	}
}

// TestListDefinitions_InvalidKind_IsBadRequest proves the {kind} path
// segment is validated the identical way every sibling route in this
// package already validates it (pathKind, envelope.go).
func TestListDefinitions_InvalidKind_IsBadRequest(t *testing.T) {
	e := newTestEnv(t)
	resp := e.do(t, http.MethodGet, "/definitions/NOT_A_REAL_KIND", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}
