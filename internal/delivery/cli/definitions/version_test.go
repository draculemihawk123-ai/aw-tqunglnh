package definitions_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	clidefinitions "github.com/taQuangLing/agent-workflow/internal/delivery/cli/definitions"
)

// TestRunVersionShow_PinsExactVersion_NeverLatest is V6-15E's own "diff/pin
// output" Verify bullet applied to `aw version show`: two distinct
// published Versions of the same Definition must each still resolve, by
// their own immutable VersionID, to their OWN distinct compiled content —
// never silently coalescing to "whichever is newest".
func TestRunVersionShow_PinsExactVersion_NeverLatest(t *testing.T) {
	deps := newTestDeps(t)
	v1 := mustPublishBlock(t, deps, "", "blk-1", "pub-1", validBlockDocumentJSON)
	// A second publish for the SAME definition, different content ->
	// VersionNumber 2, a different VersionID and CompiledHash.
	var stdout2 bytes.Buffer
	if err := clidefinitions.RunDefinitionPublish(context.Background(), deps, []string{"--kind", "BLOCK", "--idempotency-key", "pub-2", "blk-1"}, strings.NewReader(altBlockDocumentJSON), &stdout2, &bytes.Buffer{}); err != nil {
		t.Fatalf("second publish error = %v", err)
	}
	v2 := jsonField(t, resultField(t, stdout2.String()), "id")
	if v1 == v2 {
		t.Fatalf("two distinct publishes produced the same VersionID %q", v1)
	}

	var showV1 bytes.Buffer
	if err := clidefinitions.RunVersionShow(context.Background(), deps, []string{v1}, &showV1, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunVersionShow(v1) error = %v", err)
	}
	var showV2 bytes.Buffer
	if err := clidefinitions.RunVersionShow(context.Background(), deps, []string{v2}, &showV2, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunVersionShow(v2) error = %v", err)
	}

	var view1, view2 struct {
		ID            string `json:"id"`
		VersionNumber uint64 `json:"versionNumber"`
		CompiledHash  string `json:"compiledHash"`
	}
	if err := json.Unmarshal(showV1.Bytes(), &view1); err != nil {
		t.Fatalf("decode v1: %v", err)
	}
	if err := json.Unmarshal(showV2.Bytes(), &view2); err != nil {
		t.Fatalf("decode v2: %v", err)
	}
	if view1.ID != v1 || view2.ID != v2 {
		t.Fatalf("show returned the wrong id: view1.ID=%s (want %s), view2.ID=%s (want %s)", view1.ID, v1, view2.ID, v2)
	}
	if view1.VersionNumber != 1 || view2.VersionNumber != 2 {
		t.Fatalf("VersionNumber = (%d, %d), want (1, 2)", view1.VersionNumber, view2.VersionNumber)
	}
	if view1.CompiledHash == view2.CompiledHash {
		t.Fatal("two genuinely different documents compiled to the same hash")
	}
}

func TestRunVersionShow_WrongScope_NotFound(t *testing.T) {
	deps := newTestDeps(t)
	versionID := mustPublishBlock(t, deps, "proj-a", "blk-1", "pub-1", validBlockDocumentJSON)

	var stdout, stderr bytes.Buffer
	err := clidefinitions.RunVersionShow(context.Background(), deps, []string{versionID}, &stdout, &stderr)
	if !errors.Is(err, clidefinitions.ErrDefinitionVersionNotFound) {
		t.Fatalf("RunVersionShow(project-scoped version via global) error = %v, want ErrDefinitionVersionNotFound", err)
	}
}

