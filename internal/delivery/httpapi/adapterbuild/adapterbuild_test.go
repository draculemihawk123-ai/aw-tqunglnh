package adapterbuild_test

// Real HTTP round-trip coverage for V6-10J (docs/design/08-v6-api-projections.md):
// every test in this file drives a REAL httpapi.Server (real TCP loopback
// listener, real middleware chain, real session-token/Origin/Host guards)
// backed by a REAL *sqlite.Store through the REAL, already-hardened
// internal/app/adapterbuild application commands — never a mock, never a
// direct row fabrication (this repo's own hard rule). Mirrors
// internal/delivery/httpapi/workitem's own workitem_test.go newTestEnv idiom
// exactly, adapted for this package's own installation-scoped routes.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
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
	httpadapterbuild "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/adapterbuild"
	domainadapterbuild "github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
)

const testSessionToken = "test-adapterbuild-session-token"

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
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "adapterbuild-http.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	uow := sqlite.NewUnitOfWork(store)

	reg := httpapi.NewRouteRegistry()
	httpadapterbuild.RegisterRoutes(reg, httpadapterbuild.Dependencies{UnitOfWork: uow, Clock: clock.System{}})

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

// writeExecutable writes content to a fresh temp file and returns its path
// — the one real executable fingerprint helper every probe/register test
// below hashes, mirroring internal/app/adapterbuild/commands_test.go's own
// identical writeExecutable helper exactly.
func writeExecutable(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "provider-cli")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable fixture: %v", err)
	}
	return path
}

func validManifestJSON() map[string]any {
	return map[string]any{
		"supportsStart": true, "supportsResume": true, "supportsCancel": true,
		"canonicalEventKinds": []string{"TEXT_DELTA", "TOOL_CALL"},
	}
}

func probeBodyJSON(executablePath string) map[string]any {
	return map[string]any{
		"providerKey": "claude", "executablePath": executablePath, "protocolVersion": "claude-stream-json/v1",
		"capabilityManifest": validManifestJSON(), "os": "linux", "toolchain": "node-20", "configIdentity": "default",
	}
}

// TestRegisterRoutes_ExposesExactlyTheDocumentedOperationSet proves this
// task's own route inventory from the router side: exactly these four
// operationIds, every one of them installation-scoped (ADR-025's own closed
// table; this task's own "Không làm: no project mirror" line) — the "scope
// schema" half of this task's own Verify bullet.
func TestRegisterRoutes_ExposesExactlyTheDocumentedOperationSet(t *testing.T) {
	reg := httpapi.NewRouteRegistry()
	httpadapterbuild.RegisterRoutes(reg, httpadapterbuild.Dependencies{UnitOfWork: nil, Clock: clock.System{}})

	want := map[string]bool{
		"listAdapterBuilds": true, "getAdapterBuild": true, "probeAdapterBuild": true, "registerAdapterBuild": true,
	}
	got := reg.Descriptors()
	if len(got) != len(want) {
		t.Fatalf("len(Descriptors()) = %d, want %d", len(got), len(want))
	}
	for _, d := range got {
		if !want[d.OperationID] {
			t.Errorf("unexpected operationId %q registered", d.OperationID)
		}
		if d.ScopeKind != httpapi.ScopeInstallation {
			t.Errorf("operationId %q has ScopeKind %q, want INSTALLATION (ADR-022/ADR-025: AdapterBuildVersion is a machine-level resource, never project-scoped)", d.OperationID, d.ScopeKind)
		}
		delete(want, d.OperationID)
	}
	if len(want) != 0 {
		t.Errorf("missing operationIds: %v", want)
	}
}

