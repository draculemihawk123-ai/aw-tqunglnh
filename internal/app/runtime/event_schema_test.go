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

// TestExecutionAttemptFinalizedV1_GoldenFixtureDecodes is
// EXECUTION_ATTEMPT_FINALIZED's own golden-fixture proof, mirroring
// TestNodeRoutedV1_GoldenFixtureDecodes.
func TestExecutionAttemptFinalizedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "execution_attempt_finalized_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(ExecutionAttemptFinalizedEventType, ExecutionAttemptFinalizedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", ExecutionAttemptFinalizedEventType, ExecutionAttemptFinalizedSchemaVersion, err)
	}
	want := executionAttemptFinalizedEventPayload{
		RunID: "run-1", WorkItemID: "work-item-1", NodeRunID: "node-run-1", AttemptID: "attempt-1",
		NextState: "SUCCEEDED", TerminationReason: "COMPLETED", JobID: "job-1",
	}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", ExecutionAttemptFinalizedEventType, ExecutionAttemptFinalizedSchemaVersion, got, want)
	}
}

// TestExecutionAttemptFinalizedV1_RealEventPayloadDecodes proves
// FinalizeExecutionAttempt's own actual marshaled EXECUTION_ATTEMPT_FINALIZED
// payload round-trips through the registered decoder, mirroring
// TestNodeRoutedV1_RealEventPayloadDecodes.
func TestExecutionAttemptFinalizedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := executionAttemptFinalizedEventPayload{
		RunID: "run-9", WorkItemID: "work-item-9", NodeRunID: "node-run-9", AttemptID: "attempt-9",
		NextState: "FAILED", TerminationReason: "EXECUTION_FAILED", JobID: "job-9",
	}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(ExecutionAttemptFinalizedEventType, ExecutionAttemptFinalizedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}

// TestNodeRunFailedV1_GoldenFixtureDecodes is NODE_RUN_FAILED's own
// golden-fixture proof, mirroring TestNodeRoutedV1_GoldenFixtureDecodes.
func TestNodeRunFailedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "node_run_failed_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(NodeRunFailedEventType, NodeRunFailedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", NodeRunFailedEventType, NodeRunFailedSchemaVersion, err)
	}
	want := nodeRunFailedEventPayload{
		RunID: "run-1", WorkItemID: "work-item-1", NodeRunID: "node-run-1", FailureKind: "RETRY_EXHAUSTED",
		LastAttemptID: "attempt-3", AttemptsUsed: 3, MaxAttempts: 3, LastErrorCode: "PROVIDER_UNAVAILABLE",
		AttemptPolicyVersionID: "policy-version-1", JobID: "job-1",
	}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", NodeRunFailedEventType, NodeRunFailedSchemaVersion, got, want)
	}
}

// TestNodeRunFailedV1_RealEventPayloadDecodes proves
// FinalizeExecutionAttempt's own actual marshaled NODE_RUN_FAILED payload
// round-trips through the registered decoder, mirroring
// TestNodeRoutedV1_RealEventPayloadDecodes.
func TestNodeRunFailedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := nodeRunFailedEventPayload{
		RunID: "run-9", WorkItemID: "work-item-9", NodeRunID: "node-run-9", FailureKind: "NON_RETRYABLE_FAILURE",
		LastAttemptID: "attempt-9", AttemptsUsed: 1, MaxAttempts: 3, LastErrorCode: "VALIDATION_FAILED",
		AttemptPolicyVersionID: "policy-version-9", JobID: "job-9",
	}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(NodeRunFailedEventType, NodeRunFailedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}

// TestNodeCycleExhaustedV1_GoldenFixtureDecodes is NODE_CYCLE_EXHAUSTED's
// own golden-fixture proof, mirroring TestNodeRoutedV1_GoldenFixtureDecodes.
func TestNodeCycleExhaustedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "node_cycle_exhausted_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(NodeCycleExhaustedEventType, NodeCycleExhaustedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", NodeCycleExhaustedEventType, NodeCycleExhaustedSchemaVersion, err)
	}
	want := nodeCycleExhaustedEventPayload{
		RunID: "run-1", WorkItemID: "work-item-1", NodeRunID: "node-run-skipped-1", NodeKey: "reviewer",
		MaxIterations: 2, AttemptedIteration: 3, TriggeringNodeRunID: "node-run-maker-1", TriggeringNodeKey: "maker",
		TriggeringOutcome: "needs_rework", EscalationOutcome: "escalate", EscalationNodeRunID: "node-run-escalation-1",
		EscalationNodeKey: "human_review", JobID: "job-1",
	}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", NodeCycleExhaustedEventType, NodeCycleExhaustedSchemaVersion, got, want)
	}
}

// TestNodeCycleExhaustedV1_RealEventPayloadDecodes proves AdvanceRun's own
// actual marshaled NODE_CYCLE_EXHAUSTED payload round-trips through the
// registered decoder, mirroring TestNodeRoutedV1_RealEventPayloadDecodes.
func TestNodeCycleExhaustedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := nodeCycleExhaustedEventPayload{
		RunID: "run-9", WorkItemID: "work-item-9", NodeRunID: "node-run-skipped-9", NodeKey: "reviewer",
		MaxIterations: 1, AttemptedIteration: 2, TriggeringNodeRunID: "node-run-maker-9", TriggeringNodeKey: "maker",
		TriggeringOutcome: "needs_rework", EscalationOutcome: "escalate", EscalationNodeRunID: "node-run-escalation-9",
		EscalationNodeKey: "human_review", JobID: "job-9",
	}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(NodeCycleExhaustedEventType, NodeCycleExhaustedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}
