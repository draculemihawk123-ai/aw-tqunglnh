package safesettings_test

// Real HTTP round-trip coverage for V6-10H (docs/design/08-v6-api-projections.md):
// every test in this file drives a REAL httpapi.Server (real TCP loopback
// listener, real middleware chain, real session-token/Origin/Host guards)
// backed by a REAL *sqlite.Store — never a mock, never a direct row
// fabrication (this repo's own hard rule; mirrors
// internal/delivery/httpapi/workitem/workitem_test.go's own newTestEnv
// idiom).

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/clock"
	"github.com/taQuangLing/agent-workflow/internal/app/idsource"
	"github.com/taQuangLing/agent-workflow/internal/app/logging"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	"github.com/taQuangLing/agent-workflow/internal/app/redact"
	safesettingsapp "github.com/taQuangLing/agent-workflow/internal/app/safesettings"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
	httpsafesettings "github.com/taQuangLing/agent-workflow/internal/delivery/httpapi/safesettings"
	"github.com/taQuangLing/agent-workflow/internal/domain/safesettings"
)

const testSessionToken = "test-safesettings-session-token"

// validDesiredBody is a well-formed desired document covering all 7
// allowlisted fields — mirrors internal/app/safesettings/commands_sqlite_test.go's
// own validDesiredJSON exactly, so the two test suites (application-command
// layer and this HTTP layer) exercise the identical payload shape.
const validDesiredBody = `{
	"managedWorkspaceRoot":"data/workspaces",
	"managedArtifactRoot":"data/artifacts",
	"evidenceRetention":"168h0m0s",
	"processOutputLimit":1048576,
	"providerExecutablePath":"C:/tools/claude/claude.exe",
	"providerDefaultModel":"claude-sonnet-4-5",
	"providerCredentialRef":"keychain:claude-api-key"
}`

const secretCredentialRef = "keychain:claude-api-key"

func testPrincipal() httpapi.LocalPrincipalSnapshot {
	return httpapi.LocalPrincipalSnapshot{Actor: "local-operator", Roles: []string{"operator"}}
}

// testEnv is one real Server + real UnitOfWork pair, torn down via
// t.Cleanup.
type testEnv struct {
	base   string
	client *http.Client
	uow    ports.UnitOfWork
}

func openStore(t *testing.T, name string) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// defaultEffective is the Effective value a brand-new, never-before-run
// process boots with: defaults only, no file/env/flag override, and SQLite
// still at the zero/"never configured" document.
func defaultEffective() safesettingsapp.Effective {
	return safesettingsapp.ResolveEffective(
		safesettingsapp.Defaults(), safesettingsapp.StartupOverrides{}, safesettings.SafeSettings{},
		safesettingsapp.StartupOverrides{}, safesettingsapp.StartupOverrides{},
	)
}

// newTestEnvFromStore builds a real server over an ALREADY-OPEN store — so
// a test can seed SQLite state (simulating "a previous process already
// configured this") before this "process" ever boots and captures its own
// fixed Effective snapshot.
func newTestEnvFromStore(t *testing.T, store *sqlite.Store, effective safesettingsapp.Effective) *testEnv {
	t.Helper()
	uow := sqlite.NewUnitOfWork(store)

	reg := httpapi.NewRouteRegistry()
	httpsafesettings.RegisterRoutes(reg, httpsafesettings.Dependencies{
		UnitOfWork: uow, IDs: idsource.Random{}, Clock: clock.System{},
		Matcher: redact.NewMatcher(), Effective: effective,
	})

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

	return &testEnv{base: "http://" + server.Addr(), client: &http.Client{Timeout: 10 * time.Second}, uow: uow}
}

func newTestEnv(t *testing.T) *testEnv {
	return newTestEnvFromStore(t, openStore(t, "safesettings-http.db"), defaultEffective())
}

// do issues a real HTTP request against e's own real server. idempotencyKey/
// ifMatch/rawBody are omitted from the request entirely when "". rawBody is
// sent verbatim (never auto-marshaled) so a test can inject a malformed or
// extra-field body exactly as written.
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

func decodeBytes(t *testing.T, raw []byte, dst any) {
	t.Helper()
	if err := json.Unmarshal(raw, dst); err != nil {
		t.Fatalf("decode response body %s: %v", raw, err)
	}
}

