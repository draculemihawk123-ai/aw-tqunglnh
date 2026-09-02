package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func definitionTestDB(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "agentkit-definition.db")
}

// writeDefinitionFile writes content to a fresh temp file and returns its
// path -- the same "write a fixture, return its path" convention
// adapter_test.go's own writeAdapterExecutable/writeTokenFile already
// established.
func writeDefinitionFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
	return path
}

// validBlockDocument is a minimal, fully valid BlockDocument (mirrors
// internal/domain/block/testdata/golden/block-example.json) with a
// tweakable timeoutSeconds so two distinct-content fixtures can be
// produced from one template (see blockDocumentWithTimeout).
func blockDocumentWithTimeout(timeoutSeconds int) string {
	return fmt.Sprintf(`{"compatibleNodeTypes":["AGENT"],"doneCondition":"result.outcome","executorRef":{"definitionId":"cmd-1","kind":"COMMAND","versionId":"cmd-1-v1"},"outcomes":["failure","success"],"policyRefs":[{"definitionId":"policy-1","kind":"POLICY","versionId":"policy-1-v1"}],"requiredCapabilities":["INTEGRATION_MULTI_REPOSITORY_WRITE"],"scopeSelector":{"access":"WRITE","pathScopes":["src/**"]},"timeoutSeconds":%d}`, timeoutSeconds)
}

// validAgentProfileDocument mirrors
// internal/domain/agentprofile/testdata/golden/agent-profile-example.json.
const validAgentProfileDocument = `{"budget":{"maxTokens":100000},"compatibility":{"os":["linux","windows"],"toolchain":["git"]},"contextPolicyRef":{"definitionId":"policy-context-1","kind":"POLICY","versionId":"policy-context-1-v1"},"model":"claude-opus-4","providerKey":"claude","requiredCapabilities":["INTEGRATION_MULTI_REPOSITORY_WRITE"],"toolRefs":["read_file","write_file"]}`

func workflowDocumentPinning(profileDefinitionID, profileVersionID string) string {
	return fmt.Sprintf(`{"schemaVersion":"1","nodes":[{"key":"start","type":"START","outcomes":["go"]},{"key":"agent","type":"AGENT","outcomes":["done"],"agent":{"profileRef":{"kind":"AGENT_PROFILE","definitionId":%q,"versionId":%q}}},{"key":"end","type":"END"}],"edges":[{"key":"e1","from":"start","outcome":"go","to":"agent"},{"key":"e2","from":"agent","outcome":"done","to":"end"}]}`, profileDefinitionID, profileVersionID)
}

func mustDecodeVersionFieldsView(t *testing.T, raw string) versionFieldsView {
	t.Helper()
	var v versionFieldsView
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		t.Fatalf("decode versionFieldsView: %v\nraw: %s", err, raw)
	}
	return v
}

