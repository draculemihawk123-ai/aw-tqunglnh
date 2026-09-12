package artifactsweep

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

// TestArtifactSweepCompletedV1_GoldenFixtureDecodes is
// ArtifactSweepCompleted's own golden-fixture proof, mirroring
// internal/app/runtime/event_schema_test.go's own
// TestNodeForkedV1_GoldenFixtureDecodes pattern (V6-00A) — compared with
// reflect.DeepEqual since SweepManifest carries a slice field (Groups)
// Go's own == cannot compare.
func TestArtifactSweepCompletedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "artifact_sweep_completed_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(ArtifactSweepCompletedEventType, ArtifactSweepCompletedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", ArtifactSweepCompletedEventType, ArtifactSweepCompletedSchemaVersion, err)
	}
	want := SweepManifest{
		Generation: 1, DryRun: true,
		Groups: []LocatorGroupResult{{Locator: "sha256:abc123", ArtifactIDs: []string{"artifact-1"}, Decision: "WOULD_PURGE"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", ArtifactSweepCompletedEventType, ArtifactSweepCompletedSchemaVersion, got, want)
	}
}

// TestArtifactSweepCompletedV1_RealEventPayloadDecodes proves
// recordSweepManifestTx's own actual marshaled ArtifactSweepCompleted
// payload round-trips through the registered decoder.
func TestArtifactSweepCompletedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := SweepManifest{
		Generation: 9, DryRun: false,
		Groups: []LocatorGroupResult{{Locator: "sha256:def456", ArtifactIDs: []string{"artifact-9a", "artifact-9b"}, Decision: "PURGED", BytesFreed: 1024}},
	}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(ArtifactSweepCompletedEventType, ArtifactSweepCompletedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !reflect.DeepEqual(got, produced) {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}