// fieldWireTest mirrors this package's own unexported stringFieldWire/
// durationFieldWire/intFieldWire (dto.go) — Effective is decoded as `any`
// so this single struct works for the string, duration-string and int
// variants alike.
type fieldWireTest struct {
	Effective             any    `json:"effective"`
	Source                string `json:"source"`
	MaskedByStartupSource string `json:"maskedByStartupSource"`
}

// responseTest mirrors this package's own unexported responseDTO exactly
// (dto.go) — tests live in the external safesettings_test package (this
// repo's own convention for real-HTTP-round-trip suites), so they decode
// into their own copy of the wire shape rather than reaching into
// unexported types.
type responseTest struct {
	Desired struct {
		ManagedWorkspaceRoot   string `json:"managedWorkspaceRoot"`
		ManagedArtifactRoot    string `json:"managedArtifactRoot"`
		EvidenceRetention      string `json:"evidenceRetention"`
		ProcessOutputLimit     int    `json:"processOutputLimit"`
		ProviderExecutablePath string `json:"providerExecutablePath"`
		ProviderDefaultModel   string `json:"providerDefaultModel"`
		ProviderCredentialRef  string `json:"providerCredentialRef"`
	} `json:"desired"`
	Version         uint64    `json:"version"`
	UpdatedAt       time.Time `json:"updatedAt"`
	UpdatedBy       string    `json:"updatedBy"`
	RestartRequired bool      `json:"restartRequired"`
	Effective       struct {
		ManagedWorkspaceRoot   fieldWireTest `json:"managedWorkspaceRoot"`
		ManagedArtifactRoot    fieldWireTest `json:"managedArtifactRoot"`
		EvidenceRetention      fieldWireTest `json:"evidenceRetention"`
		ProcessOutputLimit     fieldWireTest `json:"processOutputLimit"`
		ProviderExecutablePath fieldWireTest `json:"providerExecutablePath"`
		ProviderDefaultModel   fieldWireTest `json:"providerDefaultModel"`
		ProviderCredentialRef  fieldWireTest `json:"providerCredentialRef"`
	} `json:"effective"`
}

// wantMaskedCredentialRef is the exact masked form every response's own
// providerCredentialRef (desired AND effective) must equal — derived via
// the SAME redact.Matcher.Tagged(redact.Secret, ...) call production code
// uses, rather than hardcoding redact's own private placeholder text.
func wantMaskedCredentialRef() string {
	return redact.NewMatcher().Tagged(redact.Secret, "any-value-at-all")
}

// TestRegisterRoutes_ExposesExactlyGetAndUpdate proves the router side of
// this task's own closed scope: exactly GET/PUT /settings/safe, both
// installation-scoped (ADR-025's own command-scope table).
func TestRegisterRoutes_ExposesExactlyGetAndUpdate(t *testing.T) {
	reg := httpapi.NewRouteRegistry()
	httpsafesettings.RegisterRoutes(reg, httpsafesettings.Dependencies{})

	want := map[string]bool{"getSafeSettings": true, "updateSafeSettings": true}
	got := reg.Descriptors()
	if len(got) != len(want) {
		t.Fatalf("len(Descriptors()) = %d, want %d", len(got), len(want))
	}
	for _, d := range got {
		if !want[d.OperationID] {
			t.Errorf("unexpected operationId %q registered", d.OperationID)
		}
		if d.ScopeKind != httpapi.ScopeInstallation {
			t.Errorf("operationId %q has ScopeKind %q, want INSTALLATION", d.OperationID, d.ScopeKind)
		}
		if d.Path != "/settings/safe" {
			t.Errorf("operationId %q has Path %q, want /settings/safe", d.OperationID, d.Path)
		}
		delete(want, d.OperationID)
	}
	if len(want) != 0 {
		t.Errorf("missing operationIds: %v", want)
	}
}

// TestGetSafeSettings_FreshDatabase_HTTP is this task's own GET happy path
// on a freshly migrated database.
func TestGetSafeSettings_FreshDatabase_HTTP(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(t, http.MethodGet, "/settings/safe", "", "", "")
	raw := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d, want 200, body=%s", resp.StatusCode, raw)
	}
	var body responseTest
	decodeBytes(t, raw, &body)
	if body.Version != 1 {
		t.Fatalf("version = %d, want 1", body.Version)
	}
	if body.RestartRequired {
		t.Fatal("restartRequired = true on fresh DB, want false")
	}
	if got := resp.Header.Get(httpapi.ETagHeader); got != `"1"` {
		t.Fatalf("ETag = %q, want %q", got, `"1"`)
	}
}

