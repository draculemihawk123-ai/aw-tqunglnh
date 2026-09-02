package engineeringpack_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/engineeringpack"
)

func draftDefinition(id string) engineeringpack.EngineeringPackDefinition {
	fields, err := definition.Create(definition.CreateRequest{
		Kind: definition.KindEngineeringPack, Scope: definition.GlobalScope(), Name: "demo-pack",
	})
	if err != nil {
		panic(err)
	}
	return engineeringpack.EngineeringPackDefinition{ID: engineeringpack.EngineeringPackDefinitionID(id), Fields: fields}
}

func validPublishRequest() engineeringpack.PublishRequest {
	return engineeringpack.PublishRequest{
		VersionID:     "pack-v1",
		VersionNumber: 1,
		SchemaVersion: 1,
		Document:      validDocument(),
		PublishedBy:   "alice",
		PublishedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestCompile_ValidRequestSucceeds(t *testing.T) {
	fields, err := engineeringpack.Compile(draftDefinition("pack-1"), validPublishRequest())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if fields.Kind() != definition.KindEngineeringPack {
		t.Fatalf("Kind() = %v, want KindEngineeringPack", fields.Kind())
	}
	if fields.SourceHash() == "" || fields.CompiledHash() == "" {
		t.Fatal("Compile should produce non-empty SourceHash and CompiledHash")
	}
}

func TestCompile_RejectsInvalidDocument(t *testing.T) {
	req := validPublishRequest()
	req.Document.Dependencies = nil
	if _, err := engineeringpack.Compile(draftDefinition("pack-1"), req); err == nil {
		t.Fatal("Compile should reject an invalid document")
	}
}

// TestCompile_RejectsSelfDependency is this package's own local cycle
// check: a pack cannot pin its own DefinitionID as an Engineering Pack
// dependency.
func TestCompile_RejectsSelfDependency(t *testing.T) {
	def := draftDefinition("pack-1")
	req := validPublishRequest()
	req.Document.Dependencies = append(req.Document.Dependencies, definition.DependencyPin{
		Kind: definition.KindEngineeringPack, DefinitionID: "pack-1", VersionID: "pack-1-v0",
	})
	if _, err := engineeringpack.Compile(def, req); err == nil {
		t.Fatal("Compile should reject a pack that depends on its own definition id")
	}
}

func TestCompile_RejectsArchivedDefinition(t *testing.T) {
	def := draftDefinition("pack-1")
	archived, err := definition.Archive(def.Fields)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	def.Fields = archived
	if _, err := engineeringpack.Compile(def, validPublishRequest()); err == nil {
		t.Fatal("Compile should reject publishing a version for an archived definition")
	}
}

func TestCompile_RejectsMissingPublisherOrTimestamp(t *testing.T) {
	req := validPublishRequest()
	req.PublishedBy = ""
	if _, err := engineeringpack.Compile(draftDefinition("pack-1"), req); err == nil {
		t.Fatal("Compile should reject a missing publisher")
	}

	req = validPublishRequest()
	req.PublishedAt = time.Time{}
	if _, err := engineeringpack.Compile(draftDefinition("pack-1"), req); err == nil {
		t.Fatal("Compile should reject a missing publish timestamp")
	}
}

func TestCompile_SetOrderIndependent(t *testing.T) {
	first := engineeringpack.EngineeringPackDocument{Dependencies: []definition.DependencyPin{
		{Kind: definition.KindSkill, DefinitionID: "skill-1", VersionID: "skill-1-v1"},
		{Kind: definition.KindLayer, DefinitionID: "layer-1", VersionID: "layer-1-v1"},
	}}
	second := engineeringpack.EngineeringPackDocument{Dependencies: []definition.DependencyPin{
		{Kind: definition.KindLayer, DefinitionID: "layer-1", VersionID: "layer-1-v1"},
		{Kind: definition.KindSkill, DefinitionID: "skill-1", VersionID: "skill-1-v1"},
	}}

	reqFirst := validPublishRequest()
	reqFirst.Document = first
	reqSecond := validPublishRequest()
	reqSecond.Document = second

	fieldsFirst, err := engineeringpack.Compile(draftDefinition("pack-1"), reqFirst)
	if err != nil {
		t.Fatalf("Compile(first): %v", err)
	}
	fieldsSecond, err := engineeringpack.Compile(draftDefinition("pack-1"), reqSecond)
	if err != nil {
		t.Fatalf("Compile(second): %v", err)
	}
	if fieldsFirst.SourceHash() != fieldsSecond.SourceHash() {
		t.Fatalf("SourceHash differs for the same dependencies in different order: %s vs %s",
			fieldsFirst.SourceHash(), fieldsSecond.SourceHash())
	}
}

func TestCompile_DifferentContentHashesDiffer(t *testing.T) {
	first := validPublishRequest()
	second := validPublishRequest()
	second.Document.Dependencies[0].VersionID = "skill-1-v2"

	fieldsFirst, err := engineeringpack.Compile(draftDefinition("pack-1"), first)
	if err != nil {
		t.Fatalf("Compile(first): %v", err)
	}
	fieldsSecond, err := engineeringpack.Compile(draftDefinition("pack-1"), second)
	if err != nil {
		t.Fatalf("Compile(second): %v", err)
	}
	if fieldsFirst.SourceHash() == fieldsSecond.SourceHash() {
		t.Fatal("SourceHash should differ when a pinned version genuinely differs")
	}
}

func TestCompile_SourceHashExcludesDependencies_CompiledHashIncludesThem(t *testing.T) {
	reqNoDeps := validPublishRequest()
	reqWithDeps := validPublishRequest()
	reqWithDeps.Dependencies = definition.DependencyManifest{Pins: []definition.DependencyPin{
		{Kind: definition.KindSkill, DefinitionID: "skill-1", VersionID: "skill-1-v1"},
	}}

	fieldsNoDeps, err := engineeringpack.Compile(draftDefinition("pack-1"), reqNoDeps)
	if err != nil {
		t.Fatalf("Compile(no deps): %v", err)
	}
	fieldsWithDeps, err := engineeringpack.Compile(draftDefinition("pack-1"), reqWithDeps)
	if err != nil {
		t.Fatalf("Compile(with deps): %v", err)
	}
	if fieldsNoDeps.SourceHash() != fieldsWithDeps.SourceHash() {
		t.Fatalf("SourceHash should be identical regardless of the resolved dependency manifest: %s vs %s",
			fieldsNoDeps.SourceHash(), fieldsWithDeps.SourceHash())
	}
	if fieldsNoDeps.CompiledHash() == fieldsWithDeps.CompiledHash() {
		t.Fatal("CompiledHash should differ once a resolved dependency manifest is added")
	}
}

func TestCompile_Deterministic_RepeatedCalls(t *testing.T) {
	req := validPublishRequest()
	def := draftDefinition("pack-1")

	first, err := engineeringpack.Compile(def, req)
	if err != nil {
		t.Fatalf("Compile (attempt 0): %v", err)
	}
	for i := 1; i < 10; i++ {
		next, err := engineeringpack.Compile(def, req)
		if err != nil {
			t.Fatalf("Compile (attempt %d): %v", i, err)
		}
		if next.SourceHash() != first.SourceHash() || next.CompiledHash() != first.CompiledHash() {
			t.Fatalf("attempt %d produced different hashes than attempt 0", i)
		}
	}
}

func TestCompileFrom_RejectsUnknownField(t *testing.T) {
	source := `{"dependencies": [], "typo_field": "x"}`
	if _, err := engineeringpack.CompileFrom(draftDefinition("pack-1"), []byte(source), authoring.FormatJSON, validPublishRequest()); err == nil {
		t.Fatal("CompileFrom should reject an unknown field")
	}
}

// TestCompile_Golden is V2-06's own "golden manifest tests" requirement
// applied to a single Engineering Pack's canonical source.
func TestCompile_Golden(t *testing.T) {
	fields, err := engineeringpack.Compile(draftDefinition("pack-1"), validPublishRequest())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	goldenPath := filepath.Join("testdata", "golden", "engineeringpack-example.json")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got := fields.CanonicalSource() + "\n"
	if got != string(want) {
		t.Fatalf("canonical source does not match golden fixture.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