// TestDefinitionCLI_BlockFullFlow_NeverTouchesSQLiteDirectly is V2-11's
// own "Hoàn thành khi" bar proven directly: create -> validate -> publish
// -> list -> show -> diff for one non-Workflow kind (BLOCK), driven
// entirely through the CLI's own run() -- the test itself only ever
// opens the sqlite file the CLI manages via --db, never a *sql.DB of its
// own.
func TestDefinitionCLI_BlockFullFlow_NeverTouchesSQLiteDirectly(t *testing.T) {
	dbPath := definitionTestDB(t)
	fileA := writeDefinitionFile(t, "block-a.json", blockDocumentWithTimeout(60))
	fileB := writeDefinitionFile(t, "block-b.json", blockDocumentWithTimeout(120))

	// create
	createOut, stderr, code := runCLI(t, "definition", "create", "--db", dbPath,
		"--kind", "BLOCK", "--definition-id", "blk-1", "--name", "test block",
		"--idempotency-key", "idem-create-blk-1")
	if code != exitSuccess {
		t.Fatalf("create: code=%d stderr=%q", code, stderr)
	}
	if !strings.Contains(createOut, `"blk-1"`) {
		t.Fatalf("create output should mention the definition id, got %q", createOut)
	}

	// validate (dry run: must not create any version yet)
	validateOut, stderr, code := runCLI(t, "definition", "validate", "--db", dbPath,
		"--kind", "BLOCK", "--definition-id", "blk-1", "--file", fileA)
	if code != exitSuccess {
		t.Fatalf("validate: code=%d stderr=%q", code, stderr)
	}
	validated := mustDecodeVersionFieldsView(t, validateOut)
	if validated.DefinitionID != "blk-1" || validated.Kind != "BLOCK" {
		t.Fatalf("validate result = %+v, want DefinitionID=blk-1 Kind=BLOCK", validated)
	}

	listAfterValidate, stderr, code := runCLI(t, "definition", "list", "--db", dbPath, "--kind", "BLOCK", "--definition-id", "blk-1")
	if code != exitSuccess {
		t.Fatalf("list after validate: code=%d stderr=%q", code, stderr)
	}
	if strings.TrimSpace(listAfterValidate) != "[]" {
		t.Fatalf("list after a dry-run validate = %q, want empty array (validate must never persist)", listAfterValidate)
	}

	// publish fileA
	publishOutA, stderr, code := runCLI(t, "definition", "publish", "--db", dbPath,
		"--kind", "BLOCK", "--definition-id", "blk-1", "--file", fileA,
		"--idempotency-key", "idem-publish-blk-1-a", "--actor", "operator-1")
	if code != exitSuccess {
		t.Fatalf("publish A: code=%d stderr=%q", code, stderr)
	}
	publishedA := mustDecodeVersionFieldsView(t, publishOutA)
	if publishedA.VersionNumber != 1 {
		t.Fatalf("publishedA.VersionNumber = %d, want 1", publishedA.VersionNumber)
	}
	if publishedA.PublishedBy != "operator-1" {
		t.Fatalf("publishedA.PublishedBy = %q, want operator-1", publishedA.PublishedBy)
	}

	// idempotent replay: same idempotency key + same file must replay
	// byte-identical output, never create a second version.
	replayOutA, stderr, code := runCLI(t, "definition", "publish", "--db", dbPath,
		"--kind", "BLOCK", "--definition-id", "blk-1", "--file", fileA,
		"--idempotency-key", "idem-publish-blk-1-a", "--actor", "operator-1")
	if code != exitSuccess {
		t.Fatalf("replay publish A: code=%d stderr=%q", code, stderr)
	}
	if strings.TrimSpace(replayOutA) != strings.TrimSpace(publishOutA) {
		t.Fatalf("replayed publish output differs from the original:\noriginal: %s\nreplay:   %s", publishOutA, replayOutA)
	}

	// publish fileB under a fresh idempotency key -> a genuinely new version.
	publishOutB, stderr, code := runCLI(t, "definition", "publish", "--db", dbPath,
		"--kind", "BLOCK", "--definition-id", "blk-1", "--file", fileB,
		"--idempotency-key", "idem-publish-blk-1-b", "--actor", "operator-2")
	if code != exitSuccess {
		t.Fatalf("publish B: code=%d stderr=%q", code, stderr)
	}
	publishedB := mustDecodeVersionFieldsView(t, publishOutB)
	if publishedB.VersionNumber != 2 {
		t.Fatalf("publishedB.VersionNumber = %d, want 2", publishedB.VersionNumber)
	}
	if publishedB.ID == publishedA.ID {
		t.Fatal("genuinely different content must publish as a distinct version id")
	}

	// list: exactly two versions now.
	listOut, stderr, code := runCLI(t, "definition", "list", "--db", dbPath, "--kind", "BLOCK", "--definition-id", "blk-1")
	if code != exitSuccess {
		t.Fatalf("list: code=%d stderr=%q", code, stderr)
	}
	var listed []versionFieldsView
	if err := json.Unmarshal([]byte(listOut), &listed); err != nil {
		t.Fatalf("decode list output: %v\nraw: %s", err, listOut)
	}
	if len(listed) != 2 {
		t.Fatalf("len(listed) = %d, want 2", len(listed))
	}

	// show both.
	showOutA, stderr, code := runCLI(t, "definition", "show", "--db", dbPath, "--version-id", publishedA.ID)
	if code != exitSuccess {
		t.Fatalf("show A: code=%d stderr=%q", code, stderr)
	}
	shownA := mustDecodeVersionFieldsView(t, showOutA)
	if shownA.ID != publishedA.ID || shownA.SourceHash != publishedA.SourceHash {
		t.Fatalf("show A = %+v, want it to match published A %+v", shownA, publishedA)
	}

	// diff: genuinely different content must show a real diff and
	// Identical=false.
	diffOut, stderr, code := runCLI(t, "definition", "diff", "--db", dbPath,
		"--version-id-a", publishedA.ID, "--version-id-b", publishedB.ID)
	if code != exitSuccess {
		t.Fatalf("diff: code=%d stderr=%q", code, stderr)
	}
	var diff versionDiffView
	if err := json.Unmarshal([]byte(diffOut), &diff); err != nil {
		t.Fatalf("decode diff output: %v\nraw: %s", err, diffOut)
	}
	if diff.Identical {
		t.Fatal("diff.Identical should be false for genuinely different content")
	}
	if len(diff.SourceDiff) == 0 {
		t.Fatal("diff.SourceDiff should be non-empty for genuinely different content")
	}
	foundAdd, foundRemove := false, false
	for _, line := range diff.SourceDiff {
		if line.Op == "add" {
			foundAdd = true
		}
		if line.Op == "remove" {
			foundRemove = true
		}
	}
	if !foundAdd || !foundRemove {
		t.Fatalf("diff.SourceDiff should contain both an add and a remove line for a changed field, got %+v", diff.SourceDiff)
	}

	// diff a version against itself: Identical=true, no add/remove lines.
	selfDiffOut, stderr, code := runCLI(t, "definition", "diff", "--db", dbPath,
		"--version-id-a", publishedA.ID, "--version-id-b", publishedA.ID)
	if code != exitSuccess {
		t.Fatalf("self diff: code=%d stderr=%q", code, stderr)
	}
	var selfDiff versionDiffView
	if err := json.Unmarshal([]byte(selfDiffOut), &selfDiff); err != nil {
		t.Fatalf("decode self diff output: %v", err)
	}
	if !selfDiff.Identical {
		t.Fatal("diffing a version against itself should report Identical=true")
	}
	for _, line := range selfDiff.SourceDiff {
		if line.Op != "equal" {
			t.Fatalf("self diff should contain only equal lines, got %+v", line)
		}
	}
}