// TestUpdateSafeSettings_HappyPath_HTTP is this task's own end-to-end PUT
// proof: a well-formed update persists, bumps version, sets
// restartRequired, and never echoes the credential reference back in
// cleartext.
func TestUpdateSafeSettings_HappyPath_HTTP(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(t, http.MethodPut, "/settings/safe", "idem-1", `"1"`, validDesiredBody)
	raw := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200, body=%s", resp.StatusCode, raw)
	}
	var body responseTest
	decodeBytes(t, raw, &body)
	if body.Version != 2 {
		t.Fatalf("version = %d, want 2", body.Version)
	}
	if !body.RestartRequired {
		t.Fatal("restartRequired = false after a real update, want true")
	}
	if body.Desired.ManagedWorkspaceRoot != "data/workspaces" {
		t.Fatalf("desired.managedWorkspaceRoot = %q, want data/workspaces", body.Desired.ManagedWorkspaceRoot)
	}
	if want := wantMaskedCredentialRef(); body.Desired.ProviderCredentialRef != want {
		t.Fatalf("desired.providerCredentialRef = %q, want masked %q", body.Desired.ProviderCredentialRef, want)
	}
	if got := resp.Header.Get(httpapi.ETagHeader); got != `"2"` {
		t.Fatalf("ETag = %q, want %q", got, `"2"`)
	}
}

// TestUpdateSafeSettings_UnknownFieldRejected_HTTP is this task's own
// "strict unknown-field rejection" Verify scenario — the same DecodeJSON
// strict-decode convention every other V6-02A-era endpoint already uses.
func TestUpdateSafeSettings_UnknownFieldRejected_HTTP(t *testing.T) {
	env := newTestEnv(t)
	body := `{"managedWorkspaceRoot":"w","managedArtifactRoot":"a","evidenceRetention":"1h","processOutputLimit":1,"providerExecutablePath":"p","providerDefaultModel":"m","providerCredentialRef":"r","databasePath":"/tmp/evil.db"}`
	resp := env.do(t, http.MethodPut, "/settings/safe", "idem-unknown", `"1"`, body)
	raw := readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", resp.StatusCode, raw)
	}
}

// TestUpdateSafeSettings_MalformedJSONRejected_HTTP proves a syntactically
// broken body is rejected the same way, not just an unknown field.
func TestUpdateSafeSettings_MalformedJSONRejected_HTTP(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(t, http.MethodPut, "/settings/safe", "idem-malformed", `"1"`, `{not valid json`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// TestUpdateSafeSettings_InvalidValueRejected_HTTP is this task's own
// semantic-validation Verify scenario, proven at the HTTP layer (the
// application command's own internal safesettings.Validate call is already
// exhaustively proven by internal/app/safesettings/commands_sqlite_test.go
// — this proves the HTTP handler's own PRE-DISPATCH call to the identical
// function actually runs and maps to 400).
func TestUpdateSafeSettings_InvalidValueRejected_HTTP(t *testing.T) {
	env := newTestEnv(t)
	body := `{"managedWorkspaceRoot":"w","managedArtifactRoot":"a","evidenceRetention":"0s","processOutputLimit":1,"providerExecutablePath":"p","providerDefaultModel":"m","providerCredentialRef":"r"}`
	resp := env.do(t, http.MethodPut, "/settings/safe", "idem-invalid", `"1"`, body)
	raw := readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", resp.StatusCode, raw)
	}

	// The rejected update must never have touched storage.
	getResp := env.do(t, http.MethodGet, "/settings/safe", "", "", "")
	var current responseTest
	decodeBytes(t, readBody(t, getResp), &current)
	if current.Version != 1 {
		t.Fatalf("version after rejected update = %d, want unchanged 1", current.Version)
	}
}

// TestUpdateSafeSettings_MissingHeaders_HTTP proves both required headers
// are actually enforced by this route (V6-02's own "Idempotency-Key bắt
// buộc"/"update bắt buộc strong If-Match").
func TestUpdateSafeSettings_MissingHeaders_HTTP(t *testing.T) {
	env := newTestEnv(t)
	if resp := env.do(t, http.MethodPut, "/settings/safe", "", `"1"`, validDesiredBody); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing Idempotency-Key: status = %d, want 400", resp.StatusCode)
	}
	if resp := env.do(t, http.MethodPut, "/settings/safe", "idem-noifmatch", "", validDesiredBody); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing If-Match: status = %d, want 400", resp.StatusCode)
	}
}

