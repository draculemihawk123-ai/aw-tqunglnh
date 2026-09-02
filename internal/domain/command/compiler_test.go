package command_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/command"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

func draftDefinition(id string) command.CommandDefinition {
	fields, err := definition.Create(definition.CreateRequest{
		Kind: definition.KindCommand, Scope: definition.GlobalScope(), Name: "demo-command",
	})
	if err != nil {
		panic(err)
	}
	return command.CommandDefinition{ID: command.CommandDefinitionID(id), Fields: fields}
}

func validPublishRequest() command.PublishRequest {
	return command.PublishRequest{
		VersionID:     "command-v1",
		VersionNumber: 1,
		SchemaVersion: 1,
		Document:      validDocument(),
		PublishedBy:   "alice",
		PublishedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestCompile_ValidRequestSucceeds(t *testing.T) {
	fields, err := command.Compile(draftDefinition("command-1"), validPublishRequest())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if fields.Kind() != definition.KindCommand {
		t.Fatalf("Kind() = %v, want KindCommand", fields.Kind())
	}
	if fields.SourceHash() == "" || fields.CompiledHash() == "" {
		t.Fatal("Compile should produce non-empty SourceHash and CompiledHash")
	}
}

func TestCompile_RejectsInvalidDocument(t *testing.T) {
	req := validPublishRequest()
	req.Document.Argv = nil
	if _, err := command.Compile(draftDefinition("command-1"), req); err == nil {
		t.Fatal("Compile should reject an invalid document")
	}
}

func TestCompile_RejectsArchivedDefinition(t *testing.T) {
	def := draftDefinition("command-1")
	archived, err := definition.Archive(def.Fields)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	def.Fields = archived
	if _, err := command.Compile(def, validPublishRequest()); err == nil {
		t.Fatal("Compile should reject publishing a version for an archived definition")
	}
}

// TestCompile_ArgvOrderIsSignificant makes sure Compile did NOT treat
// argv as a set: two documents with the same argv elements in a
// different order must hash differently, since argument order is a
// genuine semantic difference for a Command.
func TestCompile_ArgvOrderIsSignificant(t *testing.T) {
	first := validDocument()
	second := validDocument()
	second.Argv[0], second.Argv[1] = second.Argv[1], second.Argv[0]
	// keep the reordered placeholder discoverable in the allowlist so
	// this stays a pure order difference, not also an unknown-placeholder
	// difference
	second.PlaceholderAllowlist = []string{"TARGET"}

	reqFirst := validPublishRequest()
	reqFirst.Document = first
	reqSecond := validPublishRequest()
	reqSecond.Document = second

	fieldsFirst, err := command.Compile(draftDefinition("command-1"), reqFirst)
	if err != nil {
		t.Fatalf("Compile(first): %v", err)
	}
	fieldsSecond, err := command.Compile(draftDefinition("command-1"), reqSecond)
	if err != nil {
		t.Fatalf("Compile(second): %v", err)
	}
	if fieldsFirst.SourceHash() == fieldsSecond.SourceHash() {
		t.Fatal("SourceHash should differ when argv order differs — argument order is semantically significant")
	}
}

// TestCompile_SetOrderIndependent is V2-05's own "hash tests"
// requirement: two documents differing only in the authored order of
// set-like fields (placeholderAllowlist, envAllowlist, secretRefs,
// compatibility.os) must compile to the identical hash.
func TestCompile_SetOrderIndependent(t *testing.T) {
	first := validDocument()
	first.EnvAllowlist = []string{"PATH", "HOME"}
	first.SecretRefs = []string{"a", "b"}

	second := validDocument()
	second.EnvAllowlist = []string{"HOME", "PATH"}
	second.SecretRefs = []string{"b", "a"}

	reqFirst := validPublishRequest()
	reqFirst.Document = first
	reqSecond := validPublishRequest()
	reqSecond.Document = second

	fieldsFirst, err := command.Compile(draftDefinition("command-1"), reqFirst)
	if err != nil {
		t.Fatalf("Compile(first): %v", err)
	}
	fieldsSecond, err := command.Compile(draftDefinition("command-1"), reqSecond)
	if err != nil {
		t.Fatalf("Compile(second): %v", err)
	}
	if fieldsFirst.SourceHash() != fieldsSecond.SourceHash() {
		t.Fatalf("SourceHash differs for the same content in different set order: %s vs %s",
			fieldsFirst.SourceHash(), fieldsSecond.SourceHash())
	}
}

// TestCompile_SourceHashExcludesDependencies_CompiledHashIncludesThem is
// ADR-012's own distinction, verified the same way Block's own test
// verifies it.
func TestCompile_SourceHashExcludesDependencies_CompiledHashIncludesThem(t *testing.T) {
	reqNoDeps := validPublishRequest()
	reqWithDeps := validPublishRequest()
	reqWithDeps.Dependencies = definition.DependencyManifest{Pins: []definition.DependencyPin{
		{Kind: definition.KindPolicy, DefinitionID: "policy-2", VersionID: "policy-2-v1"},
	}}

	fieldsNoDeps, err := command.Compile(draftDefinition("command-1"), reqNoDeps)
	if err != nil {
		t.Fatalf("Compile(no deps): %v", err)
	}
	fieldsWithDeps, err := command.Compile(draftDefinition("command-1"), reqWithDeps)
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
	def := draftDefinition("command-1")

	first, err := command.Compile(def, req)
	if err != nil {
		t.Fatalf("Compile (attempt 0): %v", err)
	}
	for i := 1; i < 10; i++ {
		next, err := command.Compile(def, req)
		if err != nil {
			t.Fatalf("Compile (attempt %d): %v", i, err)
		}
		if next.SourceHash() != first.SourceHash() || next.CompiledHash() != first.CompiledHash() {
			t.Fatalf("attempt %d produced different hashes than attempt 0", i)
		}
	}
}

func TestCompileFrom_RejectsUnknownField(t *testing.T) {
	source := `{"executable": {"ownerVersionId": "x", "resourceKey": "y", "contentHash": "z"}, "typo_field": "x"}`
	if _, err := command.CompileFrom(draftDefinition("command-1"), []byte(source), authoring.FormatJSON, validPublishRequest()); err == nil {
		t.Fatal("CompileFrom should reject an unknown field")
	}
}

func TestCompileFrom_JSONAndYAML_SameContent_SameHash(t *testing.T) {
	jsonSource := `{
		"executable": {"ownerVersionId": "skill-1-v1", "resourceKey": "scripts/run.sh", "contentHash": "sha256:abc"},
		"argv": [{"kind": "LITERAL", "value": "run.sh"}, {"kind": "PLACEHOLDER", "value": "TARGET"}],
		"placeholderAllowlist": ["TARGET"],
		"cwdRepositoryTarget": "primary",
		"compatibility": {"os": ["linux"]},
		"envAllowlist": ["PATH"],
		"networkAccess": "NONE",
		"secretRefs": ["deploy-token"],
		"timeoutSeconds": 60,
		"output": {"captureStdout": true, "captureStderr": true, "maxOutputBytes": 1048576},
		"policyRefs": [{"kind": "POLICY", "definitionId": "policy-1", "versionId": "policy-1-v1"}]
	}`
	yamlSource := "executable:\n  ownerVersionId: skill-1-v1\n  resourceKey: scripts/run.sh\n  contentHash: sha256:abc\n" +
		"argv:\n  - kind: LITERAL\n    value: run.sh\n  - kind: PLACEHOLDER\n    value: TARGET\n" +
		"placeholderAllowlist: [TARGET]\n" +
		"cwdRepositoryTarget: primary\n" +
		"compatibility:\n  os: [linux]\n" +
		"envAllowlist: [PATH]\n" +
		"networkAccess: NONE\n" +
		"secretRefs: [deploy-token]\n" +
		"timeoutSeconds: 60\n" +
		"output:\n  captureStdout: true\n  captureStderr: true\n  maxOutputBytes: 1048576\n" +
		"policyRefs:\n  - kind: POLICY\n    definitionId: policy-1\n    versionId: policy-1-v1\n"

	fieldsJSON, err := command.CompileFrom(draftDefinition("command-1"), []byte(jsonSource), authoring.FormatJSON, validPublishRequest())
	if err != nil {
		t.Fatalf("CompileFrom(json): %v", err)
	}
	fieldsYAML, err := command.CompileFrom(draftDefinition("command-1"), []byte(yamlSource), authoring.FormatYAML, validPublishRequest())
	if err != nil {
		t.Fatalf("CompileFrom(yaml): %v", err)
	}
	if fieldsJSON.SourceHash() != fieldsYAML.SourceHash() {
		t.Fatalf("jsonHash = %s, yamlHash = %s, want identical for the same semantic content",
			fieldsJSON.SourceHash(), fieldsYAML.SourceHash())
	}
}

// TestCompile_Golden is V2-05's own "hash tests" requirement: a
// representative CommandDocument's canonical SourceHash is compared
// against a checked-in fixture.
func TestCompile_Golden(t *testing.T) {
	fields, err := command.Compile(draftDefinition("command-1"), validPublishRequest())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	goldenPath := filepath.Join("testdata", "golden", "command-example.json")
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got := fields.CanonicalSource() + "\n"
	if got != string(want) {
		t.Fatalf("canonical source does not match golden fixture.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
