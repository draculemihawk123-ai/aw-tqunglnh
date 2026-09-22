package main

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/adapters/sqlite"
	"github.com/taQuangLing/agent-workflow/internal/app/ports"
	domainadapterbuild "github.com/taQuangLing/agent-workflow/internal/domain/adapterbuild"
)

// This file ports, against the V6-15O routing (`aw adapter ...` ->
// internal/delivery/cli/adapterbuild, `aw definition|version ...` ->
// internal/delivery/cli/definitions), every behavior the retired pre-V6
// cmd/aw/adapter_test.go and cmd/aw/definition_test.go asserted through
// run() and that the leaf packages' own tests do not already prove one for
// one. baocaov6checklist.md's V6-15O section carries the full old-test ->
// new-test map; the comment on each test below names the retired test it
// replaces. The flag shapes differ by design (the legacy `--db/--actor/
// --definition-id/--version-id` handlers are gone; principal comes from
// --principal-config, ids are positional) — the asserted BEHAVIOR is what is
// preserved.

// ---- adapter helpers -----------------------------------------------------

func adapterManifestFlags() []string {
	return []string{"--supports-start", "--supports-cancel", "--canonical-event-kinds", "TEXT_DELTA"}
}

func adapterProbeArgs(executable string, idempotencyKey string, extra ...string) []string {
	args := []string{"adapter", "probe", "--json",
		"--provider-key", "claude", "--executable-path", executable,
		"--protocol-version", "1", "--os", runtime.GOOS, "--toolchain", "go", "--config-identity", "default"}
	if idempotencyKey != "" {
		args = append(args, "--idempotency-key", idempotencyKey)
	}
	args = append(args, adapterManifestFlags()...)
	return append(args, extra...)
}

func (a *awInstall) probeToken(t *testing.T, executable, idempotencyKey string) domainadapterbuild.CandidateToken {
	t.Helper()
	doc := decodeResult(t, a.mustOK("", adapterProbeArgs(executable, idempotencyKey)...))
	var token domainadapterbuild.CandidateToken
	if err := json.Unmarshal(doc.Result, &token); err != nil {
		t.Fatalf("decode candidate token: %v\n%s", err, doc.Result)
	}
	return token
}

func writeTokenFile(t *testing.T, token domainadapterbuild.CandidateToken) string {
	t.Helper()
	encoded, err := json.Marshal(token)
	if err != nil {
		t.Fatal(err)
	}
	return writeFixture(t, "candidate-token.json", string(encoded))
}

func registerArgs(tokenFile, idempotencyKey string, extra ...string) []string {
	args := []string{"adapter", "register", "--json", "--yes", "--file", tokenFile}
	if idempotencyKey != "" {
		args = append(args, "--idempotency-key", idempotencyKey)
	}
	args = append(args, adapterManifestFlags()...)
	return append(args, extra...)
}

type registerResult struct {
	Build struct {
		ID                    string `json:"id"`
		ProviderKey           string `json:"providerKey"`
		RegisteredBy          string `json:"registeredBy"`
		ExecutableContentHash string `json:"executableContentHash"`
	} `json:"build"`
	AlreadyExisted bool `json:"alreadyExisted"`
}

func (a *awInstall) registerOK(t *testing.T, tokenFile, idempotencyKey string, extra ...string) registerResult {
	t.Helper()
	doc := decodeResult(t, a.mustOK("", registerArgs(tokenFile, idempotencyKey, extra...)...))
	var result registerResult
	if err := json.Unmarshal(doc.Result, &result); err != nil {
		t.Fatalf("decode register result: %v\n%s", err, doc.Result)
	}
	return result
}

func (a *awInstall) listBuildIDs(t *testing.T) []string {
	t.Helper()
	var list struct {
		Builds []struct {
			ID string `json:"id"`
		} `json:"builds"`
	}
	if err := json.Unmarshal([]byte(a.mustOK("", "adapter", "list", "--json")), &list); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(list.Builds))
	for _, b := range list.Builds {
		ids = append(ids, b.ID)
	}
	return ids
}

func (a *awInstall) assertRegistryEmpty(t *testing.T, why string) {
	t.Helper()
	if ids := a.listBuildIDs(t); len(ids) != 0 {
		t.Fatalf("adapter list = %v, want an empty registry — %s", ids, why)
	}
}

