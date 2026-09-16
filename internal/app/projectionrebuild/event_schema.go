package projectionrebuild

import (
	"encoding/json"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

// This file is V6-00A's own "domain-event catalog closure" convention,
// applied from the start (mirrors releasesetcommit/event_schema.go
// exactly): every event type this package produces is named and wired
// through eventschema.Registry from its first commit.

const (
	ProjectionRebuildRequestedEventType     = "ProjectionRebuildRequested"
	ProjectionRebuildRequestedSchemaVersion = 1
)

// projectionRebuildRequestedEventPayload is ProjectionRebuildRequested
// v1's own shape (RequestProjectionRebuild, commands.go).
type projectionRebuildRequestedEventPayload struct {
	OperationID    string `json:"operationId"`
	ProjectID      string `json:"projectId"`
	ProjectionName string `json:"projectionName"`
	JobID          string `json:"jobId"`
}

func DecodeProjectionRebuildRequestedV1(payloadJSON string) (any, error) {
	var payload projectionRebuildRequestedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// RegisterEventSchemas registers every event type this package produces
// with registry — mirrors internal/app/releasesetcommit/event_schema.go's
// own RegisterEventSchemas exactly.
func RegisterEventSchemas(registry *eventschema.Registry) {
	registry.Register(ProjectionRebuildRequestedEventType, ProjectionRebuildRequestedSchemaVersion, DecodeProjectionRebuildRequestedV1)
}
