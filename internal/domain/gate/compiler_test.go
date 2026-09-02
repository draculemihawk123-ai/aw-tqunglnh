package gate_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/gate"
)

func draftDefinition(id string) gate.GateDefinition {
	fields, err := definition.Create(definition.CreateRequest{
		Kind: definition.KindGate, Scope: definition.GlobalScope(), Name: "demo-gate",
	})
	if err != nil {
		panic(err)
	}
	return gate.GateDefinition{ID: gate.GateDefinitionID(id), Fields: fields}
}

func validPublishRequest() gate.PublishRequest {
	return gate.PublishRequest{
		VersionID:     "gate-v1",
		VersionNumber: 1,
		SchemaVersion: 1,
		Document:      validDocument(),
		PublishedBy:   "alice",
		PublishedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestCompile_ValidRequestSucceeds(t *testing.T) {
	fields, err := gate.Compile(draftDefinition("gate-1"), validPublishRequest())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if fields.Kind() != definition.KindGate {
		t.Fatalf("Kind() = %v, want KindGate", fields.Kind())
	}
	if fields.SourceHash() == "" || fields.CompiledHash() == "" {
		t.Fatal("Compile should produce non-empty SourceHash and CompiledHash")
	}
}

func TestCompile_RejectsInvalidDocument(t *testing.T) {
	req := validPublishRequest()
	req.Document.Criteria = nil
	if _, err := gate.Compile(draftDefinition("gate-1"), req); err == nil {
		t.Fatal("Compile should reject an invalid document")
	}
}

func TestCompile_RejectsArchivedDefinition(t *testing.T) {
	def := draftDefinition("gate-1")
	archived, err := definition.Archive(def.Fields)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	def.Fields = archived
	if _, err := gate.Compile(def, validPublishRequest()); err == nil {
		t.Fatal("Compile should reject publishing a version for an archived definition")
	}
}

func TestCompile_CriteriaOrderIndependent(t *testing.T) {
	first := validDocument()
	second := validDocument()
	second.Criteria[0], second.Criteria[1] = second.Criteria[1], second.Criteria[0]

	reqFirst := validPublishRequest()
	reqFirst.Document = first
	reqSecond := validPublishRequest()
	reqSecond.Document = second

	fieldsFirst, err := gate.Compile(draftDefinition("gate-1"), reqFirst)
	if err != nil {
		t.Fatalf("Compile(first): %v", err)
	}
	fieldsSecond, err := gate.Compile(draftDefinition("gate-1"), reqSecond)
	if err != nil {
		t.Fatalf("Compile(second): %v", err)
	}
	if fieldsFirst.SourceHash() != fieldsSecond.SourceHash() {
		t.Fatalf("SourceHash differs for the same criteria in different order: %s vs %s",
			fieldsFirst.SourceHash(), fieldsSecond.SourceHash())
	}
}

func TestCompile_SourceHashExcludesDependencies_CompiledHashIncludesThem(t *testing.T) {
	reqNoDeps := validPublishRequest()
	reqWithDeps := validPublishRequest()
	reqWithDeps.Dependencies = definition.DependencyManifest{Pins: []definition.DependencyPin{
		{Kind: definition.KindPolicy, DefinitionID: "policy-2", VersionID: "policy-2-v1"},
	}}

	fieldsNoDeps, err := gate.Compile(draftDefinition("gate-1"), reqNoDeps)
	if err != nil {
		t.Fatalf("Compile(no deps): %v", err)
	}
	fieldsWithDeps, err := gate.Compile(draftDefinition("gate-1"), reqWithDeps)
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

func TestCompile_Deterministic_RepeatedCalls(t *testing.T) {
	req := validPublishRequest()
	def := draftDefinition("gate-1")

	first, err := gate.Compile(def, req)
	if err != nil {
		t.Fatalf("Compile (attempt 0): %v", err)
	}
	for i := 1; i < 10; i++ {
		next, err := gate.Compile(def, req)
		if err != nil {
			t.Fatalf("Compile (attempt %d): %v", i, err)
		}
		if next.SourceHash() != first.SourceHash() || next.CompiledHash() != first.CompiledHash() {
			t.Fatalf("attempt %d produced different hashes than attempt 0", i)
		}
	}
}

func TestCompileFrom_RejectsUnknownField(t *testing.T) {
	source := `{"commandRef": {"kind": "COMMAND", "definitionId": "x", "versionId": "y"}, "typo_field": "x"}`
	if _, err := gate.CompileFrom(draftDefinition("gate-1"), []byte(source), authoring.FormatJSON, validPublishRequest()); err == nil {
		t.Fatal("CompileFrom should reject an unknown field")
	}
}

func TestCompileFrom_JSONAndYAML_SameContent_SameHash(t *testing.T) {
	jsonSource := `{
		"commandRef": {"kind": "COMMAND", "definitionId": "command-1", "versionId": "command-1-v1"},
		"criteria": [{"name": "exit-code-zero", "evidenceKey": "exitCode"}, {"name": "no-stderr", "evidenceKey": "stderr"}],
		"policyRefs": [{"kind": "POLICY", "definitionId": "policy-1", "versionId": "policy-1-v1"}]
	}`
	yamlSource := "commandRef:\n  kind: COMMAND\n  definitionId: command-1\n  versionId: command-1-v1\n" +
		"criteria:\n  - name: exit-code-zero\n    evidenceKey: exitCode\n  - name: no-stderr\n    evidenceKey: stderr\n" +
		"policyRefs:\n  - kind: POLICY\n    definitionId: policy-1\n    versionId: policy-1-v1\n"

	fieldsJSON, err := gate.CompileFrom(draftDefinition("gate-1"), []byte(jsonSource), authoring.FormatJSON, validPublishRequest())
	if err != nil {
		t.Fatalf("CompileFrom(json): %v", err)
	}
	fieldsYAML, err := gate.CompileFrom(draftDefinition("gate-1"), []byte(yamlSource), authoring.FormatYAML, validPublishRequest())
	if err != nil {
		t.Fatalf("CompileFrom(yaml): %v", err)
	}
	if fieldsJSON.SourceHash() != fieldsYAML.SourceHash() {
		t.Fatalf("jsonHash = %s, yamlHash = %s, want identical for the same semantic content",
			fieldsJSON.SourceHash(), fieldsYAML.SourceHash())
	}
}

func TestCompile_Golden(t *testing.T) {
	fields, err := gate.Compile(draftDefinition("gate-1"), validPublishRequest())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	goldenPath := filepath.Join("testdata", "golden", "gate-example.json")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got := fields.CanonicalSource() + "\n"
	if got != string(want) {
		t.Fatalf("canonical source does not match golden fixture.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