func (a *awInstall) loadSigningKey(t *testing.T) []byte {
	t.Helper()
	store, err := sqlite.Open(context.Background(), a.db)
	if err != nil {
		t.Fatalf("open db to read the signing key: %v", err)
	}
	defer store.Close()
	var key []byte
	err = sqlite.NewUnitOfWork(store).WithReadOnly(context.Background(), func(tx ports.Tx) error {
		loaded, loadErr := tx.AdapterBuilds().LoadSigningKey(context.Background())
		key = loaded
		return loadErr
	})
	if err != nil {
		t.Fatalf("load signing key: %v", err)
	}
	return key
}

// ---- adapter: retired cmd/aw/adapter_test.go ----------------------------

// Ports TestAdapterProbe_DoesNotMutateRegistry (V2-07B's own "probe không
// mutate registry" bar) and TestAdapterCLI_TokenSurvivesSeparate
// ProbeAndRegisterInvocations (every runStreams call opens and closes its
// own database, so probe and register are two separate "processes" and the
// signing key must be read back from disk).
func TestOneShot_AdapterProbeNeverMutatesTheRegistry_AndTokenSurvivesSeparateProcesses(t *testing.T) {
	a := newAWInstall(t)
	executable := writeFixture(t, "claude-cli", "binary-v1")

	a.assertRegistryEmpty(t, "before any probe")
	token := a.probeToken(t, executable, "idem-probe")
	if token.Signature == "" {
		t.Fatal("probe should print a signed candidate token")
	}
	a.assertRegistryEmpty(t, "a probe only ever writes the signing key, never a registry row")

	// A separate invocation registers with the token an earlier one signed.
	result := a.registerOK(t, writeTokenFile(t, token), "idem-register")
	if result.AlreadyExisted || result.Build.ID == "" {
		t.Fatalf("register = %+v, want a fresh build with an id", result)
	}
}

// Ports TestAdapterProbeThenRegister_Succeeds plus the actor contract: the
// build's RegisteredBy is the PRINCIPAL (trusted --principal-config), never
// a per-command --actor flag (ADR-028) — the legacy test passed --actor.
func TestOneShot_AdapterRegisterRecordsThePrincipalNotAFlag(t *testing.T) {
	a := newAWInstall(t)
	executable := writeFixture(t, "claude-cli", "binary-v1")
	principalFile := writeFixture(t, "principal.json", `{"localPrincipal":{"actor":"alice","roles":["operator"]}}`)

	token := a.probeToken(t, executable, "idem-probe")
	result := a.registerOK(t, writeTokenFile(t, token), "idem-register", "--principal-config", principalFile)
	if result.Build.RegisteredBy != "alice" {
		t.Errorf("registeredBy = %q, want the principal file's actor alice", result.Build.RegisteredBy)
	}
	if result.Build.ProviderKey != "claude" {
		t.Errorf("providerKey = %q, want claude", result.Build.ProviderKey)
	}

	// There is no --actor flag anywhere: it is an ordinary unknown flag.
	_, stderr, code := a.aw("", append(registerArgs(writeTokenFile(t, token), "idem-register-2"), "--actor", "mallory")...)
	if code != exitUsage || !strings.Contains(stderr, "actor") {
		t.Errorf("--actor: exit=%d stderr=%q, want an unknown-flag usage error", code, stderr)
	}
}

// Ports TestAdapterRegister_DuplicateIsIdempotent: registering the identical
// measured tuple twice (each with its own token and idempotency key) reports
// alreadyExisted, keeps the original id and the original registeredBy, and
// never creates a second row.
func TestOneShot_AdapterRegisterDuplicateFingerprintIsIdempotent(t *testing.T) {
	a := newAWInstall(t)
	executable := writeFixture(t, "claude-cli", "binary-v1")
	first, second := writeFixture(t, "p1.json", `{"localPrincipal":{"actor":"operator-1","roles":["operator"]}}`), writeFixture(t, "p2.json", `{"localPrincipal":{"actor":"operator-2","roles":["operator"]}}`)

	firstResult := a.registerOK(t, writeTokenFile(t, a.probeToken(t, executable, "idem-probe-1")), "idem-register-1", "--principal-config", first)
	secondResult := a.registerOK(t, writeTokenFile(t, a.probeToken(t, executable, "idem-probe-2")), "idem-register-2", "--principal-config", second)

	if !secondResult.AlreadyExisted {
		t.Fatal("the second registration of the identical measured tuple must report alreadyExisted=true")
	}
	if secondResult.Build.ID != firstResult.Build.ID {
		t.Fatalf("second registration id %s != first %s", secondResult.Build.ID, firstResult.Build.ID)
	}
	if secondResult.Build.RegisteredBy != "operator-1" {
		t.Fatalf("registeredBy = %q, want the original operator-1 preserved (a duplicate register never overwrites)", secondResult.Build.RegisteredBy)
	}
	if ids := a.listBuildIDs(t); len(ids) != 1 {
		t.Fatalf("builds = %v, want exactly one (a duplicate register must never create a second row)", ids)
	}
}

