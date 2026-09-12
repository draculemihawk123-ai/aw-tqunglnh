package workspacerelease

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

// TestWorkspaceSetReleaseRequestedV1_GoldenFixtureDecodes is
// WorkspaceSetReleaseRequested's own golden-fixture proof, mirroring
// internal/app/runtime/event_schema_test.go's own
// TestNodeRoutedV1_GoldenFixtureDecodes pattern (V6-00A).
func TestWorkspaceSetReleaseRequestedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "workspace_set_release_requested_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(WorkspaceSetReleaseRequestedEventType, WorkspaceSetReleaseRequestedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", WorkspaceSetReleaseRequestedEventType, WorkspaceSetReleaseRequestedSchemaVersion, err)
	}
	want := workspaceSetReleaseRequestedEventPayload{
		WorkspaceSetID: "workspace-set-1", FamilyID: "family-1", ProjectID: "project-1",
		State: "RELEASING", ReleaseJobID: "job-1",
	}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", WorkspaceSetReleaseRequestedEventType, WorkspaceSetReleaseRequestedSchemaVersion, got, want)
	}
}

// TestWorkspaceSetReleaseRequestedV1_RealEventPayloadDecodes proves
// RequestWorkspaceSetRelease's own actual marshaled
// WorkspaceSetReleaseRequested payload round-trips through the
// registered decoder.
func TestWorkspaceSetReleaseRequestedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := workspaceSetReleaseRequestedEventPayload{
		WorkspaceSetID: "workspace-set-9", FamilyID: "family-9", ProjectID: "project-9",
		State: "RELEASING", ReleaseJobID: "job-9",
	}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(WorkspaceSetReleaseRequestedEventType, WorkspaceSetReleaseRequestedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}
