package doctor_test

// Real HTTP round-trip coverage for V6-10A (docs/design/08-v6-api-projections.md):
// every test in this file drives a REAL httpapi.Server (real TCP loopback
// listener, real middleware chain, real session-token/Origin/Host guards)
// backed by a REAL *sqlite.Store, the REAL internal/app/doctor.Run, and the
// REAL production internal/adapters/process.IsolationChecker — never a
// mock, never a direct row fabrication (this repo's own hard rule; mirrors
// internal/delivery/httpapi/safesettings/safesettings_test.go's own
// newTestEnv idiom).

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/process"
	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/config"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/logging"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	safesettingsapp "github.com/taQuangLing/agent-workflow/internal/app/safesettings"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	httpdoctor "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/doctor"
	httpsafesettings "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/safesettings"
	"github.com/taQuangLing/agent-workflow/internal/domain/safesettings"
)

const testSessionToken = "test-doctor-session-token"

const validDesiredBody = `{
	"managedWorkspaceRoot":"data/workspaces",
	"managedArtifactRoot":"data/artifacts",
	"evidenceRetention":"168h0m0s",
	"processOutputLimit":1048576,
	"providerExecutablePath":"C:/tools/claude/claude.exe",
	"providerDefaultModel":"claude-sonnet-4-5",
	"providerCredentialRef":"keychain:doctor-test-secret-xyz"
}`

const secretCredentialRef = "keychain:doctor-test-secret-xyz"

func testPrincipal() httpapi.LocalPrincipalSnapshot {
	return httpapi.LocalPrincipalSnapshot{Actor: "local-operator", Roles: []string{"operator"}}
}

func defaultEffective() safesettingsapp.Effective {
	return safesettingsapp.ResolveEffective(
		safesettingsapp.Defaults(), safesettingsapp.StartupOverrides{}, safesettings.SafeSettings{},
		safesettingsapp.StartupOverrides{}, safesettingsapp.StartupOverrides{},
	)
}

// testEnv is one real Server + real *sqlite.Store pair, torn down via
// t.Cleanup. store is exposed directly (not just uow) so a test can close
// it out from under a running server to drive a real "database unreachable"
// BLOCKED scenario.
type testEnv struct {
	base   string
	client *http.Client
	store  *sqlite.Store
	uow    ports.UnitOfWork
}

// newTestEnv opens a fresh real database, registers GET /doctor (and, when
// withSafeSettings, GET/PUT /settings/safe too — needed for the
// restartRequired and secret-redaction scenarios below, which both need a
// real place to persist a desired document) over it, and serves both
// through one real httpapi.Server.
func newTestEnv(t *testing.T, cfg config.Config, isolation ports.IsolationEnforcementChecker, withSafeSettings bool) *testEnv {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "doctor-http.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	uow := sqlite.NewUnitOfWork(store)

	reg := httpapi.NewRouteRegistry()
	httpdoctor.RegisterRoutes(reg, httpdoctor.Dependencies{
		Config: cfg, Store: sqlite.NewQueryStore(store), UnitOfWork: uow, Isolation: isolation,
	})
	if withSafeSettings {
		httpsafesettings.RegisterRoutes(reg, httpsafesettings.Dependencies{
			UnitOfWork: uow, IDs: idsource.Random{}, Clock: clock.System{},
			Matcher: redact.NewMatcher(), Effective: defaultEffective(),
		})
	}

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

	return &testEnv{base: "http://" + server.Addr(), client: &http.Client{Timeout: 10 * time.Second}, store: store, uow: uow}
}

func (e *testEnv) do(t *testing.T, method, path, idempotencyKey, ifMatch, rawBody string) *http.Response {
	t.Helper()
	var reader io.Reader
	if rawBody != "" {
		reader = strings.NewReader(rawBody)
	}
	req, err := http.NewRequest(method, e.base+path, reader)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set(httpapi.SessionTokenHeader, testSessionToken)
	if rawBody != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		req.Header.Set(httpapi.IdempotencyKeyHeader, idempotencyKey)
	}
	if ifMatch != "" {
		req.Header.Set(httpapi.IfMatchHeader, ifMatch)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func readBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return b
}

