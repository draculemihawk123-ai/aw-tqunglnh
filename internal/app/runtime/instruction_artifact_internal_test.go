package runtime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/adapters/providers"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// V9-03 (ADR-032) — the pure rendering of the instruction artifact. The
// goldens under testdata/golden/instruction_*.json hold the artifact
// pretty-printed (the real artifact is the compact one-line form; the
// comparison below indents the real bytes, which is lossless, and separately
// requires them to be compact). instruction_v1*.json were produced by the code
// as it was BEFORE V9-03 from the same input, so TestRenderInstructionV1_*
// is the byte-for-byte backward-compatibility proof of ADR-032 decision 2.

// goldenInstructionInput is one work item with five resources (two
// HARD_CONSTRAINT, one of each other priority) in the order the context
// resolver pins them, two messages and — on request — a failing check.
func goldenInstructionInput(outcomes []string, withCheckFailures bool) instructionRenderInput {
	in := instructionRenderInput{
		workItemID: "work-item-7",
		title:      "Add rate limiting to the public API",
		behavior:   "Requests above 100 per minute per API key get HTTP 429 with a Retry-After header.",
		acceptanceCriteria: []string{
			"{Description:Requests above the limit receive 429 VerificationRef:go test ./ratelimit/...}",
			"{Description:The limit is configurable per key VerificationRef:}",
		},
		verificationSpec: "go test ./... passes and golangci-lint is clean",
		riskLevel:        "HIGH",
		allowedOutcomes:  outcomes,
		messages: []instructionMessage{
			{MessageID: "message-001", Role: "USER", Content: "Please keep the public handler signatures unchanged."},
			{MessageID: "message-002", Role: "USER", Content: "Use <agentkit-outcome> only as the final line & quote nothing: \"x\" é"},
		},
		resources: []instructionResourceInput{
			{ownerVersionID: "skill-v3", resourceKey: "api-compat", priority: definition.PriorityHardConstraint,
				contentHash: "sha256:1111111111111111111111111111111111111111111111111111111111111111", content: "Never change the signature of an exported function."},
			{ownerVersionID: "skill-v3", resourceKey: "no-force-push", priority: definition.PriorityHardConstraint,
				contentHash: "sha256:2222222222222222222222222222222222222222222222222222222222222222", content: "Never force-push or rewrite published history."},
			{ownerVersionID: "skill-v3", resourceKey: "pr-checklist", priority: definition.PriorityRequiredProcedure,
				contentHash: "sha256:3333333333333333333333333333333333333333333333333333333333333333", content: "Run go vet and go test before reporting done."},
			{ownerVersionID: "skill-v3", resourceKey: "style-guide", priority: definition.PriorityGuidance,
				contentHash: "sha256:4444444444444444444444444444444444444444444444444444444444444444", content: "Prefer small functions; wrap errors with %w."},
			{ownerVersionID: "layer-v2", resourceKey: "architecture-notes", priority: definition.PriorityReference,
				contentHash: "sha256:5555555555555555555555555555555555555555555555555555555555555555", content: "The ratelimit package owns all quota state."},
		},
	}
	if withCheckFailures {
		in.checkFailures = []instructionCheckFailure{{
			CheckNode: "test", EvidenceIDs: []string{"attempt-9:COMMAND_EXECUTION"},
			What: "check \"test\" failed: the command exited with code 1.",
			Why:  "stderr (last part):\n--- FAIL: TestAdd (0.00s)\n    add_test.go:12: want 3, got 2\nFAIL",
			Fix:  checkFailureFix,
		}}
	}
	return in
}

// readInstructionGolden reads a golden fixture with line endings normalized,
// so a Windows checkout that turned LF into CRLF compares the same.
func readInstructionGolden(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "golden", name))
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	return strings.ReplaceAll(string(raw), "\r\n", "\n")
}