// Ports TestAdapterRegister_DriftCreatesNewBuild: a genuinely different
// executable, re-probed, registers as a DISTINCT build — never an overwrite.
func TestOneShot_AdapterDriftedExecutableRegistersAsADistinctBuild(t *testing.T) {
	a := newAWInstall(t)
	executable := writeFixture(t, "claude-cli", "binary-v1")

	first := a.registerOK(t, writeTokenFile(t, a.probeToken(t, executable, "idem-probe-1")), "idem-register-1")
	if err := os.WriteFile(executable, []byte("binary-v2-genuinely-different"), 0o755); err != nil {
		t.Fatal(err)
	}
	second := a.registerOK(t, writeTokenFile(t, a.probeToken(t, executable, "idem-probe-2")), "idem-register-2")

	if second.Build.ID == first.Build.ID || second.AlreadyExisted {
		t.Fatalf("a drifted executable must register as a distinct build: first=%+v second=%+v", first, second)
	}
	if ids := a.listBuildIDs(t); len(ids) != 2 {
		t.Fatalf("builds = %v, want both the original and the drifted build", ids)
	}
}

// Ports TestAdapterRegister_RejectsExecutableSwappedBetweenProbeAndRegister
// (the TOCTOU-closing property ADR-022 exists for): swap the binary after
// probe, register with the now-stale token — a clean failure that names the
// drift, an empty stdout, and nothing persisted.
func TestOneShot_AdapterRegisterRejectsAnExecutableSwappedAfterProbe(t *testing.T) {
	a := newAWInstall(t)
	executable := writeFixture(t, "claude-cli", "binary-original")
	stale := writeTokenFile(t, a.probeToken(t, executable, "idem-probe"))

	if err := os.WriteFile(executable, []byte("binary-SWAPPED"), 0o755); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := a.aw("", registerArgs(stale, "idem-register")...)
	if code != exitFailure {
		t.Fatalf("register with a stale token: exit=%d, want %d; stdout=%q stderr=%q", code, exitFailure, stdout, stderr)
	}
	if !strings.Contains(stdout, "no longer matches") && !strings.Contains(stderr, "no longer matches") {
		t.Fatalf("the failure must explain the executable drift; stdout=%q stderr=%q", stdout, stderr)
	}
	a.assertRegistryEmpty(t, "a rejected registration must never persist anything")
}

// Ports TestAdapterRegister_RejectsExpiredToken: a token that is genuinely,
// correctly signed (with the installation's real key) but already expired is
// rejected for EXPIRY — distinct from the forged-signature case below.
func TestOneShot_AdapterRegisterRejectsAGenuinelySignedExpiredToken(t *testing.T) {
	a := newAWInstall(t)
	executable := writeFixture(t, "claude-cli", "binary-v1")
	token := a.probeToken(t, executable, "idem-probe")

	expired, err := domainadapterbuild.SignToken(token.Tuple, "expired-nonce", time.Now().UTC().Add(-time.Minute), a.loadSigningKey(t))
	if err != nil {
		t.Fatalf("sign an already-expired token: %v", err)
	}
	stdout, stderr, code := a.aw("", registerArgs(writeTokenFile(t, expired), "idem-register")...)
	if code != exitFailure {
		t.Fatalf("expired token: exit=%d stdout=%q stderr=%q, want a failure", code, stdout, stderr)
	}
	a.assertRegistryEmpty(t, "a rejected registration must never persist anything")
}

