package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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

// TestNodeForkedV1_GoldenFixtureDecodes is NODE_FORKED's own golden-fixture
// proof, mirroring TestNodeCycleExhaustedV1_GoldenFixtureDecodes. Compared
// with reflect.DeepEqual, not ==, since nodeForkedEventPayload carries a
// slice field (Branches) that Go's own == operator cannot compare.
func TestNodeForkedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "node_forked_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(NodeForkedEventType, NodeForkedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", NodeForkedEventType, NodeForkedSchemaVersion, err)
	}
	want := nodeForkedEventPayload{
		RunID: "run-1", WorkItemID: "work-item-1", ForkNodeRunID: "node-run-fork-1", ForkKey: "fork",
		Branches: []forkedBranchEventEntry{
			{BranchKey: "branch_a", BranchTokenID: "token-1", NodeRunID: "node-run-a-1", NodeKey: "a_step"},
			{BranchKey: "branch_b", BranchTokenID: "token-2", NodeRunID: "node-run-b-1", NodeKey: "b_step"},
			{BranchKey: "branch_c", BranchTokenID: "token-3", ReachedJoin: true},
		},
		JobID: "job-1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", NodeForkedEventType, NodeForkedSchemaVersion, got, want)
	}
}

// TestNodeForkedV1_RealEventPayloadDecodes proves dispatchForkBranches' own
// actual marshaled NODE_FORKED payload round-trips through the registered
// decoder, mirroring TestNodeCycleExhaustedV1_RealEventPayloadDecodes.
func TestNodeForkedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := nodeForkedEventPayload{
		RunID: "run-9", WorkItemID: "work-item-9", ForkNodeRunID: "node-run-fork-9", ForkKey: "fork",
		Branches: []forkedBranchEventEntry{
			{BranchKey: "branch_a", BranchTokenID: "token-9a", NodeRunID: "node-run-a-9", NodeKey: "a_step"},
		},
		JobID: "job-9",
	}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(NodeForkedEventType, NodeForkedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !reflect.DeepEqual(got, produced) {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}

// TestJoinDecidedV1_GoldenFixtureDecodes is JOIN_DECIDED's own golden-
// fixture proof, mirroring TestNodeCycleExhaustedV1_GoldenFixtureDecodes.
func TestJoinDecidedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "join_decided_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(JoinDecidedEventType, JoinDecidedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", JoinDecidedEventType, JoinDecidedSchemaVersion, err)
	}
	want := joinDecidedEventPayload{
		RunID: "run-1", WorkItemID: "work-item-1", JoinNodeRunID: "join-node-run-1", JoinNodeKey: "join",
		ForkNodeRunID: "node-run-fork-1", Mode: "QUORUM", QuorumCount: 2,
		SucceededCount: 2, FailedCount: 1, CancelledCount: 0, ActiveCount: 0,
		Verdict: "SUCCEEDED", JobID: "job-1",
	}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", JoinDecidedEventType, JoinDecidedSchemaVersion, got, want)
	}
}

// TestJoinDecidedV1_RealEventPayloadDecodes proves evaluateJoinTx's own
// actual marshaled JOIN_DECIDED payload round-trips through the registered
// decoder, mirroring TestNodeCycleExhaustedV1_RealEventPayloadDecodes.
func TestJoinDecidedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := joinDecidedEventPayload{
		RunID: "run-9", WorkItemID: "work-item-9", JoinNodeRunID: "join-node-run-9", JoinNodeKey: "join",
		ForkNodeRunID: "node-run-fork-9", Mode: "ALL", SucceededCount: 1, FailedCount: 1, ActiveCount: 0,
		Verdict: "FAILED", Reason: JoinPolicyUnsatisfiableReason, JobID: "job-9",
	}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(JoinDecidedEventType, JoinDecidedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}