// TestFullJourney_ProbeRegisterListGet is this task's own end-to-end happy
// path (this task's own "Hoàn thành khi": "provider upgrade flow ... usable
// without bypassing command contract"): probe -> review candidate ->
// register -> list -> detail, entirely through real HTTP.
func TestFullJourney_ProbeRegisterListGet(t *testing.T) {
	env := newTestEnv(t)
	executablePath := writeExecutable(t, "binary-content-v1")

	// 1. Probe.
	probeResp := env.do(t, http.MethodPost, "/adapter-builds/probe", "idem-probe-1", probeBodyJSON(executablePath))
	if probeResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /adapter-builds/probe status = %d, want 200", probeResp.StatusCode)
	}
	var token domainadapterbuild.CandidateToken
	decodeInto(t, probeResp, &token)
	if token.Tuple.ProviderKey != "claude" || token.Tuple.ExecutablePath != executablePath {
		t.Fatalf("candidate token tuple = %+v, want ProviderKey=claude ExecutablePath=%s", token.Tuple, executablePath)
	}
	if token.Signature == "" || token.Nonce == "" || token.ExpiresAt.IsZero() {
		t.Fatalf("candidate token missing signature/nonce/expiry: %+v", token)
	}

	// 2. Register, confirming the exact candidate just reviewed.
	registerBody := map[string]any{"token": token, "capabilityManifest": validManifestJSON()}
	registerResp := env.do(t, http.MethodPost, "/adapter-builds", "idem-register-1", registerBody)
	if registerResp.StatusCode != http.StatusCreated {
		t.Fatalf("POST /adapter-builds status = %d, want 201", registerResp.StatusCode)
	}
	var registerResult struct {
		Build struct {
			ID                    string `json:"id"`
			ProviderKey           string `json:"providerKey"`
			ExecutablePath        string `json:"executablePath"`
			ExecutableContentHash string `json:"executableContentHash"`
			ProtocolVersion       string `json:"protocolVersion"`
			ConfigIdentity        string `json:"configIdentity"`
			RegisteredBy          string `json:"registeredBy"`
		} `json:"build"`
		AlreadyExisted bool `json:"alreadyExisted"`
	}
	decodeInto(t, registerResp, &registerResult)
	if registerResult.AlreadyExisted {
		t.Fatal("first registration should not report AlreadyExisted")
	}
	if registerResult.Build.ID == "" {
		t.Fatal("registered build should have a non-empty ID")
	}
	if registerResult.Build.RegisteredBy != "local-operator" {
		t.Fatalf("RegisteredBy = %q, want local-operator (derived from the bound principal, never a request field)", registerResult.Build.RegisteredBy)
	}
	if registerResult.Build.ProtocolVersion != "claude-stream-json/v1" || registerResult.Build.ConfigIdentity != "default" {
		t.Fatalf("build tuple fields did not round-trip: %+v", registerResult.Build)
	}

	// 3. List.
	listResp := env.do(t, http.MethodGet, "/adapter-builds", "", nil)
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /adapter-builds status = %d, want 200", listResp.StatusCode)
	}
	var listResult struct {
		Builds []struct {
			ID string `json:"id"`
		} `json:"builds"`
	}
	decodeInto(t, listResp, &listResult)
	if len(listResult.Builds) != 1 || listResult.Builds[0].ID != registerResult.Build.ID {
		t.Fatalf("list result = %+v, want exactly one build with ID %s", listResult, registerResult.Build.ID)
	}

	// 4. Detail.
	detailResp := env.do(t, http.MethodGet, "/adapter-builds/"+registerResult.Build.ID, "", nil)
	if detailResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /adapter-builds/{id} status = %d, want 200", detailResp.StatusCode)
	}
	var detail struct {
		ID                    string `json:"id"`
		ExecutableContentHash string `json:"executableContentHash"`
	}
	decodeInto(t, detailResp, &detail)
	if detail.ID != registerResult.Build.ID || detail.ExecutableContentHash != registerResult.Build.ExecutableContentHash {
		t.Fatalf("detail = %+v, want to match registered build %+v", detail, registerResult.Build)
	}
}

// TestRegister_DuplicateFingerprintReturns200AlreadyExisted proves the
// content-hash fingerprint dedup axis is visible over HTTP as a 200 (not
// 201, and not an error): registering the exact same measured executable
// twice through two entirely independent probe+register command pairs.
func TestRegister_DuplicateFingerprintReturns200AlreadyExisted(t *testing.T) {
	env := newTestEnv(t)
	executablePath := writeExecutable(t, "binary-content-v1")

	probeAndRegister := func(idemSuffix string) (buildID string, status int) {
		probeResp := env.do(t, http.MethodPost, "/adapter-builds/probe", "idem-probe-"+idemSuffix, probeBodyJSON(executablePath))
		if probeResp.StatusCode != http.StatusOK {
			t.Fatalf("probe (%s) status = %d, want 200", idemSuffix, probeResp.StatusCode)
		}
		var token domainadapterbuild.CandidateToken
		decodeInto(t, probeResp, &token)

		registerResp := env.do(t, http.MethodPost, "/adapter-builds", "idem-register-"+idemSuffix, map[string]any{
			"token": token, "capabilityManifest": validManifestJSON(),
		})
		var result struct {
			Build struct {
				ID string `json:"id"`
			} `json:"build"`
			AlreadyExisted bool `json:"alreadyExisted"`
		}
		decodeInto(t, registerResp, &result)
		if idemSuffix == "2" && !result.AlreadyExisted {
			t.Fatal("second, independent registration of the identical executable should report AlreadyExisted")
		}
		return result.Build.ID, registerResp.StatusCode
	}

	firstID, firstStatus := probeAndRegister("1")
	if firstStatus != http.StatusCreated {
		t.Fatalf("first register status = %d, want 201", firstStatus)
	}
	secondID, secondStatus := probeAndRegister("2")
	if secondStatus != http.StatusOK {
		t.Fatalf("second (duplicate) register status = %d, want 200", secondStatus)
	}
	if firstID != secondID {
		t.Fatalf("duplicate registration produced a different build ID: %s vs %s", firstID, secondID)
	}
}

