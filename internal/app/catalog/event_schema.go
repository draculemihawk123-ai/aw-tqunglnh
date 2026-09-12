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

	// ProjectCreatedEventType/ProjectCreatedSchemaVersion are V6-03's own
	// registered event (docs/design/08-v6-api-projections.md V6-03's own
	// "append registered PROJECT_CREATED v1"), CreateProject's
	// (commands.go) genesis event for the new Project's own aggregate
	// stream — unlike the three events above, which were retrofitted for
	// V6-00A onto business logic V3-01/V3-02 had already shipped, this
	// one is registered from day one, alongside the command that first
	// produces it.
	ProjectCreatedEventType     = "ProjectCreated"
	ProjectCreatedSchemaVersion = 1
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

// projectCreatedEventPayload is ProjectCreated v1's own shape
// (CreateProject, commands.go): the new Project's own generated ID, its
// name and its starting status (always project.ProjectActive, per
// project.NewProject's own rule — carried explicitly rather than assumed
// so a future status machine change can never silently make this
// historical field misleading).
type projectCreatedEventPayload struct {
	ProjectID string `json:"projectId"`
	Name      string `json:"name"`
	Status    string `json:"status"`
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

func DecodeProjectCreatedV1(payloadJSON string) (any, error) {
	var payload projectCreatedEventPayload
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
	registry.Register(ProjectCreatedEventType, ProjectCreatedSchemaVersion, DecodeProjectCreatedV1)
}