// Ports TestAdapterRegister_RejectsForgedSignature.
func TestOneShot_AdapterRegisterRejectsAForgedSignature(t *testing.T) {
	a := newAWInstall(t)
	executable := writeFixture(t, "claude-cli", "binary-v1")
	token := a.probeToken(t, executable, "idem-probe")
	token.Signature = "forged" + token.Signature

	stdout, stderr, code := a.aw("", registerArgs(writeTokenFile(t, token), "idem-register")...)
	if code != exitFailure {
		t.Fatalf("forged signature: exit=%d stdout=%q stderr=%q, want a failure", code, stdout, stderr)
	}
	a.assertRegistryEmpty(t, "a rejected registration must never persist anything")
}

// Ports the substance of TestAdapterProbe_CapabilityManifestIsSystemMeasured_
// NotClientSuppliable that survives V6-10J/V6-15F. The legacy assertion "no
// flag carries capability data" is deliberately reversed by those merged
// tasks (the leaf, like POST /adapter-builds, takes the manifest as
// caller-supplied flags — see internal/delivery/cli/adapterbuild/doc.go), so
// it cannot be ported. What still holds, and is asserted here through the
// real routing, is that a caller cannot LIE at registration: the manifest
// re-supplied to `adapter register` must hash to the one the probe token
// bound, or the register is rejected and nothing persists.
func TestOneShot_AdapterRegisterRejectsAManifestDifferentFromTheProbedOne(t *testing.T) {
	a := newAWInstall(t)
	executable := writeFixture(t, "claude-cli", "binary-v1")
	tokenFile := writeTokenFile(t, a.probeToken(t, executable, "idem-probe"))

	// Probe declared start+cancel+TEXT_DELTA; register claims resume too.
	stdout, stderr, code := a.aw("", "adapter", "register", "--json", "--yes", "--file", tokenFile, "--idempotency-key", "idem-register",
		"--supports-start", "--supports-cancel", "--supports-resume", "--canonical-event-kinds", "TEXT_DELTA")
	if code != exitFailure {
		t.Fatalf("a different manifest: exit=%d stdout=%q stderr=%q, want a failure", code, stdout, stderr)
	}
	a.assertRegistryEmpty(t, "a manifest mismatch must never persist anything")
}

// Ports TestAdapterShow_NotFoundIsCleanError and TestAdapterShow_
// ReturnsRegisteredBuild.
func TestOneShot_AdapterShowNotFoundAndFound(t *testing.T) {
	a := newAWInstall(t)

	// (The legacy message echoed the id — `adapter build "x" not found`; the
	// leaf reports the application layer's own leakage-safe
	// `ports: adapter build version not found`. The clean-failure contract —
	// exit 1, empty stdout, one explained stderr line — is what is ported.)
	stdout, stderr, code := a.aw("", "adapter", "show", "does-not-exist")
	if code != exitFailure || stdout != "" || !strings.Contains(stderr, "not found") {
		t.Fatalf("show unknown id: exit=%d stdout=%q stderr=%q, want a clean not-found failure on stderr", code, stdout, stderr)
	}
	if strings.Contains(stderr, "goroutine") || strings.Contains(stderr, "panic") {
		t.Errorf("a not-found must never dump a stack: %q", stderr)
	}

	executable := writeFixture(t, "claude-cli", "binary-v1")
	registered := a.registerOK(t, writeTokenFile(t, a.probeToken(t, executable, "idem-probe")), "idem-register")
	var shown struct {
		ID                    string `json:"id"`
		ExecutableContentHash string `json:"executableContentHash"`
	}
	if err := json.Unmarshal([]byte(a.mustOK("", "adapter", "show", "--json", registered.Build.ID)), &shown); err != nil {
		t.Fatal(err)
	}
	if shown.ID != registered.Build.ID || shown.ExecutableContentHash != registered.Build.ExecutableContentHash {
		t.Fatalf("show = %+v, want the registered build %+v", shown, registered.Build)
	}
}