// TestDefinitionCLI_PublishDedupesIdenticalContentUnderFreshIdempotencyKey
// proves AK-ARCH-005B's dedup-by-CompiledSnapshotHash rule through the
// CLI: republishing byte-identical content under a genuinely different
// --idempotency-key must resolve to the same version id, and must never
// create a second version row.
func TestDefinitionCLI_PublishDedupesIdenticalContentUnderFreshIdempotencyKey(t *testing.T) {
	dbPath := definitionTestDB(t)
	file := writeDefinitionFile(t, "block.json", blockDocumentWithTimeout(60))

	if _, stderr, code := runCLI(t, "definition", "create", "--db", dbPath,
		"--kind", "BLOCK", "--definition-id", "blk-dedup", "--name", "dedup target",
		"--idempotency-key", "idem-create"); code != exitSuccess {
		t.Fatalf("create: code=%d stderr=%q", code, stderr)
	}

	firstOut, stderr, code := runCLI(t, "definition", "publish", "--db", dbPath,
		"--kind", "BLOCK", "--definition-id", "blk-dedup", "--file", file,
		"--idempotency-key", "idem-publish-1")
	if code != exitSuccess {
		t.Fatalf("first publish: code=%d stderr=%q", code, stderr)
	}
	first := mustDecodeVersionFieldsView(t, firstOut)

	secondOut, stderr, code := runCLI(t, "definition", "publish", "--db", dbPath,
		"--kind", "BLOCK", "--definition-id", "blk-dedup", "--file", file,
		"--idempotency-key", "idem-publish-2")
	if code != exitSuccess {
		t.Fatalf("second publish: code=%d stderr=%q", code, stderr)
	}
	second := mustDecodeVersionFieldsView(t, secondOut)

	if second.ID != first.ID {
		t.Fatalf("second publish (fresh idempotency key, identical content) got id %q, want the same id %q", second.ID, first.ID)
	}

	listOut, stderr, code := runCLI(t, "definition", "list", "--db", dbPath, "--kind", "BLOCK", "--definition-id", "blk-dedup")
	if code != exitSuccess {
		t.Fatalf("list: code=%d stderr=%q", code, stderr)
	}
	var listed []versionFieldsView
	if err := json.Unmarshal([]byte(listOut), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("len(listed) = %d, want 1 (identical content must dedupe, never duplicate)", len(listed))
	}
}

