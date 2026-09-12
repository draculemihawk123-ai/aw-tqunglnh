package adapterbuild

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

// TestAdapterBuildRegisteredV1_GoldenFixtureDecodes is
// AdapterBuildRegistered's own golden-fixture proof, mirroring
// internal/app/catalog/event_schema_test.go's own
// TestRepositoryRegisteredV1_GoldenFixtureDecodes pattern (V6-00A/V6-10I).
func TestAdapterBuildRegisteredV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "adapter_build_registered_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(AdapterBuildRegisteredEventType, AdapterBuildRegisteredSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", AdapterBuildRegisteredEventType, AdapterBuildRegisteredSchemaVersion, err)
	}
	wantRegisteredAt, err := time.Parse(time.RFC3339, "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("parse want RegisteredAt: %v", err)
	}
	want := adapterBuildRegisteredEventPayload{
		BuildID: "build-1", ProviderKey: "claude", ExecutablePath: "/usr/local/bin/claude",
		ExecutableContentHash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ProtocolVersion:       "claude-stream-json/v1", OS: "linux", Toolchain: "node-20", ConfigIdentity: "default",
		RegisteredBy: "operator-1", RegisteredAt: wantRegisteredAt,
	}
	got2, ok := got.(adapterBuildRegisteredEventPayload)
	if !ok {
		t.Fatalf("Decode returned %T, want adapterBuildRegisteredEventPayload", got)
	}
	if !got2.RegisteredAt.Equal(want.RegisteredAt) {
		t.Fatalf("RegisteredAt = %v, want %v", got2.RegisteredAt, want.RegisteredAt)
	}
	got2.RegisteredAt = want.RegisteredAt
	if got2 != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", AdapterBuildRegisteredEventType, AdapterBuildRegisteredSchemaVersion, got2, want)
	}
}

// TestAdapterBuildRegisteredV1_RealEventPayloadDecodes proves
// RegisterAdapterBuild's own actual marshaled AdapterBuildRegistered
// payload round-trips through the registered decoder.
func TestAdapterBuildRegisteredV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := adapterBuildRegisteredEventPayload{
		BuildID: "build-9", ProviderKey: "codex", ExecutablePath: "/usr/local/bin/codex",
		ExecutableContentHash: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		ProtocolVersion:       "codex-stream-json/v1", OS: "darwin", Toolchain: "node-22", ConfigIdentity: "sandboxed",
		RegisteredBy: "operator-9", RegisteredAt: time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC),
	}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(AdapterBuildRegisteredEventType, AdapterBuildRegisteredSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	got2, ok := got.(adapterBuildRegisteredEventPayload)
	if !ok {
		t.Fatalf("Decode returned %T, want adapterBuildRegisteredEventPayload", got)
	}
	if !got2.RegisteredAt.Equal(produced.RegisteredAt) {
		t.Fatalf("RegisteredAt = %v, want %v", got2.RegisteredAt, produced.RegisteredAt)
	}
	got2.RegisteredAt = produced.RegisteredAt
	if got2 != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got2, produced)
	}
}