// Ports the exit-code/usage contract of TestAdapter_MissingRequiredFlags,
// TestAdapterProbe_UnknownProviderIsUsageError (see the note below) and
// TestAdapter_MissingIdempotencyKeyIsUsageError.
func TestOneShot_AdapterUsageContract(t *testing.T) {
	a := newAWInstall(t)
	executable := writeFixture(t, "claude-cli", "binary-v1")
	tokenFile := writeTokenFile(t, a.probeToken(t, executable, "idem-probe"))

	for _, args := range [][]string{
		{"adapter"},
		{"adapter", "bogus-verb"},
		{"adapter", "probe", "--json"}, // every required flag missing
		{"adapter", "probe", "--json", "--provider-key", "claude"},      // executable etc. missing
		{"adapter", "register", "--json", "--yes"},                      // no token
		{"adapter", "register", "--json", "--yes", "--file", tokenFile}, // no --supports-start
		{"adapter", "show"},                          // no id
		{"adapter", "show", "a", "b"},                // two ids
		{"adapter", "list", "unexpected-positional"}, // list takes none
		{"adapter", "probe", "--json", "--executable-path", executable, "--no-such"}, // unknown flag
	} {
		if _, _, code := a.aw("", args...); code != exitUsage {
			t.Errorf("aw %v: exit=%d, want %d (usage)", args, code, exitUsage)
		}
	}

	// V6-10I's legacy "--idempotency-key is required" is superseded by
	// ADR-028: it is OPTIONAL, generated when omitted and ALWAYS returned so
	// the operator can retry with it.
	generated := decodeResult(t, a.mustOK("", adapterProbeArgs(executable, "")...))
	if generated.IdempotencyKey == "" {
		t.Fatal("a probe without --idempotency-key must return the generated key")
	}
}

// ---- definitions: retired cmd/aw/definition_test.go ---------------------

func (a *awInstall) createDefinition(t *testing.T, kind, id, name, key string) {
	t.Helper()
	a.mustOK(`{"definitionId":"`+id+`","name":"`+name+`"}`, "definition", "create", "--kind", kind, "--idempotency-key", key)
}

func (a *awInstall) publishOK(t *testing.T, kind, id, file, key string) resultDoc {
	t.Helper()
	return decodeResult(t, a.mustOK("", "definition", "publish", "--kind", kind, "--file", file, "--yes", "--idempotency-key", key, id))
}

// Ports the self-diff tail of TestDefinitionCLI_BlockFullFlow_...: a version
// diffed against itself is Identical with only "equal" lines, and the
// Workflow-kind version (which loads through a different repository path)
// resolves through `version show` and `version diff` too — the tail of
// TestDefinitionCLI_WorkflowFullFlow_ResolvesRealAgentProfile.
func TestOneShot_VersionSelfDiffAndWorkflowVersionsResolve(t *testing.T) {
	a := newAWInstall(t)
	a.createDefinition(t, "BLOCK", "blk-1", "test block", "idem-create-blk")
	var block versionDoc
	if err := json.Unmarshal(a.publishOK(t, "BLOCK", "blk-1", writeFixture(t, "b.json", blockDocumentWithTimeout(60)), "idem-publish-blk").Result, &block); err != nil {
		t.Fatal(err)
	}

	a.createDefinition(t, "AGENT_PROFILE", "profile-1", "test profile", "idem-create-profile")
	var profile versionDoc
	if err := json.Unmarshal(a.publishOK(t, "AGENT_PROFILE", "profile-1", writeFixture(t, "p.json", validAgentProfileDocument), "idem-publish-profile").Result, &profile); err != nil {
		t.Fatal(err)
	}
	a.createDefinition(t, "WORKFLOW", "wf-1", "test workflow", "idem-create-wf")
	var workflow versionDoc
	if err := json.Unmarshal(a.publishOK(t, "WORKFLOW", "wf-1", writeFixture(t, "wf.json", workflowDocumentPinning("profile-1", profile.ID)), "idem-publish-wf").Result, &workflow); err != nil {
		t.Fatal(err)
	}

	for name, id := range map[string]string{"block": block.ID, "workflow": workflow.ID} {
		var shown versionDoc
		if err := json.Unmarshal([]byte(a.mustOK("", "version", "show", id)), &shown); err != nil || shown.ID != id {
			t.Fatalf("%s: version show = %+v (%v), want %s", name, shown, err, id)
		}
		var diff struct {
			Identical  bool `json:"identical"`
			SourceDiff []struct {
				Op string `json:"op"`
			} `json:"sourceDiff"`
		}
		if err := json.Unmarshal([]byte(a.mustOK("", "version", "diff", id, id)), &diff); err != nil {
			t.Fatal(err)
		}
		if !diff.Identical {
			t.Errorf("%s: a version diffed against itself must be Identical", name)
		}
		for _, line := range diff.SourceDiff {
			if line.Op != "equal" {
				t.Errorf("%s: a self diff may contain only equal lines, got %+v", name, line)
			}
		}
	}
}

