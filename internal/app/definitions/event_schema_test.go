package definitions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

// TestDefinitionCreatedV1_GoldenFixtureDecodes is DefinitionCreated's own
// golden-fixture proof, mirroring internal/app/runtime/event_schema_test.go's
// own TestNodeRoutedV1_GoldenFixtureDecodes pattern (V6-00A).
func TestDefinitionCreatedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "definition_created_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(DefinitionCreatedEventType, DefinitionCreatedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", DefinitionCreatedEventType, DefinitionCreatedSchemaVersion, err)
	}
	want := definitionCreatedEventPayload{DefinitionID: "skill-def-1", Kind: "SKILL", Name: "skill one"}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", DefinitionCreatedEventType, DefinitionCreatedSchemaVersion, got, want)
	}
}

// TestDefinitionCreatedV1_RealEventPayloadDecodes proves CreateDefinition's
// own actual marshaled DefinitionCreated payload round-trips through the
// registered decoder.
func TestDefinitionCreatedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := definitionCreatedEventPayload{DefinitionID: "command-def-9", Kind: "COMMAND", Name: "command nine"}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(DefinitionCreatedEventType, DefinitionCreatedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}

// TestDefinitionVersionPublishedV1_GoldenFixtureDecodes is
// DefinitionVersionPublished's own golden-fixture proof.
func TestDefinitionVersionPublishedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "definition_version_published_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(DefinitionVersionPublishedEventType, DefinitionVersionPublishedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", DefinitionVersionPublishedEventType, DefinitionVersionPublishedSchemaVersion, err)
	}
	want := definitionVersionPublishedEventPayload{
		DefinitionID: "skill-def-1", VersionID: "skill-v1", Kind: "SKILL", VersionNumber: 1, CompiledHash: "sha256:abc123",
	}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", DefinitionVersionPublishedEventType, DefinitionVersionPublishedSchemaVersion, got, want)
	}
}

// TestDefinitionVersionPublishedV1_RealEventPayloadDecodes proves
// PublishDefinitionVersion's own actual marshaled
// DefinitionVersionPublished payload round-trips through the registered
// decoder.
func TestDefinitionVersionPublishedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := definitionVersionPublishedEventPayload{
		DefinitionID: "workflow-def-9", VersionID: "workflow-v9", Kind: "WORKFLOW", VersionNumber: 3, CompiledHash: "sha256:def456",
	}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(DefinitionVersionPublishedEventType, DefinitionVersionPublishedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}
