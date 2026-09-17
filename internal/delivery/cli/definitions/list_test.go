package definitions_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	clidefinitions "github.com/taQuangLing/agent-workflow/internal/delivery/cli/definitions"
)

// TestRunDefinitionList_ScopeIsolation proves `aw definition list` only
// ever returns Definitions of the requested Kind actually belonging to the
// requested scope — a global listing never leaks a project's own
// Definitions and vice versa, the "aw definition list (query, scope flag
// for global vs. project)" command surface's own scope contract.
func TestRunDefinitionList_ScopeIsolation(t *testing.T) {
	deps := newTestDeps(t)
	mustCreateDefinition(t, deps, "BLOCK", "", "global-1", "g1", "c1")
	mustCreateDefinition(t, deps, "BLOCK", "", "global-2", "g2", "c2")
	mustCreateDefinition(t, deps, "BLOCK", "proj-a", "a-1", "a1", "c3")
	mustCreateDefinition(t, deps, "BLOCK", "proj-b", "b-1", "b1", "c4")
	// A different Kind in the same global scope must never appear in a
	// BLOCK-kind listing.
	mustCreateDefinition(t, deps, "SKILL", "", "global-skill", "gs", "c5")

	var globalOut bytes.Buffer
	if err := clidefinitions.RunDefinitionList(context.Background(), deps, []string{"--kind", "BLOCK"}, &globalOut, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunDefinitionList(global) error = %v", err)
	}
	globalIDs := decodeDefinitionIDs(t, globalOut.Bytes())
	if len(globalIDs) != 2 || !globalIDs["global-1"] || !globalIDs["global-2"] {
		t.Fatalf("global BLOCK listing = %v, want exactly {global-1, global-2}", globalIDs)
	}

	var projAOut bytes.Buffer
	if err := clidefinitions.RunDefinitionList(context.Background(), deps, []string{"--kind", "BLOCK", "--project-id", "proj-a"}, &projAOut, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunDefinitionList(proj-a) error = %v", err)
	}
	projAIDs := decodeDefinitionIDs(t, projAOut.Bytes())
	if len(projAIDs) != 1 || !projAIDs["a-1"] {
		t.Fatalf("proj-a BLOCK listing = %v, want exactly {a-1}", projAIDs)
	}
}

func TestRunDefinitionList_EmptyScope_ReturnsEmptyNotError(t *testing.T) {
	deps := newTestDeps(t)
	var stdout bytes.Buffer
	if err := clidefinitions.RunDefinitionList(context.Background(), deps, []string{"--kind", "WORKFLOW"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunDefinitionList(empty) error = %v", err)
	}
	ids := decodeDefinitionIDs(t, stdout.Bytes())
	if len(ids) != 0 {
		t.Fatalf("empty-scope listing = %v, want empty", ids)
	}
}

func TestRunDefinitionList_MissingKind_IsUsageError(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	err := clidefinitions.RunDefinitionList(context.Background(), deps, nil, &stdout, &stderr)
	if err == nil || !isUsageError(err) {
		t.Fatalf("RunDefinitionList(no --kind) error = %v, want a cli.UsageError", err)
	}
}

func decodeDefinitionIDs(t *testing.T, body []byte) map[string]bool {
	t.Helper()
	var view struct {
		Definitions []struct {
			ID string `json:"id"`
		} `json:"definitions"`
	}
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	ids := make(map[string]bool, len(view.Definitions))
	for _, d := range view.Definitions {
		ids[d.ID] = true
	}
	return ids
}
