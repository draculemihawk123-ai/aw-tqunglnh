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

func TestRunDefinitionVersions_ReturnsPublishedVersionsOldestFirst(t *testing.T) {
	deps := newTestDeps(t)
	mustCreateDefinition(t, deps, "BLOCK", "", "blk-1", "n", "create-1")

	// Publish two distinct contents so two distinct versions exist.
	if err := clidefinitions.RunDefinitionPublish(context.Background(), deps, []string{"--kind", "BLOCK", "--idempotency-key", "pub-1", "blk-1"}, strings.NewReader(validBlockDocumentJSON), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("first publish error = %v", err)
	}
	if err := clidefinitions.RunDefinitionPublish(context.Background(), deps, []string{"--kind", "BLOCK", "--idempotency-key", "pub-2", "blk-1"}, strings.NewReader(altBlockDocumentJSON), &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("second publish error = %v", err)
	}

	var stdout bytes.Buffer
	if err := clidefinitions.RunDefinitionVersions(context.Background(), deps, []string{"--kind", "BLOCK", "blk-1"}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunDefinitionVersions() error = %v", err)
	}
	var view struct {
		Items []struct {
			VersionNumber uint64 `json:"versionNumber"`
		} `json:"items"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &view); err != nil {
		t.Fatalf("decode %s: %v", stdout.String(), err)
	}
	if len(view.Items) != 2 || view.Items[0].VersionNumber != 1 || view.Items[1].VersionNumber != 2 {
		t.Fatalf("items = %+v, want [1, 2] oldest first", view.Items)
	}
}

func TestRunDefinitionVersions_WrongScope_NotFound(t *testing.T) {
	deps := newTestDeps(t)
	mustCreateDefinition(t, deps, "BLOCK", "proj-a", "blk-1", "n", "create-1")

	var stdout, stderr bytes.Buffer
	err := clidefinitions.RunDefinitionVersions(context.Background(), deps, []string{"--kind", "BLOCK", "blk-1"}, &stdout, &stderr)
	if !errors.Is(err, clidefinitions.ErrDefinitionNotFound) {
		t.Fatalf("RunDefinitionVersions(wrong scope) error = %v, want ErrDefinitionNotFound", err)
	}
}
