package workspacereconcile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

// TestWorkspaceReconciliationRequestedV1_GoldenFixtureDecodes is
// WorkspaceReconciliationRequested's own golden-fixture proof, mirroring
// internal/app/runtime/event_schema_test.go's own
// TestNodeRoutedV1_GoldenFixtureDecodes pattern (V6-00A).
func TestWorkspaceReconciliationRequestedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "workspace_reconciliation_requested_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(WorkspaceReconciliationRequestedEventType, WorkspaceReconciliationRequestedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", WorkspaceReconciliationRequestedEventType, WorkspaceReconciliationRequestedSchemaVersion, err)
	}
	want := workspaceReconciliationRequestedEventPayload{
		RepositoryWorkspaceID: "repo-workspace-1", ProjectID: "project-1", State: "READY", ReconciliationJobID: "job-1",
	}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", WorkspaceReconciliationRequestedEventType, WorkspaceReconciliationRequestedSchemaVersion, got, want)
	}
}

// TestWorkspaceReconciliationRequestedV1_RealEventPayloadDecodes proves
// RequestWorkspaceReconciliation's own actual marshaled
// WorkspaceReconciliationRequested payload round-trips through the
// registered decoder.
func TestWorkspaceReconciliationRequestedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := workspaceReconciliationRequestedEventPayload{
		RepositoryWorkspaceID: "repo-workspace-9", ProjectID: "project-9", State: "QUARANTINED", ReconciliationJobID: "job-9",
	}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(WorkspaceReconciliationRequestedEventType, WorkspaceReconciliationRequestedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}

// TestWorkspaceHeadAdoptedV1_GoldenFixtureDecodes is WorkspaceHeadAdopted's own
// golden-fixture proof (V11-01).
func TestWorkspaceHeadAdoptedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "workspace_head_adopted_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	decoded, err := registry.Decode(WorkspaceHeadAdoptedEventType, WorkspaceHeadAdoptedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", WorkspaceHeadAdoptedEventType, WorkspaceHeadAdoptedSchemaVersion, err)
	}
	got, ok := decoded.(workspaceHeadAdoptedEventPayload)
	if !ok {
		t.Fatalf("Decode returned %T, want workspaceHeadAdoptedEventPayload", decoded)
	}
	if got.RepositoryWorkspaceID != "repo-workspace-1" || got.Actor != "operator-1" || got.AdoptedCommitCount != 1 ||
		len(got.AdoptedCommits) != 1 || got.AdoptedCommits[0].AuthorName != "Ide User" || got.AdoptedRevision != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("Decode(%s v%d) = %+v", WorkspaceHeadAdoptedEventType, WorkspaceHeadAdoptedSchemaVersion, got)
	}
}
