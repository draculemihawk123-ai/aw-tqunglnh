package adapterbuild

import (
	"encoding/json"
	"time"

	"github.com/taQuangLing/agent-workflow/internal/app/eventschema"
)

// AdapterBuildRegisteredEventType/SchemaVersion is V6-10I's own "registered
// event" (docs/design/08-v6-api-projections.md V6-10I's own Thực hiện
// line): RegisterAdapterBuild's real domain event, wired through
// eventschema exactly like every other command handler's own event in
// this codebase (internal/app/catalog/event_schema.go is the closest,
// most recent sibling) — never a bare anonymous struct under an inline
// string EventType.
const (
	AdapterBuildRegisteredEventType     = "AdapterBuildRegistered"
	AdapterBuildRegisteredSchemaVersion = 1
)

// adapterBuildRegisteredEventPayload is AdapterBuildRegistered v1's own
// shape (RegisterAdapterBuild, commands.go) — every axis
// adapterbuild.CandidateTuple binds, plus RegisteredBy/RegisteredAt, the
// same fields cmd/aw/adapter.go's own adapterBuildView already exposes
// through the CLI.
type adapterBuildRegisteredEventPayload struct {
	BuildID               string    `json:"buildId"`
	ProviderKey           string    `json:"providerKey"`
	ExecutablePath        string    `json:"executablePath"`
	ExecutableContentHash string    `json:"executableContentHash"`
	ProtocolVersion       string    `json:"protocolVersion"`
	OS                    string    `json:"os"`
	Toolchain             string    `json:"toolchain"`
	ConfigIdentity        string    `json:"configIdentity"`
	RegisteredBy          string    `json:"registeredBy"`
	RegisteredAt          time.Time `json:"registeredAt"`
}

func DecodeAdapterBuildRegisteredV1(payloadJSON string) (any, error) {
	var payload adapterBuildRegisteredEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// RegisterEventSchemas registers every event type this package produces
// with registry — mirrors internal/app/catalog/event_schema.go's own
// RegisterEventSchemas exactly. internal/archtest's own
// TestEmittedDomainEventInventoryMatchesRegisteredInventory
// (event_catalog_test.go) composes this into its combined registry, so
// AdapterBuildRegistered's own emission site in commands.go always has a
// registered decoder.
func RegisterEventSchemas(registry *eventschema.Registry) {
	registry.Register(AdapterBuildRegisteredEventType, AdapterBuildRegisteredSchemaVersion, DecodeAdapterBuildRegisteredV1)
}
