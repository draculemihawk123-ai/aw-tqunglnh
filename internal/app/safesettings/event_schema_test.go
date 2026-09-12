package safesettings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

// TestSafeSettingsUpdatedV1_GoldenFixtureDecodes mirrors
// internal/app/work/event_schema_test.go's own
// TestRootWorkItemCreatedV1_GoldenFixtureDecodes pattern (V6-00A).
func TestSafeSettingsUpdatedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "safe_settings_updated_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(SafeSettingsUpdatedEventType, SafeSettingsUpdatedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", SafeSettingsUpdatedEventType, SafeSettingsUpdatedSchemaVersion, err)
	}
	want := safeSettingsUpdatedEventPayload{
		ManagedWorkspaceRoot: "data/workspaces", ManagedArtifactRoot: "data/artifacts",
		EvidenceRetentionSeconds: 604800, ProcessOutputLimit: 1 << 20,
		ProviderExecutablePath: "C:/tools/claude/claude.exe", ProviderDefaultModel: "claude-sonnet-4-5",
		ProviderCredentialRef: "keychain:claude-api-key", Version: 2, UpdatedBy: "actor-1",
	}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", SafeSettingsUpdatedEventType, SafeSettingsUpdatedSchemaVersion, got, want)
	}
}

// TestSafeSettingsUpdatedV1_RealEventPayloadDecodes proves
// UpdateSafeSettings' own actual marshaled SafeSettingsUpdated payload
// round-trips through the registered decoder.
func TestSafeSettingsUpdatedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := safeSettingsUpdatedEventPayload{
		ManagedWorkspaceRoot: "data/workspaces-9", ManagedArtifactRoot: "data/artifacts-9",
		EvidenceRetentionSeconds: 3600, ProcessOutputLimit: 2048,
		ProviderExecutablePath: "/usr/local/bin/codex", ProviderDefaultModel: "gpt-5-codex",
		ProviderCredentialRef: "env:CODEX_API_KEY", Version: 9, UpdatedBy: "actor-9",
	}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(SafeSettingsUpdatedEventType, SafeSettingsUpdatedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}
