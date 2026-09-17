package definitions_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	clidefinitions "github.com/taQuangLing/agent-workflow/internal/delivery/cli/definitions"
)

func TestRunDefinitionValidate_ValidDocument_ReturnsCompiledVersionFields(t *testing.T) {
	deps := newTestDeps(t)
	mustCreateDefinition(t, deps, "BLOCK", "", "blk-1", "n", "create-1")

	var stdout, stderr bytes.Buffer
	err := clidefinitions.RunDefinitionValidate(context.Background(), deps, []string{"--kind", "BLOCK", "blk-1"}, strings.NewReader(validBlockDocumentJSON), &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunDefinitionValidate(valid) error = %v, stderr = %s", err, stderr.String())
	}
	var view struct {
		Valid   bool `json:"valid"`
		Version struct {
			CompiledHash string `json:"compiledHash"`
		} `json:"version"`
		Diagnostics []any `json:"diagnostics"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &view); err != nil {
		t.Fatalf("decode %s: %v", stdout.String(), err)
	}
	if !view.Valid {
		t.Fatal("valid = false for a structurally valid document, want true")
	}
	if view.Version.CompiledHash == "" {
		t.Fatal("version.compiledHash is empty, want a real compiled hash")
	}
	if len(view.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %v, want empty for a valid document", view.Diagnostics)
	}

	// A dry run must never persist anything: no Version exists yet.
	var versionsOut bytes.Buffer
	if err := clidefinitions.RunDefinitionVersions(context.Background(), deps, []string{"--kind", "BLOCK", "blk-1"}, &versionsOut, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunDefinitionVersions() error = %v", err)
	}
	var versionsView struct {
		Items []any `json:"items"`
	}
	if err := json.Unmarshal(versionsOut.Bytes(), &versionsView); err != nil {
		t.Fatalf("decode %s: %v", versionsOut.String(), err)
	}
	if len(versionsView.Items) != 0 {
		t.Fatalf("versions after a dry-run validate = %v, want none (validate must never publish)", versionsView.Items)
	}
}

// TestRunDefinitionValidate_InvalidDocument_ReturnsStructuredDiagnostics is
// V6-15E's own "Diagnostics" Verify bullet: a compile failure must never
// be surfaced as a bare error string — the structured, per-field
// diagnostic list ValidateDraft's own underlying authoring.Diagnostics
// already carries must be faithfully written to stdout.
func TestRunDefinitionValidate_InvalidDocument_ReturnsStructuredDiagnostics(t *testing.T) {
	deps := newTestDeps(t)
	mustCreateDefinition(t, deps, "BLOCK", "", "blk-1", "n", "create-1")

	var stdout, stderr bytes.Buffer
	err := clidefinitions.RunDefinitionValidate(context.Background(), deps, []string{"--kind", "BLOCK", "blk-1"}, strings.NewReader(invalidBlockDocumentJSON), &stdout, &stderr)
	if err == nil {
		t.Fatal("RunDefinitionValidate(invalid) error = nil, want a non-nil error (so exit code reports failure)")
	}
	if isUsageError(err) {
		t.Fatalf("RunDefinitionValidate(invalid) error = %v is a cli.UsageError, want a plain document-validation error", err)
	}

	var view struct {
		Valid       bool `json:"valid"`
		Diagnostics []struct {
			Field   string `json:"field"`
			Message string `json:"message"`
		} `json:"diagnostics"`
	}
	if decodeErr := json.Unmarshal(stdout.Bytes(), &view); decodeErr != nil {
		t.Fatalf("stdout is not the structured diagnostics document: %s (decode error: %v)", stdout.String(), decodeErr)
	}
	if view.Valid {
		t.Fatal("valid = true for a document missing every required field, want false")
	}
	if len(view.Diagnostics) == 0 {
		t.Fatal("diagnostics is empty for an invalid document, want at least one real diagnostic")
	}
	for _, d := range view.Diagnostics {
		if d.Message == "" {
			t.Fatalf("diagnostic %+v has an empty message", d)
		}
	}
}

func TestRunDefinitionValidate_WrongScope_NotFound(t *testing.T) {
	deps := newTestDeps(t)
	mustCreateDefinition(t, deps, "BLOCK", "proj-a", "blk-1", "n", "create-1")

	var stdout, stderr bytes.Buffer
	err := clidefinitions.RunDefinitionValidate(context.Background(), deps, []string{"--kind", "BLOCK", "blk-1"}, strings.NewReader(validBlockDocumentJSON), &stdout, &stderr)
	if err == nil {
		t.Fatal("RunDefinitionValidate against a project-scoped definition via the global route succeeded, want ErrDefinitionNotFound")
	}
}

func TestRunDefinitionValidate_MissingContent_IsUsageError(t *testing.T) {
	deps := newTestDeps(t)
	mustCreateDefinition(t, deps, "BLOCK", "", "blk-1", "n", "create-1")

	var stdout, stderr bytes.Buffer
	err := clidefinitions.RunDefinitionValidate(context.Background(), deps, []string{"--kind", "BLOCK", "blk-1"}, strings.NewReader(""), &stdout, &stderr)
	if err == nil || !isUsageError(err) {
		t.Fatalf("RunDefinitionValidate(empty content) error = %v, want a cli.UsageError", err)
	}
}