// TestUpdateSafeSettings_IfMatchMismatch_HTTP is this task's own "If-Match
// mismatch → 412" Verify scenario.
func TestUpdateSafeSettings_IfMatchMismatch_HTTP(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(t, http.MethodPut, "/settings/safe", "idem-stale", `"99"`, validDesiredBody)
	raw := readBody(t, resp)
	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("status = %d, want 412, body=%s", resp.StatusCode, raw)
	}
}

// TestUpdateSafeSettings_ReplaySameKeySameHash_HTTP is this task's own
// "same IdempotencyKey+RequestHash replay → same result" Verify scenario —
// including proving the replayed response is masked/shaped identically to
// the fresh one, not the raw stored receipt JSON (see commands.go's own
// replayOrProceed doc comment for why that distinction is load-bearing).
func TestUpdateSafeSettings_ReplaySameKeySameHash_HTTP(t *testing.T) {
	env := newTestEnv(t)
	first := env.do(t, http.MethodPut, "/settings/safe", "idem-replay", `"1"`, validDesiredBody)
	firstRaw := readBody(t, first)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first PUT status = %d, want 200, body=%s", first.StatusCode, firstRaw)
	}
	var firstBody responseTest
	decodeBytes(t, firstRaw, &firstBody)

	second := env.do(t, http.MethodPut, "/settings/safe", "idem-replay", `"1"`, validDesiredBody)
	secondRaw := readBody(t, second)
	if second.StatusCode != http.StatusOK {
		t.Fatalf("replayed PUT status = %d, want 200, body=%s", second.StatusCode, secondRaw)
	}
	var secondBody responseTest
	decodeBytes(t, secondRaw, &secondBody)

	if secondBody.Version != firstBody.Version {
		t.Fatalf("replayed version = %d, want identical to first %d", secondBody.Version, firstBody.Version)
	}
	if secondBody.RestartRequired != firstBody.RestartRequired {
		t.Fatalf("replayed restartRequired = %v, want identical to first %v", secondBody.RestartRequired, firstBody.RestartRequired)
	}
	if want := wantMaskedCredentialRef(); secondBody.Desired.ProviderCredentialRef != want {
		t.Fatalf("replayed desired.providerCredentialRef = %q, want masked %q (replay must stay masked too)", secondBody.Desired.ProviderCredentialRef, want)
	}
	if strings.Contains(string(secondRaw), secretCredentialRef) {
		t.Fatalf("replayed PUT response body leaks providerCredentialRef in cleartext: %s", secondRaw)
	}

	// The underlying CAS must never have re-applied a second time.
	getResp := env.do(t, http.MethodGet, "/settings/safe", "", "", "")
	var current responseTest
	decodeBytes(t, readBody(t, getResp), &current)
	if current.Version != 2 {
		t.Fatalf("current version = %d after replay, want 2 (replay must never re-apply the CAS)", current.Version)
	}
}

// TestUpdateSafeSettings_ReplayDifferentHashConflicts_HTTP is this task's
// own "same key+different hash → 409 conflict" Verify scenario.
func TestUpdateSafeSettings_ReplayDifferentHashConflicts_HTTP(t *testing.T) {
	env := newTestEnv(t)
	first := env.do(t, http.MethodPut, "/settings/safe", "idem-conflict", `"1"`, validDesiredBody)
	firstRaw := readBody(t, first)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first PUT status = %d, want 200, body=%s", first.StatusCode, firstRaw)
	}

	differentBody := strings.Replace(validDesiredBody, "claude-sonnet-4-5", "claude-other-model", 1)
	second := env.do(t, http.MethodPut, "/settings/safe", "idem-conflict", `"1"`, differentBody)
	secondRaw := readBody(t, second)
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("second PUT (same key, different body) status = %d, want 409, body=%s", second.StatusCode, secondRaw)
	}
}

