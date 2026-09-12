package artifactsweep

import (
	"encoding/json"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

// This file is V6-00A's own "domain-event catalog closure"
// (docs/design/08-v6-api-projections.md V6-00A) retrofit for this
// package's one event: recordSweepManifestTx (sweep.go) already used a
// named, exported payload type (SweepManifest) — unlike most of this
// task's other retrofits, nothing there needed renaming — but had no
// eventschema.Registry decoder or golden fixture.

func DecodeArtifactSweepCompletedV1(payloadJSON string) (any, error) {
	var payload SweepManifest
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// RegisterEventSchemas registers every event type this package produces
// with registry — mirrors internal/app/runtime/event_schema.go's own
// RegisterEventSchemas exactly.
func RegisterEventSchemas(registry *eventschema.Registry) {
	registry.Register(ArtifactSweepCompletedEventType, ArtifactSweepCompletedSchemaVersion, DecodeArtifactSweepCompletedV1)
}