// TestDefinitionCLI_PublishConflictingIdempotencyKey_IsCleanError proves
// the same --idempotency-key reused for a genuinely different --file is
// rejected as a clean conflict, not replayed and not a raw sentinel dump.
func TestDefinitionCLI_PublishConflictingIdempotencyKey_IsCleanError(t *testing.T) {
	dbPath := definitionTestDB(t)
	fileA := writeDefinitionFile(t, "block-a.json", blockDocumentWithTimeout(60))
	fileB := writeDefinitionFile(t, "block-b.json", blockDocumentWithTimeout(120))

	if _, stderr, code := runCLI(t, "definition", "create", "--db", dbPath,
		"--kind", "BLOCK", "--definition-id", "blk-conflict", "--name", "conflict target",
		"--idempotency-key", "idem-create"); code != exitSuccess {
		t.Fatalf("create: code=%d stderr=%q", code, stderr)
	}

	if _, stderr, code := runCLI(t, "definition", "publish", "--db", dbPath,
		"--kind", "BLOCK", "--definition-id", "blk-conflict", "--file", fileA,
		"--idempotency-key", "idem-shared"); code != exitSuccess {
		t.Fatalf("first publish: code=%d stderr=%q", code, stderr)
	}

	_, stderr, code := runCLI(t, "definition", "publish", "--db", dbPath,
		"--kind", "BLOCK", "--definition-id", "blk-conflict", "--file", fileB,
		"--idempotency-key", "idem-shared")
	if code != exitFailure {
		t.Fatalf("conflicting republish: code=%d, want exitFailure; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "idempotency-key was already used") {
		t.Fatalf("stderr should clearly explain the conflict, got %q", stderr)
	}
	if strings.Contains(stderr, "goroutine") || strings.Contains(stderr, "panic") {
		t.Fatalf("stderr should never contain a raw Go stack trace, got %q", stderr)
	}
}

// TestDefinitionCLI_WorkflowFullFlow_ResolvesRealAgentProfile is the
// Workflow-kind counterpart of the Block full-flow test: it publishes a
// real AgentProfile through the CLI first, then authors a Workflow
// document pinning that profile's exact published version, proving
// workflowcompiler.CompileAndResolve genuinely resolves the pin against
// real sqlite-backed registry state reached only through run() --
// mirroring internal/adapters/sqlite/workflowcompiler_integration_test.go's
// own end-to-end seeding, but driven entirely by the CLI.
func TestDefinitionCLI_WorkflowFullFlow_ResolvesRealAgentProfile(t *testing.T) {
	dbPath := definitionTestDB(t)
	profileFile := writeDefinitionFile(t, "profile.json", validAgentProfileDocument)

	if _, stderr, code := runCLI(t, "definition", "create", "--db", dbPath,
		"--kind", "AGENT_PROFILE", "--definition-id", "profile-1", "--name", "test profile",
		"--idempotency-key", "idem-create-profile"); code != exitSuccess {
		t.Fatalf("create profile: code=%d stderr=%q", code, stderr)
	}
	profileOut, stderr, code := runCLI(t, "definition", "publish", "--db", dbPath,
		"--kind", "AGENT_PROFILE", "--definition-id", "profile-1", "--file", profileFile,
		"--idempotency-key", "idem-publish-profile", "--actor", "operator-1")
	if code != exitSuccess {
		t.Fatalf("publish profile: code=%d stderr=%q", code, stderr)
	}
	profile := mustDecodeVersionFieldsView(t, profileOut)

	if _, stderr, code := runCLI(t, "definition", "create", "--db", dbPath,
		"--kind", "WORKFLOW", "--definition-id", "wf-1", "--name", "test workflow",
		"--idempotency-key", "idem-create-wf"); code != exitSuccess {
		t.Fatalf("create workflow: code=%d stderr=%q", code, stderr)
	}

	wfFile := writeDefinitionFile(t, "workflow.json", workflowDocumentPinning("profile-1", profile.ID))

	validateOut, stderr, code := runCLI(t, "definition", "validate", "--db", dbPath,
		"--kind", "WORKFLOW", "--definition-id", "wf-1", "--file", wfFile)
	if code != exitSuccess {
		t.Fatalf("validate workflow: code=%d stderr=%q", code, stderr)
	}
	validated := mustDecodeVersionFieldsView(t, validateOut)
	if len(validated.Dependencies.Pins) != 1 {
		t.Fatalf("validated workflow dependencies = %+v, want exactly 1 resolved pin", validated.Dependencies.Pins)
	}
	pin := validated.Dependencies.Pins[0]
	if pin.DefinitionID != "profile-1" || pin.VersionID != profile.ID {
		t.Fatalf("resolved pin = %+v, want profile-1/%s", pin, profile.ID)
	}

	publishOut, stderr, code := runCLI(t, "definition", "publish", "--db", dbPath,
		"--kind", "WORKFLOW", "--definition-id", "wf-1", "--name", "test workflow", "--file", wfFile,
		"--idempotency-key", "idem-publish-wf", "--actor", "operator-1")
	if code != exitSuccess {
		t.Fatalf("publish workflow: code=%d stderr=%q", code, stderr)
	}
	published := mustDecodeVersionFieldsView(t, publishOut)
	if published.VersionNumber != 1 {
		t.Fatalf("published workflow VersionNumber = %d, want 1", published.VersionNumber)
	}
	if len(published.Dependencies.Pins) != 1 {
		t.Fatalf("published workflow dependencies = %+v, want exactly 1 resolved pin", published.Dependencies.Pins)
	}

	listOut, stderr, code := runCLI(t, "definition", "list", "--db", dbPath, "--kind", "WORKFLOW", "--definition-id", "wf-1")
	if code != exitSuccess {
		t.Fatalf("list workflow: code=%d stderr=%q", code, stderr)
	}
	var listed []versionFieldsView
	if err := json.Unmarshal([]byte(listOut), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("len(listed) = %d, want 1", len(listed))
	}

	showOut, stderr, code := runCLI(t, "definition", "show", "--db", dbPath, "--version-id", published.ID)
	if code != exitSuccess {
		t.Fatalf("show workflow: code=%d stderr=%q", code, stderr)
	}
	shown := mustDecodeVersionFieldsView(t, showOut)
	if shown.ID != published.ID {
		t.Fatalf("shown.ID = %q, want %q", shown.ID, published.ID)
	}

	// diff also resolves a Workflow-kind version id (via loadAnyVersion's
	// own fallback to store.LoadWorkflowVersion, since
	// definitions.LoadVersion alone only ever resolves the eight shared
	// kinds -- see loadAnyVersion's own doc comment).
	diffOut, stderr, code := runCLI(t, "definition", "diff", "--db", dbPath,
		"--version-id-a", published.ID, "--version-id-b", published.ID)
	if code != exitSuccess {
		t.Fatalf("diff workflow version against itself: code=%d stderr=%q", code, stderr)
	}
	var diff versionDiffView
	if err := json.Unmarshal([]byte(diffOut), &diff); err != nil {
		t.Fatalf("decode diff output: %v\nraw: %s", err, diffOut)
	}
	if !diff.Identical {
		t.Fatal("diffing a workflow version against itself should report Identical=true")
	}
}

// TestDefinitionCLI_WorkflowPublish_UnresolvablePinIsCleanError proves an
// unpublished pin target fails with a clean, informative error rather
// than a raw internal dump -- the safe-diagnostics bar applied to
// workflowcompiler.ResolutionError specifically.
func TestDefinitionCLI_WorkflowPublish_UnresolvablePinIsCleanError(t *testing.T) {
	dbPath := definitionTestDB(t)

	if _, stderr, code := runCLI(t, "definition", "create", "--db", dbPath,
		"--kind", "WORKFLOW", "--definition-id", "wf-bad-pin", "--name", "test workflow",
		"--idempotency-key", "idem-create-wf"); code != exitSuccess {
		t.Fatalf("create workflow: code=%d stderr=%q", code, stderr)
	}

	wfFile := writeDefinitionFile(t, "workflow.json", workflowDocumentPinning("profile-does-not-exist", "profile-does-not-exist-v1"))

	_, stderr, code := runCLI(t, "definition", "validate", "--db", dbPath,
		"--kind", "WORKFLOW", "--definition-id", "wf-bad-pin", "--file", wfFile)
	if code != exitFailure {
		t.Fatalf("validate with an unresolvable pin: code=%d, want exitFailure; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "does not resolve to any published version") {
		t.Fatalf("stderr should explain the unresolved pin, got %q", stderr)
	}
	if strings.Contains(stderr, "goroutine") || strings.Contains(stderr, "panic") {
		t.Fatalf("stderr should never contain a raw Go stack trace, got %q", stderr)
	}
}

// TestDefinitionCLI_SafeDiagnostics_MalformedInput feeds validate/publish
// a series of malformed files (bad JSON, unknown fields, a document that
// does not match the claimed --kind) and confirms every failure is a
// clean, bounded, non-panicking message -- V2-11's own "safe
// diagnostics" bar.
func TestDefinitionCLI_SafeDiagnostics_MalformedInput(t *testing.T) {
	dbPath := definitionTestDB(t)
	if _, stderr, code := runCLI(t, "definition", "create", "--db", dbPath,
		"--kind", "BLOCK", "--definition-id", "blk-malformed", "--name", "malformed target",
		"--idempotency-key", "idem-create"); code != exitSuccess {
		t.Fatalf("create: code=%d stderr=%q", code, stderr)
	}

	cases := []struct {
		name    string
		kind    string
		content string
	}{
		{"bad JSON syntax", "BLOCK", `{"compatibleNodeTypes": [`},
		{"unknown field", "BLOCK", `{"compatibleNodeTypes":["AGENT"],"doneCondition":"result.outcome","executorRef":{"definitionId":"cmd-1","kind":"COMMAND","versionId":"cmd-1-v1"},"outcomes":["success"],"scopeSelector":{"access":"WRITE"},"timeoutSeconds":60,"thisFieldDoesNotExist":true}`},
		{"wrong kind (agent profile document validated as BLOCK)", "BLOCK", validAgentProfileDocument},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file := writeDefinitionFile(t, "malformed.json", tc.content)
			stdout, stderr, code := runCLI(t, "definition", "validate", "--db", dbPath,
				"--kind", tc.kind, "--definition-id", "blk-malformed", "--file", file)
			if code != exitFailure {
				t.Fatalf("code=%d, want exitFailure; stdout=%q stderr=%q", code, stdout, stderr)
			}
			if stdout != "" {
				t.Fatalf("stdout should be empty on failure, got %q", stdout)
			}
			if strings.Contains(stderr, "goroutine") || strings.Contains(stderr, "panic") {
				t.Fatalf("stderr should never contain a raw Go stack trace, got %q", stderr)
			}
			if len(stderr) > 4000 {
				t.Fatalf("stderr should be a bounded, human-sized message, got %d bytes", len(stderr))
			}
		})
	}
}

