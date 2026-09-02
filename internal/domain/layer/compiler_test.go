package layer_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/layer"
)

func draftDefinition(id string) layer.LayerDefinition {
	fields, err := definition.Create(definition.CreateRequest{
		Kind: definition.KindLayer, Scope: definition.GlobalScope(), Name: "demo-layer",
	})
	if err != nil {
		panic(err)
	}
	return layer.LayerDefinition{ID: layer.LayerDefinitionID(id), Fields: fields}
}

func validPublishRequest() layer.PublishRequest {
	return layer.PublishRequest{
		VersionID:     "layer-v1",
		VersionNumber: 1,
		SchemaVersion: 1,
		Document:      validDocument(),
		PublishedBy:   "alice",
		PublishedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestCompile_ValidRequestSucceeds(t *testing.T) {
	fields, err := layer.Compile(draftDefinition("layer-1"), validPublishRequest())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if fields.Kind() != definition.KindLayer {
		t.Fatalf("Kind() = %v, want KindLayer", fields.Kind())
	}
	if fields.SourceHash() == "" || fields.CompiledHash() == "" {
		t.Fatal("Compile should produce non-empty SourceHash and CompiledHash")
	}
}

func TestCompile_RejectsInvalidDocument(t *testing.T) {
	req := validPublishRequest()
	req.Document.Resources = nil
	if _, err := layer.Compile(draftDefinition("layer-1"), req); err == nil {
		t.Fatal("Compile should reject an invalid document")
	}
}

func TestCompile_RejectsArchivedDefinition(t *testing.T) {
	def := draftDefinition("layer-1")
	archived, err := definition.Archive(def.Fields)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	def.Fields = archived
	if _, err := layer.Compile(def, validPublishRequest()); err == nil {
		t.Fatal("Compile should reject publishing a version for an archived definition")
	}
}

func TestCompile_RejectsMissingPublisherOrTimestamp(t *testing.T) {
	req := validPublishRequest()
	req.PublishedBy = ""
	if _, err := layer.Compile(draftDefinition("layer-1"), req); err == nil {
		t.Fatal("Compile should reject a missing publisher")
	}

	req = validPublishRequest()
	req.PublishedAt = time.Time{}
	if _, err := layer.Compile(draftDefinition("layer-1"), req); err == nil {
		t.Fatal("Compile should reject a missing publish timestamp")
	}
}

func TestCompile_SetOrderIndependent(t *testing.T) {
	resourceA := layer.Resource{
		Key: "a", Convention: "do a", Priority: definition.PriorityGuidance,
		Selector: layer.Selector{ComponentTags: []string{"x", "y"}},
		Provenance: layer.Provenance{
			Owner: "team", Source: "doc", LastVerified: verifiedAt(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
		},
	}
	resourceB := layer.Resource{
		Key: "b", Convention: "do b", Priority: definition.PriorityGuidance,
		Selector: layer.Selector{ComponentTags: []string{"y", "x"}},
		Provenance: layer.Provenance{
			Owner: "team", Source: "doc", LastVerified: verifiedAt(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
		},
	}

	first := layer.LayerDocument{Resources: []layer.Resource{resourceA, resourceB}}
	second := layer.LayerDocument{Resources: []layer.Resource{resourceB, resourceA}}

	reqFirst := validPublishRequest()
	reqFirst.Document = first
	reqSecond := validPublishRequest()
	reqSecond.Document = second

	fieldsFirst, err := layer.Compile(draftDefinition("layer-1"), reqFirst)
	if err != nil {
		t.Fatalf("Compile(first): %v", err)
	}
	fieldsSecond, err := layer.Compile(draftDefinition("layer-1"), reqSecond)
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
	second.Document.Resources[0].Convention = "a genuinely different convention"

	fieldsFirst, err := layer.Compile(draftDefinition("layer-1"), first)
	if err != nil {
		t.Fatalf("Compile(first): %v", err)
	}
	fieldsSecond, err := layer.Compile(draftDefinition("layer-1"), second)
	if err != nil {
		t.Fatalf("Compile(second): %v", err)
	}
	if fieldsFirst.SourceHash() == fieldsSecond.SourceHash() {
		t.Fatal("SourceHash should differ when convention content genuinely differs")
	}
}

func TestCompile_SourceHashExcludesDependencies_CompiledHashIncludesThem(t *testing.T) {
	reqNoDeps := validPublishRequest()
	reqWithDeps := validPublishRequest()
	reqWithDeps.Dependencies = definition.DependencyManifest{Pins: []definition.DependencyPin{
		{Kind: definition.KindPolicy, DefinitionID: "policy-1", VersionID: "policy-1-v1"},
	}}

	fieldsNoDeps, err := layer.Compile(draftDefinition("layer-1"), reqNoDeps)
	if err != nil {
		t.Fatalf("Compile(no deps): %v", err)
	}
	fieldsWithDeps, err := layer.Compile(draftDefinition("layer-1"), reqWithDeps)
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
	def := draftDefinition("layer-1")

	first, err := layer.Compile(def, req)
	if err != nil {
		t.Fatalf("Compile (attempt 0): %v", err)
	}
	for i := 1; i < 10; i++ {
		next, err := layer.Compile(def, req)
		if err != nil {
			t.Fatalf("Compile (attempt %d): %v", i, err)
		}
		if next.SourceHash() != first.SourceHash() || next.CompiledHash() != first.CompiledHash() {
			t.Fatalf("attempt %d produced different hashes than attempt 0", i)
		}
	}
}

func TestCompileFrom_RejectsUnknownField(t *testing.T) {
	source := `{"resources": [], "typo_field": "x"}`
	if _, err := layer.CompileFrom(draftDefinition("layer-1"), []byte(source), authoring.FormatJSON, validPublishRequest()); err == nil {
		t.Fatal("CompileFrom should reject an unknown field")
	}
}

// TestCompile_Golden is V2-06's own "golden manifest tests" requirement
// applied to a single Layer's canonical source.
func TestCompile_Golden(t *testing.T) {
	fields, err := layer.Compile(draftDefinition("layer-1"), validPublishRequest())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	goldenPath := filepath.Join("testdata", "golden", "layer-example.json")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got := fields.CanonicalSource() + "\n"
	if got != string(want) {
		t.Fatalf("canonical source does not match golden fixture.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
