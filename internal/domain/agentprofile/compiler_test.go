package agentprofile_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/agentprofile"
	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

func draftDefinition(id string) agentprofile.AgentProfileDefinition {
	fields, err := definition.Create(definition.CreateRequest{
		Kind: definition.KindAgentProfile, Scope: definition.GlobalScope(), Name: "demo-agent-profile",
	})
	if err != nil {
		panic(err)
	}
	return agentprofile.AgentProfileDefinition{ID: agentprofile.AgentProfileDefinitionID(id), Fields: fields}
}

func validPublishRequest() agentprofile.PublishRequest {
	return agentprofile.PublishRequest{
		VersionID:     "agent-profile-v1",
		VersionNumber: 1,
		SchemaVersion: 1,
		Document:      validDocument(),
		PublishedBy:   "alice",
		PublishedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestCompile_ValidRequestSucceeds(t *testing.T) {
	fields, err := agentprofile.Compile(draftDefinition("agent-profile-1"), validPublishRequest())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if fields.Kind() != definition.KindAgentProfile {
		t.Fatalf("Kind() = %v, want KindAgentProfile", fields.Kind())
	}
	if fields.SourceHash() == "" || fields.CompiledHash() == "" {
		t.Fatal("Compile should produce non-empty SourceHash and CompiledHash")
	}
}

func TestCompile_RejectsInvalidDocument(t *testing.T) {
	req := validPublishRequest()
	req.Document.Budget.MaxTokens = 0
	if _, err := agentprofile.Compile(draftDefinition("agent-profile-1"), req); err == nil {
		t.Fatal("Compile should reject an invalid document")
	}
}

func TestCompile_RejectsArchivedDefinition(t *testing.T) {
	def := draftDefinition("agent-profile-1")
	archived, err := definition.Archive(def.Fields)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	def.Fields = archived
	if _, err := agentprofile.Compile(def, validPublishRequest()); err == nil {
		t.Fatal("Compile should reject publishing a version for an archived definition")
	}
}

func TestCompile_RejectsMissingPublisherOrTimestamp(t *testing.T) {
	req := validPublishRequest()
	req.PublishedBy = ""
	if _, err := agentprofile.Compile(draftDefinition("agent-profile-1"), req); err == nil {
		t.Fatal("Compile should reject a missing publisher")
	}

	req = validPublishRequest()
	req.PublishedAt = time.Time{}
	if _, err := agentprofile.Compile(draftDefinition("agent-profile-1"), req); err == nil {
		t.Fatal("Compile should reject a missing publish timestamp")
	}
}

func TestCompile_SetOrderIndependent(t *testing.T) {
	first := validDocument()
	first.ToolRefs = []string{"read_file", "write_file"}
	first.RequiredCapabilities = []string{"A", "B"}
	first.Compatibility.OS = []string{"windows", "linux"}

	second := validDocument()
	second.ToolRefs = []string{"write_file", "read_file"}
	second.RequiredCapabilities = []string{"B", "A"}
	second.Compatibility.OS = []string{"linux", "windows"}

	reqFirst := validPublishRequest()
	reqFirst.Document = first
	reqSecond := validPublishRequest()
	reqSecond.Document = second

	fieldsFirst, err := agentprofile.Compile(draftDefinition("agent-profile-1"), reqFirst)
	if err != nil {
		t.Fatalf("Compile(first): %v", err)
	}
	fieldsSecond, err := agentprofile.Compile(draftDefinition("agent-profile-1"), reqSecond)
	if err != nil {
		t.Fatalf("Compile(second): %v", err)
	}
	if fieldsFirst.SourceHash() != fieldsSecond.SourceHash() {
		t.Fatalf("SourceHash differs for the same content in different set order: %s vs %s",
			fieldsFirst.SourceHash(), fieldsSecond.SourceHash())
	}
	if fieldsFirst.CompiledHash() != fieldsSecond.CompiledHash() {
		t.Fatalf("CompiledHash differs for the same content in different set order: %s vs %s",
			fieldsFirst.CompiledHash(), fieldsSecond.CompiledHash())
	}
}

func TestCompile_DifferentContentHashesDiffer(t *testing.T) {
	first := validPublishRequest()
	second := validPublishRequest()
	second.Document.Model = "claude-sonnet-5"

	fieldsFirst, err := agentprofile.Compile(draftDefinition("agent-profile-1"), first)
	if err != nil {
		t.Fatalf("Compile(first): %v", err)
	}
	fieldsSecond, err := agentprofile.Compile(draftDefinition("agent-profile-1"), second)
	if err != nil {
		t.Fatalf("Compile(second): %v", err)
	}
	if fieldsFirst.SourceHash() == fieldsSecond.SourceHash() {
		t.Fatal("SourceHash should differ when model genuinely differs")
	}
}

// TestCompile_SourceHashExcludesDependencies_CompiledHashIncludesThem is
// ADR-012's own distinction, mirroring
// block.TestCompile_SourceHashExcludesDependencies_CompiledHashIncludesThem.
func TestCompile_SourceHashExcludesDependencies_CompiledHashIncludesThem(t *testing.T) {
	reqNoDeps := validPublishRequest()
	reqWithDeps := validPublishRequest()
	reqWithDeps.Dependencies = definition.DependencyManifest{Pins: []definition.DependencyPin{
		{Kind: definition.KindPolicy, DefinitionID: "policy-context-1", VersionID: "policy-context-1-v1"},
	}}

	fieldsNoDeps, err := agentprofile.Compile(draftDefinition("agent-profile-1"), reqNoDeps)
	if err != nil {
		t.Fatalf("Compile(no deps): %v", err)
	}
	fieldsWithDeps, err := agentprofile.Compile(draftDefinition("agent-profile-1"), reqWithDeps)
	if err != nil {
		t.Fatalf("Compile(with deps): %v", err)
	}
	if fieldsNoDeps.SourceHash() != fieldsWithDeps.SourceHash() {
		t.Fatalf("SourceHash should be identical regardless of dependencies: %s vs %s",
			fieldsNoDeps.SourceHash(), fieldsWithDeps.SourceHash())
	}
	if fieldsNoDeps.CompiledHash() == fieldsWithDeps.CompiledHash() {
		t.Fatal("CompiledHash should differ once a dependency manifest is added")
	}
}

// TestCompile_EffectiveProfile_CanonicalHash is V2-07's own "Hoàn thành
// khi: effective profile canonical/hash được" bar made concrete: the
// "effective" profile is the authored document plus its exact resolved
// dependency manifest (here, the context-route Policy pin actually
// resolved to a real published version) — that combination canonicalizes
// and hashes deterministically, and a different resolved dependency
// (e.g. the same context-route DefinitionID resolving to a different
// published VersionID, simulating a republish having moved the pin)
// produces a genuinely different effective hash even though the
// authored document's own ContextPolicyRef field did not change.
func TestCompile_EffectiveProfile_CanonicalHash(t *testing.T) {
	resolvedV1 := definition.DependencyManifest{Pins: []definition.DependencyPin{
		{Kind: definition.KindPolicy, DefinitionID: "policy-context-1", VersionID: "policy-context-1-v1"},
	}}
	resolvedV2 := definition.DependencyManifest{Pins: []definition.DependencyPin{
		{Kind: definition.KindPolicy, DefinitionID: "policy-context-1", VersionID: "policy-context-1-v2"},
	}}

	reqV1 := validPublishRequest()
	reqV1.Dependencies = resolvedV1
	reqV2 := validPublishRequest()
	reqV2.Dependencies = resolvedV2

	fieldsV1, err := agentprofile.Compile(draftDefinition("agent-profile-1"), reqV1)
	if err != nil {
		t.Fatalf("Compile(resolvedV1): %v", err)
	}
	fieldsV2, err := agentprofile.Compile(draftDefinition("agent-profile-1"), reqV2)
	if err != nil {
		t.Fatalf("Compile(resolvedV2): %v", err)
	}

	if fieldsV1.CanonicalSource() == "" || fieldsV1.CompiledHash() == "" {
		t.Fatal("an effective profile must canonicalize and hash to non-empty values")
	}
	if fieldsV1.SourceHash() != fieldsV2.SourceHash() {
		t.Fatal("the authored document did not change, so SourceHash must stay identical across a different resolved dependency")
	}
	if fieldsV1.CompiledHash() == fieldsV2.CompiledHash() {
		t.Fatal("the effective (compiled) profile hash must change when the resolved context-route dependency version changes")
	}
}

func TestCompile_Deterministic_RepeatedCalls(t *testing.T) {
	req := validPublishRequest()
	def := draftDefinition("agent-profile-1")

	first, err := agentprofile.Compile(def, req)
	if err != nil {
		t.Fatalf("Compile (attempt 0): %v", err)
	}
	for i := 1; i < 10; i++ {
		next, err := agentprofile.Compile(def, req)
		if err != nil {
			t.Fatalf("Compile (attempt %d): %v", i, err)
		}
		if next.SourceHash() != first.SourceHash() || next.CompiledHash() != first.CompiledHash() {
			t.Fatalf("attempt %d produced different hashes than attempt 0", i)
		}
	}
}

func TestCompileFrom_JSONAndYAML_SameContent_SameHash(t *testing.T) {
	jsonSource := `{
		"providerKey": "claude",
		"model": "claude-opus-4",
		"toolRefs": ["read_file", "write_file"],
		"contextPolicyRef": {"kind": "POLICY", "definitionId": "policy-context-1", "versionId": "policy-context-1-v1"},
		"compatibility": {"os": ["windows", "linux"], "toolchain": ["git"]},
		"requiredCapabilities": ["INTEGRATION_MULTI_REPOSITORY_WRITE"],
		"budget": {"maxTokens": 100000}
	}`
	yamlSource := "providerKey: claude\n" +
		"model: claude-opus-4\n" +
		"toolRefs: [read_file, write_file]\n" +
		"contextPolicyRef:\n  kind: POLICY\n  definitionId: policy-context-1\n  versionId: policy-context-1-v1\n" +
		"compatibility:\n  os: [windows, linux]\n  toolchain: [git]\n" +
		"requiredCapabilities: [INTEGRATION_MULTI_REPOSITORY_WRITE]\n" +
		"budget:\n  maxTokens: 100000\n"

	reqJSON := validPublishRequest()
	reqYAML := validPublishRequest()

	fieldsJSON, err := agentprofile.CompileFrom(draftDefinition("agent-profile-1"), []byte(jsonSource), authoring.FormatJSON, reqJSON)
	if err != nil {
		t.Fatalf("CompileFrom(json): %v", err)
	}
	fieldsYAML, err := agentprofile.CompileFrom(draftDefinition("agent-profile-1"), []byte(yamlSource), authoring.FormatYAML, reqYAML)
	if err != nil {
		t.Fatalf("CompileFrom(yaml): %v", err)
	}
	if fieldsJSON.SourceHash() != fieldsYAML.SourceHash() {
		t.Fatalf("jsonHash = %s, yamlHash = %s, want identical for the same semantic content",
			fieldsJSON.SourceHash(), fieldsYAML.SourceHash())
	}
}

func TestCompileFrom_RejectsUnknownField(t *testing.T) {
	source := `{"providerKey": "claude", "typo_field": "x"}`
	if _, err := agentprofile.CompileFrom(draftDefinition("agent-profile-1"), []byte(source), authoring.FormatJSON, validPublishRequest()); err == nil {
		t.Fatal("CompileFrom should reject an unknown field")
	}
}

// TestCompile_Golden is V2-07's own "hash tests" requirement: a
// representative AgentProfileDocument's canonical SourceHash is compared
// against a checked-in fixture.
func TestCompile_Golden(t *testing.T) {
	fields, err := agentprofile.Compile(draftDefinition("agent-profile-1"), validPublishRequest())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	goldenPath := filepath.Join("testdata", "golden", "agent-profile-example.json")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got := fields.CanonicalSource() + "\n"
	if got != string(want) {
		t.Fatalf("canonical source does not match golden fixture.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
