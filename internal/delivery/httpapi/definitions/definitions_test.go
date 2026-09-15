package definitions_test

// Real HTTP round-trip coverage for V6-05 (docs/design/08-v6-api-projections.md):
// every test in this file (and its siblings) drives a REAL httpapi.Server
// (real TCP loopback listener, real middleware chain, real session-token/
// Origin/Host guards) backed by a REAL *sqlite.Store — never a mock, never
// a direct row fabrication — mirroring
// internal/delivery/httpapi/workitem/workitem_test.go's own newTestEnv
// idiom exactly.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/logging"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	definitionshttp "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/definitions"
)

const testSessionToken = "test-definitions-session-token"

func testPrincipal() httpapi.LocalPrincipalSnapshot {
	return httpapi.LocalPrincipalSnapshot{Actor: "local-operator", Roles: []string{"operator"}}
}

// testEnv is one real Server + real UnitOfWork pair, torn down via
// t.Cleanup.
type testEnv struct {
	server *httpapi.Server
	base   string
	client *http.Client
	uow    ports.UnitOfWork
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "definitions-http.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	uow := sqlite.NewUnitOfWork(store)

	reg := httpapi.NewRouteRegistry()
	definitionshttp.RegisterRoutes(reg, definitionshttp.Dependencies{UnitOfWork: uow, IDs: idsource.Random{}, Clock: clock.System{}})

	server, err := httpapi.NewServer(httpapi.Config{
		Host: "127.0.0.1", Port: 0, Routes: reg, IDs: idsource.Random{},
		Logger:       logging.New(io.Discard, logging.JSON, redact.NewMatcher()),
		MaxBodyBytes: 1 << 20, Token: testSessionToken, Principal: testPrincipal(),
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve() }()
	t.Cleanup(func() {
		if err := server.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
		if err := <-done; err != nil {
			t.Errorf("Serve returned error after Shutdown: %v", err)
		}
	})

	return &testEnv{server: server, base: "http://" + server.Addr(), client: &http.Client{Timeout: 10 * time.Second}, uow: uow}
}

// do issues a real HTTP request against e's own real server. idempotencyKey
// is omitted from the request entirely when "".
func (e *testEnv) do(t *testing.T, method, path, idempotencyKey string, body any) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.base+path, reader)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set(httpapi.SessionTokenHeader, testSessionToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		req.Header.Set(httpapi.IdempotencyKeyHeader, idempotencyKey)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func decodeInto(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
}

// seedProject mirrors internal/delivery/httpapi/workitem/workitem_test.go's
// own seedProject exactly: fixture setup for a concern (Project catalog)
// this task does not itself own, via a direct tx.Catalog().CreateProject
// call, never a route this package exposes.
func (e *testEnv) seedProject(t *testing.T, id string) {
	t.Helper()
	err := e.uow.WithSerializedWrite(context.Background(), func(tx ports.Tx) error {
		_, err := tx.Catalog().CreateProject(context.Background(), ports.CreateProjectRequest{ID: id, Name: "project " + id})
		return err
	})
	if err != nil {
		t.Fatalf("seed project %s: %v", id, err)
	}
}

// validBlockDocumentJSON is a minimal, structurally valid BlockDocument
// (mirrors internal/domain/block/validate_test.go's own validDocument) —
// used across this package's own tests as the "happy path" shared-kind
// document, since block.ValidateDocument/Compile never resolves its own
// ExecutorRef/PolicyRefs pins against the real registry (V2-11's own
// established scope boundary), so no dependency fixtures need to exist for
// this to compile successfully.
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

// invalidBlockDocumentJSON is missing every required field
// block.ValidateDocument checks (no compatibleNodeTypes, no outcomes, no
// executorRef, zero timeout) — used to prove the location-diagnostics
// Verify bullet: the 400 response must carry real per-field ErrorDetail
// entries, not just one generic message.
const invalidBlockDocumentJSON = `{}`

// minimalWorkflowDocumentJSON is a trivial START->END WorkflowDocument with
// no AGENT/COMMAND/... work nodes at all — deliberately: a work node would
// need a real, already-published BlockVersion/AgentProfile pin for
// workflowcompiler.CompileAndResolve's own registry resolution to succeed
// against, which is real fixture-seeding work orthogonal to what this
// smoke test needs to prove (that this package's own Kind==WORKFLOW branch
// dispatches through CompileAndResolve/workflow.Compile correctly end to
// end via real HTTP). Zero work nodes means collectReferences finds zero
// references to resolve, so this compiles with no registry fixtures.
const minimalWorkflowDocumentJSON = `{
  "schemaVersion": "1",
  "nodes": [
    {"key": "start", "type": "START", "outcomes": ["next"]},
    {"key": "end", "type": "END"}
  ],
  "edges": [
    {"key": "edge-1", "from": "start", "outcome": "next", "to": "end"}
  ]
}`

// invalidWorkflowDocumentJSON has no START/END node and one dangling edge
// — used to prove the Workflow-only *workflow.ValidationError mapping path
// (writeCommandError's own "no source position" branch) is reachable and
// carries real per-problem ErrorDetail entries, all via real HTTP, with no
// registry fixtures needed (structural validation runs before any
// database read).
const invalidWorkflowDocumentJSON = `{
  "schemaVersion": "1",
  "nodes": [
    {"key": "orphan", "type": "AGENT", "outcomes": ["ok"]}
  ],
  "edges": []
}`