// TestRunCompletionRequestedV1_GoldenFixtureDecodes is
// RUN_COMPLETION_REQUESTED's own golden-fixture proof, mirroring
// TestNodeRoutedV1_GoldenFixtureDecodes.
func TestRunCompletionRequestedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "run_completion_requested_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(RunCompletionRequestedEventType, RunCompletionRequestedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", RunCompletionRequestedEventType, RunCompletionRequestedSchemaVersion, err)
	}
	want := runCompletionRequestedEventPayload{
		RunID: "run-1", WorkItemID: "work-item-1", EndNodeRunID: "node-run-end-1", EndNodeKey: "end", JobID: "job-1",
	}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", RunCompletionRequestedEventType, RunCompletionRequestedSchemaVersion, got, want)
	}
}

// TestRunCompletionRequestedV1_RealEventPayloadDecodes proves
// transitionRunToVerifyingTx's own actual marshaled
// RUN_COMPLETION_REQUESTED payload round-trips through the registered
// decoder, mirroring TestNodeRoutedV1_RealEventPayloadDecodes.
func TestRunCompletionRequestedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := runCompletionRequestedEventPayload{
		RunID: "run-9", WorkItemID: "work-item-9", EndNodeRunID: "node-run-end-9", EndNodeKey: "end", JobID: "job-9",
	}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(RunCompletionRequestedEventType, RunCompletionRequestedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}

// TestRunFailedV1_GoldenFixtureDecodes is RUN_FAILED's own golden-fixture
// proof, mirroring TestNodeRoutedV1_GoldenFixtureDecodes.
func TestRunFailedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "run_failed_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(RunFailedEventType, RunFailedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", RunFailedEventType, RunFailedSchemaVersion, err)
	}
	want := runFailedEventPayload{RunID: "run-1", WorkItemID: "work-item-1", Reason: RunFailureReasonRunFailed, JobID: "job-1"}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", RunFailedEventType, RunFailedSchemaVersion, got, want)
	}
}

// TestRunFailedV1_RealEventPayloadDecodes proves transitionRunToFailedTx's
// own actual marshaled RUN_FAILED payload round-trips through the
// registered decoder, mirroring TestNodeRoutedV1_RealEventPayloadDecodes.
func TestRunFailedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := runFailedEventPayload{
		RunID: "run-9", WorkItemID: "work-item-9", Reason: RunFailureReasonTerminalPathInvalid, JobID: "job-9",
	}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(RunFailedEventType, RunFailedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}

// TestRunCancellationRequestedV1_GoldenFixtureDecodes is
// RUN_CANCELLATION_REQUESTED's own golden-fixture proof (V4-12B).
func TestRunCancellationRequestedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "run_cancellation_requested_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(RunCancellationRequestedEventType, RunCancellationRequestedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", RunCancellationRequestedEventType, RunCancellationRequestedSchemaVersion, err)
	}
	want := runCancellationRequestedEventPayload{RunID: "run-1", WorkItemID: "work-item-1", Actor: "actor-1", Reason: "operator requested cancel"}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", RunCancellationRequestedEventType, RunCancellationRequestedSchemaVersion, got, want)
	}
}

// TestRunCancellationRequestedV1_RealEventPayloadDecodes proves CancelRun's
// own actual marshaled RUN_CANCELLATION_REQUESTED payload round-trips
// through the registered decoder.
func TestRunCancellationRequestedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := runCancellationRequestedEventPayload{
		RunID: "run-9", WorkItemID: "work-item-9", Actor: "actor-9", Reason: "cleanup",
	}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(RunCancellationRequestedEventType, RunCancellationRequestedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}

// TestRunCancelledV1_GoldenFixtureDecodes is RUN_CANCELLED's own
// golden-fixture proof (V4-12B), mirroring TestRunFailedV1_GoldenFixtureDecodes.
func TestRunCancelledV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "run_cancelled_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(RunCancelledEventType, RunCancelledSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", RunCancelledEventType, RunCancelledSchemaVersion, err)
	}
	want := runCancelledEventPayload{RunID: "run-1", WorkItemID: "work-item-1", JobID: "job-1"}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", RunCancelledEventType, RunCancelledSchemaVersion, got, want)
	}
}

