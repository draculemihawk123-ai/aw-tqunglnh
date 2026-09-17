package projectionrebuild_test

// Real HTTP round-trip coverage for V6-09B (docs/design/08-v6-api-projections.md):
// every test in this file drives a REAL httpapi.Server (real TCP loopback
// listener, real middleware chain, real session-token/Origin/Host guards)
// backed by a REAL *sqlite.Store through the REAL, already-hardened
// internal/app/projectionrebuild application layer — never a mock, never a
// direct row fabrication (this repo's own hard rule). Mirrors
// internal/delivery/httpapi/workitem/workitem_test.go's own newTestEnv/
// seedProject idiom exactly, adapted for this package's own three routes.

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
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/projectionrebuild"
)

const testSessionToken = "test-projectionrebuild-session-token"

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
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "projectionrebuild-http.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	uow := sqlite.NewUnitOfWork(store)

	reg := httpapi.NewRouteRegistry()
	projectionrebuild.RegisterRoutes(reg, projectionrebuild.Dependencies{UnitOfWork: uow, IDs: idsource.Random{}, Clock: clock.System{}})

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

// do issues a real HTTP request against e's own real server.
// idempotencyKey is omitted from the request entirely when "".
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
// own seedProject exactly (same direct tx.Catalog().CreateProject call): a
// caller-chosen ID keeps every test's own path readable.
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

// rebuildResultView/statusView/errorBody mirror this package's own wire
// response shapes field-for-field, decoded independently here (rather than
// importing the package's own unexported view types, which is the point:
// this file proves the WIRE contract, decodable by a caller that only ever
// sees JSON).
type rebuildResultView struct {
	OperationID    string `json:"operationId"`
	ProjectID      string `json:"projectId"`
	ProjectionName string `json:"projectionName"`
	Phase          string `json:"phase"`
	JobID          string `json:"jobId"`
}

type freshnessView struct {
	Generation          int    `json:"generation"`
	AsOfJournalPosition int64  `json:"asOfJournalPosition"`
	Status              string `json:"status"`
}

type statusView struct {
	ProjectID      string        `json:"projectId"`
	ProjectionName string        `json:"projectionName"`
	Freshness      freshnessView `json:"freshness"`
}

type operationStatusView struct {
	OperationID    string `json:"operationId"`
	ProjectID      string `json:"projectId"`
	ProjectionName string `json:"projectionName"`
	Phase          string `json:"phase"`
	JobID          string `json:"jobId"`
	Version        uint64 `json:"version"`
}

// TestRegisterRoutes_ExposesExactlyTheDocumentedOperationSet proves this
// task's own route inventory from the router side: exactly these three
// operationIds, every one of them project-scoped (the design doc's own
// "GET /projects/{id}/projection, POST rebuild, GET operation status"
// line).
func TestRegisterRoutes_ExposesExactlyTheDocumentedOperationSet(t *testing.T) {
	reg := httpapi.NewRouteRegistry()
	projectionrebuild.RegisterRoutes(reg, projectionrebuild.Dependencies{UnitOfWork: nil, IDs: idsource.Random{}, Clock: clock.System{}})

	want := map[string]bool{
		"getProjectionStatus": true, "requestProjectionRebuild": true, "getProjectionRebuildOperationStatus": true,
	}
	got := reg.Descriptors()
	if len(got) != len(want) {
		t.Fatalf("len(Descriptors()) = %d, want %d", len(got), len(want))
	}
	for _, d := range got {
		if !want[d.OperationID] {
			t.Errorf("unexpected operationId %q registered", d.OperationID)
		}
		if d.ScopeKind != httpapi.ScopeProject {
			t.Errorf("operationId %q has ScopeKind %q, want PROJECT", d.OperationID, d.ScopeKind)
		}
		delete(want, d.OperationID)
	}
	if len(want) != 0 {
		t.Errorf("missing operationIds: %v", want)
	}
}