// TestSafeSettings_EffectiveFrozenAtBoot_AndStartupSourceMasking_HTTP is
// this task's own hardest Verify scenario, combining two distinct
// behaviors:
//
//  1. "restart/mask": env overriding an already-SQLite-configured field
//     reports Source=environment and MaskedByStartupSource=environment —
//     internal/app/safesettings/startup.go's own ResolveEffective contract,
//     surfaced correctly over HTTP.
//  2. Effective is a FIXED boot-time snapshot: a live PUT changes Desired
//     immediately, but Effective (and therefore what this endpoint reports
//     as "what the running process actually uses") stays byte-for-byte
//     unchanged until a real process restart recomputes it — proving
//     dependencies.go's own "captured once, never per-request" design
//     decision is what this package actually does, not just what its doc
//     comment claims.
func TestSafeSettings_EffectiveFrozenAtBoot_AndStartupSourceMasking_HTTP(t *testing.T) {
	store := openStore(t, "safesettings-effective.db")
	uow := sqlite.NewUnitOfWork(store)

	// Simulate "a previous process already configured this": seed a real
	// desired document directly through the real application command,
	// exactly as internal/app/safesettings/commands_sqlite_test.go does for
	// its own fixtures.
	seedCmd := ports.Command{
		ID: "cmd-seed", IdempotencyKey: "seed", Actor: "seed-actor", CorrelationID: "seed",
		Scope: ports.InstallationScope(), ExpectedVersion: 1, RequestedAt: time.Now().UTC(),
		Type: "UpdateSafeSettings", RequestHash: "seed-hash",
	}
	seeded, err := safesettingsapp.UpdateSafeSettings(context.Background(), uow, idsource.Random{}, seedCmd,
		safesettingsapp.UpdateSafeSettingsRequest{DesiredJSON: json.RawMessage(validDesiredBody)})
	if err != nil {
		t.Fatalf("seed UpdateSafeSettings: %v", err)
	}

	// This "process" boots with an environment override on exactly one
	// field — the other 6 fields fall through to the seeded SQLite value.
	const envOverridePath = "/opt/claude/claude"
	effective := safesettingsapp.ResolveEffective(
		safesettingsapp.Defaults(), safesettingsapp.StartupOverrides{}, seeded.Desired,
		safesettingsapp.StartupOverrides{ProviderExecutablePath: ptr(envOverridePath)}, safesettingsapp.StartupOverrides{},
	)

	env := newTestEnvFromStore(t, store, effective)

	before := env.do(t, http.MethodGet, "/settings/safe", "", "", "")
	var beforeBody responseTest
	decodeBytes(t, readBody(t, before), &beforeBody)

	if got := beforeBody.Effective.ProviderExecutablePath.Source; got != string(safesettingsapp.SourceEnv) {
		t.Fatalf("effective.providerExecutablePath.source = %q, want %q", got, safesettingsapp.SourceEnv)
	}
	if got := beforeBody.Effective.ProviderExecutablePath.MaskedByStartupSource; got != string(safesettingsapp.SourceEnv) {
		t.Fatalf("effective.providerExecutablePath.maskedByStartupSource = %q, want %q (sqlite value is masked by env)", got, safesettingsapp.SourceEnv)
	}
	if got, _ := beforeBody.Effective.ProviderExecutablePath.Effective.(string); got != envOverridePath {
		t.Fatalf("effective.providerExecutablePath.effective = %v, want %q", beforeBody.Effective.ProviderExecutablePath.Effective, envOverridePath)
	}
	if got := beforeBody.Effective.ManagedWorkspaceRoot.Source; got != string(safesettingsapp.SourceSQLite) {
		t.Fatalf("effective.managedWorkspaceRoot.source = %q, want %q (no override on this field)", got, safesettingsapp.SourceSQLite)
	}
	if got := beforeBody.Effective.ManagedWorkspaceRoot.MaskedByStartupSource; got != "" {
		t.Fatalf("effective.managedWorkspaceRoot.maskedByStartupSource = %q, want empty (no override)", got)
	}
	if want := wantMaskedCredentialRef(); beforeBody.Desired.ProviderCredentialRef != want {
		t.Fatalf("desired.providerCredentialRef = %q, want masked %q", beforeBody.Desired.ProviderCredentialRef, want)
	}
	if got, _ := beforeBody.Effective.ProviderCredentialRef.Effective.(string); got != wantMaskedCredentialRef() {
		t.Fatalf("effective.providerCredentialRef.effective = %v, want masked %q", beforeBody.Effective.ProviderCredentialRef.Effective, wantMaskedCredentialRef())
	}

	// Now mutate the LIVE desired document over HTTP.
	newBody := strings.Replace(validDesiredBody, "data/workspaces", "data/new-workspaces", 1)
	putResp := env.do(t, http.MethodPut, "/settings/safe", "idem-after-boot", `"2"`, newBody)
	putRaw := readBody(t, putResp)
	if putResp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200, body=%s", putResp.StatusCode, putRaw)
	}

	after := env.do(t, http.MethodGet, "/settings/safe", "", "", "")
	var afterBody responseTest
	decodeBytes(t, readBody(t, after), &afterBody)

	if afterBody.Desired.ManagedWorkspaceRoot != "data/new-workspaces" {
		t.Fatalf("desired.managedWorkspaceRoot after PUT = %q, want data/new-workspaces (the LIVE document must reflect the new PUT)", afterBody.Desired.ManagedWorkspaceRoot)
	}
	if !afterBody.RestartRequired {
		t.Fatal("restartRequired = false after a real update, want true")
	}
	// Effective must be UNCHANGED — this process has no hot-reload path, so
	// what it reports as "actually running with" cannot move just because
	// Desired did.
	if afterBody.Effective.ManagedWorkspaceRoot.Effective != beforeBody.Effective.ManagedWorkspaceRoot.Effective {
		t.Fatalf("effective.managedWorkspaceRoot changed after a live PUT (got %v, want unchanged %v) — Effective must stay frozen at its boot-time snapshot until a real restart",
			afterBody.Effective.ManagedWorkspaceRoot.Effective, beforeBody.Effective.ManagedWorkspaceRoot.Effective)
	}
	if afterBody.Effective.ProviderExecutablePath.Effective != beforeBody.Effective.ProviderExecutablePath.Effective {
		t.Fatal("effective.providerExecutablePath changed after a live PUT, want unchanged (frozen boot-time snapshot)")
	}
}