// TestRunCancelledV1_RealEventPayloadDecodes proves
// transitionRunToCancelledTx's own actual marshaled RUN_CANCELLED payload
// round-trips through the registered decoder.
func TestRunCancelledV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := runCancelledEventPayload{RunID: "run-9", WorkItemID: "work-item-9", JobID: "job-9"}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(RunCancelledEventType, RunCancelledSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}

// TestWorkItemCancellationRequestedV1_GoldenFixtureDecodes is
// WORK_ITEM_CANCELLATION_REQUESTED's own golden-fixture proof (V4-12C),
// mirroring TestRunCancellationRequestedV1_GoldenFixtureDecodes.
func TestWorkItemCancellationRequestedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "work_item_cancellation_requested_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(WorkItemCancellationRequestedEventType, WorkItemCancellationRequestedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", WorkItemCancellationRequestedEventType, WorkItemCancellationRequestedSchemaVersion, err)
	}
	want := workItemCancellationRequestedEventPayload{WorkItemID: "work-item-1", Actor: "actor-1", Reason: "operator requested cancel"}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", WorkItemCancellationRequestedEventType, WorkItemCancellationRequestedSchemaVersion, got, want)
	}
}

// TestWorkItemCancellationRequestedV1_RealEventPayloadDecodes proves
// CancelWorkItem's own actual marshaled WORK_ITEM_CANCELLATION_REQUESTED
// payload round-trips through the registered decoder.
func TestWorkItemCancellationRequestedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := workItemCancellationRequestedEventPayload{WorkItemID: "work-item-9", Actor: "actor-9", Reason: "cleanup"}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(WorkItemCancellationRequestedEventType, WorkItemCancellationRequestedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}

// TestWorkItemCancelledV1_GoldenFixtureDecodes is WORK_ITEM_CANCELLED's own
// golden-fixture proof (V4-12C), mirroring TestRunCancelledV1_GoldenFixtureDecodes.
func TestWorkItemCancelledV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "work_item_cancelled_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(WorkItemCancelledEventType, WorkItemCancelledSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", WorkItemCancelledEventType, WorkItemCancelledSchemaVersion, err)
	}
	want := workItemCancelledEventPayload{WorkItemID: "work-item-1", JobID: "job-1"}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", WorkItemCancelledEventType, WorkItemCancelledSchemaVersion, got, want)
	}
}

// TestWorkItemCancelledV1_RealEventPayloadDecodes proves
// reconcileWorkItemCancellationTx's own actual marshaled WORK_ITEM_CANCELLED
// payload round-trips through the registered decoder.
func TestWorkItemCancelledV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := workItemCancelledEventPayload{WorkItemID: "work-item-9", JobID: "job-9"}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(WorkItemCancelledEventType, WorkItemCancelledSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}

// TestWorkItemBlockedV1_GoldenFixtureDecodes is WORK_ITEM_BLOCKED's own
// golden-fixture proof (V4-12C).
func TestWorkItemBlockedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "work_item_blocked_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(WorkItemBlockedEventType, WorkItemBlockedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", WorkItemBlockedEventType, WorkItemBlockedSchemaVersion, err)
	}
	want := workItemBlockedEventPayload{
		WorkItemID: "work-item-1", BlockerID: "blocker-1", BlockerType: "RUN_CANCELLED", SourceRunID: "run-1", JobID: "job-1",
	}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", WorkItemBlockedEventType, WorkItemBlockedSchemaVersion, got, want)
	}
}

// TestWorkItemBlockedV1_RealEventPayloadDecodes proves openWorkItemBlockerTx's
// own actual marshaled WORK_ITEM_BLOCKED payload round-trips through the
// registered decoder.
func TestWorkItemBlockedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := workItemBlockedEventPayload{
		WorkItemID: "work-item-9", BlockerID: "blocker-9", BlockerType: "SCOPE_EXPANSION_REQUIRED", SourceRunID: "run-9", JobID: "job-9",
	}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(WorkItemBlockedEventType, WorkItemBlockedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}

