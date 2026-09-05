package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

// TestNodeRoutedV1_GoldenFixtureDecodes is NODE_ROUTED's own golden-fixture
// proof, mirroring internal/app/eventschema's own
// TestGoldenFixtures_DecodeEveryRegisteredVersion pattern (V1-07A) but for
// this package's real, production event rather than that package's
// illustrative ProjectCreated example.
func TestNodeRoutedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "node_routed_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(NodeRoutedEventType, NodeRoutedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", NodeRoutedEventType, NodeRoutedSchemaVersion, err)
	}
	want := nodeRoutedEventPayload{
		RunID: "run-1", WorkItemID: "work-item-1", NodeRunID: "node-run-1", NodeKey: "start",
		SelectedOutcome: "next", NextNodeRunID: "node-run-2", NextNodeKey: "end", JobID: "job-1",
	}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", NodeRoutedEventType, NodeRoutedSchemaVersion, got, want)
	}
}

// TestNodeRoutedV1_RealEventPayloadDecodes proves AdvanceRun's own actual
// marshaled NODE_ROUTED payload (not just the static golden fixture) round-
// trips through the registered decoder — catching a drift between the
// producer's own struct and the registered Decoder that the golden fixture
// alone, being hand-written, could not.
func TestNodeRoutedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := nodeRoutedEventPayload{
		RunID: "run-9", WorkItemID: "work-item-9", NodeRunID: "node-run-9", NodeKey: "router",
		SelectedOutcome: "go", NextNodeRunID: "node-run-10", NextNodeKey: "end", JobID: "job-9",
	}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(NodeRoutedEventType, NodeRoutedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}

// TestNodeScheduledV1_GoldenFixtureDecodes is NODE_SCHEDULED's own
// golden-fixture proof, mirroring TestNodeRoutedV1_GoldenFixtureDecodes.
func TestNodeScheduledV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "node_scheduled_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(NodeScheduledEventType, NodeScheduledSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", NodeScheduledEventType, NodeScheduledSchemaVersion, err)
	}
	want := nodeScheduledEventPayload{
		RunID: "run-1", WorkItemID: "work-item-1", NodeRunID: "node-run-1", NodeKey: "implement",
		ExecutorKind: "AGENT", AttemptID: "attempt-1", ExecutionProfileHash: "sha256:profile-1", JobID: "job-1",
	}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", NodeScheduledEventType, NodeScheduledSchemaVersion, got, want)
	}
}

// TestNodeScheduledV1_RealEventPayloadDecodes proves
// ScheduleExecutableNodeRun's own actual marshaled NODE_SCHEDULED payload
// (not just the static golden fixture) round-trips through the registered
// decoder, mirroring TestNodeRoutedV1_RealEventPayloadDecodes.
func TestNodeScheduledV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := nodeScheduledEventPayload{
		RunID: "run-9", WorkItemID: "work-item-9", NodeRunID: "node-run-9", NodeKey: "implement",
		ExecutorKind: "COMMAND", AttemptID: "attempt-9", ExecutionProfileHash: "sha256:profile-9", JobID: "job-9",
	}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(NodeScheduledEventType, NodeScheduledSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}