// Ports the dedup and conflict assertions of TestDefinitionCLI_
// PublishDedupesIdenticalContentUnderFreshIdempotencyKey and
// TestDefinitionCLI_PublishConflictingIdempotencyKey_IsCleanError with their
// original strictness: identical content under a fresh key leaves EXACTLY ONE
// version, and the conflict is an explained clean failure (no stack trace).
func TestOneShot_PublishDedupLeavesOneVersionAndAConflictIsExplained(t *testing.T) {
	a := newAWInstall(t)
	a.createDefinition(t, "BLOCK", "blk-dedup", "dedup target", "idem-create")
	fileA := writeFixture(t, "a.json", blockDocumentWithTimeout(60))
	fileB := writeFixture(t, "b.json", blockDocumentWithTimeout(120))

	a.publishOK(t, "BLOCK", "blk-dedup", fileA, "idem-publish-1")
	a.publishOK(t, "BLOCK", "blk-dedup", fileA, "idem-publish-2")
	var listed struct {
		Items []versionDoc `json:"items"`
	}
	if err := json.Unmarshal([]byte(a.mustOK("", "definition", "versions", "--kind", "BLOCK", "blk-dedup")), &listed); err != nil || len(listed.Items) != 1 {
		t.Fatalf("versions = %+v (%v), want exactly one (identical content must dedupe, never duplicate)", listed, err)
	}

	_, stderr, code := a.aw("", "definition", "publish", "--kind", "BLOCK", "--file", fileB, "--yes", "--idempotency-key", "idem-publish-1", "blk-dedup")
	if code != exitFailure {
		t.Fatalf("conflicting republish: exit=%d stderr=%q, want %d", code, stderr, exitFailure)
	}
	if !strings.Contains(stderr, "idempotency key reused with a different request") {
		t.Errorf("stderr should clearly explain the conflict, got %q", stderr)
	}
	if strings.Contains(stderr, "goroutine") || strings.Contains(stderr, "panic") {
		t.Errorf("stderr must never contain a raw Go stack trace, got %q", stderr)
	}
}

// Ports TestDefinitionCLI_WorkflowPublish_UnresolvablePinIsCleanError with
// its message assertion.
func TestOneShot_UnresolvablePinNamesTheProblem(t *testing.T) {
	a := newAWInstall(t)
	a.createDefinition(t, "WORKFLOW", "wf-bad-pin", "test workflow", "idem-create")
	file := writeFixture(t, "wf.json", workflowDocumentPinning("profile-does-not-exist", "profile-does-not-exist-v1"))

	stdout, stderr, code := a.aw("", "definition", "validate", "--kind", "WORKFLOW", "--file", file, "wf-bad-pin")
	if code != exitFailure {
		t.Fatalf("exit=%d, want %d; stdout=%q stderr=%q", code, exitFailure, stdout, stderr)
	}
	if combined := stdout + stderr; !strings.Contains(combined, "does not resolve to any published version") {
		t.Errorf("the failure must explain the unresolved pin; stdout=%q stderr=%q", stdout, stderr)
	}
}