// TestProbe_MissingIdempotencyKeyIsBadRequest proves the shared
// Idempotency-Key-required contract (V6-02) applies to this package's own
// mutating routes too.
func TestProbe_MissingIdempotencyKeyIsBadRequest(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(t, http.MethodPost, "/adapter-builds/probe", "", probeBodyJSON(writeExecutable(t, "content")))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// TestProbe_MissingRequiredFieldIsBadRequest is the "protocol/config schema"
// half of this task's own Verify bullet: every required probeAdapterBuildBody
// field, removed one at a time, must produce a precise 400 with a
// field-level ErrorDetail naming exactly that field.
func TestProbe_MissingRequiredFieldIsBadRequest(t *testing.T) {
	fields := []string{"providerKey", "executablePath", "protocolVersion", "os", "toolchain", "configIdentity"}
	for _, field := range fields {
		t.Run(field, func(t *testing.T) {
			env := newTestEnv(t)
			body := probeBodyJSON(writeExecutable(t, "content"))
			delete(body, field)
			body[field] = ""

			resp := env.do(t, http.MethodPost, "/adapter-builds/probe", "idem-"+field, body)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", resp.StatusCode)
			}
			var errBody httpapi.ErrorResponse
			decodeInto(t, resp, &errBody)
			if len(errBody.Error.Details) != 1 || errBody.Error.Details[0].Field != field {
				t.Fatalf("error details = %+v, want exactly one detail naming field %q", errBody.Error.Details, field)
			}
		})
	}
}

// TestProbe_InvalidCapabilityManifestIsBadRequest proves
// domainadapterbuild.ValidateCapabilityManifest's own real rule
// (SupportsStart must be true) is enforced before ever dispatching.
func TestProbe_InvalidCapabilityManifestIsBadRequest(t *testing.T) {
	env := newTestEnv(t)
	body := probeBodyJSON(writeExecutable(t, "content"))
	body["capabilityManifest"] = map[string]any{"supportsStart": false}

	resp := env.do(t, http.MethodPost, "/adapter-builds/probe", "idem-bad-manifest", body)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// TestProbe_SameIdempotencyKeyDifferentBodyIsConflict proves
// ProbeAdapterBuild's own internal receipt recheck (V6-10I) is what actually
// catches an idempotency-key reuse with a different payload — this package
// deliberately runs no pre-dispatch receipt check of its own (adapterbuild.go's
// own doc comment), so this test is also, indirectly, proof that omission
// does not weaken the guarantee.
func TestProbe_SameIdempotencyKeyDifferentBodyIsConflict(t *testing.T) {
	env := newTestEnv(t)
	first := env.do(t, http.MethodPost, "/adapter-builds/probe", "idem-reuse", probeBodyJSON(writeExecutable(t, "content-a")))
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first probe status = %d, want 200", first.StatusCode)
	}
	second := env.do(t, http.MethodPost, "/adapter-builds/probe", "idem-reuse", probeBodyJSON(writeExecutable(t, "content-b")))
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("second probe (same key, different body) status = %d, want 409", second.StatusCode)
	}
}

