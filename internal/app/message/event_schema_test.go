package message

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

// TestMessageAppendedV1_GoldenFixtureDecodes is MessageAppended's own
// golden-fixture proof, mirroring internal/app/runtime/event_schema_test.go's
// own TestNodeRoutedV1_GoldenFixtureDecodes pattern (V6-00A).
func TestMessageAppendedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "message_appended_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(MessageAppendedEventType, MessageAppendedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", MessageAppendedEventType, MessageAppendedSchemaVersion, err)
	}
	want := messageAppendedEventPayload{WorkItemID: "work-item-1", Role: "USER", Sequence: 1, ContentArtifactID: "artifact-1"}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", MessageAppendedEventType, MessageAppendedSchemaVersion, got, want)
	}
}

// TestMessageAppendedV1_RealEventPayloadDecodes proves AppendMessage's own
// actual marshaled MessageAppended payload round-trips through the
// registered decoder.
func TestMessageAppendedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := messageAppendedEventPayload{WorkItemID: "work-item-9", Role: "ASSISTANT", Sequence: 3, ContentArtifactID: "artifact-9"}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(MessageAppendedEventType, MessageAppendedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}