// Ports TestDefinitionCLI_SafeDiagnostics_MalformedInput: every malformed
// document is a clean, bounded, non-panicking failure. (The leaf's contract
// differs from the legacy one in one respect, by design (V6-15E): an
// INVALID document prints its structured diagnostics as the one JSON document
// on stdout before failing, where the legacy handler printed nothing there.)
func TestOneShot_MalformedDocumentsFailCleanly(t *testing.T) {
	a := newAWInstall(t)
	a.createDefinition(t, "BLOCK", "blk-malformed", "malformed target", "idem-create")

	cases := []struct{ name, content string }{
		{"bad JSON syntax", `{"compatibleNodeTypes": [`},
		{"unknown field", `{"compatibleNodeTypes":["AGENT"],"doneCondition":"result.outcome","executorRef":{"definitionId":"cmd-1","kind":"COMMAND","versionId":"cmd-1-v1"},"outcomes":["success"],"scopeSelector":{"access":"WRITE"},"timeoutSeconds":60,"thisFieldDoesNotExist":true}`},
		{"wrong kind (agent profile document validated as BLOCK)", validAgentProfileDocument},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file := writeFixture(t, "malformed.json", tc.content)
			stdout, stderr, code := a.aw("", "definition", "validate", "--kind", "BLOCK", "--file", file, "blk-malformed")
			if code != exitFailure && code != exitUsage {
				t.Fatalf("exit=%d, want a failure; stdout=%q stderr=%q", code, stdout, stderr)
			}
			if strings.Contains(stdout+stderr, "goroutine") || strings.Contains(stdout+stderr, "panic") {
				t.Fatalf("output must never contain a raw Go stack trace: %q %q", stdout, stderr)
			}
			if len(stderr) > 4000 {
				t.Fatalf("stderr should be a bounded, human-sized message, got %d bytes", len(stderr))
			}
			if stdout != "" {
				var doc struct {
					Valid bool `json:"valid"`
				}
				if err := json.Unmarshal([]byte(stdout), &doc); err != nil || doc.Valid {
					t.Fatalf("stdout, when present, must be one valid:false diagnostics document: %q (%v)", stdout, err)
				}
			}
		})
	}
}

// Ports TestDefinitionCLI_UnknownKindIsUsageError, ...ShowNotFoundIsCleanError,
// ...DiffNotFoundIsCleanError and the exit-code/usage contract of
// TestDefinitionCLI_MissingRequiredFlags.
func TestOneShot_DefinitionUsageAndNotFoundContract(t *testing.T) {
	a := newAWInstall(t)
	file := writeFixture(t, "block.json", blockDocumentWithTimeout(60))

	if _, _, code := a.aw("", "definition", "validate", "--kind", "NOT_A_REAL_KIND", "--file", file, "x"); code != exitUsage {
		t.Errorf("an unknown --kind must be a usage error, exit=%d", code)
	}

	stdout, stderr, code := a.aw("", "version", "show", "does-not-exist")
	if code != exitFailure || stdout != "" || !strings.Contains(stderr, "not found") {
		t.Errorf("version show unknown id: exit=%d stdout=%q stderr=%q, want a clean not-found failure", code, stdout, stderr)
	}
	stdout, stderr, code = a.aw("", "version", "diff", "does-not-exist-a", "does-not-exist-b")
	if code != exitFailure || stdout != "" || !strings.Contains(stderr, "not found") {
		t.Errorf("version diff unknown ids: exit=%d stdout=%q stderr=%q, want a clean not-found failure", code, stdout, stderr)
	}

	a.createDefinition(t, "BLOCK", "x", "existing definition, no document supplied", "idem-create-x")
	for _, args := range [][]string{
		{"definition"},
		{"definition", "bogus-verb"},
		{"definition", "create"},                              // no --kind
		{"definition", "create", "--kind", "BLOCK"},           // no body
		{"definition", "validate"},                            // no id
		{"definition", "validate", "--kind", "BLOCK", "x"},    // existing id, empty document
		{"definition", "publish"},                             // no id
		{"definition", "publish", "--kind", "BLOCK", "--yes"}, // no id (gate passed)
		{"definition", "list"},                                // no --kind
		{"definition", "show"},                                // no id
		{"definition", "versions", "--kind", "BLOCK"},         // no id
		{"version"},
		{"version", "show"},
		{"version", "diff", "only-one-id"},
	} {
		if _, _, code := a.aw("", args...); code != exitUsage {
			t.Errorf("aw %v: exit=%d, want %d (usage)", args, code, exitUsage)
		}
	}

	// A project-scoped definition is a distinct scope from the global one:
	// the same id is not visible through the other scope (leakage-normalized
	// as not found).
	a.createDefinition(t, "BLOCK", "blk-scope", "scoped", "idem-create-scope")
	_, _, code = a.aw("", "definition", "versions", "--kind", "BLOCK", "--project-id", "some-project", "blk-scope")
	if code != exitFailure {
		t.Errorf("a global definition read through a project scope must fail closed, exit=%d", code)
	}
}