// checkTest mirrors this package's own unexported checkResultWire.
type checkTest struct {
	Name        string `json:"name"`
	Category    string `json:"category"`
	Status      string `json:"status"`
	Detail      string `json:"detail"`
	Remediation string `json:"remediation"`
}

// responseTest mirrors this package's own unexported responseDTO exactly
// (dto.go) — tests live in the external doctor_test package (this repo's
// own convention for real-HTTP-round-trip suites), so they decode into
// their own copy of the wire shape rather than reaching into unexported
// types.
type responseTest struct {
	Status          string            `json:"status"`
	Checks          []checkTest       `json:"checks"`
	RestartRequired bool              `json:"restartRequired"`
	Links           map[string]string `json:"links"`
}

// jsonEscaped returns s as it appears INSIDE a JSON string literal (e.g. a
// Windows path's single backslashes doubled) — the raw-bytes redaction
// assertions below must search for THIS form, not s itself, or a real leak
// of a backslash-containing path would silently escape detection on
// Windows.
func jsonEscaped(t *testing.T, s string) string {
	t.Helper()
	encoded, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("json.Marshal(%q): %v", s, err)
	}
	return strings.Trim(string(encoded), `"`)
}

func decodeResponse(t *testing.T, raw []byte) responseTest {
	t.Helper()
	var body responseTest
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode response body %s: %v", raw, err)
	}
	return body
}

func findCheck(body responseTest, name string) (checkTest, bool) {
	for _, c := range body.Checks {
		if c.Name == name {
			return c, true
		}
	}
	return checkTest{}, false
}

// writeFixtureExecutable writes a small real file to stand in for a
// configured provider executable — internal/app/doctor.CheckProviderExecutable
// only stats and sha256-hashes it, never executes it, so the content is
// arbitrary (mirrors internal/app/doctor/report_golden_test.go's own
// identical fixture).
func writeFixtureExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("fake-provider-binary-v1\n"), 0o755); err != nil {
		t.Fatalf("write fixture executable: %v", err)
	}
	return path
}

func validConfig(dbPath, artifactRoot, workerID string) config.Config {
	cfg := config.Defaults()
	cfg.DatabasePath = dbPath
	cfg.ArtifactRoot = artifactRoot
	cfg.WorkerID = workerID
	return cfg
}

// TestRegisterRoutes_ExposesExactlyDoctor proves the router side of this
// task's own closed scope: exactly one route, GET /doctor,
// installation-scoped.
func TestRegisterRoutes_ExposesExactlyDoctor(t *testing.T) {
	reg := httpapi.NewRouteRegistry()
	httpdoctor.RegisterRoutes(reg, httpdoctor.Dependencies{})

	got := reg.Descriptors()
	if len(got) != 1 {
		t.Fatalf("len(Descriptors()) = %d, want 1: %+v", len(got), got)
	}
	d := got[0]
	if d.OperationID != "doctor" {
		t.Errorf("OperationID = %q, want doctor", d.OperationID)
	}
	if d.Method != http.MethodGet {
		t.Errorf("Method = %q, want GET", d.Method)
	}
	if d.Path != "/doctor" {
		t.Errorf("Path = %q, want /doctor", d.Path)
	}
	if d.ScopeKind != httpapi.ScopeInstallation {
		t.Errorf("ScopeKind = %q, want INSTALLATION", d.ScopeKind)
	}
}

