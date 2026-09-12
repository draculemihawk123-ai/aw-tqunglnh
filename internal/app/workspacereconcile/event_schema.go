package workspacereconcile

import (
	"encoding/json"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

// This file is V6-00A's own "domain-event catalog closure"
// (docs/design/08-v6-api-projections.md V6-00A) retrofit for this
// package's one event: RequestWorkspaceReconciliation originally
// appended an inline anonymous struct under a bare string EventType,
// with no eventschema.Registry decoder, golden fixture, or typed
// constant. The payload shape below is unchanged from what commands.go
// already marshaled; this task only names it and wires it through
// eventschema, never a business-transition change.

const (
	WorkspaceReconciliationRequestedEventType     = "WorkspaceReconciliationRequested"
	WorkspaceReconciliationRequestedSchemaVersion = 1
)

// workspaceReconciliationRequestedEventPayload is
// WorkspaceReconciliationRequested v1's own shape
// (RequestWorkspaceReconciliation, commands.go).
type workspaceReconciliationRequestedEventPayload struct {
	RepositoryWorkspaceID string `json:"repositoryWorkspaceId"`
	ProjectID             string `json:"projectId"`
	State                 string `json:"state"`
	ReconciliationJobID   string `json:"reconciliationJobId"`
}

func DecodeWorkspaceReconciliationRequestedV1(payloadJSON string) (any, error) {
	var payload workspaceReconciliationRequestedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// RegisterEventSchemas registers every event type this package produces
// with registry — mirrors internal/app/runtime/event_schema.go's own
// RegisterEventSchemas exactly.
func RegisterEventSchemas(registry *eventschema.Registry) {
	registry.Register(WorkspaceReconciliationRequestedEventType, WorkspaceReconciliationRequestedSchemaVersion, DecodeWorkspaceReconciliationRequestedV1)
}