// TestWorkItemBlockerResolvedV1_GoldenFixtureDecodes is
// WORK_ITEM_BLOCKER_RESOLVED's own golden-fixture proof (V4-12C).
func TestWorkItemBlockerResolvedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "work_item_blocker_resolved_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(WorkItemBlockerResolvedEventType, WorkItemBlockerResolvedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", WorkItemBlockerResolvedEventType, WorkItemBlockerResolvedSchemaVersion, err)
	}
	want := workItemBlockerResolvedEventPayload{
		WorkItemID: "work-item-1", BlockerID: "blocker-1", BlockerType: "RUN_CANCELLED", ResolutionMode: "RESOLVED",
		ResolvedBy: "actor-1", WorkItemUnblocked: true, NewWorkItemStatus: "READY", JobID: "job-1",
	}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", WorkItemBlockerResolvedEventType, WorkItemBlockerResolvedSchemaVersion, got, want)
	}
}

// TestWorkItemBlockerResolvedV1_RealEventPayloadDecodes proves
// closeWorkItemBlockerTx's own actual marshaled WORK_ITEM_BLOCKER_RESOLVED
// payload round-trips through the registered decoder.
func TestWorkItemBlockerResolvedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := workItemBlockerResolvedEventPayload{
		WorkItemID: "work-item-9", BlockerID: "blocker-9", BlockerType: "COMPLETION_POLICY_FAILED", ResolutionMode: "WAIVED",
		ResolvedBy: "actor-9", WorkItemUnblocked: false,
	}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(WorkItemBlockerResolvedEventType, WorkItemBlockerResolvedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}

// TestRecoveryDecisionRecordedV1_GoldenFixtureDecodes is
// RECOVERY_DECISION_RECORDED's own golden-fixture proof (V4-13).
func TestRecoveryDecisionRecordedV1_GoldenFixtureDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	payload, err := os.ReadFile(filepath.Join("testdata", "golden", "recovery_decision_recorded_v1.json"))
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	got, err := registry.Decode(RecoveryDecisionRecordedEventType, RecoveryDecisionRecordedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode(%s v%d): %v", RecoveryDecisionRecordedEventType, RecoveryDecisionRecordedSchemaVersion, err)
	}
	want := recoveryDecisionRecordedEventPayload{
		AttemptID: "attempt-1", NodeRunID: "node-run-1", RunID: "run-1", WorkItemID: "work-item-1",
		NextAction: "ESCALATE", Reason: "LEASE_LOST",
	}
	if got != want {
		t.Fatalf("Decode(%s v%d) = %+v, want %+v", RecoveryDecisionRecordedEventType, RecoveryDecisionRecordedSchemaVersion, got, want)
	}
}

// TestRecoveryDecisionRecordedV1_RealEventPayloadDecodes proves
// recordDecision's own actual marshaled RECOVERY_DECISION_RECORDED payload
// round-trips through the registered decoder.
func TestRecoveryDecisionRecordedV1_RealEventPayloadDecodes(t *testing.T) {
	registry := eventschema.NewRegistry()
	RegisterEventSchemas(registry)

	produced := recoveryDecisionRecordedEventPayload{
		AttemptID: "attempt-9", NodeRunID: "node-run-9", RunID: "run-9", WorkItemID: "work-item-9",
		NextAction: "FRESH_START", Reason: "OWNERSHIP_LOST_MUTATING",
	}
	payload, err := json.Marshal(produced)
	if err != nil {
		t.Fatalf("marshal produced payload: %v", err)
	}
	got, err := registry.Decode(RecoveryDecisionRecordedEventType, RecoveryDecisionRecordedSchemaVersion, string(payload))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != produced {
		t.Fatalf("Decode(marshal(produced)) = %+v, want %+v", got, produced)
	}
}