// TestDoctor_Healthy_HTTP is this task's own "healthy golden" Verify
// scenario, driven through a real, fully-valid config.Config/database/
// artifact-root/provider-executable/isolation-checker combination — the
// exact same shape cmd/aw/serve.go's own real composition root now builds.
func TestDoctor_Healthy_HTTP(t *testing.T) {
	artifactDir := t.TempDir()
	providerDir := t.TempDir()
	providerPath := writeFixtureExecutable(t, providerDir, "fake-provider")

	dbPath := filepath.Join(t.TempDir(), "agentkit.db")
	cfg := validConfig(dbPath, artifactDir, "aw-serve-test")
	cfg.ProviderExecutables = map[string]string{"claude": providerPath}

	env := newTestEnv(t, cfg, process.NewIsolationChecker(), false)
	resp := env.do(t, http.MethodGet, "/doctor", "", "", "")
	raw := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /doctor status = %d, want 200, body=%s", resp.StatusCode, raw)
	}
	body := decodeResponse(t, raw)
	if body.Status != "HEALTHY" {
		t.Fatalf("status = %q, want HEALTHY: %+v", body.Status, body.Checks)
	}
	if body.RestartRequired {
		t.Fatal("restartRequired = true on a never-configured install, want false")
	}
	if _, ok := findCheck(body, "database"); !ok {
		t.Error("missing \"database\" check")
	}
	if _, ok := findCheck(body, "artifact_root"); !ok {
		t.Error("missing \"artifact_root\" check")
	}
	if _, ok := findCheck(body, "git"); !ok {
		t.Error("missing \"git\" check")
	}
	if _, ok := findCheck(body, "safe_settings"); !ok {
		t.Error("missing \"safe_settings\" check")
	}
	iso, ok := findCheck(body, "isolation_enforcement")
	if !ok {
		t.Fatal("missing \"isolation_enforcement\" check")
	}
	if iso.Category != "CAPABILITY" {
		t.Errorf("isolation_enforcement.category = %q, want CAPABILITY", iso.Category)
	}
	if iso.Status != "HEALTHY" {
		t.Errorf("isolation_enforcement.status = %q, want HEALTHY: %+v", iso.Status, iso)
	}
	wantLinks := map[string]string{
		"adapterBuilds": "/adapter-builds",
		"safeSettings":  "/settings/safe",
		"projects":      "/projects",
		"healthLive":    "/health/live",
		"healthReady":   "/health/ready",
	}
	if len(body.Links) != len(wantLinks) {
		t.Fatalf("links = %+v, want %+v", body.Links, wantLinks)
	}
	for k, v := range wantLinks {
		if body.Links[k] != v {
			t.Errorf("links[%q] = %q, want %q", k, body.Links[k], v)
		}
	}
}

// TestDoctor_Degraded_MissingArtifactRoot_HTTP is this task's own "degraded
// golden" Verify scenario: the artifact root does not exist yet — nothing
// is BLOCKED, but the report cannot be HEALTHY either.
func TestDoctor_Degraded_MissingArtifactRoot_HTTP(t *testing.T) {
	missingArtifactRoot := filepath.Join(t.TempDir(), "not-created-yet")
	dbPath := filepath.Join(t.TempDir(), "agentkit.db")
	cfg := validConfig(dbPath, missingArtifactRoot, "aw-serve-test")

	env := newTestEnv(t, cfg, process.NewIsolationChecker(), false)
	resp := env.do(t, http.MethodGet, "/doctor", "", "", "")
	raw := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /doctor status = %d, want 200, body=%s", resp.StatusCode, raw)
	}
	body := decodeResponse(t, raw)
	if body.Status != "DEGRADED" {
		t.Fatalf("status = %q, want DEGRADED: %+v", body.Status, body.Checks)
	}
	root, ok := findCheck(body, "artifact_root")
	if !ok {
		t.Fatal("missing \"artifact_root\" check")
	}
	if root.Status != "DEGRADED" {
		t.Errorf("artifact_root.status = %q, want DEGRADED", root.Status)
	}
	if root.Remediation == "" {
		t.Error("artifact_root check has no Remediation — Doctor exists to say what to do next")
	}
}

// TestDoctor_Blocked_DatabaseUnreachable_HTTP is this task's own "blocked
// golden" Verify scenario: the database becomes unreachable (closed)
// underneath an otherwise perfectly healthy configuration.
func TestDoctor_Blocked_DatabaseUnreachable_HTTP(t *testing.T) {
	artifactDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "agentkit.db")
	cfg := validConfig(dbPath, artifactDir, "aw-serve-test")

	env := newTestEnv(t, cfg, process.NewIsolationChecker(), false)
	if err := env.store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	resp := env.do(t, http.MethodGet, "/doctor", "", "", "")
	raw := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /doctor status = %d, want 200 (severity lives in the body, not the HTTP status), body=%s", resp.StatusCode, raw)
	}
	body := decodeResponse(t, raw)
	if body.Status != "BLOCKED" {
		t.Fatalf("status = %q, want BLOCKED: %+v", body.Status, body.Checks)
	}
	db, ok := findCheck(body, "database")
	if !ok {
		t.Fatal("missing \"database\" check")
	}
	if db.Status != "BLOCKED" {
		t.Errorf("database.status = %q, want BLOCKED", db.Status)
	}
	if db.Remediation == "" {
		t.Error("database check has no Remediation — Doctor exists to say what to do next")
	}
	// restartRequired must degrade to a safe false, never crash the whole
	// request, when the same closed database also breaks the safe-settings
	// lookup behind it.
	if body.RestartRequired {
		t.Error("restartRequired = true with an unreachable database, want false (safe default)")
	}
}

