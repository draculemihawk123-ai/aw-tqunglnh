package sqlite

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

// TestRepositoryWorkspaceQuarantinedV1_GoldenFixtureDecodes is
// REPOSITORY_WORKSPACE_QUARANTINED's own golden-fixture proof, mirroring
// internal/app/runtime/event_schema_test.go's own
// TestNodeRoutedV1_GoldenFixtureDecodes pattern (V6-00A).
func TestRepositoryWorkspaceQuarantinedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "repository_workspace_quarantined_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(RepositoryWorkspaceQuarantinedEventType, RepositoryWorkspaceQuarantinedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", RepositoryWorkspaceQuarantinedEventType, RepositoryWorkspaceQuarantinedSchemaVersion, err)
	}
	want := repositoryWorkspaceQuarantinedEventPayload{RepositoryWorkspaceID: "repo-workspace-1", Reason: "OWNERSHIP_LOST_MUTATING"}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", RepositoryWorkspaceQuarantinedEventType, RepositoryWorkspaceQuarantinedSchemaVersion, got, want)
	}
}

// TestRepositoryWorkspaceQuarantinedV1_RealEventPayloadDecodes proves
// quarantineRepositoryWorkspaceTx's own actual marshaled
// REPOSITORY_WORKSPACE_QUARANTINED payload round-trips through the
// registered decoder.
func TestRepositoryWorkspaceQuarantinedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := repositoryWorkspaceQuarantinedEventPayload{RepositoryWorkspaceID: "repo-workspace-9", Reason: "SCOPE_VIOLATION"}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(RepositoryWorkspaceQuarantinedEventType, RepositoryWorkspaceQuarantinedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}

// TestRepositoryWorkspaceReleasedV1_GoldenFixtureDecodes is
// REPOSITORY_WORKSPACE_RELEASED's own golden-fixture proof.
func TestRepositoryWorkspaceReleasedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "repository_workspace_released_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(RepositoryWorkspaceReleasedEventType, RepositoryWorkspaceReleasedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", RepositoryWorkspaceReleasedEventType, RepositoryWorkspaceReleasedSchemaVersion, err)
	}
	want := repositoryWorkspaceReleasedEventPayload{RepositoryWorkspaceID: "repo-workspace-1"}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", RepositoryWorkspaceReleasedEventType, RepositoryWorkspaceReleasedSchemaVersion, got, want)
	}
}

// TestRepositoryWorkspaceReleasedV1_RealEventPayloadDecodes proves
// Store.ReleaseRepositoryWorkspace's own actual marshaled
// REPOSITORY_WORKSPACE_RELEASED payload round-trips through the
// registered decoder.
func TestRepositoryWorkspaceReleasedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := repositoryWorkspaceReleasedEventPayload{RepositoryWorkspaceID: "repo-workspace-9"}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(RepositoryWorkspaceReleasedEventType, RepositoryWorkspaceReleasedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}

// TestRepositoryWorkspaceRecreatedV1_GoldenFixtureDecodes is
// REPOSITORY_WORKSPACE_RECREATED's own golden-fixture proof.
func TestRepositoryWorkspaceRecreatedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "repository_workspace_recreated_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(RepositoryWorkspaceRecreatedEventType, RepositoryWorkspaceRecreatedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", RepositoryWorkspaceRecreatedEventType, RepositoryWorkspaceRecreatedSchemaVersion, err)
	}
	want := repositoryWorkspaceRecreatedEventPayload{
		PreviousRepositoryWorkspaceID: "repo-workspace-1", RepositoryWorkspaceID: "repo-workspace-2", Generation: "2",
	}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", RepositoryWorkspaceRecreatedEventType, RepositoryWorkspaceRecreatedSchemaVersion, got, want)
	}
}

// TestRepositoryWorkspaceRecreatedV1_RealEventPayloadDecodes proves
// Store.RecreateRepositoryWorkspace's own actual marshaled
// REPOSITORY_WORKSPACE_RECREATED payload round-trips through the
// registered decoder.
func TestRepositoryWorkspaceRecreatedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := repositoryWorkspaceRecreatedEventPayload{
		PreviousRepositoryWorkspaceID: "repo-workspace-8", RepositoryWorkspaceID: "repo-workspace-9", Generation: "3",
	}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(RepositoryWorkspaceRecreatedEventType, RepositoryWorkspaceRecreatedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}