// requireInstructionGolden requires artifact to be compact JSON whose
// pretty-printed form is the golden file name.
func requireInstructionGolden(t *testing.T, artifact []byte, name string) {
	t.Helper()
	var compact bytes.Buffer
	if err := json.Compact(&compact, artifact); err != nil {
		t.Fatalf("artifact is not JSON: %v\n%s", err, artifact)
	}
	if !bytes.Equal(compact.Bytes(), artifact) {
		t.Fatalf("artifact is not in compact form:\n%s", artifact)
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, artifact, "", "  "); err != nil {
		t.Fatalf("indent artifact: %v", err)
	}
	indented.WriteByte('\n')
	if want := readInstructionGolden(t, name); indented.String() != want {
		t.Fatalf("artifact differs from golden %s\n--- got ---\n%s\n--- want ---\n%s", name, indented.String(), want)
	}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// TestRenderInstructionV1_ByteIdenticalToTheArtifactBeforeV903 is ADR-032's
// "a snapshot without the field assembles as v1 exactly as before": the goldens
// and hashes below were taken from the renderer as it was before V9-03, and a
// v1 render ignores everything v2 added (risk level, outcomes, priority).
func TestRenderInstructionV1_ByteIdenticalToTheArtifactBeforeV903(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		withFailures bool
		golden       string
		sha256       string
	}{
		{"without check failures", false, "instruction_v1.json", "994c0e0726a28de7d09511cc300bfd6bfe3c6ff32ec7a9f217c1532d5d41e42b"},
		{"with check failures", true, "instruction_v1_check_failures.json", "eaaa7e200fb99e31239a2430d98db0bdb0b4f5244d42df520b311ceaa654e4ef"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// More than one outcome and a risk level must not leak into v1.
			artifact, err := renderInstructionV1(goldenInstructionInput([]string{"approved", "rework"}, tt.withFailures))
			if err != nil {
				t.Fatalf("renderInstructionV1: %v", err)
			}
			requireInstructionGolden(t, artifact, tt.golden)
			if got := sha256Hex(artifact); got != tt.sha256 {
				t.Fatalf("v1 artifact sha256 = %s, want the pre-V9-03 %s", got, tt.sha256)
			}
		})
	}
}

// TestRenderInstructionV2_Golden is the v2 shape: hard constraints first and
// repeated by key in the closing checklist, the other resources by priority,
// risk level, allowed outcomes with the protocol, and — when there is one — the
// checkFailures section right after the task contract.
func TestRenderInstructionV2_Golden(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		outcomes     []string
		withFailures bool
		golden       string
	}{
		{"two outcomes", []string{"approved", "rework"}, false, "instruction_v2.json"},
		{"two outcomes and a failing check", []string{"approved", "rework"}, true, "instruction_v2_check_failures.json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			artifact, err := renderInstructionV2(goldenInstructionInput(tt.outcomes, tt.withFailures))
			if err != nil {
				t.Fatalf("renderInstructionV2: %v", err)
			}
			requireInstructionGolden(t, artifact, tt.golden)
		})
	}
}