// TestDoctor_RestartRequired_AfterSafeSettingsUpdate_HTTP is this task's own
// "restart states" Verify scenario, mirroring V6-10H's own restartRequired
// concept: a real PUT /settings/safe flips it from false to true, visible
// through GET /doctor without Doctor ever re-deriving the semantic itself.
func TestDoctor_RestartRequired_AfterSafeSettingsUpdate_HTTP(t *testing.T) {
	artifactDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "agentkit.db")
	cfg := validConfig(dbPath, artifactDir, "aw-serve-test")

	env := newTestEnv(t, cfg, process.NewIsolationChecker(), true)

	before := decodeResponse(t, readBody(t, env.do(t, http.MethodGet, "/doctor", "", "", "")))
	if before.RestartRequired {
		t.Fatal("restartRequired = true before any safe-settings update, want false")
	}

	putResp := env.do(t, http.MethodPut, "/settings/safe", "idem-1", `"1"`, validDesiredBody)
	putRaw := readBody(t, putResp)
	if putResp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /settings/safe status = %d, want 200, body=%s", putResp.StatusCode, putRaw)
	}

	after := decodeResponse(t, readBody(t, env.do(t, http.MethodGet, "/doctor", "", "", "")))
	if !after.RestartRequired {
		t.Fatal("restartRequired = false after a real safe-settings update, want true")
	}
}

// TestDoctor_NeverLeaksDatabasePathOrCredential_HTTP is this task's own
// "secret/path redaction" Verify scenario: proves, against the REAL raw
// response bytes of a real install with a real persisted credential
// reference, that (1) a credential never appears in cleartext anywhere in
// Doctor's response and (2) the database's own filesystem path never
// appears either — while (3) the artifact root's own path DOES appear,
// proving this is a deliberate, selective redaction (only what CheckRoot's
// own Detail documents as "explicitly safe to show"), not Doctor
// accidentally omitting every path indiscriminately.
func TestDoctor_NeverLeaksDatabasePathOrCredential_HTTP(t *testing.T) {
	artifactDir := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "very-secret-database-location", "agentkit.db")
	cfg := validConfig(dbPath, artifactDir, "aw-serve-test")

	env := newTestEnv(t, cfg, process.NewIsolationChecker(), true)

	putResp := env.do(t, http.MethodPut, "/settings/safe", "idem-secret", `"1"`, validDesiredBody)
	if putResp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /settings/safe status = %d, want 200", putResp.StatusCode)
	}
	_ = readBody(t, putResp)

	resp := env.do(t, http.MethodGet, "/doctor", "", "", "")
	raw := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /doctor status = %d, want 200, body=%s", resp.StatusCode, raw)
	}
	rawStr := string(raw)
	if strings.Contains(rawStr, jsonEscaped(t, secretCredentialRef)) {
		t.Fatalf("GET /doctor response body leaks providerCredentialRef in cleartext: %s", raw)
	}
	if strings.Contains(rawStr, jsonEscaped(t, dbPath)) {
		t.Fatalf("GET /doctor response body leaks the database's own filesystem path: %s", raw)
	}

	// The artifact root's own path DOES legitimately appear (CheckRoot's own
	// Detail documents it as "explicitly safe to show") — proving this is a
	// deliberate, selective redaction, not Doctor blanking out every path
	// indiscriminately. Checked against the DECODED field (not raw bytes),
	// since JSON string-escaping (e.g. a Windows path's backslashes) would
	// otherwise make a raw substring search fail even on a correct response.
	body := decodeResponse(t, raw)
	root, ok := findCheck(body, "artifact_root")
	if !ok {
		t.Fatal("missing \"artifact_root\" check")
	}
	if !strings.Contains(root.Detail, artifactDir) {
		t.Fatalf("artifact_root.detail = %q, want it to contain the artifact root's own path %q", root.Detail, artifactDir)
	}
}