// TestRunVersionDiff_Identical proves diff reports Identical=true and an
// all-"equal" SourceDiff for two publishes of byte-identical content.
func TestRunVersionDiff_Identical(t *testing.T) {
	deps := newTestDeps(t)
	a := mustPublishBlock(t, deps, "", "blk-a", "pub-a", validBlockDocumentJSON)
	b := mustPublishBlock(t, deps, "", "blk-b", "pub-b", validBlockDocumentJSON)

	var stdout bytes.Buffer
	if err := clidefinitions.RunVersionDiff(context.Background(), deps, []string{a, b}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunVersionDiff(identical) error = %v", err)
	}
	var view struct {
		Identical  bool `json:"identical"`
		SourceDiff []struct {
			Op string `json:"op"`
		} `json:"sourceDiff"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &view); err != nil {
		t.Fatalf("decode %s: %v", stdout.String(), err)
	}
	if !view.Identical {
		t.Fatalf("identical = false for byte-identical content, got %+v", view)
	}
	for _, line := range view.SourceDiff {
		if line.Op != "equal" {
			t.Fatalf("expected every diff line to be 'equal', got %+v", view.SourceDiff)
		}
	}
}

// TestRunVersionDiff_Different proves diff reports Identical=false and at
// least one add/remove line for genuinely different content — V6-15E's
// own "diff/pin output" Verify bullet's own add/remove/change case.
func TestRunVersionDiff_Different(t *testing.T) {
	deps := newTestDeps(t)
	a := mustPublishBlock(t, deps, "", "blk-a", "pub-a", validBlockDocumentJSON)
	b := mustPublishBlock(t, deps, "", "blk-b", "pub-b", altBlockDocumentJSON)

	var stdout bytes.Buffer
	if err := clidefinitions.RunVersionDiff(context.Background(), deps, []string{a, b}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunVersionDiff(different) error = %v", err)
	}
	var view struct {
		Identical  bool `json:"identical"`
		SourceDiff []struct {
			Op   string `json:"op"`
			Text string `json:"text"`
		} `json:"sourceDiff"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &view); err != nil {
		t.Fatalf("decode %s: %v", stdout.String(), err)
	}
	if view.Identical {
		t.Fatal("identical = true for genuinely different content, want false")
	}
	hasAdd, hasRemove := false, false
	for _, line := range view.SourceDiff {
		switch line.Op {
		case "add":
			hasAdd = true
		case "remove":
			hasRemove = true
		}
	}
	if !hasAdd || !hasRemove {
		t.Fatalf("expected at least one add and one remove line, got %+v", view.SourceDiff)
	}
}

func TestRunVersionDiff_CrossScope_NotFound(t *testing.T) {
	deps := newTestDeps(t)
	a := mustPublishBlock(t, deps, "", "blk-a", "pub-a", validBlockDocumentJSON)
	b := mustPublishBlock(t, deps, "proj-a", "blk-b", "pub-b", validBlockDocumentJSON)

	var stdout, stderr bytes.Buffer
	err := clidefinitions.RunVersionDiff(context.Background(), deps, []string{a, b}, &stdout, &stderr)
	if !errors.Is(err, clidefinitions.ErrDefinitionVersionNotFound) {
		t.Fatalf("RunVersionDiff(cross-scope) error = %v, want ErrDefinitionVersionNotFound", err)
	}
}

func TestRunVersionDiff_CrossKind_UsageError(t *testing.T) {
	deps := newTestDeps(t)
	a := mustPublishBlock(t, deps, "", "blk-a", "pub-a", validBlockDocumentJSON)
	mustCreateDefinition(t, deps, "SKILL", "", "skl-1", "n", "create-skl")
	var stdout bytes.Buffer
	if err := clidefinitions.RunDefinitionPublish(context.Background(), deps, []string{"--kind", "SKILL", "--idempotency-key", "pub-skl", "skl-1"}, strings.NewReader(validSkillDocumentJSON), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("publish skill error = %v", err)
	}
	skillVersionID := jsonField(t, resultField(t, stdout.String()), "id")

	var diffOut, diffErr bytes.Buffer
	err := clidefinitions.RunVersionDiff(context.Background(), deps, []string{a, skillVersionID}, &diffOut, &diffErr)
	if err == nil || !isUsageError(err) {
		t.Fatalf("RunVersionDiff(cross-kind) error = %v, want a cli.UsageError", err)
	}
}

func TestRunVersionDiff_WrongArgCount_IsUsageError(t *testing.T) {
	deps := newTestDeps(t)
	var stdout, stderr bytes.Buffer
	err := clidefinitions.RunVersionDiff(context.Background(), deps, []string{"only-one"}, &stdout, &stderr)
	if err == nil || !isUsageError(err) {
		t.Fatalf("RunVersionDiff(one arg) error = %v, want a cli.UsageError", err)
	}
}

const validSkillDocumentJSON = `{
  "resources": [
    {
      "key": "go-error-wrapping",
      "instruction": "wrap errors with fmt.Errorf",
      "priority": "HARD_CONSTRAINT",
      "selector": {"componentTags": ["backend"], "taskKinds": ["code-change"]},
      "provenance": {"owner": "platform-team", "source": "docs/style/go-errors.md", "lastVerified": "2026-01-01T00:00:00Z"}
    }
  ]
}`
