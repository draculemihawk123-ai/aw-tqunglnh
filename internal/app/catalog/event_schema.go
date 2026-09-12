package catalog

import (
	"encoding/json"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

// This file is V6-00A's own "domain-event catalog closure"
// (docs/design/08-v6-api-projections.md V6-00A) retrofit for this
// package's three events: RegisterRepository/RetryRepositoryProbe/
// AssignComponentPack each originally appended an inline anonymous struct
// under a bare string EventType (V3-01/V3-02) — real production events
// with no eventschema.Registry decoder, golden fixture, or typed
// constant, exactly the "historical event chưa đăng ký" gap V6-00A closes.
// The payload shapes below are unchanged from what commands.go already
// marshaled; this task only names them and wires them through
// eventschema, never a business-transition change.

const (
	RepositoryRegisteredEventType     = "RepositoryRegistered"
	RepositoryRegisteredSchemaVersion = 1

	RepositoryProbeRetriedEventType     = "RepositoryProbeRetried"
	RepositoryProbeRetriedSchemaVersion = 1

	ComponentPackAssignedEventType     = "ComponentPackAssigned"
	ComponentPackAssignedSchemaVersion = 1
)

// repositoryRegisteredEventPayload is RepositoryRegistered v1's own shape
// (RegisterRepository, commands.go) — field-for-field identical to the
// anonymous struct that file used to marshal inline.
type repositoryRegisteredEventPayload struct {
	RepositoryID string `json:"repositoryId"`
	ProjectID    string `json:"projectId"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	ProbeJobID   string `json:"probeJobId"`
}

// repositoryProbeRetriedEventPayload is RepositoryProbeRetried v1's own
// shape (RetryRepositoryProbe, commands.go).
type repositoryProbeRetriedEventPayload struct {
	RepositoryID string `json:"repositoryId"`
	ProjectID    string `json:"projectId"`
	Status       string `json:"status"`
	ProbeJobID   string `json:"probeJobId"`
}

// componentPackAssignedEventPayload is ComponentPackAssigned v1's own
// shape (AssignComponentPack, commands.go).
type componentPackAssignedEventPayload struct {
	AssignmentID  string    `json:"assignmentId"`
	ComponentID   string    `json:"componentId"`
	PackVersionID string    `json:"packVersionId"`
	EffectiveAt   time.Time `json:"effectiveAt"`
	Actor         string    `json:"actor"`
}

func DecodeRepositoryRegisteredV1(payloadJSON string) (any, error) {
	var payload repositoryRegisteredEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func DecodeRepositoryProbeRetriedV1(payloadJSON string) (any, error) {
	var payload repositoryProbeRetriedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func DecodeComponentPackAssignedV1(payloadJSON string) (any, error) {
	var payload componentPackAssignedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// RegisterEventSchemas registers every event type this package produces
// with registry — mirrors internal/app/runtime/event_schema.go's own
// RegisterEventSchemas exactly.
func RegisterEventSchemas(registry *eventschema.Registry) {
	registry.Register(RepositoryRegisteredEventType, RepositoryRegisteredSchemaVersion, DecodeRepositoryRegisteredV1)
	registry.Register(RepositoryProbeRetriedEventType, RepositoryProbeRetriedSchemaVersion, DecodeRepositoryProbeRetriedV1)
	registry.Register(ComponentPackAssignedEventType, ComponentPackAssignedSchemaVersion, DecodeComponentPackAssignedV1)
}
