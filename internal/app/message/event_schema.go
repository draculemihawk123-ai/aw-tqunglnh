package message

import (
	"encoding/json"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

// This file is V6-00A's own "domain-event catalog closure"
// (docs/design/08-v6-api-projections.md V6-00A) retrofit for this
// package's one event: AppendMessage originally appended an inline
// anonymous struct under a bare string EventType, with no
// eventschema.Registry decoder, golden fixture, or typed constant. The
// payload shape below is unchanged from what commands.go already
// marshaled; this task only names it and wires it through eventschema,
// never a business-transition change.

const (
	MessageAppendedEventType     = "MessageAppended"
	MessageAppendedSchemaVersion = 1
)

// messageAppendedEventPayload is MessageAppended v1's own shape
// (AppendMessage, commands.go).
type messageAppendedEventPayload struct {
	WorkItemID        string `json:"workItemId"`
	Role              string `json:"role"`
	Sequence          uint64 `json:"sequence"`
	ContentArtifactID string `json:"contentArtifactId"`
}

func DecodeMessageAppendedV1(payloadJSON string) (any, error) {
	var payload messageAppendedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// RegisterEventSchemas registers every event type this package produces
// with registry — mirrors internal/app/runtime/event_schema.go's own
// RegisterEventSchemas exactly.
func RegisterEventSchemas(registry *eventschema.Registry) {
	registry.Register(MessageAppendedEventType, MessageAppendedSchemaVersion, DecodeMessageAppendedV1)
}
