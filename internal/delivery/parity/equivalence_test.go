package parity

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/cli"
	"github.com/taQuangLing/agent-workflow/internal/delivery/httpapi"
)

// The equivalence tests are V6-15O's second Verify bullet: "same semantic
// input/principal HTTP<->CLI normalized result tests". Each case drives the
// REAL HTTP handlers (a real httpapi.Server with its full middleware chain,
// reached over a real loopback socket) and the REAL CLI leaf code (through
// clicompose.Execute, the same entry point `aw` uses) against REAL SQLite,
// with the same semantic input and the same principal, and compares the
// normalized results: generated ids become <id#n> numbered by first
// appearance, timestamps become <time>, and only fields that are random per
// call (adapter-token nonce/signature/expiry) or CLI-transport-only (the
// result envelope's idempotencyKey/replayed) are dropped.

var testPrincipal = httpapi.LocalPrincipalSnapshot{Actor: "alice", Roles: []string{"operator", "auditor"}}

const validBlockDocument = `{"compatibleNodeTypes":["AGENT"],"doneCondition":"result.outcome","executorRef":{"definitionId":"cmd-1","kind":"COMMAND","versionId":"cmd-1-v1"},"outcomes":["failure","success"],"policyRefs":[{"definitionId":"policy-1","kind":"POLICY","versionId":"policy-1-v1"}],"requiredCapabilities":["INTEGRATION_MULTI_REPOSITORY_WRITE"],"scopeSelector":{"access":"WRITE","pathScopes":["src/**"]},"timeoutSeconds":60}`

const validSettingsDocument = `{"managedWorkspaceRoot":"ws-root","managedArtifactRoot":"art-root","evidenceRetention":"24h0m0s","processOutputLimit":2048,"providerExecutablePath":"prov/bin","providerDefaultModel":"model-1","providerCredentialRef":"keychain:aw-parity"}`

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// cliResultDoc returns the `result` of a CLI command's stdout envelope.
func cliResultDoc(t *testing.T, r cliResult) []byte {
	t.Helper()
	if r.Code != cli.ExitSuccess {
		t.Fatalf("CLI exit=%d stdout=%q stderr=%q", r.Code, r.Stdout, r.Stderr)
	}
	return jsonField(t, []byte(r.Stdout), "result")
}

func mustHTTP(t *testing.T, r httpResult, want ...int) httpResult {
	t.Helper()
	for _, s := range want {
		if r.Status == s {
			return r
		}
	}
	t.Fatalf("HTTP status %d, want one of %v: %s", r.Status, want, r.Body)
	return r
}