// TestSafeSettings_ProviderCredentialRef_NeverInRawResponseBytes_HTTP is
// this task's own "secret/redaction goldens" Verify scenario: scans the
// RAW response bytes (not just the decoded field) for the real secret
// value, across a fresh PUT, a plain GET, and a REPLAYED PUT — the one
// response path (see commands.go's own replayOrProceed doc comment) that a
// naive implementation reusing httpapi.WriteReceiptReplay directly would
// have leaked through.
func TestSafeSettings_ProviderCredentialRef_NeverInRawResponseBytes_HTTP(t *testing.T) {
	env := newTestEnv(t)

	putResp := env.do(t, http.MethodPut, "/settings/safe", "idem-secret", `"1"`, validDesiredBody)
	putRaw := readBody(t, putResp)
	if putResp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200, body=%s", putResp.StatusCode, putRaw)
	}
	if strings.Contains(string(putRaw), secretCredentialRef) {
		t.Fatalf("PUT response body leaks providerCredentialRef in cleartext: %s", putRaw)
	}

	getResp := env.do(t, http.MethodGet, "/settings/safe", "", "", "")
	getRaw := readBody(t, getResp)
	if strings.Contains(string(getRaw), secretCredentialRef) {
		t.Fatalf("GET response body leaks providerCredentialRef in cleartext: %s", getRaw)
	}

	replayResp := env.do(t, http.MethodPut, "/settings/safe", "idem-secret", `"1"`, validDesiredBody)
	replayRaw := readBody(t, replayResp)
	if replayResp.StatusCode != http.StatusOK {
		t.Fatalf("replayed PUT status = %d, want 200, body=%s", replayResp.StatusCode, replayRaw)
	}
	if strings.Contains(string(replayRaw), secretCredentialRef) {
		t.Fatalf("replayed PUT response body leaks providerCredentialRef in cleartext: %s", replayRaw)
	}
}

func ptr[T any](v T) *T { return &v }
