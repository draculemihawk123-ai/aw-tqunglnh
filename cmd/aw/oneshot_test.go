package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/delivery/clicompose"
)

// This file is V6-15O's composition-level proof that `aw <resource>
// <action>` really routes: every test drives the exact run()/runStreams
// entry point main() calls, against a REAL SQLite database and real
// artifact/workspace directories (never a fake UnitOfWork), one `run` call
// per simulated process. It also carries the composition-level flows the
// retired pre-V6 `aw definition`/`aw adapter` handlers used to prove
// (create -> validate -> publish -> list -> show -> diff, Workflow pin
// resolution, probe -> register -> list -> show), now through the V6-15E/
// V6-15F leaves — the per-leaf behavior matrices (scope negatives, replay,
// concurrency, redaction) stay in internal/delivery/cli/...'s own tests.
//
// Only the leaves that define --json (adapter, doctor, health, run start,
// settings) accept it; every other finite leaf always prints one JSON
// document, so these tests pass --json only where the leaf defines it.

type awInstall struct {
	t                          *testing.T
	db, artifactRoot, workRoot string
}

func newAWInstall(t *testing.T) *awInstall {
	t.Helper()
	dir := t.TempDir()
	artifactRoot := filepath.Join(dir, "artifacts")
	if err := os.MkdirAll(artifactRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	return &awInstall{t: t, db: filepath.Join(dir, "aw.db"), artifactRoot: artifactRoot, workRoot: filepath.Join(dir, "workspaces")}
}

// aw runs one invocation with the installation's global options prepended,
// stdin fed from stdin, and never interactive.
func (a *awInstall) aw(stdin string, args ...string) (stdout, stderr string, code exitCode) {
	a.t.Helper()
	full := append([]string{"--db", a.db, "--artifact-root", a.artifactRoot, "--workspace-root", a.workRoot}, args...)
	return awRaw(stdin, full...)
}

func awRaw(stdin string, args ...string) (stdout, stderr string, code exitCode) {
	var out, errBuf bytes.Buffer
	code = runStreams(context.Background(), args, strings.NewReader(stdin), false, &out, &errBuf)
	return out.String(), errBuf.String(), code
}

func (a *awInstall) mustOK(stdin string, args ...string) string {
	a.t.Helper()
	stdout, stderr, code := a.aw(stdin, args...)
	if code != exitSuccess {
		a.t.Fatalf("aw %v: exit=%d stderr=%q stdout=%q", args, code, stderr, stdout)
	}
	return stdout
}

type resultDoc struct {
	IdempotencyKey string          `json:"idempotencyKey"`
	Replayed       bool            `json:"replayed"`
	Result         json.RawMessage `json:"result"`
}

func decodeResult(t *testing.T, stdout string) resultDoc {
	t.Helper()
	var doc resultDoc
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("decode result envelope: %v\n%s", err, stdout)
	}
	return doc
}

func writeFixture(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
	return path
}

func blockDocumentWithTimeout(timeoutSeconds int) string {
	return fmt.Sprintf(`{"compatibleNodeTypes":["AGENT"],"doneCondition":"result.outcome","executorRef":{"definitionId":"cmd-1","kind":"COMMAND","versionId":"cmd-1-v1"},"outcomes":["failure","success"],"policyRefs":[{"definitionId":"policy-1","kind":"POLICY","versionId":"policy-1-v1"}],"requiredCapabilities":["INTEGRATION_MULTI_REPOSITORY_WRITE"],"scopeSelector":{"access":"WRITE","pathScopes":["src/**"]},"timeoutSeconds":%d}`, timeoutSeconds)
}

const validAgentProfileDocument = `{"budget":{"maxTokens":100000},"compatibility":{"os":["linux","windows"],"toolchain":["git"]},"contextPolicyRef":{"definitionId":"policy-context-1","kind":"POLICY","versionId":"policy-context-1-v1"},"model":"claude-opus-4","providerKey":"claude","requiredCapabilities":["INTEGRATION_MULTI_REPOSITORY_WRITE"],"toolRefs":["read_file","write_file"]}`

