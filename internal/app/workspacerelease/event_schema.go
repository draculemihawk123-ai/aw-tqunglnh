package workspacerelease

import (
	"encoding/json"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

// This file is V6-00A's own "domain-event catalog closure"
// (docs/design/08-v6-api-projections.md V6-00A) retrofit for this
// package's one event: RequestWorkspaceSetRelease originally appended an
// inline anonymous struct under a bare string EventType, with no
// eventschema.Registry decoder, golden fixture, or typed constant. The
// payload shape below is unchanged from what commands.go already
// marshaled; this task only names it and wires it through eventschema,
// never a business-transition change.

const (
	WorkspaceSetReleaseRequestedEventType     = "WorkspaceSetReleaseRequested"
	WorkspaceSetReleaseRequestedSchemaVersion = 1
)

// workspaceSetReleaseRequestedEventPayload is WorkspaceSetReleaseRequested
// v1's own shape (RequestWorkspaceSetRelease, commands.go).
type workspaceSetReleaseRequestedEventPayload struct {
	WorkspaceSetID string `json:"workspaceSetId"`
	FamilyID       string `json:"familyId"`
	ProjectID      string `json:"projectId"`
	State          string `json:"state"`
	ReleaseJobID   string `json:"releaseJobId"`
}

func DecodeWorkspaceSetReleaseRequestedV1(payloadJSON string) (any, error) {
	var payload workspaceSetReleaseRequestedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// RegisterEventSchemas registers every event type this package produces
// with registry — mirrors internal/app/runtime/event_schema.go's own
// RegisterEventSchemas exactly.
func RegisterEventSchemas(registry *eventschema.Registry) {
	registry.Register(WorkspaceSetReleaseRequestedEventType, WorkspaceSetReleaseRequestedSchemaVersion, DecodeWorkspaceSetReleaseRequestedV1)
}
