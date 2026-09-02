package skill_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
	"github.com/taQuangLing/agent-workflow/internal/domain/skill"
)

func draftDefinition(id string) skill.SkillDefinition {
	fields, err := definition.Create(definition.CreateRequest{
		Kind: definition.KindSkill, Scope: definition.GlobalScope(), Name: "demo-skill",
	})
	if err != nil {
		panic(err)
	}
	return skill.SkillDefinition{ID: skill.SkillDefinitionID(id), Fields: fields}
}

func validPublishRequest() skill.PublishRequest {
	return skill.PublishRequest{
		VersionID:     "skill-v1",
		VersionNumber: 1,
		SchemaVersion: 1,
		Document:      validDocument(),
		PublishedBy:   "alice",
		PublishedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestCompile_ValidRequestSucceeds(t *testing.T) {
	fields, err := skill.Compile(draftDefinition("skill-1"), validPublishRequest())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if fields.Kind() != definition.KindSkill {
		t.Fatalf("Kind() = %v, want KindSkill", fields.Kind())
	}
	if fields.SourceHash() == "" || fields.CompiledHash() == "" {
		t.Fatal("Compile should produce non-empty SourceHash and CompiledHash")
	}
}

func TestCompile_RejectsInvalidDocument(t *testing.T) {
	req := validPublishRequest()
	req.Document.Resources = nil
	if _, err := skill.Compile(draftDefinition("skill-1"), req); err == nil {
		t.Fatal("Compile should reject an invalid document")
	}
}

func TestCompile_RejectsArchivedDefinition(t *testing.T) {
	def := draftDefinition("skill-1")
	archived, err := definition.Archive(def.Fields)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	def.Fields = archived
	if _, err := skill.Compile(def, validPublishRequest()); err == nil {
		t.Fatal("Compile should reject publishing a version for an archived definition")
	}
}

func TestCompile_RejectsMissingPublisherOrTimestamp(t *testing.T) {
	req := validPublishRequest()
	req.PublishedBy = ""
	if _, err := skill.Compile(draftDefinition("skill-1"), req); err == nil {
		t.Fatal("Compile should reject a missing publisher")
	}

	req = validPublishRequest()
	req.PublishedAt = time.Time{}
	if _, err := skill.Compile(draftDefinition("skill-1"), req); err == nil {
		t.Fatal("Compile should reject a missing publish timestamp")
	}
}

// TestCompile_SetOrderIndependent is V2-06's own "hash tests"
// requirement: two documents that only differ in the authored order of
// set-like fields (resources themselves, and each resource's selector
// dimensions) must compile to the identical SourceHash and CompiledHash.
func TestCompile_SetOrderIndependent(t *testing.T) {
	resourceA := skill.Resource{
		Key: "a", Instruction: "do a", Priority: definition.PriorityGuidance,
		Selector: skill.Selector{ComponentTags: []string{"x", "y"}},
		Provenance: skill.Provenance{
			Owner: "team", Source: "doc", LastVerified: verifiedAt(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
		},
	}
	resourceB := skill.Resource{
		Key: "b", Instruction: "do b", Priority: definition.PriorityGuidance,
		Selector: skill.Selector{ComponentTags: []string{"y", "x"}},
		Provenance: skill.Provenance{
			Owner: "team", Source: "doc", LastVerified: verifiedAt(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
		},
	}

	first := skill.SkillDocument{Resources: []skill.Resource{resourceA, resourceB}}
	second := skill.SkillDocument{Resources: []skill.Resource{resourceB, resourceA}}

	reqFirst := validPublishRequest()
	reqFirst.Document = first
	reqSecond := validPublishRequest()
	reqSecond.Document = second

	fieldsFirst, err := skill.Compile(draftDefinition("skill-1"), reqFirst)
	if err != nil {
		t.Fatalf("Compile(first): %v", err)
	}
	fieldsSecond, err := skill.Compile(draftDefinition("skill-1"), reqSecond)
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
	second.Document.Resources[0].Instruction = "a genuinely different instruction"

	fieldsFirst, err := skill.Compile(draftDefinition("skill-1"), first)
	if err != nil {
		t.Fatalf("Compile(first): %v", err)
	}
	fieldsSecond, err := skill.Compile(draftDefinition("skill-1"), second)
	if err != nil {
		t.Fatalf("Compile(second): %v", err)
	}
	if fieldsFirst.SourceHash() == fieldsSecond.SourceHash() {
		t.Fatal("SourceHash should differ when instruction content genuinely differs")
	}
}

// TestCompile_SourceHashExcludesDependencies_CompiledHashIncludesThem is
// ADR-012's own distinction: SourceHash covers authored content only,
// CompiledHash covers authored content plus the dependency manifest.
func TestCompile_SourceHashExcludesDependencies_CompiledHashIncludesThem(t *testing.T) {
	reqNoDeps := validPublishRequest()
	reqWithDeps := validPublishRequest()
	reqWithDeps.Dependencies = definition.DependencyManifest{Pins: []definition.DependencyPin{
		{Kind: definition.KindPolicy, DefinitionID: "policy-1", VersionID: "policy-1-v1"},
	}}

	fieldsNoDeps, err := skill.Compile(draftDefinition("skill-1"), reqNoDeps)
	if err != nil {
		t.Fatalf("Compile(no deps): %v", err)
	}
	fieldsWithDeps, err := skill.Compile(draftDefinition("skill-1"), reqWithDeps)
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
	def := draftDefinition("skill-1")

	first, err := skill.Compile(def, req)
	if err != nil {
		t.Fatalf("Compile (attempt 0): %v", err)
	}
	for i := 1; i < 10; i++ {
		next, err := skill.Compile(def, req)
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
	if _, err := skill.CompileFrom(draftDefinition("skill-1"), []byte(source), authoring.FormatJSON, validPublishRequest()); err == nil {
		t.Fatal("CompileFrom should reject an unknown field")
	}
}

// TestCompile_Golden is V2-06's own "golden manifest tests" requirement
// applied to a single Skill's canonical source: a representative
// SkillDocument's canonical SourceHash is compared against a checked-in
// fixture, so a change to Skill's canonicalization behavior itself (not
// just its determinism) is caught.
func TestCompile_Golden(t *testing.T) {
	fields, err := skill.Compile(draftDefinition("skill-1"), validPublishRequest())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	goldenPath := filepath.Join("testdata", "golden", "skill-example.json")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got := fields.CanonicalSource() + "\n"
	if got != string(want) {
		t.Fatalf("canonical source does not match golden fixture.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
