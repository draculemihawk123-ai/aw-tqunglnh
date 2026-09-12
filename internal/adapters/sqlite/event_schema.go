package sqlite

import (
	"encoding/json"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

// This file is V6-00A's own "domain-event catalog closure"
// (docs/design/08-v6-api-projections.md V6-00A) retrofit for three
// events found only by a second, deeper sweep: workspace_lifecycle.go's
// own Quarantine/Release/RecreateRepositoryWorkspace each append their
// own domain event via a raw `INSERT INTO domain_events` inside this
// package — entirely bypassing ports.EventsRepository.Append (and thus
// eventschema.EnforcingEventsRepository, even once something finally
// wires it into a real composition root) — which is exactly why the
// first pass over internal/app/*'s own ports.DomainEvent{}/Events().Append(
// call sites missed them. They previously used an inline
// map[string]string payload; this task types them (repositoryWorkspaceEvent.payload
// is now `any`, not `map[string]string`) without changing the JSON shape
// each one already produced.
//
// This is a real, separate architectural fact worth carrying forward,
// not just a decode gap: even after V6-00A's own catalog closes and
// EnforcingEventsRepository is eventually wired in, THESE three events
// would still bypass that enforcement, because they never call
// ports.EventsRepository.Append at all. Closing that gap for real would
// mean routing this package's own raw domain_events inserts through the
// same interface every app-layer command already uses — a bigger,
// separate refactor V6-00A's own "Không làm: không đổi business
// transition" deliberately does not attempt here.

const (
	RepositoryWorkspaceQuarantinedEventType     = "REPOSITORY_WORKSPACE_QUARANTINED"
	RepositoryWorkspaceQuarantinedSchemaVersion = 1

	RepositoryWorkspaceReleasedEventType     = "REPOSITORY_WORKSPACE_RELEASED"
	RepositoryWorkspaceReleasedSchemaVersion = 1

	RepositoryWorkspaceRecreatedEventType     = "REPOSITORY_WORKSPACE_RECREATED"
	RepositoryWorkspaceRecreatedSchemaVersion = 1
)

// repositoryWorkspaceQuarantinedEventPayload is
// REPOSITORY_WORKSPACE_QUARANTINED v1's own shape
// (quarantineRepositoryWorkspaceTx, workspace_lifecycle.go).
type repositoryWorkspaceQuarantinedEventPayload struct {
	RepositoryWorkspaceID string `json:"repositoryWorkspaceId"`
	Reason                string `json:"reason"`
}

// repositoryWorkspaceReleasedEventPayload is
// REPOSITORY_WORKSPACE_RELEASED v1's own shape
// (Store.ReleaseRepositoryWorkspace, workspace_lifecycle.go).
type repositoryWorkspaceReleasedEventPayload struct {
	RepositoryWorkspaceID string `json:"repositoryWorkspaceId"`
}

// repositoryWorkspaceRecreatedEventPayload is
// REPOSITORY_WORKSPACE_RECREATED v1's own shape
// (Store.RecreateRepositoryWorkspace, workspace_lifecycle.go) — Generation
// stays a string, matching the original fmt.Sprintf("%d", ...) exactly,
// so this retrofit changes no caller-visible JSON type.
type repositoryWorkspaceRecreatedEventPayload struct {
	PreviousRepositoryWorkspaceID string `json:"previousRepositoryWorkspaceId"`
	RepositoryWorkspaceID         string `json:"repositoryWorkspaceId"`
	Generation                    string `json:"generation"`
}

func DecodeRepositoryWorkspaceQuarantinedV1(payloadJSON string) (any, error) {
	var payload repositoryWorkspaceQuarantinedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func DecodeRepositoryWorkspaceReleasedV1(payloadJSON string) (any, error) {
	var payload repositoryWorkspaceReleasedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func DecodeRepositoryWorkspaceRecreatedV1(payloadJSON string) (any, error) {
	var payload repositoryWorkspaceRecreatedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// RegisterEventSchemas registers every event type this package produces
// with registry — mirrors internal/app/runtime/event_schema.go's own
// RegisterEventSchemas exactly.
func RegisterEventSchemas(registry *eventschema.Registry) {
	registry.Register(RepositoryWorkspaceQuarantinedEventType, RepositoryWorkspaceQuarantinedSchemaVersion, DecodeRepositoryWorkspaceQuarantinedV1)
	registry.Register(RepositoryWorkspaceReleasedEventType, RepositoryWorkspaceReleasedSchemaVersion, DecodeRepositoryWorkspaceReleasedV1)
	registry.Register(RepositoryWorkspaceRecreatedEventType, RepositoryWorkspaceRecreatedSchemaVersion, DecodeRepositoryWorkspaceRecreatedV1)
}