// TestDefinitionCLI_UnknownKindIsUsageError proves an unrecognized --kind
// value is rejected before ever touching the registry.
func TestDefinitionCLI_UnknownKindIsUsageError(t *testing.T) {
	dbPath := definitionTestDB(t)
	file := writeDefinitionFile(t, "block.json", blockDocumentWithTimeout(60))
	_, _, code := runCLI(t, "definition", "validate", "--db", dbPath,
		"--kind", "NOT_A_REAL_KIND", "--definition-id", "x", "--file", file)
	if code != exitUsage {
		t.Fatalf("validate with unknown kind: code=%d, want exitUsage", code)
	}
}

// TestDefinitionCLI_ShowNotFoundIsCleanError proves an unknown version id
// maps to a clear message rather than a raw
// ports.ErrDefinitionVersionNotFound dump.
func TestDefinitionCLI_ShowNotFoundIsCleanError(t *testing.T) {
	dbPath := definitionTestDB(t)
	stdout, stderr, code := runCLI(t, "definition", "show", "--db", dbPath, "--version-id", "does-not-exist")
	if code != exitFailure {
		t.Fatalf("show unknown id: code=%d, want exitFailure", code)
	}
	if stdout != "" {
		t.Fatalf("stdout should be empty, got %q", stdout)
	}
	if !strings.Contains(stderr, `"does-not-exist" not found`) {
		t.Fatalf("stderr should give a clean not-found message naming the id, got %q", stderr)
	}
}

