package agentprofile_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/domain/agentprofile"
	"github.com/taQuangLing/agent-workflow/internal/domain/authoring"
	"github.com/taQuangLing/agent-workflow/internal/domain/definition"
)

// V9-05 (gap G5): AgentProfileDocument.EnvAllowlist — publishing, canonical
// form and hash stability. Validation rules are in validate_test.go.

// The hashes below were computed on origin/master (b01ca55), BEFORE the
// EnvAllowlist field existed, for validDocument() published through
// validPublishRequest(); the compiled one adds the single context-policy
// dependency pin. A profile that never declares the field must keep
// producing exactly these, so every AgentProfileVersion published before
// V9-05 keeps its SourceHash and CompiledHash (and, through the latter, the
// Executor.CompiledHash every pinned ResolvedExecutionProfileV1 carries).
const (
	preV905SourceHash       = "sha256:6b4766a560b03121ac47c0df52116e7a3c5840fc9271d61202d3b35e6bded899"
	preV905CompiledHashDeps = "sha256:c5b4ea467eaf3cd67ea59c0907d8ab9f1ca6daf023f88a27778ed9822f7be3fa"
)

func compileWithDependencies(t *testing.T, doc agentprofile.AgentProfileDocument) definition.VersionFields {
	t.Helper()
	req := validPublishRequest()
	req.Document = doc
	req.Dependencies = definition.DependencyManifest{Pins: []definition.DependencyPin{
		{Kind: definition.KindPolicy, DefinitionID: "policy-context-1", VersionID: "policy-context-1-v1"},
	}}
	fields, err := agentprofile.Compile(draftDefinition("agent-profile-1"), req)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return fields
}

// TestCompile_ProfileWithoutEnvAllowlist_HashesExactlyAsBeforeV905 is the
// backward-compatibility pin: absent, nil and explicitly empty all encode,
// compile and hash byte-for-byte as they did before the field existed.
func TestCompile_ProfileWithoutEnvAllowlist_HashesExactlyAsBeforeV905(t *testing.T) {
	for name, list := range map[string][]string{"absent": nil, "explicitly empty": {}} {
		t.Run(name, func(t *testing.T) {
			doc := validDocument()
			doc.EnvAllowlist = list
			fields := compileWithDependencies(t, doc)
			if fields.SourceHash() != preV905SourceHash {
				t.Fatalf("SourceHash = %s, want the pre-V9-05 %s", fields.SourceHash(), preV905SourceHash)
			}
			if fields.CompiledHash() != preV905CompiledHashDeps {
				t.Fatalf("CompiledHash = %s, want the pre-V9-05 %s", fields.CompiledHash(), preV905CompiledHashDeps)
			}
			if strings.Contains(fields.CanonicalSource(), "envAllowlist") || strings.Contains(fields.CompiledSnapshot(), "envAllowlist") {
				t.Fatalf("a profile without the field must not encode it:\n%s\n%s", fields.CanonicalSource(), fields.CompiledSnapshot())
			}
		})
	}
}

// TestCompile_EnvAllowlist_SortedInCanonicalAndCompiledForm: the list is a
// set, so two authors listing the same names in any order publish the same
// bytes and hashes, and both the canonical source and the compiled snapshot
// (what the scheduler decodes) carry the SORTED list.
func TestCompile_EnvAllowlist_SortedInCanonicalAndCompiledForm(t *testing.T) {
	first := validDocument()
	first.EnvAllowlist = []string{"PATH", "AW_TOKEN_NAME", "HOME"}
	second := validDocument()
	second.EnvAllowlist = []string{"HOME", "PATH", "AW_TOKEN_NAME"}

	fieldsFirst := compileWithDependencies(t, first)
	fieldsSecond := compileWithDependencies(t, second)
	if fieldsFirst.SourceHash() != fieldsSecond.SourceHash() || fieldsFirst.CompiledHash() != fieldsSecond.CompiledHash() {
		t.Fatalf("hashes differ for the same names in a different order:\n%s %s\n%s %s",
			fieldsFirst.SourceHash(), fieldsFirst.CompiledHash(), fieldsSecond.SourceHash(), fieldsSecond.CompiledHash())
	}
	if fieldsFirst.CanonicalSource() != fieldsSecond.CanonicalSource() || fieldsFirst.CompiledSnapshot() != fieldsSecond.CompiledSnapshot() {
		t.Fatal("canonical source / compiled snapshot differ for the same names in a different order")
	}
	const sorted = `"envAllowlist":["AW_TOKEN_NAME","HOME","PATH"]`
	if !strings.Contains(fieldsFirst.CanonicalSource(), sorted) {
		t.Fatalf("canonical source does not carry the sorted list %s:\n%s", sorted, fieldsFirst.CanonicalSource())
	}
	if !strings.Contains(fieldsFirst.CompiledSnapshot(), sorted) {
		t.Fatalf("compiled snapshot does not carry the sorted list %s:\n%s", sorted, fieldsFirst.CompiledSnapshot())
	}
}