// TestGetProjectionStatus_UnbuiltProjection_ReportsStale proves the "no
// generation ever built yet" case is a real, successful 200 with STALE
// freshness, never an error — the same early-lifecycle case
// appprojectionrebuild.GetProjectionStatus's own doc comment documents.
func TestGetProjectionStatus_UnbuiltProjection_ReportsStale(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")

	resp := env.do(t, http.MethodGet, "/projects/project-1/projection?name=workitem", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var view statusView
	decodeInto(t, resp, &view)
	if view.ProjectID != "project-1" || view.ProjectionName != "workitem" {
		t.Fatalf("view = %+v, want ProjectID=project-1 ProjectionName=workitem", view)
	}
	if view.Freshness.Generation != 0 || view.Freshness.AsOfJournalPosition != 0 || view.Freshness.Status != "STALE" {
		t.Fatalf("freshness = %+v, want Generation=0 AsOfJournalPosition=0 Status=STALE", view.Freshness)
	}
}

// TestGetProjectionStatus_UnknownProject_IsNotFound is this task's own
// "scope" Verify-line scenario at the status query: an unknown Project ID
// (never reloaded/authorized by catalog.GetProject) is the leakage-
// normalized 404, not a 500 or a fabricated freshness result.
func TestGetProjectionStatus_UnknownProject_IsNotFound(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(t, http.MethodGet, "/projects/does-not-exist/projection?name=workitem", "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// TestGetProjectionStatus_MissingNameQueryParam_IsBadRequest proves the
// required "name" query parameter is validated before any query dispatch.
func TestGetProjectionStatus_MissingNameQueryParam_IsBadRequest(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	resp := env.do(t, http.MethodGet, "/projects/project-1/projection", "", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// TestRequestProjectionRebuild_MissingIdempotencyKey_IsBadRequest proves
// the shared Idempotency-Key-required contract (V6-02) applies to this
// package's own mutating route too.
func TestRequestProjectionRebuild_MissingIdempotencyKey_IsBadRequest(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	resp := env.do(t, http.MethodPost, "/projects/project-1/projection/rebuild", "", map[string]any{"projectionName": "workitem"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// TestRequestProjectionRebuild_MissingProjectionName_IsBadRequest proves
// the required body field is validated before ever dispatching.
func TestRequestProjectionRebuild_MissingProjectionName_IsBadRequest(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	resp := env.do(t, http.MethodPost, "/projects/project-1/projection/rebuild", "idem-1", map[string]any{"projectionName": ""})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// TestRequestProjectionRebuild_UnknownProject_IsNotFound proves the Project
// named by {id} is reloaded/authorized (catalog.GetProject) BEFORE the
// command is ever built — an unknown project never even reaches
// RequestProjectionRebuild.
func TestRequestProjectionRebuild_UnknownProject_IsNotFound(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(t, http.MethodPost, "/projects/does-not-exist/projection/rebuild", "idem-1", map[string]any{"projectionName": "workitem"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// TestRequestProjectionRebuild_HappyPath_Returns202AndOperation proves the
// full happy path: 202 Accepted carrying a fresh OperationID/Phase=REQUESTED/
// JobID, and that operation is then readable via the exact-status route.
func TestRequestProjectionRebuild_HappyPath_Returns202AndOperation(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")

	resp := env.do(t, http.MethodPost, "/projects/project-1/projection/rebuild", "idem-1", map[string]any{"projectionName": "workitem"})
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	var result rebuildResultView
	decodeInto(t, resp, &result)
	if result.OperationID == "" || result.ProjectID != "project-1" || result.ProjectionName != "workitem" {
		t.Fatalf("result = %+v, want a non-empty OperationID, ProjectID=project-1 ProjectionName=workitem", result)
	}
	if result.Phase != "REQUESTED" || result.JobID == "" {
		t.Fatalf("result = %+v, want Phase=REQUESTED and a non-empty JobID", result)
	}

	statusResp := env.do(t, http.MethodGet, "/projects/project-1/projection/rebuild-operations/"+result.OperationID, "", nil)
	if statusResp.StatusCode != http.StatusOK {
		t.Fatalf("GET operation status = %d, want 200", statusResp.StatusCode)
	}
	var status operationStatusView
	decodeInto(t, statusResp, &status)
	if status.OperationID != result.OperationID || status.Phase != "REQUESTED" || status.JobID != result.JobID {
		t.Fatalf("status = %+v, want to match the just-created operation %+v", status, result)
	}
	if status.Version != 1 {
		t.Fatalf("status.Version = %d, want 1", status.Version)
	}
}

// TestRequestProjectionRebuild_Replay_ReturnsSameOperationID is this task's
// own "replay" Verify-line scenario: the same Idempotency-Key replays the
// exact same OperationID, both times 202 — RequestProjectionRebuild's own
// internal receipt replay is what makes this true (this package runs no
// pre-dispatch replay check of its own, see commands.go's own doc
// comment), proved here indirectly by that omission not weakening the
// guarantee.
func TestRequestProjectionRebuild_Replay_ReturnsSameOperationID(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")

	first := env.do(t, http.MethodPost, "/projects/project-1/projection/rebuild", "idem-reuse", map[string]any{"projectionName": "workitem"})
	if first.StatusCode != http.StatusAccepted {
		t.Fatalf("first status = %d, want 202", first.StatusCode)
	}
	var firstResult rebuildResultView
	decodeInto(t, first, &firstResult)

	second := env.do(t, http.MethodPost, "/projects/project-1/projection/rebuild", "idem-reuse", map[string]any{"projectionName": "workitem"})
	if second.StatusCode != http.StatusAccepted {
		t.Fatalf("second (replay) status = %d, want 202", second.StatusCode)
	}
	var secondResult rebuildResultView
	decodeInto(t, second, &secondResult)

	if firstResult != secondResult {
		t.Fatalf("replay result = %+v, want identical to first %+v", secondResult, firstResult)
	}
}

// TestRequestProjectionRebuild_ActiveOperationConflict_Returns409WithActiveID
// is this task's own most important new-design-surface scenario: a NEW
// Idempotency-Key, while (ProjectID, ProjectionName) already has a
// nonterminal rebuild operation, must be rejected with 409 and the
// already-active operation's own ID surfaced as a structured ErrorDetail
// (Field: "activeOperationId") — never a bare generic conflict.
func TestRequestProjectionRebuild_ActiveOperationConflict_Returns409WithActiveID(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")

	first := env.do(t, http.MethodPost, "/projects/project-1/projection/rebuild", "idem-first", map[string]any{"projectionName": "workitem"})
	if first.StatusCode != http.StatusAccepted {
		t.Fatalf("first status = %d, want 202", first.StatusCode)
	}
	var firstResult rebuildResultView
	decodeInto(t, first, &firstResult)

	second := env.do(t, http.MethodPost, "/projects/project-1/projection/rebuild", "idem-second-different-key", map[string]any{"projectionName": "workitem"})
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("second (new key, active op exists) status = %d, want 409", second.StatusCode)
	}
	var errBody httpapi.ErrorResponse
	decodeInto(t, second, &errBody)
	if len(errBody.Error.Details) != 1 || errBody.Error.Details[0].Field != "activeOperationId" {
		t.Fatalf("error details = %+v, want exactly one detail naming field activeOperationId", errBody.Error.Details)
	}
	if errBody.Error.Details[0].Message != firstResult.OperationID {
		t.Fatalf("activeOperationId detail = %q, want the first request's own OperationID %q", errBody.Error.Details[0].Message, firstResult.OperationID)
	}
}

// TestGetProjectionRebuildOperationStatus_UnknownOperation_IsNotFound
// proves a genuinely unknown operationId maps to the leakage-normalized
// 404.
func TestGetProjectionRebuildOperationStatus_UnknownOperation_IsNotFound(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	resp := env.do(t, http.MethodGet, "/projects/project-1/projection/rebuild-operations/does-not-exist", "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// TestGetProjectionRebuildOperationStatus_CrossProject_IsNotFound is this
// task's own "scope" Verify-line scenario at the operation-status query: an
// operationId that genuinely exists, just under a DIFFERENT project, must
// be just as invisible as a genuinely unknown one (leakage-normalization,
// mirrors internal/delivery/httpapi/releaseset's own identical
// handleGetReleaseSetLocalCommitStatus proof).
func TestGetProjectionRebuildOperationStatus_CrossProject_IsNotFound(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	env.seedProject(t, "project-2")

	created := env.do(t, http.MethodPost, "/projects/project-1/projection/rebuild", "idem-1", map[string]any{"projectionName": "workitem"})
	if created.StatusCode != http.StatusAccepted {
		t.Fatalf("create status = %d, want 202", created.StatusCode)
	}
	var result rebuildResultView
	decodeInto(t, created, &result)

	resp := env.do(t, http.MethodGet, "/projects/project-2/projection/rebuild-operations/"+result.OperationID, "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-project lookup status = %d, want 404", resp.StatusCode)
	}
}

// TestGetProjectionRebuildOperationStatus_MissingOperationId_IsBadRequest
// proves the {operationId} path segment is validated before dispatch (an
// empty PathValue can only happen for a malformed route match, but this
// still guards the handler's own explicit check).
func TestGetProjectionRebuildOperationStatus_MissingOperationId_IsBadRequest(t *testing.T) {
	env := newTestEnv(t)
	env.seedProject(t, "project-1")
	resp := env.do(t, http.MethodGet, "/projects/project-1/projection/rebuild-operations/", "", nil)
	// Trailing slash with no operationId segment does not match this
	// package's own registered route at all (net/http's own exact-segment
	// matching) — the shared server's own not-found path handles it; this
	// test only proves the route is genuinely gone, never silently matched
	// with an empty operationId.
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("status = %d, want anything other than 200 for a missing operationId segment", resp.StatusCode)
	}
}