// TestDefinitionCLI_DiffNotFoundIsCleanError mirrors the show case for
// diff's own two LoadVersion calls.
func TestDefinitionCLI_DiffNotFoundIsCleanError(t *testing.T) {
	dbPath := definitionTestDB(t)
	_, stderr, code := runCLI(t, "definition", "diff", "--db", dbPath,
		"--version-id-a", "does-not-exist-a", "--version-id-b", "does-not-exist-b")
	if code != exitFailure {
		t.Fatalf("diff unknown ids: code=%d, want exitFailure", code)
	}
	if !strings.Contains(stderr, `"does-not-exist-a" not found`) {
		t.Fatalf("stderr should name the first missing id, got %q", stderr)
	}
}

// TestDefinitionCLI_MissingRequiredFlags proves every required flag
// across all six subcommands is enforced as a usage error, not a runtime
// crash -- mirroring adapter_test.go's own TestAdapter_MissingRequiredFlags.
func TestDefinitionCLI_MissingRequiredFlags(t *testing.T) {
	dbPath := definitionTestDB(t)
	file := writeDefinitionFile(t, "block.json", blockDocumentWithTimeout(60))

	cases := [][]string{
		{"definition"},
		{"definition", "bogus-verb"},
		{"definition", "create"},
		{"definition", "create", "--db", dbPath},
		{"definition", "create", "--db", dbPath, "--kind", "BLOCK"},
		{"definition", "create", "--db", dbPath, "--kind", "BLOCK", "--definition-id", "x"},
		{"definition", "create", "--db", dbPath, "--kind", "BLOCK", "--definition-id", "x", "--name", "n"},
		{"definition", "validate"},
		{"definition", "validate", "--db", dbPath},
		{"definition", "validate", "--db", dbPath, "--kind", "BLOCK"},
		{"definition", "validate", "--db", dbPath, "--kind", "BLOCK", "--definition-id", "x"},
		{"definition", "publish"},
		{"definition", "publish", "--db", dbPath, "--kind", "BLOCK", "--definition-id", "x", "--file", file},
		{"definition", "publish", "--db", dbPath, "--kind", "WORKFLOW", "--definition-id", "wf-x", "--file", file, "--idempotency-key", "idem-1"},
		{"definition", "list"},
		{"definition", "list", "--db", dbPath},
		{"definition", "show"},
		{"definition", "show", "--db", dbPath},
		{"definition", "diff"},
		{"definition", "diff", "--db", dbPath},
		{"definition", "diff", "--db", dbPath, "--version-id-a", "a"},
	}
	for _, args := range cases {
		_, _, code := runCLI(t, args...)
		if code != exitUsage {
			t.Errorf("run(%v): code=%d, want exitUsage", args, code)
		}
	}
}