func workflowDocumentPinning(profileDefinitionID, profileVersionID string) string {
	return fmt.Sprintf(`{"schemaVersion":"1","nodes":[{"key":"start","type":"START","outcomes":["go"]},{"key":"agent","type":"AGENT","outcomes":["done"],"agent":{"profileRef":{"kind":"AGENT_PROFILE","definitionId":%q,"versionId":%q},"role":"MAKER"}},{"key":"end","type":"END"}],"edges":[{"key":"e1","from":"start","outcome":"go","to":"agent"},{"key":"e2","from":"agent","outcome":"done","to":"end"}]}`, profileDefinitionID, profileVersionID)
}

type versionDoc struct {
	ID            string `json:"id"`
	DefinitionID  string `json:"definitionId"`
	Kind          string `json:"kind"`
	VersionNumber uint64 `json:"versionNumber"`
	SourceHash    string `json:"sourceHash"`
	Dependencies  struct {
		Pins []struct {
			DefinitionID string `json:"definitionId"`
			VersionID    string `json:"versionId"`
		} `json:"pins"`
	} `json:"dependencies"`
}

// ---- process-level commands ---------------------------------------------

func TestOneShot_VersionAndHelp(t *testing.T) {
	stdout, stderr, code := awRaw("", "version")
	if code != exitSuccess || !strings.HasPrefix(stdout, "aw ") || stderr != "" {
		t.Fatalf("aw version: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if _, _, code := awRaw("", "version", "extra"); code != exitUsage {
		t.Errorf("aw version extra: exit=%d, want %d", code, exitUsage)
	}

	helpOut, _, code := awRaw("", "help")
	if code != exitSuccess {
		t.Fatalf("aw help exit=%d", code)
	}
	for _, want := range []string{"work-item", "release-set", "projection", "--db", "--yes", "--principal-config"} {
		if !strings.Contains(helpOut, want) {
			t.Errorf("aw help does not mention %q", want)
		}
	}
}

// ---- routing ------------------------------------------------------------

func TestOneShot_HealthLiveNeedsNoInstallation(t *testing.T) {
	stdout, _, code := awRaw("", "health", "live", "--json")
	if code != exitSuccess || !strings.Contains(stdout, `"live"`) {
		t.Fatalf("health live: exit=%d stdout=%q", code, stdout)
	}
}

func TestOneShot_ResourceCommandsNeedAnInstallationAndFailBeforeAnyIO(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "never-created.db")
	// No --db and no AW_DB: a usage error before any I/O.
	t.Setenv("AW_DB", "")
	_, stderr, code := awRaw("", "project", "list")
	if code != exitUsage || !strings.Contains(stderr, "--db is required") {
		t.Fatalf("project list without --db: exit=%d stderr=%q", code, stderr)
	}
	if _, err := os.Stat(dbPath); err == nil {
		t.Fatal("a rejected invocation created a database file")
	}

	// AW_DB alone selects the installation.
	t.Setenv("AW_DB", dbPath)
	stdout, stderr, code := awRaw("", "project", "list")
	if code != exitSuccess {
		t.Fatalf("project list with AW_DB: exit=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "[") && !strings.Contains(stdout, "{") {
		t.Errorf("project list did not print JSON: %q", stdout)
	}
}

func TestOneShot_GlobalOptionsMayPrecedeTheResource(t *testing.T) {
	a := newAWInstall(t)
	stdout, stderr, code := awRaw("", "--db", a.db, "project", "list")
	if code != exitSuccess {
		t.Fatalf("aw --db x project list: exit=%d stderr=%q stdout=%q", code, stderr, stdout)
	}
}

func TestOneShot_ArtifactCommandsNeedAnExistingArtifactRoot(t *testing.T) {
	a := newAWInstall(t)
	_, stderr, code := awRaw("", "--db", a.db, "--artifact-root", filepath.Join(t.TempDir(), "missing"), "evidence", "list", "--project-id", "p-1", "wi-1")
	if code != exitUsage || !strings.Contains(stderr, "is not an existing directory") {
		t.Fatalf("exit=%d stderr=%q, want a usage error about the artifact root", code, stderr)
	}
}

// ---- catalog: create / list / show and cross-process replay -------------

func TestOneShot_ProjectCatalogAndCrossProcessReplay(t *testing.T) {
	a := newAWInstall(t)

	first := decodeResult(t, a.mustOK(`{"name":"alpha"}`, "project", "create", "--idempotency-key", "k-create-alpha"))
	if first.Replayed || first.IdempotencyKey != "k-create-alpha" {
		t.Fatalf("first create = %+v", first)
	}
	var generic map[string]any
	if err := json.Unmarshal(first.Result, &generic); err != nil {
		t.Fatal(err)
	}
	projectID := fmt.Sprint(firstNonEmpty(generic, "ProjectID", "projectId", "ID", "id"))
	if projectID == "" || projectID == "<nil>" {
		t.Fatalf("create result carries no project id: %s", first.Result)
	}

	// A second, separate process: same key + same payload replays the stored
	// result, never a second project.
	second := decodeResult(t, a.mustOK(`{"name":"alpha"}`, "project", "create", "--idempotency-key", "k-create-alpha"))
	if !second.Replayed {
		t.Fatalf("second create with the same key must replay: %+v", second)
	}
	if string(second.Result) != string(first.Result) {
		t.Errorf("replayed result differs:\n first: %s\nsecond: %s", first.Result, second.Result)
	}

	// The same key with a different payload is a conflict, not a replay.
	if _, stderr, code := a.aw(`{"name":"beta"}`, "project", "create", "--idempotency-key", "k-create-alpha"); code != exitFailure {
		t.Errorf("same key different payload: exit=%d stderr=%q, want a failure", code, stderr)
	}

	listOut := a.mustOK("", "project", "list")
	if strings.Count(listOut, "alpha") != 1 {
		t.Errorf("project list should show exactly one 'alpha':\n%s", listOut)
	}
	if showOut := a.mustOK("", "project", "show", projectID); !strings.Contains(showOut, "alpha") {
		t.Errorf("project show did not return the project:\n%s", showOut)
	}
}

func firstNonEmpty(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil && fmt.Sprint(v) != "" {
			return v
		}
	}
	return ""
}

// ---- definitions: the composition-level flows V2-11's CLI used to prove --

func TestOneShot_DefinitionBlockFlow(t *testing.T) {
	a := newAWInstall(t)
	fileA := writeFixture(t, "block-a.json", blockDocumentWithTimeout(60))
	fileB := writeFixture(t, "block-b.json", blockDocumentWithTimeout(120))

	a.mustOK(`{"definitionId":"blk-1","name":"test block"}`, "definition", "create", "--kind", "BLOCK", "--idempotency-key", "idem-create-blk-1")

	// validate is a dry run: nothing persists.
	a.mustOK("", "definition", "validate", "--kind", "BLOCK", "--file", fileA, "blk-1")
	if versions := a.mustOK("", "definition", "versions", "--kind", "BLOCK", "blk-1"); strings.Contains(versions, `"versionNumber"`) {
		t.Fatalf("a dry-run validate must never persist a version:\n%s", versions)
	}

	// publish is high-impact: without --yes (non-interactive) it is refused
	// before any version is written.
	_, stderr, code := a.aw("", "definition", "publish", "--kind", "BLOCK", "--file", fileA, "--idempotency-key", "idem-publish-a", "blk-1")
	if code != exitUsage || !strings.Contains(stderr, "--yes") {
		t.Fatalf("publish without --yes: exit=%d stderr=%q, want the confirmation refusal", code, stderr)
	}
	if strings.Contains(a.mustOK("", "definition", "versions", "--kind", "BLOCK", "blk-1"), `"versionNumber"`) {
		t.Fatal("a refused publish still wrote a version")
	}

	publishedA := decodeResult(t, a.mustOK("", "definition", "publish", "--kind", "BLOCK", "--file", fileA, "--yes", "--idempotency-key", "idem-publish-a", "blk-1"))
	var vA versionDoc
	if err := json.Unmarshal(publishedA.Result, &vA); err != nil || vA.VersionNumber != 1 {
		t.Fatalf("published A = %s (err %v), want version 1", publishedA.Result, err)
	}

	// Same key + same content replays byte-identically, never a second version.
	replayA := decodeResult(t, a.mustOK("", "definition", "publish", "--kind", "BLOCK", "--file", fileA, "--yes", "--idempotency-key", "idem-publish-a", "blk-1"))
	if !replayA.Replayed || string(replayA.Result) != string(publishedA.Result) {
		t.Fatalf("replay = %+v, want replayed=true and an identical result", replayA)
	}

	// Identical content under a FRESH key dedupes by CompiledSnapshotHash
	// (AK-ARCH-005B) to the same version id.
	dedup := decodeResult(t, a.mustOK("", "definition", "publish", "--kind", "BLOCK", "--file", fileA, "--yes", "--idempotency-key", "idem-publish-a2", "blk-1"))
	var vDedup versionDoc
	if err := json.Unmarshal(dedup.Result, &vDedup); err != nil || vDedup.ID != vA.ID {
		t.Fatalf("dedup publish id = %q, want %q (%v)", vDedup.ID, vA.ID, err)
	}

	// The same key reused for genuinely different content is a clean conflict.
	if _, stderr, code := a.aw("", "definition", "publish", "--kind", "BLOCK", "--file", fileB, "--yes", "--idempotency-key", "idem-publish-a", "blk-1"); code != exitFailure {
		t.Fatalf("conflicting republish: exit=%d stderr=%q, want a failure", code, stderr)
	}

	publishedB := decodeResult(t, a.mustOK("", "definition", "publish", "--kind", "BLOCK", "--file", fileB, "--yes", "--idempotency-key", "idem-publish-b", "blk-1"))
	var vB versionDoc
	if err := json.Unmarshal(publishedB.Result, &vB); err != nil || vB.VersionNumber != 2 || vB.ID == vA.ID {
		t.Fatalf("published B = %s (err %v), want a distinct version 2", publishedB.Result, err)
	}

	var listed struct {
		Items []versionDoc `json:"items"`
	}
	if err := json.Unmarshal([]byte(a.mustOK("", "definition", "versions", "--kind", "BLOCK", "blk-1")), &listed); err != nil || len(listed.Items) != 2 {
		t.Fatalf("versions = %+v (err %v), want exactly two", listed, err)
	}

	var shown versionDoc
	if err := json.Unmarshal([]byte(a.mustOK("", "version", "show", vA.ID)), &shown); err != nil || shown.SourceHash != vA.SourceHash {
		t.Fatalf("version show = %+v (err %v), want A's own hash", shown, err)
	}

	var diff struct {
		Identical  bool `json:"identical"`
		SourceDiff []struct {
			Op string `json:"op"`
		} `json:"sourceDiff"`
	}
	if err := json.Unmarshal([]byte(a.mustOK("", "version", "diff", vA.ID, vB.ID)), &diff); err != nil {
		t.Fatal(err)
	}
	if diff.Identical || len(diff.SourceDiff) == 0 {
		t.Fatalf("diff of different content = %+v, want a real diff", diff)
	}

	// definition list (global scope) sees the definition.
	if out := a.mustOK("", "definition", "list", "--kind", "BLOCK"); !strings.Contains(out, "blk-1") {
		t.Errorf("definition list does not show blk-1:\n%s", out)
	}
}

func TestOneShot_WorkflowFlowResolvesRealAgentProfilePin(t *testing.T) {
	a := newAWInstall(t)
	profileFile := writeFixture(t, "profile.json", validAgentProfileDocument)

	a.mustOK(`{"definitionId":"profile-1","name":"test profile"}`, "definition", "create", "--kind", "AGENT_PROFILE", "--idempotency-key", "idem-create-profile")
	profile := decodeResult(t, a.mustOK("", "definition", "publish", "--kind", "AGENT_PROFILE", "--file", profileFile, "--yes", "--idempotency-key", "idem-publish-profile", "profile-1"))
	var profileVersion versionDoc
	if err := json.Unmarshal(profile.Result, &profileVersion); err != nil || profileVersion.ID == "" {
		t.Fatalf("profile publish = %s (%v)", profile.Result, err)
	}

	a.mustOK(`{"definitionId":"wf-1","name":"test workflow"}`, "definition", "create", "--kind", "WORKFLOW", "--idempotency-key", "idem-create-wf")
	wfFile := writeFixture(t, "workflow.json", workflowDocumentPinning("profile-1", profileVersion.ID))

	validateOut := a.mustOK("", "definition", "validate", "--kind", "WORKFLOW", "--file", wfFile, "wf-1")
	var validated struct {
		Valid   bool       `json:"valid"`
		Version versionDoc `json:"version"`
	}
	if err := json.Unmarshal([]byte(validateOut), &validated); err != nil {
		t.Fatal(err)
	}
	pins := validated.Version.Dependencies.Pins
	if !validated.Valid || len(pins) != 1 || pins[0].VersionID != profileVersion.ID {
		t.Fatalf("validated workflow = %+v, want valid with exactly the published profile version pinned; raw: %s", validated, validateOut)
	}

	published := decodeResult(t, a.mustOK("", "definition", "publish", "--kind", "WORKFLOW", "--file", wfFile, "--yes", "--idempotency-key", "idem-publish-wf", "wf-1"))
	var wfVersion versionDoc
	if err := json.Unmarshal(published.Result, &wfVersion); err != nil || wfVersion.VersionNumber != 1 || len(wfVersion.Dependencies.Pins) != 1 {
		t.Fatalf("published workflow = %s (%v)", published.Result, err)
	}
}

func TestOneShot_WorkflowUnresolvablePinIsACleanError(t *testing.T) {
	a := newAWInstall(t)
	a.mustOK(`{"definitionId":"wf-bad-pin","name":"test workflow"}`, "definition", "create", "--kind", "WORKFLOW", "--idempotency-key", "idem-create-wf")
	wfFile := writeFixture(t, "workflow.json", workflowDocumentPinning("profile-does-not-exist", "profile-does-not-exist-v1"))

	_, stderr, code := a.aw("", "definition", "validate", "--kind", "WORKFLOW", "--file", wfFile, "wf-bad-pin")
	if code != exitFailure {
		t.Fatalf("exit=%d stderr=%q, want a failure", code, stderr)
	}
	if strings.Contains(stderr, "goroutine") || strings.Contains(stderr, "panic") || len(stderr) > 4000 {
		t.Errorf("stderr must be a clean bounded message, got %d bytes: %q", len(stderr), stderr)
	}
}

// ---- adapter: probe -> register -> list -> show -------------------------

func TestOneShot_AdapterProbeRegisterListShow(t *testing.T) {
	a := newAWInstall(t)
	manifest := []string{"--supports-start", "--supports-cancel", "--canonical-event-kinds", "TEXT_DELTA"}
	// ProbeAdapterBuild hashes the executable file itself (the capability
	// manifest is caller-supplied, the file identity is measured), so the
	// path must name a real file; it never has to run.
	executable := writeFixture(t, "claude-cli", "fake provider executable bytes")
	probeArgs := append([]string{"adapter", "probe", "--json", "--idempotency-key", "idem-probe",
		"--provider-key", "claude", "--executable-path", executable,
		"--protocol-version", "1", "--os", runtime.GOOS, "--toolchain", "go", "--config-identity", "default"}, manifest...)

	probed := decodeResult(t, a.mustOK("", probeArgs...))
	tokenFile := writeFixture(t, "candidate-token.json", string(probed.Result))

	// An empty registry before register.
	if out := a.mustOK("", "adapter", "list", "--json"); strings.Contains(out, "claude") {
		t.Fatalf("probe must not register a build:\n%s", out)
	}

	// register is high-impact: --json without --yes is the typed refusal.
	registerArgs := append([]string{"adapter", "register", "--json", "--idempotency-key", "idem-register", "--file", tokenFile}, manifest...)
	stdout, _, code := a.aw("", registerArgs...)
	if code != exitUsage || !strings.Contains(stdout, "PRECONDITION_FAILED") || !strings.Contains(stdout, "confirmation") {
		t.Fatalf("register without --yes: exit=%d stdout=%q", code, stdout)
	}

	registered := decodeResult(t, a.mustOK("", append(registerArgs, "--yes")...))
	if registered.Replayed {
		t.Fatalf("first register must not be a replay: %+v", registered)
	}
	if listOut := a.mustOK("", "adapter", "list", "--json"); !strings.Contains(listOut, "claude") {
		t.Fatalf("adapter list does not show the registered build:\n%s", listOut)
	}
	if replay := decodeResult(t, a.mustOK("", append(registerArgs, "--yes")...)); !replay.Replayed {
		t.Fatalf("same key register must replay: %+v", replay)
	}
}

// ---- installation diagnosis and settings --------------------------------

func TestOneShot_HealthReadyDoctorAndSettings(t *testing.T) {
	a := newAWInstall(t)

	if out := a.mustOK("", "health", "ready", "--json"); !strings.Contains(out, `"ready"`) {
		t.Fatalf("health ready on a healthy install:\n%s", out)
	}

	// Doctor always exits 0 and carries its severity in the typed body.
	out := a.mustOK("", "doctor", "--json")
	var report struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil || report.Status == "" {
		t.Fatalf("doctor --json = %q (%v), want a typed report", out, err)
	}

	var shown struct {
		Version uint64 `json:"version"`
	}
	if err := json.Unmarshal([]byte(a.mustOK("", "settings", "show", "--json")), &shown); err != nil || shown.Version == 0 {
		t.Fatalf("settings show = %v (%v)", shown, err)
	}

	// health ready fails (and still writes its report) when the artifact root vanished.
	if err := os.RemoveAll(a.artifactRoot); err != nil {
		t.Fatal(err)
	}
	stdout, _, code := a.aw("", "health", "ready", "--json")
	if code != exitFailure || !strings.Contains(stdout, "not_ready") {
		t.Fatalf("health ready without an artifact root: exit=%d stdout=%q", code, stdout)
	}
	if n := strings.Count(stdout, "\"status\""); n != 1 {
		t.Errorf("health ready must emit exactly one document, got %d status fields:\n%s", n, stdout)
	}
}

// ---- the confirmation gate through the real entry point -----------------

func TestOneShot_HighImpactRunCancelIsGatedThenDispatched(t *testing.T) {
	a := newAWInstall(t)

	// Without --yes and non-interactive: refused before anything is opened —
	// no --db is given at all, and the failure is the gate, not a
	// missing-installation usage error.
	stdout, stderr, code := awRaw("", "run", "cancel", "--reason", "operator request", "run-that-does-not-exist")
	if code != exitUsage || stdout != "" || !strings.Contains(stderr, "--yes") {
		t.Fatalf("run cancel without --yes: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if strings.Contains(stderr, "--db is required") {
		t.Fatalf("the gate must fire before installation options are checked: %q", stderr)
	}

	// With --yes the gate passes and the leaf really dispatches: an unknown
	// run is then a failure from the application layer, not the gate.
	stdout, stderr, code = a.aw("", "run", "cancel", "--reason", "operator request", "--yes", "run-that-does-not-exist")
	if code == exitSuccess {
		t.Fatalf("cancelling a run that does not exist cannot succeed; stdout=%q", stdout)
	}
	if strings.Contains(stderr, "confirmation") || strings.Contains(stderr, "--yes") {
		t.Fatalf("with --yes the gate must not fire again: stdout=%q stderr=%q", stdout, stderr)
	}
	if stderr == "" {
		t.Fatal("the leaf failure must be reported on stderr")
	}
}

func TestOneShot_LeafFailureIsOneTypedDocumentInJSONMode(t *testing.T) {
	a := newAWInstall(t)
	stdout, _, code := a.aw("", "adapter", "show", "--json", "no-such-adapter-build")
	if code == exitSuccess {
		t.Fatalf("showing an unknown adapter build cannot succeed")
	}
	var doc struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	dec := json.NewDecoder(strings.NewReader(stdout))
	if err := dec.Decode(&doc); err != nil || doc.Error.Code == "" || doc.Error.Message == "" {
		t.Fatalf("stdout = %q (%v), want one typed error document", stdout, err)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err == nil {
		t.Errorf("more than one document on stdout: %q", stdout)
	}
}

// ---- evidence verify: the one CLI_LOCAL command, both modes -------------

func TestOneShot_EvidenceVerifyRoutesRuntimeModeWithoutBundleFlags(t *testing.T) {
	a := newAWInstall(t)
	// No --evidence-dir/--suite: this is the runtime-evidence leaf, so it
	// must reach the leaf (an unknown evidence id is a leaf failure), not the
	// legacy bundle verifier's own usage error.
	_, stderr, code := a.aw("", "evidence", "verify", "--project-id", "p-1", "ev-1", "wi-1")
	if code == exitSuccess {
		t.Fatal("verifying an unknown evidence id cannot succeed")
	}
	if strings.Contains(stderr, "--evidence-dir and --suite are required") {
		t.Fatalf("runtime mode was routed to the legacy bundle verifier: %q", stderr)
	}
}

// ---- the router table itself -------------------------------------------

func TestOneShot_EveryRoutedResourceIsReachableAndUsageErrorsDoNotTouchDisk(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range clicompose.Routes() {
		if seen[r.Path[0]] || len(r.Path) != 2 {
			continue
		}
		seen[r.Path[0]] = true
		dbPath := filepath.Join(t.TempDir(), "probe.db")
		// `aw --db x <resource>` (no action) resolves the resource — so it
		// is reachable — and answers with the action-listing usage error
		// without ever opening the database.
		_, stderr, code := awRaw("", "--db", dbPath, r.Path[0])
		if code != exitUsage || !strings.Contains(stderr, "expected 'aw "+r.Path[0]) {
			t.Errorf("aw %s: exit=%d stderr=%q, want the action-listing usage error", r.Path[0], code, stderr)
		}
		if _, err := os.Stat(dbPath); err == nil {
			t.Errorf("aw %s created a database for a usage error", r.Path[0])
		}
	}
	if len(seen) < 20 {
		t.Fatalf("only %d resources are routed — the routing table looks truncated", len(seen))
	}
}

// ---- every dependency shape builds from the real adapters ---------------

// TestOneShot_EveryDependencyShapeBuildsFromRealAdapters drives one command
// per distinct clicompose.Needs combination through the real oneShotFactory
// (real SQLite, artifact store, Git worktree provider, isolation checker,
// agent registry): each must get past dependency construction and reach its
// leaf, which then fails with a domain error for the unknown ids (exit 1) —
// never a usage error (exit 2: wrong arguments or a missing option) and never
// a factory failure.
func TestOneShot_EveryDependencyShapeBuildsFromRealAdapters(t *testing.T) {
	a := newAWInstall(t)
	cases := []struct {
		needs string
		args  []string
	}{
		{"UoW + ArtifactStore", []string{"evidence", "list", "--project-id", "p-1", "wi-1"}},
		{"UoW + ArtifactStore", []string{"message", "list", "--project-id", "p-1", "wi-1"}},
		{"UoW + Workspace (Git worktree provider)", []string{"workspace-set", "show", "--project-id", "p-1", "fam-1"}},
		{"UoW + Isolation + Agents", []string{"run", "diagnostics", "--project-id", "p-1", "run-1"}},
		{"UoW", []string{"work-item", "show", "--project-id", "p-1", "wi-1"}},
		{"UoW", []string{"release-set", "list", "--project-id", "p-1", "fam-1"}},
		{"UoW", []string{"projection", "status", "--project-id", "p-1", "--projection-name", "kanban"}},
		{"UoW", []string{"component", "list", "p-1"}},
		{"UoW", []string{"repository", "onboarding", "repo-1"}},
	}
	for _, tc := range cases {
		stdout, stderr, code := a.aw("", tc.args...)
		if code == exitUsage {
			t.Errorf("aw %v (%s): usage error %q — the arguments or the composed dependencies are wrong", tc.args, tc.needs, stderr)
			continue
		}
		if code != exitFailure && code != exitSuccess {
			t.Errorf("aw %v (%s): exit=%d stdout=%q stderr=%q", tc.args, tc.needs, code, stdout, stderr)
		}
	}

	// Doctor diagnoses rather than fails: an unusable --claude-executable
	// must still produce a report (exit 0).
	if _, stderr, code := a.aw("", "doctor", "--json", "--claude-executable", filepath.Join(t.TempDir(), "no-such-claude")); code != exitSuccess {
		t.Errorf("doctor with an unusable --claude-executable must still report, exit=%d stderr=%q", code, stderr)
	}
}