// TestRegister_InvalidTokenSignatureIsBadRequest is the "token schema" half
// of this task's own Verify bullet: a register request carrying a
// structurally well-formed but unsigned/forged token must fail
// domainadapterbuild.VerifyToken with ErrInvalidSignature, mapped to 400.
func TestRegister_InvalidTokenSignatureIsBadRequest(t *testing.T) {
	env := newTestEnv(t)
	// Bootstrap a real signing key first (RegisterAdapterBuild never creates
	// one itself — ports.ErrNoSigningKey would otherwise fire first and mask
	// the signature check this test targets).
	probeResp := env.do(t, http.MethodPost, "/adapter-builds/probe", "idem-bootstrap-key", probeBodyJSON(writeExecutable(t, "content")))
	if probeResp.StatusCode != http.StatusOK {
		t.Fatalf("bootstrap probe status = %d, want 200", probeResp.StatusCode)
	}
	var token domainadapterbuild.CandidateToken
	decodeInto(t, probeResp, &token)
	token.Signature = "not-the-real-signature"

	resp := env.do(t, http.MethodPost, "/adapter-builds", "idem-forged-token", map[string]any{
		"token": token, "capabilityManifest": validManifestJSON(),
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// TestRegister_ExpiredTokenIsConflict signs a genuinely valid, but already
// expired, CandidateToken under this installation's own real signing key
// (loaded directly off uow after a bootstrap probe, then signed via the
// real domainadapterbuild.SignToken — never a hand-fabricated signature)
// and proves RegisterAdapterBuild's real VerifyToken call rejects it with
// ErrTokenExpired, mapped to 409.
func TestRegister_ExpiredTokenIsConflict(t *testing.T) {
	env := newTestEnv(t)
	executablePath := writeExecutable(t, "content")

	bootstrap := env.do(t, http.MethodPost, "/adapter-builds/probe", "idem-bootstrap-key-2", probeBodyJSON(executablePath))
	if bootstrap.StatusCode != http.StatusOK {
		t.Fatalf("bootstrap probe status = %d, want 200", bootstrap.StatusCode)
	}
	var fresh domainadapterbuild.CandidateToken
	decodeInto(t, bootstrap, &fresh)

	var key []byte
	if err := env.uow.WithReadOnly(context.Background(), func(tx ports.Tx) error {
		loaded, err := tx.AdapterBuilds().LoadSigningKey(context.Background())
		key = loaded
		return err
	}); err != nil {
		t.Fatalf("LoadSigningKey: %v", err)
	}

	expired, err := domainadapterbuild.SignToken(fresh.Tuple, "expired-nonce", time.Now().UTC().Add(-time.Hour), key)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}

	resp := env.do(t, http.MethodPost, "/adapter-builds", "idem-expired-token", map[string]any{
		"token": expired, "capabilityManifest": validManifestJSON(),
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
}

// TestRegister_NoSigningKeyIsConflict proves RegisterAdapterBuild called on
// a fresh installation that has never run a single probe (so
// LoadOrCreateSigningKey has never run, and RegisterAdapterBuild itself
// only ever calls the non-bootstrapping LoadSigningKey) fails
// ports.ErrNoSigningKey, mapped to 409 — before signature verification is
// even reachable.
func TestRegister_NoSigningKeyIsConflict(t *testing.T) {
	env := newTestEnv(t)
	zeroToken := domainadapterbuild.CandidateToken{}
	resp := env.do(t, http.MethodPost, "/adapter-builds", "idem-no-key", map[string]any{
		"token": zeroToken, "capabilityManifest": validManifestJSON(),
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
}

// TestRegister_ExecutableDriftIsConflict is the "fingerprint schema" half of
// this task's own Verify bullet: the executable is overwritten AFTER probe
// but BEFORE register, so RegisterAdapterBuild's own real re-hash
// (ADR-022's TOCTOU-closing step) observes a different content hash than
// the token pinned.
func TestRegister_ExecutableDriftIsConflict(t *testing.T) {
	env := newTestEnv(t)
	executablePath := writeExecutable(t, "original-content")

	probeResp := env.do(t, http.MethodPost, "/adapter-builds/probe", "idem-drift-probe", probeBodyJSON(executablePath))
	if probeResp.StatusCode != http.StatusOK {
		t.Fatalf("probe status = %d, want 200", probeResp.StatusCode)
	}
	var token domainadapterbuild.CandidateToken
	decodeInto(t, probeResp, &token)

	if err := os.WriteFile(executablePath, []byte("swapped-content"), 0o755); err != nil {
		t.Fatalf("swap executable content: %v", err)
	}

	resp := env.do(t, http.MethodPost, "/adapter-builds", "idem-drift-register", map[string]any{
		"token": token, "capabilityManifest": validManifestJSON(),
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
}

// TestGetAdapterBuild_UnknownIdIsNotFound proves ports.ErrAdapterBuildNotFound
// maps through the shared WriteResourceHidden funnel.
func TestGetAdapterBuild_UnknownIdIsNotFound(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(t, http.MethodGet, "/adapter-builds/does-not-exist", "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// TestListAdapterBuilds_EmptyRegistryReturnsEmptyList proves the list route
// never errors on a fresh installation with no registered builds.
func TestListAdapterBuilds_EmptyRegistryReturnsEmptyList(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(t, http.MethodGet, "/adapter-builds", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var result struct {
		Builds []any `json:"builds"`
	}
	decodeInto(t, resp, &result)
	if len(result.Builds) != 0 {
		t.Fatalf("builds = %+v, want empty", result.Builds)
	}
}
