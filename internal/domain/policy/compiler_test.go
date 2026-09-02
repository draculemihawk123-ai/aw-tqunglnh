package policy_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/policy"
)

func draftDefinition(id string) policy.PolicyDefinition {
	fields, err := definition.Create(definition.CreateRequest{
		Kind: definition.KindPolicy, Scope: definition.GlobalScope(), Name: "demo-policy",
	})
	if err != nil {
		panic(err)
	}
	return policy.PolicyDefinition{ID: policy.PolicyDefinitionID(id), Fields: fields}
}

func validPublishRequest() policy.PublishRequest {
	return policy.PublishRequest{
		VersionID:     "policy-v1",
		VersionNumber: 1,
		SchemaVersion: 1,
		Document:      validContextDocument(),
		PublishedBy:   "alice",
		PublishedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestCompile_ValidRequestSucceeds(t *testing.T) {
	fields, err := policy.Compile(draftDefinition("policy-1"), validPublishRequest())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if fields.Kind() != definition.KindPolicy {
		t.Fatalf("Kind() = %v, want KindPolicy", fields.Kind())
	}
	if fields.SourceHash() == "" || fields.CompiledHash() == "" {
		t.Fatal("Compile should produce non-empty SourceHash and CompiledHash")
	}
}

func TestCompile_RejectsInvalidDocument(t *testing.T) {
	req := validPublishRequest()
	req.Document.Context.Selector = nil
	if _, err := policy.Compile(draftDefinition("policy-1"), req); err == nil {
		t.Fatal("Compile should reject an invalid document")
	}
}

func TestCompile_RejectsArchivedDefinition(t *testing.T) {
	def := draftDefinition("policy-1")
	archived, err := definition.Archive(def.Fields)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	def.Fields = archived
	if _, err := policy.Compile(def, validPublishRequest()); err == nil {
		t.Fatal("Compile should reject publishing a version for an archived definition")
	}
}

func TestCompile_RejectsMissingPublisherOrTimestamp(t *testing.T) {
	req := validPublishRequest()
	req.PublishedBy = ""
	if _, err := policy.Compile(draftDefinition("policy-1"), req); err == nil {
		t.Fatal("Compile should reject a missing publisher")
	}

	req = validPublishRequest()
	req.PublishedAt = time.Time{}
	if _, err := policy.Compile(draftDefinition("policy-1"), req); err == nil {
		t.Fatal("Compile should reject a missing publish timestamp")
	}
}

// TestCompile_SetOrderIndependent proves selector/resourceRefs/
// grantedCapabilities/retryableErrorCodes/requiredEvidenceKinds are all
// treated as sets, while context.order (checked separately below) is
// not.
func TestCompile_SetOrderIndependent(t *testing.T) {
	first := validContextDocument()
	first.Context.Selector = []string{"skills/*", "layers/*"}

	second := validContextDocument()
	second.Context.Selector = []string{"layers/*", "skills/*"}

	reqFirst := validPublishRequest()
	reqFirst.Document = first
	reqSecond := validPublishRequest()
	reqSecond.Document = second

	fieldsFirst, err := policy.Compile(draftDefinition("policy-1"), reqFirst)
	if err != nil {
		t.Fatalf("Compile(first): %v", err)
	}
	fieldsSecond, err := policy.Compile(draftDefinition("policy-1"), reqSecond)
	if err != nil {
		t.Fatalf("Compile(second): %v", err)
	}
	if fieldsFirst.SourceHash() != fieldsSecond.SourceHash() {
		t.Fatalf("SourceHash differs for the same content in different set order: %s vs %s",
			fieldsFirst.SourceHash(), fieldsSecond.SourceHash())
	}
}

// TestCompile_OrderIsSignificant makes sure Compile did not over-apply
// set semantics to context.order: a genuinely different sequence must
// hash differently.
func TestCompile_OrderIsSignificant(t *testing.T) {
	first := validPublishRequest()
	second := validPublishRequest()
	second.Document.Context.Order = []string{"skills/lint", "layers/base"} // reversed

	fieldsFirst, err := policy.Compile(draftDefinition("policy-1"), first)
	if err != nil {
		t.Fatalf("Compile(first): %v", err)
	}
	fieldsSecond, err := policy.Compile(draftDefinition("policy-1"), second)
	if err != nil {
		t.Fatalf("Compile(second): %v", err)
	}
	if fieldsFirst.SourceHash() == fieldsSecond.SourceHash() {
		t.Fatal("SourceHash should differ when context.order genuinely differs")
	}
}

// TestCompile_SourceHashExcludesDependencies_CompiledHashIncludesThem is
// ADR-012's own distinction, mirroring block.TestCompile_SourceHashExcludesDependencies_CompiledHashIncludesThem.
func TestCompile_SourceHashExcludesDependencies_CompiledHashIncludesThem(t *testing.T) {
	reqNoDeps := validPublishRequest()
	reqWithDeps := validPublishRequest()
	reqWithDeps.Dependencies = definition.DependencyManifest{Pins: []definition.DependencyPin{
		{Kind: definition.KindLayer, DefinitionID: "layer-1", VersionID: "layer-1-v1"},
	}}

	fieldsNoDeps, err := policy.Compile(draftDefinition("policy-1"), reqNoDeps)
	if err != nil {
		t.Fatalf("Compile(no deps): %v", err)
	}
	fieldsWithDeps, err := policy.Compile(draftDefinition("policy-1"), reqWithDeps)
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
	def := draftDefinition("policy-1")

	first, err := policy.Compile(def, req)
	if err != nil {
		t.Fatalf("Compile (attempt 0): %v", err)
	}
	for i := 1; i < 10; i++ {
		next, err := policy.Compile(def, req)
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
		"category": "ATTEMPT",
		"attempt": {
			"maxAttempts": 3,
			"retryableErrorCodes": ["TIMEOUT", "UNAVAILABLE"],
			"backoffSeconds": 5,
			"timeoutSeconds": 60
		}
	}`
	yamlSource := "category: ATTEMPT\n" +
		"attempt:\n" +
		"  maxAttempts: 3\n" +
		"  retryableErrorCodes: [TIMEOUT, UNAVAILABLE]\n" +
		"  backoffSeconds: 5\n" +
		"  timeoutSeconds: 60\n"

	reqJSON := validPublishRequest()
	reqYAML := validPublishRequest()

	fieldsJSON, err := policy.CompileFrom(draftDefinition("policy-1"), []byte(jsonSource), authoring.FormatJSON, reqJSON)
	if err != nil {
		t.Fatalf("CompileFrom(json): %v", err)
	}
	fieldsYAML, err := policy.CompileFrom(draftDefinition("policy-1"), []byte(yamlSource), authoring.FormatYAML, reqYAML)
	if err != nil {
		t.Fatalf("CompileFrom(yaml): %v", err)
	}
	if fieldsJSON.SourceHash() != fieldsYAML.SourceHash() {
		t.Fatalf("jsonHash = %s, yamlHash = %s, want identical for the same semantic content",
			fieldsJSON.SourceHash(), fieldsYAML.SourceHash())
	}
}

func TestCompileFrom_RejectsUnknownField(t *testing.T) {
	source := `{"category": "ATTEMPT", "typo_field": "x"}`
	if _, err := policy.CompileFrom(draftDefinition("policy-1"), []byte(source), authoring.FormatJSON, validPublishRequest()); err == nil {
		t.Fatal("CompileFrom should reject an unknown field")
	}
}

// TestCompile_Golden is V2-07's own "hash tests" requirement: a
// representative PolicyDocument's canonical SourceHash is compared
// against a checked-in fixture.
func TestCompile_Golden(t *testing.T) {
	fields, err := policy.Compile(draftDefinition("policy-1"), validPublishRequest())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	goldenPath := filepath.Join("testdata", "golden", "policy-context-example.json")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got := fields.CanonicalSource() + "\n"
	if got != string(want) {
		t.Fatalf("canonical source does not match golden fixture.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