// TestCompile_EnvAllowlist_ChangesTheHashes: declaring (or changing) the list
// is a real change of the profile, so it must produce a new version content.
func TestCompile_EnvAllowlist_ChangesTheHashes(t *testing.T) {
	without := compileWithDependencies(t, validDocument())
	withHome := validDocument()
	withHome.EnvAllowlist = []string{"HOME"}
	withPath := validDocument()
	withPath.EnvAllowlist = []string{"PATH"}

	fieldsHome := compileWithDependencies(t, withHome)
	fieldsPath := compileWithDependencies(t, withPath)
	if fieldsHome.SourceHash() == without.SourceHash() || fieldsHome.CompiledHash() == without.CompiledHash() {
		t.Fatal("declaring envAllowlist did not change the hashes")
	}
	if fieldsHome.SourceHash() == fieldsPath.SourceHash() || fieldsHome.CompiledHash() == fieldsPath.CompiledHash() {
		t.Fatal("two different envAllowlists produced the same hashes")
	}
}

func TestCompile_RejectsInvalidEnvAllowlist(t *testing.T) {
	for name, list := range map[string][]string{
		"a NAME=value pair": {"HOME=/root"},
		"a duplicate":       {"HOME", "HOME"},
		"an empty name":     {""},
	} {
		t.Run(name, func(t *testing.T) {
			req := validPublishRequest()
			req.Document.EnvAllowlist = list
			if _, err := agentprofile.Compile(draftDefinition("agent-profile-1"), req); err == nil {
				t.Fatal("Compile should refuse to publish an invalid envAllowlist")
			}
		})
	}
}

// TestCompileFrom_EnvAllowlist_JSONAndYAML_SameContent_SameHash: the field is
// authored the same way in both formats, decoded strictly (the key is known),
// and a typo of the key is still an unknown field, not silently dropped.
func TestCompileFrom_EnvAllowlist_JSONAndYAML_SameContent_SameHash(t *testing.T) {
	jsonSource := `{
		"providerKey": "claude",
		"model": "fake-model",
		"toolRefs": ["read_file", "write_file"],
		"contextPolicyRef": {"kind": "POLICY", "definitionId": "policy-context-1", "versionId": "policy-context-1-v1"},
		"compatibility": {"os": ["windows", "linux"], "toolchain": ["git"]},
		"requiredCapabilities": ["INTEGRATION_MULTI_REPOSITORY_WRITE"],
		"budget": {"maxTokens": 100000},
		"envAllowlist": ["PATH", "HOME"]
	}`
	yamlSource := "providerKey: claude\n" +
		"model: fake-model\n" +
		"toolRefs: [read_file, write_file]\n" +
		"contextPolicyRef:\n  kind: POLICY\n  definitionId: policy-context-1\n  versionId: policy-context-1-v1\n" +
		"compatibility:\n  os: [windows, linux]\n  toolchain: [git]\n" +
		"requiredCapabilities: [INTEGRATION_MULTI_REPOSITORY_WRITE]\n" +
		"budget:\n  maxTokens: 100000\n" +
		"envAllowlist:\n  - HOME\n  - PATH\n"

	fieldsJSON, err := agentprofile.CompileFrom(draftDefinition("agent-profile-1"), []byte(jsonSource), authoring.FormatJSON, validPublishRequest())
	if err != nil {
		t.Fatalf("CompileFrom(json): %v", err)
	}
	fieldsYAML, err := agentprofile.CompileFrom(draftDefinition("agent-profile-1"), []byte(yamlSource), authoring.FormatYAML, validPublishRequest())
	if err != nil {
		t.Fatalf("CompileFrom(yaml): %v", err)
	}
	if fieldsJSON.SourceHash() != fieldsYAML.SourceHash() {
		t.Fatalf("jsonHash = %s, yamlHash = %s, want identical for the same semantic content", fieldsJSON.SourceHash(), fieldsYAML.SourceHash())
	}
	if !strings.Contains(fieldsJSON.CanonicalSource(), `"envAllowlist":["HOME","PATH"]`) {
		t.Fatalf("canonical source does not carry the sorted list:\n%s", fieldsJSON.CanonicalSource())
	}

	typo := strings.Replace(jsonSource, `"envAllowlist"`, `"envAllowlst"`, 1)
	if _, err := agentprofile.CompileFrom(draftDefinition("agent-profile-1"), []byte(typo), authoring.FormatJSON, validPublishRequest()); err == nil {
		t.Fatal("a misspelt envAllowlist key must be rejected as an unknown field, not silently ignored")
	}
}

// TestCompile_Golden_EnvAllowlist pins the canonical encoding of a profile that
// declares the field, next to the pre-existing golden of one that does not.
func TestCompile_Golden_EnvAllowlist(t *testing.T) {
	req := validPublishRequest()
	req.Document.Model = "fake-model"
	req.Document.EnvAllowlist = []string{"PATH", "HOME"}
	fields, err := agentprofile.Compile(draftDefinition("agent-profile-1"), req)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "golden", "agent-profile-env-allowlist-example.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	if got := fields.CanonicalSource() + "\n"; got != string(want) {
		t.Fatalf("canonical source does not match golden fixture.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
