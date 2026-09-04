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

// RegisterEventSchemas registers every event type this package produces
// with registry. A composition root that wires up EnforcingEventsRepository
// calls this once at startup, the same "register at init/startup time"
// discipline eventschema.Registry's own doc comment describes — this
// package does not call it itself (no composition root exists yet in this
// codebase for the runtime engine; V4-04+/V6 is where one is most likely
// to first assemble and call this).
func RegisterEventSchemas(registry *eventschema.Registry) {
	registry.Register(NodeRoutedEventType, NodeRoutedSchemaVersion, DecodeNodeRoutedV1)
}