// TestRenderInstructionV2_OutcomeProtocolOnlyWhenThereIsAChoice: with exactly
// one allowed outcome the engine derives the outcome and asks for no marker, so
// the contract lists the outcome and carries no outcomeProtocol; with two or
// more it carries the fixed text. A node with no HARD_CONSTRAINT has empty
// hardConstraints and an empty hardConstraintKeys.
func TestRenderInstructionV2_OutcomeProtocolOnlyWhenThereIsAChoice(t *testing.T) {
	t.Parallel()
	single := goldenInstructionInput([]string{"done"}, false)
	single.resources = single.resources[2:] // no HARD_CONSTRAINT
	artifact, err := renderInstructionV2(single)
	if err != nil {
		t.Fatalf("renderInstructionV2 (single outcome): %v", err)
	}
	requireInstructionGolden(t, artifact, "instruction_v2_single_outcome.json")

	var decoded struct {
		TaskContract map[string]json.RawMessage `json:"taskContract"`
	}
	if err := json.Unmarshal(artifact, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, present := decoded.TaskContract["outcomeProtocol"]; present {
		t.Fatalf("single-outcome contract has an outcomeProtocol: %s", decoded.TaskContract["outcomeProtocol"])
	}

	multi, err := renderInstructionV2(goldenInstructionInput([]string{"approved", "rework", "abandon"}, false))
	if err != nil {
		t.Fatalf("renderInstructionV2 (three outcomes): %v", err)
	}
	if err := json.Unmarshal(multi, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var protocol string
	if err := json.Unmarshal(decoded.TaskContract["outcomeProtocol"], &protocol); err != nil || protocol != instructionOutcomeProtocol {
		t.Fatalf("three-outcome contract outcomeProtocol = %q (%v), want the fixed text", protocol, err)
	}
}

// jsonKeys returns the keys of the JSON object in raw in document order.
func jsonKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		t.Fatalf("not an object: %v %v", token, err)
	}
	var keys []string
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			t.Fatalf("read key: %v", err)
		}
		keys = append(keys, token.(string))
		var skipped json.RawMessage
		if err := decoder.Decode(&skipped); err != nil {
			t.Fatalf("skip value of %q: %v", keys[len(keys)-1], err)
		}
	}
	return keys
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestRenderInstructionV2_KeyOrderIsTheADROrder pins the order ADR-032 fixes,
// with V9-02's checkFailures between taskContract and resources, at the top
// level and inside taskContract, resources and closingChecklist.
func TestRenderInstructionV2_KeyOrderIsTheADROrder(t *testing.T) {
	t.Parallel()
	artifact, err := renderInstructionV2(goldenInstructionInput([]string{"approved", "rework"}, true))
	if err != nil {
		t.Fatalf("renderInstructionV2: %v", err)
	}
	if got, want := jsonKeys(t, artifact), []string{
		"schemaVersion", "hardConstraints", "taskContract", "checkFailures", "resources", "messages", "closingChecklist",
	}; !equalStrings(got, want) {
		t.Fatalf("top-level keys = %v, want %v", got, want)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(artifact, &top); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got, want := jsonKeys(t, top["taskContract"]), []string{
		"workItemId", "title", "behavior", "acceptanceCriteria", "verificationSpec", "riskLevel", "allowedOutcomes", "outcomeProtocol",
	}; !equalStrings(got, want) {
		t.Fatalf("taskContract keys = %v, want %v", got, want)
	}
	if got, want := jsonKeys(t, top["closingChecklist"]), []string{"hardConstraintKeys", "allowedOutcomes"}; !equalStrings(got, want) {
		t.Fatalf("closingChecklist keys = %v, want %v", got, want)
	}
	var resources []json.RawMessage
	if err := json.Unmarshal(top["resources"], &resources); err != nil || len(resources) == 0 {
		t.Fatalf("resources = %s (%v)", top["resources"], err)
	}
	if got, want := jsonKeys(t, resources[0]), []string{"ownerVersionId", "resourceKey", "priority", "contentHash", "content"}; !equalStrings(got, want) {
		t.Fatalf("resource keys = %v, want %v", got, want)
	}
	if string(top["schemaVersion"]) != "2" {
		t.Fatalf("schemaVersion = %s, want 2", top["schemaVersion"])
	}

	// Without a failing check the section is absent, not empty.
	plain, err := renderInstructionV2(goldenInstructionInput([]string{"approved", "rework"}, false))
	if err != nil {
		t.Fatalf("renderInstructionV2 (no failure): %v", err)
	}
	if got, want := jsonKeys(t, plain), []string{
		"schemaVersion", "hardConstraints", "taskContract", "resources", "messages", "closingChecklist",
	}; !equalStrings(got, want) {
		t.Fatalf("top-level keys without a failing check = %v, want %v", got, want)
	}
}

// TestRenderInstructionV2_OrdersByPriorityWhateverTheSnapshotOrder: the order is
// a property of the renderer, not of how the snapshot happened to list its
// resources — HARD_CONSTRAINTs are pulled out first (snapshot order among
// them), the rest follow REQUIRED_PROCEDURE, GUIDANCE, REFERENCE with the
// snapshot's order kept inside a priority.
func TestRenderInstructionV2_OrdersByPriorityWhateverTheSnapshotOrder(t *testing.T) {
	t.Parallel()
	resource := func(key string, priority definition.PriorityClass) instructionResourceInput {
		return instructionResourceInput{ownerVersionID: "skill-v1", resourceKey: key, priority: priority, contentHash: "sha256:" + key, content: key}
	}
	in := goldenInstructionInput([]string{"done"}, false)
	in.resources = []instructionResourceInput{
		resource("ref-b", definition.PriorityReference),
		resource("guide-b", definition.PriorityGuidance),
		resource("hard-b", definition.PriorityHardConstraint),
		resource("proc-b", definition.PriorityRequiredProcedure),
		resource("ref-a", definition.PriorityReference),
		resource("hard-a", definition.PriorityHardConstraint),
		resource("guide-a", definition.PriorityGuidance),
		resource("proc-a", definition.PriorityRequiredProcedure),
	}
	artifact, err := renderInstructionV2(in)
	if err != nil {
		t.Fatalf("renderInstructionV2: %v", err)
	}
	var decoded struct {
		HardConstraints []instructionResourceV2     `json:"hardConstraints"`
		Resources       []instructionResourceV2     `json:"resources"`
		Closing         instructionClosingChecklist `json:"closingChecklist"`
	}
	if err := json.Unmarshal(artifact, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	keysOf := func(list []instructionResourceV2) []string {
		keys := make([]string, 0, len(list))
		for _, r := range list {
			keys = append(keys, r.ResourceKey)
		}
		return keys
	}
	if got, want := keysOf(decoded.HardConstraints), []string{"hard-b", "hard-a"}; !equalStrings(got, want) {
		t.Fatalf("hardConstraints = %v, want %v (snapshot order among HARD_CONSTRAINT)", got, want)
	}
	if got, want := keysOf(decoded.Resources), []string{"proc-b", "proc-a", "guide-b", "guide-a", "ref-b", "ref-a"}; !equalStrings(got, want) {
		t.Fatalf("resources = %v, want %v (REQUIRED_PROCEDURE, GUIDANCE, REFERENCE; snapshot order inside each)", got, want)
	}
	if want := []string{"hard-b", "hard-a"}; !equalStrings(decoded.Closing.HardConstraintKeys, want) {
		t.Fatalf("closingChecklist.hardConstraintKeys = %v, want %v", decoded.Closing.HardConstraintKeys, want)
	}
	for _, r := range decoded.HardConstraints {
		if r.Priority != string(definition.PriorityHardConstraint) {
			t.Fatalf("hardConstraints entry %s has priority %q", r.ResourceKey, r.Priority)
		}
	}
	for _, r := range decoded.Resources {
		if r.Priority == "" || r.Priority == string(definition.PriorityHardConstraint) {
			t.Fatalf("resources entry %s has priority %q, want a non-hard priority", r.ResourceKey, r.Priority)
		}
	}
}

func TestRenderInstructionV2_FailsClosed(t *testing.T) {
	t.Parallel()
	in := goldenInstructionInput([]string{"done"}, false)
	in.resources = append(in.resources, instructionResourceInput{ownerVersionID: "skill-v1", resourceKey: "odd", priority: "SOMETIMES", contentHash: "sha256:odd"})
	if _, err := renderInstructionV2(in); err == nil {
		t.Fatal("renderInstructionV2 accepted a resource with an unknown priority")
	}
	if _, err := renderInstructionV2(goldenInstructionInput(nil, false)); err == nil {
		t.Fatal("renderInstructionV2 accepted a node with no allowed outcome")
	}
}

// TestRenderInstruction_SameInputSameBytesAndHash is V5-08B0's lock for both
// schemas: rendering twice from equal input is byte-identical, so the hash is.
func TestRenderInstruction_SameInputSameBytesAndHash(t *testing.T) {
	t.Parallel()
	for name, render := range map[string]func(instructionRenderInput) ([]byte, error){"v1": renderInstructionV1, "v2": renderInstructionV2} {
		first, err := render(goldenInstructionInput([]string{"approved", "rework"}, true))
		if err != nil {
			t.Fatalf("%s render: %v", name, err)
		}
		second, err := render(goldenInstructionInput([]string{"approved", "rework"}, true))
		if err != nil {
			t.Fatalf("%s render again: %v", name, err)
		}
		if !bytes.Equal(first, second) || sha256Hex(first) != sha256Hex(second) {
			t.Fatalf("%s: two renders of the same input differ:\n%s\n%s", name, first, second)
		}
	}
}

// TestRenderInstructionV2_DoesNotEscapeMarkup: the outcome protocol contains
// the marker's own angle brackets; a model must see them, not a JSON escape it
// could copy into a marker the parser rejects. v1 keeps its historical escaping.
func TestRenderInstructionV2_DoesNotEscapeMarkup(t *testing.T) {
	t.Parallel()
	v2, err := renderInstructionV2(goldenInstructionInput([]string{"approved", "rework"}, false))
	if err != nil {
		t.Fatalf("renderInstructionV2: %v", err)
	}
	if bytes.Contains(v2, []byte(`\u003c`)) || bytes.Contains(v2, []byte(`\u0026`)) {
		t.Fatalf("v2 artifact escapes markup:\n%s", v2)
	}
	if !bytes.Contains(v2, []byte("<agentkit-outcome>")) {
		t.Fatalf("v2 artifact does not show the marker tag:\n%s", v2)
	}
	v1, err := renderInstructionV1(goldenInstructionInput([]string{"approved", "rework"}, false))
	if err != nil {
		t.Fatalf("renderInstructionV1: %v", err)
	}
	if !bytes.Contains(v1, []byte(`\u003c`)) {
		t.Fatalf("v1 artifact no longer escapes markup, which would change every v1 hash:\n%s", v1)
	}
}

// TestInstructionOutcomeProtocol_MatchesTheMarkerTheProvidersParse ties the
// fixed protocol text to the marker the provider adapters parse and the fake
// CLI emits: the text shows exactly the bytes OutcomeMarker builds, and says
// the things the parser enforces.
func TestInstructionOutcomeProtocol_MatchesTheMarkerTheProvidersParse(t *testing.T) {
	t.Parallel()
	if want := providers.OutcomeMarker("NAME"); !strings.Contains(instructionOutcomeProtocol, want) {
		t.Fatalf("outcomeProtocol does not contain the marker %s the providers parse:\n%s", want, instructionOutcomeProtocol)
	}
	for _, mustSay := range []string{"allowedOutcomes", "very last thing", "once", "missing, repeated, malformed"} {
		if !strings.Contains(instructionOutcomeProtocol, mustSay) {
			t.Fatalf("outcomeProtocol does not say %q:\n%s", mustSay, instructionOutcomeProtocol)
		}
	}
	// The text is JSON-string safe and the same for every node: no rendering
	// per node, no placeholders left open.
	if strings.ContainsAny(instructionOutcomeProtocol, "\n\r\t") {
		t.Fatalf("outcomeProtocol contains control characters: %q", instructionOutcomeProtocol)
	}
}
