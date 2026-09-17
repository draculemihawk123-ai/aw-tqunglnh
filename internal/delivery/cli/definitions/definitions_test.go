package definitions_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/ports/fake"
	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	clidefinitions "github.com/taQuangLing/agent-workflow/internal/delivery/cli/definitions"
)

// newTestDeps builds a fresh in-memory clidefinitions.Dependencies for one
// test — a fake.UnitOfWork (internal/app/ports/fake), a deterministic
// idsource.Sequential and a fixed Now, mirroring
// internal/delivery/cli/catalog's own newTestDeps idiom exactly. Every
// test in this package gets its own isolated UnitOfWork — never shared
// state between tests.
func newTestDeps(t *testing.T) clidefinitions.Dependencies {
	t.Helper()
	fixedNow := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	return clidefinitions.Dependencies{
		UoW: fake.New(),
		IDs: idsource.NewSequential("id"),
		Now: func() time.Time { return fixedNow },
	}
}

// mustCreateDefinition runs `aw definition create` for real (never a
// direct Definitions() insert) — shared setup for every show/versions/
// validate/publish test in this package.
func mustCreateDefinition(t *testing.T, deps clidefinitions.Dependencies, kind, projectID, definitionID, name, idempotencyKey string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	stdin := strings.NewReader(`{"definitionId":"` + definitionID + `","name":"` + name + `"}`)
	args := []string{"--kind", kind, "--idempotency-key", idempotencyKey}
	if projectID != "" {
		args = append(args, "--project-id", projectID)
	}
	if err := clidefinitions.RunDefinitionCreate(context.Background(), deps, args, stdin, &stdout, &stderr); err != nil {
		t.Fatalf("RunDefinitionCreate(%s) error = %v, stderr = %s", definitionID, err, stderr.String())
	}
}

// resultField extracts the top-level "result" object from a
// cli.ResultEnvelope-shaped JSON document and returns it re-encoded as its
// own JSON string.
func resultField(t *testing.T, body string) string {
	t.Helper()
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("decode ResultEnvelope %s: %v", body, err)
	}
	return string(envelope.Result)
}

func isUsageError(err error) bool {
	return cli.IsUsageError(err)
}

// validBlockDocumentJSON/invalidBlockDocumentJSON mirror
// internal/delivery/httpapi/definitions/definitions_test.go's own
// identically-named constants exactly — the same minimal, structurally
// valid (and deliberately empty/invalid) BlockDocument fixtures, reused
// here so this package's own validate/publish tests exercise the real
// block.ValidateDocument/CompileFrom path end to end, never a mock.
const validBlockDocumentJSON = `{
  "compatibleNodeTypes": ["AGENT"],
  "requiredCapabilities": ["INTEGRATION_MULTI_REPOSITORY_WRITE"],
  "timeoutSeconds": 60,
  "scopeSelector": {"access": "WRITE", "pathScopes": ["src/**"]},
  "doneCondition": "result.outcome",
  "executorRef": {"kind": "COMMAND", "definitionId": "cmd-1", "versionId": "cmd-1-v1"},
  "policyRefs": [{"kind": "POLICY", "definitionId": "policy-1", "versionId": "policy-1-v1"}],
  "outcomes": ["success", "failure"]
}`

const invalidBlockDocumentJSON = `{}`

// altBlockDocumentJSON is validBlockDocumentJSON with a genuinely
// different ExecutorRef/timeout, used by version-diff tests to prove a
// real add/remove/change diff — mirrors
// internal/delivery/httpapi/definitions/version_diff_test.go's own
// altBlock fixture.
const altBlockDocumentJSON = `{
  "compatibleNodeTypes": ["COMMAND"],
  "requiredCapabilities": [],
  "timeoutSeconds": 30,
  "scopeSelector": {"access": "READ", "pathScopes": ["docs/**"]},
  "doneCondition": "result.ok",
  "executorRef": {"kind": "COMMAND", "definitionId": "cmd-2", "versionId": "cmd-2-v1"},
  "outcomes": ["done"]
}`

// mustPublishBlock creates and publishes a BLOCK definition in one step,
// returning the published VersionID — shared setup for version show/diff
// tests.
func mustPublishBlock(t *testing.T, deps clidefinitions.Dependencies, projectID, definitionID, idempotencyKey, content string) string {
	t.Helper()
	mustCreateDefinition(t, deps, "BLOCK", projectID, definitionID, definitionID, "create-"+definitionID)

	var stdout, stderr bytes.Buffer
	stdin := strings.NewReader(content)
	args := []string{"--kind", "BLOCK", "--idempotency-key", idempotencyKey}
	if projectID != "" {
		args = append(args, "--project-id", projectID)
	}
	if err := clidefinitions.RunDefinitionPublish(context.Background(), deps, append(args, definitionID), stdin, &stdout, &stderr); err != nil {
		t.Fatalf("RunDefinitionPublish(%s) error = %v, stderr = %s", definitionID, err, stderr.String())
	}
	var view struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(resultField(t, stdout.String())), &view); err != nil {
		t.Fatalf("decode publish result %s: %v", stdout.String(), err)
	}
	return view.ID
}
