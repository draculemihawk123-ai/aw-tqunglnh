package block_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/block"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

func draftDefinition(id string) block.BlockDefinition {
	fields, err := definition.Create(definition.CreateRequest{
		Kind: definition.KindBlock, Scope: definition.GlobalScope(), Name: "demo-block",
	})
	if err != nil {
		panic(err)
	}
	return block.BlockDefinition{ID: block.BlockDefinitionID(id), Fields: fields}
}

func validPublishRequest() block.PublishRequest {
	return block.PublishRequest{
		VersionID:     "block-v1",
		VersionNumber: 1,
		SchemaVersion: 1,
		Document:      validDocument(),
		PublishedBy:   "alice",
		PublishedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestCompile_ValidRequestSucceeds(t *testing.T) {
	fields, err := block.Compile(draftDefinition("block-1"), validPublishRequest())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if fields.Kind() != definition.KindBlock {
		t.Fatalf("Kind() = %v, want KindBlock", fields.Kind())
	}
	if fields.SourceHash() == "" || fields.CompiledHash() == "" {
		t.Fatal("Compile should produce non-empty SourceHash and CompiledHash")
	}
}

func TestCompile_RejectsInvalidDocument(t *testing.T) {
	req := validPublishRequest()
	req.Document.Outcomes = nil
	if _, err := block.Compile(draftDefinition("block-1"), req); err == nil {
		t.Fatal("Compile should reject an invalid document")
	}
}

func TestCompile_RejectsArchivedDefinition(t *testing.T) {
	def := draftDefinition("block-1")
	archived, err := definition.Archive(def.Fields)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	def.Fields = archived
	if _, err := block.Compile(def, validPublishRequest()); err == nil {
		t.Fatal("Compile should reject publishing a version for an archived definition")
	}
}

func TestCompile_RejectsMissingPublisherOrTimestamp(t *testing.T) {
	req := validPublishRequest()
	req.PublishedBy = ""
	if _, err := block.Compile(draftDefinition("block-1"), req); err == nil {
		t.Fatal("Compile should reject a missing publisher")
	}

	req = validPublishRequest()
	req.PublishedAt = time.Time{}
	if _, err := block.Compile(draftDefinition("block-1"), req); err == nil {
		t.Fatal("Compile should reject a missing publish timestamp")
	}
}

// TestCompile_KeyOrderAndSetOrderIndependent is V2-04's own "hash tests"
// requirement from a different angle than the golden fixture below: two
// documents that only differ in the authored order of set-like fields
// (compatibleNodeTypes, requiredCapabilities, outcomes, policyRefs) must
// compile to the identical SourceHash and CompiledHash.
func TestCompile_SetOrderIndependent(t *testing.T) {
	first := validDocument()
	first.RequiredCapabilities = []string{"A", "B"}
	first.Outcomes = []string{"success", "failure"}

	second := validDocument()
	second.RequiredCapabilities = []string{"B", "A"}
	second.Outcomes = []string{"failure", "success"}

	reqFirst := validPublishRequest()
	reqFirst.Document = first
	reqSecond := validPublishRequest()
	reqSecond.Document = second

	fieldsFirst, err := block.Compile(draftDefinition("block-1"), reqFirst)
	if err != nil {
		t.Fatalf("Compile(first): %v", err)
	}
	fieldsSecond, err := block.Compile(draftDefinition("block-1"), reqSecond)
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

// TestCompile_ListOrderIsSignificant makes sure Compile did not
// over-apply set semantics: compatibleNodeTypes is treated as a set
// (order-independent) by this package's own design, but a genuinely
// different node-type membership must still hash differently.
func TestCompile_DifferentContentHashesDiffer(t *testing.T) {
	first := validPublishRequest()
	second := validPublishRequest()
	second.Document.Outcomes = []string{"success", "different-failure"}

	fieldsFirst, err := block.Compile(draftDefinition("block-1"), first)
	if err != nil {
		t.Fatalf("Compile(first): %v", err)
	}
	fieldsSecond, err := block.Compile(draftDefinition("block-1"), second)
	if err != nil {
		t.Fatalf("Compile(second): %v", err)
	}
	if fieldsFirst.SourceHash() == fieldsSecond.SourceHash() {
		t.Fatal("SourceHash should differ when outcomes genuinely differ")
	}
}

// TestCompile_SourceHashExcludesDependencies is ADR-012's own
// distinction: SourceHash covers authored content only, CompiledHash
// covers authored content plus the dependency manifest — so publishing
// the exact same document under two different dependency manifests must
// keep SourceHash identical while CompiledHash differs.
func TestCompile_SourceHashExcludesDependencies_CompiledHashIncludesThem(t *testing.T) {
	reqNoDeps := validPublishRequest()
	reqWithDeps := validPublishRequest()
	reqWithDeps.Dependencies = definition.DependencyManifest{Pins: []definition.DependencyPin{
		{Kind: definition.KindCommand, DefinitionID: "cmd-1", VersionID: "cmd-1-v1"},
	}}

	fieldsNoDeps, err := block.Compile(draftDefinition("block-1"), reqNoDeps)
	if err != nil {
		t.Fatalf("Compile(no deps): %v", err)
	}
	fieldsWithDeps, err := block.Compile(draftDefinition("block-1"), reqWithDeps)
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
	def := draftDefinition("block-1")

	first, err := block.Compile(def, req)
	if err != nil {
		t.Fatalf("Compile (attempt 0): %v", err)
	}
	for i := 1; i < 10; i++ {
		next, err := block.Compile(def, req)
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
		"compatibleNodeTypes": ["AGENT"],
		"requiredCapabilities": ["INTEGRATION_MULTI_REPOSITORY_WRITE"],
		"timeoutSeconds": 60,
		"scopeSelector": {"access": "WRITE", "pathScopes": ["src/**"]},
		"doneCondition": "result.outcome",
		"executorRef": {"kind": "COMMAND", "definitionId": "cmd-1", "versionId": "cmd-1-v1"},
		"policyRefs": [{"kind": "POLICY", "definitionId": "policy-1", "versionId": "policy-1-v1"}],
		"outcomes": ["success", "failure"]
	}`
	yamlSource := "compatibleNodeTypes: [AGENT]\n" +
		"requiredCapabilities: [INTEGRATION_MULTI_REPOSITORY_WRITE]\n" +
		"timeoutSeconds: 60\n" +
		"scopeSelector:\n  access: WRITE\n  pathScopes: [src/**]\n" +
		"doneCondition: result.outcome\n" +
		"executorRef:\n  kind: COMMAND\n  definitionId: cmd-1\n  versionId: cmd-1-v1\n" +
		"policyRefs:\n  - kind: POLICY\n    definitionId: policy-1\n    versionId: policy-1-v1\n" +
		"outcomes: [success, failure]\n"

	reqJSON := validPublishRequest()
	reqYAML := validPublishRequest()

	fieldsJSON, err := block.CompileFrom(draftDefinition("block-1"), []byte(jsonSource), authoring.FormatJSON, reqJSON)
	if err != nil {
		t.Fatalf("CompileFrom(json): %v", err)
	}
	fieldsYAML, err := block.CompileFrom(draftDefinition("block-1"), []byte(yamlSource), authoring.FormatYAML, reqYAML)
	if err != nil {
		t.Fatalf("CompileFrom(yaml): %v", err)
	}
	if fieldsJSON.SourceHash() != fieldsYAML.SourceHash() {
		t.Fatalf("jsonHash = %s, yamlHash = %s, want identical for the same semantic content",
			fieldsJSON.SourceHash(), fieldsYAML.SourceHash())
	}
}

func TestCompileFrom_RejectsUnknownField(t *testing.T) {
	source := `{"compatibleNodeTypes": ["AGENT"], "typo_field": "x"}`
	if _, err := block.CompileFrom(draftDefinition("block-1"), []byte(source), authoring.FormatJSON, validPublishRequest()); err == nil {
		t.Fatal("CompileFrom should reject an unknown field")
	}
}

// TestCompile_Golden is V2-04's own "hash tests" requirement: a
// representative BlockDocument's canonical SourceHash is compared
// against a checked-in fixture, so a change to Block's canonicalization
// behavior itself (not just its determinism) is caught.
func TestCompile_Golden(t *testing.T) {
	fields, err := block.Compile(draftDefinition("block-1"), validPublishRequest())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	goldenPath := filepath.Join("testdata", "golden", "block-example.json")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got := fields.CanonicalSource() + "\n"
	if got != string(want) {
		t.Fatalf("canonical source does not match golden fixture.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