func idOf(t *testing.T, raw []byte, field string) string {
	t.Helper()
	var v string
	if err := json.Unmarshal(jsonField(t, raw, field), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// TestSameSemanticInputAndPrincipalGiveTheSameNormalizedResultOverHTTPAndCLI
// runs one representative journey twice — once over HTTP against
// installation A, once over the CLI against installation B, each starting
// empty — and demands identical normalized results at every step: an
// installation-scoped command and query (projects), a versioned safe-settings
// update, the global-scope definition commands and queries (create, publish,
// versions, show) and the adapter-build probe/register/list flow (the
// installation-scoped commands ADR-025 lists).
func TestSameSemanticInputAndPrincipalGiveTheSameNormalizedResultOverHTTPAndCLI(t *testing.T) {
	httpSide := newLiveInstall(t, testPrincipal)
	cliSide := newLiveInstall(t, testPrincipal)
	principal := cliSide.principalFile()

	same := func(step string, httpRaw, cliRaw []byte, dropKeys ...string) {
		t.Helper()
		if h, c := canonical(t, httpRaw, dropKeys...), canonical(t, cliRaw, dropKeys...); h != c {
			t.Errorf("%s: normalized HTTP and CLI results differ\n--- HTTP\n%s\n--- CLI\n%s", step, h, c)
		}
	}

	// ---- installation scope: projects -------------------------------------
	same("project list (empty)",
		mustHTTP(t, httpSide.do("GET", "/projects", nil, ""), 200).Body,
		[]byte(cliSide.cli("", "project", "list").Stdout))

	httpProject := mustHTTP(t, httpSide.do("POST", "/projects", map[string]string{"Idempotency-Key": "k-project"}, `{"name":"alpha"}`), 201)
	cliProject := cliSide.cli(`{"name":"alpha"}`, "project", "create", "--principal-config", principal, "--idempotency-key", "k-project")
	same("project create", httpProject.Body, cliResultDoc(t, cliProject))

	same("project list",
		mustHTTP(t, httpSide.do("GET", "/projects", nil, ""), 200).Body,
		[]byte(cliSide.cli("", "project", "list").Stdout))
	same("project show",
		mustHTTP(t, httpSide.do("GET", "/projects/"+idOf(t, httpProject.Body, "projectId"), nil, ""), 200).Body,
		[]byte(cliSide.cli("", "project", "show", idOf(t, cliResultDoc(t, cliProject), "projectId")).Stdout))

	// ---- installation scope: versioned safe settings ----------------------
	same("settings show (initial)",
		mustHTTP(t, httpSide.do("GET", "/settings/safe", nil, ""), 200).Body,
		[]byte(cliSide.cli("", "settings", "show", "--json").Stdout))

	settingsFile := writeFile(t, "settings.json", validSettingsDocument)
	httpUpdate := mustHTTP(t, httpSide.do("PUT", "/settings/safe", map[string]string{"Idempotency-Key": "k-settings", "If-Match": `"1"`}, validSettingsDocument), 200)
	cliUpdate := cliSide.cli("", "settings", "update", "--json", "--principal-config", principal, "--expected-version", "1", "--idempotency-key", "k-settings", "--file", settingsFile)
	// desired document, version, updatedBy and restartRequired must match
	// exactly. `effective` is the ONE reviewed divergence (V6-15C, pinned
	// below): HTTP reports the boot-time snapshot the running server was
	// started with, the one-shot CLI resolves it fresh on every invocation.
	same("settings update (records the principal as updatedBy)", httpUpdate.Body, cliResultDoc(t, cliUpdate), "effective")
	if !strings.Contains(canonical(t, httpUpdate.Body), `"updatedBy": "alice"`) {
		t.Errorf("settings update must record the bound principal (alice) as updatedBy, got %s", httpUpdate.Body)
	}
	httpShown := mustHTTP(t, httpSide.do("GET", "/settings/safe", nil, ""), 200).Body
	cliShown := []byte(cliSide.cli("", "settings", "show", "--json").Stdout)
	same("settings show (after update)", httpShown, cliShown, "effective")
	assertEffectiveSource := func(surface string, raw []byte, want string) {
		t.Helper()
		var doc struct {
			Effective struct {
				EvidenceRetention struct {
					Source string `json:"source"`
				} `json:"evidenceRetention"`
			} `json:"effective"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		if doc.Effective.EvidenceRetention.Source != want {
			t.Errorf("%s effective.evidenceRetention.source = %q, want %q — the reviewed HTTP/CLI effective-settings divergence changed; review V6-15C's decision before updating this pin",
				surface, doc.Effective.EvidenceRetention.Source, want)
		}
	}
	assertEffectiveSource("HTTP (boot-time snapshot)", httpShown, "default")
	assertEffectiveSource("CLI (resolved fresh per invocation)", cliShown, "sqlite")

	// ---- definitions, global scope ----------------------------------------
	blockFile := writeFile(t, "block.json", validBlockDocument)
	httpCreate := mustHTTP(t, httpSide.do("POST", "/definitions/BLOCK", map[string]string{"Idempotency-Key": "k-create"}, `{"definitionId":"blk-1","name":"block one"}`), 201)
	cliCreate := cliSide.cli(`{"definitionId":"blk-1","name":"block one"}`, "definition", "create", "--kind", "BLOCK", "--principal-config", principal, "--idempotency-key", "k-create")
	same("definition create", httpCreate.Body, cliResultDoc(t, cliCreate))

	publishBody := fmt.Sprintf(`{"content":%q,"format":"json","schemaVersion":1}`, validBlockDocument)
	httpPublish := mustHTTP(t, httpSide.do("POST", "/definitions/BLOCK/blk-1/publish", map[string]string{"Idempotency-Key": "k-publish"}, publishBody), 201)
	// definition publish is high-impact: the CLI's typed gate is satisfied
	// with --yes and then dispatches the very command HTTP dispatched.
	cliPublish := cliSide.cli("", "definition", "publish", "--kind", "BLOCK", "--yes", "--principal-config", principal, "--idempotency-key", "k-publish", "--file", blockFile, "blk-1")
	same("definition publish (hashes, version number and publishedBy)", httpPublish.Body, cliResultDoc(t, cliPublish))
	if !strings.Contains(canonical(t, httpPublish.Body), `"publishedBy": "alice"`) {
		t.Errorf("publish must record the bound principal as publishedBy, got %s", httpPublish.Body)
	}

	same("definition versions",
		mustHTTP(t, httpSide.do("GET", "/definitions/BLOCK/blk-1/versions", nil, ""), 200).Body,
		[]byte(cliSide.cli("", "definition", "versions", "--kind", "BLOCK", "blk-1").Stdout))
	same("version show",
		mustHTTP(t, httpSide.do("GET", "/definitions/versions/"+idOf(t, httpPublish.Body, "id"), nil, ""), 200).Body,
		[]byte(cliSide.cli("", "version", "show", idOf(t, cliResultDoc(t, cliPublish), "id")).Stdout))

	// ---- adapter builds (installation-global) -----------------------------
	executable := writeFile(t, "claude-cli", "fake provider executable bytes")
	probeBody := fmt.Sprintf(`{"providerKey":"claude","executablePath":%q,"protocolVersion":"1","capabilityManifest":{"supportsStart":true,"supportsResume":false,"supportsCancel":true,"canonicalEventKinds":["TEXT_DELTA"]},"os":"windows","toolchain":"go","configIdentity":"default"}`, executable)
	httpProbe := mustHTTP(t, httpSide.do("POST", "/adapter-builds/probe", map[string]string{"Idempotency-Key": "k-probe"}, probeBody), 200)
	cliProbe := cliSide.cli("", "adapter", "probe", "--json", "--principal-config", principal, "--idempotency-key", "k-probe",
		"--provider-key", "claude", "--executable-path", executable, "--protocol-version", "1", "--os", "windows", "--toolchain", "go", "--config-identity", "default",
		"--supports-start", "--supports-cancel", "--canonical-event-kinds", "TEXT_DELTA")
	// nonce/signature/expiresAt are random per probe by design: the tuple —
	// including the measured executable content hash and the capability
	// manifest hash — is what must match.
	same("adapter probe (tuple)", httpProbe.Body, cliResultDoc(t, cliProbe), "nonce", "signature", "expiresAt")

	registerBody := fmt.Sprintf(`{"token":%s,"capabilityManifest":{"supportsStart":true,"supportsResume":false,"supportsCancel":true,"canonicalEventKinds":["TEXT_DELTA"]}}`, httpProbe.Body)
	httpRegister := mustHTTP(t, httpSide.do("POST", "/adapter-builds", map[string]string{"Idempotency-Key": "k-register"}, registerBody), 200, 201)
	tokenFile := writeFile(t, "token.json", string(cliResultDoc(t, cliProbe)))
	cliRegister := cliSide.cli("", "adapter", "register", "--json", "--yes", "--principal-config", principal, "--idempotency-key", "k-register", "--file", tokenFile,
		"--supports-start", "--supports-cancel", "--canonical-event-kinds", "TEXT_DELTA")
	same("adapter register (registeredBy is the principal)", httpRegister.Body, cliResultDoc(t, cliRegister))
	same("adapter list",
		mustHTTP(t, httpSide.do("GET", "/adapter-builds", nil, ""), 200).Body,
		[]byte(cliSide.cli("", "adapter", "list", "--json").Stdout))
}

// TestHTTPAndCLIShareOneReplayAuthority proves, on ONE installation, that a
// receipt written through one delivery adapter is the replay authority for the
// other (contract point 1: "Command receipt trong application UnitOfWork là
// replay authority duy nhất; delivery không cache response, ghi receipt hoặc
// gọi internal worker command").
func TestHTTPAndCLIShareOneReplayAuthority(t *testing.T) {
	inst := newLiveInstall(t, testPrincipal)
	principal := inst.principalFile()

	// HTTP writes, the CLI replays.
	httpCreate := mustHTTP(t, inst.do("POST", "/projects", map[string]string{"Idempotency-Key": "k-1"}, `{"name":"alpha"}`), 201)
	replayed := inst.cli(`{"name":"alpha"}`, "project", "create", "--principal-config", principal, "--idempotency-key", "k-1")
	var envelope struct {
		Replayed bool `json:"replayed"`
	}
	if err := json.Unmarshal([]byte(replayed.Stdout), &envelope); err != nil || !envelope.Replayed {
		t.Fatalf("the CLI must REPLAY the HTTP-written receipt (replayed=true), got %+v err=%v stdout=%s", envelope, err, replayed.Stdout)
	}
	if h, c := canonical(t, httpCreate.Body), canonical(t, cliResultDoc(t, replayed)); h != c {
		t.Errorf("the replayed result differs from the original\n--- HTTP\n%s\n--- CLI replay\n%s", h, c)
	}

	// The CLI writes, HTTP replays.
	cliCreate := inst.cli(`{"name":"beta"}`, "project", "create", "--principal-config", principal, "--idempotency-key", "k-2")
	httpReplay := mustHTTP(t, inst.do("POST", "/projects", map[string]string{"Idempotency-Key": "k-2"}, `{"name":"beta"}`), 200, 201)
	if h, c := canonical(t, httpReplay.Body), canonical(t, cliResultDoc(t, cliCreate)); h != c {
		t.Errorf("HTTP's replay of a CLI-written receipt differs\n--- HTTP replay\n%s\n--- CLI original\n%s", h, c)
	}
	if list := mustHTTP(t, inst.do("GET", "/projects", nil, ""), 200); strings.Count(string(list.Body), `"name"`) != 2 {
		t.Errorf("exactly two projects must exist (no duplicate from either replay): %s", list.Body)
	}

	// The same key with a different payload conflicts on BOTH surfaces.
	conflictHTTP := inst.do("POST", "/projects", map[string]string{"Idempotency-Key": "k-1"}, `{"name":"gamma"}`)
	if conflictHTTP.Status < 400 || conflictHTTP.Status >= 500 {
		t.Errorf("HTTP: same key + different payload must be a client error, got %d %s", conflictHTTP.Status, conflictHTTP.Body)
	}
	conflictCLI := inst.cli(`{"name":"gamma"}`, "project", "create", "--principal-config", principal, "--idempotency-key", "k-2")
	if conflictCLI.Code == cli.ExitSuccess || !strings.Contains(conflictCLI.Stderr, "idempotency key reused with a different request") {
		t.Errorf("CLI: same key + different payload must fail as an idempotency conflict, got exit=%d stderr=%q", conflictCLI.Code, conflictCLI.Stderr)
	}
}

// TestNeitherSurfaceLetsTheCallerChooseTheActor proves ADR-028's "Actor và
// ActorRoles là authentication context, không phải input tự khai" on both
// surfaces with the same principal: spoofed identity headers are ignored by
// HTTP (the recorded actor stays the bound principal) and an --actor/--roles
// flag does not exist on the CLI (a usage error, nothing dispatched).
func TestNeitherSurfaceLetsTheCallerChooseTheActor(t *testing.T) {
	inst := newLiveInstall(t, testPrincipal)
	principal := inst.principalFile()
	blockFile := writeFile(t, "block.json", validBlockDocument)

	mustHTTP(t, inst.do("POST", "/definitions/BLOCK", map[string]string{"Idempotency-Key": "k-create"}, `{"definitionId":"blk-1","name":"block one"}`), 201)
	spoofed := mustHTTP(t, inst.do("POST", "/definitions/BLOCK/blk-1/publish", map[string]string{
		"Idempotency-Key": "k-publish", "X-Actor": "mallory", "Actor": "mallory", "X-Aw-Actor": "mallory", "X-Roles": "admin",
	}, fmt.Sprintf(`{"content":%q,"format":"json","schemaVersion":1}`, validBlockDocument)), 201)
	if got := idOf(t, spoofed.Body, "publishedBy"); got != "alice" {
		t.Errorf("HTTP recorded publishedBy=%q despite spoofed identity headers, want the bound principal alice", got)
	}

	for _, flagName := range []string{"--actor", "--role", "--roles", "--actor-roles"} {
		r := inst.cli("", "definition", "publish", "--kind", "BLOCK", "--yes", "--principal-config", principal, "--idempotency-key", "k-spoof-"+flagName[2:], "--file", blockFile, flagName, "mallory", "blk-1")
		if r.Code != cli.ExitUsage {
			t.Errorf("CLI %s: exit=%d stdout=%q stderr=%q, want a usage error (no such flag)", flagName, r.Code, r.Stdout, r.Stderr)
		}
	}
	// Nothing was dispatched by the spoof attempts: still exactly one version.
	if list := mustHTTP(t, inst.do("GET", "/definitions/BLOCK/blk-1/versions", nil, ""), 200); strings.Count(string(list.Body), `"versionNumber"`) != 1 {
		t.Errorf("a rejected CLI spoof must dispatch nothing: %s", list.Body)
	}
}

// TestSameInvalidInputIsRejectedTheSameWayOverHTTPAndCLI: the two surfaces
// run the SAME validation and classify the failure the same way.
func TestSameInvalidInputIsRejectedTheSameWayOverHTTPAndCLI(t *testing.T) {
	inst := newLiveInstall(t, testPrincipal)
	principal := inst.principalFile()

	// An invalid safe-settings document: same message, same typed code.
	invalid := `{"managedWorkspaceRoot":"","managedArtifactRoot":"","evidenceRetention":"0s","processOutputLimit":0,"providerExecutablePath":"","providerDefaultModel":"","providerCredentialRef":"keychain:aw-parity"}`
	httpBad := inst.do("PUT", "/settings/safe", map[string]string{"Idempotency-Key": "k-bad", "If-Match": `"1"`}, invalid)
	cliBad := inst.cli("", "settings", "update", "--json", "--principal-config", principal, "--expected-version", "1", "--idempotency-key", "k-bad", "--file", writeFile(t, "bad.json", invalid))
	if httpBad.Status != 400 {
		t.Fatalf("HTTP status %d, want 400: %s", httpBad.Status, httpBad.Body)
	}
	var httpErr, cliErr struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Details []struct {
				Message string `json:"message"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(httpBad.Body, &httpErr); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(cliBad.Stdout), &cliErr); err != nil {
		t.Fatalf("CLI --json failure is not one typed document: %v\n%s", err, cliBad.Stdout)
	}
	if httpErr.Error.Code != "INVALID_REQUEST" || cliErr.Error.Code != "INVALID_REQUEST" {
		t.Errorf("error codes: HTTP %q, CLI %q, want INVALID_REQUEST on both", httpErr.Error.Code, cliErr.Error.Code)
	}
	if len(httpErr.Error.Details) == 0 || !strings.Contains(cliErr.Error.Message, httpErr.Error.Details[0].Message) {
		t.Errorf("both surfaces must report the same validation message: HTTP %+v, CLI %q", httpErr.Error.Details, cliErr.Error.Message)
	}
	if cliBad.Code != cli.ExitUsage {
		t.Errorf("CLI exit=%d, want %d for an invalid document", cliBad.Code, cli.ExitUsage)
	}

	// A stale version: both refuse to update.
	mustHTTP(t, inst.do("PUT", "/settings/safe", map[string]string{"Idempotency-Key": "k-ok", "If-Match": `"1"`}, validSettingsDocument), 200)
	staleHTTP := inst.do("PUT", "/settings/safe", map[string]string{"Idempotency-Key": "k-stale-http", "If-Match": `"1"`}, strings.Replace(validSettingsDocument, "model-1", "model-2", 1))
	staleCLI := inst.cli("", "settings", "update", "--json", "--principal-config", principal, "--expected-version", "1", "--idempotency-key", "k-stale-cli",
		"--file", writeFile(t, "settings2.json", strings.Replace(validSettingsDocument, "model-1", "model-3", 1)))
	if staleHTTP.Status < 400 || staleCLI.Code == cli.ExitSuccess {
		t.Errorf("a stale expected version must be refused on both: HTTP %d %s / CLI exit=%d %s", staleHTTP.Status, staleHTTP.Body, staleCLI.Code, staleCLI.Stdout)
	}

	// An unknown target is "not found" on both.
	notFoundHTTP := inst.do("GET", "/definitions/versions/no-such-version", nil, "")
	notFoundCLI := inst.cli("", "version", "show", "no-such-version")
	if notFoundHTTP.Status != 404 || notFoundCLI.Code != cli.ExitFailure || !strings.Contains(notFoundCLI.Stderr, "not found") {
		t.Errorf("unknown version: HTTP %d %s / CLI exit=%d stderr=%q, want 404 and a not-found failure", notFoundHTTP.Status, notFoundHTTP.Body, notFoundCLI.Code, notFoundCLI.Stderr)
	}
}

// TestUnknownProviderKeyIsAcceptedIdenticallyOverHTTPAndCLI pins a reviewed
// behavior CHANGE from the retired pre-V6 `aw adapter probe`, which rejected
// any provider key other than claude/codex as a usage error: since V6-10I the
// provider key is an open, non-empty string at the application layer
// (internal/domain/adapterbuild.CandidateTuple), and HTTP and the CLI leaf
// both accept an unknown one. The test asserts the two surfaces agree — the
// property this task guards — and fails loudly the day either side starts
// validating, so closing the vocabulary is a conscious, both-surfaces change.
func TestUnknownProviderKeyIsAcceptedIdenticallyOverHTTPAndCLI(t *testing.T) {
	inst := newLiveInstall(t, testPrincipal)
	executable := writeFile(t, "provider-cli", "bytes")
	httpProbe := inst.do("POST", "/adapter-builds/probe", map[string]string{"Idempotency-Key": "k-http"}, fmt.Sprintf(
		`{"providerKey":"not-a-real-provider","executablePath":%q,"protocolVersion":"1","capabilityManifest":{"supportsStart":true,"supportsResume":false,"supportsCancel":false},"os":"windows","toolchain":"go","configIdentity":"default"}`, executable))
	cliProbe := inst.cli("", "adapter", "probe", "--json", "--principal-config", inst.principalFile(), "--idempotency-key", "k-cli",
		"--provider-key", "not-a-real-provider", "--executable-path", executable, "--protocol-version", "1", "--os", "windows", "--toolchain", "go",
		"--config-identity", "default", "--supports-start")
	if httpProbe.Status != 200 || cliProbe.Code != cli.ExitSuccess {
		t.Fatalf("HTTP %d %s / CLI exit=%d %s — both surfaces must treat an unknown provider key the same way (currently: accept)",
			httpProbe.Status, httpProbe.Body, cliProbe.Code, cliProbe.Stderr)
	}
}
