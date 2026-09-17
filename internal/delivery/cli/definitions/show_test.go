package definitions_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	clidefinitions "github.com/taQuangLing/agent-workflow/internal/delivery/cli/definitions"
)

// TestRunDefinitionShow_ScopeNegativeMatrix is V6-15E's own "scope
// negative matrix" Verify bullet made concrete: a global Definition must
// be invisible via a project-scoped `show`, a project Definition must be
// invisible via the global `show`, and a project Definition must be
// invisible via a DIFFERENT project's own `show` — every wrong-scope
// combination, not just the happy path — while the two matching
// combinations (global/global, project/its own project) both succeed.
func TestRunDefinitionShow_ScopeNegativeMatrix(t *testing.T) {
	deps := newTestDeps(t)
	mustCreateDefinition(t, deps, "BLOCK", "", "global-def", "global", "create-global")
	mustCreateDefinition(t, deps, "BLOCK", "proj-a", "proj-a-def", "in-a", "create-proj-a")

	cases := []struct {
		name      string
		id        string
		projectID string
		wantFound bool
	}{
		{"global shown at global scope", "global-def", "", true},
		{"global shown at project scope is hidden", "global-def", "proj-a", false},
		{"project def shown at its own project scope", "proj-a-def", "proj-a", true},
		{"project def shown at global scope is hidden", "proj-a-def", "", false},
		{"project def shown at a different project is hidden", "proj-a-def", "proj-b", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args := []string{"--kind", "BLOCK"}
			if tc.projectID != "" {
				args = append(args, "--project-id", tc.projectID)
			}
			err := clidefinitions.RunDefinitionShow(context.Background(), deps, append(args, tc.id), &stdout, &stderr)
			if tc.wantFound {
				if err != nil {
					t.Fatalf("RunDefinitionShow(%s, project=%s) error = %v, want nil", tc.id, tc.projectID, err)
				}
				var view struct {
					ID string `json:"id"`
				}
				if err := json.Unmarshal(stdout.Bytes(), &view); err != nil {
					t.Fatalf("decode %s: %v", stdout.String(), err)
				}
				if view.ID != tc.id {
					t.Fatalf("view.ID = %q, want %q", view.ID, tc.id)
				}
				return
			}
			if err == nil {
				t.Fatalf("RunDefinitionShow(%s, project=%s) error = nil, want ErrDefinitionNotFound (leakage-normalized)", tc.id, tc.projectID)
			}
			if !errors.Is(err, clidefinitions.ErrDefinitionNotFound) {
				t.Fatalf("RunDefinitionShow(%s, project=%s) error = %v, want ErrDefinitionNotFound", tc.id, tc.projectID, err)
			}
		})
	}
}

func TestRunDefinitionShow_UnknownID_ReturnsNotFound(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	err := clidefinitions.RunDefinitionShow(context.Background(), deps, []string{"--kind", "BLOCK", "does-not-exist"}, &stdout, &stderr)
	if !errors.Is(err, clidefinitions.ErrDefinitionNotFound) {
		t.Fatalf("RunDefinitionShow(unknown) error = %v, want ErrDefinitionNotFound", err)
	}
}

func TestRunDefinitionShow_WrongArgCount_IsUsageError(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	err := clidefinitions.RunDefinitionShow(context.Background(), deps, []string{"--kind", "BLOCK"}, &stdout, &stderr)
	if err == nil || !isUsageError(err) {
		t.Fatalf("RunDefinitionShow(no positional) error = %v, want a cli.UsageError", err)
	}
}

func TestRunDefinitionShow_MissingKind_IsUsageError(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	err := clidefinitions.RunDefinitionShow(context.Background(), deps, []string{"some-id"}, &stdout, &stderr)
	if err == nil || !isUsageError(err) {
		t.Fatalf("RunDefinitionShow(no --kind) error = %v, want a cli.UsageError", err)
	}
}
