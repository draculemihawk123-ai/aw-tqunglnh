package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

// TestRepositoryRegisteredV1_GoldenFixtureDecodes is RepositoryRegistered's
// own golden-fixture proof, mirroring internal/app/runtime/event_schema_test.go's
// own TestNodeRoutedV1_GoldenFixtureDecodes pattern (V6-00A).
func TestRepositoryRegisteredV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "repository_registered_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(RepositoryRegisteredEventType, RepositoryRegisteredSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", RepositoryRegisteredEventType, RepositoryRegisteredSchemaVersion, err)
	}
	want := repositoryRegisteredEventPayload{
		RepositoryID: "repo-1", ProjectID: "project-1", Name: "repo-1", Status: "REGISTERING", ProbeJobID: "job-1",
	}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", RepositoryRegisteredEventType, RepositoryRegisteredSchemaVersion, got, want)
	}
}

// TestRepositoryRegisteredV1_RealEventPayloadDecodes proves
// RegisterRepository's own actual marshaled RepositoryRegistered payload
// round-trips through the registered decoder.
func TestRepositoryRegisteredV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := repositoryRegisteredEventPayload{
		RepositoryID: "repo-9", ProjectID: "project-9", Name: "repo-9", Status: "REGISTERING", ProbeJobID: "job-9",
	}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(RepositoryRegisteredEventType, RepositoryRegisteredSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}

// TestRepositoryProbeRetriedV1_GoldenFixtureDecodes is
// RepositoryProbeRetried's own golden-fixture proof.
func TestRepositoryProbeRetriedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "repository_probe_retried_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(RepositoryProbeRetriedEventType, RepositoryProbeRetriedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", RepositoryProbeRetriedEventType, RepositoryProbeRetriedSchemaVersion, err)
	}
	want := repositoryProbeRetriedEventPayload{
		RepositoryID: "repo-1", ProjectID: "project-1", Status: "REGISTERING", ProbeJobID: "job-2",
	}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", RepositoryProbeRetriedEventType, RepositoryProbeRetriedSchemaVersion, got, want)
	}
}

// TestRepositoryProbeRetriedV1_RealEventPayloadDecodes proves
// RetryRepositoryProbe's own actual marshaled RepositoryProbeRetried
// payload round-trips through the registered decoder.
func TestRepositoryProbeRetriedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := repositoryProbeRetriedEventPayload{
		RepositoryID: "repo-9", ProjectID: "project-9", Status: "BLOCKED", ProbeJobID: "job-19",
	}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(RepositoryProbeRetriedEventType, RepositoryProbeRetriedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}

// TestComponentPackAssignedV1_GoldenFixtureDecodes is
// ComponentPackAssigned's own golden-fixture proof.
func TestComponentPackAssignedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "component_pack_assigned_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(ComponentPackAssignedEventType, ComponentPackAssignedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", ComponentPackAssignedEventType, ComponentPackAssignedSchemaVersion, err)
	}
	wantEffectiveAt, err := time.Parse(time.RFC3339, "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatalf("parse want EffectiveAt: %v", err)
	}
	want := componentPackAssignedEventPayload{
		AssignmentID: "assignment-1", ComponentID: "component-1", PackVersionID: "pack-version-1",
		EffectiveAt: wantEffectiveAt, Actor: "actor-1",
	}
	got2, ok := got.(componentPackAssignedEventPayload)
	if !ok {
		t.Fatalf("Decode returned %T, want componentPackAssignedEventPayload", got)
	}
	if !got2.EffectiveAt.Equal(want.EffectiveAt) || got2.AssignmentID != want.AssignmentID ||
		got2.ComponentID != want.ComponentID || got2.PackVersionID != want.PackVersionID || got2.Actor != want.Actor {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", ComponentPackAssignedEventType, ComponentPackAssignedSchemaVersion, got2, want)
	}
}

// TestComponentPackAssignedV1_RealEventPayloadDecodes proves
// AssignComponentPack's own actual marshaled ComponentPackAssigned
// payload round-trips through the registered decoder.
func TestComponentPackAssignedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := componentPackAssignedEventPayload{
		AssignmentID: "assignment-9", ComponentID: "component-9", PackVersionID: "pack-version-9",
		EffectiveAt: time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC), Actor: "actor-9",
	}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(ComponentPackAssignedEventType, ComponentPackAssignedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	got2, ok := got.(componentPackAssignedEventPayload)
	if !ok {
		t.Fatalf("Decode returned %T, want componentPackAssignedEventPayload", got)
	}
	if !got2.EffectiveAt.Equal(produced.EffectiveAt) || got2.AssignmentID != produced.AssignmentID ||
		got2.ComponentID != produced.ComponentID || got2.PackVersionID != produced.PackVersionID || got2.Actor != produced.Actor {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got2, produced)
	}
}

// TestProjectCreatedV1_GoldenFixtureDecodes is ProjectCreated's own
// golden-fixture proof (V6-03), mirroring this file's other three events.
func TestProjectCreatedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "project_created_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(ProjectCreatedEventType, ProjectCreatedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", ProjectCreatedEventType, ProjectCreatedSchemaVersion, err)
	}
	want := projectCreatedEventPayload{ProjectID: "project-1", Name: "demo", Status: "ACTIVE"}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", ProjectCreatedEventType, ProjectCreatedSchemaVersion, got, want)
	}
}

// TestProjectCreatedV1_RealEventPayloadDecodes proves CreateProject's own
// actual marshaled ProjectCreated payload round-trips through the
// registered decoder.
func TestProjectCreatedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := projectCreatedEventPayload{ProjectID: "project-9", Name: "ninth project", Status: "ACTIVE"}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(ProjectCreatedEventType, ProjectCreatedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}
