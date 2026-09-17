package definitions_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	clidefinitions "github.com/taQuangLing/agent-workflow/internal/delivery/cli/definitions"
)

func TestRunDefinitionPublish_FreshPublish_ReturnsCompiledVersion(t *testing.T) {
	deps := newTestDeps(t)
	mustCreateDefinition(t, deps, "BLOCK", "", "blk-1", "n", "create-1")

	var stdout, stderr bytes.Buffer
	err := clidefinitions.RunDefinitionPublish(context.Background(), deps, []string{"--kind", "BLOCK", "--idempotency-key", "pub-1", "blk-1"}, strings.NewReader(validBlockDocumentJSON), &stdout, &stderr)
	if err != nil {
		t.Fatalf("RunDefinitionPublish() error = %v, stderr = %s", err, stderr.String())
	}
	var envelope struct {
		Replayed bool `json:"replayed"`
		Result   struct {
			VersionNumber uint64 `json:"versionNumber"`
			CompiledHash  string `json:"compiledHash"`
		} `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("decode %s: %v", stdout.String(), err)
	}
	if envelope.Replayed {
		t.Fatal("first publish reported Replayed = true, want false")
	}
	if envelope.Result.VersionNumber != 1 || envelope.Result.CompiledHash == "" {
		t.Fatalf("result = %+v, want VersionNumber=1 and a real CompiledHash", envelope.Result)
	}
}

// TestRunDefinitionPublish_ReplaySameIdempotencyKey_NeverCreatesTwice is
// V6-15E's own "Replay" Verify bullet applied to `aw definition publish`:
// the exact same --idempotency-key resubmitted must return the identical
// stored Version and report Replayed=true, never append a second Version.
func TestRunDefinitionPublish_ReplaySameIdempotencyKey_NeverCreatesTwice(t *testing.T) {
	deps := newTestDeps(t)
	mustCreateDefinition(t, deps, "BLOCK", "", "blk-1", "n", "create-1")
	args := []string{"--kind", "BLOCK", "--idempotency-key", "pub-1", "blk-1"}

	var first bytes.Buffer
	if err := clidefinitions.RunDefinitionPublish(context.Background(), deps, args, strings.NewReader(validBlockDocumentJSON), &first, &bytes.Buffer{}); err != nil {
		t.Fatalf("first publish error = %v", err)
	}
	firstID := jsonField(t, resultField(t, first.String()), "id")

	var second bytes.Buffer
	if err := clidefinitions.RunDefinitionPublish(context.Background(), deps, args, strings.NewReader(validBlockDocumentJSON), &second, &bytes.Buffer{}); err != nil {
		t.Fatalf("second publish error = %v", err)
	}
	var secondEnvelope struct {
		Replayed bool `json:"replayed"`
	}
	if err := json.Unmarshal(second.Bytes(), &secondEnvelope); err != nil {
		t.Fatalf("decode %s: %v", second.String(), err)
	}
	if !secondEnvelope.Replayed {
		t.Fatal("second publish with the identical idempotency key reported Replayed = false, want true")
	}
	secondID := jsonField(t, resultField(t, second.String()), "id")
	if secondID != firstID {
		t.Fatalf("replay id = %q, want the exact original %q", secondID, firstID)
	}

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
	if len(versionsView.Items) != 1 {
		t.Fatalf("versions after a replayed publish = %d, want exactly 1 (replay must never create a second Version)", len(versionsView.Items))
	}
}

func TestRunDefinitionPublish_WrongScope_NotFound(t *testing.T) {
	deps := newTestDeps(t)
	mustCreateDefinition(t, deps, "BLOCK", "proj-a", "blk-1", "n", "create-1")

	var stdout, stderr bytes.Buffer
	err := clidefinitions.RunDefinitionPublish(context.Background(), deps, []string{"--kind", "BLOCK", "--idempotency-key", "pub-1", "blk-1"}, strings.NewReader(validBlockDocumentJSON), &stdout, &stderr)
	if err == nil {
		t.Fatal("RunDefinitionPublish against a project-scoped definition via the global route succeeded, want ErrDefinitionNotFound")
	}
}

func jsonField(t *testing.T, body, field string) string {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("decode JSON body %s: %v", body, err)
	}
	value, _ := raw[field].(string)
	return value
}
