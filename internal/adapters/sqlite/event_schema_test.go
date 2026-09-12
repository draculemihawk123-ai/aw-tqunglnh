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

// TestNodeRunCompletedV1_GoldenFixtureDecodes is NODE_RUN_COMPLETED's own
// golden-fixture proof (V6-00A, second sweep).
func TestNodeRunCompletedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "node_run_completed_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(NodeRunCompletedEventType, NodeRunCompletedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", NodeRunCompletedEventType, NodeRunCompletedSchemaVersion, err)
	}
	want := nodeRunCompletedEventPayload{NodeRunID: "node-run-1", SelectedOutcome: "done"}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", NodeRunCompletedEventType, NodeRunCompletedSchemaVersion, got, want)
	}
}

// TestNodeRunCompletedV1_RealEventPayloadDecodes proves
// Store.CompleteNodeAndDispatchNext's own actual marshaled
// NODE_RUN_COMPLETED payload round-trips through the registered decoder.
func TestNodeRunCompletedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := nodeRunCompletedEventPayload{NodeRunID: "node-run-9", SelectedOutcome: "next"}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(NodeRunCompletedEventType, NodeRunCompletedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}

// TestNodeRunDispatchedV1_GoldenFixtureDecodes is NODE_RUN_DISPATCHED's
// own golden-fixture proof (V6-00A, second sweep).
func TestNodeRunDispatchedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "node_run_dispatched_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(NodeRunDispatchedEventType, NodeRunDispatchedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", NodeRunDispatchedEventType, NodeRunDispatchedSchemaVersion, err)
	}
	want := nodeRunDispatchedEventPayload{RunID: "run-1", NodeRunID: "node-run-2", NodeKey: "implement"}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", NodeRunDispatchedEventType, NodeRunDispatchedSchemaVersion, got, want)
	}
}

// TestNodeRunDispatchedV1_RealEventPayloadDecodes proves this package's
// own actual marshaled NODE_RUN_DISPATCHED payload round-trips through
// the registered decoder.
func TestNodeRunDispatchedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := nodeRunDispatchedEventPayload{RunID: "run-9", NodeRunID: "node-run-19", NodeKey: "review"}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(NodeRunDispatchedEventType, NodeRunDispatchedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}

// TestWorkflowRunFinalizedV1_GoldenFixtureDecodes is
// WORKFLOW_RUN_FINALIZED's own golden-fixture proof (V6-00A, second
// sweep).
func TestWorkflowRunFinalizedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "workflow_run_finalized_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(WorkflowRunFinalizedEventType, WorkflowRunFinalizedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", WorkflowRunFinalizedEventType, WorkflowRunFinalizedSchemaVersion, err)
	}
	want := workflowRunFinalizedEventPayload{JobID: "job-1", JobLeaseOwner: "worker-1", RunID: "run-1", TerminalState: "SUCCEEDED"}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", WorkflowRunFinalizedEventType, WorkflowRunFinalizedSchemaVersion, got, want)
	}
}

// TestWorkflowRunFinalizedV1_RealEventPayloadDecodes proves this
// package's own actual marshaled WORKFLOW_RUN_FINALIZED payload
// round-trips through the registered decoder.
func TestWorkflowRunFinalizedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := workflowRunFinalizedEventPayload{JobID: "job-9", JobLeaseOwner: "worker-9", RunID: "run-9", TerminalState: "FAILED"}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(WorkflowRunFinalizedEventType, WorkflowRunFinalizedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}
