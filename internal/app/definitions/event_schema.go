package definitions

import (
	"encoding/json"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

// This file is V6-00A's own "domain-event catalog closure"
// (docs/design/08-v6-api-projections.md V6-00A) retrofit for this
// package's two events: CreateDefinition/PublishDefinitionVersion each
// originally appended an inline anonymous/positional struct under a bare
// string EventType (V2), with no eventschema.Registry decoder, golden
// fixture, or typed constant. The payload shapes below are unchanged from
// what commands.go already marshaled; this task only names them and
// wires them through eventschema, never a business-transition change.

const (
	DefinitionCreatedEventType     = "DefinitionCreated"
	DefinitionCreatedSchemaVersion = 1

	DefinitionVersionPublishedEventType     = "DefinitionVersionPublished"
	DefinitionVersionPublishedSchemaVersion = 1
)

// definitionCreatedEventPayload is DefinitionCreated v1's own shape
// (CreateDefinition, commands.go).
type definitionCreatedEventPayload struct {
	DefinitionID string `json:"definitionId"`
	Kind         string `json:"kind"`
	Name         string `json:"name"`
}

// definitionVersionPublishedEventPayload is DefinitionVersionPublished
// v1's own shape (PublishDefinitionVersion, commands.go) — covers every
// shared Definition kind plus Workflow; there is no separate
// workflow-specific event.
type definitionVersionPublishedEventPayload struct {
	DefinitionID  string `json:"definitionId"`
	VersionID     string `json:"versionId"`
	Kind          string `json:"kind"`
	VersionNumber uint64 `json:"versionNumber"`
	CompiledHash  string `json:"compiledHash"`
}

func DecodeDefinitionCreatedV1(payloadJSON string) (any, error) {
	var payload definitionCreatedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func DecodeDefinitionVersionPublishedV1(payloadJSON string) (any, error) {
	var payload definitionVersionPublishedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// RegisterEventSchemas registers every event type this package produces
// with registry — mirrors internal/app/runtime/event_schema.go's own
// RegisterEventSchemas exactly.
func RegisterEventSchemas(registry *eventschema.Registry) {
	registry.Register(DefinitionCreatedEventType, DefinitionCreatedSchemaVersion, DecodeDefinitionCreatedV1)
	registry.Register(DefinitionVersionPublishedEventType, DefinitionVersionPublishedSchemaVersion, DecodeDefinitionVersionPublishedV1)
}
