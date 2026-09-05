package runtime

import (
	"encoding/json"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

// DecodeNodeRoutedV1 is NODE_ROUTED v1's own eventschema.Decoder
// (internal/app/eventschema, V1-07A) — correction found during V4-03
// review: this event had no decoder/registration/golden fixture at all, so
// once EnforcingEventsRepository is ever wired into the production
// UnitOfWork, every NODE_ROUTED append would be rejected as unregistered.
func DecodeNodeRoutedV1(payloadJSON string) (any, error) {
	var payload nodeRoutedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// DecodeNodeScheduledV1 is NODE_SCHEDULED v1's own eventschema.Decoder
// (V4-04, schedule.go) — registered from the same changeset that produces
// this event, not deferred the way NODE_ROUTED's own registration was the
// first time (correction found during V4-03 review; this task does not
// repeat it).
func DecodeNodeScheduledV1(payloadJSON string) (any, error) {
	var payload nodeScheduledEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// DecodeExecutionAttemptFinalizedV1 is EXECUTION_ATTEMPT_FINALIZED v1's own
// eventschema.Decoder (V4-05, finalize.go) — registered from the same
// changeset that produces this event.
func DecodeExecutionAttemptFinalizedV1(payloadJSON string) (any, error) {
	var payload executionAttemptFinalizedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// DecodeNodeRunFailedV1 is NODE_RUN_FAILED v1's own eventschema.Decoder
// (V4-06, finalize.go) — registered from the same changeset that produces
// this event, not deferred (the corrected discipline this package's own
// DecodeNodeScheduledV1/DecodeExecutionAttemptFinalizedV1 doc comments
// already name).
func DecodeNodeRunFailedV1(payloadJSON string) (any, error) {
	var payload nodeRunFailedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// DecodeNodeCycleExhaustedV1 is NODE_CYCLE_EXHAUSTED v1's own
// eventschema.Decoder (V4-07, advance.go) — registered from the same
// changeset that produces this event, not deferred.
func DecodeNodeCycleExhaustedV1(payloadJSON string) (any, error) {
	var payload nodeCycleExhaustedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// DecodeNodeForkedV1 is NODE_FORKED v1's own eventschema.Decoder (V4-10,
// advance.go) — registered from the same changeset that produces this
// event, not deferred.
func DecodeNodeForkedV1(payloadJSON string) (any, error) {
	var payload nodeForkedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// DecodeJoinDecidedV1 is JOIN_DECIDED v1's own eventschema.Decoder (V4-11,
// advance.go) — registered from the same changeset that produces this
// event, not deferred.
func DecodeJoinDecidedV1(payloadJSON string) (any, error) {
	var payload joinDecidedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// RegisterEventSchemas registers every event type this package produces
// with registry. A composition root that wires up EnforcingEventsRepository
// calls this once at startup, the same "register at init/startup time"
// discipline eventschema.Registry's own doc comment describes — this
// package does not call it itself (no composition root exists yet in this
// codebase for the runtime engine; V4-04+/V6 is where one is most likely
// to first assemble and call this).
func RegisterEventSchemas(registry *eventschema.Registry) {
	registry.Register(NodeRoutedEventType, NodeRoutedSchemaVersion, DecodeNodeRoutedV1)
	registry.Register(NodeScheduledEventType, NodeScheduledSchemaVersion, DecodeNodeScheduledV1)
	registry.Register(ExecutionAttemptFinalizedEventType, ExecutionAttemptFinalizedSchemaVersion, DecodeExecutionAttemptFinalizedV1)
	registry.Register(NodeRunFailedEventType, NodeRunFailedSchemaVersion, DecodeNodeRunFailedV1)
	registry.Register(NodeCycleExhaustedEventType, NodeCycleExhaustedSchemaVersion, DecodeNodeCycleExhaustedV1)
	registry.Register(NodeForkedEventType, NodeForkedSchemaVersion, DecodeNodeForkedV1)
	registry.Register(JoinDecidedEventType, JoinDecidedSchemaVersion, DecodeJoinDecidedV1)
}
